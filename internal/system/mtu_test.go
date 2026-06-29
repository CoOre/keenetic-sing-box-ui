package system

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

var errStub = errors.New("stub")

func callLine(c cmdrun.FakeCall) string { return c.Name + " " + strings.Join(c.Args, " ") }

func hasCall(calls []cmdrun.FakeCall, substr string) bool {
	for _, c := range calls {
		if strings.Contains(callLine(c), substr) {
			return true
		}
	}
	return false
}

func TestSetMSSClamp_Rules(t *testing.T) {
	// Default error makes -C/-N/-D report "absent/fail", so the add + jump paths
	// run and the Clear delete loop terminates. Calls are recorded regardless.
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}}
	if err := SetMSSClamp(context.Background(), f, "144.31.159.202", 1388); err != nil {
		// add returns the stub err for the last -A; that's fine, rules still recorded
		_ = err
	}
	want := []string{
		"-t mangle -A " + mssChain + " -p tcp -d 144.31.159.202 -m tcp --tcp-flags SYN,RST SYN -j TCPMSS --set-mss 1388",
		"-t mangle -A " + mssChain + " -p tcp -s 144.31.159.202 -m tcp --tcp-flags SYN,RST SYN -j TCPMSS --set-mss 1388",
		"-t mangle -A OUTPUT -j " + mssChain,
		"-t mangle -A FORWARD -j " + mssChain,
	}
	for _, wnt := range want {
		if !hasCall(f.Calls, wnt) {
			t.Errorf("missing iptables call: %q", wnt)
		}
	}
}

func TestSetMSSClamp_RejectsBadInput(t *testing.T) {
	f := &cmdrun.Fake{}
	if err := SetMSSClamp(context.Background(), f, "not-an-ip", 1388); err == nil {
		t.Errorf("expected error for bad ip")
	}
	if err := SetMSSClamp(context.Background(), f, "1.2.3.4", 0); err == nil {
		t.Errorf("expected error for bad mss")
	}
}

func TestClearMSSClamp_FlushesAndDrops(t *testing.T) {
	f := &cmdrun.Fake{Default: cmdrun.FakeResponse{Err: errStub}} // -D fails -> loop exits
	ClearMSSClamp(context.Background(), f)
	for _, wnt := range []string{
		"-t mangle -D OUTPUT -j " + mssChain,
		"-t mangle -D FORWARD -j " + mssChain,
		"-t mangle -F " + mssChain,
		"-t mangle -X " + mssChain,
	} {
		if !hasCall(f.Calls, wnt) {
			t.Errorf("missing iptables call: %q", wnt)
		}
	}
}
