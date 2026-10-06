package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// DNS defaults and reserved tags.
const (
	DNSLocalTag      = "local"  // built-in system resolver; route.default_domain_resolver
	DNSInboundTag    = "dns-in" // direct inbound answering DNS (resolver/lookup/intercept)
	DefaultDNSPort   = 1053
	DefaultDNSDetour = OutboundDirectTag
	// DefaultDNSFailoverTimeout (seconds) is how long a server in a failover
	// chain gets before the query moves on to the next one.
	DefaultDNSFailoverTimeout = 2
	// DNSProbeBasePort..+DNSProbeMaxServers is the loopback SOURCE-port range
	// whose queries to dns-in bypass every chain and go straight to one server
	// (local = base, then servers in order), uncached — so the UI's lookup can
	// ask each server individually through sing-box (same protocol and detour)
	// and show which one answered. Below Linux's ephemeral range (32768+), so
	// ordinary clients never hit it by accident.
	DNSProbeBasePort   = 20530
	DNSProbeMaxServers = 64
)

// DNS strategies accepted by sing-box.
var dnsStrategies = []string{"ipv4_only", "prefer_ipv4", "prefer_ipv6", "ipv6_only"}

// DNS preset categories (UI groups presets by these).
const (
	DNSCatPlain   = "Без фильтрации"
	DNSCatAds     = "Блокировка рекламы"
	DNSCatMalware = "Защита от вредоносных сайтов"
	DNSCatFamily  = "Семейные (взрослый контент)"
	DNSCatLocal   = "Провайдер"
)

// DNSPreset is a well-known public resolver offered by the UI.
type DNSPreset struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	Tag      string `json:"tag"`
	Address  string `json:"address"`
	Note     string `json:"note,omitempty"`
	// Proxy marks resolvers blocked on direct paths in Russia (verified):
	// the UI turns on "Через VPN" for them.
	Proxy bool `json:"proxy,omitempty"`
}

