package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/proxyretry"
	"github.com/CoOre/keenetic-sing-box-ui/internal/singbox"
)

const (
	// SingBoxChangelogURL is sing-box's own changelog. GitHub release bodies
	// of sing-box are mostly "Fixes and improvements" and the releases API
	// weighs ~350 KB per release (asset lists), so the docs file — one
	// "#### <version>" section per release — is both richer and lighter.
	SingBoxChangelogURL  = "https://raw.githubusercontent.com/SagerNet/sing-box/stable/docs/changelog.md"
	singBoxChangelogPage = "https://sing-box.sagernet.org/changelog/"
	singBoxDocsBase      = "https://sing-box.sagernet.org/"

	changelogTTL = time.Hour
	// changelogMissTTL: how soon a cache lacking the version the last check
	// reported may be refetched (sing-box's docs can lag its GitHub release).
	changelogMissTTL = 10 * time.Minute
	// changelogErrTTL: back-off after a failed fetch.
	changelogErrTTL   = 2 * time.Minute
	changelogMaxBytes = 4 << 20
	changelogMaxItems = 15
)

// changelogAttemptTimeout bounds each of the direct and proxied attempts
// (DPI tends to stall connections rather than reset them). Var for tests.
var changelogAttemptTimeout = 15 * time.Second

// Release is the notes of one released version (Markdown).
type Release struct {
	Version string `json:"version"`
	Date    string `json:"date,omitempty"` // YYYY-MM-DD, when known
	Notes   string `json:"notes"`
	URL     string `json:"url,omitempty"`
}

// Changelog is what /api/update/changelog returns for one component.
type Changelog struct {
	Target  string `json:"target"`
	Current string `json:"current,omitempty"`
	// Newer: Releases are the versions after Current (newest first).
	// Otherwise Releases holds one release: the installed version itself
	// (Installed) or, when it has no notes (dev build, docs lagging), the
	// newest release before it.
	Newer     bool      `json:"newer"`
	Installed bool      `json:"installed"`
	Releases  []Release `json:"releases"`
	URL       string    `json:"url"` // full changelog page
}

type changelogCache struct {
	rels    []Release
	err     error     // last fetch failed and there was nothing to fall back to
	fetched time.Time // last fetch attempt
	nextTry time.Time // served from cache until then
}

// changelogCall is an in-flight fetch shared by concurrent requests.
type changelogCall struct {
	done chan struct{}
	rels []Release
	err  error
}

// Changelog returns release notes for target ("singbox"|"ui") relative to the
// installed version. Fetched lists are cached for changelogTTL, or until the
// last check reports a latest version the cache doesn't have yet (at most
// once per changelogMissTTL); failures back off for changelogErrTTL.
func (m *Manager) Changelog(ctx context.Context, target string) (Changelog, error) {
	var (
		cl     = Changelog{Target: target}
		latest string
		fetch  func(context.Context) ([]Release, error)
	)
	st := m.Status()
	switch target {
	case "singbox":
		cl.URL = singBoxChangelogPage
		cl.Current, latest = st.SingBox.Current, st.SingBox.Latest
		fetch = m.fetchSingBoxChangelog
	case "ui":
		cl.URL = "https://github.com/" + m.uiRepo() + "/releases"
		cl.Current, latest = strings.TrimPrefix(m.UIVersion, "v"), st.UI.Latest
		fetch = m.fetchUIReleases
	default:
		return cl, fmt.Errorf("unknown target %q (want singbox|ui)", target)
	}
	if !validVersion(cl.Current) {
		cl.Current = "" // dev/hash build: show the latest release, not "installed"
	}
	rels, err := m.cachedReleases(ctx, target, latest, fetch)
	if err != nil {
		return cl, err
	}
	cl.Releases, cl.Newer, cl.Installed = selectReleases(rels, cl.Current)
	return cl, nil
}

