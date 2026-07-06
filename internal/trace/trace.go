// Package trace answers "why does this destination go (or not go) through the
// proxy". In the selective transparent modes the proxy/direct decision is
// spread over several layers — DNS resolution feeding the route ipset, static
// CIDRs from settings and URL lists, the exclude/reject sets, the PREROUTING
// capture jump, and finally sing-box's outbound selector — so debugging one
// site by hand means conntrack greps, `ipset test` and log reading. Run folds
// all of that into a single structured report:
//
//   - how the target resolves (through the router's own resolver, the same
//     path the route-set resolver uses);
//   - which rule source matched it — route_domains, route_cidr, exclude_cidr,
//     reject_cidr, or a URL list — including matches that have NO data-plane
//     effect (URL-list domains are intentionally never resolved into the
//     ipset), which is a classic source of "why isn't this proxied";
//   - live ipset membership and whether the PREROUTING capture is actually
//     installed (a firewall rebuild can strip it while sing-box stays up);
//   - the verdict per IP, in the same order the firewall evaluates:
//     reject(:443) → exclude bypass → route capture → direct;
//   - live conntrack flows to each IP (REDIRECT detected via the reply tuple);
//   - the outbound the sing-box "proxy" selector currently points at.
package trace

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
	"github.com/CoOre/keenetic-sing-box-ui/internal/lists"
	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
	"github.com/CoOre/keenetic-sing-box-ui/internal/transparent"
)

const (
	lookupTimeout = 5 * time.Second
	clashTimeout  = 3 * time.Second
	maxFlowsPerIP = 20
)

// conntrackPaths are tried in order; which one exists depends on the kernel.
var conntrackPaths = []string{"/proc/net/nf_conntrack", "/proc/net/ip_conntrack"}

// Tracer carries one request's inputs. It is built per request from the API
// deps; all fields are plain data or optional collaborators, so tests can
// construct it directly.
type Tracer struct {
	Settings settings.Settings
	Sources  []*lists.Source // URL-list sources incl. cached entries, for attribution

	// Engine runs the live ipset/iptables checks; nil (or a non-transparent
	// mode) skips them and the verdict falls back to the static rule matches.
	Engine       *transparent.Engine
	EngineConfig transparent.Config

	// Clash API endpoint for the current selector outbound.
	ClashAddr   string
	ClashSecret string

	// Test seams; nil means the real thing.
	LookupIP       func(ctx context.Context, host string) ([]net.IP, error)
	ConntrackPaths []string
	HTTPClient     *http.Client
}

// Report is the full trace result.
type Report struct {
	Target string `json:"target"` // normalized host
	Kind   string `json:"kind"`   // "domain" | "ip"
	Mode   string `json:"mode"`   // inbound mode the verdicts were computed for

	// CaptureInstalled is nil when it can't be checked (no engine, mode not
	// transparent); false means the route set may match but nothing is captured.
	CaptureInstalled *bool `json:"capture_installed,omitempty"`

	ResolveError   string      `json:"resolve_error,omitempty"`
	DomainMatches  []RuleMatch `json:"domain_matches,omitempty"`
	IPs            []IPReport  `json:"ips"`
	ConntrackError string      `json:"conntrack_error,omitempty"`
	Outbound       *Outbound   `json:"outbound,omitempty"`
}

// RuleMatch attributes the target (or one of its IPs) to a configured rule
// source. Effective=false flags matches with no data-plane effect — the trap
// this tool exists to surface (e.g. URL-list domains are never resolved into
// the route ipset, and route_domains only resolve the exact listed name, not
// subdomains).
type RuleMatch struct {
	Source    string `json:"source"` // "route_domains" | "route_cidr" | "exclude_cidr" | "reject_cidr" | "list"
	Entry     string `json:"entry"`
	Match     string `json:"match,omitempty"` // domains: "exact" | "suffix"
	ListURL   string `json:"list_url,omitempty"`
	ListKind  string `json:"list_kind,omitempty"` // list entries: "domain" | "cidr"
	Effective bool   `json:"effective"`
}

// IPReport is the per-destination-IP part of the trace.
type IPReport struct {
	IP             string                     `json:"ip"`
	Sets           *transparent.SetMembership `json:"sets,omitempty"`
	Matches        []RuleMatch                `json:"matches,omitempty"`
	Conntrack      []Flow                     `json:"conntrack,omitempty"`
	ConntrackTotal int                        `json:"conntrack_total"`
	Verdict        string                     `json:"verdict"`        // proxy|direct|bypass|reject|capture_missing|no_capture|unknown
	VerdictSource  string                     `json:"verdict_source"` // "live" (ipset) | "static" (rule matches only)
}