// DNSPresets are the resolvers the UI offers as one-click choices. Hostnames
// are bootstrapped via the local resolver (domain_resolver=local); IPs are
// used only where the provider's certificate covers the IP. Every entry is
// verified against the live resolver by `go test -tags live ./internal/config`
// (Proxy entries through LIVE_PROXY, since they are blocked directly).
var DNSPresets = []DNSPreset{
	{DNSCatPlain, "Google DoT", "google", "tls://8.8.8.8", "", false},
	{DNSCatPlain, "Google DoH", "google-doh", "https://8.8.8.8/dns-query", "", false},
	{DNSCatPlain, "Google DoH3", "google-h3", "h3://dns.google/dns-query", "", false},
	{DNSCatPlain, "Cloudflare DoT", "cloudflare", "tls://1.1.1.1", "", false},
	{DNSCatPlain, "Cloudflare DoH", "cloudflare-doh", "https://1.1.1.1/dns-query", "", false},
	{DNSCatPlain, "Cloudflare DoH3", "cloudflare-h3", "h3://cloudflare-dns.com/dns-query", "", false},
	{DNSCatPlain, "Mullvad DoH", "mullvad", "https://dns.mullvad.net/dns-query", "без логов", true},
	{DNSCatPlain, "Mullvad DoT", "mullvad-dot", "tls://dns.mullvad.net", "без логов", false},
	{DNSCatPlain, "AdGuard без фильтрации DoH", "adguard-plain", "https://unfiltered.adguard-dns.com/dns-query", "", true},
	{DNSCatPlain, "Control D без фильтрации", "controld", "https://freedns.controld.com/p0", "", false},
	{DNSCatPlain, "OpenDNS DoH", "opendns", "https://doh.opendns.com/dns-query", "", false},
	{DNSCatPlain, "Яндекс DNS", "yandex", "77.88.8.8", "обычный UDP", false},
	{DNSCatPlain, "Яндекс DoT", "yandex-dot", "tls://common.dot.dns.yandex.net", "", false},
	{DNSCatPlain, "Яндекс DoH", "yandex-doh", "https://common.dot.dns.yandex.net/dns-query", "", false},

	{DNSCatAds, "AdGuard DoH", "adguard-doh", "https://dns.adguard-dns.com/dns-query", "реклама + трекеры", false},
	{DNSCatAds, "AdGuard DoT", "adguard", "tls://dns.adguard-dns.com", "реклама + трекеры", false},
	{DNSCatAds, "AdGuard DoQ", "adguard-doq", "quic://dns.adguard-dns.com", "реклама + трекеры", false},
	{DNSCatAds, "AdGuard DoH3", "adguard-h3", "h3://dns.adguard-dns.com/dns-query", "реклама + трекеры", false},
	{DNSCatAds, "Mullvad Adblock", "mullvad-adblock", "https://adblock.dns.mullvad.net/dns-query", "реклама + трекеры", true},
	{DNSCatAds, "Mullvad Base", "mullvad-base", "https://base.dns.mullvad.net/dns-query", "реклама + трекеры + вредоносные", true},
	{DNSCatAds, "Mullvad Extended", "mullvad-ext", "https://extended.dns.mullvad.net/dns-query", "+ соцсети", true},
	{DNSCatAds, "Control D Ads", "controld-ads", "https://freedns.controld.com/p2", "реклама + трекеры + вредоносные", false},
	{DNSCatAds, "DNS4EU без рекламы DoH", "dns4eu-noads", "https://noads.joindns4.eu/dns-query", "реклама + вредоносные", false},
	{DNSCatAds, "DNS4EU без рекламы DoT", "dns4eu-noads-dot", "tls://noads.joindns4.eu", "реклама + вредоносные", false},
	{DNSCatAds, "Comss DNS DoH", "comss", "https://dns.comss.one/dns-query", "реклама + обход геоблокировок", false},
	{DNSCatAds, "Comss DNS DoT", "comss-dot", "tls://dns.comss.one", "реклама + обход геоблокировок", false},

	{DNSCatMalware, "Quad9 DoT", "quad9", "tls://9.9.9.9", "", false},
	{DNSCatMalware, "Quad9 DoH", "quad9-doh", "https://9.9.9.9/dns-query", "", false},
	{DNSCatMalware, "Cloudflare Security", "cloudflare-sec", "https://security.cloudflare-dns.com/dns-query", "", false},
	{DNSCatMalware, "Control D Malware", "controld-malware", "https://freedns.controld.com/p1", "", false},
	{DNSCatMalware, "DNS4EU Protective", "dns4eu", "https://protective.joindns4.eu/dns-query", "", false},
	{DNSCatMalware, "Яндекс Безопасный", "yandex-safe", "tls://safe.dot.dns.yandex.net", "", false},

	{DNSCatFamily, "AdGuard Семейный", "adguard-family", "https://family.adguard-dns.com/dns-query", "+ реклама", false},
	{DNSCatFamily, "Cloudflare Family", "cloudflare-family", "https://family.cloudflare-dns.com/dns-query", "", false},
	{DNSCatFamily, "Mullvad Family", "mullvad-family", "https://family.dns.mullvad.net/dns-query", "+ реклама", true},
	{DNSCatFamily, "Control D Family", "controld-family", "https://freedns.controld.com/family", "", false},
	{DNSCatFamily, "DNS4EU Детский", "dns4eu-child", "https://child-noads.joindns4.eu/dns-query", "+ реклама", false},
	{DNSCatFamily, "Яндекс Семейный", "yandex-family", "tls://family.dot.dns.yandex.net", "", false},

	{DNSCatLocal, "DNS от провайдера (DHCP)", "dhcp", "dhcp://auto", "", false},
}

var dnsTagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// DNSServer is one user-defined upstream resolver. Address is a URL-ish
// string (see ParseDNSAddress); Detour picks whether the query egresses
// directly or through the proxy selector.
type DNSServer struct {
	Tag     string `json:"tag"`
	Address string `json:"address"`
	Detour  string `json:"detour"` // "direct" | "proxy"
}

// DNSRule sends queries for the given domain suffixes to Server (a tag),
// falling back to Backups in order if it fails.
type DNSRule struct {
	Domains []string `json:"domains"`
	Server  string   `json:"server"`
	Backups []string `json:"backups,omitempty"`
}

// DNSOptions is the user's DNS configuration.
type DNSOptions struct {
	Servers []DNSServer `json:"servers"`
	// Final is the server for everything not matched by a rule.
	Final string `json:"final"`
	// FinalBackups are tried in order when Final fails (see failover below).
	FinalBackups []string `json:"final_backups,omitempty"`
	// ProxiedServer, when set, resolves the routing domains (manual
	// RouteDomains + URL-list domains) via this server — the preset for
	// "blocked domains through the proxy, the rest locally".
	ProxiedServer  string   `json:"proxied_server"`
	ProxiedBackups []string `json:"proxied_backups,omitempty"`
	// Rules are manual rules, evaluated before the ProxiedServer preset.
	Rules    []DNSRule `json:"rules"`
	Strategy string    `json:"strategy"`
	// InterceptClients redirects LAN DNS (port 53) into sing-box's dns-in
	// inbound in the transparent modes; the route-ipset resolver then also
	// queries sing-box so clients and the ipset see the same IPs.
	InterceptClients bool `json:"intercept_clients"`
	// Port is the dns-in listen port.
	Port int `json:"port"`
	// FailoverTimeout (seconds) bounds each non-last server of a failover
	// chain before the query falls through to the next one.
	FailoverTimeout int `json:"failover_timeout,omitempty"`
}

// Failover chains (primary → backup → backup) use the sing-box 1.14 DNS rule
// actions: `evaluate` queries a server with a timeout and keeps the response;
// tagged `match_response` + `respond` rules return it when it is a real answer
// (NOERROR/NXDOMAIN). A failed/timed-out evaluate or a SERVFAIL/REFUSED answer
// falls through to the next server's evaluate, and the last server is a plain
// `route`. Verified on 1.14.2. Cost while the primary is down: each uncached
// query waits FailoverTimeout first.

// HasFailover reports whether any chain has backups (needs sing-box ≥ 1.14).
func (o DNSOptions) HasFailover() bool {
	if len(o.FinalBackups) > 0 || (o.ProxiedServer != "" && len(o.ProxiedBackups) > 0) {
		return true
	}
	return slices.ContainsFunc(o.Rules, func(r DNSRule) bool { return len(r.Backups) > 0 })
}

// DefaultDNS reproduces the historical hardcoded DNS block: DoT to 8.8.8.8
// (plus the built-in local resolver), ipv4_only, no rules.
func DefaultDNS() DNSOptions {
	return DNSOptions{
		Servers:  []DNSServer{{Tag: "google", Address: "tls://8.8.8.8", Detour: DefaultDNSDetour}},
		Final:    "google",
		Strategy: "ipv4_only",
		Port:     DefaultDNSPort,
	}
}