// cachedReleases serves target's release list from cache or joins/starts a
// fetch. The fetch runs detached from ctx: a client reloading the page must
// neither abort the download others wait on nor get "context canceled"
// cached as the outcome; each attempt is bounded by changelogAttemptTimeout.
func (m *Manager) cachedReleases(ctx context.Context, target, latest string, fetch func(context.Context) ([]Release, error)) ([]Release, error) {
	m.mu.Lock()
	c, ok := m.changelogs[target]
	now := time.Now()
	if ok && now.Before(c.nextTry) &&
		(c.err != nil || latest == "" || hasVersion(c.rels, latest) || now.Sub(c.fetched) < changelogMissTTL) {
		m.mu.Unlock()
		return c.rels, c.err
	}
	call, running := m.inflight[target]
	if !running {
		call = &changelogCall{done: make(chan struct{})}
		if m.inflight == nil {
			m.inflight = map[string]*changelogCall{}
		}
		m.inflight[target] = call
		go m.refreshReleases(target, call, fetch)
	}
	m.mu.Unlock()

	select {
	case <-call.done:
		return call.rels, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) refreshReleases(target string, call *changelogCall, fetch func(context.Context) ([]Release, error)) {
	rels, err := fetch(context.Background())
	now := time.Now()

	m.mu.Lock()
	old, ok := m.changelogs[target]
	var c changelogCache
	switch {
	case err == nil:
		c = changelogCache{rels: rels, nextTry: now.Add(changelogTTL)}
	case ok && old.rels != nil:
		c = changelogCache{rels: old.rels, nextTry: now.Add(changelogErrTTL)} // stale beats nothing
	default:
		c = changelogCache{err: err, nextTry: now.Add(changelogErrTTL)}
	}
	c.fetched = now
	if m.changelogs == nil {
		m.changelogs = map[string]changelogCache{}
	}
	m.changelogs[target] = c
	delete(m.inflight, target)
	call.rels, call.err = c.rels, c.err
	m.mu.Unlock()
	close(call.done)
}

func validVersion(v string) bool {
	_, _, ok := splitVersion(v)
	return ok
}

func hasVersion(rels []Release, v string) bool {
	for _, r := range rels {
		if CompareVersions(r.Version, v) == 0 {
			return true
		}
	}
	return false
}

// selectReleases picks the releases newer than current (rels is newest
// first). With none newer it falls back to the newest release not after
// current; installed reports whether that is current itself.
func selectReleases(rels []Release, current string) (sel []Release, newer, installed bool) {
	if current == "" {
		return headOf(rels, 1), false, false
	}
	for _, r := range rels {
		if CompareVersions(r.Version, current) > 0 {
			sel = append(sel, r)
		}
	}
	if len(sel) > 0 {
		return headOf(sel, changelogMaxItems), true, false
	}
	for _, r := range rels {
		if c := CompareVersions(r.Version, current); c <= 0 {
			return []Release{r}, false, c == 0
		}
	}
	return []Release{}, false, false
}

func headOf(rels []Release, n int) []Release {
	if len(rels) > n {
		rels = rels[:n]
	}
	return append([]Release{}, rels...)
}

// normalizeReleases drops releases whose version doesn't parse (they compare
// "equal" to everything and would scramble the order), sorts newest first and
// keeps the first of duplicate versions (sing-box's changelog repeats some
// headings).
func normalizeReleases(rels []Release) []Release {
	out := rels[:0]
	for _, r := range rels {
		if validVersion(r.Version) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return CompareVersions(out[i].Version, out[j].Version) > 0
	})
	seen := make(map[string]bool, len(out))
	uniq := out[:0]
	for _, r := range out {
		if !seen[r.Version] {
			seen[r.Version] = true
			uniq = append(uniq, r)
		}
	}
	return uniq
}

// get fetches url with client, retrying through the local sing-box proxy
// like install does (raw.githubusercontent.com is commonly interfered with).
// Each attempt is bounded by changelogAttemptTimeout.
func (m *Manager) get(ctx context.Context, client *http.Client, url, accept string) ([]byte, error) {
	var body []byte
	err := m.withProxyFallback(ctx, changelogAttemptTimeout, client, func(ctx context.Context, c *http.Client) error {
		var err error
		body, err = httpGet(ctx, c, url, accept)
		return err
	})
	return body, err
}

