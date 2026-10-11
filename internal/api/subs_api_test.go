package api

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
	"github.com/CoOre/keenetic-sing-box-ui/internal/subs"
)

func TestAPI_Subscriptions(t *testing.T) {
	e := newEnv(t)
	links := "vless://11111111-2222-3333-4444-555555555555@a.example.com:443?security=tls&sni=a.example.com#A\n" +
		"trojan://pw@b.example.com:443#B"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sub" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(links))))
	}))
	defer provider.Close()

	// Bad scheme is rejected up front.
	if resp := e.bearer("POST", "/api/subs", []byte(`{"url":"ftp://x"}`)); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad url: %d", resp.StatusCode)
	}
	// A first fetch that fails doesn't leave the subscription behind.
	resp := e.bearer("POST", "/api/subs", []byte(`{"url":"`+provider.URL+`/missing"}`))
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("missing: %d %s", resp.StatusCode, readAll(t, resp))
	}
	if list, _ := e.deps.Subs.List(); len(list) != 0 {
		t.Fatalf("failed sub kept: %+v", list)
	}

	resp = e.bearer("POST", "/api/subs", []byte(`{"name":"prov","url":"`+provider.URL+`/sub","interval":60,"auto_apply":true}`))
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add: %d %s", resp.StatusCode, body)
	}
	var added subsRefreshResp
	mustJSON(t, body, &added)
	if !added.Changed || added.Sub.LastCount != 2 || added.Sub.Name != "prov" {
		t.Fatalf("add resp: %s", body)
	}
	// sing-box isn't running in the test env, so auto-apply is skipped.
	if added.Apply != nil {
		t.Errorf("unexpected apply: %+v", added.Apply)
	}

	var st struct {
		Servers []servers.Entry `json:"servers"`
	}
	mustJSON(t, readAll(t, e.bearer("GET", "/api/servers", nil)), &st)
	if len(st.Servers) != 2 || st.Servers[0].SubID != added.Sub.ID {
		t.Fatalf("servers: %+v", st.Servers)
	}

	resp = e.bearer("PUT", "/api/subs/"+added.Sub.ID, []byte(`{"name":"renamed","url":"`+provider.URL+`/sub","interval":120,"enabled":false}`))
	var upd subs.Subscription
	mustJSON(t, readAll(t, resp), &upd)
	if upd.Name != "renamed" || upd.Enabled || upd.Interval != 120 || upd.LastCount != 2 {
		t.Errorf("update: %+v", upd)
	}

	resp = e.bearer("POST", "/api/subs/"+added.Sub.ID+"/refresh", nil)
	var ref subsRefreshResp
	mustJSON(t, readAll(t, resp), &ref)
	if ref.Changed {
		t.Error("refresh of unchanged sub reported a change")
	}

	if resp := e.bearer("DELETE", "/api/subs/"+added.Sub.ID, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if list, _ := e.deps.Servers.List(); len(list) != 0 {
		t.Errorf("servers left after delete: %+v", list)
	}
	if resp := e.bearer("DELETE", "/api/subs/"+added.Sub.ID, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("second delete: %d", resp.StatusCode)
	}
}

// subsEnvWithChains adds a subscription with servers A and B, a manual
// server M, and chains via = [A, B] (primary) and old = [A, M] (broken by
// deleting M). It returns the subscription ID and a setter for the provider's
// link list.
func subsEnvWithChains(t *testing.T, e *testEnv) (string, func(string)) {
	t.Helper()
	links := "vless://11111111-2222-3333-4444-555555555555@a.example.com:443?security=tls&sni=a.example.com#A\n" +
		"trojan://pw@b.example.com:443#B"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(links))
	}))
	t.Cleanup(provider.Close)

	var added subsRefreshResp
	mustJSON(t, readAll(t, e.bearer("POST", "/api/subs", []byte(`{"name":"prov","url":"`+provider.URL+`"}`))), &added)
	m := saveServers(t, e, `{"name":"M","type":"trojan","server":"9.9.9.9","server_port":443,"password":"p"}`)[0]
	list, _ := e.deps.Servers.List()
	via, err := e.deps.Servers.SaveChain(servers.Chain{Name: "via", Hops: []string{list[0].ID, list[1].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.deps.Servers.SaveChain(servers.Chain{Name: "old", Hops: []string{list[0].ID, m}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.deps.Servers.SetPrimary(via.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.deps.Servers.Delete(m, false); err != nil {
		t.Fatal(err)
	}
	return added.Sub.ID, func(l string) { links = l }
}

// A refresh that drops a chain's hop server reports that chain, even without
// auto-apply, and logs it (a background refresh has no one to show it to).
// A chain broken before the refresh isn't its news and isn't repeated.
func TestAPI_SubRefreshWarnsBrokenChains(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	e := newEnv(t)
	subID, setLinks := subsEnvWithChains(t, e)
	setLinks("vless://11111111-2222-3333-4444-555555555555@a.example.com:443?security=tls&sni=a.example.com#A")
	var ref subsRefreshResp
	mustJSON(t, readAll(t, e.bearer("POST", "/api/subs/"+subID+"/refresh", nil)), &ref)
	if !ref.Changed || len(ref.Warnings) != 1 || !strings.Contains(ref.Warnings[0], `"via"`) ||
		!strings.Contains(ref.Warnings[0], "была основной") {
		t.Fatalf("refresh warnings: %q", ref.Warnings)
	}
	if n := strings.Count(buf.String(), "refresh broke"); n != 1 {
		t.Errorf("logged %d warnings, want 1:\n%s", n, buf.String())
	}
}

// Deleting a subscription reports the chains it breaks.
func TestAPI_SubDeleteWarnsBrokenChains(t *testing.T) {
	e := newEnv(t)
	subID, _ := subsEnvWithChains(t, e)
	resp := e.bearer("DELETE", "/api/subs/"+subID+"?dry_run=1", nil)
	var preview deleteResp
	mustJSON(t, readAll(t, resp), &preview)
	if resp.StatusCode != http.StatusOK || len(preview.Warnings) != 1 {
		t.Fatalf("dry run: %d %q", resp.StatusCode, preview.Warnings)
	}
	if _, err := e.deps.Subs.Get(subID); err != nil {
		t.Fatalf("dry run deleted the subscription: %v", err)
	}
	if resp := e.bearer("DELETE", "/api/subs/missing?dry_run=1", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("dry run on a missing subscription: %d", resp.StatusCode)
	}
	resp = e.bearer("DELETE", "/api/subs/"+subID, nil)
	var del deleteResp
	mustJSON(t, readAll(t, resp), &del)
	if resp.StatusCode != http.StatusOK || len(del.Warnings) != 1 || !strings.Contains(del.Warnings[0], `"via"`) {
		t.Fatalf("delete: %d %q", resp.StatusCode, del.Warnings)
	}
}
