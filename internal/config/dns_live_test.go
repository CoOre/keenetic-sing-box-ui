//go:build live

// Live checks against the real public resolvers (network required):
//
//	SINGBOX_BIN=/path/to/sing-box-1.14 LIVE_PROXY=host:port go test -tags live -run Live -v ./internal/config
//
// LIVE_PROXY is an HTTP proxy used as the "proxy" outbound for presets marked
// Proxy (blocked on direct paths); without it those presets are skipped.
package config

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startDNS runs sing-box on the assembled config and returns a resolver bound
// to its dns-in inbound.
func startDNS(t *testing.T, bin string, o DNSOptions) *net.Resolver {
	t.Helper()
	var outs []ProxyOutbound
	if hp := os.Getenv("LIVE_PROXY"); hp != "" {
		host, port, _ := net.SplitHostPort(hp)
		n, _ := strconv.Atoi(port)
		outs = append(outs, ProxyOutbound{Tag: "live-proxy", Object: map[string]any{"type": "http", "server": host, "server_port": n}})
	}
	return startDNSWith(t, bin, o, outs)
}

func startDNSWith(t *testing.T, bin string, o DNSOptions, outs []ProxyOutbound) *net.Resolver {
	t.Helper()
	dir := t.TempDir()
	o.Port = freePort(t)
	body, err := Assemble(AssembleOptions{
		DefaultOptions: DefaultOptions{LogPath: dir + "/sb.log", CachePath: dir + "/cache.db", ClashAddr: "127.0.0.1:" + strconv.Itoa(freePort(t))},
		InboundMode:    InboundSocks,
		InboundPort:    freePort(t),
		DNS:            o,
	}, outs)
	if err != nil {
		t.Fatal(err)
	}
	cfg := dir + "/config.json"
	if err := os.WriteFile(cfg, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", cfg)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr := "127.0.0.1:" + strconv.Itoa(o.Port)
	for i := 0; i < 40; i++ {
		if c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
}

func lookup(r *net.Resolver, host string) ([]net.IP, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	ips, err := r.LookupIP(ctx, "ip4", host)
	return ips, time.Since(start), err
}

// blocked reports whether an ad-blocker answered a sinkholed domain: no
// address, NXDOMAIN, or the 0.0.0.0 / 94.140.14.33-style sinkhole.
func blocked(ips []net.IP, err error) bool {
	if err != nil || len(ips) == 0 {
		return true
	}
	for _, ip := range ips {
		if !ip.IsUnspecified() && !ip.IsLoopback() {
			return false
		}
	}
	return true
}

func TestLiveDNSPresets(t *testing.T) {
	bin := singBoxBin(t, 14)
	for _, p := range DNSPresets {
		if strings.HasPrefix(p.Address, "dhcp") {
			continue
		}
		t.Run(p.Tag, func(t *testing.T) {
			t.Parallel()
			detour := "direct"
			if p.Proxy {
				if os.Getenv("LIVE_PROXY") == "" {
					t.Skip("blocked directly; set LIVE_PROXY")
				}
				detour = "proxy"
			}
			r := startDNS(t, bin, DNSOptions{Servers: []DNSServer{{Tag: p.Tag, Address: p.Address, Detour: detour}}, Final: p.Tag})
			ips, took, err := lookup(r, "example.com")
			if err != nil || len(ips) == 0 {
				t.Fatalf("%s (%s): example.com: %v", p.Name, p.Address, err)
			}
			msg := "ok " + took.Round(time.Millisecond).String() + " (" + detour + ")"
			if p.Category == DNSCatAds {
				ips, err := func() ([]net.IP, error) { ips, _, err := lookup(r, "doubleclick.net"); return ips, err }()
				if !blocked(ips, err) {
					t.Errorf("%s: doubleclick.net not blocked: %v", p.Name, ips)
				}
				msg += ", реклама блокируется"
			}
			t.Logf("%s (%s): %s", p.Name, p.Address, msg)
		})
	}
}

func TestLiveDNSFailover(t *testing.T) {
	bin := singBoxBin(t, 14)
	r := startDNS(t, bin, DNSOptions{
		Servers: []DNSServer{
			{Tag: "dead1", Address: "tls://192.0.2.1"},
			{Tag: "dead2", Address: "tls://192.0.2.2"},
			{Tag: "cf", Address: "https://1.1.1.1/dns-query"},
		},
		Final: "dead1", FinalBackups: []string{"dead2", "cf"},
		FailoverTimeout: 1,
	})
	ips, took, err := lookup(r, "example.com")
	if err != nil || len(ips) == 0 {
		t.Fatalf("failover lookup: %v", err)
	}
	if took < 2*time.Second {
		t.Errorf("expected both dead servers to be tried first, took %v", took)
	}
	t.Logf("dead → dead → cf: %v in %v", ips, took)
}
