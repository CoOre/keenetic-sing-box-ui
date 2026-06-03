package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

const (
	DefaultLogPath      = "/opt/var/log/sing-box.log"
	DefaultCachePath    = "/opt/var/lib/sing-box/cache.db"
	DefaultClashAddr    = "127.0.0.1:9090"
	OutboundProxyTag    = "proxy"
	OutboundDirectTag   = "direct"
	OutboundBlockTag    = "block"
	OutboundAutoTag     = "auto"
	DefaultURLTestProbe = "https://www.gstatic.com/generate_204"
)

type DefaultOptions struct {
	LogPath      string
	CachePath    string
	ClashAddr    string
	ClashSecret  string // generated if empty
	URLTestProbe string
}

// DefaultConfig produces a baseline sing-box config.json with stable outbound
// tags (proxy/direct/block/auto), clash_api enabled on loopback with a
// generated secret, and cache_file enabled.
func DefaultConfig(opts DefaultOptions) ([]byte, error) {
	if opts.LogPath == "" {
		opts.LogPath = DefaultLogPath
	}
	if opts.CachePath == "" {
		opts.CachePath = DefaultCachePath
	}
	if opts.ClashAddr == "" {
		opts.ClashAddr = DefaultClashAddr
	}
	if opts.URLTestProbe == "" {
		opts.URLTestProbe = DefaultURLTestProbe
	}
	if opts.ClashSecret == "" {
		s, err := generateSecret()
		if err != nil {
			return nil, fmt.Errorf("generate secret: %w", err)
		}
		opts.ClashSecret = s
	}

	// Config targets the sing-box 1.12+ schema (new DNS server format, rule
	// actions, route.default_domain_resolver). Verified against 1.13.x with
	// `sing-box check`. Legacy fields (dns address strings, `block`/`dns`
	// outbounds, rule `outbound`) are rejected by 1.13+ and intentionally
	// avoided here.
	cfg := map[string]any{
		"log": map[string]any{
			"level":     "info",
			"output":    opts.LogPath,
			"timestamp": true,
		},
		"dns": map[string]any{
			"servers": []map[string]any{
				{"type": "tls", "tag": "google", "server": "8.8.8.8"},
				{"type": "local", "tag": "local"},
			},
			"strategy": "ipv4_only",
		},
		"inbounds": []any{},
		"outbounds": []any{
			map[string]any{
				"type":      "selector",
				"tag":       OutboundProxyTag,
				"outbounds": []string{OutboundAutoTag, OutboundDirectTag},
				"default":   OutboundAutoTag,
			},
			map[string]any{
				"type":      "urltest",
				"tag":       OutboundAutoTag,
				"outbounds": []string{OutboundDirectTag},
				"url":       opts.URLTestProbe,
				"interval":  "5m",
			},
			map[string]any{"type": "direct", "tag": OutboundDirectTag},
		},
		"route": map[string]any{
			"rules": []map[string]any{
				{"action": "sniff"},
				{"protocol": "dns", "action": "hijack-dns"},
			},
			"final":                   OutboundProxyTag,
			"auto_detect_interface":   true,
			"default_domain_resolver": map[string]any{"server": "local"},
		},
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": opts.ClashAddr,
				"secret":              opts.ClashSecret,
			},
			"cache_file": map[string]any{
				"enabled": true,
				"path":    opts.CachePath,
			},
		},
	}

	return json.MarshalIndent(cfg, "", "  ")
}

func generateSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
