package lists

import (
	"slices"
	"testing"
)

// opencck wraps each site in an object carrying both "ip4" (the addresses the
// site's domains currently resolve to) and "cidr4" (the whole ASN/cloud blocks
// the site might sit behind, e.g. 3.0.0.0/9 = all of AWS us-east). We must take
// only ip4/ip6 + domains and DROP cidr4/cidr6 — routing the cloud blocks drags
// unrelated CDN neighbours through the proxy.
func TestExtractFromObject_DropsCidrBlocks(t *testing.T) {
	body := []byte(`{
		"claude.ai": {
			"domains": ["claude.ai", "anthropic.com"],
			"ip4": ["1.2.3.4", "5.6.7.8"],
			"cidr4": ["3.0.0.0/9", "172.64.0.0/13"],
			"cidr6": ["2400:cb00::/32"],
			"dns": ["8.8.8.8:53"]
		}
	}`)
	got := parseEntries(body)

	mustHave := []string{"claude.ai", "anthropic.com", "1.2.3.4", "5.6.7.8"}
	for _, w := range mustHave {
		if !slices.Contains(got, w) {
			t.Errorf("expected %q in parsed entries, got %v", w, got)
		}
	}
	mustDrop := []string{"3.0.0.0/9", "172.64.0.0/13", "2400:cb00::/32", "8.8.8.8:53"}
	for _, w := range mustDrop {
		if slices.Contains(got, w) {
			t.Errorf("entry %q must be dropped but was present in %v", w, got)
		}
	}
}

// The resolved ip4 addresses must classify as CIDRs (as bare /32 hosts), while
// the hostnames classify as domains — so they land in the right sing-box rule.
func TestClassify_OpenCCK(t *testing.T) {
	body := []byte(`{"site":{"domains":["claude.ai"],"ip4":["1.2.3.4"],"cidr4":["3.0.0.0/9"]}}`)
	domains, cidrs := classify(parseEntries(body), TypeAuto)
	if !slices.Contains(domains, "claude.ai") {
		t.Errorf("claude.ai should be a domain, got domains=%v", domains)
	}
	if !slices.Contains(cidrs, "1.2.3.4") {
		t.Errorf("1.2.3.4 should be a cidr, got cidrs=%v", cidrs)
	}
	if slices.Contains(cidrs, "3.0.0.0/9") {
		t.Errorf("3.0.0.0/9 (cloud block) must not appear, got cidrs=%v", cidrs)
	}
}

// Auto sources contribute host routes only — subnets are dropped (they over-
// capture). An explicit type=cidr source keeps subnets (deliberate narrow block).
func TestClassify_AutoDropsSubnets(t *testing.T) {
	raw := []string{"1.2.3.4", "5.6.7.8/32", "9.9.9.0/24", "10.0.0.0/8", "2001:db8::/32"}

	_, autoCIDR := classify(raw, TypeAuto)
	for _, want := range []string{"1.2.3.4", "5.6.7.8/32"} {
		if !slices.Contains(autoCIDR, want) {
			t.Errorf("auto: host %q should be kept, got %v", want, autoCIDR)
		}
	}
	for _, drop := range []string{"9.9.9.0/24", "10.0.0.0/8", "2001:db8::/32"} {
		if slices.Contains(autoCIDR, drop) {
			t.Errorf("auto: subnet %q must be dropped, got %v", drop, autoCIDR)
		}
	}

	// Explicit cidr source keeps everything, subnets included.
	_, cidrCIDR := classify(raw, TypeCIDR)
	if !slices.Contains(cidrCIDR, "10.0.0.0/8") {
		t.Errorf("type=cidr: subnet 10.0.0.0/8 should be kept, got %v", cidrCIDR)
	}
}

func TestIsHostCIDR(t *testing.T) {
	cases := map[string]bool{
		"1.2.3.4": true, "1.2.3.4/32": true, "2001:db8::1": true, "2001:db8::1/128": true,
		"1.2.3.0/24": false, "10.0.0.0/8": false, "2001:db8::/32": false, "2001:db8::/64": false,
	}
	for in, want := range cases {
		if got := isHostCIDR(in); got != want {
			t.Errorf("isHostCIDR(%q)=%v, want %v", in, got, want)
		}
	}
}
