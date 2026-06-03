package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "config.json"))
	tick := 0
	s.Now = func() time.Time {
		tick++
		return time.Date(2026, 1, 1, 0, 0, tick, 0, time.UTC)
	}
	return s
}

func TestWrite_RejectsInvalidJSON(t *testing.T) {
	s := newStore(t)
	if _, err := s.Write([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestWrite_CreatesFileWithoutBackup(t *testing.T) {
	s := newStore(t)
	bk, err := s.Write([]byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if bk.Path != "" {
		t.Errorf("no prior file → no backup, got %+v", bk)
	}
	body, _ := os.ReadFile(s.Path)
	if string(body) != `{"a":1}` {
		t.Errorf("content: %q", body)
	}
}

func TestWrite_BackupsPrevious(t *testing.T) {
	s := newStore(t)
	if _, err := s.Write([]byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	bk, err := s.Write([]byte(`{"v":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if bk.Path == "" {
		t.Fatal("expected backup path")
	}
	body, _ := os.ReadFile(bk.Path)
	if string(body) != `{"v":1}` {
		t.Errorf("backup content: %q", body)
	}
	cur, _ := os.ReadFile(s.Path)
	if string(cur) != `{"v":2}` {
		t.Errorf("current content: %q", cur)
	}
}

func TestRotate_KeepsLastN(t *testing.T) {
	s := newStore(t)
	s.KeepBackup = 3
	for i := 0; i < 6; i++ {
		if _, err := s.Write([]byte(fmt.Sprintf(`{"v":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	backs, err := s.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backs) != 3 {
		t.Fatalf("expected 3 backups kept, got %d (%v)", len(backs), backs)
	}
	// Newest first → highest timestamps. Our fake Now starts at sec=1.
	// Total writes=6, snapshots taken for writes 2..6 (5 backups), kept newest 3.
	for _, p := range backs {
		if !strings.Contains(p, ".bak.") {
			t.Errorf("unexpected backup name: %s", p)
		}
	}
}

func TestListBackupMeta_AndReadBackup(t *testing.T) {
	s := newStore(t)
	if _, err := s.Write([]byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte(`{"v":3}`)); err != nil {
		t.Fatal(err)
	}
	// 3 writes → 2 backups (snapshots of v1 and v2).
	metas, err := s.ListBackupMeta()
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(metas))
	}
	// Newest first; timestamps parsed (non-zero).
	if metas[0].Timestamp.IsZero() || metas[0].Bytes == 0 {
		t.Errorf("bad meta: %+v", metas[0])
	}
	// Read the newest backup (snapshot of v2).
	body, err := s.ReadBackup(metas[0].Name)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(body) != `{"v":2}` {
		t.Errorf("backup content: %s", body)
	}
}

func TestReadBackup_RejectsTraversal(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"../etc/passwd", "config.json", "/etc/passwd", "foo/bar"} {
		if _, err := s.ReadBackup(bad); err == nil {
			t.Errorf("expected rejection for %q", bad)
		}
	}
}

func TestRead_Roundtrip(t *testing.T) {
	s := newStore(t)
	if _, err := s.Write([]byte(`{"x":42}`)); err != nil {
		t.Fatal(err)
	}
	body, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"x":42}` {
		t.Errorf("got %q", body)
	}
}
