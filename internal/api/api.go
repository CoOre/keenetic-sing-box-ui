package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/auth"
	"github.com/CoOre/keenetic-sing-box-ui/internal/clash"
	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
	"github.com/CoOre/keenetic-sing-box-ui/internal/lists"
	"github.com/CoOre/keenetic-sing-box-ui/internal/resolve"
	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
	"github.com/CoOre/keenetic-sing-box-ui/internal/share"
	"github.com/CoOre/keenetic-sing-box-ui/internal/singbox"
	"github.com/CoOre/keenetic-sing-box-ui/internal/system"
	"github.com/CoOre/keenetic-sing-box-ui/internal/transparent"
)

type Deps struct {
	Paths       system.Paths
	Detector    *system.Detector
	Service     *singbox.Service
	Opkg        *singbox.Opkg
	Github      *singbox.Github
	Config      *config.Store
	Checker     *config.Checker
	LogPath     string
	ClashAddr   string // sing-box clash_api external_controller, e.g. 127.0.0.1:9090
	ClashSecret string // injected as Bearer toward the Clash API
	Servers     *servers.Store
	Settings    *settings.Store
	Firewall    *transparent.Engine
	Lists       *lists.Store
	ListRunner  *lists.Runner
	Resolver    *resolve.Resolver
}

// Register mounts all /api/* routes on mux behind RequireAuth + RequireCSRF.
// Login/logout are mounted separately by the caller.
func Register(mux *http.ServeMux, a *auth.Authenticator, d *Deps) {
	h := &handlers{d: d}

	protect := func(handler http.Handler) http.Handler {
		return a.RequireAuth(a.RequireCSRF(handler))
	}

	mux.Handle("GET /api/system", protect(http.HandlerFunc(h.system)))
	mux.Handle("GET /api/install/status", protect(http.HandlerFunc(h.installStatus)))
	mux.Handle("POST /api/install", protect(http.HandlerFunc(h.install)))
	mux.Handle("POST /api/service/{action}", protect(http.HandlerFunc(h.serviceAction)))
	mux.Handle("GET /api/config", protect(http.HandlerFunc(h.configRead)))
	mux.Handle("PUT /api/config", protect(http.HandlerFunc(h.configWrite)))
	mux.Handle("POST /api/config/check", protect(http.HandlerFunc(h.configCheck)))
	mux.Handle("GET /api/config/backups", protect(http.HandlerFunc(h.configBackups)))
	mux.Handle("GET /api/config/backups/{name}", protect(http.HandlerFunc(h.configBackupRead)))
	mux.Handle("GET /api/logs", protect(http.HandlerFunc(h.logs)))
	mux.Handle("GET /api/diag/net", protect(http.HandlerFunc(h.diagNet)))
	mux.Handle("POST /api/diag/exec", protect(http.HandlerFunc(h.diagExec)))
	mux.Handle("POST /api/diag/mtu", protect(http.HandlerFunc(h.diagMTU)))
	mux.Handle("POST /api/diag/mtu/clamp", protect(http.HandlerFunc(h.diagMTUClamp)))
	mux.Handle("DELETE /api/diag/mtu/clamp", protect(http.HandlerFunc(h.diagMTUClampClear)))

	if d.Servers != nil {
		mux.Handle("POST /api/servers/parse", protect(http.HandlerFunc(h.serversParse)))
		mux.Handle("GET /api/servers", protect(http.HandlerFunc(h.serversList)))
		mux.Handle("POST /api/servers", protect(http.HandlerFunc(h.serversSave)))
		mux.Handle("DELETE /api/servers/{id}", protect(http.HandlerFunc(h.serversDelete)))
		mux.Handle("POST /api/servers/apply", protect(http.HandlerFunc(h.serversApply)))
	}

	if d.Settings != nil {
		mux.Handle("GET /api/settings", protect(http.HandlerFunc(h.settingsGet)))
		mux.Handle("PUT /api/settings", protect(http.HandlerFunc(h.settingsSave)))
	}

	mux.Handle("GET /api/transparent/policies", protect(http.HandlerFunc(h.transparentPolicies)))

	if d.Lists != nil {
		mux.Handle("GET /api/lists", protect(http.HandlerFunc(h.listsList)))
		mux.Handle("POST /api/lists", protect(http.HandlerFunc(h.listsAdd)))
		mux.Handle("DELETE /api/lists/{id}", protect(http.HandlerFunc(h.listsDelete)))
		mux.Handle("POST /api/lists/refresh", protect(http.HandlerFunc(h.listsRefreshAll)))
		mux.Handle("POST /api/lists/{id}/refresh", protect(http.HandlerFunc(h.listsRefreshOne)))
	}

	if d.ClashAddr != "" {
		if cp, err := clash.New(d.ClashAddr, d.ClashSecret, "/api/clash"); err == nil {
			// All methods; the Clash API uses GET/PUT/DELETE. CSRF still
			// applies to mutating methods via RequireCSRF.
			mux.Handle("/api/clash/", protect(cp))
		}
	}
}

