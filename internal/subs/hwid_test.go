package subs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
)

func TestValidateHWID(t *testing.T) {
	for _, value := range []string{"", "abcDEF12=-", strings.Repeat("a", 64)} {
		if err := ValidateHWID(value); err != nil {
			t.Errorf("valid %q: %v", value, err)
		}
	}
	for _, value := range []string{"short", strings.Repeat("a", 65), "кириллица123", "abcdefghij_", "abcdefghij\r\n", " abcdefghij"} {
		if ValidateHWID(value) == nil {
			t.Errorf("accepted invalid HWID %q", value)
		}
	}
}

func TestDeviceHWID_Lifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subs.json")
	// A pre-HWID file must remain readable, without generating an identity.
	os.WriteFile(path, []byte(`{"subscriptions":[{"id":"old","url":"https://example.com/sub"}]}`), 0o600)
	st := NewStore(path)
	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].HWIDEnabled {
		t.Fatalf("legacy file: %+v %v", list, err)
	}
	before, _ := os.ReadFile(path)
	if strings.Contains(string(before), "device_hwid") {
		t.Fatal("reading old state generated an identity")
	}
	hwid, err := st.DeviceHWID()
	if err != nil || len(hwid) != 32 || ValidateHWID(hwid) != nil {
		t.Fatalf("identity: %q %v", hwid, err)
	}
	// Every store operation must preserve top-level metadata.
	added, err := st.Add(Subscription{URL: "https://example.com/new"})
	if err != nil {
		t.Fatal(err)
	}
	added.Name = "renamed"
	if err := st.Update(added); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{added.ID, "old"} {
		if err := st.Delete(id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := NewStore(path).DeviceHWID()
	if err != nil || got != hwid {
		t.Fatalf("identity after deletes/restart: %q %v, want %q", got, err, hwid)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions: %v", info.Mode())
	}
	// A running store must also see an identity restored from a backup.
	restored := "restored-hwid-123456"
	os.WriteFile(path, []byte(`{"device_hwid":"`+restored+`","subscriptions":[]}`), 0o600)
	if got, err := st.DeviceHWID(); err != nil || got != restored {
		t.Fatalf("restored identity: %q %v", got, err)
	}
}

func TestDeviceHWID_ConcurrentInitialization(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "subs.json"))
	var wg sync.WaitGroup
	ids := make(chan string, 10)
	for range 10 {
		wg.Go(func() {
			id, err := st.DeviceHWID()
			if err != nil {
				t.Error(err)
			}
			ids <- id
		})
	}
	wg.Wait()
	close(ids)
	want, err := st.DeviceHWID()
	if err != nil {
		t.Fatal(err)
	}
	for id := range ids {
		if id != want {
			t.Errorf("concurrent identity: %q, want %q", id, want)
		}
	}
}

func TestRunner_HWIDPreparationFailure(t *testing.T) {
	for _, mode := range []string{"write-error", "invalid-stored", "invalid-override"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Write([]byte(linkTrojan))
			}))
			defer provider.Close()
			st := NewStore(filepath.Join(t.TempDir(), "subs.json"))
			sub, _ := st.Add(Subscription{URL: provider.URL, HWIDEnabled: true})
			switch mode {
			case "write-error":
				os.Mkdir(st.Path+".new", 0o700)
			case "invalid-stored":
				body, _ := json.Marshal(file{DeviceHWID: "broken", Subscriptions: []*Subscription{sub}})
				os.WriteFile(st.Path, body, 0o600)
			case "invalid-override":
				sub.HWID = "broken"
				st.Update(sub)
			}
			r := Runner{Store: st}
			if _, err := r.Refresh(context.Background(), sub.ID); err == nil {
				t.Fatal("expected HWID preparation error")
			}
			if requests != 0 {
				t.Fatalf("sent %d requests before persisting a valid HWID", requests)
			}
		})
	}
}

func TestRunner_HWIDHeadersAndProxy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		override string
	}{
		{"disabled", false, "manual-hwid-123"},
		{"automatic", true, ""},
		{"manual", true, "manual-hwid-123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var identities []string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				identities = append(identities, r.Header.Get("x-hwid"))
				wantOS := ""
				if tc.enabled {
					wantOS = "Linux"
					if ValidateHWID(r.Header.Get("x-hwid")) != nil || r.Header.Get("x-hwid") == "" {
						t.Error("missing/invalid HWID")
					}
				}
				if r.UserAgent() != "v2rayN/7.0" || r.Header.Get("x-device-os") != wantOS {
					t.Errorf("headers: %v", r.Header)
				}
				if len(identities) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable) // force proxy retry
					return
				}
				w.Header().Set("x-hwid-active", "true") // informational, not a denial
				w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(linkTrojan))))
			}))
			defer provider.Close()
			dir := t.TempDir()
			st := NewStore(filepath.Join(dir, "subs.json"))
			sub, _ := st.Add(Subscription{URL: provider.URL, HWIDEnabled: tc.enabled, HWID: tc.override, UserAgent: "v2rayN/7.0", Enabled: true})
			r := Runner{Store: st, Servers: servers.NewStore(filepath.Join(dir, "servers.json")), ProxyClient: provider.Client}
			res, err := r.Refresh(context.Background(), sub.ID)
			if err != nil || res.Sub.LastCount != 1 || res.Sub.Via != "proxy" {
				t.Fatalf("refresh: %+v %v", res, err)
			}
			// Schedule refresh uses the same identity too.
			sub, _ = st.Get(sub.ID)
			sub.LastFetch = nil
			st.Update(sub)
			r.refreshDue(context.Background())
			if len(identities) != 3 || identities[0] != identities[1] || identities[1] != identities[2] {
				t.Fatalf("identities changed: %v", identities)
			}
			want := tc.override
			if !tc.enabled {
				want = ""
			} else if want == "" {
				want, _ = NewStore(st.Path).DeviceHWID()
			}
			if identities[0] != want {
				t.Errorf("HWID = %q, want %q", identities[0], want)
			}
		})
	}
}

func TestRunner_HWIDDenials(t *testing.T) {
	for header, want := range map[string]error{
		"x-hwid-not-supported":       errHWIDRequired,
		"x-hwid-max-devices-reached": errHWIDLimit,
		"x-hwid-limit":               errHWIDLimit,
	} {
		for _, status := range []int{http.StatusOK, http.StatusNotFound} {
			t.Run(fmt.Sprintf("%s/%d", header, status), func(t *testing.T) {
				deny, requests := false, 0
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					if deny {
						w.Header().Set(header, "true")
						w.WriteHeader(status)
					}
					// Even a parseable response must not override a HWID denial.
					w.Write([]byte(linkTrojan))
				}))
				defer provider.Close()
				dir := t.TempDir()
				st := NewStore(filepath.Join(dir, "subs.json"))
				sub, _ := st.Add(Subscription{URL: provider.URL})
				r := Runner{Store: st, Servers: servers.NewStore(filepath.Join(dir, "servers.json")), ProxyClient: provider.Client}
				if _, err := r.Refresh(context.Background(), sub.ID); err != nil {
					t.Fatal(err)
				}
				deny = true
				if _, err := r.Refresh(context.Background(), sub.ID); !errors.Is(err, want) {
					t.Fatalf("denial: %v, want %v", err, want)
				}
				if requests != 2 {
					t.Fatalf("denial retried through proxy: %d requests", requests)
				}
				stored, _ := st.Get(sub.ID)
				list, err := r.Servers.List()
				if err != nil || len(list) != 1 || stored.LastError != want.Error() {
					t.Fatalf("state after denial: %v %+v %v", list, stored, err)
				}
			})
		}
	}
}
