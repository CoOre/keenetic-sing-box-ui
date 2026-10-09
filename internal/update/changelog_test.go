package update

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/singbox"
)

const sbChangelog = `---
icon: material/alert-decagram
---

#### 1.15.0-alpha.2

* Alpha only

#### 1.14.3

* Fixes and improvements

#### 1.14.2

* Fix DNS

See [Route Rule](/configuration/route/rule/#x).

#### 1.14.0-rc.1

* RC only

#### 1.13.21

* Old fix

#### 1.14.2

* Older duplicate heading
`

func versions(rels []Release) string {
	var vs []string
	for _, r := range rels {
		vs = append(vs, r.Version)
	}
	return strings.Join(vs, ",")
}

func TestParseSingBoxChangelog(t *testing.T) {
	rels := parseSingBoxChangelog(sbChangelog)
	if got := versions(rels); got != "1.14.3,1.14.2,1.13.21" {
		t.Fatalf("versions = %s", got)
	}
	if rels[0].Notes != "* Fixes and improvements" {
		t.Errorf("notes[0] = %q", rels[0].Notes)
	}
	if !strings.Contains(rels[1].Notes, "](https://sing-box.sagernet.org/configuration/route/rule/#x)") {
		t.Errorf("relative link not made absolute: %q", rels[1].Notes)
	}
}

func TestSelectReleases(t *testing.T) {
	rels := parseSingBoxChangelog(sbChangelog)
	cases := []struct {
		current, want    string
		newer, installed bool
	}{
		{"1.13.21", "1.14.3,1.14.2", true, false},
		{"1.14.2", "1.14.3", true, false},
		{"1.14.3", "1.14.3", false, true},
		{"1.14.1", "1.14.3,1.14.2", true, false},
		{"1.14.0", "1.14.3,1.14.2", true, false},
		{"1.14.4", "1.14.3", false, false},          // docs lag the release
		{"1.15.0-alpha.1", "1.14.3", false, false}, // prerelease ahead of all stables
		{"", "1.14.3", false, false},
	}
	for _, c := range cases {
		got, newer, installed := selectReleases(rels, c.current)
		if versions(got) != c.want || newer != c.newer || installed != c.installed {
			t.Errorf("current %q: got %s newer=%v installed=%v, want %s newer=%v installed=%v",
				c.current, versions(got), newer, installed, c.want, c.newer, c.installed)
		}
	}
	// A dev build ahead of the tag shows the tag's notes, but not as installed.
	got, newer, installed := selectReleases([]Release{{Version: "0.1.7"}, {Version: "0.1.6"}}, "0.1.7-3-gabc1234")
	if versions(got) != "0.1.7" || newer || installed {
		t.Errorf("dev build: got %s newer=%v installed=%v", versions(got), newer, installed)
	}
	// "-dirty" is the tag itself.
	if _, _, installed := selectReleases([]Release{{Version: "0.1.6"}}, "0.1.6-dirty"); !installed {
		t.Error("0.1.6-dirty must count as installed 0.1.6")
	}
}

func TestChangelog_CanceledRequestNotCached(t *testing.T) {
	gate := make(chan struct{})
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-gate
		_, _ = w.Write([]byte(sbChangelog))
	}))
	defer srv.Close()

	gh := singbox.NewGithub("/nonexistent")
	gh.HTTP = srv.Client()
	m := &Manager{SingBoxGH: gh, SingBoxChangelogURL: srv.URL, Log: slog.Default()}
	m.status.SingBox = Component{Current: "1.14.2"}

	// The client goes away mid-download.
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := m.Changelog(ctx, "singbox"); errc <- err }()
	for hits.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-errc; err == nil {
		t.Fatal("canceled request must return an error")
	}
	// A concurrent request joins the same fetch instead of starting another.
	done := make(chan Changelog, 1)
	go func() { cl, _ := m.Changelog(context.Background(), "singbox"); done <- cl }()
	time.Sleep(20 * time.Millisecond)
	close(gate)
	if cl := <-done; versions(cl.Releases) != "1.14.3" {
		t.Errorf("joined fetch: got %+v", cl)
	}
	if hits.Load() != 1 {
		t.Errorf("want one shared fetch, got %d", hits.Load())
	}
	// And the detached fetch's success is what got cached.
	if cl, err := m.Changelog(context.Background(), "singbox"); err != nil || versions(cl.Releases) != "1.14.3" {
		t.Errorf("after cancel: %+v, %v", cl, err)
	}
	if hits.Load() != 1 {
		t.Errorf("result must be cached, fetched %d times", hits.Load())
	}
}

