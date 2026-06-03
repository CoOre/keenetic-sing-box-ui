package transparent

import (
	"net/netip"
	"strings"
)

// normalizeCIDR trims and validates an IPv4 address/CIDR, appending /32 to a
// bare address. Returns "" if it isn't a valid IPv4 address or prefix.
func normalizeCIDR(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return ""
	}
	if !strings.Contains(s, "/") {
		if addr, err := netip.ParseAddr(s); err == nil && addr.Is4() {
			return s + "/32"
		}
		return ""
	}
	if p, err := netip.ParsePrefix(s); err == nil && p.Addr().Is4() {
		return p.String()
	}
	return ""
}

func dedup(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
