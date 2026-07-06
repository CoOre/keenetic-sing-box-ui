package transparent

import (
	"context"
	"strings"
)

// SetMembership is the live ipset verdict for one destination IP: which of our
// sets it belongs to right now. Err is set when a set couldn't be tested at
// all (ipset missing, set not created — e.g. transparent mode is off); a clean
// "not in set" is a false, not an error.
type SetMembership struct {
	Route   bool   `json:"route"`
	Exclude bool   `json:"exclude"`
	Reject  bool   `json:"reject"`
	Err     string `json:"err,omitempty"`
}

// TestSetMembership runs `ipset test` for ip against the route, exclude and
// reject sets. Read-only; used by the route-trace diagnostic to answer "is
// this destination captured right now" with the kernel's own data rather than
// re-deriving it from settings.
func (e *Engine) TestSetMembership(ctx context.Context, ip string) SetMembership {
	var m SetMembership
	var errs []string
	test := func(set string) bool {
		out, err := run(ctx, e.Runner, "ipset", "test", set, ip)
		if err == nil {
			return true
		}
		// `ipset test` exits 1 both for "not in set" and for real failures
		// (unknown set); only the former prints "is NOT in set".
		if strings.Contains(out, "NOT in set") {
			return false
		}
		if out == "" {
			out = err.Error()
		}
		errs = append(errs, set+": "+out)
		return false
	}
	m.Route = test(routeSetV4())
	m.Exclude = test(excludeSetV4())
	m.Reject = test(rejectSetV4())
	m.Err = strings.Join(errs, "; ")
	return m
}
