package resolve

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
)

// LookupFunc resolves a host to IPv4 addresses.
type LookupFunc func(ctx context.Context, host string) ([]net.IP, error)

// SingBoxResolver is a pure-Go resolver that queries sing-box's dns-in inbound
// on loopback (so the answer follows the user's DNS servers and rules).
func SingBoxResolver(port int) *net.Resolver {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, network, addr)
		},
	}
}

// SingBoxProbeResolver queries dns-in from the fixed loopback source port
// srcPort, which the applied config routes to exactly one server, uncached
// (see config.DNSProbeBasePort).
func SingBoxProbeResolver(port, srcPort int) *net.Resolver {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	lo := net.IPv4(127, 0, 0, 1)
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 2 * time.Second}
			if network == "tcp" || network == "tcp4" {
				d.LocalAddr = &net.TCPAddr{IP: lo, Port: srcPort}
			} else {
				d.LocalAddr = &net.UDPAddr{IP: lo, Port: srcPort}
			}
			return d.DialContext(ctx, network, addr)
		},
	}
}

// LookupFor returns the lookup the route-ipset resolver (and the route trace)
// should use under the given settings. With client DNS intercepted, clients
// get their answers from sing-box, so the ipset must too — otherwise rotating
// CDN pools / geo-DNS hand clients IPs the set never saw. It falls back to the
// system resolver when sing-box doesn't answer (e.g. it's down and the
// intercept has been unhooked). Without the intercept, clients use the
// router's resolver, and so does this.
func LookupFor(s settings.Settings) LookupFunc {
	system := func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip4", host)
	}
	port := s.DNSRedirectPort()
	if port == 0 {
		return system
	}
	sb := SingBoxResolver(port)
	return func(ctx context.Context, host string) ([]net.IP, error) {
		ips, err := sb.LookupIP(ctx, "ip4", host)
		if err == nil {
			return ips, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		return system(ctx, host)
	}
}
