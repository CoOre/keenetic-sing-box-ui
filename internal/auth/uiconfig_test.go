package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrInit_FirstRunCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, created, err := LoadOrInit(path, UIConfigDefaults{
		Dir:         dir,
		HTTPListen:  "0.0.0.0:9091",
		HTTPSListen: "0.0.0.0:9443",
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if !created {
		t.Error("expected created=true on first run")
	}
	if len(cfg.AdminToken) < 32 {
		t.Errorf("admin token too short: %q", cfg.AdminToken)
	}
	if cfg.AdminToken == cfg.ClashSecret {
		t.Error("admin token and clash secret must differ")
	}
	if cfg.TLSCertPath == "" || cfg.TLSKeyPath == "" {
		t.Errorf("tls paths empty: %+v", cfg)
	}

	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("expected 0600, got %o", st.Mode().Perm())
		}
	}

	var disk UIConfig
	body, _ := os.ReadFile(path)
	if err := json.Unmarshal(body, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.AdminToken != cfg.AdminToken {
		t.Error("disk token mismatch")
	}
}

func TestLoadOrInit_SecondRunReusesToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	first, _, err := LoadOrInit(path, UIConfigDefaults{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := LoadOrInit(path, UIConfigDefaults{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("expected created=false on second run")
	}
	if second.AdminToken != first.AdminToken {
		t.Error("admin token changed between runs")
	}
}

func TestLoadOrInit_AppliesMissingDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"admin_token":"a","clash_secret":"b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadOrInit(path, UIConfigDefaults{
		Dir:         dir,
		HTTPListen:  "0.0.0.0:9091",
		HTTPSListen: "0.0.0.0:9443",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPListen != "0.0.0.0:9091" || cfg.HTTPSListen != "0.0.0.0:9443" {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if cfg.SessionTTLHours != 168 {
		t.Errorf("session ttl default: %d", cfg.SessionTTLHours)
	}
}
