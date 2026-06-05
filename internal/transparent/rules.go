package transparent

import (
	"context"
	"strconv"
)

// add appends a rule to a chain (errors are surfaced only via the engine log;
// individual rule failures shouldn't abort the whole apply).
func (e *Engine) add(ctx context.Context, ipt, table, chain string, args ...string) {
	full := append([]string{"-w", "-t", table, "-A", chain}, args...)
	if _, err := run(ctx, e.Runner, ipt, full...); err != nil {
		e.log().Warn("iptables add", "table", table, "chain", chain, "args", args, "err", err)
	}
}

// recreateChain ensures a custom chain exists and is empty.
func (e *Engine) recreateChain(ctx context.Context, ipt, table, chain string) {
	if ok(ctx, e.Runner, ipt, "-w", "-t", table, "-S", chain) {
		_, _ = run(ctx, e.Runner, ipt, "-w", "-t", table, "-F", chain)
		return
	}
	_, _ = run(ctx, e.Runner, ipt, "-w", "-t", table, "-N", chain)
}

// jumpArgs is the spec used to enter our chain from a built-in chain. Guarding
// on ! INVALID avoids hijacking conntrack-invalid packets.
func jumpArgs(proto, target string) []string {
	return []string{"-p", proto, "-m", "conntrack", "!", "--ctstate", "INVALID", "-g", target}
}

// ensureJump adds the PREROUTING->chain jump once (checked via -C).
func (e *Engine) ensureJump(ctx context.Context, ipt, table, parent, target, proto string) {
	args := jumpArgs(proto, target)
	if ok(ctx, e.Runner, ipt, append([]string{"-w", "-t", table, "-C", parent}, args...)...) {
		return
	}
	_, _ = run(ctx, e.Runner, ipt, append([]string{"-w", "-t", table, "-A", parent}, args...)...)
}

// ensureJumpTop adds the parent->chain jump at the TOP of the parent chain
// (position 1), checked via -C so it's not duplicated. Used for the filter
// FORWARD QUIC-block, which must run before any broad upstream ACCEPT.
func (e *Engine) ensureJumpTop(ctx context.Context, ipt, table, parent, target, proto string) {
	args := jumpArgs(proto, target)
	if ok(ctx, e.Runner, ipt, append([]string{"-w", "-t", table, "-C", parent}, args...)...) {
		return
	}
	_, _ = run(ctx, e.Runner, ipt, append([]string{"-w", "-t", table, "-I", parent, "1"}, args...)...)
}

// deleteJump removes every copy of our jump from a built-in chain.
func (e *Engine) deleteJump(ctx context.Context, ipt, table, parent, target, proto string) {
	args := append([]string{"-w", "-t", table, "-D", parent}, jumpArgs(proto, target)...)
	for ok(ctx, e.Runner, ipt, args...) {
	}
}

func (e *Engine) dropChain(ctx context.Context, ipt, table, chain string) {
	_, _ = run(ctx, e.Runner, ipt, "-w", "-t", table, "-F", chain)
	_, _ = run(ctx, e.Runner, ipt, "-w", "-t", table, "-X", chain)
}

// applyTProxy installs the mangle TPROXY path (TCP+UDP). Order matters: policy
// gate, then divert already-established transparent sockets, then skip reply
// packets, then bypass excluded destinations, finally TPROXY the rest into
// sing-box. sing-box then decides proxy-vs-direct per its own route rules.
func (e *Engine) applyTProxy(ctx context.Context, cfg Config) error {
	const ipt, table = "iptables", "mangle"
	set := excludeSetV4()
	port := strconv.Itoa(cfg.TProxyPort)

	e.recreateChain(ctx, ipt, table, chainPrerouting)
	e.recreateChain(ctx, ipt, table, chainDivert)
	e.add(ctx, ipt, table, chainDivert, "-j", "MARK", "--set-mark", MarkHex)
	e.add(ctx, ipt, table, chainDivert, "-j", "ACCEPT")

	if cfg.policyMark != "" {
		e.add(ctx, ipt, table, chainPrerouting, "-m", "connmark", "!", "--mark", cfg.policyMark, "-j", "ACCEPT")
	}
	e.add(ctx, ipt, table, chainPrerouting, "-p", "tcp", "-m", "socket", "--transparent", "-g", chainDivert)
	if cfg.UseConntrack {
		e.add(ctx, ipt, table, chainPrerouting, "-m", "conntrack", "--ctdir", "REPLY", "-j", "ACCEPT")
	}
	e.add(ctx, ipt, table, chainPrerouting, "-m", "set", "--match-set", set, "dst", "-j", "ACCEPT")
	for _, proto := range cfg.protocols() {
		e.add(ctx, ipt, table, chainPrerouting, "-p", proto, "-j", "TPROXY",
			"--on-ip", "127.0.0.1", "--on-port", port, "--tproxy-mark", MarkHex)
	}
	for _, proto := range cfg.protocols() {
		e.ensureJump(ctx, ipt, table, "PREROUTING", chainPrerouting, proto)
	}
	return nil
}

