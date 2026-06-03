package transparent

import (
	"context"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
)

// wanIPv4 returns the global IPv4 addresses of every interface carrying a
// default route, as /32 CIDRs. These must bypass the proxy — most importantly
// the path to the proxy server itself, so its TLS/Reality handshake isn't
// recursively captured. Mirrors SKeen's get_all_wan_ips.
func wanIPv4(ctx context.Context, r cmdrun.Runner) []string {
	out, _ := run(ctx, r, "ip", "-4", "route", "show", "table", "all")
	devs := map[string]struct{}{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] != "default" {
			continue
		}
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "dev" {
				devs[f[i+1]] = struct{}{}
			}
		}
	}
	var ips []string
	seen := map[string]struct{}{}
	for dev := range devs {
		addrOut, _ := run(ctx, r, "ip", "-4", "addr", "show", "dev", dev, "scope", "global")
		for _, line := range strings.Split(addrOut, "\n") {
			f := strings.Fields(line)
			for i := 0; i+1 < len(f); i++ {
				if f[i] == "inet" {
					ip := f[i+1]
					if slash := strings.IndexByte(ip, '/'); slash >= 0 {
						ip = ip[:slash]
					}
					cidr := ip + "/32"
					if _, dup := seen[cidr]; !dup {
						seen[cidr] = struct{}{}
						ips = append(ips, cidr)
					}
				}
			}
		}
	}
	return ips
}
