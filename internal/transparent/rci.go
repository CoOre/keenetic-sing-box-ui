package transparent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// PolicyMark resolves a KeeneticOS policy's fwmark by its description (the name
// shown in the router UI), via the local RCI endpoint. Returns "" if RCI is
// unreachable or the policy isn't found. The returned value is "0x"+mark, ready
// to use in iptables/ip rule. Mirrors SKeen's get_mark_policy.
//
// Binding to a policy mark is what lets selected proxying ride alongside the
// router's existing WireGuard/OpenConnect policy routing instead of fighting it.
func PolicyMark(ctx context.Context, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	body := rciPost(ctx, "show/ip/policy")
	if len(body) == 0 {
		return ""
	}
	// Response shape: {"policy": {"Policy0": {"description": "...", "mark": "..."}, ...}}
	var resp struct {
		Policy map[string]struct {
			Description string `json:"description"`
			Mark        string `json:"mark"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	want := strings.ToLower(name)
	for _, p := range resp.Policy {
		if strings.ToLower(strings.TrimSpace(p.Description)) == want && p.Mark != "" {
			return "0x" + strings.TrimPrefix(p.Mark, "0x")
		}
	}
	return ""
}

// Policy is a KeeneticOS routing policy as seen via RCI.
type Policy struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Mark        string `json:"mark"`
}

// ListPolicies returns the router's configured routing policies, so the UI can
// offer them for binding. Empty slice if RCI is unreachable.
func ListPolicies(ctx context.Context) []Policy {
	body := rciPost(ctx, "show/ip/policy")
	if len(body) == 0 {
		return nil
	}
	var resp struct {
		Policy map[string]struct {
			Description string `json:"description"`
			Mark        string `json:"mark"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	out := make([]Policy, 0, len(resp.Policy))
	for id, p := range resp.Policy {
		out = append(out, Policy{ID: id, Description: p.Description, Mark: p.Mark})
	}
	return out
}

func rciPost(ctx context.Context, path string) []byte {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rciURL+"/"+path, bytes.NewBufferString("{}"))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	return buf.Bytes()
}
