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
//
// useGoto picks -g vs -j, and the choice is load-bearing:
//   - PREROUTING (mangle/nat, policy ACCEPT) uses -g (goto): when our chain
//     falls through without a match, control returns to the policy (ACCEPT), so
//     unmatched traffic is simply accepted and continues normally.
//   - FORWARD (filter, policy DROP) MUST use -j (jump): with -g, a fall-through
//     returns to the *policy* — which is DROP — so ALL traffic not matched by
//     our reject/route rules would be dropped (i.e. the whole internet except
//     the proxied set). With -j, fall-through continues at the next FORWARD rule
//     (the router's own ACCEPT-established / NDM chains), as intended.
func jumpArgs(proto, target string, useGoto bool) []string {
	jt := "-j"
	if useGoto {
		jt = "-g"
	}
	return []string{"-p", proto, "-m", "conntrack", "!", "--ctstate", "INVALID", jt, target}
}

// CaptureInstalled reports whether our PREROUTING capture jump is present for
// the active mode's table. Cheap — a single `iptables -C`. The watchdog uses it
// to detect that a firewall rebuild (e.g. KeeneticOS wiping our chains on a
// WAN-interface change) left sing-box up but capture gone. The netfilter.d hook
// is meant to re-drive Apply then, but doesn't fire on every such event on this
// firmware, so the watchdog needs a way to notice independently. Mode off has
// nothing to install, so it reports installed.
func (e *Engine) CaptureInstalled(ctx context.Context, cfg Config) bool {
	if cfg.Mode == ModeOff || cfg.Mode == "" {
		return true
	}
	table := "nat"
	if cfg.Mode == ModeTProxy {
		table = "mangle"
	}
	// The tcp PREROUTING jump is installed for both modes; if a rebuild dropped
	// our chains it's gone too, so checking it alone is a sufficient signal.
	args := append([]string{"-w", "-t", table, "-C", "PREROUTING"}, jumpArgs("tcp", chainPrerouting, true)...)
	return ok(ctx, e.Runner, "iptables", args...)
}

// ensureJump adds the PREROUTING->chain jump once (checked via -C). Goto: our
// chain falls through to the table's ACCEPT policy when nothing matches.
func (e *Engine) ensureJump(ctx context.Context, ipt, table, parent, target, proto string) {
	args := jumpArgs(proto, target, true)
	if ok(ctx, e.Runner, ipt, append([]string{"-w", "-t", table, "-C", parent}, args...)...) {
		return
	}
	_, _ = run(ctx, e.Runner, ipt, append([]string{"-w", "-t", table, "-A", parent}, args...)...)
}

// ensureForwardJumpTop adds the filter FORWARD->chain jump at the TOP (position
// 1), checked via -C so it's not duplicated. It must run before any broad
// upstream ACCEPT, and uses -j (NOT goto) so unmatched packets fall through to
// the rest of FORWARD instead of hitting the DROP policy.
func (e *Engine) ensureForwardJumpTop(ctx context.Context, ipt, table, parent, target, proto string) {
	args := jumpArgs(proto, target, false)
	if ok(ctx, e.Runner, ipt, append([]string{"-w", "-t", table, "-C", parent}, args...)...) {
		return
	}
	_, _ = run(ctx, e.Runner, ipt, append([]string{"-w", "-t", table, "-I", parent, "1"}, args...)...)
}

// deleteJump removes every copy of our jump from a built-in chain. useGoto must
// match how the jump was installed (the -g/-j flag is part of the rule spec, so
// a -D with the wrong flag won't match). For robustness it also sweeps the other
// flavour, so a jump left over from an older build (e.g. a -g FORWARD jump from
// before the goto→jump fix) is cleaned up too.
func (e *Engine) deleteJump(ctx context.Context, ipt, table, parent, target, proto string, useGoto bool) {
	for _, g := range []bool{useGoto, !useGoto} {
		args := append([]string{"-w", "-t", table, "-D", parent}, jumpArgs(proto, target, g)...)
		for ok(ctx, e.Runner, ipt, args...) {
		}
	}
}

func (e *Engine) dropChain(ctx context.Context, ipt, table, chain string) {
	_, _ = run(ctx, e.Runner, ipt, "-w", "-t", table, "-F", chain)
	_, _ = run(ctx, e.Runner, ipt, "-w", "-t", table, "-X", chain)
}

