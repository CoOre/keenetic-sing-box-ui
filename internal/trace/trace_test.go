package trace

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/lists"
	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
)

func TestNormalizeTarget(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{in: "ChatGPT.com", want: "chatgpt.com"},
		{in: "  https://web.telegram.org/k/  ", want: "web.telegram.org"},
		{in: "http://1.2.3.4:8080/path?q=1", want: "1.2.3.4"},
		{in: "example.com:443", want: "example.com"},
		{in: "149.154.167.99", want: "149.154.167.99"},
		{in: "example.com.", want: "example.com"},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "bad target with spaces", wantErr: true},
	}
	for _, c := range cases {
		got, err := NormalizeTarget(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizeTarget(%q): want error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeTarget(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeTarget(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDomainMatches(t *testing.T) {
	tr := &Tracer{
		Settings: settings.Settings{
			RouteDomains: []string{"chatgpt.com", "Telegram.org", "# comment", ""},
		},
		Sources: []*lists.Source{
			{URL: "https://lists/x.lst", Enabled: true, Domains: []string{"openai.com"}},
			{URL: "https://lists/off.lst", Enabled: false, Domains: []string{"chatgpt.com"}},
		},
	}

	got := tr.domainMatches("chatgpt.com")
	if len(got) != 1 || got[0].Source != "route_domains" || got[0].Match != "exact" || !got[0].Effective {
		t.Fatalf("exact match: got %+v", got)
	}

	got = tr.domainMatches("web.telegram.org")
	if len(got) != 1 || got[0].Match != "suffix" || got[0].Effective {
		t.Fatalf("suffix match must be non-effective (resolver resolves only listed names): %+v", got)
	}
	if got[0].Entry != "telegram.org" {
		t.Fatalf("entry not normalized: %+v", got[0])
	}

	// URL-list domains are matched for attribution but never effective, and
	// disabled sources are skipped entirely.
	got = tr.domainMatches("api.openai.com")
	if len(got) != 1 || got[0].Source != "list" || got[0].Effective || got[0].ListURL != "https://lists/x.lst" {
		t.Fatalf("list match: got %+v", got)
	}
}

func TestCIDRMatches(t *testing.T) {
	tr := &Tracer{
		Settings: settings.Settings{
			RouteCIDR:   []string{"149.154.160.0/20", "1.2.3.4"},
			ExcludeCIDR: []string{"10.0.0.0/8"},
			RejectCIDR:  []string{"173.194.0.0/16"},
		},
		Sources: []*lists.Source{
			{URL: "https://lists/cidr.lst", Enabled: true, CIDRs: []string{"45.9.13.0/24"}},
		},
	}

	if got := tr.cidrMatches("149.154.167.99"); len(got) != 1 || got[0].Source != "route_cidr" || !got[0].Effective {
		t.Fatalf("route_cidr: %+v", got)
	}
	if got := tr.cidrMatches("1.2.3.4"); len(got) != 1 || got[0].Entry != "1.2.3.4" {
		t.Fatalf("bare-IP entry: %+v", got)
	}
	if got := tr.cidrMatches("10.1.2.3"); len(got) != 1 || got[0].Source != "exclude_cidr" {
		t.Fatalf("exclude_cidr: %+v", got)
	}
	if got := tr.cidrMatches("173.194.5.5"); len(got) != 1 || got[0].Source != "reject_cidr" {
		t.Fatalf("reject_cidr: %+v", got)
	}
	if got := tr.cidrMatches("45.9.13.188"); len(got) != 1 || got[0].Source != "list" || got[0].ListURL == "" {
		t.Fatalf("list cidr: %+v", got)
	}
	if got := tr.cidrMatches("8.8.8.8"); len(got) != 0 {
		t.Fatalf("no match expected: %+v", got)
	}
}

func TestVerdictStatic(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }
	cases := []struct {
		name    string
		mode    string
		matches []RuleMatch
		capture *bool
		want    string
	}{
		{name: "socks never captures", mode: "socks", want: "no_capture"},
		{name: "tun proxies everything", mode: "tun", want: "proxy"},
		{name: "route cidr -> proxy", mode: "tproxy",
			matches: []RuleMatch{{Source: "route_cidr", Effective: true}}, want: "proxy"},
		{name: "list cidr -> proxy", mode: "redirect",
			matches: []RuleMatch{{Source: "list", ListKind: "cidr", Effective: true}}, want: "proxy"},
		{name: "list domain not effective -> direct", mode: "tproxy",
			matches: []RuleMatch{{Source: "list", ListKind: "domain", Effective: false}}, want: "direct"},
		{name: "exclude beats route", mode: "tproxy",
			matches: []RuleMatch{
				{Source: "route_cidr", Effective: true},
				{Source: "exclude_cidr", Effective: true},
			}, want: "bypass"},
		{name: "reject beats exclude", mode: "tproxy",
			matches: []RuleMatch{
				{Source: "exclude_cidr", Effective: true},
				{Source: "reject_cidr", Effective: true},
			}, want: "reject"},
		{name: "route but capture stripped", mode: "tproxy",
			matches: []RuleMatch{{Source: "route_cidr", Effective: true}},
			capture: boolPtr(false), want: "capture_missing"},
		{name: "nothing matches -> direct", mode: "redirect", want: "direct"},
	}
	for _, c := range cases {
		tr := &Tracer{Settings: settings.Settings{InboundMode: c.mode}}
		got, src := tr.verdict(IPReport{Matches: c.matches}, c.capture)
		if got != c.want {
			t.Errorf("%s: verdict = %q, want %q", c.name, got, c.want)
		}
		if src != "static" {
			t.Errorf("%s: verdict source = %q, want static", c.name, src)
		}
	}
}

const sampleNFConntrack = `ipv4     2 tcp      6 3599 ESTABLISHED src=192.168.6.44 dst=149.154.167.99 sport=52034 dport=443 src=149.154.167.99 dst=100.64.1.2 sport=443 dport=52034 [ASSURED] mark=0 use=1
ipv4     2 tcp      6 117 TIME_WAIT src=192.168.6.44 dst=1.2.3.4 sport=41000 dport=443 src=192.168.6.1 dst=192.168.6.44 sport=1081 dport=41000 [ASSURED] mark=274 use=1
ipv4     2 udp      17 175 src=192.168.6.10 dst=8.8.8.8 sport=5353 dport=53 src=8.8.8.8 dst=100.64.1.2 sport=53 dport=5353 mark=0 use=1
garbage line without tuples
`

func TestReadConntrack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nf_conntrack")
	if err := os.WriteFile(path, []byte(sampleNFConntrack), 0o600); err != nil {
		t.Fatal(err)
	}

	flows, total, errStr := readConntrack([]string{path}, []string{"149.154.167.99", "1.2.3.4", "5.5.5.5"})
	if errStr != "" {
		t.Fatalf("unexpected error: %s", errStr)
	}
	if total["149.154.167.99"] != 1 || total["1.2.3.4"] != 1 || total["5.5.5.5"] != 0 {
		t.Fatalf("totals: %+v", total)
	}

	tg := flows["149.154.167.99"][0]
	if tg.Proto != "tcp" || tg.State != "ESTABLISHED" || tg.Src != "192.168.6.44" ||
		tg.SPort != 52034 || tg.DPort != 443 {
		t.Fatalf("telegram flow: %+v", tg)
	}
	if tg.Redirected {
		t.Fatalf("plain flow (reply src == orig dst) must not be redirected: %+v", tg)
	}

	rd := flows["1.2.3.4"][0]
	if !rd.Redirected {
		t.Fatalf("NAT'd flow (reply src != orig dst) must be redirected: %+v", rd)
	}
	if rd.Mark != "274" {
		t.Fatalf("mark: %+v", rd)
	}

	// Older ip_conntrack format (no address-family prefix).
	old := filepath.Join(dir, "ip_conntrack")
	if err := os.WriteFile(old, []byte("tcp      6 3599 ESTABLISHED src=192.168.6.44 dst=9.9.9.9 sport=1 dport=2 src=9.9.9.9 dst=192.168.6.44 sport=2 dport=1 mark=0 use=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flows, total, errStr = readConntrack([]string{filepath.Join(dir, "missing"), old}, []string{"9.9.9.9"})
	if errStr != "" || total["9.9.9.9"] != 1 || flows["9.9.9.9"][0].Proto != "tcp" {
		t.Fatalf("ip_conntrack fallback: flows=%+v total=%+v err=%s", flows, total, errStr)
	}
}

func TestRunEndToEndStatic(t *testing.T) {
	dir := t.TempDir()
	ct := filepath.Join(dir, "nf_conntrack")
	if err := os.WriteFile(ct, []byte(sampleNFConntrack), 0o600); err != nil {
		t.Fatal(err)
	}

	tr := &Tracer{
		Settings: settings.Settings{
			InboundMode:  "tproxy",
			RouteDomains: []string{"telegram.org"},
			RouteCIDR:    []string{"149.154.160.0/20"},
		},
		ConntrackPaths: []string{ct},
		LookupIP: func(ctx context.Context, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("149.154.167.99")}, nil
		},
	}

	rep, err := tr.Run(context.Background(), "https://web.telegram.org/k/")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Target != "web.telegram.org" || rep.Kind != "domain" || rep.Mode != "tproxy" {
		t.Fatalf("header: %+v", rep)
	}
	if len(rep.DomainMatches) != 1 || rep.DomainMatches[0].Match != "suffix" {
		t.Fatalf("domain matches: %+v", rep.DomainMatches)
	}
	if len(rep.IPs) != 1 {
		t.Fatalf("ips: %+v", rep.IPs)
	}
	ipr := rep.IPs[0]
	if ipr.IP != "149.154.167.99" {
		t.Fatalf("ip: %+v", ipr)
	}
	// No engine → static verdict from the route_cidr match.
	if ipr.Verdict != "proxy" || ipr.VerdictSource != "static" {
		t.Fatalf("verdict: %+v", ipr)
	}
	if ipr.ConntrackTotal != 1 || len(ipr.Conntrack) != 1 || ipr.Conntrack[0].State != "ESTABLISHED" {
		t.Fatalf("conntrack: %+v", ipr)
	}
	if rep.Outbound != nil {
		t.Fatalf("no clash addr configured, outbound must be nil: %+v", rep.Outbound)
	}
}

func TestRunIPTarget(t *testing.T) {
	tr := &Tracer{
		Settings:       settings.Settings{InboundMode: "redirect", ExcludeCIDR: []string{"192.168.0.0/16"}},
		ConntrackPaths: []string{filepath.Join(t.TempDir(), "missing")},
	}
	rep, err := tr.Run(context.Background(), "192.168.6.1:443")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Kind != "ip" || len(rep.IPs) != 1 || rep.IPs[0].Verdict != "bypass" {
		t.Fatalf("ip trace: %+v", rep)
	}
	if rep.ConntrackError == "" {
		t.Fatalf("missing conntrack table must be surfaced: %+v", rep)
	}
}

func TestMatchesDeduped(t *testing.T) {
	tr := &Tracer{
		Settings: settings.Settings{
			RouteDomains: []string{"chatgpt.com", "chatgpt.com", "ChatGPT.com."},
			RouteCIDR:    []string{"1.2.3.0/24", "1.2.3.0/24"},
		},
	}
	if got := tr.domainMatches("chatgpt.com"); len(got) != 1 {
		t.Fatalf("duplicate settings entries must collapse to one match: %+v", got)
	}
	if got := tr.cidrMatches("1.2.3.4"); len(got) != 1 {
		t.Fatalf("duplicate CIDR entries must collapse to one match: %+v", got)
	}
}
