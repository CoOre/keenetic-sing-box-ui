package clash

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxy_StripsPrefixAndInjectsBearer(t *testing.T) {
	var gotPath, gotAuth, gotCookie string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCookie = r.Header.Get("Cookie")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer backend.Close()

	p, err := New(backend.URL, "s3cr3t", "/api/clash")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/clash/proxies", nil)
	req.Header.Set("Cookie", "ksbui_session=leak")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	if gotPath != "/proxies" {
		t.Errorf("path: got %q want /proxies", gotPath)
	}
	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("auth: got %q", gotAuth)
	}
	if gotCookie != "" {
		t.Errorf("cookie leaked to backend: %q", gotCookie)
	}
}

func TestProxy_RootPrefix(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
	}))
	defer backend.Close()

	p, _ := New(backend.URL, "", "/api/clash")
	req := httptest.NewRequest(http.MethodGet, "/api/clash", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if gotPath != "/" {
		t.Errorf("path: got %q want /", gotPath)
	}
}

func TestProxy_AddrWithoutScheme(t *testing.T) {
	if _, err := New("127.0.0.1:9090", "x", "/api/clash"); err != nil {
		t.Fatalf("expected scheme to be added, got %v", err)
	}
}
