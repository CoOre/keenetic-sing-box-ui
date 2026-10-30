package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const mergeBase = `{
  "log": {"level": "info"},
  "dns": {"servers": [{"type": "udp", "tag": "local", "server": "1.1.1.1"}], "final": "local"},
  "inbounds": [{"type": "tproxy", "tag": "tproxy-in", "listen": "0.0.0.0", "listen_port": 2080}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"rules": [{"action": "sniff"}], "final": "direct"}
}`

const egressCustom = `{
  "dns": {"servers": [{"type": "udp", "tag": "kg-dns", "server": "192.168.9.1"}]},
  "inbounds": [{"type": "mixed", "tag": "in-kg", "listen": "192.168.6.1", "listen_port": 1091}],
  "outbounds": [{"type": "direct", "tag": "via-kg", "bind_interface": "nwg4",
                 "domain_resolver": {"server": "kg-dns", "strategy": "ipv4_only"}}],
  "route": {"rules": [{"inbound": ["in-kg"], "outbound": "via-kg"}]}
}`

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, b)
	}
	return m
}

func TestMergeCustomAppendsArraysAndMergesObjects(t *testing.T) {
	out, overrides, err := MergeCustom([]byte(mergeBase), []byte(egressCustom))
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 0 {
		t.Fatalf("overrides = %v, want none", overrides)
	}
	m := decode(t, out)
	inb := m["inbounds"].([]any)
	if len(inb) != 2 || inb[1].(map[string]any)["tag"] != "in-kg" {
		t.Fatalf("inbounds not appended: %v", inb)
	}
	rules := m["route"].(map[string]any)["rules"].([]any)
	if len(rules) != 2 || rules[0].(map[string]any)["action"] != "sniff" {
		t.Fatalf("custom rule must follow generated ones: %v", rules)
	}
	if m["route"].(map[string]any)["final"] != "direct" {
		t.Fatal("route.final lost")
	}
	if n := len(m["dns"].(map[string]any)["servers"].([]any)); n != 2 {
		t.Fatalf("dns.servers = %d, want 2", n)
	}
	if errs := Lint(out); len(errs) != 0 {
		t.Fatalf("lint: %v", errs)
	}
}

func TestMergeCustomKeepsNumbersExact(t *testing.T) {
	out, _, err := MergeCustom([]byte(mergeBase), []byte(`{"route":{"default_mark":4294967295}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "4294967295") {
		t.Fatalf("number mangled:\n%s", out)
	}
	if !strings.Contains(string(out), `"listen_port": 2080`) {
		t.Fatalf("generated number mangled:\n%s", out)
	}
}

func TestMergeCustomReportsOverrides(t *testing.T) {
	_, overrides, err := MergeCustom([]byte(mergeBase), []byte(`{"log":{"level":"debug"},"route":{"final":"direct"}}`))
	if err != nil {
		t.Fatal(err)
	}
	// route.final is set to the same value — not an override.
	if !slices.Equal(overrides, []string{"log.level"}) {
		t.Fatalf("overrides = %v", overrides)
	}
}

func TestMergeCustomTypeClash(t *testing.T) {
	for _, c := range []string{
		`{"inbounds": {"type": "mixed"}}`,
		`{"route": []}`,
		`{"log": {"level": {"x": 1}}}`,
	} {
		if _, _, err := MergeCustom([]byte(mergeBase), []byte(c)); err == nil {
			t.Errorf("%s: expected type-clash error", c)
		}
	}
}

func TestMergeCustomBlank(t *testing.T) {
	for _, c := range []string{"", "  \n", "{}", " { } "} {
		out, _, err := MergeCustom([]byte(mergeBase), []byte(c))
		if err != nil || string(out) != mergeBase {
			t.Errorf("%q: base must pass through unchanged (err %v)", c, err)
		}
	}
}

func TestParseCustomSyntaxErrorPosition(t *testing.T) {
	_, err := ParseCustom([]byte("{\n  \"a\": 1,\n  \"b\" 2\n}"))
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("want *SyntaxError, got %v", err)
	}
	if se.Line != 3 || se.Col != 7 {
		t.Fatalf("position = %d:%d, want 3:7", se.Line, se.Col)
	}

	_, err = ParseCustom([]byte("{\n  \"a\": [1, 2"))
	if !errors.As(err, &se) || se.Line != 2 {
		t.Fatalf("truncated: %v", err)
	}
	for _, c := range []string{`[1]`, `"x"`, `{} {}`} {
		if _, err := ParseCustom([]byte(c)); !errors.As(err, &se) {
			t.Errorf("%s: want *SyntaxError, got %v", c, err)
		}
	}
}

func TestLintCatchesWhatCheckMisses(t *testing.T) {
	custom := `{
	  "inbounds": [
	    {"type": "mixed", "tag": "in-a", "listen": "192.168.6.1", "listen_port": 2080},
	    {"type": "mixed", "tag": "tproxy-in", "listen_port": 3000}
	  ],
	  "outbounds": [
	    {"type": "direct", "tag": "via-a", "detour": "direct"},
	    {"type": "selector", "tag": "sel", "outbounds": ["via-a", "nope"]}
	  ],
	  "dns": {"servers": [{"type": "udp", "tag": "d2", "server": "8.8.8.8", "detour": "missing"}],
	          "rules": [{"server": "ghost"}]},
	  "route": {"rules": [{"inbound": ["in-typo"], "outbound": "via-a"},
	                      {"type": "logical", "mode": "and", "rules": [{"inbound": "in-b"}], "outbound": "lost"}]}
	}`
	out, _, err := MergeCustom([]byte(mergeBase), []byte(custom))
	if err != nil {
		t.Fatal(err)
	}
	errs := strings.Join(Lint(out), "\n")
	for _, want := range []string{
		`inbounds: тег "tproxy-in" встречается дважды`,
		`detour на пустой direct "direct"`,
		`outbound "sel".outbounds: нет outbound с тегом "nope"`,
		`dns.servers "d2".detour: нет outbound с тегом "missing"`,
		`dns.rules[0].server: нет DNS-сервера с тегом "ghost"`,
		`route.rules[1].inbound: нет inbound с тегом "in-typo"`,
		`route.rules[2].rules[0].inbound: нет inbound с тегом "in-b"`,
		`route.rules[2].outbound: нет outbound с тегом "lost"`,
		`слушают один порт 2080`,
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}
}

func TestLintGeneratedConfigClean(t *testing.T) {
	body, err := Assemble(AssembleOptions{InboundMode: InboundTProxy}, []ProxyOutbound{
		{Tag: "s1", Object: map[string]any{"type": "vless", "server": "1.2.3.4", "server_port": 443}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if errs := Lint(body); len(errs) != 0 {
		t.Fatalf("generated config must lint clean: %v", errs)
	}
}

func TestCustomStoreRoundTrip(t *testing.T) {
	s := NewCustomStore(filepath.Join(t.TempDir(), "custom.json"))
	if b, err := s.Read(); err != nil || b != nil {
		t.Fatalf("missing file: %q, %v", b, err)
	}
	if err := s.Write([]byte(egressCustom)); err != nil {
		t.Fatal(err)
	}
	if b, _ := s.Read(); string(b) != egressCustom {
		t.Fatal("content not stored")
	}
	if err := s.Write([]byte("{oops")); err == nil {
		t.Fatal("invalid JSON must be refused")
	}
	if err := s.Write([]byte("  ")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Fatal("blank content must remove the file")
	}
}
