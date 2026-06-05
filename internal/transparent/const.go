// Package transparent implements selective transparent proxying on Keenetic
// routers, ported from the proven shell logic of jinndi/SKeen.
//
// The design that makes this coexist with KeeneticOS's own policy routing
// (WireGuard/OpenConnect) is twofold:
//
//   - Rules live in iptables mangle/nat as dedicated chains, not as a tun
//     default-route capture. A tun inbound with auto_route fights the router's
//     routing tables; mangle TPROXY rules sit alongside them.
//   - Rules are (re)installed from a hook in /opt/etc/ndm/netfilter.d/, which
//     KeeneticOS runs on every firewall rebuild and at boot. That hook calls
//     back into this binary (`keenetic-sing-box-ui firewall apply --table X`),
//     so our chains survive the router flushing and rebuilding its own rules.
//
// Only traffic the user selects (by domain or destination CIDR) is ultimately
// sent through the proxy: the iptables layer transparently hands traffic to
// sing-box, and sing-box's own route rules decide proxy-vs-direct. The exclude
// ipset (reserved ranges + the router's WAN IPs incl. the proxy server itself)
// keeps that from looping or breaking the router's own connectivity.
package transparent

const (
	// Name prefixes our artifacts so they're identifiable in iptables -S and
	// on disk. Kept short; KeeneticOS chain names have practical length limits.
	Name = "ksbui"

	// fwmark / routing table used for the TPROXY divert. 0x112 mirrors SKeen so
	// the two never need to coexist on the same box anyway, and the value is
	// well clear of KeeneticOS's own policy marks.
	MarkHex    = "0x112"
	RouteTblID = "112"

	// netfilter.d hook: KeeneticOS sources every executable here whenever it
	// (re)builds the firewall, passing $type (iptables/ip6tables) and $table.
	netfilterDir = "/opt/etc/ndm/netfilter.d"
	HookFileName = Name + "_firewall.sh"

	// Kernel modules live in the firmware tree; we copy the ones we need into
	// the Entware tree and insmod them: <osDir>/<uname -r>/<mod>. The firmware
	// dir varies by Keenetic model/firmware — see modulesOSDirs.
	modulesEntwareDir = "/opt/lib/modules"

	// Keenetic's local config RPC. Used read-only to resolve a policy's fwmark.
	rciURL = "http://127.0.0.1:79/rci"
)

// Chain names. PREROUTING chains capture transit (LAN) traffic; the OUTPUT
// chain (mask) handles the router's own traffic when proxy-router is enabled.
const (
	chainPrerouting = Name             // mangle/nat PREROUTING jump target
	chainOutput     = Name + "_mask"   // OUTPUT jump target (router self-proxy)
	chainDivert     = Name + "_divert" // -m socket --transparent divert
	chainTproxy     = Name + "_tproxy" // connmark-optimized tproxy leaf
	chainRedirect   = Name + "_redirect"
	chainMarkOut    = Name + "_mark_out"
	chainForward    = Name + "_fwd" // filter FORWARD leaf: UDP/443 (QUIC) block
)

// ipset names. v4/v6 suffix is appended at use sites.
const (
	netExcludeSet = Name + "_exclude_net"
	netRouteSet   = Name + "_route_net" // optional dst-intercept set
)

// modulesOSDirs are the firmware module trees we search, in order, for a
// <dir>/<uname -r>/<mod>.ko file. Most Keenetic firmwares use /lib/modules,
// but on some (e.g. KN with kernel 4.9-ndm-5) the "Netfilter kernel modules"
// component installs xt_TPROXY/xt_socket under /lib/system-modules instead —
// without this second path, TPROXY mode aborts in LoadModules even though the
// modules are present. Verified live: insmod from /lib/system-modules loads
// xt_TPROXY/xt_socket and registers the TPROXY target + socket match.
var modulesOSDirs = []string{"/lib/modules", "/lib/system-modules"}

// Modules we try to load. xt_owner is special-cased (often built in).
var requiredModules = []string{
	"xt_TPROXY.ko",
	"xt_socket.ko",
	"xt_owner.ko",
	"xt_comment.ko",
	"ip_set_bitmap_port.ko",
	"ip_set_hash_net.ko",
}

// Absolute tool paths. The netfilter.d hook and our service run with a
// restricted PATH, so we don't rely on PATH resolution.
var toolPaths = map[string][]string{
	"iptables":  {"/opt/sbin/iptables", "/usr/sbin/iptables", "/sbin/iptables", "/opt/bin/iptables"},
	"ip6tables": {"/opt/sbin/ip6tables", "/usr/sbin/ip6tables", "/sbin/ip6tables", "/opt/bin/ip6tables"},
	"ipset":     {"/opt/sbin/ipset", "/usr/sbin/ipset", "/sbin/ipset", "/opt/bin/ipset"},
	"ip":        {"/opt/sbin/ip", "/usr/sbin/ip", "/sbin/ip", "/opt/bin/ip"},
	"insmod":    {"/sbin/insmod", "/usr/sbin/insmod", "/opt/sbin/insmod"},
	// On many Keenetic firmwares insmod is only a busybox applet (no standalone
	// /sbin/insmod), so loadModule falls back to `busybox insmod`.
	"busybox": {"/opt/bin/busybox", "/bin/busybox", "/usr/bin/busybox", "/usr/sbin/busybox"},
}
