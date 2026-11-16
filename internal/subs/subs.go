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
	"regexp"
	"sync"
	"time"
)

// DefaultInterval is the refresh period (minutes) when none is set.
const DefaultInterval = 12 * 60

// Subscription is one subscription URL plus the state of its last fetch.
type Subscription struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Interval    int    `json:"interval"` // minutes between fetches; 0 = DefaultInterval
	Enabled     bool   `json:"enabled"`
	AutoApply   bool   `json:"auto_apply"` // rebuild config + restart sing-box when the server set changes
	UserAgent   string `json:"user_agent,omitempty"`
	HWIDEnabled bool   `json:"hwid_enabled,omitempty"`
	HWID        string `json:"hwid,omitempty"` // optional override; empty uses the installation's HWID

	// Updated by the runner.
	LastFetch *time.Time `json:"last_fetch,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	LastCount int        `json:"last_count"`        // servers taken from the last successful fetch
	Skipped   int        `json:"skipped,omitempty"` // links that couldn't be parsed or were placeholders
	Via       string     `json:"via,omitempty"`     // "direct" | "proxy": how the last fetch got through
	Info      *UserInfo  `json:"info,omitempty"`    // from the Subscription-Userinfo header
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
	DeviceHWID    string          `json:"device_hwid,omitempty"`
	Subscriptions []*Subscription `json:"subscriptions"`
}

// Store persists subscriptions in a JSON file.
type Store struct {
	Path string
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{Path: path} }

func (s *Store) load() (*file, error) {
	body, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &file{Subscriptions: []*Subscription{}}, nil
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
	return &f, nil
}

func (s *Store) save(f *file) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(f, "", "  ")
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
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	return f.Subscriptions, nil
}

func (s *Store) Get(id string) (*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	for _, sub := range f.Subscriptions {
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
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	sub.ID = newID()
	f.Subscriptions = append(f.Subscriptions, &sub)
	if err := s.save(f); err != nil {
		return nil, err
	}
	return &sub, nil
}

// Update replaces the stored subscription with the same ID.
func (s *Store) Update(sub *Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	for i := range f.Subscriptions {
		if f.Subscriptions[i].ID == sub.ID {
			f.Subscriptions[i] = sub
			return s.save(f)
		}
	}
	return ErrNotFound
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	out := f.Subscriptions[:0]
	for _, sub := range f.Subscriptions {
		if sub.ID != id {
			out = append(out, sub)
		}
	}
	if len(out) == len(f.Subscriptions) {
		return ErrNotFound
	}
	f.Subscriptions = out
	return s.save(f)
}

var hwidRe = regexp.MustCompile(`^[a-zA-Z0-9=_-]{10,64}$`)

// ValidateHWID accepts an optional override, including underscores used by
// providers with a broader HWID format than stock Remnawave 3.x. The provider
// decides whether to accept the ID; it must be sent without rewriting it.
func ValidateHWID(hwid string) error {
	if hwid != "" && !hwidRe.MatchString(hwid) {
		return errors.New("HWID должен содержать от 10 до 64 символов: латинские буквы, цифры, _, = или -")
	}
	return nil
}

// DeviceHWID returns the installation's identity, persisting it before it can
// be sent to a provider. Loading it on each call also honours restored backups.
func (s *Store) DeviceHWID() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return "", err
	}
	if f.DeviceHWID != "" {
		return f.DeviceHWID, ValidateHWID(f.DeviceHWID)
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	f.DeviceHWID = hex.EncodeToString(b[:])
	if err := s.save(f); err != nil {
		return "", err
	}
	return f.DeviceHWID, nil
}

func newID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "sub000000000"
	}
	return hex.EncodeToString(b)
}
