package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// CustomStore holds the user's custom.json: a sing-box config fragment merged
// into every generated config (see MergeCustom). A missing file means "no
// additions"; saving empty content removes the file.
type CustomStore struct {
	Path string
}

// NewCustomStore returns a store backed by path.
func NewCustomStore(path string) *CustomStore { return &CustomStore{Path: path} }

// Read returns the stored fragment, or nil when there is none.
func (s *CustomStore) Read() ([]byte, error) {
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// Write replaces the fragment atomically; blank content removes the file.
func (s *CustomStore) Write(content []byte) error {
	if IsBlankCustom(content) {
		if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if _, err := ParseCustom(content); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	tmp := s.Path + ".new"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// IsBlankCustom reports whether content holds no additions at all.
func IsBlankCustom(content []byte) bool {
	if len(bytes.TrimSpace(content)) == 0 {
		return true
	}
	obj, err := ParseCustom(content)
	return err == nil && len(obj) == 0
}

// SyntaxError is a JSON syntax error in custom.json with a 1-based position,
// so the editor can point at it.
type SyntaxError struct {
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Msg  string `json:"message"`
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("custom.json:%d:%d: %s", e.Line, e.Col, e.Msg)
}

// ParseCustom decodes custom.json, which must be a JSON object. Syntax errors
// come back as *SyntaxError. Numbers are kept as json.Number so ports and
// marks round-trip exactly.
func ParseCustom(content []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, positioned(content, err)
	}
	if dec.More() {
		off := int(dec.InputOffset())
		line, col := lineCol(content, off)
		return nil, &SyntaxError{Line: line, Col: col, Msg: "лишние данные после JSON-объекта"}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, &SyntaxError{Line: 1, Col: 1, Msg: "ожидается JSON-объект { … }"}
	}
	return obj, nil
}

func positioned(content []byte, err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		// Offset counts the offending byte too; point at it, not past it.
		line, col := lineCol(content, max(int(se.Offset)-1, 0))
		return &SyntaxError{Line: line, Col: col, Msg: se.Error()}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		line, col := lineCol(content, len(content))
		return &SyntaxError{Line: line, Col: col, Msg: "JSON обрывается раньше времени"}
	}
	return &SyntaxError{Line: 1, Col: 1, Msg: err.Error()}
}

// lineCol converts a byte offset into a 1-based line and column (in runes).
func lineCol(content []byte, off int) (int, int) {
	if off > len(content) {
		off = len(content)
	}
	before := content[:off]
	line := bytes.Count(before, []byte("\n")) + 1
	lastNL := bytes.LastIndexByte(before, '\n')
	col := len([]rune(string(before[lastNL+1:]))) + 1
	return line, col
}

// MergeCustom merges the custom.json fragment into a generated config:
//   - objects merge recursively;
//   - arrays are appended to (custom inbounds/outbounds/rules come after the
//     generated ones — route rules thus land after the service rules
//     (DNS hijack, sniff, private → direct) and before route.final);
//   - any other value from custom replaces the generated one; such
//     replacements are reported in overrides as dotted paths.
//
// A type clash (e.g. an object where the generated config has an array) is an
// error. Blank custom content returns base unchanged.
func MergeCustom(base, custom []byte) (merged []byte, overrides []string, err error) {
	if IsBlankCustom(custom) {
		return base, nil, nil
	}
	add, err := ParseCustom(custom)
	if err != nil {
		return nil, nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(base))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return nil, nil, fmt.Errorf("generated config: %w", err)
	}
	if err := mergeObject(cfg, add, "", &overrides); err != nil {
		return nil, nil, err
	}
	sort.Strings(overrides)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return out, overrides, nil
}

