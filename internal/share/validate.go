package share

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var portRangeRe = regexp.MustCompile(`^\d{1,5}(:\d{1,5})?$`)

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
	default:
		return fmt.Errorf("неподдерживаемый тип %q", s.Type)
	}
	return nil
}
