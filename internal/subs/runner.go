package subs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/proxyretry"
	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
)

// DefaultUserAgent is sent when a subscription sets none. Panels (Marzban,
// Remnawave, 3x-ui) pick the response format by User-Agent; an unknown client
// gets the universal base64 link list, which is what ParseBody reads.
const DefaultUserAgent = "keenetic-sing-box-ui"

const fetchTimeout = 30 * time.Second

// Runner fetches subscriptions on schedule and syncs their servers.
type Runner struct {
	Store   *Store
	Servers *servers.Store
	Log     *slog.Logger
	// ProxyClient returns a client tunnelling through the local sing-box, or
	// nil when none is listening. Used when the direct fetch fails: provider
	// panels are often blocked by the ISP.
	ProxyClient func() *http.Client
	// OnChanged is called (from the background loop only) when a subscription
	// with AutoApply changed the server set; it rebuilds the sing-box config.
	OnChanged func(ctx context.Context)

	mu sync.Mutex // one refresh at a time: they all rewrite servers.json
}

// Result reports one refresh.
type Result struct {
	Sub     *Subscription `json:"subscription"`
	Changed bool          `json:"changed"`
	// Warnings say what this refresh broke: the primary server or chains
	// using a server that's gone (see servers.Store.SyncSub). Also logged.
	Warnings []string `json:"warnings,omitempty"`
	// StateError is set when the servers were synced but the subscription's
	// own state (last fetch, count, error) couldn't be saved.
	StateError string `json:"state_error,omitempty"`
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Start runs the refresh loop until ctx is done.
func (r *Runner) Start(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(20 * time.Second): // let sing-box come up for the proxy fallback
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		r.refreshDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *Runner) refreshDue(ctx context.Context) {
	list, err := r.Store.List()
	if err != nil {
		r.log().Warn("subs: load", "err", err)
		return
	}
	apply := false
	now := time.Now()
	for _, sub := range list {
		if !sub.Enabled || !sub.isDue(now) {
			continue
		}
		res, err := r.Refresh(ctx, sub.ID)
		if err != nil {
			continue // already recorded on the subscription and logged
		}
		if res.Changed && res.Sub.AutoApply {
			apply = true
		}
	}
	if apply && r.OnChanged != nil {
		r.OnChanged(ctx)
	}
}

// Refresh fetches one subscription now and syncs its servers. The fetch
// outcome (including errors) is persisted on the subscription; on error the
// previously synced servers are left untouched.
func (r *Runner) Refresh(ctx context.Context, id string) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub, err := r.Store.Get(id)
	if err != nil {
		return Result{}, err
	}
	f, via, ferr := r.fetch(ctx, sub)
	now := time.Now()
	sub.LastFetch = &now
	if ferr != nil {
		sub.LastError = ferr.Error()
		r.log().Warn("subs: fetch failed", "id", sub.ID, "err", ferr)
		_ = r.Store.Update(sub)
		return Result{Sub: sub}, ferr
	}

	changed, warnings, serr := r.Servers.SyncSub(sub.ID, f.Servers)
	if serr != nil {
		sub.LastError = "сохранение серверов: " + serr.Error()
		_ = r.Store.Update(sub)
		return Result{Sub: sub}, serr
	}
	// Logged here, whether or not anything gets applied: a background refresh
	// has no one to show them to. Each is reported once — by the refresh that
	// broke it.
	for _, w := range warnings {
		r.log().Warn("subs: refresh broke a server pick", "id", sub.ID, "warning", w)
	}
	sub.LastError = ""
	sub.LastCount = len(f.Servers)
	sub.Skipped = f.Skipped
	sub.Via = via
	if f.Info != nil {
		sub.Info = f.Info
	}
	if sub.Name == "" {
		sub.Name = f.Title
	}
	if sub.Name == "" {
		sub.Name = hostOf(sub.URL)
	}
	stateErr := ""
	if err := r.Store.Update(sub); err != nil {
		// The servers are synced already: failing the refresh would lose its
		// warnings for good (the next one finds nothing changed).
		r.log().Error("subs: saving subscription state failed", "id", sub.ID, "err", err)
		stateErr = err.Error()
	}
	r.log().Info("subs: refreshed", "id", sub.ID, "servers", len(f.Servers), "skipped", f.Skipped, "changed", changed, "via", via)
	return Result{Sub: sub, Changed: changed, Warnings: warnings, StateError: stateErr}, nil
}

// fetch tries a direct download first, then through the local proxy.
func (r *Runner) fetch(ctx context.Context, sub *Subscription) (*Fetched, string, error) {
	ua := strings.TrimSpace(sub.UserAgent)
	if ua == "" {
		ua = DefaultUserAgent
	}
	var f *Fetched
	via, err := proxyretry.Do(ctx, fetchTimeout, &http.Client{}, r.ProxyClient, nil,
		func(ctx context.Context, c *http.Client) error {
			var err error
			f, err = Fetch(ctx, c, sub.URL, ua)
			// A response we got but couldn't parse won't improve through the proxy.
			var perr *parseError
			if errors.As(err, &perr) {
				return proxyretry.Permanent(err)
			}
			return err
		})
	if err != nil {
		return nil, "", err
	}
	return f, via, nil
}

// ValidateURL checks that raw is an absolute http(s) URL.
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("нужна ссылка http:// или https://")
	}
	return nil
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Hostname()
	}
	return ""
}
