package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

const (
	SessionCookieName = "ksbui_session"
	CSRFCookieName    = "ksbui_csrf"
	CSRFHeader        = "X-CSRF-Token"
)

type Session struct {
	ID        string
	CSRFToken string
	Created   time.Time
	LastSeen  time.Time
	Expires   time.Time
}

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
	now      func() time.Time
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return &SessionStore{
		sessions: map[string]*Session{},
		ttl:      ttl,
		now:      time.Now,
	}
}

func (s *SessionStore) Create() (*Session, error) {
	id, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	now := s.now()
	sess := &Session{
		ID:        id,
		CSRFToken: csrf,
		Created:   now,
		LastSeen:  now,
		Expires:   now.Add(s.ttl),
	}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return sess, nil
}

// Get returns the session if present and not expired. Touches LastSeen.
func (s *SessionStore) Get(id string) (*Session, bool) {
	if id == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, false
	}
	if s.now().After(sess.Expires) {
		delete(s.sessions, id)
		return nil, false
	}
	sess.LastSeen = s.now()
	return sess, true
}

func (s *SessionStore) Delete(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// SweepExpired removes any sessions past their Expires. Returns count removed.
func (s *SessionStore) SweepExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	now := s.now()
	for id, sess := range s.sessions {
		if now.After(sess.Expires) {
			delete(s.sessions, id)
			n++
		}
	}
	return n
}

// ConstantTimeCompare reports whether two tokens are equal in constant time.
func ConstantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ErrInvalidCredentials is returned by Login when the token does not match.
var ErrInvalidCredentials = errors.New("invalid credentials")
