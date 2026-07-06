package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
	"github.com/CoOre/keenetic-sing-box-ui/internal/singbox"
	"github.com/CoOre/keenetic-sing-box-ui/internal/system"
	"github.com/CoOre/keenetic-sing-box-ui/internal/transparent"
)

// Component describes the update state of one updatable piece of software.
type Component struct {
	Current   string `json:"current,omitempty"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
}

// Status is the cached result of the last version check.
type Status struct {
	SingBox   Component `json:"sing_box"`
	UI        Component `json:"ui"`
	CheckedAt time.Time `json:"checked_at"`
	Checking  bool      `json:"checking"`
	Updating  string    `json:"updating,omitempty"` // "singbox"|"ui" while an install runs
	// LastResult/LastError describe how the most recent install attempt ended
	// (installs run detached from the HTTP request, so this is how the UI
	// learns the outcome).
	LastResult string `json:"last_result,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

// Manager periodically checks GitHub for new sing-box and UI releases,
// caches the result, and can install either — on demand or automatically
// when the corresponding settings toggle is on.
type Manager struct {
	SingBoxGH  *singbox.Github  // resolver/installer for SagerNet/sing-box
	UIGH       *singbox.Github  // resolver/installer for this UI's releases
	Service    *singbox.Service // sing-box init script driver
	Settings   *settings.Store
	Runner     cmdrun.Runner
	SingBoxBin string // path to installed sing-box (current-version probe)
	UIVersion  string // this binary's version (from ldflags)
	UIInit     string // path to the UI's own init script (self-restart)
	UIInitrc   string // fallback restart script (/opt/etc/initrc, install-router.sh setups)
	BaseURL    string // GitHub API base override for tests; "" = api.github.com
	Log        *slog.Logger

	mu       sync.Mutex
	status   Status
	updating bool
}

// Status returns the cached result of the last check without touching the
// network.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Check queries GitHub for the latest releases of both components and
// refreshes the cached status. Errors are per-component and non-fatal.
func (m *Manager) Check(ctx context.Context) Status {
	m.mu.Lock()
	m.status.Checking = true
	m.mu.Unlock()

	st := Status{CheckedAt: time.Now()}
	st.SingBox = m.checkSingBox(ctx)
	st.UI = m.checkUI(ctx)

	m.mu.Lock()
	// A check refreshes the version fields only; in-progress/outcome state of
	// installs is owned by beginUpdate/finishUpdate.
	st.Updating = m.status.Updating
	st.LastResult = m.status.LastResult
	st.LastError = m.status.LastError
	m.status = st
	m.mu.Unlock()
	return st
}

func (m *Manager) checkSingBox(ctx context.Context) Component {
	var c Component
	if m.SingBoxBin == "" || !fileExists(m.SingBoxBin) {
		return c // not installed — nothing to update
	}
	if v, err := system.SingBoxVersion(ctx, m.Runner, m.SingBoxBin); err == nil {
		c.Current = v
	}
	asset, err := m.SingBoxGH.ResolveLatest(ctx, m.BaseURL, runtime.GOARCH)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	c.Latest = asset.Version
	c.Available = c.Current != "" && CompareVersions(c.Current, c.Latest) < 0
	return c
}

func (m *Manager) checkUI(ctx context.Context) Component {
	c := Component{Current: strings.TrimPrefix(m.UIVersion, "v")}
	asset, err := m.UIGH.ResolveLatest(ctx, m.BaseURL, runtime.GOARCH)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	c.Latest = strings.TrimPrefix(asset.Version, "v")
	c.Available = CompareVersions(c.Current, c.Latest) < 0
	return c
}

// UpdateSingBox installs the latest sing-box release and restarts the
// service if it was running. Returns the installed version.
func (m *Manager) UpdateSingBox(ctx context.Context) (string, error) {
	if err := m.beginUpdate("singbox"); err != nil {
		return "", err
	}
	v, err := m.updateSingBox(ctx)
	m.finishUpdate("sing-box "+v, err)
	return v, err
}

func (m *Manager) updateSingBox(ctx context.Context) (string, error) {
	asset, err := m.SingBoxGH.ResolveLatest(ctx, m.BaseURL, runtime.GOARCH)
	if err != nil {
		return "", fmt.Errorf("resolve: %w", err)
	}
	wasRunning := m.singBoxRunning(ctx)
	if err := m.install(ctx, m.SingBoxGH, asset); err != nil {
		return asset.Version, fmt.Errorf("install: %w", err)
	}
	if wasRunning {
		if _, err := m.Service.Do(ctx, singbox.ActionRestart); err != nil {
			return asset.Version, fmt.Errorf("installed %s but restart failed: %w", asset.Version, err)
		}
	}
	m.refreshAfterInstall(ctx)
	return asset.Version, nil
}

// UpdateSingBoxDetached runs UpdateSingBox in the background with its own
// context, detached from the HTTP request that triggered it — a dropped
// client connection must not abort a half-done download. Progress/outcome
// are reported via Status.
func (m *Manager) UpdateSingBoxDetached() error {
	return m.detach("singbox", func(ctx context.Context) error {
		_, err := m.UpdateSingBox(ctx)
		return err
	})
}

// UpdateUI installs the latest UI release over this binary and schedules a
// detached self-restart via the init script. Returns the installed version;
// the process restarts ~1 s after return.
func (m *Manager) UpdateUI(ctx context.Context) (string, error) {
	if err := m.beginUpdate("ui"); err != nil {
		return "", err
	}
	v, err := m.updateUI(ctx)
	m.finishUpdate("ui "+v, err)
	return v, err
}

func (m *Manager) updateUI(ctx context.Context) (string, error) {
	asset, err := m.UIGH.ResolveLatest(ctx, m.BaseURL, runtime.GOARCH)
	if err != nil {
		return "", fmt.Errorf("resolve: %w", err)
	}
	if err := m.install(ctx, m.UIGH, asset); err != nil {
		return asset.Version, fmt.Errorf("install: %w", err)
	}
	if err := m.ScheduleSelfRestart(); err != nil {
		return asset.Version, fmt.Errorf("installed %s but restart failed: %w", asset.Version, err)
	}
	return asset.Version, nil
}

// UpdateUIDetached is the fire-and-forget form of UpdateUI (see
// UpdateSingBoxDetached).
func (m *Manager) UpdateUIDetached() error {
	return m.detach("ui", func(ctx context.Context) error {
		_, err := m.UpdateUI(ctx)
		return err
	})
}

// detach spawns fn in the background unless an update is already running.
// The pre-check is advisory (beginUpdate inside fn still arbitrates); it
// exists so the HTTP handler can reject an obviously-duplicate request.
func (m *Manager) detach(what string, fn func(ctx context.Context) error) error {
	m.mu.Lock()
	busy := m.updating
	m.mu.Unlock()
	if busy {
		return errors.New("another update is already in progress")
	}
	go func() {
		// Generous ceiling: a 60 MB archive through a throttled tunnel can
		// legitimately take a while.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := fn(ctx); err != nil {
			m.Log.Warn("update failed", "target", what, "err", err)
		}
	}()
	return nil
}