func TestChangelog_UIWithoutSingBoxClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v0.1.6", "body": "- x"}})
	}))
	defer srv.Close()
	ui := singbox.NewGithubUI("/x")
	ui.HTTP = srv.Client()
	m := &Manager{UIGH: ui, UIVersion: "v0.1.6", BaseURL: srv.URL, Log: slog.Default()} // no SingBoxGH
	cl, err := m.Changelog(context.Background(), "ui")
	if err != nil || !cl.Installed || versions(cl.Releases) != "0.1.6" {
		t.Errorf("got %+v, %v", cl, err)
	}
}

func TestChangelog_SingBoxCachedUntilNewLatest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(sbChangelog))
	}))
	defer srv.Close()

	gh := singbox.NewGithub("/nonexistent")
	gh.HTTP = srv.Client()
	m := &Manager{SingBoxGH: gh, SingBoxChangelogURL: srv.URL, Log: slog.Default()}
	m.status.SingBox = Component{Current: "1.14.2", Latest: "1.14.3"}

	cl, err := m.Changelog(context.Background(), "singbox")
	if err != nil {
		t.Fatal(err)
	}
	if !cl.Newer || versions(cl.Releases) != "1.14.3" || cl.Current != "1.14.2" {
		t.Errorf("got %+v", cl)
	}
	if _, err := m.Changelog(context.Background(), "singbox"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("second call must hit the cache, fetched %d times", hits.Load())
	}
	// A check reporting a version the cache lacks refetches, but at most once
	// per changelogMissTTL (the docs file may lag the GitHub release).
	m.status.SingBox.Latest = "1.14.4"
	if _, err := m.Changelog(context.Background(), "singbox"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("missing latest within changelogMissTTL must not refetch, fetched %d times", hits.Load())
	}
	ageCache(m, "singbox", changelogMissTTL+time.Second)
	if _, err := m.Changelog(context.Background(), "singbox"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Errorf("missing latest past changelogMissTTL must refetch, fetched %d times", hits.Load())
	}
}

// ageCache pretends the cached fetch for target happened d ago.
func ageCache(m *Manager, target string, d time.Duration) {
	m.mu.Lock()
	c := m.changelogs[target]
	c.fetched = c.fetched.Add(-d)
	m.changelogs[target] = c
	m.mu.Unlock()
}

func expireCache(m *Manager, target string) {
	m.mu.Lock()
	c := m.changelogs[target]
	c.nextTry = time.Time{}
	m.changelogs[target] = c
	m.mu.Unlock()
}

func TestChangelog_FailureBacksOff(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			http.Error(w, "nope", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(sbChangelog))
	}))
	defer srv.Close()

	gh := singbox.NewGithub("/nonexistent")
	gh.HTTP = srv.Client()
	m := &Manager{SingBoxGH: gh, SingBoxChangelogURL: srv.URL, Log: slog.Default()}
	m.status.SingBox = Component{Current: "1.14.2"}
	ctx := context.Background()

	// No cache: the error is remembered, not retried on every call.
	fail.Store(true)
	for range 2 {
		if _, err := m.Changelog(ctx, "singbox"); err == nil {
			t.Fatal("want error")
		}
	}
	if hits.Load() != 1 {
		t.Errorf("failure must back off, fetched %d times", hits.Load())
	}

	// Success, then a failed refresh: stale data is served and restamped.
	fail.Store(false)
	expireCache(m, "singbox")
	if _, err := m.Changelog(ctx, "singbox"); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	expireCache(m, "singbox")
	for range 2 {
		cl, err := m.Changelog(ctx, "singbox")
		if err != nil || versions(cl.Releases) != "1.14.3" {
			t.Fatalf("stale fallback: %+v, %v", cl, err)
		}
	}
	if hits.Load() != 3 {
		t.Errorf("failed refresh must back off, fetched %d times", hits.Load())
	}
}

