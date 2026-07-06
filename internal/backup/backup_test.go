package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T, root string) *Manager {
	t.Helper()
	ui := filepath.Join(root, "opt/etc/keenetic-sing-box-ui")
	return &Manager{
		UIConfigPath:      filepath.Join(ui, "config.json"),
		ServersPath:       filepath.Join(ui, "servers.json"),
		SettingsPath:      filepath.Join(ui, "singbox-settings.json"),
		ListsPath:         filepath.Join(ui, "lists.json"),
		TLSCertPath:       filepath.Join(ui, "tls", "cert.pem"),
		TLSKeyPath:        filepath.Join(ui, "tls", "key.pem"),
		SingBoxConfigPath: filepath.Join(root, "opt/etc/sing-box/config.json"),
		UIVersion:         "test",
		Now:               func() time.Time { return time.Unix(1700000000, 0) },
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

const testPEM = "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"

func seedState(t *testing.T, m *Manager) {
	t.Helper()
	mustWrite(t, m.UIConfigPath, `{"admin_token":"tok123","password_hash":"h","clash_secret":"s"}`)
	mustWrite(t, m.ServersPath, `[{"id":"abc","name":"srv"}]`)
	mustWrite(t, m.SettingsPath, `{"inbound_mode":"tproxy","inbound_port":1081}`)
	mustWrite(t, m.ListsPath, `{"sources":[{"id":"x","url":"http://e/l.txt","type":"cidr","enabled":true}]}`)
	mustWrite(t, m.TLSCertPath, testPEM)
	mustWrite(t, m.TLSKeyPath, testPEM)
	mustWrite(t, m.SingBoxConfigPath, `{"log":{"level":"info"}}`)
}

func TestExportImportRoundtrip(t *testing.T) {
	src := testManager(t, t.TempDir())
	seedState(t, src)

	var buf bytes.Buffer
	if err := src.Export(&buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	dst := testManager(t, t.TempDir())
	res, err := dst.Import(&buf)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Restored) != 7 {
		t.Fatalf("restored %d files, want 7: %v", len(res.Restored), res.Restored)
	}
	if !res.UIRestartNeeded {
		t.Fatal("UIRestartNeeded should be set when ui/config.json is restored")
	}
	if res.Meta.Format != FormatVersion || res.Meta.UIVersion != "test" {
		t.Fatalf("meta mismatch: %+v", res.Meta)
	}

	for _, pair := range [][2]string{
		{src.UIConfigPath, dst.UIConfigPath},
		{src.ServersPath, dst.ServersPath},
		{src.SettingsPath, dst.SettingsPath},
		{src.ListsPath, dst.ListsPath},
		{src.TLSCertPath, dst.TLSCertPath},
		{src.TLSKeyPath, dst.TLSKeyPath},
		{src.SingBoxConfigPath, dst.SingBoxConfigPath},
	} {
		want, _ := os.ReadFile(pair[0])
		got, err := os.ReadFile(pair[1])
		if err != nil {
			t.Fatalf("read %s: %v", pair[1], err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("content mismatch for %s", pair[1])
		}
	}

	st, _ := os.Stat(dst.UIConfigPath)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("ui config mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestExportSkipsMissingFiles(t *testing.T) {
	src := testManager(t, t.TempDir())
	mustWrite(t, src.UIConfigPath, `{"admin_token":"tok123"}`)

	var buf bytes.Buffer
	if err := src.Export(&buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	dst := testManager(t, t.TempDir())
	res, err := dst.Import(&buf)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Restored) != 1 || res.Restored[0] != "ui/config.json" {
		t.Fatalf("restored = %v, want only ui/config.json", res.Restored)
	}
}

// makeArchive builds a tar.gz with the given members, meta.json included
// unless withMeta is false.
func makeArchive(t *testing.T, withMeta bool, members map[string]string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if withMeta {
		add("meta.json", `{"format":1,"created_at":"2024-01-01T00:00:00Z"}`)
	}
	for name, body := range members {
		add(name, body)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func TestImportRejects(t *testing.T) {
	cases := []struct {
		name    string
		archive *bytes.Buffer
		wantErr string
	}{
		{"not gzip", bytes.NewBufferString("plain text"), "not a gzip archive"},
		{"no meta", makeArchive(t, false, map[string]string{"ui/servers.json": "[]"}), "meta.json missing"},
		{"nothing restorable", makeArchive(t, true, nil), "no restorable files"},
		{"future format", func() *bytes.Buffer {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			tw := tar.NewWriter(gz)
			meta := `{"format":99}`
			_ = tw.WriteHeader(&tar.Header{Name: "meta.json", Mode: 0o600, Size: int64(len(meta))})
			_, _ = tw.Write([]byte(meta))
			_ = tw.Close()
			_ = gz.Close()
			return &buf
		}(), "newer than supported"},
		{"invalid ui config", makeArchive(t, true, map[string]string{"ui/config.json": `{"admin_token":""}`}), "admin_token is empty"},
		{"invalid json", makeArchive(t, true, map[string]string{"singbox/config.json": "{oops"}), "not valid JSON"},
		{"invalid pem", makeArchive(t, true, map[string]string{"ui/tls/cert.pem": "nope"}), "not valid PEM"},
		{"invalid servers", makeArchive(t, true, map[string]string{"ui/servers.json": `{"not":"a list"}`}), "ui/servers.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := testManager(t, t.TempDir())
			_, err := m.Import(tc.archive)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			// Nothing must have been written.
			if _, serr := os.Stat(m.UIConfigPath); !os.IsNotExist(serr) {
				t.Fatalf("ui config written despite error: %v", serr)
			}
		})
	}
}

func TestImportSkipsUnknownMembers(t *testing.T) {
	arch := makeArchive(t, true, map[string]string{
		"ui/servers.json": `[]`,
		"evil/../../path": "x",
		"unknown.txt":     "y",
	})
	m := testManager(t, t.TempDir())
	res, err := m.Import(arch)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(res.Restored) != 1 || res.Restored[0] != "ui/servers.json" {
		t.Fatalf("restored = %v", res.Restored)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("skipped = %v, want 2 entries", res.Skipped)
	}
	if res.UIRestartNeeded {
		t.Fatal("UIRestartNeeded should be false without ui/config.json")
	}
}

func TestImportValidatesBeforeWriting(t *testing.T) {
	// A valid member alongside an invalid one: nothing may be committed.
	arch := makeArchive(t, true, map[string]string{
		"ui/servers.json":     `[]`,
		"singbox/config.json": "{broken",
	})
	m := testManager(t, t.TempDir())
	if _, err := m.Import(arch); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(m.ServersPath); !os.IsNotExist(err) {
		t.Fatal("servers.json written despite a failed import")
	}
}