func mergeObject(dst, src map[string]any, prefix string, overrides *[]string) error {
	for k, sv := range src {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		dv, exists := dst[k]
		if !exists {
			dst[k] = sv
			continue
		}
		switch d := dv.(type) {
		case map[string]any:
			s, ok := sv.(map[string]any)
			if !ok {
				return fmt.Errorf("custom.json: %s: ожидается объект { … }, как в сгенерированном конфиге", path)
			}
			if err := mergeObject(d, s, path, overrides); err != nil {
				return err
			}
		case []any:
			s, ok := sv.([]any)
			if !ok {
				return fmt.Errorf("custom.json: %s: ожидается массив [ … ], как в сгенерированном конфиге", path)
			}
			dst[k] = append(d, s...)
		default:
			if _, isObj := sv.(map[string]any); isObj {
				return fmt.Errorf("custom.json: %s: в сгенерированном конфиге это значение, а не объект", path)
			}
			if _, isArr := sv.([]any); isArr {
				return fmt.Errorf("custom.json: %s: в сгенерированном конфиге это значение, а не массив", path)
			}
			if !jsonEqual(dv, sv) {
				*overrides = append(*overrides, path)
			}
			dst[k] = sv
		}
	}
	return nil
}

func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// Listen is one inbound's listen endpoint, for port-conflict checks.
type Listen struct {
	Tag  string
	Addr string // "" = all addresses
	Port int
}

// Lint finds mistakes in a merged config that `sing-box check` lets through
// but that break `sing-box run` or silently misroute traffic: duplicate tags,
// references to tags that don't exist, a detour to an empty direct outbound
// (sing-box ≥ 1.13 refuses to start) and two inbounds on one port.
func Lint(merged []byte) (errs []string) {
	dec := json.NewDecoder(bytes.NewReader(merged))
	dec.UseNumber()
	var cfg map[string]any
	if err := dec.Decode(&cfg); err != nil {
		return []string{err.Error()}
	}

	inbounds := objects(cfg["inbounds"])
	outbounds := append(objects(cfg["outbounds"]), objects(cfg["endpoints"])...)
	dnsBlk, _ := cfg["dns"].(map[string]any)
	dnsServers := objects(dnsBlk["servers"])
	route, _ := cfg["route"].(map[string]any)

	inTags := tagSet(inbounds, "inbounds", &errs)
	outTags := tagSet(outbounds, "outbounds/endpoints", &errs)
	dnsTags := tagSet(dnsServers, "dns.servers", &errs)

	emptyDirect := map[string]bool{}
	for _, o := range outbounds {
		if str(o["type"]) == "direct" && len(o) <= 2 { // only type + tag
			emptyDirect[str(o["tag"])] = true
		}
	}

	needOut := func(where, tag string) {
		if tag != "" && !outTags[tag] {
			errs = append(errs, fmt.Sprintf("%s: нет outbound с тегом %q", where, tag))
		}
	}
	needDetour := func(where, tag string) {
		needOut(where, tag)
		if emptyDirect[tag] {
			errs = append(errs, fmt.Sprintf("%s: detour на пустой direct %q — sing-box не запустится; уберите detour или задайте direct-у параметры", where, tag))
		}
	}
	needDNS := func(where, tag string) {
		if tag != "" && !dnsTags[tag] {
			errs = append(errs, fmt.Sprintf("%s: нет DNS-сервера с тегом %q", where, tag))
		}
	}
	needResolver := func(where string, v any) {
		switch r := v.(type) {
		case string:
			needDNS(where, r)
		case map[string]any:
			needDNS(where, str(r["server"]))
		}
	}

	for _, o := range outbounds {
		where := "outbound " + quoteTag(o)
		needDetour(where+".detour", str(o["detour"]))
		for _, t := range listable(o["outbounds"]) {
			needOut(where+".outbounds", t)
		}
		needOut(where+".default", str(o["default"]))
		needResolver(where+".domain_resolver", o["domain_resolver"])
	}
	for _, s := range dnsServers {
		where := "dns.servers " + quoteTag(s)
		needDetour(where+".detour", str(s["detour"]))
		needResolver(where+".domain_resolver", s["domain_resolver"])
	}
	needDNS("dns.final", str(dnsBlk["final"]))
	for i, r := range objects(dnsBlk["rules"]) {
		needDNS(fmt.Sprintf("dns.rules[%d].server", i), str(r["server"]))
		lintRuleInbounds(fmt.Sprintf("dns.rules[%d]", i), r, inTags, &errs)
	}
	needOut("route.final", str(route["final"]))
	needResolver("route.default_domain_resolver", route["default_domain_resolver"])
	for i, r := range objects(route["rules"]) {
		needOut(fmt.Sprintf("route.rules[%d].outbound", i), str(r["outbound"]))
		lintRuleInbounds(fmt.Sprintf("route.rules[%d]", i), r, inTags, &errs)
	}

	errs = append(errs, portConflicts(InboundListens(merged))...)
	return errs
}

