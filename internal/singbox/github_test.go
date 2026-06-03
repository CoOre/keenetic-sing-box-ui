package singbox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func makeArchive(t *testing.T, dirName, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	mustHdr := func(name string, mode int64, size int64, typeflag byte) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: size, Typeflag: typeflag}); err != nil {
			t.Fatalf("hdr: %v", err)
		}
	}

	mustHdr(dirName+"/", 0o755, 0, tar.TypeDir)
	mustHdr(dirName+"/LICENSE", 0o644, int64(len("MIT")), tar.TypeReg)
	if _, err := tw.Write([]byte("MIT")); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustHdr(dirName+"/sing-box", 0o755, int64(len(content)), tar.TypeReg)
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractSingBox(t *testing.T) {
	tmp := t.TempDir()
	arc := filepath.Join(tmp, "a.tar.gz")
	if err := os.WriteFile(arc, makeArchive(t, "sing-box-1.10.7-linux-arm64", "BINARY"), 0o644); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	out, err := extractSingBox(arc, tmp)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	body, _ := os.ReadFile(out)
	if string(body) != "BINARY" {
		t.Errorf("unexpected content: %q", body)
	}
}

func TestInstall_RoundTrip(t *testing.T) {
	archive := makeArchive(t, "sing-box-1.10.7-linux-arm64", "BINARY")
	sum := sha256.Sum256(archive)
	hexSum := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "opt", "bin", "sing-box")
	g := &Github{HTTP: srv.Client(), Repo: DefaultRepo, DestBin: dest}

	err := g.Install(context.Background(), Asset{URL: srv.URL + "/archive.tar.gz", SHA256: hexSum})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if string(body) != "BINARY" {
		t.Errorf("installed content mismatch: %q", body)
	}
	st, _ := os.Stat(dest)
	if st.Mode()&0o111 == 0 {
		t.Errorf("expected executable, got %s", st.Mode())
	}
}

func TestInstall_SHAMismatch(t *testing.T) {
	archive := makeArchive(t, "sing-box-x-linux-arm64", "BINARY")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	}))
	defer srv.Close()

	g := &Github{HTTP: srv.Client(), Repo: DefaultRepo, DestBin: filepath.Join(t.TempDir(), "sing-box")}
	err := g.Install(context.Background(), Asset{URL: srv.URL, SHA256: "deadbeef"})
	if err == nil {
		t.Fatal("expected SHA mismatch error")
	}
}

func TestInstall_EmptySHA_Refused(t *testing.T) {
	g := &Github{HTTP: http.DefaultClient, Repo: DefaultRepo, DestBin: "/tmp/x"}
	err := g.Install(context.Background(), Asset{URL: "http://example/x.tar.gz"})
	if err == nil {
		t.Fatal("expected error when SHA empty")
	}
}

func TestResolveLatest(t *testing.T) {
	archiveName := "sing-box-1.10.7-linux-arm64.tar.gz"

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/repos/SagerNet/sing-box/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		// Assets carry a "digest" field ("sha256:<hex>"); also include an
		// amd64 archive and a .asc companion to verify selection logic.
		fmt.Fprintf(w, `{
			"tag_name": "v1.10.7",
			"assets": [
				{"name": "sing-box-1.10.7-linux-amd64.tar.gz", "browser_download_url": %q, "digest": "sha256:aaaa"},
				{"name": %q, "browser_download_url": %q, "digest": "sha256:FEEDFACE"},
				{"name": "sing-box-1.10.7-linux-arm64.tar.gz.asc", "browser_download_url": %q, "digest": ""}
			]
		}`,
			srv.URL+"/dl/amd64",
			archiveName, srv.URL+"/dl/"+archiveName,
			srv.URL+"/dl/asc",
		)
	})

	g := &Github{HTTP: srv.Client(), Repo: DefaultRepo, DestBin: "/tmp/sing-box"}
	asset, err := g.ResolveLatest(context.Background(), srv.URL, "arm64")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if asset.SHA256 != "feedface" {
		t.Errorf("sha mismatch: got %q (want lowercased feedface)", asset.SHA256)
	}
	if asset.Version != "1.10.7" {
		t.Errorf("version: got %q", asset.Version)
	}
	if asset.Name != archiveName {
		t.Errorf("name: got %q", asset.Name)
	}
}

func TestResolveLatest_NoDigest(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/repos/SagerNet/sing-box/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tag_name":"v1.0.0","assets":[
			{"name":"sing-box-1.0.0-linux-arm64.tar.gz","browser_download_url":"http://x/a.tgz"}
		]}`)
	})
	g := &Github{HTTP: srv.Client(), Repo: DefaultRepo, DestBin: "/tmp/sing-box"}
	if _, err := g.ResolveLatest(context.Background(), srv.URL, "arm64"); err == nil {
		t.Fatal("expected error when asset has no digest")
	}
}

func TestSHA256FromDigest(t *testing.T) {
	if s, err := sha256FromDigest("sha256:ABCDEF"); err != nil || s != "abcdef" {
		t.Errorf("got %q, %v", s, err)
	}
	if _, err := sha256FromDigest(""); err == nil {
		t.Error("empty digest must error")
	}
	if _, err := sha256FromDigest("md5:abc"); err == nil {
		t.Error("non-sha256 digest must error")
	}
}
