package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
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

	if resp := e.bearer("DELETE", "/api/subs/"+added.Sub.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if list, _ := e.deps.Servers.List(); len(list) != 0 {
		t.Errorf("servers left after delete: %+v", list)
	}
	if resp := e.bearer("DELETE", "/api/subs/"+added.Sub.ID, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("second delete: %d", resp.StatusCode)
	}
}
