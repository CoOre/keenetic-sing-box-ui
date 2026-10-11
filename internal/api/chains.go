package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
	"github.com/CoOre/keenetic-sing-box-ui/internal/servers"
)

// buildOutbounds turns the stored servers and chains into config outbounds.
// A chain [A, B, C] becomes: A's own outbound (entry, not cloned), a clone of
// B dialed through A, and a clone of C dialed through that — the exit, tagged
// as the chain. Broken chains (a hop server is gone) are skipped with a
// warning rather than failing the whole apply.
func buildOutbounds(st servers.State) ([]config.ProxyOutbound, string, []string, error) {
	list, chains := st.Servers, st.Chains
	tags := servers.Layout(st)
	outs := make([]config.ProxyOutbound, 0, len(list))
	for i, e := range list {
		obj := e.Server.ToOutbound(tags.Servers[i])
		if obj == nil {
			return nil, "", nil, fmt.Errorf("server %q: unsupported type %q", e.Name, e.Type)
		}
		outs = append(outs, config.ProxyOutbound{Tag: tags.Servers[i], Object: obj})
	}
	for i, c := range chains {
		if tags.Broken(i) {
			continue
		}
		hops := tags.Hops[i]
		for j := 1; j < len(c.Hops); j++ {
			e := list[tags.Index[c.Hops[j]]]
			obj := e.Server.ToOutbound(hops[j])
			obj["detour"] = hops[j-1]
			role := config.RoleHop
			if j == len(c.Hops)-1 {
				role = config.RoleChain
			}
			outs = append(outs, config.ProxyOutbound{Tag: hops[j], Object: obj, Role: role})
		}
	}
	return outs, tags.Primary, tags.BrokenWarnings(chains), nil
}

// chainView is a stored chain plus its tag in the generated config, whether
// it's the primary pick, and the hop IDs that no longer exist (non-empty =
// broken, left out of the config).
type chainView struct {
	servers.Chain
	Tag     string   `json:"tag"`
	Primary bool     `json:"primary,omitempty"`
	Missing []string `json:"missing,omitempty"`
	Broken  bool     `json:"broken,omitempty"`
}

func chainViews(st servers.State, tags servers.Tags) []chainView {
	views := make([]chainView, len(st.Chains))
	for i, c := range st.Chains {
		views[i] = chainView{Chain: c, Tag: tags.Chains[i], Primary: st.IsPrimary(c.ID), Missing: tags.Missing[i], Broken: tags.Broken(i)}
	}
	return views
}

func (h *handlers) chainsSave(w http.ResponseWriter, r *http.Request) {
	var c servers.Chain
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	saved, err := h.d.Servers.SaveChain(c)
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, servers.ErrInvalidChain):
			code = http.StatusBadRequest
		case errors.Is(err, servers.ErrChainNotFound):
			code = http.StatusNotFound // deleted meanwhile (e.g. in another tab)
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (h *handlers) chainsDelete(w http.ResponseWriter, r *http.Request) {
	warnings, err := h.d.Servers.DeleteChain(r.PathValue("id"), dryRun(r))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, deleteResp{Warnings: warnings})
}
