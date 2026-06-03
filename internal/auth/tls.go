package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

type TLSPaths struct {
	CertPath string
	KeyPath  string
}

// EnsureTLS makes sure a self-signed cert/key pair exists at the given
// paths. If both exist they are reused; if either is missing, a new pair
// is generated. Files are written with 0600.
func EnsureTLS(p TLSPaths, extraSANs []string) (created bool, err error) {
	if p.CertPath == "" || p.KeyPath == "" {
		return false, errors.New("empty cert/key path")
	}
	if existsFile(p.CertPath) && existsFile(p.KeyPath) {
		return false, nil
	}
	for _, d := range []string{filepath.Dir(p.CertPath), filepath.Dir(p.KeyPath)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return false, err
		}
	}
	certPEM, keyPEM, err := generateSelfSigned(extraSANs)
	if err != nil {
		return false, err
	}
	if err := writePrivate(p.CertPath, certPEM); err != nil {
		return false, err
	}
	if err := writePrivate(p.KeyPath, keyPEM); err != nil {
		return false, err
	}
	return true, nil
}

func generateSelfSigned(extraSANs []string) ([]byte, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, nil, err
	}

	dnsNames := []string{"localhost", "keenetic-sing-box-ui"}
	ipAddrs := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	for _, s := range extraSANs {
		if s == "" {
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			ipAddrs = append(ipAddrs, ip)
		} else {
			dnsNames = append(dnsNames, s)
		}
	}

	now := time.Now().UTC()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "keenetic-sing-box-ui",
			Organization: []string{"keenetic-sing-box-ui"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddrs,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func writePrivate(path string, content []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

func existsFile(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !st.IsDir()
}