// Normalize fills defaults in place: empty server list → DefaultDNS servers,
// empty detour → direct, missing strategy/port/final.
func (o *DNSOptions) Normalize() {
	if len(o.Servers) == 0 {
		d := DefaultDNS()
		o.Servers = d.Servers
		if o.Final == "" {
			o.Final = d.Final
		}
	}
	for i := range o.Servers {
		o.Servers[i].Tag = strings.TrimSpace(o.Servers[i].Tag)
		o.Servers[i].Address = strings.TrimSpace(o.Servers[i].Address)
		if o.Servers[i].Detour == "" {
			o.Servers[i].Detour = DefaultDNSDetour
		}
	}
	if o.FailoverTimeout <= 0 || o.FailoverTimeout > 30 {
		o.FailoverTimeout = DefaultDNSFailoverTimeout
	}
	if o.Strategy == "" {
		o.Strategy = "ipv4_only"
	}
	if o.Port <= 0 || o.Port > 65535 {
		o.Port = DefaultDNSPort
	}
	// Dangling references (e.g. defaults merged under a stored server list)
	// would fail sing-box check; repoint them.
	if !o.hasTag(o.Final) {
		o.Final = o.Servers[0].Tag
	}
	if o.ProxiedServer != "" && !o.hasTag(o.ProxiedServer) {
		o.ProxiedServer = ""
	}
	o.FinalBackups = o.cleanBackups(o.Final, o.FinalBackups)
	o.ProxiedBackups = o.cleanBackups(o.ProxiedServer, o.ProxiedBackups)
	for i := range o.Rules {
		o.Rules[i].Domains = CleanDomains(o.Rules[i].Domains)
		o.Rules[i].Backups = o.cleanBackups(o.Rules[i].Server, o.Rules[i].Backups)
	}
}

// cleanBackups drops unknown tags, duplicates and the primary itself.
func (o DNSOptions) cleanBackups(primary string, backups []string) []string {
	if primary == "" {
		return nil
	}
	var out []string
	for _, b := range backups {
		b = strings.TrimSpace(b)
		if b != primary && o.hasTag(b) && !slices.Contains(out, b) {
			out = append(out, b)
		}
	}
	return out
}

func (o DNSOptions) hasTag(tag string) bool {
	if tag == DNSLocalTag {
		return true
	}
	return slices.ContainsFunc(o.Servers, func(s DNSServer) bool { return s.Tag == tag })
}

// Validate checks user input. Empty detour/strategy/port/final are accepted
// (Normalize fills them); dangling references are errors here, whereas
// Normalize silently repoints them. inboundPort is the main proxy inbound
// port (dns-in must not collide with it or its loopback twin).
func (o DNSOptions) Validate(inboundPort int) error {
	if o.Final == "" {
		if len(o.Servers) == 0 {
			return errors.New("не выбран основной DNS-сервер")
		}
		o.Final = o.Servers[0].Tag
	}
	if o.Strategy == "" {
		o.Strategy = "ipv4_only"
	}
	if o.Port == 0 {
		o.Port = DefaultDNSPort
	}
	tags := map[string]bool{DNSLocalTag: true}
	for i, s := range o.Servers {
		s.Tag = strings.TrimSpace(s.Tag)
		if s.Tag == DNSLocalTag || s.Tag == DNSInboundTag {
			return fmt.Errorf("сервер #%d: тег %q зарезервирован", i+1, s.Tag)
		}
		if !dnsTagRe.MatchString(s.Tag) {
			return fmt.Errorf("сервер #%d: тег %q: допустимы a-z, 0-9, _ и - (до 32 символов)", i+1, s.Tag)
		}
		if tags[s.Tag] {
			return fmt.Errorf("тег %q повторяется", s.Tag)
		}
		tags[s.Tag] = true
		if _, err := ParseDNSAddress(s.Address); err != nil {
			return fmt.Errorf("сервер %q: %w", s.Tag, err)
		}
		if s.Detour != "" && s.Detour != OutboundDirectTag && s.Detour != OutboundProxyTag {
			return fmt.Errorf("сервер %q: detour должен быть direct или proxy", s.Tag)
		}
	}
	checkChain := func(what, primary string, backups []string) error {
		if !tags[primary] {
			return fmt.Errorf("%s: сервер %q не найден", what, primary)
		}
		seen := map[string]bool{primary: true}
		for _, b := range backups {
			if !tags[b] {
				return fmt.Errorf("%s: резервный сервер %q не найден", what, b)
			}
			if seen[b] {
				return fmt.Errorf("%s: сервер %q указан в цепочке дважды", what, b)
			}
			seen[b] = true
		}
		return nil
	}
	if err := checkChain("сервер по умолчанию", o.Final, o.FinalBackups); err != nil {
		return err
	}
	if o.ProxiedServer != "" {
		if err := checkChain("домены из маршрутизации", o.ProxiedServer, o.ProxiedBackups); err != nil {
			return err
		}
	} else if len(o.ProxiedBackups) > 0 {
		return errors.New("домены из маршрутизации: резервные серверы без основного")
	}
	for i, r := range o.Rules {
		if err := checkChain(fmt.Sprintf("правило #%d", i+1), r.Server, r.Backups); err != nil {
			return err
		}
		if len(CleanDomains(r.Domains)) == 0 {
			return fmt.Errorf("правило #%d: нет доменов", i+1)
		}
	}
	if o.FailoverTimeout < 0 || o.FailoverTimeout > 30 {
		return fmt.Errorf("таймаут переключения %d с: допустимо 1–30", o.FailoverTimeout)
	}
	if !slices.Contains(dnsStrategies, o.Strategy) {
		return fmt.Errorf("неизвестная стратегия %q", o.Strategy)
	}
	if o.Port <= 0 || o.Port > 65535 {
		return fmt.Errorf("порт DNS %d вне диапазона", o.Port)
	}
	if len(o.Servers) >= DNSProbeMaxServers {
		return fmt.Errorf("слишком много DNS-серверов (максимум %d)", DNSProbeMaxServers-1)
	}
	if o.Port >= DNSProbeBasePort && o.Port < DNSProbeBasePort+DNSProbeMaxServers {
		return fmt.Errorf("порт DNS %d зарезервирован (%d–%d)", o.Port, DNSProbeBasePort, DNSProbeBasePort+DNSProbeMaxServers-1)
	}
	if o.Port == 53 {
		return errors.New("порт 53 занят DNS Keenetic")
	}
	if inboundPort > 0 && (o.Port == inboundPort || o.Port == LoopbackProxyPort(inboundPort)) {
		return fmt.Errorf("порт DNS %d совпадает с портом входа прокси", o.Port)
	}
	return nil
}