type handlers struct{ d *Deps }

func (h *handlers) system(w http.ResponseWriter, r *http.Request) {
	info, err := h.d.Detector.Detect(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

type installStatusResp struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
	Entware   bool   `json:"entware"`
}

func (h *handlers) installStatus(w http.ResponseWriter, r *http.Request) {
	info, err := h.d.Detector.Detect(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	resp := installStatusResp{Entware: info.Entware != nil}
	if info.SingBox != nil {
		resp.Installed = true
		resp.Path = info.SingBox.Path
		resp.Version = info.SingBox.Version
	}
	writeJSON(w, http.StatusOK, resp)
}

type installReq struct {
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	Arch    string `json:"arch,omitempty"`
}

type installResp struct {
	Source           string                   `json:"source"`
	Version          string                   `json:"version,omitempty"`
	Opkg             *singbox.OpkgResult      `json:"opkg,omitempty"`
	Bootstrap        *singbox.BootstrapResult `json:"bootstrap,omitempty"`
	ConfigRegnerated bool                     `json:"config_regenerated,omitempty"`
	ConfigBackup     string                   `json:"config_backup,omitempty"`
}

func (h *handlers) install(w http.ResponseWriter, r *http.Request) {
	var req installReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	switch req.Source {
	case "opkg":
		if h.d.Opkg == nil {
			writeErr(w, http.StatusConflict, errors.New("opkg not configured"))
			return
		}
		res, err := h.d.Opkg.Install(r.Context(), "sing-box")
		out := installResp{Source: "opkg", Opkg: &res}
		if err != nil {
			writeJSON(w, http.StatusBadGateway, errorEnvelope{Error: err.Error(), Data: out})
			return
		}
		bs, err := singbox.Bootstrap(h.bootstrapPaths())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorEnvelope{Error: "bootstrap: " + err.Error(), Data: out})
			return
		}
		out.Bootstrap = &bs
		h.repairInvalidConfig(r, &out)
		writeJSON(w, http.StatusOK, out)
	case "github":
		arch := req.Arch
		if arch == "" {
			arch = runtime.GOARCH
		}
		asset, err := h.d.Github.ResolveLatest(r.Context(), "", arch)
		if err != nil {
			writeErr(w, http.StatusBadGateway, fmt.Errorf("resolve: %w", err))
			return
		}
		if err := h.d.Github.Install(r.Context(), asset); err != nil {
			writeErr(w, http.StatusBadGateway, fmt.Errorf("download: %w", err))
			return
		}
		bs, err := singbox.Bootstrap(h.bootstrapPaths())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Errorf("bootstrap: %w", err))
			return
		}
		out := installResp{Source: "github", Version: asset.Version, Bootstrap: &bs}
		h.repairInvalidConfig(r, &out)
		writeJSON(w, http.StatusOK, out)
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown source %q (want opkg|github)", req.Source))
	}
}