// lintRuleInbounds flags rules (and logical sub-rules) matching an inbound
// that doesn't exist: a typo there silently routes the traffic elsewhere.
func lintRuleInbounds(where string, r map[string]any, inTags map[string]bool, errs *[]string) {
	for _, t := range listable(r["inbound"]) {
		if !inTags[t] {
			*errs = append(*errs, fmt.Sprintf("%s.inbound: нет inbound с тегом %q", where, t))
		}
	}
	for i, sub := range objects(r["rules"]) {
		lintRuleInbounds(fmt.Sprintf("%s.rules[%d]", where, i), sub, inTags, errs)
	}
}

// InboundListens lists the TCP/UDP listen endpoints of a config's inbounds
// (tun and other inbounds without listen_port are skipped).
func InboundListens(cfgJSON []byte) []Listen {
	dec := json.NewDecoder(bytes.NewReader(cfgJSON))
	dec.UseNumber()
	var cfg map[string]any
	if dec.Decode(&cfg) != nil {
		return nil
	}
	var out []Listen
	for _, in := range objects(cfg["inbounds"]) {
		n, ok := in["listen_port"].(json.Number)
		if !ok {
			continue
		}
		port, err := n.Int64()
		if err != nil || port <= 0 {
			continue
		}
		out = append(out, Listen{Tag: str(in["tag"]), Addr: str(in["listen"]), Port: int(port)})
	}
	return out
}

// Wildcard reports whether a listen address binds every address.
func (l Listen) Wildcard() bool {
	return l.Addr == "" || l.Addr == "0.0.0.0" || l.Addr == "::"
}

// Overlaps reports whether two listens can't both bind.
func (l Listen) Overlaps(o Listen) bool {
	return l.Port == o.Port && (l.Wildcard() || o.Wildcard() || l.Addr == o.Addr)
}

func portConflicts(ls []Listen) (errs []string) {
	for i := range ls {
		for j := i + 1; j < len(ls); j++ {
			if ls[i].Overlaps(ls[j]) {
				errs = append(errs, fmt.Sprintf("inbound %q и %q слушают один порт %d", ls[i].Tag, ls[j].Tag, ls[i].Port))
			}
		}
	}
	return errs
}

// BindInterfaces lists the interfaces named by outbounds' bind_interface.
func BindInterfaces(cfgJSON []byte) []string {
	var cfg map[string]any
	if json.Unmarshal(cfgJSON, &cfg) != nil {
		return nil
	}
	var out []string
	for _, o := range append(objects(cfg["outbounds"]), objects(cfg["endpoints"])...) {
		if s := str(o["bind_interface"]); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func tagSet(items []map[string]any, kind string, errs *[]string) map[string]bool {
	set := map[string]bool{}
	for _, it := range items {
		t := str(it["tag"])
		if t == "" {
			continue
		}
		if set[t] {
			*errs = append(*errs, fmt.Sprintf("%s: тег %q встречается дважды", kind, t))
		}
		set[t] = true
	}
	return set
}

func objects(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// listable reads a sing-box "listable" field: a string or an array of strings.
func listable(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, it := range t {
			if s, ok := it.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func quoteTag(o map[string]any) string {
	if t := str(o["tag"]); t != "" {
		return fmt.Sprintf("%q", t)
	}
	return "без тега"
}
