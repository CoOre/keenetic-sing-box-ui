package singbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapPaths(root string) BootstrapPaths {
	return BootstrapPaths{
		ConfigPath: filepath.Join(root, "opt/etc/sing-box/config.json"),
		InitPath:   filepath.Join(root, "opt/etc/init.d/S99sing-box"),
		LogPath:    filepath.Join(root, "opt/var/log/sing-box.log"),
		CacheDir:   filepath.Join(root, "opt/var/lib/sing-box"),
	}
}

func TestBootstrap_FreshLayout(t *testing.T) {
	root := t.TempDir()
	p := bootstrapPaths(root)

	res, err := Bootstrap(p)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !res.CreatedConfig || !res.CreatedInit {
		t.Errorf("expected created config+init, got %+v", res)
	}

	st, err := os.Stat(p.InitPath)
	if err != nil {
		t.Fatalf("init not created: %v", err)
	}
	if st.Mode()&0o111 == 0 {
		t.Errorf("init not executable: %s", st.Mode())
	}

	body, err := os.ReadFile(p.ConfigPath)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v", err)
	}
	if got := cfg["log"].(map[string]any)["output"]; got != p.LogPath {
		t.Errorf("log.output not customized: %v", got)
	}
	if got := cfg["experimental"].(map[string]any)["cache_file"].(map[string]any)["path"]; got != filepath.Join(p.CacheDir, "cache.db") {
		t.Errorf("cache_file.path not customized: %v", got)
	}
}

func TestBootstrap_DoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	p := bootstrapPaths(root)
	if err := os.MkdirAll(filepath.Dir(p.ConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ConfigPath, []byte(`{"user":"keep"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.InitPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.InitPath, []byte("# user"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Bootstrap(p)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Config is user-owned: must NOT be overwritten.
	if res.CreatedConfig {
		t.Errorf("config should not have been recreated: %+v", res)
	}
	body, _ := os.ReadFile(p.ConfigPath)
	if string(body) != `{"user":"keep"}` {
		t.Errorf("config overwritten: %s", body)
	}

	// Init script is a managed artifact: it IS refreshed to the template.
	if res.CreatedInit {
		t.Errorf("init existed before, CreatedInit should be false: %+v", res)
	}
	init, _ := os.ReadFile(p.InitPath)
	if string(init) == "# user" {
		t.Error("init script should have been refreshed to the template")
	}
	if !strings.Contains(string(init), "sing-box") {
		t.Errorf("refreshed init script unexpected: %s", init)
	}
}

func TestEnsureInitScript_NoRcFuncDependency(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "S99sing-box")
	if err := EnsureInitScript(p); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(p)
	// Must not *source* rc.func (the actual dependency); a mention in a
	// comment is fine.
	if strings.Contains(string(body), ". /opt/etc/init.d/rc.func") {
		t.Error("init script must not source Entware rc.func")
	}
	st, _ := os.Stat(p)
	if st.Mode()&0o111 == 0 {
		t.Errorf("init script not executable: %s", st.Mode())
	}
}
