// Package backup implements full state export/import: a single tar.gz archive
// holding everything needed to rebuild the router setup from scratch — the
// generated sing-box config plus the UI's own state (admin token/password,
// servers, settings, URL-list sources, TLS keypair). The sing-box config alone
// is not enough: it is *generated* from the servers/settings/lists stores, and
// the UI credentials live outside it entirely.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/auth"
	"github.com/CoOre/keenetic-sing-box-ui/internal/lists"
	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
)

// FormatVersion is bumped when the archive layout changes incompatibly.
// Import refuses archives with a newer format than it understands.
const FormatVersion = 1

// Size caps: lists.json carries cached list entries (a large CIDR list is a
// few MB), everything else is small. The caps bound memory on the router
// (GOMEMLIMIT is 120 MiB) and reject hostile archives outright.
const (
	maxFileBytes  = 16 << 20
	maxTotalBytes = 32 << 20
)

const metaName = "meta.json"

// Meta identifies the archive: format version, origin UI version, timestamp.
type Meta struct {
	Format    int       `json:"format"`
	UIVersion string    `json:"ui_version,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Manager knows where each piece of state lives on disk.
type Manager struct {
	UIConfigPath      string // /opt/etc/keenetic-sing-box-ui/config.json
	ServersPath       string // …/servers.json
	SettingsPath      string // …/singbox-settings.json
	ListsPath         string // …/lists.json
	TLSCertPath       string // …/tls/cert.pem
	TLSKeyPath        string // …/tls/key.pem
	SingBoxConfigPath string // /opt/etc/sing-box/config.json

	UIVersion string
	Now       func() time.Time // nil = time.Now
}

// entry maps one archive member to its on-disk location and validator.
type entry struct {
	Name     string // path inside the archive
	Path     string // destination on disk
	Mode     os.FileMode
	Validate func([]byte) error
}

func (m *Manager) entries() []entry {
	return []entry{
		{"ui/config.json", m.UIConfigPath, 0o600, validateUIConfig},
		{"ui/servers.json", m.ServersPath, 0o600, validateServers},
		{"ui/singbox-settings.json", m.SettingsPath, 0o600, validateSettings},
		{"ui/lists.json", m.ListsPath, 0o600, validateLists},
		{"ui/tls/cert.pem", m.TLSCertPath, 0o600, validatePEM},
		{"ui/tls/key.pem", m.TLSKeyPath, 0o600, validatePEM},
		{"singbox/config.json", m.SingBoxConfigPath, 0o644, validateJSON},
	}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Export streams a tar.gz archive of all existing state files to w. Files
// that don't exist yet (e.g. no servers added) are simply omitted.
func (m *Manager) Export(w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	meta, err := json.MarshalIndent(Meta{
		Format:    FormatVersion,
		UIVersion: m.UIVersion,
		CreatedAt: m.now().UTC(),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := writeMember(tw, metaName, meta, m.now()); err != nil {
		return err
	}

	for _, e := range m.entries() {
		if e.Path == "" {
			continue
		}
		body, err := os.ReadFile(e.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read %s: %w", e.Path, err)
		}
		if err := writeMember(tw, e.Name, body, m.now()); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeMember(tw *tar.Writer, name string, body []byte, mod time.Time) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o600,
		Size:    int64(len(body)),
		ModTime: mod,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(body)
	return err
}

// Result reports what an import did.
type Result struct {
	Meta     Meta     `json:"meta"`
	Restored []string `json:"restored"`          // archive member names written to disk
	Skipped  []string `json:"skipped,omitempty"` // unknown members ignored
	// UIRestartNeeded is set when ui/config.json was restored: the admin
	// token, password hash and listeners are read once at process start, so
	// the imported values only take effect after a UI restart.
	UIRestartNeeded bool `json:"ui_restart_needed"`
}

// Import reads a tar.gz archive produced by Export, validates every member,
// and only then writes them to disk (each atomically via rename). A malformed
// archive therefore leaves the current state untouched.
func (m *Manager) Import(r io.Reader) (Result, error) {
	var res Result
	gz, err := gzip.NewReader(r)
	if err != nil {
		return res, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	known := map[string]entry{}
	for _, e := range m.entries() {
		known[e.Name] = e
	}

	staged := map[string][]byte{}
	var total int64
	var haveMeta bool
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return res, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		if hdr.Size > maxFileBytes {
			return res, fmt.Errorf("%s: too large (%d bytes)", name, hdr.Size)
		}
		total += hdr.Size
		if total > maxTotalBytes {
			return res, errors.New("archive too large")
		}
		body, err := io.ReadAll(io.LimitReader(tr, maxFileBytes+1))
		if err != nil {
			return res, fmt.Errorf("read %s: %w", name, err)
		}
		if len(body) > maxFileBytes {
			return res, fmt.Errorf("%s: too large", name)
		}

		if name == metaName {
			if err := json.Unmarshal(body, &res.Meta); err != nil {
				return res, fmt.Errorf("parse %s: %w", metaName, err)
			}
			if res.Meta.Format > FormatVersion {
				return res, fmt.Errorf("archive format %d is newer than supported %d — update the UI first", res.Meta.Format, FormatVersion)
			}
			haveMeta = true
			continue
		}
		e, ok := known[name]
		if !ok {
			res.Skipped = append(res.Skipped, name)
			continue
		}
		if err := e.Validate(body); err != nil {
			return res, fmt.Errorf("%s: %w", name, err)
		}
		staged[name] = body
	}
	if !haveMeta {
		return res, errors.New("meta.json missing — not a keenetic-sing-box-ui backup archive")
	}
	if len(staged) == 0 {
		return res, errors.New("archive contains no restorable files")
	}

	// All members validated — commit. Each write is atomic (tmp+rename); a
	// mid-commit I/O error can leave a partially restored set, which the
	// returned Restored list makes visible.
	names := make([]string, 0, len(staged))
	for name := range staged {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := known[name]
		if e.Path == "" {
			continue
		}
		if err := writeFileAtomic(e.Path, staged[name], e.Mode); err != nil {
			return res, fmt.Errorf("write %s: %w", e.Path, err)
		}
		res.Restored = append(res.Restored, name)
		if name == "ui/config.json" {
			res.UIRestartNeeded = true
		}
	}
	return res, nil
}

func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, body, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// --- validators: reject archives whose members don't parse as what they
// claim to be, before anything touches disk ---

func validateJSON(b []byte) error {
	if !json.Valid(b) {
		return errors.New("not valid JSON")
	}
	return nil
}

func validateUIConfig(b []byte) error {
	var cfg auth.UIConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	if cfg.AdminToken == "" {
		return errors.New("admin_token is empty")
	}
	return nil
}

func validateServers(b []byte) error {
	var list []servers.Entry
	return json.Unmarshal(b, &list)
}

func validateSettings(b []byte) error {
	var s settings.Settings
	return json.Unmarshal(b, &s)
}

func validateLists(b []byte) error {
	var f struct {
		Sources []*lists.Source `json:"sources"`
	}
	return json.Unmarshal(b, &f)
}

func validatePEM(b []byte) error {
	if blk, _ := pem.Decode(b); blk == nil {
		return errors.New("not valid PEM")
	}
	return nil
}