// applyTProxy installs the mangle TPROXY path (TCP+UDP), SELECTIVE: only traffic
// whose destination is in the route ipset is TPROXY'd into sing-box; everything
// else (incl. client DNS) falls through the chain and egresses directly at
// kernel speed — never entering sing-box userspace. This mirrors the redirect
// path's iptables-layer selection, but for TCP+UDP, so it keeps UDP/QUIC
// proxying while eliminating the userspace relay (and DNS-hijack) overhead that
// a capture-all TPROXY imposed on direct traffic. The route set is kept current
// by the DNS resolver, so sing-box only ever receives traffic to be proxied
// (route.final=proxy). Order matters: policy gate, then divert already-
// established transparent sockets, then skip reply packets, then bypass excluded
// destinations, finally TPROXY only route-set destinations.
func (e *Engine) applyTProxy(ctx context.Context, cfg Config) error {
	const ipt, table = "iptables", "mangle"
	set := excludeSetV4()
	route := routeSetV4()
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
		e.add(ctx, ipt, table, chainPrerouting, "-p", proto, "-m", "set", "--match-set", route, "dst",
			"-j", "TPROXY", "--on-ip", "127.0.0.1", "--on-port", port, "--tproxy-mark", MarkHex)
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

// applyFilter installs the filter FORWARD leaf used by BOTH transparent modes:
//
//  1. Reject-set blackhole (both modes): TCP/443 to a reject-set member gets a
//     reset, UDP/443 an ICMP unreachable. This is for throttled CDN endpoints
//     (e.g. an ISP-embedded Google Global Cache that SYN-ACKs then stalls): the
//     instant rejection makes a client opening parallel connections drop the
//     dead node and use a healthy one we proxy, killing the multi-second stall.
//  2. Redirect-mode QUIC block: REDIRECT is TCP-only, so HTTP/3 (UDP/443) to a
//     proxied host would bypass it and egress directly — the leak we're closing.
//     Rejecting UDP/443 to route-set members makes browsers fall back to TCP,
//     which IS redirected. (TPROXY handles UDP itself, so it skips this.)
//
// Rule order matters. The reject-set is evaluated FIRST — before the exclude
// RETURN — because it's the user's explicit, specific intent: a reject entry is
// commonly a narrow sub-block of a broad exclude (e.g. an ISP GGC /21 inside a
// VK/Mail.ru /19 the user keeps direct), and honouring exclude first would mask
// it. The exclude RETURN then guards the redirect QUIC block below it, so the
// router's own/WAN UDP/443 is never hit by that catch-all.
//
// Jumps are inserted at the TOP of FORWARD (tcp + udp) so a broad upstream
// ACCEPT can't shadow them.
func (e *Engine) applyFilter(ctx context.Context, cfg Config) error {
	const ipt, table = "iptables", "filter"
	exclude := excludeSetV4()
	reject := rejectSetV4()
	route := routeSetV4()

	e.recreateChain(ctx, ipt, table, chainForward)
	// Reject-set first: explicit user intent wins over a broader exclude.
	e.add(ctx, ipt, table, chainForward, "-p", "tcp", "--dport", "443",
		"-m", "set", "--match-set", reject, "dst", "-j", "REJECT", "--reject-with", "tcp-reset")
	e.add(ctx, ipt, table, chainForward, "-p", "udp", "--dport", "443",
		"-m", "set", "--match-set", reject, "dst", "-j", "REJECT", "--reject-with", "icmp-port-unreachable")
	// Exclude RETURN guards the redirect QUIC block (router's own/WAN UDP/443).
	e.add(ctx, ipt, table, chainForward, "-m", "set", "--match-set", exclude, "dst", "-j", "RETURN")
	if cfg.Mode == ModeRedirect {
		e.add(ctx, ipt, table, chainForward, "-p", "udp", "--dport", "443",
			"-m", "set", "--match-set", route, "dst", "-j", "REJECT", "--reject-with", "icmp-port-unreachable")
	}
	e.ensureForwardJumpTop(ctx, ipt, table, "FORWARD", chainForward, "tcp")
	e.ensureForwardJumpTop(ctx, ipt, table, "FORWARD", chainForward, "udp")
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
			e.deleteJump(ctx, ipt, table, "PREROUTING", chainPrerouting, proto, true)
		}
		for _, c := range []string{chainPrerouting, chainDivert, chainTproxy, chainRedirect, chainMarkOut, chainOutput} {
			e.dropChain(ctx, ipt, table, c)
		}
	case "filter":
		e.deleteJump(ctx, ipt, "filter", "FORWARD", chainForward, "tcp", false)
		e.deleteJump(ctx, ipt, "filter", "FORWARD", chainForward, "udp", false)
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
	e.destroyRejectSet(ctx)
	_ = e.removeHook()
	return nil
}
