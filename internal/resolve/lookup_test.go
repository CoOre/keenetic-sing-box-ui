package resolve

import (
	"context"
	"net"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/settings"
)

func TestLookupForFallsBackToSystem(t *testing.T) {
	// Free port with nothing on it: sing-box "down".
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.LocalAddr().(*net.UDPAddr).Port
	_ = l.Close()

	s := settings.Defaults()
	s.InboundMode = "tproxy"
	s.DNS.InterceptClients = true
	s.DNS.Port = port
	ips, err := LookupFor(s)(context.Background(), "localhost")
	if err != nil || len(ips) == 0 {
		t.Fatalf("fallback lookup: %v %v", ips, err)
	}
}

func TestLookupForWithoutInterceptIsSystem(t *testing.T) {
	s := settings.Defaults()
	s.InboundMode = "tproxy"
	if s.DNSRedirectPort() != 0 {
		t.Fatal("intercept off must not redirect")
	}
	s.DNS.InterceptClients = true
	s.InboundMode = "socks"
	if s.DNSRedirectPort() != 0 {
		t.Fatal("socks mode has no firewall intercept")
	}
}