// finishUpdate clears the in-progress flag and records the outcome for the
// status endpoint.
func (m *Manager) finishUpdate(what string, err error) {
	m.mu.Lock()
	m.updating = false
	m.status.Updating = ""
	if err != nil {
		m.status.LastResult = ""
		m.status.LastError = what + ": " + err.Error()
	} else {
		m.status.LastResult = what + " installed"
		m.status.LastError = ""
	}
	m.mu.Unlock()
	if err == nil {
		m.Log.Info("update installed", "what", what)
	}
}

// install downloads and installs the asset, falling back to the local
// sing-box proxy inbound when the direct download fails. GitHub's release
// CDN (release-assets.githubusercontent.com) is commonly interfered with,
// and the router's own egress is not captured by the transparent firewall
// rules — so the retry explicitly tunnels through sing-box.
func (m *Manager) install(ctx context.Context, gh *singbox.Github, asset singbox.Asset) error {
	err := gh.Install(ctx, asset)
	if err == nil {
		return nil
	}
	proxied := m.proxiedClient()
	if proxied == nil {
		return err
	}
	m.Log.Info("direct download failed, retrying via local sing-box proxy", "err", err)
	ghp := *gh
	ghp.HTTP = proxied
	if perr := ghp.Install(ctx, asset); perr != nil {
		return fmt.Errorf("direct: %v; via proxy: %w", err, perr)
	}
	return nil
}

