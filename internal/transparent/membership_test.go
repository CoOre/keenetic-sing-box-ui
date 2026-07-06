package transparent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// setRunner answers `ipset test <set> <ip>` per set name; other calls succeed.
type setRunner struct {
	bySet map[string]cmdrun.FakeResponse
}

func (r *setRunner) Run(_ context.Context, name string, args ...string) (cmdrun.Result, error) {
	if strings.HasSuffix(name, "ipset") && len(args) >= 2 && args[0] == "test" {
		resp := r.bySet[args[1]]
		return cmdrun.Result{Stdout: []byte(resp.Stdout), Stderr: []byte(resp.Stderr)}, resp.Err
	}
	return cmdrun.Result{}, nil
}

func TestSetMembershipParsing(t *testing.T) {
	exit1 := errors.New("exit status 1")
	e := &Engine{Runner: &setRunner{bySet: map[string]cmdrun.FakeResponse{
		// member: exit 0
		routeSetV4(): {Stdout: "1.2.3.4 is in set " + routeSetV4() + "."},
		// clean negative: exit 1 + "is NOT in set"
		excludeSetV4(): {Stderr: "1.2.3.4 is NOT in set " + excludeSetV4() + ".", Err: exit1},
		// real failure: set doesn't exist
		rejectSetV4(): {Stderr: "ipset v7.11: The set with the given name does not exist", Err: exit1},
	}}}

	m := e.TestSetMembership(context.Background(), "1.2.3.4")
	if !m.Route {
		t.Errorf("route: want member, got %+v", m)
	}
	if m.Exclude {
		t.Errorf("exclude: want clean negative, got %+v", m)
	}
	if m.Reject {
		t.Errorf("reject: unknown set must not read as member: %+v", m)
	}
	if !strings.Contains(m.Err, rejectSetV4()) || strings.Contains(m.Err, excludeSetV4()) {
		t.Errorf("err must name only the failed set: %q", m.Err)
	}
}
