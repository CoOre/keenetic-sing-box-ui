// Package subs manages proxy subscriptions: a URL that returns a list of
// share links (the base64 "v2ray" format served by Marzban, Remnawave, 3x-ui
// and most providers). The runner re-fetches each subscription on schedule and
// syncs its servers into the servers store, where they are tagged with the
// subscription ID and replaced wholesale on every refresh.
package subs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultInterval is the refresh period (minutes) when none is set.
const DefaultInterval = 12 * 60

// Subscription is one subscription URL plus the state of its last fetch.
type Subscription struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Interval  int    `json:"interval"` // minutes between fetches; 0 = DefaultInterval
	Enabled   bool   `json:"enabled"`
	AutoApply bool   `json:"auto_apply"` // rebuild config + restart sing-box when the server set changes
	UserAgent string `json:"user_agent,omitempty"`

	// Updated by the runner.
	LastFetch *time.Time `json:"last_fetch,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	LastCount int        `json:"last_count"`         // servers taken from the last successful fetch
	Skipped   int        `json:"skipped,omitempty"`  // links that couldn't be parsed or were placeholders
	Via       string     `json:"via,omitempty"`      // "direct" | "proxy": how the last fetch got through
	Info      *UserInfo  `json:"info,omitempty"`     // from the Subscription-Userinfo header
}

// UserInfo is the provider's traffic quota, as reported in the
// Subscription-Userinfo response header. Zero means "not reported".
type UserInfo struct {
	Upload   int64 `json:"upload,omitempty"`
	Download int64 `json:"download,omitempty"`
	Total    int64 `json:"total,omitempty"`
	Expire   int64 `json:"expire,omitempty"` // unix seconds
}

func (s *Subscription) interval() time.Duration {
	m := s.Interval
	if m <= 0 {
		m = DefaultInterval
	}
	return time.Duration(m) * time.Minute
}

// isDue reports whether the subscription should be fetched now. A failed
// fetch is retried sooner than the regular interval.
func (s *Subscription) isDue(now time.Time) bool {
	if s.LastFetch == nil {
		return true
	}
	iv := s.interval()
	if s.LastError != "" && iv > 30*time.Minute {
		iv = 30 * time.Minute
	}
	return now.Sub(*s.LastFetch) >= iv
}

// ErrNotFound is returned for an unknown subscription ID.
var ErrNotFound = errors.New("подписка не найдена")

type file struct {
	Subscriptions []*Subscription `json:"subscriptions"`
}

// Store persists subscriptions in a JSON file.
type Store struct {
	Path string
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{Path: path} }

func (s *Store) load() ([]*Subscription, error) {
	body, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []*Subscription{}, nil
		}
		return nil, err
	}
	var f file
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, err
	}
	if f.Subscriptions == nil {
		f.Subscriptions = []*Subscription{}
	}
	return f.Subscriptions, nil
}

func (s *Store) save(list []*Subscription) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(file{Subscriptions: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path + ".new"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func (s *Store) List() ([]*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) Get(id string) (*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	for _, sub := range list {
		if sub.ID == id {
			return sub, nil
		}
	}
	return nil, ErrNotFound
}

// Add stores a new subscription with a fresh ID and returns it.
func (s *Store) Add(sub Subscription) (*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	sub.ID = newID()
	list = append(list, &sub)
	if err := s.save(list); err != nil {
		return nil, err
	}
	return &sub, nil
}

// Update replaces the stored subscription with the same ID.
func (s *Store) Update(sub *Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ID == sub.ID {
			list[i] = sub
			return s.save(list)
		}
	}
	return ErrNotFound
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, sub := range list {
		if sub.ID != id {
			out = append(out, sub)
		}
	}
	if len(out) == len(list) {
		return ErrNotFound
	}
	return s.save(out)
}

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "sub000000000"
	}
	return hex.EncodeToString(b)
}
