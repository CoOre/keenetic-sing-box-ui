// Package lists manages URL-based route list sources: periodic HTTP fetching,
// parsing, caching, and change detection. Parsed entries are merged into the
// sing-box config at assembly time so the router automatically routes new
// domains/CIDRs as the remote lists evolve.
package lists

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const defaultInterval = 60 // minutes

// Type controls how fetched entries are classified.
const (
	TypeAuto    = "auto"    // auto-detect: IPs→cidr, hostnames→domains
	TypeDomains = "domains" // treat all entries as domain names
	TypeCIDR    = "cidr"    // treat all entries as IP/CIDR
)

// Source is one URL-based list source.
type Source struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Type     string `json:"type"`     // "auto" | "domains" | "cidr"
	Interval int    `json:"interval"` // minutes between fetches; 0 = default (60)
	Enabled  bool   `json:"enabled"`

	// Updated by the runner; persisted so counts survive UI restarts.
	LastFetch *time.Time `json:"last_fetch,omitempty"`
	LastHash  string     `json:"last_hash,omitempty"`
	LastCount int        `json:"last_count"`
	LastError string     `json:"last_error,omitempty"`

	// Cached parsed entries (persisted). Populated after a successful fetch.
	Domains []string `json:"domains,omitempty"`
	CIDRs   []string `json:"cidrs,omitempty"`
}

func (s *Source) intervalDur() time.Duration {
	m := s.Interval
	if m <= 0 {
		m = defaultInterval
	}
	return time.Duration(m) * time.Minute
}

func (s *Source) isDue() bool {
	if s.LastFetch == nil {
		return true
	}
	return time.Since(*s.LastFetch) >= s.intervalDur()
}

// Store persists sources to a JSON file with atomic writes.
type Store struct {
	Path string
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{Path: path} }

type storeFile struct {
	Sources []*Source `json:"sources"`
}

func (s *Store) load() ([]*Source, error) {
	b, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var f storeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return f.Sources, nil
}

func (s *Store) save(srcs []*Source) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(storeFile{Sources: srcs}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// List returns all sources.
func (s *Store) List() ([]*Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Add persists a new source, assigning a random ID.
func (s *Store) Add(src Source) (*Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srcs, err := s.load()
	if err != nil {
		return nil, err
	}
	src.ID = shortID()
	if src.Type == "" {
		src.Type = TypeAuto
	}
	src.Enabled = true
	srcs = append(srcs, &src)
	if err := s.save(srcs); err != nil {
		return nil, err
	}
	return &src, nil
}

// Delete removes a source by ID.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	srcs, err := s.load()
	if err != nil {
		return err
	}
	var kept []*Source
	for _, src := range srcs {
		if src.ID != id {
			kept = append(kept, src)
		}
	}
	return s.save(kept)
}

// Update replaces a source (matched by ID).
func (s *Store) Update(src *Source) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	srcs, err := s.load()
	if err != nil {
		return err
	}
	for i, e := range srcs {
		if e.ID == src.ID {
			srcs[i] = src
			return s.save(srcs)
		}
	}
	return fmt.Errorf("source %q not found", src.ID)
}

// MergedEntries returns the union of all enabled sources' cached entries,
// split by type (domains, cidrs). Used at config-assembly time.
func (s *Store) MergedEntries() (domains, cidrs []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srcs, err := s.load()
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]struct{}{}
	add := func(slice *[]string, v string) {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			*slice = append(*slice, v)
		}
	}
	for _, src := range srcs {
		if !src.Enabled {
			continue
		}
		for _, d := range src.Domains {
			add(&domains, d)
		}
		for _, c := range src.CIDRs {
			add(&cidrs, c)
		}
	}
	return domains, cidrs, nil
}

func shortID() string {
	b := make([]byte, 8)
	h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	copy(b, h[:8])
	return fmt.Sprintf("%x", b)
}