// ParseDNSAddress converts a resolver address into a sing-box 1.12+ DNS
// server object (without tag/detour). Accepted forms:
//
//	8.8.8.8 | 8.8.8.8:53 | [2001:db8::1]:53   plain UDP
//	udp://host[:port]  tcp://host[:port]
//	tls://host[:port]                         DoT
//	https://host[:port][/path]                DoH
//	h3://host[:port][/path]                   DoH3
//	quic://host[:port]                        DoQ
//	dhcp://auto | dhcp://<iface>              DHCP-provided resolver
//	local                                     system resolver
//
// A non-IP host gets domain_resolver=local so sing-box can bootstrap it.
func ParseDNSAddress(addr string) (map[string]any, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("пустой адрес")
	}
	if addr == "local" || addr == "local://" {
		return map[string]any{"type": "local"}, nil
	}
	if !strings.Contains(addr, "://") {
		addr = "udp://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("адрес %q: %w", addr, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "dhcp" {
		out := map[string]any{"type": "dhcp"}
		if iface := u.Host; iface != "" && iface != "auto" {
			out["interface"] = iface
		}
		return out, nil
	}

	typ, defPort, hasPath := "", 0, false
	switch scheme {
	case "udp":
		typ, defPort = "udp", 53
	case "tcp":
		typ, defPort = "tcp", 53
	case "tls":
		typ, defPort = "tls", 853
	case "https":
		typ, defPort, hasPath = "https", 443, true
	case "h3":
		typ, defPort, hasPath = "h3", 443, true
	case "quic":
		typ, defPort = "quic", 853
	default:
		return nil, fmt.Errorf("неизвестная схема %q (udp, tcp, tls, https, h3, quic, dhcp, local)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("адрес %q: нет хоста", addr)
	}
	if strings.ContainsAny(host, " /") {
		return nil, fmt.Errorf("адрес %q: некорректный хост", addr)
	}
	out := map[string]any{"type": typ, "server": host}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return nil, fmt.Errorf("адрес %q: некорректный порт", addr)
		}
		if n != defPort {
			out["server_port"] = n
		}
	}
	if hasPath {
		if p := u.EscapedPath(); p != "" && p != "/" && p != "/dns-query" {
			out["path"] = p
		}
	} else if p := u.Path; p != "" && p != "/" {
		return nil, fmt.Errorf("адрес %q: путь допустим только для https/h3", addr)
	}
	if net.ParseIP(host) == nil {
		out["domain_resolver"] = DNSLocalTag
	}
	return out, nil
}

