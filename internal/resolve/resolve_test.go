package resolve

import (
	"testing"
	"time"
)

func TestCleanDomains(t *testing.T) {
	got := cleanDomains([]string{"ChatGPT.com", "  openai.com ", "#comment", "", "1.2.3.4", "chatgpt.com"})
	want := []string{"chatgpt.com", "openai.com"}
	if len(got) != len(want) {
		t.Fatalf("cleanDomains = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cleanDomains[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSignatureOrderIndependent(t *testing.T) {
	a := signature([]string{"1.1.1.1/32", "2.2.2.2/32"})
	b := signature([]string{"2.2.2.2/32", "1.1.1.1/32"})
	if a != b {
		t.Errorf("signature should be order-independent: %q != %q", a, b)
	}
	if a == signature([]string{"1.1.1.1/32"}) {
		t.Errorf("different sets must have different signatures")
	}
}

func TestUnionPrunesExpiredAndAppendsStatic(t *testing.T) {
	r := &Resolver{Grace: 10 * time.Minute}
	now := time.Now()
	r.seen = map[string]time.Time{
		"104.18.32.47": now,                       // fresh
		"9.9.9.9":      now.Add(-time.Hour),       // expired → pruned
		"8.8.8.8":      now.Add(-5 * time.Minute), // within grace
	}
	out := r.union(now, []string{"203.0.113.0/24"})

	has := func(v string) bool {
		for _, e := range out {
			if e == v {
				return true
			}
		}
		return false
	}
	if !has("104.18.32.47/32") || !has("8.8.8.8/32") {
		t.Errorf("fresh IPs missing from union: %v", out)
	}
	if has("9.9.9.9/32") {
		t.Errorf("expired IP should be pruned: %v", out)
	}
	if !has("203.0.113.0/24") {
		t.Errorf("static CIDR should be appended: %v", out)
	}
	if _, ok := r.seen["9.9.9.9"]; ok {
		t.Errorf("expired IP should be deleted from seen cache")
	}
}
