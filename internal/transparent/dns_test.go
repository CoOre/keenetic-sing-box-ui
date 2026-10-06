package transparent

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// listenPort opens a loopback TCP listener so proxyListening reports the
// port as up (the dns-in liveness interlock).
func listenPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

func TestApplyDNSRedirectsLANToDNSIn(t *testing.T) {
	port := listenPort(t)
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}}
	e := &Engine{Runner: f}
	cfg := Config{Mode: ModeTProxy, TProxyPort: 2080, DNSRedirectPort: port}
	cfg.policyMark = "0x4ff"

	if err := e.applyDNS(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	p := strconv.Itoa(port)
	want := []string{
		"-t nat -A " + chainDNS + " -m connmark ! --mark 0x4ff -j RETURN",
		"-t nat -A " + chainDNS + " -s 192.168.0.0/16 -p udp --dport 53 -j REDIRECT --to-ports " + p,
		"-t nat -A " + chainDNS + " -s 10.0.0.0/8 -p tcp --dport 53 -j REDIRECT --to-ports " + p,
		// Top of PREROUTING, -j (not -g): ahead of the redirect capture's
		// exclude ACCEPT, and non-DNS packets continue through PREROUTING.
		"-t nat -I PREROUTING 1 -p udp -m conntrack ! --ctstate INVALID -j " + chainDNS,
		"-t nat -I PREROUTING 1 -p tcp -m conntrack ! --ctstate INVALID -j " + chainDNS,
	}
	for _, w := range want {
		if !hasCall(f.Calls, w) {
			t.Errorf("missing %q", w)
		}
	}
	if hasCall(f.Calls, "-g "+chainDNS) {
		t.Error("DNS jump must not use goto")
	}
}

func TestApplyDNSSkipsWhenDNSInDown(t *testing.T) {
	// Grab a free port, then close it: nothing listens there.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}}
	e := &Engine{Runner: f}
	if err := e.applyDNS(context.Background(), Config{Mode: ModeTProxy, DNSRedirectPort: port}); err != nil {
		t.Fatal(err)
	}
	if hasCall(f.Calls, "REDIRECT") || hasCall(f.Calls, "-I PREROUTING") {
		t.Errorf("no intercept expected while dns-in is down: %+v", f.Calls)
	}
	if !hasCall(f.Calls, "-t nat -D PREROUTING -p udp -m conntrack ! --ctstate INVALID -j "+chainDNS) {
		t.Error("expected the DNS jump to be removed")
	}
}

func TestApplyDNSOffDropsChain(t *testing.T) {
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}}
	e := &Engine{Runner: f}
	if err := e.applyDNS(context.Background(), Config{Mode: ModeRedirect}); err != nil {
		t.Fatal(err)
	}
	if !hasCall(f.Calls, "-t nat -X "+chainDNS) {
		t.Error("expected DNS chain removal when intercept is off")
	}
}

func TestTablesIncludeNatForTProxyDNS(t *testing.T) {
	if (Config{Mode: ModeTProxy}).usesTable("nat") {
		t.Error("tproxy without DNS intercept must not touch nat")
	}
	if !(Config{Mode: ModeTProxy, DNSRedirectPort: 1053}).usesTable("nat") {
		t.Error("tproxy with DNS intercept must apply nat")
	}
}

func TestCaptureInstalledChecksDNSJump(t *testing.T) {
	port := listenPort(t)
	// -C of the tproxy jump succeeds, the DNS one fails.
	e := &Engine{Runner: &dnsMissingRunner{&cmdrun.Fake{}}}
	if e.CaptureInstalled(context.Background(), Config{Mode: ModeTProxy, TProxyPort: 2080, DNSRedirectPort: port}) {
		t.Error("missing DNS jump must report not installed")
	}
}

// dnsMissingRunner fails only the `-C` probe for the DNS jump.
type dnsMissingRunner struct{ *cmdrun.Fake }

func (r *dnsMissingRunner) Run(ctx context.Context, name string, args ...string) (cmdrun.Result, error) {
	res, err := r.Fake.Run(ctx, name, args...)
	for _, a := range args {
		if a == chainDNS {
			return res, errStub
		}
	}
	return res, err
}