// repairInvalidConfig runs `sing-box check` against the current config and,
// if it fails (e.g. a config generated by an older UI version that predates a
// sing-box schema change), backs it up and writes a fresh default. The
// existing Clash secret is preserved so the reverse proxy keeps working. A
// config that passes check — including a user's working config — is left
// untouched.
func (h *handlers) repairInvalidConfig(r *http.Request, out *installResp) {
	if h.d.Checker == nil || h.d.Config == nil {
		return
	}
	cr, err := h.d.Checker.Check(r.Context(), h.d.Paths.SingBoxConfig)
	if err != nil || cr.OK {
		return // can't check, or config is already valid → leave it alone
	}

	_, secret := resolveClashFromFile(h.d.Paths.SingBoxConfig, h.d.ClashSecret)
	body, err := config.DefaultConfig(config.DefaultOptions{
		LogPath:     h.d.Paths.SingBoxLog,
		CachePath:   h.d.Paths.SingBoxCache,
		ClashAddr:   h.d.ClashAddr,
		ClashSecret: secret,
	})
	if err != nil {
		return
	}
	bk, err := h.d.Config.Write(body)
	if err != nil {
		return
	}
	out.ConfigRegnerated = true
	out.ConfigBackup = bk.Path
}

// resolveClashFromFile extracts the Clash external_controller and secret from
// an existing sing-box config, falling back to defaults. Used to preserve the
// secret when regenerating a broken config.
func resolveClashFromFile(path, fallbackSecret string) (addr, secret string) {
	addr = "127.0.0.1:9090"
	secret = fallbackSecret
	body, err := os.ReadFile(path)
	if err != nil {
		return addr, secret
	}
	var cfg struct {
		Experimental struct {
			ClashAPI struct {
				ExternalController string `json:"external_controller"`
				Secret             string `json:"secret"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	if json.Unmarshal(body, &cfg) != nil {
		return addr, secret
	}
	if c := cfg.Experimental.ClashAPI.ExternalController; c != "" {
		addr = c
	}
	if s := cfg.Experimental.ClashAPI.Secret; s != "" {
		secret = s
	}
	return addr, secret
}

func (h *handlers) bootstrapPaths() singbox.BootstrapPaths {
	return singbox.BootstrapPaths{
		ConfigPath: h.d.Paths.SingBoxConfig,
		InitPath:   h.d.Paths.SingBoxInit,
		LogPath:    h.d.Paths.SingBoxLog,
		CacheDir:   filepath.Dir(h.d.Paths.SingBoxCache),
	}
}

type serviceResp struct {
	Action  string               `json:"action"`
	Result  singbox.ActionResult `json:"result"`
	Enabled bool                 `json:"enabled"`
}

func (h *handlers) serviceAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case "enable":
		if err := h.d.Service.Enable(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"action": "enable", "enabled": true})
		return
	case "disable":
		if err := h.d.Service.Disable(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		// Stop capturing traffic once the service is no longer managed.
		h.syncFirewall(r.Context(), "stop")
		writeJSON(w, http.StatusOK, map[string]any{"action": "disable", "enabled": false})
		return
	}
	a := singbox.Action(action)
	if !a.Valid() {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid action %q", action))
		return
	}
	res, err := h.d.Service.Do(r.Context(), a)
	if err != nil {
		// The init script only surfaces an exit code; the real reason a
		// start/restart failed is almost always a bad config. Run
		// `sing-box check` and the log tail so the UI can show what and why.
		diag := h.startDiagnostics(r, a)
		writeJSON(w, http.StatusBadGateway, serviceErrorEnvelope{
			Error:  err.Error(),
			Result: res,
			Check:  diag.check,
			Log:    diag.log,
		})
		return
	}
	h.syncFirewall(r.Context(), action)
	enabled, _ := h.d.Service.IsEnabled()
	writeJSON(w, http.StatusOK, serviceResp{Action: action, Result: res, Enabled: enabled})
}

// syncFirewall keeps the transparent-proxy firewall in step with the service
// lifecycle: install rules when the service comes up in a transparent mode,
// tear them down when it stops — so traffic is never captured toward a dead
// listener. No-op when no firewall engine or settings are configured.
func (h *handlers) syncFirewall(ctx context.Context, action string) {
	if h.d.Firewall == nil || h.d.Settings == nil {
		return
	}
	switch action {
	case "start", "restart":
		s, err := h.d.Settings.Get()
		if err != nil {
			return
		}
		var lCIDRs []string
		if h.d.Lists != nil {
			_, lCIDRs, _ = h.d.Lists.MergedEntries()
		}
		cfg := transparentConfigFromSettings(s, lCIDRs)
		if cfg.Mode == transparent.ModeOff {
			_ = h.d.Firewall.Clean(ctx)
			return
		}
		_ = h.d.Firewall.Up(ctx, cfg)
		// Populate the route ipset with resolved domain IPs. Async + detached
		// context: DNS lookups must not block (or be cancelled with) the HTTP
		// response, and the resolver's own tick is the steady-state path.
		if h.d.Resolver != nil {
			go h.d.Resolver.Refresh(context.WithoutCancel(ctx))
		}
	case "stop":
		_ = h.d.Firewall.Clean(ctx)
	}
}

type serviceErrorEnvelope struct {
	Error  string               `json:"error"`
	Result singbox.ActionResult `json:"result"`
	Check  *config.CheckResult  `json:"check,omitempty"`
	Log    []string             `json:"log,omitempty"`
}

type startDiag struct {
	check *config.CheckResult
	log   []string
}

// startDiagnostics gathers the likely cause of a failed start/restart:
// the config validation result and the tail of the sing-box log.
func (h *handlers) startDiagnostics(r *http.Request, a singbox.Action) startDiag {
	var d startDiag
	if a != singbox.ActionStart && a != singbox.ActionRestart {
		return d
	}
	if h.d.Checker != nil {
		if cr, err := h.d.Checker.Check(r.Context(), h.d.Paths.SingBoxConfig); err == nil {
			d.check = &cr
		}
	}
	if lines, err := tailLines(h.d.LogPath, 40); err == nil && len(lines) > 0 {
		d.log = lines
	}
	return d
}

func (h *handlers) configRead(w http.ResponseWriter, r *http.Request) {
	body, err := h.d.Config.Read()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (h *handlers) configWrite(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20)) // 4 MiB
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	bk, err := h.d.Config.Write(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backup": bk})
}

func (h *handlers) configCheck(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	var res config.CheckResult
	var err error
	if len(body) > 0 {
		res, err = h.d.Checker.CheckContent(r.Context(), body)
	} else {
		res, err = h.d.Checker.Check(r.Context(), h.d.Paths.SingBoxConfig)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handlers) configBackups(w http.ResponseWriter, r *http.Request) {
	metas, err := h.d.Config.ListBackupMeta()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if metas == nil {
		metas = []config.BackupMeta{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": metas})
}

func (h *handlers) configBackupRead(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := h.d.Config.ReadBackup(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (h *handlers) diagNet(w http.ResponseWriter, r *http.Request) {
	report := system.NetDiagnostics(r.Context(), cmdrun.OS{})
	writeJSON(w, http.StatusOK, report)
}

type diagExecReq struct {
	Action string   `json:"action"`
	Args   []string `json:"args"`
}

// diagExec runs one whitelisted action (opkg/iptables/ipset/ip/sh) for the
// transparent-proxy PoC. Whitelisted in system.RunAction; not arbitrary.
func (h *handlers) diagExec(w http.ResponseWriter, r *http.Request) {
	var req diagExecReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	step, ok := system.RunAction(r.Context(), cmdrun.OS{}, req.Action, req.Args)
	if !ok {
		writeErr(w, http.StatusForbidden, errors.New(step.Err))
		return
	}
	writeJSON(w, http.StatusOK, step)
}

// serverIPv4 returns the IPv4 address of the configured proxy server (the tunnel
// endpoint), resolving a hostname if necessary. Used to probe/clamp the path MTU
// to the server.
func (h *handlers) serverIPv4() (string, error) {
	if h.d.Servers == nil {
		return "", errors.New("no servers configured")
	}
	list, err := h.d.Servers.List()
	if err != nil {
		return "", err
	}
	var host string
	for _, e := range list {
		if e.Server.Server != "" {
			host = e.Server.Server
			break
		}
	}
	if host == "" {
		return "", errors.New("no server configured")
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
		return "", fmt.Errorf("server %q is not IPv4", host)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", host, err)
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 for %q", host)
}

// diagMTU probes the path MTU to the proxy server (ICMP + DF binary search) and
// returns it plus the recommended TCP MSS.
func (h *handlers) diagMTU(w http.ResponseWriter, r *http.Request) {
	ip, err := h.serverIPv4()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	res, err := system.ProbeMTU(ctx, ip)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type mtuClampReq struct {
	MSS int `json:"mss"`
}

// diagMTUClamp installs a TCP MSS clamp toward the proxy server (non-persistent).
func (h *handlers) diagMTUClamp(w http.ResponseWriter, r *http.Request) {
	var req mtuClampReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.MSS < 500 || req.MSS > 1460 {
		writeErr(w, http.StatusBadRequest, errors.New("mss out of range (500–1460)"))
		return
	}
	ip, err := h.serverIPv4()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := system.SetMSSClamp(r.Context(), cmdrun.OS{}, ip, req.MSS); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ip": ip, "mss": req.MSS, "applied": true})
}

// diagMTUClampClear removes the MSS clamp.
func (h *handlers) diagMTUClampClear(w http.ResponseWriter, r *http.Request) {
	system.ClearMSSClamp(r.Context(), cmdrun.OS{})
	writeJSON(w, http.StatusOK, map[string]any{"cleared": true})
}

type logsResp struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
}

func (h *handlers) logs(w http.ResponseWriter, r *http.Request) {
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 10000 {
			tail = n
		}
	}
	lines, err := tailLines(h.d.LogPath, tail)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, http.StatusOK, logsResp{Path: h.d.LogPath, Lines: []string{}})
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if lines == nil {
		lines = []string{}
	}
	writeJSON(w, http.StatusOK, logsResp{Path: h.d.LogPath, Lines: lines})
}

// --- settings (inbound mode etc.) ---

func (h *handlers) settingsGet(w http.ResponseWriter, r *http.Request) {
	s, err := h.d.Settings.Get()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *handlers) settingsSave(w http.ResponseWriter, r *http.Request) {
	var in settings.Settings
	// RouteCIDR/RouteDomains can legitimately hold a few hundred to a few
	// thousand manually-curated entries (a 240-CIDR list alone is ~4 KiB), so
	// the old 4 KiB cap rejected real saves with "request body too large".
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	saved, err := h.d.Settings.Save(in)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// transparentPolicies lists the router's Keenetic routing policies (via RCI) so
// the UI can offer one to bind transparent proxying to. Empty if RCI is
// unreachable (e.g. running off-router in dev).
func (h *handlers) transparentPolicies(w http.ResponseWriter, r *http.Request) {
	pols := transparent.ListPolicies(r.Context())
	if pols == nil {
		pols = []transparent.Policy{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"policies": pols})
}

// --- servers (form-based outbound management) ---

type parseReq struct {
	Link string `json:"link"`
}

func (h *handlers) serversParse(w http.ResponseWriter, r *http.Request) {
	var req parseReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s, err := share.ParseLink(req.Link)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (h *handlers) serversList(w http.ResponseWriter, r *http.Request) {
	list, err := h.d.Servers.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if list == nil {
		list = []servers.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": list})
}

func (h *handlers) serversSave(w http.ResponseWriter, r *http.Request) {
	var e servers.Entry
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&e); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if e.Server.Server == "" || e.Server.Type == "" {
		writeErr(w, http.StatusBadRequest, errors.New("server and type are required"))
		return
	}
	saved, err := h.d.Servers.Save(e)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *handlers) serversDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.d.Servers.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type serversApplyReq struct {
	Restart bool `json:"restart"`
}

type serversApplyResp struct {
	Check         config.CheckResult    `json:"check"`
	Applied       bool                  `json:"applied"`
	Backup        string                `json:"backup,omitempty"`
	Restart       *singbox.ActionResult `json:"restart,omitempty"`
	Servers       int                   `json:"servers"`
	FirewallMode  string                `json:"firewall_mode,omitempty"`
	FirewallError string                `json:"firewall_error,omitempty"`
}

// transparentConfigFromSettings maps persisted UI settings to a firewall
// Config. socks/tun modes map to ModeOff (the firewall is torn down).
// listCIDRs are CIDRs fetched from URL sources (matched at iptables level).
func transparentConfigFromSettings(s settings.Settings, listCIDRs []string) transparent.Config {
	mode := transparent.ModeOff
	switch s.InboundMode {
	case "tproxy":
		mode = transparent.ModeTProxy
	case "redirect":
		mode = transparent.ModeRedirect
	}
	return transparent.Config{
		Mode:         mode,
		TProxyPort:   s.InboundPort,
		RedirectPort: s.InboundPort,
		PolicyName:   s.PolicyName,
		ExtraExclude: s.ExcludeCIDR,
		UseConntrack: s.UseConntrack,
		// Static seed for the route ipset (redirect mode): manual RouteCIDR +
		// CIDRs from URL lists. The resolver folds in resolved domain IPs.
		RouteCIDR:  append(append([]string{}, s.RouteCIDR...), listCIDRs...),
		RejectCIDR: s.RejectCIDR,
	}
}

// serversApply assembles a sing-box config from the stored servers, validates
// it with `sing-box check`, and only writes it if valid. Optionally restarts.
func (h *handlers) serversApply(w http.ResponseWriter, r *http.Request) {
	var req serversApplyReq
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)

	list, err := h.d.Servers.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	tags := servers.UniqueTags(list)
	outs := make([]config.ProxyOutbound, 0, len(list))
	for i, e := range list {
		obj := e.Server.ToOutbound(tags[i])
		if obj == nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("server %q: unsupported type %q", e.Name, e.Type))
			return
		}
		outs = append(outs, config.ProxyOutbound{Tag: tags[i], Object: obj})
	}

	st := settings.Defaults()
	if h.d.Settings != nil {
		if s, serr := h.d.Settings.Get(); serr == nil {
			st = s
		}
	}
	// Cached URL-list entries: domains drive sing-box domain_suffix rules (the
	// stable selector for CDN-fronted services), CIDRs drive ip_cidr rules.
	var listDomains, listCIDRs []string
	if h.d.Lists != nil {
		listDomains, listCIDRs, _ = h.d.Lists.MergedEntries()
	}
	body, err := config.Assemble(config.AssembleOptions{
		DefaultOptions: config.DefaultOptions{
			LogPath:     h.d.Paths.SingBoxLog,
			CachePath:   h.d.Paths.SingBoxCache,
			ClashAddr:   h.d.ClashAddr,
			ClashSecret: h.d.ClashSecret,
		},
		InboundMode:       st.InboundMode,
		InboundPort:       st.InboundPort,
		Tun:               config.TunOptions{Stack: st.TunStack, MTU: st.TunMTU},
		RouteDomains:      st.RouteDomains,
		RouteCIDR:         st.RouteCIDR,
		ExtraRouteDomains: listDomains, // from URL lists, domain_suffix trie
		ExtraRouteCIDR:    listCIDRs,   // from URL lists, Patricia trie (~2 MB per 1000)
		Multiplex:         st.Multiplex,
	}, outs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	resp := serversApplyResp{Servers: len(list)}
	// Validate before writing.
	if h.d.Checker != nil {
		cr, cerr := h.d.Checker.CheckContent(r.Context(), body)
		if cerr == nil {
			resp.Check = cr
			if !cr.OK {
				writeJSON(w, http.StatusBadRequest, resp)
				return
			}
		}
	}

	bk, err := h.d.Config.Write(body)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	resp.Applied = true
	resp.Backup = bk.Path

	if req.Restart && h.d.Service != nil {
		ar, rerr := h.d.Service.Do(r.Context(), singbox.ActionRestart)
		resp.Restart = &ar
		if rerr != nil {
			// Surface but don't fail the whole apply — config is already written.
			resp.Check.Stderr = rerr.Error()
		}
	}

	// Install or tear down the transparent-proxy firewall to match the mode.
	// Done after the restart so the TPROXY/redirect listener is up before rules
	// start handing it traffic. Failures are surfaced but don't void the apply
	// (the config and service are already in place).
	if h.d.Firewall != nil {
		fcfg := transparentConfigFromSettings(st, listCIDRs)
		var ferr error
		if fcfg.Mode == transparent.ModeOff {
			ferr = h.d.Firewall.Clean(r.Context())
		} else {
			ferr = h.d.Firewall.Up(r.Context(), fcfg)
		}
		if ferr != nil {
			resp.FirewallError = ferr.Error()
		} else {
			resp.FirewallMode = fcfg.Mode
			// Seed the route ipset with resolved domain IPs (async + detached:
			// DNS must not block/cancel with the HTTP response).
			if h.d.Resolver != nil {
				go h.d.Resolver.Refresh(context.WithoutCancel(r.Context()))
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- list sources ---

func (h *handlers) listsList(w http.ResponseWriter, r *http.Request) {
	srcs, err := h.d.Lists.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if srcs == nil {
		srcs = []*lists.Source{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": srcs})
}

type listsAddReq struct {
	URL      string `json:"url"`
	Type     string `json:"type"`
	Interval int    `json:"interval"`
}

func (h *handlers) listsAdd(w http.ResponseWriter, r *http.Request) {
	var req listsAddReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.URL == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("url is required"))
		return
	}
	src, err := h.d.Lists.Add(lists.Source{
		URL:      req.URL,
		Type:     req.Type,
		Interval: req.Interval,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Kick off an immediate fetch in the background.
	if h.d.ListRunner != nil {
		go h.d.ListRunner.FetchOne(src.ID)
	}
	writeJSON(w, http.StatusOK, src)
}

func (h *handlers) listsDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.d.Lists.Delete(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) listsRefreshAll(w http.ResponseWriter, r *http.Request) {
	if h.d.ListRunner != nil {
		go h.d.ListRunner.FetchAll()
	}
	writeJSON(w, http.StatusOK, map[string]any{"refreshing": true})
}

func (h *handlers) listsRefreshOne(w http.ResponseWriter, r *http.Request) {
	if h.d.ListRunner != nil {
		go h.d.ListRunner.FetchOne(r.PathValue("id"))
	}
	writeJSON(w, http.StatusOK, map[string]any{"refreshing": true})
}

// --- helpers ---

type errorEnvelope struct {
	Error string `json:"error"`
	Data  any    `json:"data,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, errorEnvelope{Error: err.Error()})
}

// tailLines reads up to n trailing lines from path. Reads the whole file
// (logs on the router are small; we can optimize later).
// tailMaxBytes caps how much of the log file's tail tailLines reads into memory.
// The sing-box log at info level grows to hundreds of MB; the old io.ReadAll of
// the whole file blew the router's GOMEMLIMIT (120 MB) → OOM that killed the UI
// process and made /api/logs return nothing. 256 KiB is ~1–2k recent lines.
const tailMaxBytes = 256 << 10

func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var start int64
	if sz := fi.Size(); sz > tailMaxBytes {
		start = sz - tailMaxBytes
	}
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil, err
		}
	}
	body, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	// Seeking lands mid-line; drop the partial first line so we return whole ones.
	if start > 0 {
		for i := 0; i < len(body); i++ {
			if body[i] == '\n' {
				body = body[i+1:]
				break
			}
		}
	}
	lines := splitLines(string(body))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
