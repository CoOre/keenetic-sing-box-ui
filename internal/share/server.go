// Package share parses proxy "share links" (vless://, trojan://, ss://,
// vmess://) into a unified Server model and builds sing-box outbound objects
// from it. This lets the UI accept a pasted link or form fields instead of
// requiring hand-written sing-box JSON.
package share

// Protocol identifies the outbound type.
const (
	TypeVLESS       = "vless"
	TypeTrojan      = "trojan"
	TypeShadowsocks = "shadowsocks"
	TypeVMess       = "vmess"
)

// Server is a flattened, form-friendly representation of a single proxy
// server. Fields not relevant to a given Type are left empty. It round-trips
// to a sing-box outbound object via ToOutbound.
type Server struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`

	// Credentials
	UUID     string `json:"uuid,omitempty"`     // vless, vmess
	Password string `json:"password,omitempty"` // trojan, shadowsocks
	Method   string `json:"method,omitempty"`   // shadowsocks
	AlterID  int    `json:"alter_id,omitempty"` // vmess
	Flow     string `json:"flow,omitempty"`     // vless

	// TLS
	TLS         bool     `json:"tls,omitempty"`
	SNI         string   `json:"sni,omitempty"`
	ALPN        []string `json:"alpn,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"` // uTLS
	Insecure    bool     `json:"insecure,omitempty"`

	// REALITY
	PublicKey string `json:"public_key,omitempty"`
	ShortID   string `json:"short_id,omitempty"`

	// Transport
	Network     string `json:"network,omitempty"` // "", tcp, ws, grpc, http
	WSPath      string `json:"ws_path,omitempty"`
	WSHost      string `json:"ws_host,omitempty"`
	GRPCService string `json:"grpc_service_name,omitempty"`
}

// ToOutbound builds a sing-box outbound object for this server, using the
// given tag. Returns nil for an unknown Type.
func (s *Server) ToOutbound(tag string) map[string]any {
	switch s.Type {
	case TypeVLESS:
		return s.vlessOutbound(tag)
	case TypeTrojan:
		return s.trojanOutbound(tag)
	case TypeShadowsocks:
		return s.shadowsocksOutbound(tag)
	case TypeVMess:
		return s.vmessOutbound(tag)
	default:
		return nil
	}
}

func (s *Server) vlessOutbound(tag string) map[string]any {
	o := map[string]any{
		"type":        TypeVLESS,
		"tag":         tag,
		"server":      s.Server,
		"server_port": s.ServerPort,
		"uuid":        s.UUID,
	}
	if s.Flow != "" {
		o["flow"] = s.Flow
	}
	if tls := s.tlsBlock(); tls != nil {
		o["tls"] = tls
	}
	if t := s.transportBlock(); t != nil {
		o["transport"] = t
	}
	if pe := s.packetEncoding(); pe != "" {
		o["packet_encoding"] = pe
	}
	return o
}

func (s *Server) trojanOutbound(tag string) map[string]any {
	o := map[string]any{
		"type":        TypeTrojan,
		"tag":         tag,
		"server":      s.Server,
		"server_port": s.ServerPort,
		"password":    s.Password,
	}
	// Trojan implies TLS by default.
	tls := s.tlsBlock()
	if tls == nil {
		tls = map[string]any{"enabled": true}
		if s.SNI != "" {
			tls["server_name"] = s.SNI
		}
	}
	o["tls"] = tls
	if t := s.transportBlock(); t != nil {
		o["transport"] = t
	}
	return o
}

func (s *Server) shadowsocksOutbound(tag string) map[string]any {
	return map[string]any{
		"type":        TypeShadowsocks,
		"tag":         tag,
		"server":      s.Server,
		"server_port": s.ServerPort,
		"method":      s.Method,
		"password":    s.Password,
	}
}

func (s *Server) vmessOutbound(tag string) map[string]any {
	o := map[string]any{
		"type":        TypeVMess,
		"tag":         tag,
		"server":      s.Server,
		"server_port": s.ServerPort,
		"uuid":        s.UUID,
		"alter_id":    s.AlterID,
		"security":    "auto",
	}
	if tls := s.tlsBlock(); tls != nil {
		o["tls"] = tls
	}
	if t := s.transportBlock(); t != nil {
		o["transport"] = t
	}
	return o
}

// tlsBlock builds the sing-box "tls" object, or nil if TLS is disabled.
func (s *Server) tlsBlock() map[string]any {
	if !s.TLS {
		return nil
	}
	tls := map[string]any{"enabled": true}
	if s.SNI != "" {
		tls["server_name"] = s.SNI
	}
	if len(s.ALPN) > 0 {
		tls["alpn"] = s.ALPN
	}
	if s.Insecure {
		tls["insecure"] = true
	}
	if s.Fingerprint != "" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": s.Fingerprint}
	}
	if s.PublicKey != "" {
		reality := map[string]any{"enabled": true, "public_key": s.PublicKey}
		if s.ShortID != "" {
			reality["short_id"] = s.ShortID
		}
		tls["reality"] = reality
		// REALITY requires a uTLS fingerprint; default to chrome.
		if s.Fingerprint == "" {
			tls["utls"] = map[string]any{"enabled": true, "fingerprint": "chrome"}
		}
	}
	return tls
}

// transportBlock builds the sing-box "transport" object for ws/grpc/http, or
// nil for plain TCP.
func (s *Server) transportBlock() map[string]any {
	switch s.Network {
	case "ws":
		t := map[string]any{"type": "ws"}
		if s.WSPath != "" {
			t["path"] = s.WSPath
		}
		if s.WSHost != "" {
			t["headers"] = map[string]any{"Host": s.WSHost}
		}
		return t
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if s.GRPCService != "" {
			t["service_name"] = s.GRPCService
		}
		return t
	case "http":
		return map[string]any{"type": "http"}
	default:
		return nil
	}
}

func (s *Server) packetEncoding() string {
	// xudp is the common default for VLESS; harmless when unused.
	return "xudp"
}
