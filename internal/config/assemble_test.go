package config

import (
	"encoding/json"
	"testing"
)

func TestAssemble_SingleServer(t *testing.T) {
	vless := map[string]any{
		"type": "vless", "tag": "ignored", "server": "1.2.3.4", "server_port": 443,
		"uuid": "u", "flow": "xtls-rprx-vision",
		"tls": map[string]any{"enabled": true, "server_name": "x"},
	}
	body, err := Assemble(AssembleOptions{InboundMode: InboundTun}, []ProxyOutbound{{Tag: "proxy-1", Object: vless}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	// tun inbound with gvisor stack.
	inb := cfg["inbounds"].([]any)[0].(map[string]any)
	if inb["type"] != "tun" || inb["stack"] != "gvisor" {
		t.Errorf("tun: %+v", inb)
	}
	// selector default points at the single server; final = proxy.
	route := cfg["route"].(map[string]any)
	if route["final"] != "proxy" {
		t.Errorf("final: %v", route["final"])
	}
	var sel map[string]any
	for _, o := range cfg["outbounds"].([]any) {
		m := o.(map[string]any)
		if m["tag"] == "proxy" {
			sel = m
		}
	}
	if sel == nil || sel["default"] != "proxy-1" {
		t.Errorf("selector: %+v", sel)
	}
	// server tag was overridden from "ignored" to "proxy-1".
	found := false
	for _, o := range cfg["outbounds"].([]any) {
		if o.(map[string]any)["tag"] == "proxy-1" {
			found = true
		}
	}
	if !found {
		t.Error("server outbound tag not applied")
	}
}

func TestAssemble_DefaultMode_IsSocks(t *testing.T) {
	vless := map[string]any{"type": "vless", "server": "h", "server_port": 1, "uuid": "u"}
	body, err := Assemble(AssembleOptions{}, []ProxyOutbound{{Tag: "p1", Object: vless}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	json.Unmarshal(body, &cfg)
	inb := cfg["inbounds"].([]any)[0].(map[string]any)
	if inb["type"] != "mixed" {
		t.Errorf("default inbound should be mixed (socks), got %v", inb["type"])
	}
	if inb["listen_port"].(float64) != 2080 {
		t.Errorf("default port: %v", inb["listen_port"])
	}
	// socks mode: no private-bypass / quic-reject rules.
	for _, r := range cfg["route"].(map[string]any)["rules"].([]any) {
		m := r.(map[string]any)
		if m["ip_is_private"] != nil || m["protocol"] == "quic" {
			t.Errorf("socks mode should not have tun-only rule: %+v", m)
		}
	}
}

func TestAssemble_TProxyMode(t *testing.T) {
	vless := map[string]any{"type": "vless", "server": "h", "server_port": 1, "uuid": "u"}
	body, err := Assemble(AssembleOptions{InboundMode: InboundTProxy, InboundPort: 7894},
		[]ProxyOutbound{{Tag: "p1", Object: vless}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	json.Unmarshal(body, &cfg)
	inb := cfg["inbounds"].([]any)[0].(map[string]any)
	if inb["type"] != "tproxy" || inb["listen_port"].(float64) != 7894 {
		t.Errorf("tproxy inbound: %+v", inb)
	}
	// tproxy captures real traffic → needs the bypass/quic rules.
	hasPrivate := false
	for _, r := range cfg["route"].(map[string]any)["rules"].([]any) {
		if r.(map[string]any)["ip_is_private"] != nil {
			hasPrivate = true
		}
	}
	if !hasPrivate {
		t.Error("tproxy mode missing private-range bypass rule")
	}
}

func TestAssemble_TProxySelective(t *testing.T) {
	vless := map[string]any{"type": "vless", "server": "h", "server_port": 1, "uuid": "u"}
	body, err := Assemble(AssembleOptions{
		InboundMode:  InboundTProxy,
		InboundPort:  2080,
		RouteDomains: []string{"youtube.com", "  ", "#x", "youtube.com"},
		RouteCIDR:    []string{"203.0.113.0/24"},
	}, []ProxyOutbound{{Tag: "p1", Object: vless}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	json.Unmarshal(body, &cfg)
	route := cfg["route"].(map[string]any)

	// Selective: default is direct, only matched rules go to proxy.
	if route["final"] != "direct" {
		t.Errorf("transparent final should be direct, got %v", route["final"])
	}
	var domainRule, cidrRule map[string]any
	for _, r := range route["rules"].([]any) {
		m := r.(map[string]any)
		if m["domain_suffix"] != nil {
			domainRule = m
		}
		if m["ip_cidr"] != nil {
			cidrRule = m
		}
	}
	if domainRule == nil || domainRule["outbound"] != "proxy" {
		t.Fatalf("missing domain->proxy rule: %+v", domainRule)
	}
	// Deduped + cleaned: youtube.com once, blank/comment dropped.
	if got := domainRule["domain_suffix"].([]any); len(got) != 1 || got[0] != "youtube.com" {
		t.Errorf("domain_suffix not cleaned/deduped: %v", got)
	}
	if cidrRule == nil || cidrRule["outbound"] != "proxy" {
		t.Errorf("missing ip_cidr->proxy rule: %+v", cidrRule)
	}
}

func TestAssemble_RedirectInbound(t *testing.T) {
	vless := map[string]any{"type": "vless", "server": "h", "server_port": 1, "uuid": "u"}
	// RouteDomains/RouteCIDR are intentionally ignored by the sing-box config in
	// redirect mode — selection happens at the iptables layer (the route ipset).
	body, err := Assemble(AssembleOptions{
		InboundMode:  InboundRedirect,
		InboundPort:  2081,
		RouteDomains: []string{"chatgpt.com"},
		RouteCIDR:    []string{"203.0.113.0/24"},
	}, []ProxyOutbound{{Tag: "p1", Object: vless}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	json.Unmarshal(body, &cfg)
	inb := cfg["inbounds"].([]any)[0].(map[string]any)
	if inb["type"] != "redirect" || inb["listen_port"].(float64) != 2081 {
		t.Errorf("redirect inbound: %+v", inb)
	}
	route := cfg["route"].(map[string]any)
	// iptables already selected what reaches sing-box → proxy everything.
	if route["final"] != "proxy" {
		t.Errorf("redirect mode final should be proxy, got %v", route["final"])
	}
	// No domain_suffix/ip_cidr decision rules: the ipset is the source of truth.
	for _, r := range route["rules"].([]any) {
		m := r.(map[string]any)
		if m["domain_suffix"] != nil || m["ip_cidr"] != nil {
			t.Errorf("redirect mode must carry no domain/ip_cidr rules, got: %+v", m)
		}
	}
	// Still sniffs + hijacks DNS + rejects QUIC (defence in depth).
	if !hasAction(route, "hijack-dns") || !hasAction(route, "sniff") {
		t.Errorf("redirect mode should keep sniff + hijack-dns rules")
	}
}

// hasAction reports whether route.rules contains a rule with the given action.
func hasAction(route map[string]any, action string) bool {
	for _, r := range route["rules"].([]any) {
		if m, ok := r.(map[string]any); ok && m["action"] == action {
			return true
		}
	}
	return false
}

func TestAssemble_MultipleServers_AddsAuto(t *testing.T) {
	mk := func() map[string]any {
		return map[string]any{"type": "trojan", "server": "h", "server_port": 1, "password": "p",
			"tls": map[string]any{"enabled": true}}
	}
	body, err := Assemble(AssembleOptions{}, []ProxyOutbound{
		{Tag: "s1", Object: mk()}, {Tag: "s2", Object: mk()},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	json.Unmarshal(body, &cfg)
	hasAuto := false
	for _, o := range cfg["outbounds"].([]any) {
		if o.(map[string]any)["tag"] == "auto" {
			hasAuto = true
		}
	}
	if !hasAuto {
		t.Error("expected urltest 'auto' outbound for multiple servers")
	}
}
