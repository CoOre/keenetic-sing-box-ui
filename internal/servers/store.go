// Package servers persists the list of proxy servers the user has added via
// the UI form, and turns them into sing-box outbound objects for config
// assembly. The servers list is the source of truth; the sing-box config is
// generated from it.
package servers

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/CoOre/keenetic-sing-box-ui/internal/share"
)

// Entry is a stored server: a share.Server plus a stable ID. SubID links the
// entry to the subscription that manages it (see SyncSub); empty = added by
// hand.
type Entry struct {
	ID    string `json:"id"`
	SubID string `json:"sub_id,omitempty"`
	share.Server
}

// State is everything servers.json holds. It is one file, written atomically,
// so servers, the chains through them and the primary pick never disagree.
type State struct {
	Servers []Entry `json:"servers"`
	Chains  []Chain `json:"chains"`
	// Primary is the ID of the server or chain the "proxy" selector defaults
	// to; "" means auto (fastest by urltest) or the sole server.
	Primary string `json:"primary,omitempty"`
}

// IsPrimary reports whether id is the primary pick.
func (st State) IsPrimary(id string) bool { return id != "" && id == st.Primary }

type Store struct {
	Path string // servers.json
	mu   sync.Mutex
}

func NewStore(path string) *Store { return &Store{Path: path} }

// ParseState decodes servers.json. Before chains it was a plain list of
// entries, the primary one flagged with "primary": true; that form is still
// read, and the next save rewrites it.
func ParseState(body []byte) (State, error) {
	var st State
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '[' {
		var legacy []struct {
			Entry
			Primary bool `json:"primary"`
		}
		if err := json.Unmarshal(trimmed, &legacy); err != nil {
			return State{}, err
		}
		for _, e := range legacy {
			st.Servers = append(st.Servers, e.Entry)
			if e.Primary {
				st.Primary = e.ID
			}
		}
	} else if err := json.Unmarshal(body, &st); err != nil {
		return State{}, err
	}
	if st.Servers == nil {
		st.Servers = []Entry{}
	}
	if st.Chains == nil {
		st.Chains = []Chain{}
	}
	for i := range st.Chains {
		if st.Chains[i].Hops == nil {
			st.Chains[i].Hops = []string{} // hand-edited file: never "hops": null to the UI
		}
	}
	return st, nil
}

func (s *Store) load() (State, error) {
	body, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ParseState([]byte("{}"))
		}
		return State{}, err
	}
	return ParseState(body)
}

// save drops a primary pick that no longer exists (its server or chain was
// deleted): it would otherwise sit there, pointing at nothing.
func (s *Store) save(st State) error {
	if st.Primary != "" && !slices.ContainsFunc(st.Servers, func(e Entry) bool { return e.ID == st.Primary }) &&
		!slices.ContainsFunc(st.Chains, func(c Chain) bool { return c.ID == st.Primary }) {
		st.Primary = ""
	}
	return writeJSONAtomic(s.Path, st)
}

// writeJSONAtomic writes v as indented JSON via tmp+rename, so a crash never
// leaves a half-written file behind.
func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) List() ([]Entry, error) {
	st, err := s.Snapshot()
	return st.Servers, err
}

