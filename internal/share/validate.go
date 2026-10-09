package share

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	portRangeRe = regexp.MustCompile(`^\d{1,5}(:\d{1,5})?$`)
	// Canonical 8-4-4-4-12 or 32 bare hex digits — the forms sing-box accepts.
	uuidRe = regexp.MustCompile(`^(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{32})$`)
)

// Validate normalizes whitespace-only fields and checks that the fields
// required by the server's Type are present. It is meant for form input; share
// links go through the per-scheme parsers, which have their own checks.
func (s *Server) Validate() error {
	s.Server = strings.TrimSpace(s.Server)
	s.PublicKey = strings.TrimSpace(s.PublicKey)
	s.ShortID = strings.TrimSpace(s.ShortID)
	s.SNI = strings.TrimSpace(s.SNI)

	if s.Server == "" {
		return errors.New("не указан адрес сервера")
	}
	if s.ServerPort < 0 || s.ServerPort > 65535 {
		return fmt.Errorf("некорректный порт %d", s.ServerPort)
	}
	if s.ServerPort == 0 && (s.Type != TypeHysteria2 || len(s.ServerPorts) == 0) {
		return errors.New("не указан порт")
	}

	switch s.Type {
	case TypeVLESS, TypeVMess:
		if strings.TrimSpace(s.UUID) == "" {
			return errors.New("не указан UUID")
		}
	case TypeTrojan:
		if s.Password == "" {
			return errors.New("не указан пароль")
		}
	case TypeShadowsocks:
		if s.Password == "" || s.Method == "" {
			return errors.New("для Shadowsocks нужны метод и пароль")
		}
	case TypeHysteria2:
		if s.Password == "" {
			return errors.New("не указан пароль (auth)")
		}
		for _, r := range s.ServerPorts {
			if !portRangeRe.MatchString(r) {
				return fmt.Errorf("некорректный диапазон портов %q (ожидается 20000:30000)", r)
			}
		}
	case TypeTUIC:
		// sing-box refuses to start on a malformed TUIC uuid, so catch it here
		// rather than at apply time (subscriptions skip such links). Normalized
		// only here: for vless/vmess the stored UUID is part of the subscription
		// server key, and changing it would orphan existing servers.
		s.UUID = canonicalUUID(s.UUID)
		if !uuidRe.MatchString(s.UUID) {
			return errors.New("UUID не указан или некорректен")
		}
		if s.Password == "" {
			return errors.New("не указан пароль")
		}
		// Unknown tuning values (bbr2, quic-stream…) fall back to the sing-box
		// default instead of dropping the whole server from a subscription.
		switch s.CongestionControl {
		case "cubic", "new_reno", "bbr":
		default:
			s.CongestionControl = ""
		}
		switch s.UDPRelayMode {
		case "native", "quic":
		default:
			s.UDPRelayMode = ""
		}
	default:
		return fmt.Errorf("неподдерживаемый тип %q", s.Type)
	}
	return nil
}

// canonicalUUID trims a UUID and strips the "urn:uuid:" prefix and braces,
// leaving the 36- or 32-character form that uuidRe checks.
func canonicalUUID(u string) string {
	u = strings.TrimSpace(u)
	if len(u) > 9 && strings.EqualFold(u[:9], "urn:uuid:") {
		u = u[9:]
	}
	if strings.HasPrefix(u, "{") && strings.HasSuffix(u, "}") {
		u = u[1 : len(u)-1]
	}
	return u
}
