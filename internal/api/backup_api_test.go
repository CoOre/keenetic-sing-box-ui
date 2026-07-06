package api

import (
	"net/http"
	"os"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/backup"
)

func TestBackupExportImport(t *testing.T) {
	src := newEnv(t)
	if err := os.WriteFile(src.deps.Backup.UIConfigPath, []byte(`{"admin_token":"tok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src.deps.Servers.Path, []byte(`[{"id":"a1","name":"s"}]`), 0o600); err != nil {
		t.Fatal(err)
	}

	resp := src.bearer(http.MethodGet, "/api/backup/export", nil)
	arch := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status %d: %s", resp.StatusCode, arch)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("content-type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd == "" {
		t.Fatal("missing Content-Disposition")
	}

	dst := newEnv(t)
	resp = dst.bearer(http.MethodPost, "/api/backup/import", arch)
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status %d: %s", resp.StatusCode, body)
	}
	var res struct {
		backup.Result
		SingBoxRestarted bool `json:"singbox_restarted"`
		UIRestarting     bool `json:"ui_restarting"`
	}
	mustJSON(t, body, &res)
	if len(res.Restored) != 2 || !res.UIRestartNeeded {
		t.Fatalf("unexpected result: %+v", res)
	}
	// sing-box isn't installed in the test env, and Update is nil — neither
	// restart may be claimed.
	if res.SingBoxRestarted || res.UIRestarting {
		t.Fatalf("unexpected restarts: %+v", res)
	}
	got, err := os.ReadFile(dst.deps.Servers.Path)
	if err != nil || string(got) != `[{"id":"a1","name":"s"}]` {
		t.Fatalf("servers.json not restored: %q, %v", got, err)
	}
}

func TestBackupImportRejectsGarbage(t *testing.T) {
	e := newEnv(t)
	resp := e.bearer(http.MethodPost, "/api/backup/import", []byte("not an archive"))
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
}
