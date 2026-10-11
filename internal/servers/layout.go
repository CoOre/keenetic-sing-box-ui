package servers

import (
	"fmt"
	"strings"

	"github.com/CoOre/keenetic-sing-box-ui/internal/config"
)

// Tags is the outbound tag layout of the generated config for a server list
// and its chains. Every tag in it is unique, and none collides with the
// config's own outbounds (proxy, auto, direct, block).
type Tags struct {
	// Servers holds one tag per entry, in list order.
	Servers []string
	// Chains holds one tag per chain: the tag of its exit outbound, which is
	// what the "proxy" selector lists.
	Chains []string
	// Hops holds, per intact chain, the tag of each hop outbound in order:
	// Hops[i][0] is the entry server's own tag (not cloned), the middle ones
	// are detoured clones, and the last equals Chains[i]. Nil for a broken chain.
	Hops [][]string
	// Missing holds, per chain, the hop server IDs that aren't in the list.
	// A chain with any is broken: it's left out of the config.
	Missing [][]string
	// Primary is the selector default: the tag of the primary server or
	// intact chain; "" when none (auto / the sole server).
	Primary string
	// Index maps a server entry ID to its position in the list.
	Index map[string]int
}

// Broken reports whether chain i can't be built: a hop references a server
// that no longer exists (see Missing), or the stored chain is malformed.
func (t Tags) Broken(i int) bool { return t.Hops[i] == nil }

// BrokenReason says why chain i is broken, for the user; "" if it isn't.
func (t Tags) BrokenReason(i int) string {
	switch {
	case !t.Broken(i):
		return ""
	case len(t.Missing[i]) > 0:
		return "одного из её серверов больше нет (удалён или сменил адрес в подписке)"
	default:
		return "в ней меньше двух серверов"
	}
}

// Layout assigns outbound tags. Servers are tagged first, then chain exits,
// then hop clones: adding or renaming a chain never renames a server, and
// editing a chain's hops never renames another chain (either would break a
// selector pick restored from cache_file).
func Layout(st State) Tags {
	list, chains := st.Servers, st.Chains
	seen := map[string]int{}
	for _, r := range []string{config.OutboundProxyTag, config.OutboundAutoTag, config.OutboundDirectTag, config.OutboundBlockTag} {
		seen[r] = 1
	}
	alloc := func(base string) string {
		t := base
		for seen[t] != 0 {
			seen[base]++
			t = base + "-" + itoa(seen[base])
		}
		seen[t] = 1
		return t
	}

	out := Tags{
		Servers: make([]string, len(list)),
		Chains:  make([]string, len(chains)),
		Hops:    make([][]string, len(chains)),
		Missing: make([][]string, len(chains)),
	}
	out.Index = make(map[string]int, len(list))
	for i, e := range list {
		out.Servers[i] = alloc(e.Tag())
		out.Index[e.ID] = i
	}
	for i, c := range chains {
		out.Chains[i] = alloc(c.Tag())
	}
	for i, c := range chains {
		for _, id := range c.Hops {
			if _, ok := out.Index[id]; !ok {
				out.Missing[i] = append(out.Missing[i], id)
			}
		}
		if len(out.Missing[i]) > 0 || len(c.Hops) < 2 {
			continue // broken (or a hand-edited one-hop chain): left out
		}
		hops := make([]string, len(c.Hops))
		hops[0] = out.Servers[out.Index[c.Hops[0]]]
		for j := 1; j < len(c.Hops)-1; j++ {
			hops[j] = alloc(out.Chains[i] + ".hop" + itoa(j+1))
		}
		hops[len(hops)-1] = out.Chains[i]
		out.Hops[i] = hops
	}

	if i, ok := out.Index[st.Primary]; ok {
		out.Primary = out.Servers[i]
	}
	for i, c := range chains {
		if st.IsPrimary(c.ID) && !out.Broken(i) {
			out.Primary = out.Chains[i]
		}
	}
	return out
}

// BrokenWarnings explains each broken chain, for the user: such chains are
// left out of the config.
func (t Tags) BrokenWarnings(chains []Chain) []string {
	var out []string
	for i := range chains {
		if t.Broken(i) {
			out = append(out, t.chainWarning(chains, i))
		}
	}
	return out
}

func (t Tags) chainWarning(chains []Chain, i int) string {
	return fmt.Sprintf("цепочка %q пропущена: %s", chainLabel(chains, t, i), t.BrokenReason(i))
}

// chainLabel names chain i for the user: its name, else its tag.
func chainLabel(chains []Chain, t Tags, i int) string {
	if chains[i].Name != "" {
		return chains[i].Name
	}
	return t.Chains[i]
}

// changeWarnings explains what a change from before to after breaks: a
// primary server that's gone, chains that were intact and now are broken,
// and a primary chain that's gone. Chains broken earlier aren't repeated, so
// a refresh reports only its own damage. Chains are matched by ID.
func changeWarnings(before, after State) []string {
	was, now := Layout(before), Layout(after)
	chainsBefore, chainsAfter := before.Chains, after.Chains
	var out []string
	if i, ok := was.Index[before.Primary]; ok {
		if _, kept := now.Index[before.Primary]; !kept {
			out = append(out, fmt.Sprintf("основной сервер %q исчезнет из конфига — после применения основным станет %s",
				was.Servers[i], pickLabel(now)))
		}
	}
	nowAt := make(map[string]int, len(chainsAfter))
	for i, c := range chainsAfter {
		nowAt[c.ID] = i
	}
	for i, c := range chainsBefore {
		if was.Broken(i) {
			continue
		}
		wasMain := before.IsPrimary(c.ID)
		j, kept := nowAt[c.ID]
		switch {
		case !kept && wasMain:
			out = append(out, fmt.Sprintf("основная цепочка %q исчезнет из конфига — после применения основным станет %s",
				chainLabel(chainsBefore, was, i), pickLabel(now)))
		case kept && now.Broken(j):
			w := fmt.Sprintf("цепочка %q перестанет работать: %s", chainLabel(chainsAfter, now, j), now.BrokenReason(j))
			if wasMain {
				w += "; она была основной — после применения основным станет " + pickLabel(now)
			}
			out = append(out, w)
		}
	}
	return out
}

// pickLabel names the selector default of t: the primary server or chain,
// else the fallback.
func pickLabel(t Tags) string {
	switch {
	case strings.HasPrefix(t.Primary, chainTagPrefix):
		return fmt.Sprintf("цепочка %q", t.Primary)
	case t.Primary != "":
		return fmt.Sprintf("%q", t.Primary)
	}
	return fallbackLabel(t)
}

// fallbackLabel names the selector default when nothing is primary.
func fallbackLabel(t Tags) string {
	switch p := config.DefaultPick("", t.Servers, nil); p {
	case config.OutboundAutoTag:
		return "автовыбор"
	case config.OutboundDirectTag:
		return "прямое подключение"
	default:
		return fmt.Sprintf("%q", p)
	}
}

// sanitizeTag turns a display name into an outbound tag, or returns fallback
// when nothing usable is left.
func sanitizeTag(name, fallback string) string {
	t := tagSanitize.ReplaceAllString(strings.TrimSpace(name), "-")
	t = strings.Trim(t, "-")
	if t == "" {
		return fallback
	}
	return t
}
