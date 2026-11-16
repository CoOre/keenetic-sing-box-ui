package subs

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
)

func TestFetch_HWIDRedirect(t *testing.T) {
	const hwid = "manual_hwid_1234"
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-hwid") != hwid || r.Header.Get("x-device-os") != "Linux" || r.UserAgent() != "v2rayN/7.0" {
			t.Errorf("identity lost on redirect: %v", r.Header)
		}
		w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(linkTrojan))))
	}))
	defer target.Close()
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, status)
			}))
			defer redirect.Close()
			got, err := Fetch(context.Background(), redirect.Client(), redirect.URL, FetchOptions{UserAgent: "v2rayN/7.0", HWID: hwid})
			if err != nil || len(got.Servers) != 1 {
				t.Fatalf("redirect: %+v %v", got, err)
			}
		})
	}
}

func TestRunner_OversizeResponseKeepsServers(t *testing.T) {
	prefix := linkTrojan + "\n" + linkVLESS + "\n#"
	body := prefix + strings.Repeat("x", maxBody-len(prefix)) // exactly at the limit is valid
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(body))
	}))
	defer provider.Close()
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "subs.json"))
	sub, _ := st.Add(Subscription{URL: provider.URL})
	r := Runner{Store: st, Servers: servers.NewStore(filepath.Join(dir, "servers.json")), ProxyClient: provider.Client}
	if _, err := r.Refresh(context.Background(), sub.ID); err != nil {
		t.Fatal(err)
	}
	// A valid first link followed by a long comment must not turn a truncated
	// download into a successful refresh which removes the second server.
	body = linkTrojan + "\n#" + strings.Repeat("x", maxBody) + "\n" + linkVLESS
	if _, err := r.Refresh(context.Background(), sub.ID); err == nil {
		t.Error("oversized response accepted as a partial subscription")
	}
	list, err := r.Servers.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("servers lost after oversized response: %+v %v", list, err)
	}
	if requests != 2 {
		t.Errorf("oversized response retried: %d requests", requests)
	}
}

func TestParseBody_UnsupportedTransport(t *testing.T) {
	unsupported := "vless://11111111-2222-3333-4444-555555555555@de.example.com:443?type=xhttp&security=tls#XHTTP"
	if _, _, err := ParseBody([]byte(unsupported)); err == nil || !strings.Contains(err.Error(), "xhttp") {
		t.Fatalf("unsupported transport accepted or unexplained: %v", err)
	}
	list, skipped, err := ParseBody([]byte(linkTrojan + "\n" + unsupported))
	if err != nil || len(list) != 1 || skipped != 1 {
		t.Fatalf("mixed supported/unsupported transports: %+v, skipped=%d, err=%v", list, skipped, err)
	}
}
