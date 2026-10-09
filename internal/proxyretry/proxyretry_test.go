package proxyretry

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

var (
	direct  = &http.Client{}
	proxied = &http.Client{}
)

func TestDo_DirectOK(t *testing.T) {
	calls := 0
	via, err := Do(context.Background(), 0, direct, func() *http.Client { return proxied }, nil,
		func(context.Context, *http.Client) error { calls++; return nil })
	if err != nil || via != "direct" || calls != 1 {
		t.Errorf("via=%q err=%v calls=%d", via, err, calls)
	}
}

func TestDo_RetriesViaProxy(t *testing.T) {
	var seen []*http.Client
	var retried error
	via, err := Do(context.Background(), 0, direct, func() *http.Client { return proxied },
		func(err error) { retried = err },
		func(_ context.Context, c *http.Client) error {
			seen = append(seen, c)
			if c == direct {
				return errors.New("reset")
			}
			return nil
		})
	if err != nil || via != "proxy" || len(seen) != 2 || seen[1] != proxied || retried == nil {
		t.Errorf("via=%q err=%v seen=%d retried=%v", via, err, len(seen), retried)
	}
}

func TestDo_PermanentSkipsProxy(t *testing.T) {
	base := errors.New("status 404")
	calls := 0
	_, err := Do(context.Background(), 0, direct, func() *http.Client { return proxied }, nil,
		func(context.Context, *http.Client) error { calls++; return Permanent(base) })
	if calls != 1 || !errors.Is(err, base) || err.Error() != "status 404" {
		t.Errorf("calls=%d err=%v", calls, err)
	}
}

func TestDo_NoProxy(t *testing.T) {
	base := errors.New("reset")
	for _, proxy := range []func() *http.Client{nil, func() *http.Client { return nil }} {
		_, err := Do(context.Background(), 0, direct, proxy, nil,
			func(context.Context, *http.Client) error { return base })
		if err != base {
			t.Errorf("err=%v", err)
		}
	}
}

func TestDo_BothFail(t *testing.T) {
	_, err := Do(context.Background(), 0, direct, func() *http.Client { return proxied }, nil,
		func(_ context.Context, c *http.Client) error {
			if c == direct {
				return errors.New("reset")
			}
			return Permanent(errors.New("status 404"))
		})
	if err == nil || !strings.Contains(err.Error(), "напрямую: reset") || !strings.HasSuffix(err.Error(), "через прокси: status 404") {
		t.Errorf("err=%v", err)
	}
}

func TestDo_PerAttemptTimeout(t *testing.T) {
	start := time.Now()
	_, err := Do(context.Background(), 50*time.Millisecond, direct, func() *http.Client { return proxied }, nil,
		func(ctx context.Context, _ *http.Client) error { <-ctx.Done(); return ctx.Err() })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err=%v", err)
	}
	if d := time.Since(start); d < 100*time.Millisecond || d > 2*time.Second {
		t.Errorf("two bounded attempts took %v", d)
	}
}
