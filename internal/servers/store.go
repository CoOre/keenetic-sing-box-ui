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

// Entry is a stored server: a share.Server plus a stable ID.
type Entry struct {
	ID string `json:"id"`
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
// Returns the stored entry (with its ID).
func (s *Store) Save(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return Entry{}, err
	}
	if e.ID == "" {
		e.ID = newID()
		list = append(list, e)
	} else {
		replaced := false
		for i := range list {
			if list[i].ID == e.ID {
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
