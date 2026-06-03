package singbox

import (
	_ "embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
)

//go:embed assets/S99sing-box
var initScriptTemplate []byte

type BootstrapPaths struct {
	ConfigPath    string // /opt/etc/sing-box/config.json
	InitPath      string // /opt/etc/init.d/S99sing-box
	LogPath       string // /opt/var/log/sing-box.log
	CacheDir      string // /opt/var/lib/sing-box
}

type BootstrapResult struct {
	CreatedConfig bool   `json:"created_config"`
	CreatedInit   bool   `json:"created_init"`
	ConfigPath    string `json:"config_path"`
	InitPath      string `json:"init_path"`
}

// Bootstrap lays out the directories, init script, and a baseline config.json
// needed for sing-box installed from GitHub (rather than via opkg). The init
// script is a managed artifact and is always refreshed to the current
// template; the config.json is user-owned and only created if missing.
func Bootstrap(p BootstrapPaths) (BootstrapResult, error) {
	out := BootstrapResult{ConfigPath: p.ConfigPath, InitPath: p.InitPath}

	for _, d := range []string{
		filepath.Dir(p.ConfigPath),
		filepath.Dir(p.InitPath),
		filepath.Dir(p.LogPath),
		p.CacheDir,
	} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return out, err
		}
	}

	existedBefore := fileExists(p.InitPath)
	if err := EnsureInitScript(p.InitPath); err != nil {
		return out, err
	}
	out.CreatedInit = !existedBefore

	if _, err := os.Stat(p.ConfigPath); errors.Is(err, fs.ErrNotExist) {
		body, err := config.DefaultConfig(config.DefaultOptions{
			LogPath:   p.LogPath,
			CachePath: filepath.Join(p.CacheDir, "cache.db"),
		})
		if err != nil {
			return out, err
		}
		if _, err := writeIfMissing(p.ConfigPath, body, 0o644); err != nil {
			return out, err
		}
		out.CreatedConfig = true
	} else if err != nil {
		return out, err
	}

	return out, nil
}

// EnsureInitScript writes the current init-script template to path with mode
// 0755, overwriting any previous version. It is a managed artifact, so it is
// always kept in sync with the embedded template (e.g. across UI upgrades).
func EnsureInitScript(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, initScriptTemplate, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func writeIfMissing(path string, content []byte, mode os.FileMode) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, content, mode); err != nil {
		return false, err
	}
	return true, nil
}