func TestChangelog_AttemptTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // a DPI-stalled connection
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	old := changelogAttemptTimeout
	changelogAttemptTimeout = 100 * time.Millisecond
	defer func() { changelogAttemptTimeout = old }()

	gh := singbox.NewGithub("/nonexistent")
	gh.HTTP = srv.Client()
	m := &Manager{SingBoxGH: gh, SingBoxChangelogURL: srv.URL, Log: slog.Default()}
	start := time.Now()
	if _, err := m.Changelog(context.Background(), "singbox"); err == nil {
		t.Fatal("want timeout error")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("stalled fetch took %v", d)
	}
}

func TestHTTPGet_TooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, changelogMaxBytes+1))
	}))
	defer srv.Close()
	if _, err := httpGet(context.Background(), srv.Client(), srv.URL, ""); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Errorf("want size error, got %v", err)
	}
}

func TestNormalizeReleases(t *testing.T) {
	rels := normalizeReleases([]Release{
		{Version: "1.12.14", Notes: "first"},
		{Version: "nightly"},
		{Version: "1.13.0"},
		{Version: "1.12.14", Notes: "second"},
		{Version: "1.12.2"},
	})
	if got := versions(rels); got != "1.13.0,1.12.14,1.12.2" {
		t.Fatalf("versions = %s", got)
	}
	if rels[1].Notes != "first" {
		t.Errorf("duplicate must keep the first occurrence, got %q", rels[1].Notes)
	}
}

func TestChangelog_UnparsableCurrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v0.1.6", "body": "- x"}})
	}))
	defer srv.Close()
	m := &Manager{UIGH: &singbox.Github{HTTP: srv.Client(), Repo: singbox.UIRepo}, UIVersion: "9e31bac", BaseURL: srv.URL, Log: slog.Default()}
	cl, err := m.Changelog(context.Background(), "ui")
	if err != nil {
		t.Fatal(err)
	}
	if cl.Current != "" || cl.Newer || versions(cl.Releases) != "0.1.6" {
		t.Errorf("got %+v", cl)
	}
}

func TestChangelog_UIReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+singbox.UIRepo+"/releases" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "v0.2.0-rc.1", "body": "rc", "prerelease": true},
			{"tag_name": "v0.1.6", "body": "### Новое\r\n\r\n- dns", "html_url": "https://x/v0.1.6", "published_at": "2026-10-06T10:00:00Z"},
			{"tag_name": "v0.1.7", "body": "- subs", "published_at": "2026-10-09T10:00:00Z"},
			{"tag_name": "v0.1.5", "body": "- hy2"},
		})
	}))
	defer srv.Close()

	ui := singbox.NewGithubUI("/nonexistent")
	ui.HTTP = srv.Client()
	m := &Manager{UIGH: ui, UIVersion: "v0.1.5", BaseURL: srv.URL, Log: slog.Default()}

	cl, err := m.Changelog(context.Background(), "ui")
	if err != nil {
		t.Fatal(err)
	}
	if !cl.Newer || versions(cl.Releases) != "0.1.7,0.1.6" {
		t.Fatalf("got %+v", cl)
	}
	r := cl.Releases[1]
	if r.Notes != "### Новое\n\n- dns" || r.Date != "2026-10-06" || r.URL != "https://x/v0.1.6" {
		t.Errorf("release = %+v", r)
	}
	if cl.URL != "https://github.com/"+singbox.UIRepo+"/releases" {
		t.Errorf("url = %s", cl.URL)
	}
}

func TestChangelog_UnknownTarget(t *testing.T) {
	m := &Manager{Log: slog.Default()}
	if _, err := m.Changelog(context.Background(), "nope"); err == nil {
		t.Error("want error for unknown target")
	}
}