// Outbound is where sing-box's "proxy" selector currently points.
type Outbound struct {
	Selector string `json:"selector"`
	Now      string `json:"now,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Flow is one conntrack entry whose original destination is the traced IP.
type Flow struct {
	Proto    string `json:"proto"`
	State    string `json:"state,omitempty"`
	Src      string `json:"src"`
	SPort    int    `json:"sport"`
	DPort    int    `json:"dport"`
	ReplySrc string `json:"reply_src,omitempty"`
	Mark     string `json:"mark,omitempty"`
	// Redirected means the reply tuple's source differs from the original
	// destination — the kernel NAT'd the flow (our nat REDIRECT capture).
	Redirected bool `json:"redirected"`
}

// Run executes the trace. The only error is an unusable target; everything
// else (resolve failures, missing conntrack, dead Clash API) is reported
// inside the Report so a partial answer is still an answer.
func (t *Tracer) Run(ctx context.Context, target string) (*Report, error) {
	host, err := NormalizeTarget(target)
	if err != nil {
		return nil, err
	}

	rep := &Report{Target: host, Mode: t.Settings.InboundMode, Kind: "domain"}
	transparentMode := t.Settings.InboundMode == "tproxy" || t.Settings.InboundMode == "redirect"

	var ips []string
	if addr, err := netip.ParseAddr(host); err == nil {
		rep.Kind = "ip"
		ips = []string{addr.Unmap().String()}
	} else {
		rep.DomainMatches = t.domainMatches(host)
		resolved, rerr := t.resolve(ctx, host)
		if rerr != nil {
			rep.ResolveError = rerr.Error()
		}
		ips = resolved
	}

	if t.Engine != nil && transparentMode {
		ok := t.Engine.CaptureInstalled(ctx, t.EngineConfig)
		rep.CaptureInstalled = &ok
	}

	flows, total, cterr := readConntrack(t.conntrackPaths(), ips)
	if cterr != "" {
		rep.ConntrackError = cterr
	}

	rep.IPs = make([]IPReport, 0, len(ips))
	for _, ip := range ips {
		ipr := IPReport{IP: ip, Matches: t.cidrMatches(ip)}
		ipr.Conntrack = flows[ip]
		ipr.ConntrackTotal = total[ip]
		if t.Engine != nil && transparentMode {
			m := t.Engine.TestSetMembership(ctx, ip)
			ipr.Sets = &m
		}
		ipr.Verdict, ipr.VerdictSource = t.verdict(ipr, rep.CaptureInstalled)
		rep.IPs = append(rep.IPs, ipr)
	}

	if t.ClashAddr != "" {
		rep.Outbound = t.currentOutbound(ctx)
	}
	return rep, nil
}

// NormalizeTarget reduces user input (URL, host:port, bare host/IP) to a
// lowercase hostname or IP literal.
func NormalizeTarget(in string) (string, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", fmt.Errorf("empty target")
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return "", fmt.Errorf("unparseable URL %q", in)
		}
		s = u.Hostname()
	} else if h, _, err := net.SplitHostPort(s); err == nil {
		// host:port / [v6]:port. A bare IPv6 literal has ≥2 colons and no
		// brackets — SplitHostPort errors on it, so it falls through intact.
		s = h
	}
	s = strings.ToLower(strings.TrimSuffix(strings.Trim(s, "[]"), "."))
	if s == "" {
		return "", fmt.Errorf("empty target")
	}
	if strings.ContainsAny(s, " /\\@") {
		return "", fmt.Errorf("invalid target %q", in)
	}
	return s, nil
}

func (t *Tracer) resolve(ctx context.Context, host string) ([]string, error) {
	lookup := t.LookupIP
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip4", host)
		}
	}
	lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	raw, err := lookup(lctx, host)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, ip := range raw {
		v4 := ip.To4()
		if v4 == nil {
			continue
		}
		s := v4.String()
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out, nil
}

// domainMatches attributes a domain target to route_domains entries and
// URL-list domain entries. Only an EXACT route_domains match is effective:
// the resolver resolves precisely the listed names, so a parent-domain
// (suffix) match doesn't put the subdomain's own IPs into the route set. And
// URL-list domains are never resolved at all (deliberately — thousands of
// lookups would crush the router), so those matches are informational only.
func (t *Tracer) domainMatches(host string) []RuleMatch {
	var out []RuleMatch
	seen := map[string]struct{}{} // settings lists commonly hold duplicates
	add := func(m RuleMatch) {
		key := m.Source + "|" + m.Entry + "|" + m.Match + "|" + m.ListURL
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, m)
	}
	for _, entry := range t.Settings.RouteDomains {
		if kind := domainMatchKind(host, entry); kind != "" {
			add(RuleMatch{
				Source: "route_domains", Entry: normDomain(entry), Match: kind,
				Effective: kind == "exact",
			})
		}
	}
	for _, src := range t.Sources {
		if src == nil || !src.Enabled {
			continue
		}
		for _, entry := range src.Domains {
			if kind := domainMatchKind(host, entry); kind != "" {
				add(RuleMatch{
					Source: "list", Entry: normDomain(entry), Match: kind,
					ListURL: src.URL, ListKind: "domain", Effective: false,
				})
			}
		}
	}
	return out
}

// cidrMatches attributes an IP to the static CIDR sources. All of these are
// seeded into their respective ipsets, so every match is effective.
func (t *Tracer) cidrMatches(ip string) []RuleMatch {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return nil
	}
	var out []RuleMatch
	seen := map[string]struct{}{}
	scan := func(entries []string, source string, listURL string) {
		for _, e := range entries {
			if entry, ok := cidrContains(e, addr); ok {
				key := source + "|" + entry + "|" + listURL
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				m := RuleMatch{Source: source, Entry: entry, Effective: true, ListURL: listURL}
				if listURL != "" {
					m.ListKind = "cidr"
				}
				out = append(out, m)
			}
		}
	}
	scan(t.Settings.RouteCIDR, "route_cidr", "")
	scan(t.Settings.ExcludeCIDR, "exclude_cidr", "")
	scan(t.Settings.RejectCIDR, "reject_cidr", "")
	for _, src := range t.Sources {
		if src == nil || !src.Enabled {
			continue
		}
		scan(src.CIDRs, "list", src.URL)
	}
	return out
}

// verdict decides what the firewall will do with traffic to this IP, in rule
// order: reject-set (:443, evaluated first in FORWARD) → exclude bypass →
// route capture → direct fall-through. Prefers the live ipset answer; falls
// back to the static rule matches when the sets can't be tested (dev machine,
// mode off). route_domains exact matches count for the static path: the
// resolver will fold their IPs in even if it hasn't yet.
func (t *Tracer) verdict(ipr IPReport, capture *bool) (string, string) {
	switch t.Settings.InboundMode {
	case "tun":
		return "proxy", "static" // full tunnel: everything goes through
	case "tproxy", "redirect":
	default: // socks, off: nothing is transparently captured
		return "no_capture", "static"
	}

	if s := ipr.Sets; s != nil && s.Err == "" {
		return verdictFrom(s.Reject, s.Exclude, s.Route, capture), "live"
	}
	var route, exclude, reject bool
	for _, m := range ipr.Matches {
		if !m.Effective {
			continue
		}
		switch m.Source {
		case "route_cidr":
			route = true
		case "list":
			if m.ListKind == "cidr" {
				route = true
			}
		case "exclude_cidr":
			exclude = true
		case "reject_cidr":
			reject = true
		}
	}
	return verdictFrom(reject, exclude, route, capture), "static"
}

func verdictFrom(reject, exclude, route bool, capture *bool) string {
	switch {
	case reject:
		return "reject"
	case exclude:
		return "bypass"
	case route && capture != nil && !*capture:
		return "capture_missing"
	case route:
		return "proxy"
	default:
		return "direct"
	}
}

// --- matching helpers ---

func normDomain(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// domainMatchKind reports how host relates to a configured domain entry:
// "exact" for the same name, "suffix" when host is a subdomain of entry, ""
// otherwise. Comments/empties never match.
func domainMatchKind(host, entry string) string {
	e := normDomain(entry)
	if e == "" || strings.HasPrefix(e, "#") {
		return ""
	}
	if host == e {
		return "exact"
	}
	if strings.HasSuffix(host, "."+e) {
		return "suffix"
	}
	return ""
}

// cidrContains reports whether entry (a CIDR or bare IP, possibly with junk
// whitespace) contains addr; the normalized entry is returned for display.
func cidrContains(entry string, addr netip.Addr) (string, bool) {
	e := strings.TrimSpace(entry)
	if e == "" || strings.HasPrefix(e, "#") {
		return "", false
	}
	if p, err := netip.ParsePrefix(e); err == nil {
		return e, p.Contains(addr.Unmap())
	}
	if a, err := netip.ParseAddr(e); err == nil {
		return e, a.Unmap() == addr.Unmap()
	}
	return "", false
}

// --- conntrack ---

func (t *Tracer) conntrackPaths() []string {
	if len(t.ConntrackPaths) > 0 {
		return t.ConntrackPaths
	}
	return conntrackPaths
}

// readConntrack streams the first existing conntrack table and collects flows
// whose ORIGINAL destination is one of ips (capped per IP; total counts are
// exact). The table can hold tens of thousands of entries on a busy router,
// hence the line-by-line scan instead of slurping the file.
func readConntrack(paths, ips []string) (map[string][]Flow, map[string]int, string) {
	want := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		want[ip] = struct{}{}
	}
	flows := make(map[string][]Flow, len(ips))
	total := make(map[string]int, len(ips))
	if len(want) == 0 {
		return flows, total, ""
	}

	var lastErr string
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 4096), 1<<20)
		for sc.Scan() {
			fl, dst, ok := parseConntrackLine(sc.Text())
			if !ok {
				continue
			}
			if _, hit := want[dst]; !hit {
				continue
			}
			total[dst]++
			if len(flows[dst]) < maxFlowsPerIP {
				flows[dst] = append(flows[dst], fl)
			}
		}
		if err := sc.Err(); err != nil {
			return flows, total, err.Error()
		}
		return flows, total, ""
	}
	return flows, total, lastErr
}

// parseConntrackLine handles both table formats:
//
//	nf_conntrack: "ipv4 2 tcp 6 3599 ESTABLISHED src=… dst=… sport=… dport=… src=… dst=… … mark=274 …"
//	ip_conntrack: "tcp 6 3599 ESTABLISHED src=… …" (no address-family prefix)
//
// The first src/dst/sport/dport quadruple is the original tuple, the second is
// the reply tuple. Returns the flow and its original destination IP.
func parseConntrackLine(line string) (Flow, string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return Flow{}, "", false
	}
	i := 0
	if fields[0] == "ipv4" || fields[0] == "ipv6" {
		if fields[0] == "ipv6" || len(fields) < 6 {
			return Flow{}, "", false
		}
		i = 2
	}
	fl := Flow{Proto: fields[i]}

	var origSrc, origDst, replySrc string
	var origSport, origDport int
	nSrc, nDst, nSport, nDport := 0, 0, 0, 0
	for _, f := range fields[i+1:] {
		k, v, found := strings.Cut(f, "=")
		if !found {
			// The TCP state token (ESTABLISHED, TIME_WAIT, …) precedes the tuples.
			if nSrc == 0 && f == strings.ToUpper(f) && strings.IndexFunc(f, func(r rune) bool {
				return (r < 'A' || r > 'Z') && r != '_'
			}) < 0 {
				fl.State = f
			}
			continue
		}
		switch k {
		case "src":
			nSrc++
			switch nSrc {
			case 1:
				origSrc = v
			case 2:
				replySrc = v
			}
		case "dst":
			nDst++
			if nDst == 1 {
				origDst = v
			}
		case "sport":
			nSport++
			if nSport == 1 {
				origSport, _ = strconv.Atoi(v)
			}
		case "dport":
			nDport++
			if nDport == 1 {
				origDport, _ = strconv.Atoi(v)
			}
		case "mark":
			if v != "" && v != "0" {
				fl.Mark = v
			}
		}
	}
	if origDst == "" {
		return Flow{}, "", false
	}
	fl.Src = origSrc
	fl.SPort = origSport
	fl.DPort = origDport
	fl.ReplySrc = replySrc
	fl.Redirected = replySrc != "" && replySrc != origDst
	return fl, origDst, true
}

// --- clash outbound ---

// currentOutbound asks the Clash API where the "proxy" selector points right
// now — the final hop of the trace ("captured traffic goes to THIS server").
func (t *Tracer) currentOutbound(ctx context.Context) *Outbound {
	out := &Outbound{Selector: config.OutboundProxyTag}
	addr := t.ClashAddr
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u := strings.TrimSuffix(addr, "/") + "/proxies/" + url.PathEscape(config.OutboundProxyTag)

	cctx, cancel := context.WithTimeout(ctx, clashTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if t.ClashSecret != "" {
		req.Header.Set("Authorization", "Bearer "+t.ClashSecret)
	}
	client := t.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: clashTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out.Error = resp.Status
		return out
	}
	var body struct {
		Now string `json:"now"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		out.Error = err.Error()
		return out
	}
	out.Now = body.Now
	return out
}