// applyRedirect installs the nat REDIRECT path (TCP only), SELECTIVE: only TCP
// whose destination is in the route ipset is redirected into sing-box; the rest
// egresses directly. The decision lives at the iptables layer (the route set,
// kept current by the DNS resolver) — sing-box just proxies what it receives
// (final=proxy). Rule order: policy gate → exclude bypass → route-set REDIRECT.
func (e *Engine) applyRedirect(ctx context.Context, cfg Config) error {
	const ipt, table = "iptables", "nat"
	exclude := excludeSetV4()
	route := routeSetV4()
	port := strconv.Itoa(cfg.RedirectPort)

	e.recreateChain(ctx, ipt, table, chainPrerouting)
	if cfg.policyMark != "" {
		e.add(ctx, ipt, table, chainPrerouting, "-m", "connmark", "!", "--mark", cfg.policyMark, "-j", "ACCEPT")
	}
	e.add(ctx, ipt, table, chainPrerouting, "-m", "set", "--match-set", exclude, "dst", "-j", "ACCEPT")
	e.add(ctx, ipt, table, chainPrerouting, "-p", "tcp", "-m", "set", "--match-set", route, "dst", "-j", "REDIRECT", "--to-ports", port)
	e.ensureJump(ctx, ipt, table, "PREROUTING", chainPrerouting, "tcp")
	return nil
}

// applyRedirectFilter installs the filter FORWARD QUIC-block for redirect mode.
// REDIRECT is TCP-only, so HTTP/3 (UDP/443) to a proxied host would bypass it
// and egress directly — the very leak we're closing. Rejecting UDP/443 to
// route-set members makes browsers fall back to TCP, which IS redirected.
// The exclude set is honoured first so the router's own/WAN UDP is never hit.
// The jump is inserted at the top of FORWARD so a broad upstream ACCEPT can't
// shadow it.
func (e *Engine) applyRedirectFilter(ctx context.Context, cfg Config) error {
	const ipt, table = "iptables", "filter"
	exclude := excludeSetV4()
	route := routeSetV4()

	e.recreateChain(ctx, ipt, table, chainForward)
	e.add(ctx, ipt, table, chainForward, "-m", "set", "--match-set", exclude, "dst", "-j", "RETURN")
	e.add(ctx, ipt, table, chainForward, "-p", "udp", "--dport", "443",
		"-m", "set", "--match-set", route, "dst", "-j", "REJECT", "--reject-with", "icmp-port-unreachable")
	e.ensureJumpTop(ctx, ipt, table, "FORWARD", chainForward, "udp")
	return nil
}

// cleanTable removes our jumps and chains from a single iptables table. Safe to
// call when nothing is installed (all steps ignore "not found"). Used both by
// Clean (full teardown) and by Up (to strip the inactive mode's table, e.g. the
// stale nat REDIRECT when switching from redirect to tproxy — otherwise both the
// nat REDIRECT and the mangle TPROXY would try to handle the same TCP packet).
func (e *Engine) cleanTable(ctx context.Context, table string) {
	const ipt = "iptables"
	switch table {
	case "mangle", "nat":
		for _, proto := range []string{"tcp", "udp"} {
			e.deleteJump(ctx, ipt, table, "PREROUTING", chainPrerouting, proto)
		}
		for _, c := range []string{chainPrerouting, chainDivert, chainTproxy, chainRedirect, chainMarkOut, chainOutput} {
			e.dropChain(ctx, ipt, table, c)
		}
	case "filter":
		e.deleteJump(ctx, ipt, "filter", "FORWARD", chainForward, "udp")
		e.dropChain(ctx, ipt, "filter", chainForward)
	}
}

// Clean removes everything we install: jumps, chains, the routing rule/table,
// the exclude ipset, and the netfilter.d hook. Safe to call when nothing is
// installed (all steps ignore "not found" errors). It never touches rules that
// aren't ours.
func (e *Engine) Clean(ctx context.Context) error {
	for _, t := range []string{"mangle", "nat", "filter"} {
		e.cleanTable(ctx, t)
	}
	e.flushRoute(ctx)
	e.destroyExcludeSet(ctx)
	e.destroyRouteSet(ctx)
	_ = e.removeHook()
	return nil
}
