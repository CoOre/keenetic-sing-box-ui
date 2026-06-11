package transparent

import (
	"context"
	"fmt"
	"strings"
)

func excludeSetV4() string { return netExcludeSet + "4" }
func routeSetV4() string   { return netRouteSet + "4" }
func rejectSetV4() string  { return netRejectSet + "4" }

// normalizeEntries trims/validates and dedups CIDRs to add to an ipset.
func normalizeEntries(cidrs []string) []string {
	out := make([]string, 0, len(cidrs))
	for _, c := range dedup(cidrs) {
		if c = normalizeCIDR(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// ensureRouteSet guarantees the route ipset exists, without touching its
// contents (create -exist, no flush). Cheap and safe to call from the cold-boot
// path and the netfilter.d hook's short-lived `firewall apply`: it must NOT
// wipe entries that the long-lived resolver populated, only make sure the set
// is there so the `--match-set` rule doesn't error.
func (e *Engine) ensureRouteSet(ctx context.Context) {
	_, _ = run(ctx, e.Runner, "ipset", "create", routeSetV4(), "hash:net", "family", "inet", "-exist")
}

// seedRouteSet additively adds static CIDRs to the route ipset WITHOUT flushing.
// Used by Up to give the set immediate coverage before the resolver's first
// pass, without ever removing entries the resolver already folded in (resolved
// domain IPs). The resolver remains the authority for the full set via the
// atomic RebuildRouteCIDRSet. Entries are loaded in one `ipset restore` batch —
// per-entry `ipset add` subprocesses would peg the router CPU on big lists.
func (e *Engine) seedRouteSet(ctx context.Context, cidrs []string) {
	set := routeSetV4()
	entries := normalizeEntries(cidrs)
	var b strings.Builder
	b.WriteString("create " + set + " hash:net family inet -exist\n")
	for _, c := range entries {
		b.WriteString("add " + set + " " + c + " -exist\n")
	}
	if _, ok, err := runStdin(ctx, e.Runner, []byte(b.String()), "ipset", "restore", "-exist"); ok {
		if err != nil {
			e.log().Warn("ipset restore (seed)", "err", err)
		}
		return
	}
	// Fallback: no stdin support (shouldn't happen with the OS runner).
	_, _ = run(ctx, e.Runner, "ipset", "create", set, "hash:net", "family", "inet", "-exist")
	for _, c := range entries {
		_, _ = run(ctx, e.Runner, "ipset", "add", set, c, "-exist")
	}
}

// RebuildRouteCIDRSet atomically replaces the route ipset with the given CIDRs.
// These are matched at the iptables layer (selective REDIRECT) — no sing-box
// route rules needed, regardless of list size.
//
// All of it happens in ONE `ipset restore` batch: a temp set is filled and
// swapped in, so (a) the live set is never empty mid-update — a connection
// racing an empty window would leak straight to direct — and (b) tens of
// thousands of entries load in a single process instead of one subprocess per
// entry (which pegged the router CPU at 100%).
func (e *Engine) RebuildRouteCIDRSet(ctx context.Context, cidrs []string) error {
	set := routeSetV4()
	tmp := set + "_tmp"
	entries := normalizeEntries(cidrs)

	var b strings.Builder
	b.WriteString("create " + set + " hash:net family inet -exist\n")
	b.WriteString("create " + tmp + " hash:net family inet -exist\n")
	b.WriteString("flush " + tmp + "\n")
	for _, c := range entries {
		b.WriteString("add " + tmp + " " + c + "\n")
	}
	b.WriteString("swap " + tmp + " " + set + "\n")
	b.WriteString("destroy " + tmp + "\n")

	if out, ok, err := runStdin(ctx, e.Runner, []byte(b.String()), "ipset", "restore", "-exist"); ok {
		if err != nil {
			return fmt.Errorf("ipset restore: %w (%s)", err, out)
		}
		return nil
	}

	// Fallback: per-entry (no stdin support — shouldn't happen with OS runner).
	_, _ = run(ctx, e.Runner, "ipset", "create", set, "hash:net", "family", "inet", "-exist")
	_, _ = run(ctx, e.Runner, "ipset", "create", tmp, "hash:net", "family", "inet", "-exist")
	if _, err := run(ctx, e.Runner, "ipset", "flush", tmp); err != nil {
		return err
	}
	for _, c := range entries {
		_, _ = run(ctx, e.Runner, "ipset", "add", tmp, c, "-exist")
	}
	if _, err := run(ctx, e.Runner, "ipset", "swap", tmp, set); err != nil {
		_, _ = run(ctx, e.Runner, "ipset", "destroy", tmp)
		return err
	}
	_, _ = run(ctx, e.Runner, "ipset", "destroy", tmp)
	return nil
}

func (e *Engine) destroyRouteSet(ctx context.Context) {
	set := routeSetV4()
	// Drop a possibly-leaked temp set from an interrupted atomic rebuild first.
	_, _ = run(ctx, e.Runner, "ipset", "destroy", set+"_tmp")
	_, _ = run(ctx, e.Runner, "ipset", "flush", set)
	_, _ = run(ctx, e.Runner, "ipset", "destroy", set)
}

// excludeEntries is the full bypass list: reserved ranges + the router's WAN
// IPs (incl. the proxy server's egress path) + user-supplied CIDRs.
func (e *Engine) excludeEntries(ctx context.Context, cfg Config) []string {
	entries := append([]string{}, reservedV4...)
	entries = append(entries, wanIPv4(ctx, e.Runner)...)
	for _, c := range cfg.ExtraExclude {
		if c = normalizeCIDR(c); c != "" {
			entries = append(entries, c)
		}
	}
	return dedup(entries)
}

// rebuildExcludeSet creates the set if needed and replaces its contents.
func (e *Engine) rebuildExcludeSet(ctx context.Context, cfg Config) error {
	set := excludeSetV4()
	if _, err := run(ctx, e.Runner, "ipset", "create", set, "hash:net", "family", "inet", "-exist"); err != nil {
		return err
	}
	if _, err := run(ctx, e.Runner, "ipset", "flush", set); err != nil {
		return err
	}
	for _, entry := range e.excludeEntries(ctx, cfg) {
		_, _ = run(ctx, e.Runner, "ipset", "add", set, entry, "-exist")
	}
	return nil
}

// ensureExcludeSet guarantees the set exists and contains the current entries,
// without flushing (safe to call from the hook mid-traffic). Adds are -exist so
// they don't duplicate; it does not prune stale entries (Up handles that).
func (e *Engine) ensureExcludeSet(ctx context.Context, cfg Config) {
	set := excludeSetV4()
	_, _ = run(ctx, e.Runner, "ipset", "create", set, "hash:net", "family", "inet", "-exist")
	for _, entry := range e.excludeEntries(ctx, cfg) {
		_, _ = run(ctx, e.Runner, "ipset", "add", set, entry, "-exist")
	}
}

func (e *Engine) destroyExcludeSet(ctx context.Context) {
	set := excludeSetV4()
	_, _ = run(ctx, e.Runner, "ipset", "flush", set)
	_, _ = run(ctx, e.Runner, "ipset", "destroy", set)
}

// rebuildRejectSet creates the reject set if needed and replaces its contents
// with the user-curated CIDRs. Called from Up (full apply).
func (e *Engine) rebuildRejectSet(ctx context.Context, cfg Config) error {
	set := rejectSetV4()
	if _, err := run(ctx, e.Runner, "ipset", "create", set, "hash:net", "family", "inet", "-exist"); err != nil {
		return err
	}
	if _, err := run(ctx, e.Runner, "ipset", "flush", set); err != nil {
		return err
	}
	for _, entry := range normalizeEntries(cfg.RejectCIDR) {
		_, _ = run(ctx, e.Runner, "ipset", "add", set, entry, "-exist")
	}
	return nil
}

// ensureRejectSet guarantees the reject set exists and holds the current
// entries, without flushing — safe from the netfilter.d hook mid-traffic. Adds
// are -exist so they don't duplicate; stale entries are pruned only by Up.
func (e *Engine) ensureRejectSet(ctx context.Context, cfg Config) {
	set := rejectSetV4()
	_, _ = run(ctx, e.Runner, "ipset", "create", set, "hash:net", "family", "inet", "-exist")
	for _, entry := range normalizeEntries(cfg.RejectCIDR) {
		_, _ = run(ctx, e.Runner, "ipset", "add", set, entry, "-exist")
	}
}

func (e *Engine) destroyRejectSet(ctx context.Context) {
	set := rejectSetV4()
	_, _ = run(ctx, e.Runner, "ipset", "flush", set)
	_, _ = run(ctx, e.Runner, "ipset", "destroy", set)
}

// ensureRoute installs the TPROXY divert: marked packets are delivered locally
// via a dedicated routing table. Idempotent — replaces any existing rule/route.
// Mirrors SKeen's check_and_set_route_rules (the tproxy branch).
func (e *Engine) ensureRoute(ctx context.Context) error {
	// Replace is fine even if absent (ignore errors on the del).
	_, _ = run(ctx, e.Runner, "ip", "-4", "rule", "del", "fwmark", MarkHex, "lookup", RouteTblID)
	if _, err := run(ctx, e.Runner, "ip", "-4", "rule", "add", "fwmark", MarkHex, "lookup", RouteTblID); err != nil {
		return err
	}
	_, _ = run(ctx, e.Runner, "ip", "-4", "route", "flush", "table", RouteTblID)
	if _, err := run(ctx, e.Runner, "ip", "-4", "route", "add", "local", "default", "dev", "lo", "table", RouteTblID); err != nil {
		return err
	}
	return nil
}

func (e *Engine) flushRoute(ctx context.Context) {
	_, _ = run(ctx, e.Runner, "ip", "-4", "rule", "del", "fwmark", MarkHex, "lookup", RouteTblID)
	_, _ = run(ctx, e.Runner, "ip", "-4", "route", "flush", "table", RouteTblID)
}
