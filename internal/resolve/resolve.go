// Package resolve keeps the transparent-proxy route ipset current by resolving
// the user's proxied domains to their live IPs.
//
// Why this exists: in the selective transparent modes (REDIRECT and TPROXY) the
// proxy/direct decision is made at the iptables layer against the route ipset
// (see internal/transparent). Routing a
// domain therefore means having its CURRENT IPs in that set. CDN-fronted sites
// (chatgpt.com behind Cloudflare/OpenAI) rotate IPs constantly and don't get
// reliably sniffed by SNI, so a static IP snapshot lags and the connection
// leaks straight to direct. This resolver re-resolves the proxied domains on a
// short interval and folds their IPs into the set atomically — no sing-box
// restart, no SNI dependency.
package resolve

import (
	"context"
	"log/slog"
	"net"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/lists"
	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
	"github.com/CoOre/keenetic-sing-box-ui/internal/transparent"
)

const (
	defaultInterval = time.Minute
	// defaultGrace keeps a resolved IP in the set for a while after it last
	// appeared in DNS, so rotating CDN pools accumulate rather than flap (a
	// connection opened to a just-rotated-out IP still routes correctly).
	defaultGrace  = 30 * time.Minute
	lookupTimeout = 5 * time.Second
	startupDelay  = 5 * time.Second
)

// Resolver resolves Settings.RouteDomains to IPv4 addresses and maintains the
// transparent route ipset = resolved IPs ∪ static RouteCIDR ∪ URL-list CIDRs.
// Only the (small, manually-entered) RouteDomains are resolved; URL-list CIDRs
// are added verbatim and URL-list *domains* are intentionally not resolved
// (could be thousands — too heavy for the router's DNS and RAM).
type Resolver struct {
	Engine   *transparent.Engine
	Settings *settings.Store
	Lists    *lists.Store
	Log      *slog.Logger
	Interval time.Duration // default 1m
	Grace    time.Duration // default 30m

	refreshMu sync.Mutex // serializes Refresh (handler-triggered vs ticker)
	mu        sync.Mutex
	seen      map[string]time.Time // ip -> last seen in DNS
	lastSig   string               // signature of the last set we pushed
}

func (r *Resolver) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Resolver) interval() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return defaultInterval
}

func (r *Resolver) grace() time.Duration {
	if r.Grace > 0 {
		return r.Grace
	}
	return defaultGrace
}

// Start runs the resolve loop forever; call in a goroutine. It resolves shortly
// after startup, then on each interval tick.
func (r *Resolver) Start(ctx context.Context) {
	select {
	case <-time.After(startupDelay):
	case <-ctx.Done():
		return
	}
	r.Refresh(ctx)
	t := time.NewTicker(r.interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Refresh(ctx)
		}
	}
}

// Refresh resolves the proxied domains and rebuilds the route ipset. Safe to
// call on demand (e.g. right after the firewall is (re)applied). No-op unless
// the active inbound mode selects at the iptables layer (redirect or tproxy);
// both match dst against the route ipset this resolver maintains.
func (r *Resolver) Refresh(ctx context.Context) {
	if r.Engine == nil || r.Settings == nil {
		return
	}
	// Skip if a refresh is already in flight (handler-triggered + ticker can
	// overlap); concurrent ipset restores would race on the shared temp set.
	if !r.refreshMu.TryLock() {
		return
	}
	defer r.refreshMu.Unlock()
	s, err := r.Settings.Get()
	if err != nil {
		r.log().Warn("resolve: load settings", "err", err)
		return
	}
	if s.InboundMode != "redirect" && s.InboundMode != "tproxy" {
		return
	}
	defer debug.FreeOSMemory()

	now := time.Now()
	r.resolveInto(ctx, cleanDomains(s.RouteDomains), now)

	union := r.union(now, s.RouteCIDR, r.listCIDRs())
	sig := signature(union)

	r.mu.Lock()
	changed := sig != r.lastSig
	r.mu.Unlock()
	if !changed {
		return
	}
	if err := r.Engine.RebuildRouteCIDRSet(ctx, union); err != nil {
		r.log().Warn("resolve: rebuild route set", "err", err)
		return
	}
	r.mu.Lock()
	r.lastSig = sig
	r.mu.Unlock()
	r.log().Info("resolve: route set updated", "entries", len(union))
}

// resolveInto resolves each domain (IPv4) and records every returned IP's
// last-seen time in the seen cache.
func (r *Resolver) resolveInto(ctx context.Context, domains []string, now time.Time) {
	if len(domains) == 0 {
		return
	}
	r.mu.Lock()
	if r.seen == nil {
		r.seen = make(map[string]time.Time)
	}
	r.mu.Unlock()

	for _, d := range domains {
		lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		ips, err := net.DefaultResolver.LookupIP(lctx, "ip4", d)
		cancel()
		if err != nil {
			r.log().Debug("resolve: lookup failed", "domain", d, "err", err)
			continue
		}
		r.mu.Lock()
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				r.seen[v4.String()] = now
			}
		}
		r.mu.Unlock()
	}
}

// union prunes expired IPs and returns the full set the route ipset should
// hold: resolved IPs (as /32) plus the static CIDRs.
func (r *Resolver) union(now time.Time, staticCIDRs ...[]string) []string {
	out := make([]string, 0, 64)
	r.mu.Lock()
	for ip, last := range r.seen {
		if now.Sub(last) > r.grace() {
			delete(r.seen, ip)
			continue
		}
		out = append(out, ip+"/32")
	}
	r.mu.Unlock()
	for _, cidrs := range staticCIDRs {
		out = append(out, cidrs...)
	}
	return out
}

// listCIDRs returns CIDRs cached from URL list sources.
func (r *Resolver) listCIDRs() []string {
	if r.Lists == nil {
		return nil
	}
	_, cidrs, err := r.Lists.MergedEntries()
	if err != nil {
		r.log().Warn("resolve: merged list entries", "err", err)
		return nil
	}
	return cidrs
}

// cleanDomains trims, lower-cases, drops empties/comments and bare IPs, and
// dedups. Bare IPs are excluded — they belong in RouteCIDR, not resolution.
func cleanDomains(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || strings.HasPrefix(d, "#") {
			continue
		}
		if net.ParseIP(d) != nil {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

// signature is an order-independent fingerprint of the set, so Refresh can skip
// the ipset rebuild when nothing changed.
func signature(entries []string) string {
	cp := append([]string{}, entries...)
	sort.Strings(cp)
	return strings.Join(cp, ",")
}
