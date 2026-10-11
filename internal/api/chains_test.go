package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
	"github.com/CoOre/keenetic-sing-box-ui/internal/share"
)

func chainEntries() []servers.Entry {
	return []servers.Entry{
		{ID: "a", Server: share.Server{Name: "A", Type: "vless", Server: "1.1.1.1", ServerPort: 443, UUID: "u"}},
		{ID: "b", Server: share.Server{Name: "B", Type: "trojan", Server: "2.2.2.2", ServerPort: 443, Password: "p"}},
		{ID: "c", Server: share.Server{Name: "C", Type: "hysteria2", Server: "3.3.3.3", ServerPort: 443, Password: "p"}},
	}
}

func TestBuildOutbounds_Chains(t *testing.T) {
	list := chainEntries()
	chains := []servers.Chain{
		{ID: "x", Name: "two", Hops: []string{"a", "c"}},
		{ID: "y", Name: "three", Hops: []string{"c", "b", "a"}},
		{ID: "z", Name: "broken", Hops: []string{"a", "gone"}},
	}
	outs, primary, warnings, err := buildOutbounds(servers.State{Servers: list, Chains: chains, Primary: "y"})
	if err != nil {
		t.Fatal(err)
	}
	if primary != "chain-three" {
		t.Errorf("primary = %q", primary)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "broken") {
		t.Errorf("warnings = %v", warnings)
	}

	type got struct {
		detour any
		role   config.OutboundRole
		server any
	}
	byTag := map[string]got{}
	var order []string
	for _, o := range outs {
		byTag[o.Tag] = got{o.Object["detour"], o.Role, o.Object["server"]}
		order = append(order, o.Tag)
	}
	want := []string{"A", "B", "C", "chain-two", "chain-three.hop2", "chain-three"}
	if !slices.Equal(order, want) {
		t.Fatalf("outbounds = %v, want %v (entry hops reuse the server, broken chain skipped)", order, want)
	}
	for tag, w := range map[string]got{
		"A":                {nil, config.RoleServer, "1.1.1.1"},
		"chain-two":        {"A", config.RoleChain, "3.3.3.3"},
		"chain-three.hop2": {"C", config.RoleHop, "2.2.2.2"},
		"chain-three":      {"chain-three.hop2", config.RoleChain, "1.1.1.1"},
	} {
		if byTag[tag] != w {
			t.Errorf("%s = %+v, want %+v", tag, byTag[tag], w)
		}
	}
	// Clones are independent maps: the server's own outbound stays detour-free.
	for _, o := range outs {
		if o.Role == config.RoleServer && o.Object["detour"] != nil {
			t.Errorf("server %s got a detour", o.Tag)
		}
	}
}

// saveServers posts the servers and returns their IDs.
func saveServers(t *testing.T, e *testEnv, bodies ...string) []string {
	t.Helper()
	var ids []string
	for _, body := range bodies {
		resp := e.bearer(http.MethodPost, "/api/servers", []byte(body))
		var saved servers.Entry
		mustJSON(t, readAll(t, resp), &saved)
		ids = append(ids, saved.ID)
	}
	return ids
}

