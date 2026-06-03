package transparent

// reservedV4 are the IPv4 ranges that must always bypass the proxy: loopback,
// private/CGNAT, link-local, multicast, documentation, etc. Routing these into
// sing-box would break LAN and the router's own plumbing. 198.18.0.0/15 is
// intentionally NOT here — it is sing-box's FakeIP range and must be captured.
// Mirrors SKeen's RESERVED_IPV4.
var reservedV4 = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.31.196.0/24",
	"192.52.193.0/24",
	"192.88.99.0/24",
	"192.168.0.0/16",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"255.255.255.255/32",
}
