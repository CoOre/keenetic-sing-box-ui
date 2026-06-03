package transparent

import (
	"context"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// restorePayload returns the stdin of the single `ipset restore` call, or "".
func restorePayload(calls []cmdrun.FakeCall) string {
	for _, c := range calls {
		if strings.HasSuffix(c.Name, "ipset") && len(c.Args) > 0 && c.Args[0] == "restore" {
			return c.Stdin
		}
	}
	return ""
}

// RebuildRouteCIDRSet must load the set in ONE `ipset restore` batch (not one
// subprocess per entry, which pegged the router CPU), and swap atomically so
// the live set is never empty mid-update.
func TestRebuildRouteCIDRSetBatchAtomic(t *testing.T) {
	f := &cmdrun.Fake{}
	e := &Engine{Runner: f}
	set := routeSetV4()
	tmp := set + "_tmp"

	if err := e.RebuildRouteCIDRSet(context.Background(), []string{"104.18.32.47", "1.1.1.1/32", "bad", "104.18.32.47"}); err != nil {
		t.Fatalf("RebuildRouteCIDRSet: %v", err)
	}

	// Exactly one restore call carries the whole batch.
	restores := 0
	for _, c := range f.Calls {
		if strings.HasSuffix(c.Name, "ipset") && len(c.Args) > 0 && c.Args[0] == "restore" {
			restores++
		}
		// No per-entry `ipset add` subprocesses.
		if strings.HasSuffix(c.Name, "ipset") && len(c.Args) > 0 && c.Args[0] == "add" {
			t.Errorf("must not add entries via per-entry subprocess: %v", c.Args)
		}
	}
	if restores != 1 {
		t.Fatalf("expected exactly 1 ipset restore call, got %d", restores)
	}

	p := restorePayload(f.Calls)
	wantLines := []string{
		"create " + tmp + " hash:net family inet -exist",
		"add " + tmp + " 104.18.32.47/32", // bare IP normalized to /32
		"add " + tmp + " 1.1.1.1/32",
		"swap " + tmp + " " + set, // atomic swap
		"destroy " + tmp,
	}
	for _, w := range wantLines {
		if !strings.Contains(p, w+"\n") {
			t.Errorf("restore payload missing %q\n---\n%s", w, p)
		}
	}
	if strings.Contains(p, "bad") {
		t.Errorf("invalid entry must be dropped from payload:\n%s", p)
	}
	// Entries go to the temp set, never the live set directly (no empty window).
	if strings.Contains(p, "add "+set+" ") {
		t.Errorf("entries must target temp set, not live set:\n%s", p)
	}
	if strings.Contains(p, "flush "+set+"\n") {
		t.Errorf("live set must never be flushed (leak window):\n%s", p)
	}
}

// seedRouteSet adds entries additively (-exist) via restore, without flushing,
// so reviving the firewall never wipes resolver-added IPs.
func TestSeedRouteSetIsAdditive(t *testing.T) {
	f := &cmdrun.Fake{}
	e := &Engine{Runner: f}
	set := routeSetV4()

	e.seedRouteSet(context.Background(), []string{"203.0.113.0/24"})

	p := restorePayload(f.Calls)
	if !strings.Contains(p, "add "+set+" 203.0.113.0/24 -exist\n") {
		t.Errorf("seed should add entry additively to the live set:\n%s", p)
	}
	if strings.Contains(p, "flush "+set) || strings.Contains(p, "swap ") {
		t.Errorf("seed must not flush/swap the live set:\n%s", p)
	}
}
