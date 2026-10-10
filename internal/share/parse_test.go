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

func TestParseTrojan_UnescapedColonInPassword(t *testing.T) {
	s, err := ParseLink("trojan://a:b@example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	if s.Password != "a:b" {
		t.Errorf("password = %q, want a:b", s.Password)
	}
}

func TestMandatoryTLSIgnoresFlag(t *testing.T) {
	// Data that bypassed the parser/editor may carry tls:false; trojan and
	// anytls still run over TLS and must keep every TLS setting.
	for _, typ := range []string{TypeTrojan, TypeAnyTLS} {
		s := Server{Type: typ, Server: "h", ServerPort: 443, Password: "p",
			SNI: "sni.example", Insecure: true, Fingerprint: "firefox", ALPN: []string{"h2"}}
		tls, _ := s.ToOutbound("x")["tls"].(map[string]any)
		utls, _ := tls["utls"].(map[string]any)
		if tls["enabled"] != true || tls["insecure"] != true || tls["server_name"] != "sni.example" ||
			utls["fingerprint"] != "firefox" || len(tls["alpn"].([]string)) != 1 {
			t.Errorf("%s: tls = %+v", typ, tls)
		}
	}
}

func TestParseAnyTLS(t *testing.T) {
	link := "anytls://s3cr%3Det@any.example.com:8443/?sni=cdn.example.com&insecure=1&fp=chrome&alpn=h2,http/1.1#AnyTLS%20node"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != TypeAnyTLS || s.Server != "any.example.com" || s.ServerPort != 8443 ||
		s.Password != "s3cr=et" || s.Name != "AnyTLS node" || !s.TLS {
		t.Errorf("anytls: %+v", s)
	}
	if s.SNI != "cdn.example.com" || !s.Insecure || s.Fingerprint != "chrome" || len(s.ALPN) != 2 {
		t.Errorf("anytls tls: %+v", s)
	}
	out := s.ToOutbound("proxy")
	if out["type"] != "anytls" || out["password"] != "s3cr=et" || out["server_port"] != 8443 {
		t.Errorf("outbound: %+v", out)
	}
	for _, k := range []string{"transport", "multiplex"} {
		if _, ok := out[k]; ok {
			t.Errorf("anytls outbound must not have %q: %+v", k, out)
		}
	}
	tls := out["tls"].(map[string]any)
	if tls["enabled"] != true || tls["server_name"] != "cdn.example.com" || tls["insecure"] != true {
		t.Errorf("tls: %+v", tls)
	}
	if utls, _ := tls["utls"].(map[string]any); utls["fingerprint"] != "chrome" {
		t.Errorf("utls: %+v", tls)
	}
	if _, ok := tls["reality"]; ok {
		t.Errorf("unexpected reality: %+v", tls)
	}
}

func TestParseAnyTLS_Reality(t *testing.T) {
	s, err := ParseLink("anytls://pw@1.2.3.4:443?security=reality&sni=www.apple.com&pbk=PUBKEY&sid=ab12")
	if err != nil {
		t.Fatal(err)
	}
	tls := s.ToOutbound("x")["tls"].(map[string]any)
	reality, _ := tls["reality"].(map[string]any)
	utls, _ := tls["utls"].(map[string]any)
	if reality["public_key"] != "PUBKEY" || reality["short_id"] != "ab12" || utls["fingerprint"] != "chrome" ||
		tls["server_name"] != "www.apple.com" {
		t.Errorf("reality tls: %+v", tls)
	}
}

