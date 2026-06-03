package singbox

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultRepo = "SagerNet/sing-box"
)

type Github struct {
	HTTP    *http.Client
	Repo    string
	DestBin string
}

func NewGithub(destBin string) *Github {
	return &Github{HTTP: http.DefaultClient, Repo: DefaultRepo, DestBin: destBin}
}

type Asset struct {
	URL     string `json:"url"`
	Name    string `json:"name"`
	SHA256  string `json:"sha256,omitempty"`
	Version string `json:"version,omitempty"`
}

type apiAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	// Digest is provided by the GitHub API as "sha256:<hex>" for release
	// assets. sing-box ships no separate checksums file, so this is our
	// source of truth for integrity verification.
	Digest string `json:"digest"`
}

type apiRelease struct {
	TagName string     `json:"tag_name"`
	Assets  []apiAsset `json:"assets"`
}

// ResolveLatest queries the GitHub releases API for the latest sing-box
// release, picks the archive matching linux/<arch>, and reads its SHA256
// from the asset's "digest" field.
func (g *Github) ResolveLatest(ctx context.Context, baseURL, arch string) (Asset, error) {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(baseURL, "/"), g.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Asset{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return Asset{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Asset{}, fmt.Errorf("github releases: status %d", resp.StatusCode)
	}
	var rel apiRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Asset{}, fmt.Errorf("decode release: %w", err)
	}

	// Prefer the plain .tar.gz; skip companions like .tar.gz.asc/.sig.
	archiveSuffix := fmt.Sprintf("linux-%s.tar.gz", arch)
	var archive *apiAsset
	for i, a := range rel.Assets {
		if strings.HasSuffix(a.Name, archiveSuffix) {
			archive = &rel.Assets[i]
			break
		}
	}
	if archive == nil {
		return Asset{}, fmt.Errorf("no archive for linux/%s in release %s", arch, rel.TagName)
	}

	sum, err := sha256FromDigest(archive.Digest)
	if err != nil {
		return Asset{}, fmt.Errorf("%s: %w", archive.Name, err)
	}
	return Asset{
		URL:     archive.BrowserDownloadURL,
		Name:    archive.Name,
		SHA256:  sum,
		Version: strings.TrimPrefix(rel.TagName, "v"),
	}, nil
}

// sha256FromDigest parses a GitHub asset digest like "sha256:<hex>" and
// returns the lowercase hex. Only sha256 is accepted.
func sha256FromDigest(digest string) (string, error) {
	if digest == "" {
		return "", errors.New("release asset has no digest; cannot verify integrity")
	}
	algo, hex, ok := strings.Cut(digest, ":")
	if !ok || !strings.EqualFold(algo, "sha256") || hex == "" {
		return "", fmt.Errorf("unsupported digest %q", digest)
	}
	return strings.ToLower(hex), nil
}

// Install downloads the archive, verifies SHA256, extracts the sing-box
// binary, and writes it atomically to DestBin with mode 0755.
func (g *Github) Install(ctx context.Context, asset Asset) error {
	if asset.URL == "" {
		return errors.New("empty asset URL")
	}
	if asset.SHA256 == "" {
		return errors.New("empty SHA256; refusing to install unverified binary")
	}
	if g.DestBin == "" {
		return errors.New("empty DestBin")
	}

	tmpDir, err := os.MkdirTemp("", "sing-box-dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, "archive.tar.gz")
	if err := g.download(ctx, asset.URL, archivePath, asset.SHA256); err != nil {
		return err
	}

	binSrc, err := extractSingBox(archivePath, tmpDir)
	if err != nil {
		return err
	}
	return atomicInstall(binSrc, g.DestBin, 0o755)
}

func (g *Github) download(ctx context.Context, url, dest, expectedSHA string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	mw := io.MultiWriter(f, hasher)
	if _, err := io.Copy(mw, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, expectedSHA) {
		return fmt.Errorf("sha256 mismatch: want %s, got %s", expectedSHA, got)
	}
	return nil
}

func extractSingBox(archivePath, workDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", errors.New("sing-box binary not found in archive")
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != "sing-box" {
			continue
		}
		// Reject absolute paths and parent traversal.
		clean := filepath.Clean(hdr.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return "", fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		out := filepath.Join(workDir, "sing-box")
		fw, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(fw, tr); err != nil {
			fw.Close()
			return "", err
		}
		if err := fw.Close(); err != nil {
			return "", err
		}
		return out, nil
	}
}

func atomicInstall(src, dest string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".new"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}
