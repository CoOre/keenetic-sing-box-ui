package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultConfig_HasRequiredFields(t *testing.T) {
	body, err := DefaultConfig(DefaultOptions{})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	logBlock, ok := cfg["log"].(map[string]any)
	if !ok || logBlock["output"] != DefaultLogPath {
		t.Errorf("log.output: %v", logBlock)
	}
	if logBlock["level"] != "info" || logBlock["timestamp"] != true {
		t.Errorf("log block: %v", logBlock)
	}

	exp, ok := cfg["experimental"].(map[string]any)
	if !ok {
		t.Fatalf("missing experimental")
	}
	clash, ok := exp["clash_api"].(map[string]any)
	if !ok {
		t.Fatalf("missing clash_api")
	}
	if clash["external_controller"] != DefaultClashAddr {
		t.Errorf("controller: %v", clash["external_controller"])
	}
	if s, _ := clash["secret"].(string); len(s) < 32 {
		t.Errorf("secret too short: %q", s)
	}
	cache, ok := exp["cache_file"].(map[string]any)
	if !ok || cache["enabled"] != true || cache["path"] != DefaultCachePath {
		t.Errorf("cache_file: %v", cache)
	}

	outs := cfg["outbounds"].([]any)
	tags := map[string]bool{}
	for _, o := range outs {
		m := o.(map[string]any)
		if tag, ok := m["tag"].(string); ok {
			tags[tag] = true
		}
	}
	for _, want := range []string{OutboundProxyTag, OutboundDirectTag, OutboundAutoTag} {
		if !tags[want] {
			t.Errorf("missing outbound tag %q (got %v)", want, tags)
		}
	}

	route := cfg["route"].(map[string]any)
	if route["final"] != OutboundProxyTag {
		t.Errorf("route.final: %v", route["final"])
	}
	// Modern schema requirement for 1.12+: default_domain_resolver present.
	if _, ok := route["default_domain_resolver"]; !ok {
		t.Errorf("route.default_domain_resolver missing")
	}
	// DNS servers must use the new typed format (no legacy `address`).
	dnsServers := cfg["dns"].(map[string]any)["servers"].([]any)
	for _, raw := range dnsServers {
		s := raw.(map[string]any)
		if _, legacy := s["address"]; legacy {
			t.Errorf("legacy DNS `address` field present: %v", s)
		}
		if _, ok := s["type"]; !ok {
			t.Errorf("DNS server missing `type`: %v", s)
		}
	}
}

func TestDefaultConfig_SecretIsRandom(t *testing.T) {
	a, _ := DefaultConfig(DefaultOptions{})
	b, _ := DefaultConfig(DefaultOptions{})
	if extractSecret(t, a) == extractSecret(t, b) {
		t.Errorf("secrets should differ between invocations")
	}
}

func TestDefaultConfig_HonorsExplicitSecret(t *testing.T) {
	body, err := DefaultConfig(DefaultOptions{ClashSecret: "explicit-secret-value"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "explicit-secret-value") {
		t.Errorf("secret not embedded")
	}
}

func extractSecret(t *testing.T, body []byte) string {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg["experimental"].(map[string]any)["clash_api"].(map[string]any)["secret"].(string)
}
