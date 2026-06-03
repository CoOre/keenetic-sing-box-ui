package lists

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FetchAndParse downloads url, parses it into domains/CIDRs according to typ,
// and returns the hash of the raw body (for change detection).
func FetchAndParse(url, typ string) (domains, cidrs []string, hash string, err error) {
	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Get(url) //nolint:noctx
	if err != nil {
		return nil, nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB cap
	if err != nil {
		return nil, nil, "", err
	}
	sum := sha256.Sum256(body)
	hash = fmt.Sprintf("%x", sum[:8])

	raw := parseEntries(body)
	domains, cidrs = classify(raw, typ)
	return domains, cidrs, hash, nil
}

// parseEntries extracts string entries from body regardless of format.
// Supports:
//  1. opencck wrapped object: {"site":{"domains":[],"cidr4":[],"ip4":[],...}}
//  2. plain object: {"domains":[],"cidr4":[],"ips":[],"subnets":[]}
//  3. JSON array: ["entry",...]
//  4. Plain text, one entry per line (# comments ignored)
func parseEntries(body []byte) []string {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return nil
	}

	if body[0] == '[' {
		// JSON array of strings
		var arr []string
		if json.Unmarshal(body, &arr) == nil {
			return cleanLines(arr)
		}
	}

	if body[0] == '{' {
		// Try opencck-wrapped: first value is the site object
		var wrapped map[string]json.RawMessage
		if json.Unmarshal(body, &wrapped) == nil {
			var entries []string
			for _, v := range wrapped {
				entries = append(entries, extractFromObject(v)...)
			}
			if len(entries) > 0 {
				return cleanLines(entries)
			}
		}
	}

	// Plain text
	return cleanLines(strings.Split(string(body), "\n"))
}

// extractFromObject pulls domain/CIDR arrays from a JSON object by well-known keys.
func extractFromObject(raw json.RawMessage) []string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	var out []string
	// "dns" contains resolver addresses (8.8.8.8:53) — not route targets, skip.
	for _, key := range []string{"domains", "ip4", "ip6", "cidr4", "cidr6", "ips", "subnets", "nets", "entries", "list"} {
		v, ok := obj[key]
		if !ok {
			continue
		}
		var arr []string
		if json.Unmarshal(v, &arr) == nil {
			out = append(out, arr...)
		}
	}
	return out
}

// classify splits raw entries into domains and CIDRs.
// typ controls the logic: "domains", "cidr", or "auto".
func classify(entries []string, typ string) (domains, cidrs []string) {
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		switch typ {
		case TypeDomains:
			domains = append(domains, e)
		case TypeCIDR:
			cidrs = append(cidrs, e)
		default: // auto
			if looksLikeCIDR(e) {
				cidrs = append(cidrs, e)
			} else {
				domains = append(domains, e)
			}
		}
	}
	return
}

// looksLikeCIDR returns true for entries that are IPv4/IPv6 addresses or CIDRs.
// Rejects IP:port patterns (e.g. "8.8.8.8:53" from DNS config fields).
func looksLikeCIDR(s string) bool {
	// Strip CIDR prefix length
	host := s
	if i := strings.IndexByte(s, '/'); i >= 0 {
		host = s[:i]
	}
	// Reject IP:port — colon followed by digits without brackets is a port spec
	if idx := strings.LastIndexByte(host, ':'); idx >= 0 {
		after := host[idx+1:]
		isPort := true
		for _, c := range after {
			if c < '0' || c > '9' {
				isPort = false
				break
			}
		}
		if isPort && len(after) > 0 && !strings.HasPrefix(host, "[") {
			return false // IP:port, not a CIDR
		}
		// Real IPv6 (contains colon but no port-like suffix, or bracketed)
		return true
	}
	// IPv4: all digits and dots with exactly 3 dots
	for _, c := range host {
		if (c < '0' || c > '9') && c != '.' {
			return false
		}
	}
	return strings.Count(host, ".") == 3
}

func cleanLines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//") {
			continue
		}
		out = append(out, s)
	}
	return out
}
