package config

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CoOre/keenetic-sing-box-ui/internal/share"
)

// chainOutbounds mirrors what the API builds for servers A, B, C plus a chain
// A → B → C: A's own outbound is the entry, B and C are detoured clones.
func chainOutbounds() []ProxyOutbound {
	a := share.Server{Type: "vless", Server: "10.0.0.1", ServerPort: 443, UUID: "b379c1d9-0b37-41b0-96b8-467c29b8ca9d"}
	b := share.Server{Type: "trojan", Server: "10.0.0.2", ServerPort: 443, Password: "p", TLS: true, SNI: "b.example"}
	c := share.Server{Type: "hysteria2", Server: "10.0.0.3", ServerPort: 443, Password: "p", SNI: "c.example"}
	hop := b.ToOutbound("ch.hop2")
	hop["detour"] = "A"
	exit := c.ToOutbound("ch")
	exit["detour"] = "ch.hop2"
	return []ProxyOutbound{
		{Tag: "A", Object: a.ToOutbound("A")},
		{Tag: "B", Object: b.ToOutbound("B")},
		{Tag: "C", Object: c.ToOutbound("C")},
		{Tag: "ch.hop2", Object: hop, Role: RoleHop},
		{Tag: "ch", Object: exit, Role: RoleChain},
	}
}

func outboundsByTag(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	m := map[string]map[string]any{}
	for _, o := range cfg.Outbounds {
		m[o["tag"].(string)] = o
	}
	return m
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestAssemble_ChainRoles(t *testing.T) {
	body, err := Assemble(AssembleOptions{InboundMode: InboundTun, DefaultOutbound: "ch", Multiplex: true}, chainOutbounds())
	if err != nil {
		t.Fatal(err)
	}
	obs := outboundsByTag(t, body)

	if got := strs(obs[OutboundAutoTag]["outbounds"]); !slices.Equal(got, []string{"A", "B", "C"}) {
		t.Errorf("auto = %v: chains and hops must stay out", got)
	}
	sel := obs[OutboundProxyTag]
	if got := strs(sel["outbounds"]); !slices.Equal(got, []string{"auto", "A", "B", "C", "ch", "direct"}) {
		t.Errorf("selector = %v: chain after servers, hop absent", got)
	}
	if sel["default"] != "ch" {
		t.Errorf("default = %v, want the chain", sel["default"])
	}
	if obs["ch.hop2"]["detour"] != "A" || obs["ch"]["detour"] != "ch.hop2" {
		t.Errorf("detours: hop=%v exit=%v", obs["ch.hop2"]["detour"], obs["ch"]["detour"])
	}
	if _, ok := obs["B"]["multiplex"]; !ok {
		t.Error("trojan server should get multiplex")
	}
	for _, tag := range []string{"ch.hop2", "ch"} {
		if _, ok := obs[tag]["multiplex"]; ok {
			t.Errorf("%s: detoured chain outbound must not nest h2mux in the entry's mux", tag)
		}
	}
	if _, ok := obs["A"]["detour"]; ok {
		t.Error("entry server must dial directly")
	}
}

func TestAssemble_OneServerOneChain(t *testing.T) {
	a := map[string]any{"type": "vless", "server": "1.1.1.1", "server_port": 443, "uuid": "u"}
	exit := map[string]any{"type": "vless", "server": "2.2.2.2", "server_port": 443, "uuid": "u", "detour": "A"}
	body, err := Assemble(AssembleOptions{InboundMode: InboundTun},
		[]ProxyOutbound{{Tag: "A", Object: a}, {Tag: "ch", Object: exit, Role: RoleChain}})
	if err != nil {
		t.Fatal(err)
	}
	obs := outboundsByTag(t, body)
	if _, ok := obs[OutboundAutoTag]; ok {
		t.Error("auto needs 2+ plain servers; a chain doesn't count")
	}
	if d := obs[OutboundProxyTag]["default"]; d != "A" {
		t.Errorf("default = %v, want the sole server", d)
	}
}

// TestAssemble_ChainSingBoxRun starts a real sing-box on a chained config:
// `check` misses some start-time errors (see dns detour), and sing-box doesn't
// dial servers at start, so unreachable addresses are fine.
func TestAssemble_ChainSingBoxRun(t *testing.T) {
	bin := singBoxBin(t, 12)
	dir := t.TempDir()
	body, err := Assemble(AssembleOptions{
		DefaultOptions:  DefaultOptions{LogPath: dir + "/sb.log", CachePath: dir + "/cache.db", ClashAddr: "127.0.0.1:" + strconv.Itoa(freePort(t))},
		InboundMode:     InboundSocks,
		InboundPort:     freePort(t),
		RouteDomains:    []string{"example.com"},
		DefaultOutbound: "ch",
		Multiplex:       true,
	}, chainOutbounds())
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := dir + "/config.json"
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, "check", "-c", cfgPath).CombinedOutput(); err != nil {
		t.Fatalf("sing-box check: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, bin, "run", "-c", cfgPath).CombinedOutput()
	logBody, _ := os.ReadFile(dir + "/sb.log")
	all := string(out) + string(logBody)
	if ctx.Err() == nil || strings.Contains(all, "FATAL") {
		t.Fatalf("sing-box did not stay up:\n%s", all)
	}
}
