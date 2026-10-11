package servers

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// MaxChainHops caps a chain's length: every hop adds a round trip and a
// tunnel-in-tunnel, so longer chains are almost certainly a mistake.
const MaxChainHops = 5

// Chain is a user-defined server chain: traffic enters through Hops[0], and
// each next hop is dialed through the previous one (sing-box "detour"), so
// the last hop is the exit. Hops hold server entry IDs, not tags: tags follow
// server names and change on rename. A chain can be the primary pick (see
// State.Primary).
type Chain struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Hops []string `json:"hops"`
}

// ErrInvalidChain wraps chain validation failures (SaveChain).
var ErrInvalidChain = errors.New("неверная цепочка")

// ErrChainNotFound is returned by SaveChain for an ID that isn't stored.
var ErrChainNotFound = errors.New("цепочка не найдена — возможно, её удалили")

// SaveChain adds a new chain (empty ID) or replaces one by ID, and returns the
// stored chain; an ID that isn't stored is ErrChainNotFound (deleted meanwhile —
// don't resurrect it). Hops are validated against the server list under the
// store lock, so a concurrent subscription refresh can't slip a dangling ID
// in. A name that gives another chain's tag is refused (see Chain.Tag).
func (s *Store) SaveChain(c Chain) (Chain, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Chain{}, err
	}
	if err := validateChain(&c, st.Servers); err != nil {
		return Chain{}, err
	}
	isNew := c.ID == ""
	if isNew {
		c.ID = newID()
	}
	for _, other := range st.Chains {
		if other.ID != c.ID && other.Tag() == c.Tag() {
			return Chain{}, fmt.Errorf("%w: цепочка с таким названием уже есть", ErrInvalidChain)
		}
	}
	i := slices.IndexFunc(st.Chains, func(x Chain) bool { return x.ID == c.ID })
	switch {
	case isNew:
		st.Chains = append(st.Chains, c)
	case i < 0:
		return Chain{}, ErrChainNotFound
	default:
		st.Chains[i] = c
	}
	if err := s.save(st); err != nil {
		return Chain{}, err
	}
	return c, nil
}

// DeleteChain removes the chain with the given ID (no-op if absent), and
// returns warnings for what that breaks (see changeWarnings). With dryRun it
// only returns the warnings: the UI shows them before asking to confirm.
func (s *Store) DeleteChain(id string, dryRun bool) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return nil, err
	}
	next := st
	next.Chains = slices.DeleteFunc(slices.Clone(st.Chains), func(c Chain) bool { return c.ID == id })
	if !dryRun {
		if err := s.save(next); err != nil {
			return nil, err
		}
	}
	return changeWarnings(st, next), nil
}

func validateChain(c *Chain, list []Entry) error {
	c.Name = strings.TrimSpace(c.Name)
	if len(c.Hops) < 2 {
		return fmt.Errorf("%w: нужно минимум 2 сервера", ErrInvalidChain)
	}
	if len(c.Hops) > MaxChainHops {
		return fmt.Errorf("%w: не больше %d серверов", ErrInvalidChain, MaxChainHops)
	}
	known := make(map[string]bool, len(list))
	for _, e := range list {
		known[e.ID] = true
	}
	seen := make(map[string]bool, len(c.Hops))
	for _, id := range c.Hops {
		if !known[id] {
			return fmt.Errorf("%w: одного из серверов больше нет (удалён или обновилась подписка) — откройте редактор заново", ErrInvalidChain)
		}
		if seen[id] {
			return fmt.Errorf("%w: сервер повторяется в цепочке", ErrInvalidChain)
		}
		seen[id] = true
	}
	return nil
}

const chainTagPrefix = "chain-"

// Tag returns the chain's outbound tag before de-duplication. The "chain-"
// prefix is the chains' own (Entry.Tag keeps servers out of it): servers are
// tagged first, so a server named like a chain would otherwise push the chain
// to a "-2" suffix behind the user's back. Two chains can't share a tag
// either (SaveChain), so deleting one never hands its tag to the other.
func (c Chain) Tag() string {
	return chainTagPrefix + sanitizeTag(c.Name, shortID(c.ID))
}
