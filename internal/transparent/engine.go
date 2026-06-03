package transparent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// Transparent modes.
const (
	ModeOff      = "off"
	ModeTProxy   = "tproxy"   // mangle TPROXY, TCP+UDP
	ModeRedirect = "redirect" // nat REDIRECT, TCP only
)

// Config drives rule generation. It is derived from persisted UI settings plus
// the live server list (for the exclude set) by the caller.
type Config struct {
	Mode         string   // ModeTProxy | ModeRedirect | ModeOff
	TProxyPort   int      // listen port of sing-box's tproxy inbound
	RedirectPort int      // listen port of sing-box's redirect inbound
	PolicyName   string   // bind to this Keenetic policy's fwmark; "" = whole device
	ExtraExclude []string // additional IPv4 CIDRs to always bypass
	UseConntrack bool     // connmark optimization (skip rule eval on established conns)

	// RouteCIDR seeds the route ipset (redirect mode) with static destinations
	// to proxy: manually-entered RouteCIDR plus CIDRs from URL lists. The live
	// resolver later folds resolved domain IPs into the same set. Up uses this
	// for the initial population so the set isn't empty before the first resolve.
	RouteCIDR []string

	// Resolved at apply time (not persisted):
	policyMark string // "0x..." resolved from PolicyName via RCI, or ""
}

// Engine applies and tears down the transparent-proxy firewall. It holds no
// state beyond its collaborators; all rules are derived from the Config passed
// to each call, so the netfilter.d hook can re-drive it after a firewall rebuild.
type Engine struct {
	Runner cmdrun.Runner
	Log    *slog.Logger
	// Bin is the absolute path to this binary, embedded into the netfilter.d
	// hook so KeeneticOS can call back `Bin firewall apply --table $table`.
	Bin string
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

// Up performs the full setup: load modules, (re)build the exclude ipset from
// scratch, install the routing rule for TPROXY, write the netfilter.d hook, and
// apply rules to every relevant table. Call this when (re)activating transparent
// mode (e.g. on Apply & Restart). It is destructive to our own prior state
// (flushes the exclude set) but never touches the router's rules.
func (e *Engine) Up(ctx context.Context, cfg Config) error {
	if cfg.Mode == ModeOff || cfg.Mode == "" {
		return e.Clean(ctx)
	}
	// Only TPROXY needs loadable kernel modules (xt_TPROXY/xt_socket). REDIRECT
	// uses the nat REDIRECT target + the set match, which are built into the
	// KeeneticOS kernel — no insmod, and crucially no /lib/modules tree (which
	// some Keenetic firmwares don't ship at all).
	if cfg.Mode == ModeTProxy {
		if _, err := LoadModules(ctx, e.Runner); err != nil {
			return err
		}
	}
	cfg.policyMark = PolicyMark(ctx, cfg.PolicyName)

	if err := e.rebuildExcludeSet(ctx, cfg); err != nil {
		return fmt.Errorf("exclude set: %w", err)
	}
	if cfg.Mode == ModeRedirect {
		// Selective REDIRECT matches dst against the route ipset. Seed it with the
		// static CIDRs additively (no flush) so capture works before the resolver's
		// first pass and so reviving sing-box never wipes resolver-added domain IPs.
		e.ensureRouteSet(ctx)
		e.seedRouteSet(ctx, cfg.RouteCIDR)
	}
	if cfg.Mode == ModeTProxy {
		if err := e.ensureRoute(ctx); err != nil {
			return fmt.Errorf("route: %w", err)
		}
	}
	if err := e.writeHook(cfg); err != nil {
		return fmt.Errorf("hook: %w", err)
	}
	// Give sing-box a moment to bind its inbound after a restart, so the
	// liveness interlock in Apply doesn't skip capture on a startup race.
	waitProxy(cfg.inboundPort(), 5*time.Second)
	for _, table := range cfg.tables() {
		if err := e.Apply(ctx, cfg, table); err != nil {
			return fmt.Errorf("apply %s: %w", table, err)
		}
	}
	return nil
}

// Apply (re)installs our chains for a single table. Idempotent: leaf chains are
// recreated and the parent-chain jump is added only if absent. This is what the
// netfilter.d hook calls (once per table) after KeeneticOS rebuilds its firewall.
// It ensures modules/ipset/route exist so it is also correct on a cold boot,
// when Up has not necessarily run first.
func (e *Engine) Apply(ctx context.Context, cfg Config, table string) error {
	if cfg.Mode == ModeOff || cfg.Mode == "" {
		return nil
	}
	if !cfg.usesTable(table) {
		return nil
	}
	// Cold-boot safety: make sure the prerequisites exist. These are cheap and
	// idempotent (create -exist / add -exist / rule replace). Modules only for
	// TPROXY (REDIRECT needs none).
	if cfg.Mode == ModeTProxy {
		_, _ = LoadModules(ctx, e.Runner)
	}
	if cfg.policyMark == "" {
		cfg.policyMark = PolicyMark(ctx, cfg.PolicyName)
	}
	e.ensureExcludeSet(ctx, cfg)
	if cfg.Mode == ModeRedirect {
		e.ensureRouteSet(ctx) // contents preserved; resolver/Up populate it
	}
	if cfg.Mode == ModeTProxy {
		_ = e.ensureRoute(ctx)
	}

	// Safety interlock: only capture traffic if sing-box is actually listening.
	// Otherwise we'd divert the whole LAN to a dead port. When it's down we
	// strip our capture jumps so traffic flows directly until it recovers (the
	// next firewall rebuild — or a serversApply — re-drives this).
	if !proxyListening(cfg.inboundPort()) {
		e.log().Warn("transparent: proxy not listening, skipping capture", "port", cfg.inboundPort())
		const ipt = "iptables"
		for _, proto := range []string{"tcp", "udp"} {
			e.deleteJump(ctx, ipt, "mangle", "PREROUTING", chainPrerouting, proto)
			e.deleteJump(ctx, ipt, "nat", "PREROUTING", chainPrerouting, proto)
		}
		// Also drop the filter FORWARD QUIC-block jump, so UDP/443 flows freely
		// while the proxy is down (no TCP path to fall back to anyway).
		e.deleteJump(ctx, ipt, "filter", "FORWARD", chainForward, "udp")
		return nil
	}

	switch cfg.Mode {
	case ModeTProxy:
		return e.applyTProxy(ctx, cfg)
	case ModeRedirect:
		switch table {
		case "filter":
			return e.applyRedirectFilter(ctx, cfg)
		default:
			return e.applyRedirect(ctx, cfg)
		}
	}
	return nil
}

// tables returns the iptables tables this mode writes to.
func (c Config) tables() []string {
	switch c.Mode {
	case ModeTProxy:
		return []string{"mangle"}
	case ModeRedirect:
		// nat: the selective REDIRECT. filter: the UDP/443 (QUIC) block that
		// forces browsers onto the redirected TCP path.
		return []string{"nat", "filter"}
	}
	return nil
}

func (c Config) usesTable(table string) bool {
	for _, t := range c.tables() {
		if t == table {
			return true
		}
	}
	return false
}

func (c Config) protocols() []string {
	if c.Mode == ModeRedirect {
		return []string{"tcp"} // REDIRECT is TCP-only
	}
	return []string{"tcp", "udp"}
}
