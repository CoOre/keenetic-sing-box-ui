package share

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParseLink parses a single proxy share link into a Server. Supported schemes:
// vless://, trojan://, ss://, vmess://.
func ParseLink(raw string) (*Server, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "vless://"):
		return parseVLESS(raw)
	case strings.HasPrefix(raw, "trojan://"):
		return parseTrojan(raw)
	case strings.HasPrefix(raw, "ss://"):
		return parseShadowsocks(raw)
	case strings.HasPrefix(raw, "vmess://"):
		return parseVMess(raw)
	default:
		return nil, fmt.Errorf("unsupported or unrecognized link (expected vless/trojan/ss/vmess://)")
	}
}

// parseVLESS handles vless://uuid@host:port?params#name.
func parseVLESS(raw string) (*Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse vless: %w", err)
	}
	s := &Server{Type: TypeVLESS, Name: frag(u)}
	if u.User != nil {
		s.UUID = u.User.Username()
	}
	if err := setHostPort(s, u); err != nil {
		return nil, err
	}
	q := u.Query()
	s.Flow = q.Get("flow")
	applyTLSQuery(s, q)
	applyTransportQuery(s, q)
	if s.UUID == "" || s.Server == "" {
		return nil, fmt.Errorf("vless: missing uuid or server")
	}
	return s, nil
}

// parseTrojan handles trojan://password@host:port?params#name.
func parseTrojan(raw string) (*Server, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse trojan: %w", err)
	}
	s := &Server{Type: TypeTrojan, Name: frag(u)}
	if u.User != nil {
		s.Password = u.User.Username()
	}
	if err := setHostPort(s, u); err != nil {
		return nil, err
	}
	q := u.Query()
	// Trojan is TLS by default.
	s.TLS = true
	applyTLSQuery(s, q)
	s.TLS = true
	applyTransportQuery(s, q)
	if s.Password == "" || s.Server == "" {
		return nil, fmt.Errorf("trojan: missing password or server")
	}
	return s, nil
}

// parseShadowsocks handles:
//
//	ss://base64(method:password)@host:port#name
//	ss://base64(method:password@host:port)#name   (legacy, fully encoded)
func parseShadowsocks(raw string) (*Server, error) {
	name := ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		name = decodeFragment(raw[i+1:])
		raw = raw[:i]
	}
	body := strings.TrimPrefix(raw, "ss://")
	// Strip any query (plugin params) — not supported here.
	if i := strings.IndexByte(body, '?'); i >= 0 {
		body = body[:i]
	}

	s := &Server{Type: TypeShadowsocks, Name: name}
	if at := strings.LastIndexByte(body, '@'); at >= 0 {
		// userinfo@host:port; userinfo is base64(method:password) (maybe raw).
		userinfo := body[:at]
		hostport := body[at+1:]
		mp := userinfo
		if dec, err := b64decode(userinfo); err == nil && strings.Contains(string(dec), ":") {
			mp = string(dec)
		}
		method, pass, ok := strings.Cut(mp, ":")
		if !ok {
			return nil, fmt.Errorf("ss: bad method:password")
		}
		s.Method, s.Password = method, pass
		if err := splitHostPort(s, hostport); err != nil {
			return nil, err
		}
	} else {
		// Fully base64-encoded: method:password@host:port
		dec, err := b64decode(body)
		if err != nil {
			return nil, fmt.Errorf("ss: base64: %w", err)
		}
		full := string(dec)
		at := strings.LastIndexByte(full, '@')
		if at < 0 {
			return nil, fmt.Errorf("ss: missing @ in decoded link")
		}
		mp, hostport := full[:at], full[at+1:]
		method, pass, ok := strings.Cut(mp, ":")
		if !ok {
			return nil, fmt.Errorf("ss: bad method:password")
		}
		s.Method, s.Password = method, pass
		if err := splitHostPort(s, hostport); err != nil {
			return nil, err
		}
	}
	if s.Method == "" || s.Server == "" {
		return nil, fmt.Errorf("ss: missing method or server")
	}
	return s, nil
}

