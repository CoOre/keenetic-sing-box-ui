// Package settings persists UI-level sing-box generation settings that aren't
// per-server: the inbound mode (tun/socks/tproxy), its port, and tun tuning.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	InboundMode string `json:"inbound_mode"` // "tun" | "socks" | "tproxy" | "redirect"
	InboundPort int    `json:"inbound_port"`
	TunStack    string `json:"tun_stack"`
	TunMTU      int    `json:"tun_mtu"`

	// Transparent-routing settings (used by tproxy/redirect modes). In these
	// modes the firewall transparently captures LAN traffic and sing-box's own
	// route rules decide what is proxied: only RouteDomains/RouteCIDR go through
	// the proxy, everything else egresses directly.
	PolicyName   string   `json:"policy_name"`   // bind to a Keenetic policy's fwmark; "" = whole device
	ExcludeCIDR  []string `json:"exclude_cidr"`  // extra destinations to always bypass
	RouteDomains []string `json:"route_domains"` // domains to send through the proxy (via FakeIP)
	RouteCIDR    []string `json:"route_cidr"`    // destination CIDRs to send through the proxy
	UseConntrack bool     `json:"use_conntrack"` // connmark optimization (skip established conns)
}

// Defaults: socks/mixed on :2080 — the mode proven to coexist with a router's
// own VPN routing. tun tuning is kept for when tun mode is selected.
func Defaults() Settings {
	return Settings{InboundMode: "socks", InboundPort: 2080, TunStack: "gvisor", TunMTU: 1380}
}

type Store struct {
	Path string
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{Path: path} }

func (s *Store) Get() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Defaults(), nil
		}
		return Settings{}, err
	}
	out := Defaults()
	if err := json.Unmarshal(body, &out); err != nil {
		return Settings{}, err
	}
	out.normalize()
	return out, nil
}

func (s *Store) Save(in Settings) (Settings, error) {
	in.normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return Settings{}, err
	}
	body, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return Settings{}, err
	}
	tmp := s.Path + ".new"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return Settings{}, err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return Settings{}, err
	}
	return in, nil
}

func (s *Settings) normalize() {
	switch s.InboundMode {
	case "tun", "socks", "tproxy", "redirect":
	default:
		s.InboundMode = "socks"
	}
	if s.InboundPort <= 0 || s.InboundPort > 65535 {
		s.InboundPort = 2080
	}
	if s.TunStack == "" {
		s.TunStack = "gvisor"
	}
	if s.TunMTU <= 0 {
		s.TunMTU = 1380
	}
}
