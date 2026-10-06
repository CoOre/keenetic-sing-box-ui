package api

import (
	"net/http"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
)

func TestAPI_DNS(t *testing.T) {
	e := newEnv(t)

	var got struct {
		DNS     config.DNSOptions  `json:"dns"`
		Presets []config.DNSPreset `json:"presets"`
	}
	mustJSON(t, readAll(t, e.bearer("GET", "/api/dns", nil)), &got)
	if got.DNS.Final != "google" || len(got.DNS.Servers) != 1 || len(got.Presets) == 0 {
		t.Fatalf("defaults: %+v", got)
	}

	bad := `{"servers":[{"tag":"cf","address":"ftp://x"}]}`
	if resp := e.bearer("PUT", "/api/dns", []byte(bad)); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid accepted: %d", resp.StatusCode)
	}
	dangling := `{"servers":[{"tag":"cf","address":"1.1.1.1"}],"final":"nope"}`
	if resp := e.bearer("PUT", "/api/dns", []byte(dangling)); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("dangling final accepted: %d", resp.StatusCode)
	}

	good := `{"servers":[{"tag":"cf","address":"https://1.1.1.1/dns-query"},{"tag":"ag","address":"quic://dns.adguard-dns.com","detour":"proxy"}],` +
		`"final":"cf","proxied_server":"ag","intercept_clients":true,"port":5300}`
	resp := e.bearer("PUT", "/api/dns", []byte(good))
	if body := readAll(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	s, _ := e.deps.Settings.Get()
	if s.DNS.ProxiedServer != "ag" || s.DNS.Servers[0].Detour != "direct" || s.DNS.Port != 5300 {
		t.Fatalf("stored: %+v", s.DNS)
	}

	// A settings save from another screen (stale copy without our DNS) must
	// not roll DNS back...
	if resp := e.bearer("PUT", "/api/settings", []byte(`{"inbound_mode":"tproxy","dns":{"servers":[]}}`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("settings save: %d", resp.StatusCode)
	}
	s, _ = e.deps.Settings.Get()
	if s.DNS.Final != "cf" {
		t.Fatalf("dns clobbered by settings save: %+v", s.DNS)
	}
	if s.DNSRedirectPort() != 5300 {
		t.Errorf("redirect port in tproxy: %d", s.DNSRedirectPort())
	}
	// ...and an inbound port colliding with dns-in is rejected.
	if resp := e.bearer("PUT", "/api/settings", []byte(`{"inbound_port":5300}`)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("port collision accepted: %d", resp.StatusCode)
	}

	// Nothing listens on dns-in in tests: the lookup reports the error.
	var lr dnsLookupResp
	resp = e.bearer("POST", "/api/dns/lookup", []byte(`{"domain":"example.com"}`))
	mustJSON(t, readAll(t, resp), &lr)
	if resp.StatusCode != http.StatusOK || lr.Error == "" {
		t.Errorf("lookup: %d %+v", resp.StatusCode, lr)
	}
	// The default chain (cf, no backups); no applied config → no probe ports.
	if lr.Chain != "Основной DNS" || len(lr.Servers) != 1 || lr.Servers[0].Tag != "cf" || lr.Servers[0].Status != "not_applied" {
		t.Errorf("lookup chain: %+v", lr)
	}
	// A routing domain resolves via the "ag" chain (proxied_server).
	if resp := e.bearer("PUT", "/api/settings", []byte(`{"route_domains":["example.org"]}`)); resp.StatusCode != http.StatusOK {
		t.Fatalf("route domains: %d", resp.StatusCode)
	}
	mustJSON(t, readAll(t, e.bearer("POST", "/api/dns/lookup", []byte(`{"domain":"www.example.org"}`))), &lr)
	if lr.Chain != "Заблокированные сайты" || lr.Servers[0].Tag != "ag" || !lr.Servers[0].Proxy {
		t.Errorf("routing-domain chain: %+v", lr)
	}
}