// vmessJSON is the standard v2rayN vmess:// base64 JSON payload.
type vmessJSON struct {
	PS   string `json:"ps"`
	Add  string `json:"add"`
	Port any    `json:"port"`
	ID   string `json:"id"`
	Aid  any    `json:"aid"`
	Net  string `json:"net"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	Host string `json:"host"`
	Path string `json:"path"`
}

func parseVMess(raw string) (*Server, error) {
	body := strings.TrimPrefix(raw, "vmess://")
	dec, err := b64decode(body)
	if err != nil {
		return nil, fmt.Errorf("vmess: base64: %w", err)
	}
	var v vmessJSON
	if err := json.Unmarshal(dec, &v); err != nil {
		return nil, fmt.Errorf("vmess: json: %w", err)
	}
	s := &Server{
		Type:    TypeVMess,
		Name:    v.PS,
		Server:  v.Add,
		UUID:    v.ID,
		AlterID: toInt(v.Aid),
		Network: normalizeNet(v.Net),
		SNI:     v.SNI,
	}
	s.ServerPort = toInt(v.Port)
	if v.TLS == "tls" {
		s.TLS = true
		if s.SNI == "" {
			s.SNI = v.Host
		}
	}
	switch s.Network {
	case "ws":
		s.WSPath = v.Path
		s.WSHost = v.Host
	case "grpc":
		s.GRPCService = v.Path
	}
	if s.UUID == "" || s.Server == "" {
		return nil, fmt.Errorf("vmess: missing id or server")
	}
	return s, nil
}

// --- shared helpers ---

func setHostPort(s *Server, u *url.URL) error {
	s.Server = u.Hostname()
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("bad port %q", p)
		}
		s.ServerPort = n
	}
	return nil
}

func splitHostPort(s *Server, hostport string) error {
	host, port, ok := strings.Cut(hostport, ":")
	if !ok {
		return fmt.Errorf("missing port in %q", hostport)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("bad port %q", port)
	}
	s.Server, s.ServerPort = host, n
	return nil
}

// applyTLSQuery reads security/sni/fp/pbk/sid/alpn/allowInsecure params.
func applyTLSQuery(s *Server, q url.Values) {
	security := q.Get("security")
	switch security {
	case "tls", "reality", "xtls":
		s.TLS = true
	}
	if sni := firstNonEmpty(q.Get("sni"), q.Get("peer"), q.Get("host")); sni != "" {
		s.SNI = sni
	}
	if fp := q.Get("fp"); fp != "" {
		s.Fingerprint = fp
	}
	if pbk := q.Get("pbk"); pbk != "" {
		s.PublicKey = pbk
		s.TLS = true
	}
	if sid := q.Get("sid"); sid != "" {
		s.ShortID = sid
	}
	if alpn := q.Get("alpn"); alpn != "" {
		s.ALPN = strings.Split(alpn, ",")
	}
	if q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" {
		s.Insecure = true
	}
}

// applyTransportQuery reads type/path/host/serviceName params.
func applyTransportQuery(s *Server, q url.Values) {
	s.Network = normalizeNet(q.Get("type"))
	switch s.Network {
	case "ws":
		s.WSPath = q.Get("path")
		s.WSHost = firstNonEmpty(q.Get("host"), s.SNI)
	case "grpc":
		s.GRPCService = firstNonEmpty(q.Get("serviceName"), q.Get("servicename"))
	}
}

func normalizeNet(n string) string {
	switch n {
	case "", "tcp", "raw":
		return ""
	case "ws", "websocket":
		return "ws"
	case "grpc":
		return "grpc"
	case "http", "h2":
		return "http"
	default:
		return n
	}
}

func frag(u *url.URL) string {
	if u.Fragment != "" {
		return u.Fragment
	}
	return ""
}

func decodeFragment(f string) string {
	if dec, err := url.QueryUnescape(f); err == nil {
		return dec
	}
	return f
}

// b64decode tries standard and URL-safe base64, with and without padding.
func b64decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	encs := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	var lastErr error
	for _, e := range encs {
		if dec, err := e.DecodeString(s); err == nil {
			return dec, nil
		} else {
			lastErr = err
		}
	}
	return nil, lastErr
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	default:
		return 0
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