func TestParseAnyTLS_DefaultsAndErrors(t *testing.T) {
	s, err := ParseLink("anytls://a:b@host.example")
	if err != nil {
		t.Fatal(err)
	}
	if s.ServerPort != 443 || s.Password != "a:b" {
		t.Errorf("defaults: %+v", s)
	}
	// Transport params are ignored; applyTLSQuery takes SNI from host.
	s, err = ParseLink("anytls://pw@host.example:443?type=ws&host=h.example&path=/x")
	if err != nil {
		t.Fatal(err)
	}
	if s.Network != "" || s.WSPath != "" || s.SNI != "h.example" {
		t.Errorf("ws params: %+v", s)
	}
	s, _ = ParseLink("anytls://host.example:443?password=qp")
	if s == nil || s.Password != "qp" {
		t.Errorf("password from query: %+v", s)
	}
	for _, bad := range []string{"anytls://host.example:443", "anytls://pw@:443"} {
		if _, err := ParseLink(bad); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func TestParseAnyTLS_RoundTrip(t *testing.T) {
	// The subscription path: parse → Validate → ToOutbound.
	s, err := ParseLink("anytls://pw@host.example:443?sni=host.example#n")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if out := s.ToOutbound("n"); out == nil || out["type"] != TypeAnyTLS {
		t.Fatalf("outbound: %+v", out)
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

func TestParseHysteria2_ObfsHopping(t *testing.T) {
	link := "hysteria2://letmein@example.com:443,20000-30000/?sni=real.example.com&insecure=1" +
		"&obfs=salamander&obfs-password=gawr&upmbps=50&downmbps=200#HY%202"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Type != TypeHysteria2 || s.Server != "example.com" || s.ServerPort != 443 || s.Password != "letmein" {
		t.Errorf("basics: %+v", s)
	}
	if len(s.ServerPorts) != 1 || s.ServerPorts[0] != "20000:30000" {
		t.Errorf("ports: %v", s.ServerPorts)
	}
	if s.SNI != "real.example.com" || !s.Insecure || s.ObfsPassword != "gawr" || s.Name != "HY 2" {
		t.Errorf("params: %+v", s)
	}
	if s.UpMbps != 50 || s.DownMbps != 200 {
		t.Errorf("mbps: %d/%d", s.UpMbps, s.DownMbps)
	}

	out := s.ToOutbound("proxy")
	if out["type"] != "hysteria2" || out["password"] != "letmein" || out["up_mbps"] != 50 {
		t.Errorf("outbound: %+v", out)
	}
	if ports := out["server_ports"].([]string); ports[0] != "20000:30000" {
		t.Errorf("server_ports: %v", ports)
	}
	obfs := out["obfs"].(map[string]any)
	if obfs["type"] != "salamander" || obfs["password"] != "gawr" {
		t.Errorf("obfs: %+v", obfs)
	}
	tls := out["tls"].(map[string]any)
	if tls["enabled"] != true || tls["server_name"] != "real.example.com" || tls["insecure"] != true {
		t.Errorf("tls: %+v", tls)
	}
	if _, ok := tls["utls"]; ok {
		t.Error("hysteria2 tls must not carry utls")
	}
	for _, k := range []string{"transport", "multiplex", "packet_encoding"} {
		if _, ok := out[k]; ok {
			t.Errorf("unexpected %q in hysteria2 outbound", k)
		}
	}
}

func TestParseHysteria2_AliasIPv6UserPass(t *testing.T) {
	s, err := ParseLink("hy2://user:p%40ss@[2001:db8::1]:8443?mport=10000-11000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Server != "2001:db8::1" || s.ServerPort != 8443 || s.Password != "user:p@ss" {
		t.Errorf("basics: %+v", s)
	}
	if len(s.ServerPorts) != 1 || s.ServerPorts[0] != "10000:11000" {
		t.Errorf("ports: %v", s.ServerPorts)
	}
	if out := s.ToOutbound("p"); out["obfs"] != nil {
		t.Errorf("obfs should be absent: %+v", out)
	}
}

func TestParseHysteria2_DefaultPortAndErrors(t *testing.T) {
	s, err := ParseLink("hy2://secret@host.example")
	if err != nil || s.ServerPort != 443 {
		t.Fatalf("default port: %+v %v", s, err)
	}
	for _, bad := range []string{
		"hy2://@host:443",
		"hy2://a@host:99999",
		"hy2://a@host:443,30000-20000",
		"hy2://a@host:443?obfs=xplus",
	} {
		if _, err := ParseLink(bad); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func TestParseTUIC(t *testing.T) {
	link := "tuic://b379c1d9-0b37-41b0-96b8-467c29b8ca9d:p%40ss@tuic.example.com:8443" +
		"?congestion_control=bbr&udp_relay_mode=quic&alpn=h3,spdy/3.1&sni=real.example.com" +
		"&allow_insecure=1&reduce_rtt=1#TUIC%20node"
	s, err := ParseLink(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Type != TypeTUIC || s.Server != "tuic.example.com" || s.ServerPort != 8443 ||
		s.UUID != "b379c1d9-0b37-41b0-96b8-467c29b8ca9d" || s.Password != "p@ss" || s.Name != "TUIC node" {
		t.Errorf("basics: %+v", s)
	}
	if s.CongestionControl != "bbr" || s.UDPRelayMode != "quic" || !s.ZeroRTT || !s.Insecure ||
		s.SNI != "real.example.com" || len(s.ALPN) != 2 {
		t.Errorf("params: %+v", s)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}

	out := s.ToOutbound("proxy")
	if out["type"] != "tuic" || out["uuid"] != s.UUID || out["password"] != "p@ss" ||
		out["congestion_control"] != "bbr" || out["udp_relay_mode"] != "quic" || out["zero_rtt_handshake"] != true {
		t.Errorf("outbound: %+v", out)
	}
	tls := out["tls"].(map[string]any)
	if tls["enabled"] != true || tls["server_name"] != "real.example.com" || tls["insecure"] != true {
		t.Errorf("tls: %+v", tls)
	}
	if _, ok := tls["utls"]; ok {
		t.Error("tuic tls must not carry utls")
	}
	for _, k := range []string{"transport", "multiplex", "packet_encoding"} {
		if _, ok := out[k]; ok {
			t.Errorf("unexpected %q in tuic outbound", k)
		}
	}
}

func TestParseTUIC_DefaultsAndAliases(t *testing.T) {
	s, err := ParseLink("tuic://B379C1D9B37041B096B8467C29B8CA9D@[2001:db8::1]?password=secret" +
		"&congestion-control=NEW-RENO&udp-relay-mode=Native&insecure=true&disable_sni=1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Server != "2001:db8::1" || s.ServerPort != 443 || s.Password != "secret" ||
		s.CongestionControl != "new_reno" || s.UDPRelayMode != "native" || !s.Insecure || !s.DisableSNI {
		t.Errorf("aliases: %+v", s)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
	out := s.ToOutbound("p")
	// No ALPN in the link: h3 is filled in, TUIC servers require it.
	tls := out["tls"].(map[string]any)
	if alpn := tls["alpn"].([]string); len(alpn) != 1 || alpn[0] != "h3" {
		t.Errorf("default alpn: %v", alpn)
	}
	if tls["disable_sni"] != true {
		t.Errorf("disable_sni: %+v", tls)
	}
	if s, _ := ParseLink("tuic://b379c1d9-0b37-41b0-96b8-467c29b8ca9d:p@h?congestion_control=newreno"); s == nil || s.CongestionControl != "new_reno" {
		t.Errorf("newreno alias: %+v", s)
	}
	// Clash/Meta spellings and a "v"-prefixed version.
	s, err = ParseLink("tuic://b379c1d9-0b37-41b0-96b8-467c29b8ca9d:p@h?version=v5&congestion-controller=bbr&reduce-rtt=true")
	if err != nil || s.CongestionControl != "bbr" || !s.ZeroRTT {
		t.Errorf("clash aliases: %+v %v", s, err)
	}
	if _, ok := out["zero_rtt_handshake"]; ok {
		t.Error("zero_rtt_handshake must be omitted when off")
	}

	for _, bad := range []string{
		"tuic://b379c1d9-0b37-41b0-96b8-467c29b8ca9d@host:443",             // v4-style, no password
		"tuic://b379c1d9-0b37-41b0-96b8-467c29b8ca9d:p@host:443?version=4", // explicit v4
		"tuic://:p@host:443",
		"tuic://u:p@host:99999x",
	} {
		if _, err := ParseLink(bad); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func TestParseInsecureTrue(t *testing.T) {
	for _, link := range []string{
		"hy2://secret@host:443?insecure=true",
		"vless://b379c1d9-0b37-41b0-96b8-467c29b8ca9d@host:443?security=tls&allowInsecure=true",
	} {
		s, err := ParseLink(link)
		if err != nil || !s.Insecure {
			t.Errorf("%s: insecure not set: %+v %v", link, s, err)
		}
	}
}

func TestServerValidate(t *testing.T) {
	ok := []Server{
		{Type: TypeVLESS, Server: "h", ServerPort: 443, UUID: "u"},
		{Type: TypeShadowsocks, Server: "h", ServerPort: 8388, Method: "aes-128-gcm", Password: "p"},
		{Type: TypeHysteria2, Server: "h", Password: "p", ServerPorts: []string{"20000:30000"}},
		{Type: TypeTUIC, Server: "h", ServerPort: 443, UUID: " b379c1d9-0b37-41b0-96b8-467c29b8ca9d ", Password: "p", CongestionControl: "bbr"},
		{Type: TypeTUIC, Server: "h", ServerPort: 443, UUID: "b379c1d90b3741b096b8467c29b8ca9d", Password: "p"},
		{Type: TypeAnyTLS, Server: "h", ServerPort: 443, Password: "p"},
	}
	for _, s := range ok {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	bad := []Server{
		{Type: TypeVLESS, Server: "h", ServerPort: 443},
		{Type: TypeTrojan, Server: " ", ServerPort: 443, Password: "p"},
		{Type: TypeShadowsocks, Server: "h", ServerPort: 1, Password: "p"},
		{Type: TypeHysteria2, Server: "h", Password: "p"},
		{Type: TypeHysteria2, Server: "h", ServerPort: 443, Password: "p", ServerPorts: []string{"20000-30000"}},
		{Type: TypeTUIC, Server: "h", ServerPort: 443, UUID: "not-a-uuid", Password: "p"},
		{Type: TypeTUIC, Server: "h", ServerPort: 443, UUID: "b379c1d9-0b3741b0-96b8-467c29b8ca9d", Password: "p"}, // partial dashes
		{Type: TypeTUIC, Server: "h", ServerPort: 443, UUID: "b379c1d9-0b37-41b0-96b8-467c29b8ca9d"},
		{Type: TypeAnyTLS, Server: "h", ServerPort: 443},
		{Type: "wireguard", Server: "h", ServerPort: 1},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("%+v: expected error", s)
		}
	}
	r := Server{Type: TypeVLESS, Server: "h", ServerPort: 1, UUID: " u", TLS: true, PublicKey: " "}
	if err := r.Validate(); err != nil || r.PublicKey != "" {
		t.Errorf("public_key not trimmed: %q %v", r.PublicKey, err)
	}
	// vless/vmess UUIDs feed the subscription server key: left as stored.
	if r.UUID != " u" {
		t.Errorf("vless uuid must not be trimmed: %q", r.UUID)
	}
	for _, u := range []string{
		" b379c1d9-0b37-41b0-96b8-467c29b8ca9d ",
		"{b379c1d9-0b37-41b0-96b8-467c29b8ca9d}",
		"URN:UUID:b379c1d9-0b37-41b0-96b8-467c29b8ca9d",
	} {
		tu := Server{Type: TypeTUIC, Server: "h", ServerPort: 1, UUID: u, Password: "p"}
		if err := tu.Validate(); err != nil || tu.UUID != "b379c1d9-0b37-41b0-96b8-467c29b8ca9d" {
			t.Errorf("tuic uuid %q not canonicalized: %q %v", u, tu.UUID, err)
		}
	}
	// Unknown tuning values degrade to defaults rather than rejecting the server.
	tu := Server{Type: TypeTUIC, Server: "h", ServerPort: 1, UUID: "b379c1d9-0b37-41b0-96b8-467c29b8ca9d",
		Password: "p", CongestionControl: "bbr2", UDPRelayMode: "quic_stream"}
	if err := tu.Validate(); err != nil || tu.CongestionControl != "" || tu.UDPRelayMode != "" {
		t.Errorf("unknown tuning not defaulted: %+v %v", tu, err)
	}
}
