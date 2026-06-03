package transparent

import (
	"context"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// callLine renders a recorded call as "tool arg arg ..." for substring asserts.
func callLine(c cmdrun.FakeCall) string {
	return c.Name + " " + strings.Join(c.Args, " ")
}

func hasCall(calls []cmdrun.FakeCall, substr string) bool {
	for _, c := range calls {
		if strings.Contains(callLine(c), substr) {
			return true
		}
	}
	return false
}

func TestApplyTProxyRules(t *testing.T) {
	// Default error makes existence probes (-S/-C) report "absent", so chains
	// are created and the PREROUTING jump is appended (otherwise ensureJump
	// would think the jump already exists). Rules are recorded either way.
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}}
	e := &Engine{Runner: f}
	cfg := Config{Mode: ModeTProxy, TProxyPort: 2080, UseConntrack: true}

	if err := e.applyTProxy(context.Background(), cfg); err != nil {
		t.Fatalf("applyTProxy: %v", err)
	}

	want := []string{
		"-t mangle -A " + chainDivert + " -j MARK --set-mark " + MarkHex,
		"-t mangle -A " + chainPrerouting + " -p tcp -m socket --transparent -g " + chainDivert,
		"-m conntrack --ctdir REPLY -j ACCEPT",
		"-m set --match-set " + excludeSetV4() + " dst -j ACCEPT",
		"-p tcp -j TPROXY --on-ip 127.0.0.1 --on-port 2080 --tproxy-mark " + MarkHex,
		"-p udp -j TPROXY --on-ip 127.0.0.1 --on-port 2080 --tproxy-mark " + MarkHex,
		"-A PREROUTING -p tcp -m conntrack ! --ctstate INVALID -g " + chainPrerouting,
	}
	for _, w := range want {
		if !hasCall(f.Calls, w) {
			t.Errorf("missing expected iptables call: %q", w)
		}
	}
}

func TestApplyTProxyPolicyGate(t *testing.T) {
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}}
	e := &Engine{Runner: f}
	cfg := Config{Mode: ModeTProxy, TProxyPort: 2080}
	cfg.policyMark = "0x4ff"

	if err := e.applyTProxy(context.Background(), cfg); err != nil {
		t.Fatalf("applyTProxy: %v", err)
	}
	if !hasCall(f.Calls, "-m connmark ! --mark 0x4ff -j ACCEPT") {
		t.Errorf("policy gate rule not emitted")
	}
}

func TestApplyRedirectSelective(t *testing.T) {
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}}
	e := &Engine{Runner: f}
	cfg := Config{Mode: ModeRedirect, RedirectPort: 2081}

	if err := e.applyRedirect(context.Background(), cfg); err != nil {
		t.Fatalf("applyRedirect: %v", err)
	}
	// REDIRECT is selective: only route-ipset members (dst) are redirected.
	want := "-t nat -A " + chainPrerouting + " -p tcp -m set --match-set " + routeSetV4() + " dst -j REDIRECT --to-ports 2081"
	if !hasCall(f.Calls, want) {
		t.Errorf("selective redirect rule not emitted; want %q", want)
	}
	// A blanket catch-all REDIRECT (no match-set) would re-introduce the leak.
	if hasCall(f.Calls, "-A "+chainPrerouting+" -p tcp -j REDIRECT") {
		t.Errorf("catch-all REDIRECT must not be emitted")
	}
	// The nat path is TCP-only (the UDP/443 block lives in the filter table).
	for _, c := range f.Calls {
		if strings.Contains(callLine(c), "-p udp") {
			t.Errorf("redirect nat path must be TCP-only, got: %s", callLine(c))
		}
	}
}

func TestApplyRedirectFilterBlocksQUIC(t *testing.T) {
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}}
	e := &Engine{Runner: f}
	cfg := Config{Mode: ModeRedirect, RedirectPort: 2081}

	if err := e.applyRedirectFilter(context.Background(), cfg); err != nil {
		t.Fatalf("applyRedirectFilter: %v", err)
	}
	// UDP/443 to route-set members is rejected so browsers fall back to TCP.
	want := "-t filter -A " + chainForward + " -p udp --dport 443 -m set --match-set " + routeSetV4() + " dst -j REJECT --reject-with icmp-port-unreachable"
	if !hasCall(f.Calls, want) {
		t.Errorf("QUIC block rule not emitted; want %q", want)
	}
	// Jump is inserted at the top of FORWARD (not appended), so upstream ACCEPTs
	// can't shadow it.
	if !hasCall(f.Calls, "-I FORWARD 1 -p udp -m conntrack ! --ctstate INVALID -g "+chainForward) {
		t.Errorf("FORWARD top-insert jump not emitted")
	}
}

func TestCleanRemovesJumpsAndChains(t *testing.T) {
	f := &cmdrun.Fake{}
	// Make the -C / -D probes "succeed" once then stop, so deleteJump's loop
	// terminates. With Fake returning nil err always, the loop would spin; use
	// a response that errors to break it.
	f.Default = cmdrun.FakeResponse{Err: errStub}
	e := &Engine{Runner: f}
	if err := e.Clean(context.Background()); err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if !hasCall(f.Calls, "-X "+chainPrerouting) {
		t.Errorf("expected chain delete for %s", chainPrerouting)
	}
}

func TestNormalizeCIDR(t *testing.T) {
	cases := map[string]string{
		"1.2.3.4":       "1.2.3.4/32",
		"10.0.0.0/8":    "10.0.0.0/8",
		" 8.8.8.8 ":     "8.8.8.8/32",
		"#comment":      "",
		"not-an-ip":     "",
		"2001:db8::/32": "", // IPv6 not handled in v1
	}
	for in, want := range cases {
		if got := normalizeCIDR(in); got != want {
			t.Errorf("normalizeCIDR(%q) = %q, want %q", in, got, want)
		}
	}
}

var errStub = stubErr("stub")

type stubErr string

func (e stubErr) Error() string { return string(e) }
