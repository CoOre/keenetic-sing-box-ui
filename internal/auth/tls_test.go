package auth

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureTLS_CreatesAndReuses(t *testing.T) {
	dir := t.TempDir()
	p := TLSPaths{
		CertPath: filepath.Join(dir, "tls", "cert.pem"),
		KeyPath:  filepath.Join(dir, "tls", "key.pem"),
	}
	created, err := EnsureTLS(p, []string{"192.168.1.1", "router.lan"})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first call")
	}

	// Both files exist with 0600.
	if runtime.GOOS != "windows" {
		for _, f := range []string{p.CertPath, p.KeyPath} {
			st, err := os.Stat(f)
			if err != nil {
				t.Fatalf("stat %s: %v", f, err)
			}
			if st.Mode().Perm() != 0o600 {
				t.Errorf("%s: expected 0600, got %o", f, st.Mode().Perm())
			}
		}
	}

	// Parses into a valid keypair.
	if _, err := tls.LoadX509KeyPair(p.CertPath, p.KeyPath); err != nil {
		t.Fatalf("load keypair: %v", err)
	}

	// SAN list contains the extras.
	body, _ := os.ReadFile(p.CertPath)
	block, _ := pem.Decode(body)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	foundIP := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.ParseIP("192.168.1.1")) {
			foundIP = true
		}
	}
	if !foundIP {
		t.Errorf("missing 192.168.1.1 in SAN IPs: %v", cert.IPAddresses)
	}
	foundDNS := false
	for _, n := range cert.DNSNames {
		if n == "router.lan" {
			foundDNS = true
		}
	}
	if !foundDNS {
		t.Errorf("missing router.lan in DNS SANs: %v", cert.DNSNames)
	}

	// Second call: not regenerated.
	stCert, _ := os.Stat(p.CertPath)
	created2, err := EnsureTLS(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Error("expected created=false on second call")
	}
	stCert2, _ := os.Stat(p.CertPath)
	if !stCert.ModTime().Equal(stCert2.ModTime()) {
		t.Error("cert was rewritten on second call")
	}
}