// proxiedClient returns an HTTP client tunnelling through the local sing-box
// proxy inbound, or nil if none is listening: the mixed inbound itself in
// socks mode, the companion loopback inbound in the transparent modes.
func (m *Manager) proxiedClient() *http.Client {
	s, err := m.Settings.Get()
	if err != nil {
		return nil
	}
	var port int
	switch s.InboundMode {
	case "socks":
		port = s.InboundPort
	case "tproxy", "redirect":
		port = config.LoopbackProxyPort(s.InboundPort)
	default:
		return nil
	}
	if !transparent.ProxyListening(port) {
		return nil
	}
	proxyURL, perr := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if perr != nil {
		return nil
	}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
}

// ScheduleSelfRestart spawns a detached shell that restarts this service via
// its init script after a short delay, so the HTTP response for the update
// request is delivered before the process dies. Also used after a full-state
// import, which replaces the UI config read at process start.
//
// Two install layouts exist: the packaged one has an init.d script (UIInit);
// install-router.sh setups instead (re)start the UI from /opt/etc/initrc,
// which kills any previous instance before launching — so re-running it acts
// as a restart.
func (m *Manager) ScheduleSelfRestart() error {
	script := ""
	switch {
	case m.UIInit != "" && fileExists(m.UIInit):
		script = m.UIInit + " restart"
	case m.UIInitrc != "" && fileExists(m.UIInitrc):
		script = m.UIInitrc
	default:
		return errors.New("no restart script found (tried " + m.UIInit + ", " + m.UIInitrc + ")")
	}
	cmd := exec.Command("/bin/sh", "-c", "sleep 1; sh "+script+" >/dev/null 2>&1")
	// New session: survives this process's death and isn't killed by the init
	// script's own procname-based kill.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// Run is the background loop: waits for the network to settle, then checks
// on the settings-defined interval and auto-installs when enabled.
func (m *Manager) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(90 * time.Second):
	}
	for {
		s, err := m.Settings.Get()
		if err != nil {
			s = settings.Defaults()
		}
		st := m.Check(ctx)
		if st.SingBox.Available && s.AutoUpdateSingBox {
			m.Log.Info("auto-update: installing sing-box", "from", st.SingBox.Current, "to", st.SingBox.Latest)
			if v, err := m.UpdateSingBox(ctx); err != nil {
				m.Log.Warn("auto-update: sing-box failed", "err", err)
			} else {
				m.Log.Info("auto-update: sing-box updated", "version", v)
			}
		}
		if st.UI.Available && s.AutoUpdateUI {
			m.Log.Info("auto-update: installing ui", "from", st.UI.Current, "to", st.UI.Latest)
			if v, err := m.UpdateUI(ctx); err != nil {
				m.Log.Warn("auto-update: ui failed", "err", err)
			} else {
				// The detached restart kills this process shortly.
				m.Log.Info("auto-update: ui updated, restarting", "version", v)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(s.UpdateCheckHours) * time.Hour):
		}
	}
}

func (m *Manager) beginUpdate(what string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updating {
		return errors.New("another update is already in progress")
	}
	m.updating = true
	m.status.Updating = what
	m.status.LastResult = ""
	m.status.LastError = ""
	return nil
}

// refreshAfterInstall re-reads the installed sing-box version into the cache
// so the UI reflects the update without waiting for the next check.
func (m *Manager) refreshAfterInstall(ctx context.Context) {
	v, err := system.SingBoxVersion(ctx, m.Runner, m.SingBoxBin)
	if err != nil {
		return
	}
	m.mu.Lock()
	m.status.SingBox.Current = v
	m.status.SingBox.Available = m.status.SingBox.Latest != "" &&
		CompareVersions(v, m.status.SingBox.Latest) < 0
	m.mu.Unlock()
}

// singBoxRunning probes liveness via the init script's status action,
// mirroring system.Detector.serviceRunning.
func (m *Manager) singBoxRunning(ctx context.Context) bool {
	res, err := m.Service.Do(ctx, singbox.ActionStatus)
	if err != nil {
		return false
	}
	out := strings.ToLower(res.Stdout + res.Stderr)
	if strings.Contains(out, "not running") {
		return false
	}
	return strings.Contains(out, "alive") || strings.Contains(out, "running")
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
