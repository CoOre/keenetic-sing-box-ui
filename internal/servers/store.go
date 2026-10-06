// Package servers persists the list of proxy servers the user has added via
// the UI form, and turns them into sing-box outbound objects for config
// assembly. The servers list is the source of truth; the sing-box config is
// generated from it.
package servers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/CoOre/keenetic-sing-box-ui/internal/share"
)

// Entry is a stored server: a share.Server plus a stable ID. Primary marks
// the server the "proxy" selector should point at; at most one entry has it,
// and none means "auto" (fastest by urltest). SubID links the entry to the
// subscription that manages it (see SyncSub); empty = added by hand.
type Entry struct {
	ID      string `json:"id"`
	Primary bool   `json:"primary,omitempty"`
	SubID   string `json:"sub_id,omitempty"`
	share.Server
}

type Store struct {
	Path string
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{Path: path} }

func (s *Store) load() ([]Entry, error) {
	body, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, err
	}
	var list []Entry
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) save(list []Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".new"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Save adds a new entry (when ID is empty) or replaces an existing one by ID.
// Returns the stored entry (with its ID). The Primary flag is owned by
// SetPrimary and SubID by SyncSub: both are kept from the stored entry, never
// taken from e.
func (s *Store) Save(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return Entry{}, err
	}
	e.Primary = false
	e.SubID = ""
	if e.ID == "" {
		e.ID = newID()
		list = append(list, e)
	} else {
		replaced := false
		for i := range list {
			if list[i].ID == e.ID {
				e.Primary = list[i].Primary
				e.SubID = list[i].SubID
				list[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			list = append(list, e)
		}
	}
	if err := s.save(list); err != nil {
		return Entry{}, err
	}
	return e, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, e := range list {
		if e.ID != id {
			out = append(out, e)
		}
	}
	return s.save(out)
}

// SyncSub replaces the entries owned by subscription subID with fresh, in
// order. Servers that match an existing entry (same type, address and
// credentials) keep its ID and Primary flag, so a refresh doesn't lose the
// user's main-server pick. The block stays where the subscription's first
// entry was; a new subscription is appended. Reports whether anything changed.
func (s *Store) SyncSub(subID string, fresh []share.Server) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return false, err
	}
	pos := -1
	var old []Entry
	rest := make([]Entry, 0, len(list))
	for _, e := range list {
		if e.SubID == subID {
			if pos < 0 {
				pos = len(rest)
			}
			old = append(old, e)
			continue
		}
		rest = append(rest, e)
	}
	if pos < 0 {
		pos = len(rest)
	}

	byKey := map[string][]Entry{}
	for _, e := range old {
		k := serverKey(e.Server)
		byKey[k] = append(byKey[k], e)
	}
	next := make([]Entry, 0, len(fresh))
	for _, srv := range fresh {
		e := Entry{SubID: subID, Server: srv}
		k := serverKey(srv)
		if m := byKey[k]; len(m) > 0 {
			e.ID, e.Primary = m[0].ID, m[0].Primary
			byKey[k] = m[1:]
		} else {
			e.ID = newID()
		}
		next = append(next, e)
	}
	if entriesEqual(old, next) {
		return false, nil
	}
	out := make([]Entry, 0, len(rest)+len(next))
	out = append(out, rest[:pos]...)
	out = append(out, next...)
	out = append(out, rest[pos:]...)
	return true, s.save(out)
}

// DeleteSub removes every entry owned by subscription subID.
func (s *Store) DeleteSub(subID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, e := range list {
		if e.SubID != subID {
			out = append(out, e)
		}
	}
	return s.save(out)
}

// serverKey identifies "the same server" across subscription refreshes: the
// name is excluded (providers often put live stats in it).
func serverKey(v share.Server) string {
	return strings.Join([]string{v.Type, strings.ToLower(v.Server), itoa(v.ServerPort), v.UUID, v.Password}, "|")
}

func entriesEqual(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, _ := json.Marshal(a[i])
		y, _ := json.Marshal(b[i])
		if string(x) != string(y) {
			return false
		}
	}
	return true
}

// ErrNotFound is returned by SetPrimary for an unknown ID.
var ErrNotFound = errors.New("server not found")

// SetPrimary marks the entry with the given ID as primary and clears the flag
// on all others. An empty id clears it everywhere (auto mode).
func (s *Store) SetPrimary(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	found := id == ""
	for i := range list {
		list[i].Primary = list[i].ID == id && id != ""
		found = found || list[i].Primary
	}
	if !found {
		return ErrNotFound
	}
	return s.save(list)
}

// PrimaryTag returns the outbound tag of the primary entry among list (tags
// as produced by UniqueTags), or "" when none is marked.
func PrimaryTag(list []Entry, tags []string) string {
	for i, e := range list {
		if e.Primary && i < len(tags) {
			return tags[i]
		}
	}
	return ""
}

var tagSanitize = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// Tag returns a sing-box outbound tag for an entry: a sanitized name, or a
// fallback based on the ID. Uniqueness across the list is the caller's job
// (see UniqueTags).
func (e Entry) Tag() string {
	name := strings.TrimSpace(e.Name)
	if name == "" {
		return "server-" + shortID(e.ID)
	}
	t := tagSanitize.ReplaceAllString(name, "-")
	t = strings.Trim(t, "-")
	if t == "" {
		return "server-" + shortID(e.ID)
	}
	return t
}

// UniqueTags returns tags for the entries, de-duplicating collisions with a
// numeric suffix so every outbound tag is unique.
func UniqueTags(list []Entry) []string {
	seen := map[string]int{}
	out := make([]string, len(list))
	for i, e := range list {
		base := e.Tag()
		t := base
		for {
			if seen[t] == 0 {
				seen[t] = 1
				break
			}
			seen[base]++
			t = base + "-" + itoa(seen[base])
		}
		out[i] = t
	}
	return out
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "id0000000000"
	}
	return hex.EncodeToString(b)
}

func shortID(id string) string {
	if len(id) >= 6 {
		return id[:6]
	}
	if id == "" {
		return "x"
	}
	return id
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