// Snapshot returns the whole stored state: servers, chains and the primary
// pick, consistent with each other.
func (s *Store) Snapshot() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Save adds a new entry (when ID is empty) or replaces an existing one by ID.
// Returns the stored entry (with its ID). SubID is owned by SyncSub: it is
// kept from the stored entry, never taken from e.
func (s *Store) Save(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Entry{}, err
	}
	e.SubID = ""
	if e.ID == "" {
		e.ID = newID()
		st.Servers = append(st.Servers, e)
	} else {
		replaced := false
		for i := range st.Servers {
			if st.Servers[i].ID == e.ID {
				e.SubID = st.Servers[i].SubID
				st.Servers[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			st.Servers = append(st.Servers, e)
		}
	}
	if err := s.save(st); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Delete removes the entry with the given ID (no-op if absent), and returns
// warnings for what that breaks (see changeWarnings). With dryRun it only
// returns the warnings: the UI shows them before asking to confirm.
func (s *Store) Delete(id string, dryRun bool) ([]string, error) {
	return s.deleteWhere(func(e Entry) bool { return e.ID == id }, dryRun)
}

func (s *Store) deleteWhere(drop func(Entry) bool, dryRun bool) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	next := st
	next.Servers = slices.DeleteFunc(slices.Clone(st.Servers), drop)
	if !dryRun {
		if err := s.save(next); err != nil {
			return nil, err
		}
	}
	return changeWarnings(st, next), nil
}

// SyncSub replaces the entries owned by subscription subID with fresh, in
// order. A fresh server that matches an old entry keeps its ID, so a refresh
// doesn't lose the primary pick or break the chains through it. Matching
// goes from strict to loose, each pass over what's left: address,
// credentials and name; address and name (rotated credentials); address and
// credentials (a new name: providers put live stats in it). Duplicates pair
// in order. A server that changed more than that gets a new ID, and what
// this breaks is reported. The block stays where the subscription's first
// entry was; a new subscription is appended. Reports whether anything
// changed, and warnings for what the change broke (see changeWarnings).
func (s *Store) SyncSub(subID string, fresh []share.Server) (bool, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return false, nil, err
	}
	pos := -1
	var old []Entry
	rest := make([]Entry, 0, len(st.Servers))
	for _, e := range st.Servers {
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

	next := make([]Entry, len(fresh))
	taken := make([]bool, len(old))
	for _, key := range []func(share.Server) string{
		func(v share.Server) string { return addrKey(v) + "|" + credKey(v) + "|" + nameKey(v) },
		func(v share.Server) string { return addrKey(v) + "|" + nameKey(v) },
		func(v share.Server) string { return addrKey(v) + "|" + credKey(v) },
	} {
		oldBy := map[string][]int{}
		for i, e := range old {
			if !taken[i] {
				oldBy[key(e.Server)] = append(oldBy[key(e.Server)], i)
			}
		}
		for j, srv := range fresh {
			if is := oldBy[key(srv)]; next[j].ID == "" && len(is) > 0 {
				next[j] = Entry{ID: old[is[0]].ID, SubID: subID, Server: srv}
				taken[is[0]] = true
				oldBy[key(srv)] = is[1:]
			}
		}
	}
	for j, srv := range fresh {
		if next[j].ID == "" {
			next[j] = Entry{ID: newID(), SubID: subID, Server: srv}
		}
	}
	if entriesEqual(old, next) {
		return false, nil, nil
	}
	after := st
	after.Servers = make([]Entry, 0, len(rest)+len(next))
	after.Servers = append(after.Servers, rest[:pos]...)
	after.Servers = append(after.Servers, next...)
	after.Servers = append(after.Servers, rest[pos:]...)
	if err := s.save(after); err != nil {
		return false, nil, err
	}
	return true, changeWarnings(st, after), nil
}

// DeleteSub removes every entry owned by subscription subID, and returns
// warnings for what that breaks (see changeWarnings). With dryRun it only
// returns the warnings.
func (s *Store) DeleteSub(subID string, dryRun bool) ([]string, error) {
	return s.deleteWhere(func(e Entry) bool { return e.SubID == subID }, dryRun)
}

func addrKey(v share.Server) string {
	return strings.Join([]string{v.Type, strings.ToLower(v.Server), itoa(v.ServerPort)}, "|")
}

func credKey(v share.Server) string { return v.UUID + "|" + v.Password }

func nameKey(v share.Server) string { return strings.TrimSpace(v.Name) }

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

// ErrChainBroken is returned by SetPrimary for a chain that can't be built
// (a hop server is gone): it isn't in the config, so it can't be the pick.
// The returned error wraps it with the reason (Tags.BrokenReason).
var ErrChainBroken = errors.New("цепочка сломана")

// SetPrimary makes the server or chain with the given ID the primary pick; an
// empty id clears it (auto mode). Returns the tag layout of the saved state.
func (s *Store) SetPrimary(id string) (Tags, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Tags{}, err
	}
	tags := Layout(st)
	if _, isServer := tags.Index[id]; id != "" && !isServer {
		i := slices.IndexFunc(st.Chains, func(c Chain) bool { return c.ID == id })
		switch {
		case i < 0:
			return Tags{}, ErrNotFound
		case tags.Broken(i):
			return Tags{}, fmt.Errorf("%w: %s", ErrChainBroken, tags.BrokenReason(i))
		}
	}
	st.Primary = id
	if err := s.save(st); err != nil {
		return Tags{}, err
	}
	return Layout(st), nil
}

var tagSanitize = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

// Tag returns a sing-box outbound tag for an entry: a sanitized name, or a
// fallback based on the ID. Uniqueness across the list is the caller's job
// (see Layout). "chain-" belongs to chains (see Chain.Tag): a server named
// "chain-foo" is tagged "server-chain-foo", so it can't take a chain's tag.
// So is a server named "chain": its duplicates would be suffixed into
// "chain-2", the tag of a chain named "2".
func (e Entry) Tag() string {
	t := sanitizeTag(e.Name, "server-"+shortID(e.ID))
	if t == strings.TrimSuffix(chainTagPrefix, "-") || strings.HasPrefix(t, chainTagPrefix) {
		t = "server-" + t
	}
	return t
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
