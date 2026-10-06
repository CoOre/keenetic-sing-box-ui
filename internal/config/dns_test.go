package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseDNSAddress(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]any
	}{
		{"8.8.8.8", map[string]any{"type": "udp", "server": "8.8.8.8"}},
		{"8.8.8.8:5353", map[string]any{"type": "udp", "server": "8.8.8.8", "server_port": 5353}},
		{"[2001:db8::1]:53", map[string]any{"type": "udp", "server": "2001:db8::1"}},
		{"tcp://1.1.1.1", map[string]any{"type": "tcp", "server": "1.1.1.1"}},
		{"tls://1.1.1.1:853", map[string]any{"type": "tls", "server": "1.1.1.1"}},
		{"tls://dns.google", map[string]any{"type": "tls", "server": "dns.google", "domain_resolver": "local"}},
		{"https://1.1.1.1/dns-query", map[string]any{"type": "https", "server": "1.1.1.1"}},
		{"https://dns.example:8443/custom", map[string]any{"type": "https", "server": "dns.example", "server_port": 8443, "path": "/custom", "domain_resolver": "local"}},
		{"h3://cloudflare-dns.com/dns-query", map[string]any{"type": "h3", "server": "cloudflare-dns.com", "domain_resolver": "local"}},
		{"quic://dns.adguard-dns.com", map[string]any{"type": "quic", "server": "dns.adguard-dns.com", "domain_resolver": "local"}},
		{"dhcp://auto", map[string]any{"type": "dhcp"}},
		{"dhcp://eth3", map[string]any{"type": "dhcp", "interface": "eth3"}},
		{"local", map[string]any{"type": "local"}},
	}
	for _, c := range cases {
		got, err := ParseDNSAddress(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "tls://", "udp://1.1.1.1:0", "tls://1.1.1.1/path", "udp://1.1.1.1:99999"} {
		if _, err := ParseDNSAddress(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestDNSValidate(t *testing.T) {
	ok := DNSOptions{
		Servers: []DNSServer{
			{Tag: "cf", Address: "https://1.1.1.1/dns-query"},
			{Tag: "ag", Address: "quic://dns.adguard-dns.com", Detour: "proxy"},
		},
		Final:         "cf",
		ProxiedServer: "ag",
		Rules:         []DNSRule{{Domains: []string{"example.com"}, Server: "local"}},
	}
	if err := ok.Validate(2080); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
	bad := map[string]func(o *DNSOptions){
		"no servers":       func(o *DNSOptions) { o.Servers = nil; o.Final = "" },
		"reserved tag":     func(o *DNSOptions) { o.Servers[0].Tag = "local" },
		"bad tag":          func(o *DNSOptions) { o.Servers[0].Tag = "Bad Tag" },
		"dup tag":          func(o *DNSOptions) { o.Servers[1].Tag = "cf" },
		"bad address":      func(o *DNSOptions) { o.Servers[0].Address = "ftp://x" },
		"bad detour":       func(o *DNSOptions) { o.Servers[0].Detour = "auto" },
		"dangling final":   func(o *DNSOptions) { o.Final = "nope" },
		"dangling proxied": func(o *DNSOptions) { o.ProxiedServer = "nope" },
		"dangling rule":    func(o *DNSOptions) { o.Rules[0].Server = "nope" },
		"empty rule":       func(o *DNSOptions) { o.Rules[0].Domains = []string{" ", "1.2.3.4"} },
		"bad strategy":     func(o *DNSOptions) { o.Strategy = "ipv5" },
		"port 53":          func(o *DNSOptions) { o.Port = 53 },
		"inbound port":     func(o *DNSOptions) { o.Port = 2080 },
		"loopback port":    func(o *DNSOptions) { o.Port = 2081 },
	}
	for name, mut := range bad {
		o := ok
		o.Servers = append([]DNSServer{}, ok.Servers...)
		o.Rules = []DNSRule{{Domains: []string{"example.com"}, Server: "local"}}
		mut(&o)
		if err := o.Validate(2080); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestDNSValidateLocalOnly(t *testing.T) {
	// The router's own resolver as the only server is a valid choice.
	if err := (DNSOptions{Final: "local"}).Validate(2080); err != nil {
		t.Errorf("local-only rejected: %v", err)
	}
	o := DNSOptions{Final: "local"}
	o.Normalize()
	if o.Final != "local" {
		t.Errorf("normalize changed final: %+v", o)
	}
}

func TestDNSNormalizeRepointsDangling(t *testing.T) {
	o := DNSOptions{Servers: []DNSServer{{Tag: "cf", Address: "1.1.1.1"}}, Final: "google", ProxiedServer: "gone"}
	o.Normalize()
	if o.Final != "cf" || o.ProxiedServer != "" || o.Servers[0].Detour != "direct" || o.Port != DefaultDNSPort || o.Strategy != "ipv4_only" {
		t.Errorf("normalize: %+v", o)
	}
}

func assembleCfg(t *testing.T, opts AssembleOptions) map[string]any {
	t.Helper()
	body, err := Assemble(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestAssembleDNSDefaultMatchesLegacy(t *testing.T) {
	cfg := assembleCfg(t, AssembleOptions{InboundMode: InboundTProxy})
	dns := cfg["dns"].(map[string]any)
	servers := dns["servers"].([]any)
	if len(servers) != 2 {
		t.Fatalf("servers: %v", servers)
	}
	g := servers[0].(map[string]any)
	if g["type"] != "tls" || g["server"] != "8.8.8.8" || g["tag"] != "google" {
		t.Errorf("google: %v", g)
	}
	if servers[1].(map[string]any)["type"] != "local" {
		t.Errorf("local: %v", servers[1])
	}
	if dns["final"] != "google" || dns["strategy"] != "ipv4_only" || len(userDNSRules(dns["rules"])) != 0 {
		t.Errorf("dns: %v", dns)
	}
	// Probe rules: local, then google, on consecutive loopback source ports.
	if got := DNSProbePorts(mustMarshal(t, cfg)); got["local"] != DNSProbeBasePort || got["google"] != DNSProbeBasePort+1 {
		t.Errorf("probe ports: %v", got)
	}
	// dns-in on loopback, hijacked by the first route rule.
	var dnsIn map[string]any
	for _, in := range cfg["inbounds"].([]any) {
		if m := in.(map[string]any); m["tag"] == DNSInboundTag {
			dnsIn = m
		}
	}
	if dnsIn == nil || dnsIn["listen"] != "127.0.0.1" || dnsIn["listen_port"] != float64(DefaultDNSPort) {
		t.Errorf("dns-in: %v", dnsIn)
	}
	first := cfg["route"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if first["action"] != "hijack-dns" || first["inbound"] == nil {
		t.Errorf("first rule: %v", first)
	}
}

func TestAssembleDNSRulesAndIntercept(t *testing.T) {
	cfg := assembleCfg(t, AssembleOptions{
		InboundMode:       InboundTProxy,
		RouteDomains:      []string{"YouTube.com", "1.2.3.4", "# c"},
		ExtraRouteDomains: []string{"*.googlevideo.com", "youtube.com"},
		DNS: DNSOptions{
			Servers: []DNSServer{
				{Tag: "cf", Address: "https://1.1.1.1/dns-query"},
				{Tag: "ag", Address: "quic://dns.adguard-dns.com", Detour: "proxy"},
			},
			Final:            "cf",
			ProxiedServer:    "ag",
			Rules:            []DNSRule{{Domains: []string{"corp.lan"}, Server: "local"}},
			InterceptClients: true,
			Port:             5300,
		},
	})
	dns := cfg["dns"].(map[string]any)
	rules := userDNSRules(dns["rules"])
	if len(rules) != 2 {
		t.Fatalf("rules: %v", rules)
	}
	manual, preset := rules[0].(map[string]any), rules[1].(map[string]any)
	if manual["server"] != "local" || preset["server"] != "ag" {
		t.Errorf("rule order: %v", rules)
	}
	got := preset["domain_suffix"].([]any)
	if len(got) != 2 || got[0] != "youtube.com" || got[1] != "googlevideo.com" {
		t.Errorf("preset domains: %v", got)
	}
	for _, s := range dns["servers"].([]any) {
		m := s.(map[string]any)
		switch m["tag"] {
		case "ag":
			if m["detour"] != "proxy" {
				t.Errorf("ag detour: %v", m)
			}
		case "cf":
			if _, ok := m["detour"]; ok {
				t.Errorf("cf detour: %v", m)
			}
		}
	}
	for _, in := range cfg["inbounds"].([]any) {
		if m := in.(map[string]any); m["tag"] == DNSInboundTag && (m["listen"] != "0.0.0.0" || m["listen_port"] != float64(5300)) {
			t.Errorf("intercept dns-in: %v", m)
		}
	}
}

// TestAssembleDNSPresetsSingBoxCheck validates every preset (and both
// detours) with a real `sing-box check` when the binary is available.
func TestAssembleDNSPresetsSingBoxCheck(t *testing.T) {
	bin := singBoxBin(t, 14)
	o := DNSOptions{Strategy: "prefer_ipv4", InterceptClients: true}
	for i, p := range DNSPresets {
		detour := "direct"
		if i%2 == 1 {
			detour = "proxy"
		}
		o.Servers = append(o.Servers, DNSServer{Tag: p.Tag, Address: p.Address, Detour: detour})
	}
	o.Final = o.Servers[0].Tag
	o.ProxiedServer = "adguard-doq"
	o.Rules = []DNSRule{{Domains: []string{"lan"}, Server: "local"}}
	if err := o.Validate(2080); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{InboundTProxy, InboundRedirect, InboundSocks, InboundTun} {
		body, err := Assemble(AssembleOptions{InboundMode: mode, RouteDomains: []string{"example.com"}, DNS: o}, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := NewChecker(bin).CheckContent(context.Background(), body)
		if err != nil {
			t.Fatal(err)
		}
		if !res.OK {
			t.Errorf("%s: sing-box check failed: %s", mode, strings.Join(res.Errors, "; ")+res.Stderr)
		}
	}
}

// TestAssembleDNSSingBoxStarts actually starts sing-box on the assembled
// config for a moment: `check` misses start-time validation (e.g. a DNS
// server with detour to a plain direct outbound passes check but is FATAL on
// start in 1.13).
func TestAssembleDNSSingBoxStarts(t *testing.T) {
	bin := singBoxBin(t, 14)
	dir := t.TempDir()
	o := DNSOptions{
		Servers: []DNSServer{
			{Tag: "plain", Address: "1.1.1.1"},
			{Tag: "dot", Address: "tls://1.1.1.1"},
			{Tag: "doh", Address: "https://1.1.1.1/dns-query", Detour: "proxy"},
			{Tag: "doq", Address: "quic://dns.adguard-dns.com"},
			{Tag: "h3", Address: "h3://cloudflare-dns.com/dns-query", Detour: "proxy"},
		},
		Final:         "dot",
		ProxiedServer: "doh",
		Port:          freePort(t),
	}
	assertStaysUp(t, bin, dir, o)
}

// TestAssembleDNSFailoverSingBoxStarts starts sing-box ≥ 1.14 on a config
// with failover chains on every kind of target.
func TestAssembleDNSFailoverSingBoxStarts(t *testing.T) {
	bin := singBoxBin(t, 14)
	o := DNSOptions{
		Servers: []DNSServer{
			{Tag: "a", Address: "tls://1.1.1.1"},
			{Tag: "b", Address: "https://8.8.8.8/dns-query", Detour: "proxy"},
			{Tag: "c", Address: "quic://dns.adguard-dns.com"},
		},
		Final: "a", FinalBackups: []string{"b", "local"},
		ProxiedServer: "b", ProxiedBackups: []string{"c", "a"},
		Rules: []DNSRule{{Domains: []string{"lan"}, Server: "local", Backups: []string{"a"}}},
		Port:  freePort(t),
	}
	assertStaysUp(t, bin, t.TempDir(), o)
}

func assertStaysUp(t *testing.T, bin, dir string, o DNSOptions) {
	t.Helper()
	body, err := Assemble(AssembleOptions{
		DefaultOptions: DefaultOptions{LogPath: dir + "/sb.log", CachePath: dir + "/cache.db", ClashAddr: "127.0.0.1:" + strconv.Itoa(freePort(t))},
		InboundMode:    InboundSocks,
		InboundPort:    freePort(t),
		RouteDomains:   []string{"example.com"},
		DNS:            o,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := dir + "/config.json"
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "run", "-c", cfgPath).CombinedOutput()
	logBody, _ := os.ReadFile(dir + "/sb.log")
	all := string(out) + string(logBody)
	if ctx.Err() == nil || strings.Contains(all, "FATAL") {
		t.Fatalf("sing-box did not stay up:\n%s", all)
	}
}

// singBoxBin returns a sing-box binary of at least version 1.<minor>: the
// SINGBOX_BIN env var, else the one on PATH. Skips the test otherwise.
func singBoxBin(t *testing.T, minor int) string {
	t.Helper()
	bin := os.Getenv("SINGBOX_BIN")
	if bin == "" {
		var err error
		if bin, err = exec.LookPath("sing-box"); err != nil {
			t.Skip("sing-box not installed (set SINGBOX_BIN)")
		}
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Skipf("sing-box version: %v", err)
	}
	var maj, min int
	if _, err := fmt.Sscanf(string(out), "sing-box version %d.%d", &maj, &min); err != nil || maj != 1 || min < minor {
		t.Skipf("need sing-box ≥ 1.%d, have %q (set SINGBOX_BIN)", minor, strings.SplitN(string(out), "\n", 2)[0])
	}
	return bin
}

func TestAssembleDNSFailoverRules(t *testing.T) {
	cfg := assembleCfg(t, AssembleOptions{
		InboundMode:  InboundTProxy,
		RouteDomains: []string{"youtube.com"},
		DNS: DNSOptions{
			Servers: []DNSServer{
				{Tag: "a", Address: "tls://1.1.1.1"},
				{Tag: "b", Address: "tls://8.8.8.8"},
			},
			Final: "a", FinalBackups: []string{"b"},
			ProxiedServer: "b", ProxiedBackups: []string{"a", "local"},
			FailoverTimeout: 3,
		},
	})
	rules := userDNSRules(cfg["dns"].(map[string]any)["rules"])
	var got []string
	for _, r := range rules {
		m := r.(map[string]any)
		desc := m["action"].(string)
		if s, ok := m["server"]; ok {
			desc += ":" + s.(string)
		}
		if mr, ok := m["match_response"]; ok {
			desc += "@" + mr.(string) + "=" + m["response_rcode"].(string)
		}
		if rs, ok := m["rule_set"]; ok {
			desc += "[" + rs.([]any)[0].(string) + "]"
		}
		if _, ok := m["domain_suffix"]; ok {
			t.Errorf("chain rule must reference the rule-set, not inline domains: %v", m)
		}
		if m["action"] == "evaluate" && m["timeout"] != "3s" {
			t.Errorf("evaluate timeout: %v", m)
		}
		got = append(got, desc)
	}
	want := []string{
		"evaluate:b[ksbui-dns-proxied]", "respond@proxied-0=NOERROR", "respond@proxied-0=NXDOMAIN",
		"evaluate:a[ksbui-dns-proxied]", "respond@proxied-1=NOERROR", "respond@proxied-1=NXDOMAIN",
		"route:local[ksbui-dns-proxied]",
		"evaluate:a", "respond@final-0=NOERROR", "respond@final-0=NXDOMAIN",
		"route:b",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("rules:\n got  %v\n want %v", got, want)
	}
	sets := cfg["route"].(map[string]any)["rule_set"].([]any)
	if len(sets) != 1 || sets[0].(map[string]any)["tag"] != "ksbui-dns-proxied" {
		t.Errorf("rule sets: %v", sets)
	}
}

// userDNSRules drops the lookup probe rules (source_port) from a dns.rules value.
func userDNSRules(v any) []any {
	var out []any
	rules, _ := v.([]any)
	for _, r := range rules {
		if _, probe := r.(map[string]any)["source_port"]; !probe {
			out = append(out, r)
		}
	}
	return out
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDNSChainFor(t *testing.T) {
	o := DNSOptions{
		Servers: []DNSServer{{Tag: "a", Address: "1.1.1.1"}, {Tag: "b", Address: "8.8.8.8"}},
		Final:   "a", FinalBackups: []string{"b"},
		ProxiedServer: "b",
		Rules:         []DNSRule{{Domains: []string{"corp.lan"}, Server: "local"}},
	}
	route := []string{"youtube.com"}
	for domain, want := range map[string]string{
		"x.corp.lan":       "Правило 1:local",
		"www.youtube.com.": "Заблокированные сайты:b",
		"youtube.com":      "Заблокированные сайты:b",
		"notyoutube.com":   "Основной DNS:a,b",
		"example.com":      "Основной DNS:a,b",
	} {
		name, chain := o.ChainFor(domain, route)
		if got := name + ":" + strings.Join(chain, ","); got != want {
			t.Errorf("%s: got %s want %s", domain, got, want)
		}
	}
}

func TestDNSProbePortReserved(t *testing.T) {
	o := DNSOptions{Servers: []DNSServer{{Tag: "a", Address: "1.1.1.1"}}, Final: "a", Port: DNSProbeBasePort + 3}
	if err := o.Validate(2080); err == nil {
		t.Error("dns-in port inside the probe range must be rejected")
	}
}

func TestDNSBackupsValidateAndNormalize(t *testing.T) {
	base := DNSOptions{Servers: []DNSServer{{Tag: "a", Address: "1.1.1.1"}, {Tag: "b", Address: "8.8.8.8"}}, Final: "a"}
	for name, mut := range map[string]func(o *DNSOptions){
		"unknown backup":      func(o *DNSOptions) { o.FinalBackups = []string{"zz"} },
		"primary as backup":   func(o *DNSOptions) { o.FinalBackups = []string{"a"} },
		"dup backup":          func(o *DNSOptions) { o.FinalBackups = []string{"b", "b"} },
		"proxied w/o primary": func(o *DNSOptions) { o.ProxiedBackups = []string{"b"} },
		"rule unknown backup": func(o *DNSOptions) {
			o.Rules = []DNSRule{{Domains: []string{"x.com"}, Server: "a", Backups: []string{"zz"}}}
		},
		"bad timeout": func(o *DNSOptions) { o.FailoverTimeout = 99 },
	} {
		o := base
		mut(&o)
		if err := o.Validate(2080); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	ok := base
	ok.FinalBackups = []string{"b", "local"}
	if err := ok.Validate(2080); err != nil {
		t.Errorf("valid chain rejected: %v", err)
	}
	if !ok.HasFailover() || base.HasFailover() {
		t.Error("HasFailover")
	}
	n := base
	n.FinalBackups = []string{"a", "gone", "b", "b"}
	n.Normalize()
	if strings.Join(n.FinalBackups, ",") != "b" || n.FailoverTimeout != DefaultDNSFailoverTimeout {
		t.Errorf("normalize: %+v", n)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