func TestChains_SaveApplyPrimary(t *testing.T) {
	e := newEnv(t)
	fc := &fakeClash{now: map[string]string{"proxy": "direct"}}
	cs := httptest.NewServer(fc)
	t.Cleanup(cs.Close)
	e.deps.ClashAddr = cs.URL

	ids := saveServers(t, e,
		`{"name":"A","type":"vless","server":"1.1.1.1","server_port":443,"uuid":"u"}`,
		`{"name":"B","type":"trojan","server":"2.2.2.2","server_port":443,"password":"p"}`,
	)

	for name, body := range map[string]string{
		"one hop":   `{"name":"x","hops":["` + ids[0] + `"]}`,
		"unknown":   `{"name":"x","hops":["` + ids[0] + `","nope"]}`,
		"duplicate": `{"name":"x","hops":["` + ids[0] + `","` + ids[0] + `"]}`,
	} {
		resp := e.bearer(http.MethodPost, "/api/chains", []byte(body))
		if b := readAll(t, resp); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d: %s", name, resp.StatusCode, b)
		}
	}

	resp := e.bearer(http.MethodPost, "/api/chains", []byte(`{"name":"via","hops":["`+ids[0]+`","`+ids[1]+`"]}`))
	var chain servers.Chain
	if b := readAll(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("save chain: %d %s", resp.StatusCode, b)
	} else {
		mustJSON(t, b, &chain)
	}

	resp = e.bearer(http.MethodPost, "/api/servers/primary", []byte(`{"id":"`+chain.ID+`"}`))
	var pr serversPrimaryResp
	mustJSON(t, readAll(t, resp), &pr)
	if pr.Selected != "chain-via" || fc.now["proxy"] != "chain-via" {
		t.Fatalf("primary chain: %+v clash=%v", pr, fc.now)
	}

	resp = e.bearer(http.MethodGet, "/api/servers", nil)
	var lr serversListResp
	mustJSON(t, readAll(t, resp), &lr)
	if len(lr.Chains) != 1 || lr.Chains[0].Tag != "chain-via" || !lr.Chains[0].Primary || lr.Chains[0].Broken {
		t.Fatalf("list chains: %+v", lr.Chains)
	}

	resp = e.bearer(http.MethodPost, "/api/servers/apply", []byte(`{"restart":false}`))
	var ar serversApplyResp
	mustJSON(t, readAll(t, resp), &ar)
	if !ar.Applied || len(ar.Warnings) != 0 {
		t.Fatalf("apply: %+v", ar)
	}
	cfg, _ := e.deps.Config.Read()
	var parsed struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	mustJSON(t, cfg, &parsed)
	var exit, sel map[string]any
	for _, o := range parsed.Outbounds {
		switch o["tag"] {
		case "chain-via":
			exit = o
		case config.OutboundProxyTag:
			sel = o
		}
	}
	if exit == nil || exit["detour"] != "A" || exit["type"] != "trojan" {
		t.Fatalf("chain exit outbound: %v", exit)
	}
	if sel["default"] != "chain-via" {
		t.Errorf("selector default = %v", sel["default"])
	}

	// A dry run says what deleting a hop server breaks, and deletes nothing.
	resp = e.bearer(http.MethodDelete, "/api/servers/"+ids[1]+"?dry_run=1", nil)
	var del deleteResp
	mustJSON(t, readAll(t, resp), &del)
	if resp.StatusCode != http.StatusOK || len(del.Warnings) != 1 || !strings.Contains(del.Warnings[0], "основной") {
		t.Fatalf("dry run: %d %q", resp.StatusCode, del.Warnings)
	}
	if list, _ := e.deps.Servers.List(); len(list) != len(ids) {
		t.Fatal("dry run deleted the server")
	}

	// Deleting a hop server breaks the chain: apply skips it with a warning,
	// and it can't be made primary any more.
	resp = e.bearer(http.MethodDelete, "/api/servers/"+ids[1], nil)
	mustJSON(t, readAll(t, resp), &del)
	if len(del.Warnings) != 1 {
		t.Errorf("delete warnings: %q", del.Warnings)
	}
	resp = e.bearer(http.MethodPost, "/api/servers/apply", []byte(`{"restart":false}`))
	mustJSON(t, readAll(t, resp), &ar)
	if !ar.Applied || len(ar.Warnings) != 1 {
		t.Fatalf("apply with broken chain: %+v", ar)
	}
	cfg, _ = e.deps.Config.Read()
	if strings.Contains(string(cfg), `"chain-via"`) {
		t.Error("broken chain must be left out of the config")
	}
	readAll(t, e.bearer(http.MethodPost, "/api/servers/primary", []byte(`{"id":""}`)))
	resp = e.bearer(http.MethodPost, "/api/servers/primary", []byte(`{"id":"`+chain.ID+`"}`))
	if b := readAll(t, resp); resp.StatusCode != http.StatusConflict {
		t.Errorf("primary on broken chain: %d %s", resp.StatusCode, b)
	}

	resp = e.bearer(http.MethodDelete, "/api/chains/"+chain.ID, nil)
	readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("delete chain: %d", resp.StatusCode)
	}
}

// With one server and one chain there's no auto group: clearing the primary
// must select the sole server, not "auto".
func TestChains_PrimaryFallbackIgnoresChains(t *testing.T) {
	e := newEnv(t)
	fc := &fakeClash{now: map[string]string{"proxy": "direct"}}
	cs := httptest.NewServer(fc)
	t.Cleanup(cs.Close)
	e.deps.ClashAddr = cs.URL

	ids := saveServers(t, e,
		`{"name":"A","type":"vless","server":"1.1.1.1","server_port":443,"uuid":"u"}`,
		`{"name":"B","type":"trojan","server":"2.2.2.2","server_port":443,"password":"p"}`,
	)
	readAll(t, e.bearer(http.MethodPost, "/api/chains", []byte(`{"name":"via","hops":["`+ids[0]+`","`+ids[1]+`"]}`)))
	readAll(t, e.bearer(http.MethodDelete, "/api/servers/"+ids[1], nil))

	resp := e.bearer(http.MethodPost, "/api/servers/primary", []byte(`{"id":""}`))
	var pr serversPrimaryResp
	mustJSON(t, readAll(t, resp), &pr)
	if pr.Selected != "A" {
		t.Errorf("fallback = %q, want the sole server A", pr.Selected)
	}
}
