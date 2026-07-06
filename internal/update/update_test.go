package update

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/singbox"
)

const fakeDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeRelease serves a GitHub /releases/latest response with one archive
// asset of the given name.
func fakeRelease(t *testing.T, tag, assetName string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag,
			"assets": []map[string]any{
				{
					"name":                 assetName,
					"browser_download_url": "http://example.invalid/" + assetName,
					"digest":               fakeDigest,
				},
			},
		})
	}))
}

func uiArch() string {
	if runtime.GOARCH == "arm64" {
		return "aarch64"
	}
	return runtime.GOARCH
}

func TestCheckUI_UpdateAvailable(t *testing.T) {
	srv := fakeRelease(t, "v0.2.0", fmt.Sprintf("keenetic-sing-box-ui_v0.2.0_%s.tar.gz", uiArch()))
	defer srv.Close()

	ui := singbox.NewGithubUI("/nonexistent/bin")
	ui.HTTP = srv.Client()

	m := &Manager{UIGH: ui, UIVersion: "v0.1.3", BaseURL: srv.URL, Log: slog.Default()}
	c := m.checkUI(context.Background())
	if c.Error != "" {
		t.Fatalf("unexpected error: %s", c.Error)
	}
	if c.Latest != "0.2.0" || !c.Available {
		t.Errorf("got %+v, want latest 0.2.0 available", c)
	}
}

func TestCheckUI_DevBuildAheadOfTag(t *testing.T) {
	srv := fakeRelease(t, "v0.1.3", fmt.Sprintf("keenetic-sing-box-ui_v0.1.3_%s.tar.gz", uiArch()))
	defer srv.Close()

	ui := singbox.NewGithubUI("/nonexistent/bin")
	ui.HTTP = srv.Client()

	m := &Manager{UIGH: ui, UIVersion: "v0.1.3-5-gabc1234", BaseURL: srv.URL, Log: slog.Default()}
	c := m.checkUI(context.Background())
	if c.Available {
		t.Errorf("dev build ahead of tag must not report an update: %+v", c)
	}
}
