package lists

import (
	"log/slog"
	"runtime/debug"
	"time"
)

// minChangedInterval is the minimum time between OnChanged calls. Prevents
// rapid sing-box restarts when multiple sources update at the same tick.
const minChangedInterval = 5 * time.Minute

// Runner periodically fetches all enabled sources and calls onChanged when
// any source's content changes. Runs forever in a goroutine.
type Runner struct {
	Store     *Store
	Log       *slog.Logger
	OnChanged func() // called after any source changes; may trigger config rebuild

	lastChanged time.Time
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Start runs the fetch loop. Call in a goroutine.
// It runs an initial pass (fetching due sources) after a short delay, then
// ticks every minute to check which sources are due again.
func (r *Runner) Start() {
	// Give the server a moment to finish initialising before hitting the network.
	time.Sleep(5 * time.Second)
	r.fetchDue()

	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		r.fetchDue()
	}
}

// FetchAll forces an immediate fetch of every enabled source regardless of
// interval. Used by the manual-refresh API endpoint.
func (r *Runner) FetchAll() {
	srcs, err := r.Store.List()
	if err != nil {
		r.log().Warn("lists: load for refresh", "err", err)
		return
	}
	changed := false
	for _, src := range srcs {
		if !src.Enabled {
			continue
		}
		if r.fetchOne(src) {
			changed = true
		}
	}
	if changed {
		r.notifyChanged()
	}
}

// FetchOne forces a fetch of a single source by ID.
func (r *Runner) FetchOne(id string) bool {
	srcs, err := r.Store.List()
	if err != nil {
		return false
	}
	for _, src := range srcs {
		if src.ID == id {
			changed := r.fetchOne(src)
			if changed && r.OnChanged != nil {
				r.OnChanged()
			}
			return changed
		}
	}
	return false
}

func (r *Runner) fetchDue() {
	srcs, err := r.Store.List()
	if err != nil {
		r.log().Warn("lists: load", "err", err)
		return
	}
	changed := false
	for _, src := range srcs {
		if !src.Enabled || !src.isDue() {
			continue
		}
		if r.fetchOne(src) {
			changed = true
		}
	}
	// Return memory to the OS after parsing (potentially large) JSON responses.
	// The Go runtime otherwise keeps freed pages in its heap pool, inflating RSS.
	debug.FreeOSMemory()
	if changed {
		r.notifyChanged()
	}
}

// notifyChanged calls OnChanged with debounce: at most once per minChangedInterval.
func (r *Runner) notifyChanged() {
	if r.OnChanged == nil {
		return
	}
	if time.Since(r.lastChanged) < minChangedInterval {
		r.log().Info("lists: change detected but debounced", "next_in", minChangedInterval-time.Since(r.lastChanged))
		return
	}
	r.lastChanged = time.Now()
	r.OnChanged()
}

// fetchOne fetches a single source, updates it in the store, and returns true
// if the content changed (new hash ≠ old hash).
func (r *Runner) fetchOne(src *Source) bool {
	r.log().Info("lists: fetching", "id", src.ID, "url", src.URL)

	domains, cidrs, hash, err := FetchAndParse(src.URL, src.Type)
	// Return memory immediately after parsing — large lists (e.g. YouTube ~23k
	// CIDRs) allocate significant transient memory during JSON parsing.
	defer debug.FreeOSMemory()

	now := time.Now()
	src.LastFetch = &now

	if err != nil {
		src.LastError = err.Error()
		r.log().Warn("lists: fetch error", "id", src.ID, "err", err)
		_ = r.Store.Update(src)
		return false
	}

	src.LastError = ""
	src.LastCount = len(domains) + len(cidrs)

	changed := hash != src.LastHash
	if changed {
		src.LastHash = hash
		src.Domains = domains
		src.CIDRs = cidrs
		r.log().Info("lists: updated", "id", src.ID, "domains", len(domains), "cidrs", len(cidrs))
	}

	_ = r.Store.Update(src)
	return changed
}
