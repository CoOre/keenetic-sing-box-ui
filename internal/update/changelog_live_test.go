//go:build live

package update

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/singbox"
)

// TestChangelog_Live fetches the real sources: go test -tags live -run Live ./internal/update
func TestChangelog_Live(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m := &Manager{
		SingBoxGH: &singbox.Github{HTTP: http.DefaultClient},
		UIGH:      singbox.NewGithubUI("/nonexistent"),
		UIVersion: "v0.1.4",
		Log:       slog.Default(),
	}
	m.status.SingBox.Current = "1.14.0"
	for _, target := range []string{"singbox", "ui"} {
		cl, err := m.Changelog(ctx, target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if !cl.Newer || len(cl.Releases) == 0 {
			t.Fatalf("%s: want newer releases, got %+v", target, cl)
		}
		for _, r := range cl.Releases {
			t.Logf("%s %s %s: %.80q", target, r.Version, r.Date, r.Notes)
		}
	}
}