// CleanDomains trims, lower-cases, strips a leading "." / "*.", drops
// empties, comments and bare IPs, and dedups (order preserved).
func CleanDomains(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(d, "*.")
		d = strings.TrimPrefix(d, ".")
		if d == "" || strings.HasPrefix(d, "#") || strings.ContainsAny(d, " /:") {
			continue
		}
		if net.ParseIP(d) != nil {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

// dnsBlock builds the sing-box dns block plus the inline rule-sets its rules
// reference (to be placed in route.rule_set). routeDomains feed the
// ProxiedServer preset rule; probes adds the per-server lookup rules (needs
// the dns-in inbound).
func dnsBlock(o DNSOptions, routeDomains []string, probes bool) (map[string]any, []map[string]any) {
	o.Normalize()
	servers := make([]map[string]any, 0, len(o.Servers)+1)
	for _, s := range o.Servers {
		obj, err := ParseDNSAddress(s.Address)
		if err != nil {
			continue // Validate rejects these on save; skip defensively
		}
		obj["tag"] = s.Tag
		// Only the proxy detour is written: an empty detour already dials
		// directly, and sing-box (1.13, 1.14) refuses to START — check passes!
		// — with "detour to an empty direct outbound makes no sense".
		if s.Detour == OutboundProxyTag && obj["type"] != "local" && obj["type"] != "dhcp" {
			obj["detour"] = OutboundProxyTag
		}
		servers = append(servers, obj)
	}
	servers = append(servers, map[string]any{"type": "local", "tag": DNSLocalTag})

	block := map[string]any{
		"servers":  servers,
		"final":    o.Final,
		"strategy": o.Strategy,
	}
	timeout := strconv.Itoa(o.FailoverTimeout) + "s"
	var rules, ruleSets []map[string]any
	// Probe rules first (see DNSProbeBasePort).
	for i, tag := range o.probeTags() {
		if !probes {
			break
		}
		rules = append(rules, map[string]any{
			"inbound":        []string{DNSInboundTag},
			"source_ip_cidr": []string{"127.0.0.1/32"},
			"source_port":    []int{DNSProbeBasePort + i},
			"action":         "route",
			"server":         tag,
			"disable_cache":  true,
		})
	}
	addChain := func(id string, domains []string, chain []string) {
		if len(chain) == 1 {
			// Single server: plain route.
			rule := map[string]any{"action": "route", "server": chain[0]}
			if domains != nil {
				rule["domain_suffix"] = domains
			}
			rules = append(rules, rule)
			return
		}
		// The domain condition repeats on every step, so keep one copy of a
		// (possibly huge, URL-list-sized) domain set in an inline rule-set.
		var cond map[string]any
		if domains != nil {
			set := "ksbui-dns-" + id
			ruleSets = append(ruleSets, map[string]any{
				"type": "inline", "tag": set,
				"rules": []map[string]any{{"domain_suffix": domains}},
			})
			cond = map[string]any{"rule_set": []string{set}}
		}
		with := func(m map[string]any) map[string]any {
			for k, v := range cond {
				m[k] = v
			}
			return m
		}
		last := len(chain) - 1
		for i, srv := range chain[:last] {
			tag := fmt.Sprintf("%s-%d", id, i)
			rules = append(rules, with(map[string]any{"action": "evaluate", "server": srv, "tag": tag, "timeout": timeout}))
			// Tag-only respond rules: they can only match when this very
			// evaluate ran for the query, so they need no domain condition.
			for _, rcode := range []string{"NOERROR", "NXDOMAIN"} {
				rules = append(rules, map[string]any{"match_response": tag, "response_rcode": rcode, "action": "respond"})
			}
		}
		rules = append(rules, with(map[string]any{"action": "route", "server": chain[last]}))
	}
	for i, r := range o.Rules {
		if d := CleanDomains(r.Domains); len(d) > 0 {
			addChain(fmt.Sprintf("rule%d", i+1), d, append([]string{r.Server}, r.Backups...))
		}
	}
	if o.ProxiedServer != "" {
		if d := CleanDomains(routeDomains); len(d) > 0 {
			addChain("proxied", d, append([]string{o.ProxiedServer}, o.ProxiedBackups...))
		}
	}
	// The default server needs rules only with backups; dns.final covers the
	// single-server case.
	if len(o.FinalBackups) > 0 {
		addChain("final", nil, append([]string{o.Final}, o.FinalBackups...))
	}
	if len(rules) > 0 {
		block["rules"] = rules
	}
	return block, ruleSets
}

// probeTags lists the servers in probe-port order: local, then o.Servers.
func (o DNSOptions) probeTags() []string {
	tags := []string{DNSLocalTag}
	for _, s := range o.Servers {
		if len(tags) >= DNSProbeMaxServers {
			break
		}
		tags = append(tags, s.Tag)
	}
	return tags
}

// DNSProbePorts extracts tag → probe source port from an applied sing-box
// config (the rules dnsBlock emits), so a lookup matches what is running.
func DNSProbePorts(configJSON []byte) map[string]int {
	var cfg struct {
		DNS struct {
			Rules []struct {
				SourcePort []int  `json:"source_port"`
				Server     string `json:"server"`
			} `json:"rules"`
		} `json:"dns"`
	}
	out := map[string]int{}
	if json.Unmarshal(configJSON, &cfg) != nil {
		return out
	}
	for _, r := range cfg.DNS.Rules {
		if len(r.SourcePort) == 1 && r.Server != "" &&
			r.SourcePort[0] >= DNSProbeBasePort && r.SourcePort[0] < DNSProbeBasePort+DNSProbeMaxServers {
			out[r.Server] = r.SourcePort[0]
		}
	}
	return out
}

// ChainFor returns which chain resolves domain — the first matching manual
// rule, else the routing-domains chain (routeDomains = manual + URL lists),
// else the default — as a display name and the ordered server tags.
func (o DNSOptions) ChainFor(domain string, routeDomains []string) (string, []string) {
	o.Normalize()
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	match := func(suffixes []string) bool {
		for _, s := range CleanDomains(suffixes) {
			if d == s || strings.HasSuffix(d, "."+s) {
				return true
			}
		}
		return false
	}
	for i, r := range o.Rules {
		if match(r.Domains) {
			return fmt.Sprintf("Правило %d", i+1), append([]string{r.Server}, r.Backups...)
		}
	}
	if o.ProxiedServer != "" && match(routeDomains) {
		return "Заблокированные сайты", append([]string{o.ProxiedServer}, o.ProxiedBackups...)
	}
	return "Основной DNS", append([]string{o.Final}, o.FinalBackups...)
}

// dnsInbound is the direct inbound that answers DNS (hijacked by the route
// rule). Loopback-only unless client interception needs it on the LAN.
func dnsInbound(o DNSOptions) map[string]any {
	o.Normalize()
	listen := "127.0.0.1"
	if o.InterceptClients {
		listen = "0.0.0.0"
	}
	return map[string]any{
		"type":        "direct",
		"tag":         DNSInboundTag,
		"listen":      listen,
		"listen_port": o.Port,
	}
}
