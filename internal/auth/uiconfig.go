package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// UIConfig is persisted at /opt/etc/keenetic-sing-box-ui/config.json with
// chmod 0600. It holds the admin token (auth credential for the UI), the
// sing-box Clash API secret (so the UI's reverse proxy can authenticate
// outbound calls without exposing the secret to the browser), and listener
// settings.
type UIConfig struct {
	AdminToken      string `json:"admin_token"`
	PasswordHash    string `json:"password_hash,omitempty"`
	ClashSecret     string `json:"clash_secret"`
	HTTPListen      string `json:"http_listen"`
	HTTPSListen     string `json:"https_listen"`
	HTTPSOnly       bool   `json:"https_only"`
	SessionTTLHours int    `json:"session_ttl_hours"`
	TLSCertPath     string `json:"tls_cert_path"`
	TLSKeyPath      string `json:"tls_key_path"`
}

type UIConfigDefaults struct {
	Dir         string // /opt/etc/keenetic-sing-box-ui
	HTTPListen  string // 0.0.0.0:9091
	HTTPSListen string // 0.0.0.0:9443
	ClashSecret string // pre-existing secret to keep in sync (optional)
}

// LoadOrInit reads the config or, if missing, generates a new admin_token
// and clash_secret and writes the file with 0600. Returns the resulting
// config and a flag indicating whether it was freshly created (in which
// case the caller is expected to log the admin token once).
func LoadOrInit(path string, d UIConfigDefaults) (UIConfig, bool, error) {
	if path == "" {
		return UIConfig{}, false, errors.New("empty config path")
	}
	body, err := os.ReadFile(path)
	if err == nil {
		var cfg UIConfig
		if err := json.Unmarshal(body, &cfg); err != nil {
			return UIConfig{}, false, fmt.Errorf("parse %s: %w", path, err)
		}
		cfg.applyDefaults(d)
		return cfg, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return UIConfig{}, false, err
	}

	token, err := generateToken()
	if err != nil {
		return UIConfig{}, false, err
	}
	clashSecret := d.ClashSecret
	if clashSecret == "" {
		clashSecret, err = generateToken()
		if err != nil {
			return UIConfig{}, false, err
		}
	}
	cfg := UIConfig{
		AdminToken:      token,
		ClashSecret:     clashSecret,
		HTTPListen:      d.HTTPListen,
		HTTPSListen:     d.HTTPSListen,
		HTTPSOnly:       false,
		SessionTTLHours: 168,
		TLSCertPath:     filepath.Join(d.Dir, "tls", "cert.pem"),
		TLSKeyPath:      filepath.Join(d.Dir, "tls", "key.pem"),
	}
	cfg.applyDefaults(d)
	if err := saveConfig(path, cfg); err != nil {
		return UIConfig{}, false, err
	}
	return cfg, true, nil
}

func (c *UIConfig) applyDefaults(d UIConfigDefaults) {
	if c.HTTPListen == "" {
		c.HTTPListen = d.HTTPListen
	}
	if c.HTTPSListen == "" {
		c.HTTPSListen = d.HTTPSListen
	}
	if c.SessionTTLHours <= 0 {
		c.SessionTTLHours = 168
	}
	if c.TLSCertPath == "" {
		c.TLSCertPath = filepath.Join(d.Dir, "tls", "cert.pem")
	}
	if c.TLSKeyPath == "" {
		c.TLSKeyPath = filepath.Join(d.Dir, "tls", "key.pem")
	}
}

// PersistPasswordHash loads the config at path, updates only the password
// hash, and writes it back atomically with 0600. Concurrent callers are not
// expected (single backend process).
func PersistPasswordHash(path, hash string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg UIConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.PasswordHash = hash
	return saveConfig(path, cfg)
}

func saveConfig(path string, cfg UIConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
