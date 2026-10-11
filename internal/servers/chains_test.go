package servers

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/share"
)

// threeServers stores A, B, C and returns their IDs.
func threeServers(t *testing.T, s *Store) []string {
	t.Helper()
	var ids []string
	for i, name := range []string{"A", "B", "C"} {
		e, err := s.Save(Entry{Server: share.Server{Name: name, Type: "vless", Server: "10.0.0." + itoa(i+1), ServerPort: 443, UUID: "u"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	return ids
}

func TestStore_ChainCRUD(t *testing.T) {
	s := newStore(t)
	ids := threeServers(t, s)

	c, err := s.SaveChain(Chain{Name: "  via B  ", Hops: []string{ids[0], ids[1]}})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Name != "via B" {
		t.Fatalf("saved: %+v (ID generated, name trimmed)", c)
	}

	c.Hops = []string{ids[0], ids[1], ids[2]}
	if _, err := s.SaveChain(c); err != nil {
		t.Fatal(err)
	}
	chains := snapshot(t, s).Chains
	if len(chains) != 1 || len(chains[0].Hops) != 3 {
		t.Fatalf("update: %+v", chains)
	}

	if _, err := s.DeleteChain(c.ID, false); err != nil {
		t.Fatal(err)
	}
	if chains := snapshot(t, s).Chains; len(chains) != 0 {
		t.Fatalf("delete: %+v", chains)
	}
}

func TestStore_SaveChainValidates(t *testing.T) {
	s := newStore(t)
	ids := threeServers(t, s)
	for name, hops := range map[string][]string{
		"one hop":   {ids[0]},
		"unknown":   {ids[0], "nope"},
		"duplicate": {ids[0], ids[1], ids[0]},
		"too long":  {ids[0], ids[1], ids[2], "x", "y", "z"},
	} {
		if _, err := s.SaveChain(Chain{Hops: hops}); !errors.Is(err, ErrInvalidChain) {
			t.Errorf("%s: err = %v, want ErrInvalidChain", name, err)
		}
	}
	if chains := snapshot(t, s).Chains; len(chains) != 0 {
		t.Errorf("invalid chains must not be stored: %+v", chains)
	}
	// Saving by an ID that isn't stored (deleted in another tab) must not
	// resurrect it.
	if _, err := s.SaveChain(Chain{ID: "gone", Hops: ids[:2]}); !errors.Is(err, ErrChainNotFound) {
		t.Errorf("unknown ID: err = %v, want ErrChainNotFound", err)
	}
}

func TestStore_SaveChainDuplicateName(t *testing.T) {
	s := newStore(t)
	ids := threeServers(t, s)
	c, err := s.SaveChain(Chain{Name: "foo", Hops: ids[:2]})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"foo", " foo ", "foo!"} {
		if _, err := s.SaveChain(Chain{Name: name, Hops: ids[1:]}); !errors.Is(err, ErrInvalidChain) {
			t.Errorf("%q: %v, want ErrInvalidChain", name, err)
		}
	}
	c.Hops = ids[1:]
	if _, err := s.SaveChain(c); err != nil {
		t.Errorf("re-saving the chain itself: %v", err)
	}
}

func TestStore_SetPrimaryChain(t *testing.T) {
	s := newStore(t)
	ids := threeServers(t, s)
	c, _ := s.SaveChain(Chain{Name: "ch", Hops: []string{ids[0], ids[1]}})

	if _, err := s.SetPrimary(ids[2]); err != nil {
		t.Fatal(err)
	}
	tags, err := s.SetPrimary(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st := snapshot(t, s); st.Primary != c.ID || tags.Primary != "chain-ch" {
		t.Fatalf("chain must be primary: %q, tag %q", st.Primary, tags.Primary)
	}
	// Editing the chain keeps the pick.
	c.Name = "ch2"
	s.SaveChain(c)
	if st := snapshot(t, s); st.Primary != c.ID {
		t.Error("SaveChain must keep the pick")
	}

	// A broken chain can't become primary; the pick stays as it was.
	s.SetPrimary(ids[2])
	s.Delete(ids[1], false)
	_, err = s.SetPrimary(c.ID)
	if !errors.Is(err, ErrChainBroken) || !strings.Contains(err.Error(), "больше нет") {
		t.Fatalf("broken chain: err = %v, want ErrChainBroken", err)
	}
	if st := snapshot(t, s); st.Primary != ids[2] {
		t.Errorf("refused SetPrimary must not touch the pick: %q", st.Primary)
	}

	// Deleting the primary chain clears the pick.
	s.SetPrimary("")
	c2, _ := s.SaveChain(Chain{Name: "other", Hops: []string{ids[0], ids[2]}})
	s.SetPrimary(c2.ID)
	s.DeleteChain(c2.ID, false)
	if st := snapshot(t, s); st.Primary != "" {
		t.Errorf("pick of a deleted chain: %q", st.Primary)
	}
}

func TestLayout(t *testing.T) {
	list := []Entry{
		{ID: "aaaaaa1", Server: share.Server{Name: "A"}},
		{ID: "bbbbbb2", Server: share.Server{Name: "B"}},
		{ID: "cccccc3", Server: share.Server{Name: "direct"}}, // reserved
	}
	before := Layout(State{Servers: list}).Servers
	chains := []Chain{
		{ID: "c1", Name: "A", Hops: []string{"aaaaaa1", "bbbbbb2", "cccccc3"}}, // same name as a server
		{ID: "c2", Name: "gone", Hops: []string{"aaaaaa1", "zzz"}},
		{ID: "c3", Hops: []string{"bbbbbb2", "aaaaaa1"}},
		{ID: "c4", Name: "short", Hops: []string{"aaaaaa1"}}, // hand-edited file
	}
	tags := Layout(State{Servers: list, Chains: chains})

	if !slices.Equal(tags.Servers, before) {
		t.Errorf("chains must not rename servers: %v vs %v", tags.Servers, before)
	}
	if tags.Servers[2] != "direct-2" {
		t.Errorf("reserved tag must be suffixed: %q", tags.Servers[2])
	}
	if tags.Chains[0] != "chain-A" {
		t.Errorf("chains live in their own tag space: %q", tags.Chains[0])
	}
	if want := []string{"A", "chain-A.hop2", "chain-A"}; !slices.Equal(tags.Hops[0], want) {
		t.Errorf("hops = %v, want %v", tags.Hops[0], want)
	}
	if !tags.Broken(1) || !slices.Equal(tags.Missing[1], []string{"zzz"}) || tags.Hops[1] != nil {
		t.Errorf("broken chain: missing=%v hops=%v", tags.Missing[1], tags.Hops[1])
	}
	if tags.Chains[2] != "chain-c3" || tags.Broken(2) {
		t.Errorf("fallback tag: %q broken=%v", tags.Chains[2], tags.Broken(2))
	}
	if !tags.Broken(3) || !strings.Contains(tags.BrokenReason(3), "меньше двух") {
		t.Errorf("one-hop chain: broken=%v %q", tags.Broken(3), tags.BrokenReason(3))
	}
	all := append(append([]string{}, tags.Servers...), tags.Chains...)
	all = append(all, tags.Hops[0][1])
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i] == all[j] {
				t.Errorf("duplicate tag %q", all[i])
			}
		}
	}
}

func TestLayout_Primary(t *testing.T) {
	list := []Entry{{ID: "a", Server: share.Server{Name: "A"}}, {ID: "b", Server: share.Server{Name: "B"}}}
	st := func(primary string, hops ...string) State {
		return State{Servers: list, Chains: []Chain{{ID: "c", Name: "CH", Hops: hops}}, Primary: primary}
	}
	if p := Layout(st("c", "a", "b")).Primary; p != "chain-CH" {
		t.Errorf("primary chain: %q", p)
	}
	if p := Layout(st("b", "a", "b")).Primary; p != "B" {
		t.Errorf("primary server: %q", p)
	}
	// A broken primary chain or a dangling ID falls back to "" (auto / the
	// sole server).
	if p := Layout(st("c", "a", "gone")).Primary; p != "" {
		t.Errorf("broken primary chain: %q", p)
	}
	if p := Layout(st("gone", "a", "b")).Primary; p != "" {
		t.Errorf("dangling pick: %q", p)
	}
}

func TestLayout_HopClonesDontShiftChainTags(t *testing.T) {
	list := []Entry{{ID: "s1", Server: share.Server{Name: "S1"}}, {ID: "s2", Server: share.Server{Name: "S2"}}, {ID: "s3", Server: share.Server{Name: "S3"}}}
	chains := []Chain{
		{ID: "a", Name: "a", Hops: []string{"s1", "s2", "s3"}},
		{ID: "b", Name: "a.hop2", Hops: []string{"s1", "s2"}},
	}
	long := Layout(State{Servers: list, Chains: chains})
	chains[0].Hops = []string{"s1", "s3"}
	short := Layout(State{Servers: list, Chains: chains})
	if long.Chains[1] != short.Chains[1] || short.Chains[1] != "chain-a.hop2" {
		t.Errorf("chain b tag: %q with 3 hops in a, %q with 2, want chain-a.hop2 both", long.Chains[1], short.Chains[1])
	}
	if long.Hops[0][1] == long.Chains[1] {
		t.Errorf("hop clone collides with chain b: %v", long.Hops[0])
	}
}

func TestLayout_ServerNamedLikeChain(t *testing.T) {
	list := []Entry{
		{ID: "a", Server: share.Server{Name: "A"}},
		{ID: "b", Server: share.Server{Name: "B"}},
		{ID: "x", Server: share.Server{Name: "chain-foo"}},
	}
	tags := Layout(State{Servers: list, Chains: []Chain{{ID: "c", Name: "foo", Hops: []string{"a", "b"}}}})
	if tags.Chains[0] != "chain-foo" || tags.Servers[2] != "server-chain-foo" {
		t.Errorf("chain %q, server %q", tags.Chains[0], tags.Servers[2])
	}
}

// Two servers named "chain" would be deduplicated into "chain-2", the tag of
// a chain named "2".
func TestLayout_ServersNamedChainDedup(t *testing.T) {
	list := []Entry{
		{ID: "a", Server: share.Server{Name: "chain"}},
		{ID: "b", Server: share.Server{Name: "chain"}},
	}
	tags := Layout(State{Servers: list, Chains: []Chain{{ID: "c", Name: "2", Hops: []string{"a", "b"}}}})
	if tags.Chains[0] != "chain-2" {
		t.Errorf("chain tag %q, want chain-2 (servers: %v)", tags.Chains[0], tags.Servers)
	}
}

func TestStore_ChainSurvivesSubRefresh(t *testing.T) {
	s := newStore(t)
	a := share.Server{Name: "A", Type: "vless", Server: "1.1.1.1", ServerPort: 443, UUID: "u1"}
	b := share.Server{Name: "B", Type: "vless", Server: "2.2.2.2", ServerPort: 443, UUID: "u2"}
	s.SyncSub("sub", []share.Server{a, b})
	list, _ := s.List()
	if _, err := s.SaveChain(Chain{Hops: []string{list[0].ID, list[1].ID}}); err != nil {
		t.Fatal(err)
	}

	a.Name, b.Name = "A (12 ms)", "B (40 ms)" // providers put live stats in names
	s.SyncSub("sub", []share.Server{b, a})
	if Layout(snapshot(t, s)).Broken(0) {
		t.Fatal("refresh with same credentials must keep hop IDs")
	}

	// Rotated credentials, same address and name: still the same server.
	b.UUID = "rotated"
	s.SyncSub("sub", []share.Server{b, a})
	st := snapshot(t, s)
	if Layout(st).Broken(0) {
		t.Fatal("rotated credentials must keep the ID (matched by address and name)")
	}
	for _, e := range st.Servers {
		if e.Server.Server == "2.2.2.2" && e.UUID != "rotated" {
			t.Errorf("rotated server must carry the fresh credentials: %+v", e)
		}
	}

	// A new address is a different server: the chain breaks, and the refresh
	// says so.
	b.Server = "3.3.3.3"
	_, warnings, _ := s.SyncSub("sub", []share.Server{b, a})
	if !Layout(snapshot(t, s)).Broken(0) {
		t.Error("a moved server gets a new ID: the chain is expected to break")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "перестанет работать") {
		t.Errorf("warnings = %q", warnings)
	}
}

// Servers sharing one CDN host and UUID are told apart by name: a reorder
// keeps each ID with its name.
func TestStore_SyncSubSharedAddressFollowsNames(t *testing.T) {
	s := newStore(t)
	mk := func(name string) share.Server {
		return share.Server{Name: name, Type: "vless", Server: "cdn.example", ServerPort: 443, UUID: "u"}
	}
	s.SyncSub("sub", []share.Server{mk("NL"), mk("DE")})
	before, _ := s.List()
	s.SyncSub("sub", []share.Server{mk("DE"), mk("NL")})
	after, _ := s.List()
	if after[0].ID != before[1].ID || after[1].ID != before[0].ID {
		t.Errorf("IDs must follow names: before NL=%s DE=%s, after DE=%s NL=%s", before[0].ID, before[1].ID, after[0].ID, after[1].ID)
	}
}

// Exact matches win over looser ones: two servers on one host:port with
// different credentials keep their own IDs.
func TestStore_SyncSubExactFirst(t *testing.T) {
	s := newStore(t)
	a := share.Server{Type: "vless", Server: "1.1.1.1", ServerPort: 443, UUID: "u1"}
	b := share.Server{Type: "vless", Server: "1.1.1.1", ServerPort: 443, UUID: "u2"}
	s.SyncSub("sub", []share.Server{a, b})
	before, _ := s.List()
	s.SyncSub("sub", []share.Server{b, a})
	after, _ := s.List()
	if after[0].ID != before[1].ID || after[1].ID != before[0].ID {
		t.Errorf("IDs must follow credentials: before %v/%v after %v/%v", before[0].ID, before[1].ID, after[0].ID, after[1].ID)
	}
}

func TestStore_DeleteWarnings(t *testing.T) {
	s := newStore(t)
	ids := threeServers(t, s)
	c, err := s.SaveChain(Chain{Name: "via", Hops: ids[:2]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPrimary(c.ID); err != nil {
		t.Fatal(err)
	}

	w, err := s.Delete(ids[1], true)
	if err != nil || len(w) != 1 || !strings.Contains(w[0], `"via"`) || !strings.Contains(w[0], "автовыбор") {
		t.Errorf("server dry run: %q %v", w, err)
	}
	if list, _ := s.List(); len(list) != 3 {
		t.Fatal("dry run deleted the server")
	}
	if w, _ := s.Delete(ids[2], false); len(w) != 0 {
		t.Errorf("unused server: %q", w)
	}

	w, err = s.DeleteChain(c.ID, true)
	if err != nil || len(w) != 1 || !strings.Contains(w[0], "основная цепочка") {
		t.Errorf("chain dry run: %q %v", w, err)
	}
	if len(snapshot(t, s).Chains) != 1 {
		t.Fatal("dry run deleted the chain")
	}
	if w, _ := s.DeleteChain(c.ID, false); len(w) != 1 || len(snapshot(t, s).Chains) != 0 {
		t.Errorf("chain delete: %q", w)
	}

	// The primary server going away.
	s.SetPrimary(ids[0])
	if w, _ := s.Delete(ids[0], true); len(w) != 1 || !strings.Contains(w[0], `основной сервер "A"`) {
		t.Errorf("primary server: %q", w)
	}
}
