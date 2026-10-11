package servers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CoOre/keenetic-sing-box-ui/internal/share"
)

func newStore(t *testing.T) *Store {
	return NewStore(filepath.Join(t.TempDir(), "servers.json"))
}

func TestStore_SaveListDelete(t *testing.T) {
	s := newStore(t)
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("expected empty, got %d", len(list))
	}
	e1, err := s.Save(Entry{Server: share.Server{Name: "A", Type: "vless", Server: "1.1.1.1", ServerPort: 443, UUID: "u"}})
	if err != nil {
		t.Fatal(err)
	}
	if e1.ID == "" {
		t.Error("expected generated ID")
	}
	s.Save(Entry{Server: share.Server{Name: "B", Type: "trojan", Server: "2.2.2.2", ServerPort: 443, Password: "p"}})

	list, _ := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}

	// Update e1 by ID.
	e1.Name = "A2"
	if _, err := s.Save(e1); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	if len(list) != 2 {
		t.Fatalf("update should not add, got %d", len(list))
	}
	var found bool
	for _, e := range list {
		if e.ID == e1.ID && e.Name == "A2" {
			found = true
		}
	}
	if !found {
		t.Error("update not applied")
	}

	// Delete.
	if _, err := s.Delete(e1.ID, false); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	if len(list) != 1 || list[0].Name != "B" {
		t.Errorf("delete wrong: %+v", list)
	}
}

func TestLayout_ServerTags(t *testing.T) {
	list := []Entry{
		{ID: "a1b2c3d4", Server: share.Server{Name: "My Server"}},
		{ID: "e5f6a7b8", Server: share.Server{Name: "My Server"}}, // dup name
		{ID: "11223344", Server: share.Server{Name: ""}},          // empty → fallback
	}
	tags := Layout(State{Servers: list}).Servers
	if tags[0] == tags[1] {
		t.Errorf("tags must be unique: %v", tags)
	}
	if tags[0] != "My-Server" {
		t.Errorf("sanitized tag: %q", tags[0])
	}
	if tags[2] == "" {
		t.Errorf("empty-name tag should have fallback: %v", tags)
	}
}

func snapshot(t *testing.T, s *Store) State {
	t.Helper()
	st, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStore_SetPrimary(t *testing.T) {
	s := newStore(t)
	a, _ := s.Save(Entry{Server: share.Server{Name: "A", Type: "vless", Server: "1.1.1.1", ServerPort: 443, UUID: "u"}})
	b, _ := s.Save(Entry{Server: share.Server{Name: "B", Type: "trojan", Server: "2.2.2.2", ServerPort: 443, Password: "p"}})

	tags, err := s.SetPrimary(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st := snapshot(t, s); st.Primary != b.ID || tags.Primary != "B" {
		t.Fatalf("primary = %q, tag %q", st.Primary, tags.Primary)
	}

	// Editing a server doesn't touch the pick.
	b.Name = "B2"
	s.Save(b)
	s.Save(a)
	if st := snapshot(t, s); st.Primary != b.ID {
		t.Fatalf("Save must not change primary: %q", st.Primary)
	}

	if _, err := s.SetPrimary(""); err != nil {
		t.Fatal(err)
	}
	if st := snapshot(t, s); st.Primary != "" || Layout(st).Primary != "" {
		t.Fatalf("auto should clear the pick: %+v", st)
	}
	if _, err := s.SetPrimary("nope"); err != ErrNotFound {
		t.Errorf("unknown id: %v", err)
	}

	// Deleting the primary server clears the pick.
	s.SetPrimary(a.ID)
	s.Delete(a.ID, false)
	if st := snapshot(t, s); st.Primary != "" {
		t.Errorf("pick of a deleted server: %q", st.Primary)
	}
}

// servers.json before chains: a plain list with the primary flagged. It is
// read as is and rewritten in the new form on the next save.
func TestStore_LegacyList(t *testing.T) {
	s := newStore(t)
	legacy := `[{"id":"a","name":"A","type":"vless"},{"id":"b","name":"B","type":"vless","primary":true}]`
	if err := os.WriteFile(s.Path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	st := snapshot(t, s)
	if len(st.Servers) != 2 || st.Primary != "b" || len(st.Chains) != 0 || st.Chains == nil {
		t.Fatalf("legacy: %+v", st)
	}
	if _, err := s.SetPrimary("a"); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(s.Path)
	if !strings.HasPrefix(string(body), "{") || strings.Contains(string(body), `"primary": true`) {
		t.Errorf("rewritten as: %s", body)
	}
	if st := snapshot(t, s); st.Primary != "a" || len(st.Servers) != 2 {
		t.Errorf("after rewrite: %+v", st)
	}
}

func TestStore_SyncSub(t *testing.T) {
	s := newStore(t)
	manual, _ := s.Save(Entry{Server: share.Server{Name: "M", Type: "trojan", Server: "9.9.9.9", ServerPort: 443, Password: "p"}})
	a := share.Server{Name: "A", Type: "vless", Server: "1.1.1.1", ServerPort: 443, UUID: "u1"}
	b := share.Server{Name: "B", Type: "vless", Server: "2.2.2.2", ServerPort: 443, UUID: "u2"}

	changed, _, err := s.SyncSub("sub1", []share.Server{a, b})
	if err != nil || !changed {
		t.Fatalf("first sync: changed=%v err=%v", changed, err)
	}
	list, _ := s.List()
	if len(list) != 3 || list[0].ID != manual.ID || list[1].SubID != "sub1" || list[2].Name != "B" {
		t.Fatalf("unexpected list: %+v", list)
	}
	if _, err := s.SetPrimary(list[2].ID); err != nil {
		t.Fatal(err)
	}
	idB := list[2].ID

	if changed, _, _ := s.SyncSub("sub1", []share.Server{a, b}); changed {
		t.Error("identical sync reported a change")
	}

	// B renamed (same server), A gone, C new: B keeps ID and primary.
	b.Name = "B — 12 GB left"
	c := share.Server{Name: "C", Type: "trojan", Server: "3.3.3.3", ServerPort: 443, Password: "x"}
	if changed, _, _ := s.SyncSub("sub1", []share.Server{b, c}); !changed {
		t.Fatal("expected change")
	}
	st := snapshot(t, s)
	list = st.Servers
	if len(list) != 3 || list[1].ID != idB || st.Primary != idB || list[1].Name != b.Name || list[2].Name != "C" {
		t.Fatalf("after resync: %+v", st)
	}

	// Editing a subscription server by hand must not detach it.
	e := list[1]
	e.SubID = ""
	e.Name = "edited"
	s.Save(e)
	list, _ = s.List()
	if list[1].SubID != "sub1" {
		t.Error("Save dropped SubID")
	}

	if _, err := s.DeleteSub("sub1", false); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	if len(list) != 1 || list[0].ID != manual.ID {
		t.Fatalf("after DeleteSub: %+v", list)
	}
}
