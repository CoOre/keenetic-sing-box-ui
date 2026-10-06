package subs

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
)

const (
	linkVLESS  = "vless://11111111-2222-3333-4444-555555555555@de.example.com:443?security=reality&pbk=KEY&sid=ab&sni=www.apple.com&fp=chrome&flow=xtls-rprx-vision#%F0%9F%87%A9%F0%9F%87%AA%20Germany"
	linkTrojan = "trojan://secret@nl.example.com:443?sni=nl.example.com#NL"
	// Placeholder carrying the remaining quota, as Marzban/Remnawave emit.
	linkInfo = "vless://11111111-2222-3333-4444-555555555555@0.0.0.0:1?security=none#Traffic%3A%2012%20GB"
)

func TestParseBody_Base64(t *testing.T) {
	raw := strings.Join([]string{linkVLESS, linkTrojan, linkInfo, "garbage://x"}, "\n")
	for name, body := range map[string]string{
		"std":       base64.StdEncoding.EncodeToString([]byte(raw)),
		"raw-url":   base64.RawURLEncoding.EncodeToString([]byte(raw)),
		"plain":     raw,
		"wrapped":   wrap76(base64.StdEncoding.EncodeToString([]byte(raw))),
		"bom-plain": "\ufeff" + raw + "\r\n",
	} {
		srv, skipped, err := ParseBody([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(srv) != 2 || skipped != 2 {
			t.Fatalf("%s: got %d servers, %d skipped", name, len(srv), skipped)
		}
		if srv[0].Type != "vless" || srv[0].Server != "de.example.com" || srv[1].Type != "trojan" {
			t.Errorf("%s: %+v", name, srv)
		}
	}
}

func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76] + "\n")
		s = s[76:]
	}
	b.WriteString(s)
	return b.String()
}

func TestParseBody_Errors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"empty":   {"  ", "пустой"},
		"json":    {`{"outbounds":[]}`, "JSON"},
		"clash":   {"port: 7890\nproxies:\n  - name: a", "Clash"},
		"html":    {"<!DOCTYPE html><html>", "HTML"},
		"only-ph": {linkInfo, "ни одна"},
	} {
		_, _, err := ParseBody([]byte(tc.body))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v, want %q", name, err, tc.want)
		}
	}
}

func TestParseUserInfoAndTitle(t *testing.T) {
	ui := parseUserInfo("upload=100; download=2.5e3; total=10737418240; expire=1767225600")
	if ui == nil || ui.Upload != 100 || ui.Download != 2500 || ui.Total != 10737418240 || ui.Expire != 1767225600 {
		t.Errorf("userinfo: %+v", ui)
	}
	if parseUserInfo("nonsense") != nil {
		t.Error("expected nil for unparseable header")
	}
	if got := decodeTitle("base64:" + base64.StdEncoding.EncodeToString([]byte("Мой VPN"))); got != "Мой VPN" {
		t.Errorf("title: %q", got)
	}
	if got := decodeTitle("Plain"); got != "Plain" {
		t.Errorf("title: %q", got)
	}
}

func TestRunner_Refresh(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(linkVLESS + "\n" + linkTrojan))
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.UserAgent()
		if r.URL.Path == "/broken" {
			w.Write([]byte("<html>"))
			return
		}
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=3; expire=4")
		w.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte("Провайдер")))
		w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "subs.json"))
	ss := servers.NewStore(filepath.Join(dir, "servers.json"))
	r := &Runner{Store: st, Servers: ss}

	sub, _ := st.Add(Subscription{URL: srv.URL + "/sub", Enabled: true})
	res, err := r.Refresh(context.Background(), sub.ID)
	if err != nil || !res.Changed {
		t.Fatalf("refresh: %+v %v", res, err)
	}
	if gotUA != DefaultUserAgent {
		t.Errorf("UA: %q", gotUA)
	}
	got, _ := st.Get(sub.ID)
	if got.Name != "Провайдер" || got.LastCount != 2 || got.Via != "direct" || got.Info == nil || got.Info.Total != 3 || got.LastError != "" {
		t.Errorf("stored sub: %+v", got)
	}
	list, _ := ss.List()
	if len(list) != 2 || list[0].SubID != sub.ID {
		t.Fatalf("servers: %+v", list)
	}

	if res, _ := r.Refresh(context.Background(), sub.ID); res.Changed {
		t.Error("second refresh reported a change")
	}

	// A broken response records the error and keeps the servers.
	got.URL = srv.URL + "/broken"
	st.Update(got)
	if _, err := r.Refresh(context.Background(), sub.ID); err == nil {
		t.Fatal("expected error")
	}
	got, _ = st.Get(sub.ID)
	if got.LastError == "" {
		t.Error("error not recorded")
	}
	if list, _ := ss.List(); len(list) != 2 {
		t.Errorf("servers wiped on error: %d", len(list))
	}
}
