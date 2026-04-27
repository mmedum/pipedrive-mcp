package pipedrive

import (
	"context"
	"sync"
	"sync/atomic"
)

// FieldCache lazy-loads Pipedrive field metadata for one resource type
// and resolves the 40-char custom-field hash keys back to their
// human-readable names. Loaded behind a per-entry sync.Once: concurrent
// first-callers see one fetch, all subsequent callers observe the same
// error if the fetch failed. Reload() swaps the entry so the next
// access refetches, while in-flight callers safely complete on their
// own entry — never on a Reload-corrupted Once.
type FieldCache struct {
	fetch func(context.Context) ([]Field, error)

	mu  sync.Mutex
	cur *cacheEntry
}

// cacheEntry holds one generation of cached field metadata. Each
// entry has its own sync.Once so Reload can replace the active entry
// without corrupting the mutex state of an in-flight Load. byKey and
// err are written exactly once from inside once.Do; later readers
// observe stable values via the once's happens-before guarantee.
//
// loaded is set to true inside once.Do *after* byKey/err are written.
// It lets Count() report the cache size without itself triggering
// once.Do — an earlier no-op once.Do here would silently seal the
// once and skip the real fetch on a concurrent first Load.
type cacheEntry struct {
	once   sync.Once
	err    error
	byKey  map[string]Field
	loaded atomic.Bool
}

// NewFieldCache wraps fetch; fetch is invoked at most once per Reload
// cycle and must return a stable snapshot of the resource's fields.
func NewFieldCache(fetch func(context.Context) ([]Field, error)) *FieldCache {
	return &FieldCache{fetch: fetch}
}

// currentEntry returns the active entry, creating one if needed.
// Holding fc.mu only across the cur read/write keeps the fetch (which
// may block on HTTP) outside the lock so a concurrent Reload can swap
// fc.cur without waiting for the in-flight fetch.
func (fc *FieldCache) currentEntry() *cacheEntry {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.cur == nil {
		fc.cur = &cacheEntry{}
	}
	return fc.cur
}

func (e *cacheEntry) load(ctx context.Context, fetch func(context.Context) ([]Field, error)) {
	e.once.Do(func() {
		defer e.loaded.Store(true)
		fields, err := fetch(ctx)
		if err != nil {
			e.err = err
			return
		}
		e.byKey = make(map[string]Field, len(fields))
		for _, f := range fields {
			e.byKey[f.Key] = f
		}
	})
}

// Load triggers the underlying fetch on first call. Every subsequent
// caller sees the same error so the LLM-facing tool can decide
// whether to fall back to raw output.
func (fc *FieldCache) Load(ctx context.Context) error {
	e := fc.currentEntry()
	e.load(ctx, fc.fetch)
	return e.err
}

// Resolve returns a copy of raw with hash keys replaced by names.
// Unknown keys pass through verbatim so the LLM never silently loses
// data when the cache lags behind a freshly-created field; on cache
// load failure, raw is returned untouched.
//
// Reads work off the entry snapshot rather than fc.cur so a Reload
// mid-call doesn't risk a torn read. Each entry's byKey is set under
// once.Do, which provides the happens-before for safe concurrent reads.
func (fc *FieldCache) Resolve(ctx context.Context, raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return raw
	}
	e := fc.currentEntry()
	e.load(ctx, fc.fetch)
	if e.err != nil {
		return raw
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if f, ok := e.byKey[k]; ok {
			out[f.Name] = v
		} else {
			out[k] = v
		}
	}
	return out
}

// Reload clears the cache so the next access refetches. Concurrent
// callers in flight at the moment of Reload finish their load on the
// outgoing entry (its once.Do is unaffected by this swap), and their
// already-acquired entry pointer continues to surface the old data.
// The next batch of callers sees a fresh entry and a fresh fetch.
func (fc *FieldCache) Reload() {
	fc.mu.Lock()
	fc.cur = nil
	fc.mu.Unlock()
}

// Count returns the number of fields currently cached. Returns 0 if
// the cache hasn't been loaded or the load failed. Used by the
// refresh_field_cache tool to surface a per-resource sanity check
// the LLM (and operator) can read after triggering a reload.
//
// Count never triggers once.Do — calling once.Do(no-op) here would
// race with a concurrent first Load and silently seal the once,
// causing the real fetch to be skipped. Instead it reads the loaded
// flag (set inside once.Do *after* byKey is written), which provides
// the happens-before edge to byKey for free.
func (fc *FieldCache) Count() int {
	fc.mu.Lock()
	e := fc.cur
	fc.mu.Unlock()
	if e == nil || !e.loaded.Load() {
		return 0
	}
	return len(e.byKey)
}
