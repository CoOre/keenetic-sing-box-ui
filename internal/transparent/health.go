package transparent

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// ProxyListening reports whether sing-box is accepting TCP connections on the
// given port. It first tries /proc/net/tcp (Linux kernel table, zero overhead,
// no connection — no redirect-loop risk). If that file is absent (macOS dev
// machine) it falls back to a short-lived TCP dial.
//
// Exported so the watchdog in the main package can use it without going
// through a full Engine (which would try to insmod / configure iptables).
func ProxyListening(port int) bool { return proxyListening(port) }

// WaitProxy is the exported form of waitProxy, for use by the watchdog.
func WaitProxy(port int, timeout time.Duration) { waitProxy(port, timeout) }

func proxyListening(port int) bool {
	if port <= 0 {
		return false
	}
	if ok, err := procNetTCPListening(port); err == nil {
		return ok
	}
	// Fallback for non-Linux (dev machines).
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// procNetTCPListening parses /proc/net/tcp for a LISTEN entry on the given
// port. The kernel file uses hex local_address:PORT with state 0A (LISTEN).
func procNetTCPListening(port int) (bool, error) {
	b, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		return false, err
	}
	needle := fmt.Sprintf(":%04X", port)
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		// f[1] = local_address (XXXXXXXX:PPPP), f[3] = state
		if strings.HasSuffix(f[1], needle) && strings.EqualFold(f[3], "0A") {
			return true, nil
		}
	}
	return false, nil
}

// waitProxy polls until the proxy port is in LISTEN state or the deadline
// passes. Uses /proc/net/tcp when available — cheap and loop-free.
func waitProxy(port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		if proxyListening(port) {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (c Config) inboundPort() int { return c.InboundPort() }

// InboundPort returns the port sing-box listens on for this config's mode.
func (c Config) InboundPort() int {
	if c.Mode == ModeRedirect {
		return c.RedirectPort
	}
	return c.TProxyPort
}
