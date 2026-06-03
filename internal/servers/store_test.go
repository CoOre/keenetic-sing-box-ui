package servers

import (
	"path/filepath"
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
	if err := s.Delete(e1.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.List()
	if len(list) != 1 || list[0].Name != "B" {
		t.Errorf("delete wrong: %+v", list)
	}
}

func TestUniqueTags(t *testing.T) {
	list := []Entry{
		{ID: "a1b2c3d4", Server: share.Server{Name: "My Server"}},
		{ID: "e5f6a7b8", Server: share.Server{Name: "My Server"}}, // dup name
		{ID: "11223344", Server: share.Server{Name: ""}},          // empty → fallback
	}
	tags := UniqueTags(list)
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
