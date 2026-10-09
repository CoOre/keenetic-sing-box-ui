// Package proxyretry runs an HTTP operation directly and, when that fails,
// once more through the local sing-box proxy. GitHub and subscription hosts
// are commonly interfered with, and the router's own egress is not captured
// by the transparent firewall rules — so the retry explicitly tunnels.
package proxyretry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type permanent struct{ err error }

func (p permanent) Error() string { return p.err.Error() }
func (p permanent) Unwrap() error { return p.err }

// Permanent marks err as one the proxy can't fix (a response that arrived
// but is unusable: 404, unparsable, too large) — Do then skips the retry.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanent{err}
}

// Do calls fn with direct and, unless that succeeds or fails permanently,
// with proxy() (nil func or nil client = no proxy available). timeout > 0
// bounds each attempt separately (DPI tends to stall connections rather than
// reset them). onRetry, if set, sees the direct error before the retry.
// via is "direct" or "proxy"; a permanent error is returned unwrapped.
func Do(ctx context.Context, timeout time.Duration, direct *http.Client, proxy func() *http.Client,
	onRetry func(error), fn func(context.Context, *http.Client) error) (via string, err error) {
	attempt := func(c *http.Client) error {
		actx := ctx
		if timeout > 0 {
			var cancel context.CancelFunc
			actx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return fn(actx, c)
	}

	err = attempt(direct)
	if err == nil {
		return "direct", nil
	}
	var p permanent
	if errors.As(err, &p) {
		return "", p.err
	}
	var pc *http.Client
	if proxy != nil {
		pc = proxy()
	}
	if pc == nil {
		return "", err
	}
	if onRetry != nil {
		onRetry(err)
	}
	if perr := attempt(pc); perr != nil {
		if errors.As(perr, &p) {
			perr = p.err
		}
		return "", fmt.Errorf("напрямую: %v; через прокси: %w", err, perr)
	}
	return "proxy", nil
}
