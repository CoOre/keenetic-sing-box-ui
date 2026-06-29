package system

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/cmdrun"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// mssChain is a dedicated mangle chain holding the MSS clamp, jumped from OUTPUT
// (the router's own sing-box->server connection) and FORWARD (LAN clients
// reaching the server directly). Using our own chain means re-applying just
// flushes it — no need to know the previously-set MSS to delete it.
const mssChain = "ksbui_mss"

func iptablesBin() string {
	if _, err := os.Stat("/opt/sbin/iptables"); err == nil {
		return "/opt/sbin/iptables"
	}
	return "iptables"
}

// SetMSSClamp installs a fixed TCP MSS clamp for SYNs to/from ip, so packets on
// a PMTU-blackholed path stay under the working MTU. Idempotent: the clamp chain
// is flushed and rebuilt each call. NOT persistent — a KeeneticOS firewall
// rebuild or a reboot drops it (baking it into the transparent engine would make
// it survive; deferred).
func SetMSSClamp(ctx context.Context, runner cmdrun.Runner, ip string, mss int) error {
	if net.ParseIP(ip) == nil || mss <= 0 {
		return errors.New("invalid ip or mss")
	}
	bin := iptablesBin()
	mssStr := strconv.Itoa(mss)
	var firstErr error
	rec := func(args ...string) {
		if _, err := runner.Run(ctx, bin, args...); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// (Re)create + flush our chain so old clamps (any MSS) are gone. If -N fails
	// the chain already exists, so flush it instead. Best-effort: keep going on
	// errors so a single failed rule doesn't skip the jumps.
	if _, err := runner.Run(ctx, bin, "-w", "-t", "mangle", "-N", mssChain); err != nil {
		_, _ = runner.Run(ctx, bin, "-w", "-t", "mangle", "-F", mssChain)
	}
	for _, dir := range []string{"-d", "-s"} {
		rec("-w", "-t", "mangle", "-A", mssChain, "-p", "tcp", dir, ip,
			"-m", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--set-mss", mssStr)
	}
	// Jump into the chain from OUTPUT and FORWARD, once (checked via -C).
	for _, hook := range []string{"OUTPUT", "FORWARD"} {
		if _, err := runner.Run(ctx, bin, "-w", "-t", "mangle", "-C", hook, "-j", mssChain); err != nil {
			rec("-w", "-t", "mangle", "-A", hook, "-j", mssChain)
		}
	}
	return firstErr
}

// ClearMSSClamp removes the clamp chain and its jumps.
func ClearMSSClamp(ctx context.Context, runner cmdrun.Runner) {
	bin := iptablesBin()
	del := func(args ...string) error { _, err := runner.Run(ctx, bin, args...); return err }
	for _, hook := range []string{"OUTPUT", "FORWARD"} {
		for del("-w", "-t", "mangle", "-D", hook, "-j", mssChain) == nil {
		}
	}
	_ = del("-w", "-t", "mangle", "-F", mssChain)
	_ = del("-w", "-t", "mangle", "-X", mssChain)
}

// MTUResult is the outcome of a path-MTU probe to a host.
type MTUResult struct {
	IP   string `json:"ip"`
	PMTU int    `json:"pmtu"` // largest IPv4 packet (bytes, incl. headers) that reaches the host with DF set
	MSS  int    `json:"mss"`  // recommended TCP MSS for that path = PMTU - 40 (IPv4 20 + TCP 20)
}

// ProbeMTU binary-searches the path MTU to ip the way the manual `ping -M do -s`
// sweep did: it sends DF-marked ICMP echo requests of varying total IPv4 size
// and finds the largest that still elicits a reply. A PMTU-blackholed path (DF
// packet silently dropped, no ICMP frag-needed back) shows up as the largest
// size that replies being well under the interface MTU. Needs a raw socket
// (root). Each size is retried to ride out the heavy ICMP loss these CGNAT/
// throttled paths exhibit.
func ProbeMTU(ctx context.Context, ip string) (MTUResult, error) {
	dst := net.ParseIP(ip)
	if dst == nil || dst.To4() == nil {
		return MTUResult{}, errors.New("invalid IPv4 address")
	}
	dst = dst.To4()

	conn, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return MTUResult{}, err
	}
	defer conn.Close()
	raw, err := ipv4.NewRawConn(conn)
	if err != nil {
		return MTUResult{}, err
	}

	id := os.Getpid() & 0xffff

	// sendOne sends a single DF echo of total IPv4 size `total` and reports
	// whether a matching reply arrives within the per-try window.
	sendOne := func(total, seq int) bool {
		payload := total - ipv4.HeaderLen - 8 // minus IP(20) + ICMP echo header(8)
		if payload < 0 {
			payload = 0
		}
		msg := icmp.Message{
			Type: ipv4.ICMPTypeEcho, Code: 0,
			Body: &icmp.Echo{ID: id, Seq: seq, Data: make([]byte, payload)},
		}
		wb, err := msg.Marshal(nil)
		if err != nil {
			return false
		}
		h := &ipv4.Header{
			Version:  ipv4.Version,
			Len:      ipv4.HeaderLen,
			TotalLen: ipv4.HeaderLen + len(wb),
			Flags:    ipv4.DontFragment,
			TTL:      64,
			Protocol: 1, // ICMP
			Dst:      dst,
		}
		if err := raw.WriteTo(h, wb, nil); err != nil {
			return false // EMSGSIZE etc. => can't even leave locally, treat as "too big"
		}
		_ = raw.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
		buf := make([]byte, 1500)
		for {
			rh, p, _, err := raw.ReadFrom(buf)
			if err != nil {
				return false // timeout
			}
			if !rh.Src.Equal(dst) {
				continue
			}
			m, err := icmp.ParseMessage(1, p)
			if err != nil {
				continue
			}
			if e, ok := m.Body.(*icmp.Echo); ok && e.ID == id && e.Seq == seq {
				return true
			}
		}
	}

	// passes retries a size several times; any reply means the path carries it.
	const tries = 5
	passes := func(total, base int) bool {
		for t := 0; t < tries; t++ {
			if ctx.Err() != nil {
				return false
			}
			if sendOne(total, (base<<3|t)&0xffff) {
				return true
			}
		}
		return false
	}

	const lo0, hi0 = 576, 1500
	if !passes(lo0, 1) {
		return MTUResult{IP: ip}, errors.New("no ICMP echo reply — host or path filters ping, can't probe MTU")
	}
	lo, hi, best := lo0+1, hi0, lo0
	for lo <= hi {
		if ctx.Err() != nil {
			break
		}
		mid := (lo + hi) / 2
		if passes(mid, mid) {
			best, lo = mid, mid+1
		} else {
			hi = mid - 1
		}
	}
	return MTUResult{IP: ip, PMTU: best, MSS: best - 40}, nil
}