func ghClient(g *singbox.Github) *http.Client {
	if g == nil {
		return nil // httpGet falls back to http.DefaultClient
	}
	return g.HTTP
}

func (m *Manager) uiRepo() string {
	if m.UIGH == nil || m.UIGH.Repo == "" {
		return singbox.UIRepo
	}
	return m.UIGH.Repo
}

// httpGet GETs url. Answers the proxy can't change — 4xx other than 403/429
// (GitHub's per-IP rate limit, which another egress IP does dodge) and an
// oversized body — are marked proxyretry.Permanent.
func httpGet(ctx context.Context, c *http.Client, url, accept string) ([]byte, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 &&
			resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
			err = proxyretry.Permanent(err)
		}
		return nil, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, changelogMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > changelogMaxBytes {
		return nil, proxyretry.Permanent(fmt.Errorf("GET %s: response exceeds %d MiB", url, changelogMaxBytes>>20))
	}
	return body, nil
}

func (m *Manager) fetchSingBoxChangelog(ctx context.Context) ([]Release, error) {
	url := m.SingBoxChangelogURL
	if url == "" {
		url = SingBoxChangelogURL
	}
	body, err := m.get(ctx, ghClient(m.SingBoxGH), url, "")
	if err != nil {
		return nil, err
	}
	rels := parseSingBoxChangelog(string(body))
	if len(rels) == 0 {
		return nil, errors.New("sing-box changelog: no versions found")
	}
	return rels, nil
}

var (
	sbHeadingRe = regexp.MustCompile(`^####\s+(\S+)\s*$`)
	sbRelLinkRe = regexp.MustCompile(`\]\(/`)
)

// parseSingBoxChangelog splits sing-box's docs/changelog.md into stable
// releases (prereleases are skipped — their notes are rolled up into the
// stable release), newest first. Site-relative links are made absolute.
func parseSingBoxChangelog(md string) []Release {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	if strings.HasPrefix(md, "---\n") { // mkdocs front matter
		if end := strings.Index(md[4:], "\n---\n"); end >= 0 {
			md = md[4+end+5:]
		}
	}
	var (
		rels []Release
		cur  *Release
		buf  strings.Builder
	)
	flush := func() {
		if cur != nil && !strings.Contains(cur.Version, "-") {
			cur.Notes = sbRelLinkRe.ReplaceAllString(strings.TrimSpace(buf.String()), "]("+singBoxDocsBase)
			rels = append(rels, *cur)
		}
		buf.Reset()
	}
	for line := range strings.SplitSeq(md, "\n") {
		if mm := sbHeadingRe.FindStringSubmatch(line); mm != nil {
			flush()
			cur = &Release{Version: strings.TrimPrefix(mm[1], "v")}
			continue
		}
		if cur != nil {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()
	return normalizeReleases(rels)
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
}

// fetchUIReleases reads this UI's GitHub releases; their bodies are generated
// from commits by scripts/changelog.sh in CI.
func (m *Manager) fetchUIReleases(ctx context.Context) ([]Release, error) {
	base := m.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=30", strings.TrimRight(base, "/"), m.uiRepo())
	body, err := m.get(ctx, ghClient(m.UIGH), url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var gh []ghRelease
	if err := json.Unmarshal(body, &gh); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	rels := make([]Release, 0, len(gh))
	for _, r := range gh {
		if r.Draft || r.Prerelease {
			continue
		}
		rel := Release{
			Version: strings.TrimPrefix(r.TagName, "v"),
			Notes:   strings.TrimSpace(strings.ReplaceAll(r.Body, "\r\n", "\n")),
			URL:     r.HTMLURL,
		}
		if !r.PublishedAt.IsZero() {
			rel.Date = r.PublishedAt.Format("2006-01-02")
		}
		rels = append(rels, rel)
	}
	return normalizeReleases(rels), nil
}
