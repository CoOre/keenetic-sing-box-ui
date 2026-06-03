package share

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseVLESS_Reality(t *testing.T) {
	link := "vless://b379c1d9-0b37-41b0-96b8-467c29b8ca9d@45.9.13.188:8443" +
		"?type=tcp&security=reality&pbk=RItXJKVm0rgSu_yERsEZxJxhpyKmRJqay1AJDkHgTzg" +
		"&sid=96543f22d4e8445c&sni=matrix.nosov.su&fp=chrome&flow=xtls-rprx-vision#My%20Server"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Type != TypeVLESS || s.Server != "45.9.13.188" || s.ServerPort != 8443 {
		t.Errorf("basics wrong: %+v", s)
	}
	if s.UUID != "b379c1d9-0b37-41b0-96b8-467c29b8ca9d" {
		t.Errorf("uuid: %s", s.UUID)
	}
	if s.Flow != "xtls-rprx-vision" || s.SNI != "matrix.nosov.su" || s.Fingerprint != "chrome" {
		t.Errorf("tls fields: %+v", s)
	}
	if s.PublicKey == "" || s.ShortID != "96543f22d4e8445c" {
		t.Errorf("reality: %+v", s)
	}
	if s.Name != "My Server" {
		t.Errorf("name: %q", s.Name)
	}

	out := s.ToOutbound("proxy")
	if out["type"] != "vless" || out["flow"] != "xtls-rprx-vision" {
		t.Errorf("outbound: %+v", out)
	}
	tls := out["tls"].(map[string]any)
	reality := tls["reality"].(map[string]any)
	if reality["public_key"] != s.PublicKey || reality["short_id"] != "96543f22d4e8445c" {
		t.Errorf("reality block: %+v", reality)
	}
	utls := tls["utls"].(map[string]any)
	if utls["fingerprint"] != "chrome" {
		t.Errorf("utls: %+v", utls)
	}
}

func TestParseVLESS_WS_TLS(t *testing.T) {
	link := "vless://uuid-1@example.com:443?type=ws&security=tls&sni=example.com&path=/wspath&host=cdn.example.com#ws"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if s.Network != "ws" || s.WSPath != "/wspath" || s.WSHost != "cdn.example.com" {
		t.Errorf("ws transport: %+v", s)
	}
	out := s.ToOutbound("proxy")
	tr := out["transport"].(map[string]any)
	if tr["type"] != "ws" || tr["path"] != "/wspath" {
		t.Errorf("transport block: %+v", tr)
	}
	if _, ok := out["tls"].(map[string]any); !ok {
		t.Errorf("expected tls block")
	}
}

func TestParseTrojan(t *testing.T) {
	link := "trojan://secretpass@example.com:443?sni=example.com#trojan-server"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != TypeTrojan || s.Password != "secretpass" || s.ServerPort != 443 {
		t.Errorf("trojan: %+v", s)
	}
	out := s.ToOutbound("proxy")
	if out["password"] != "secretpass" {
		t.Errorf("password: %+v", out)
	}
	if _, ok := out["tls"].(map[string]any); !ok {
		t.Errorf("trojan must have tls")
	}
}

func TestParseShadowsocks_UserinfoBase64(t *testing.T) {
	userinfo := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:mypassword"))
	link := "ss://" + userinfo + "@1.2.3.4:8388#ss-server"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if s.Method != "aes-256-gcm" || s.Password != "mypassword" {
		t.Errorf("ss creds: %+v", s)
	}
	if s.Server != "1.2.3.4" || s.ServerPort != 8388 {
		t.Errorf("ss host: %+v", s)
	}
	if s.Name != "ss-server" {
		t.Errorf("name: %q", s.Name)
	}
}

func TestParseShadowsocks_FullyEncoded(t *testing.T) {
	full := base64.StdEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:pw@5.6.7.8:1234"))
	link := "ss://" + full + "#legacy"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if s.Method != "chacha20-ietf-poly1305" || s.Password != "pw" || s.Server != "5.6.7.8" || s.ServerPort != 1234 {
		t.Errorf("ss fully-encoded: %+v", s)
	}
}

func TestParseVMess(t *testing.T) {
	payload := map[string]any{
		"ps": "vmess-server", "add": "9.9.9.9", "port": "443", "id": "vmess-uuid",
		"aid": "0", "net": "ws", "tls": "tls", "host": "cdn.host", "path": "/vm", "sni": "real.sni",
	}
	b, _ := json.Marshal(payload)
	link := "vmess://" + base64.StdEncoding.EncodeToString(b)
	s, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != TypeVMess || s.Server != "9.9.9.9" || s.ServerPort != 443 || s.UUID != "vmess-uuid" {
		t.Errorf("vmess: %+v", s)
	}
	if !s.TLS || s.SNI != "real.sni" || s.Network != "ws" || s.WSPath != "/vm" || s.WSHost != "cdn.host" {
		t.Errorf("vmess tls/ws: %+v", s)
	}
	out := s.ToOutbound("proxy")
	if out["uuid"] != "vmess-uuid" || out["type"] != "vmess" {
		t.Errorf("vmess outbound: %+v", out)
	}
}

func TestParseLink_Unsupported(t *testing.T) {
	if _, err := ParseLink("http://example.com"); err == nil {
		t.Error("expected error for unsupported scheme")
	}
}
