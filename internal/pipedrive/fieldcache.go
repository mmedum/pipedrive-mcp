package pipedrive

import (
	"context"
	"sync"
)

// FieldCache caches field metadata for a single resource type
// (deals, persons, organizations, products) and exposes name/key
// resolution. Lazy-loaded: the first call that needs the cache
// triggers a single fetch via the cached `fetch` callback; concurrent
// callers wait on a sync.Once so only one HTTP call ever happens
// per cache instance.
//
// Reload() clears the loaded state so the next access refetches —
// used by the future `refresh_field_cache` tool (Phase 1.9).
type FieldCache struct {
	fetch func(context.Context) ([]Field, error)

	mu     sync.RWMutex
	once   sync.Once
	loaded bool
	err    error
	byKey  map[string]Field
	byName map[string]Field
}

// NewFieldCache returns a fresh, unloaded cache wrapping fetch. fetch
// is invoked at most once per Reload cycle and must return a stable
// snapshot of the resource's fields.
func NewFieldCache(fetch func(context.Context) ([]Field, error)) *FieldCache {
	return &FieldCache{fetch: fetch}
}

// Load forces a load if not already loaded. Subsequent calls are
// cheap (no-op once `once` has fired). Returns the load error from
// the fetch — every subsequent caller sees the same error so the
// LLM-facing tool can decide whether to fall back to raw output.
func (fc *FieldCache) Load(ctx context.Context) error {
	fc.once.Do(func() {
		fields, err := fc.fetch(ctx)
		fc.mu.Lock()
		defer fc.mu.Unlock()
		if err != nil {
			fc.err = err
			return
		}
		fc.byKey = make(map[string]Field, len(fields))
		fc.byName = make(map[string]Field, len(fields))
		for _, f := range fields {
			fc.byKey[f.Key] = f
			fc.byName[f.Name] = f
		}
		fc.loaded = true
	})
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.err
}

// NameOf returns (name, true) when key is a known field key, ("", false)
// otherwise. Triggers a Load on first use.
func (fc *FieldCache) NameOf(ctx context.Context, key string) (string, bool) {
	if err := fc.Load(ctx); err != nil {
		return "", false
	}
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	f, ok := fc.byKey[key]
	if !ok {
		return "", false
	}
	return f.Name, true
}

// Resolve returns a copy of raw with custom-field hash keys replaced
// by their human-readable names. Unknown keys are passed through
// verbatim so the LLM never silently loses data when the cache lags
// behind a freshly-created field. Triggers a Load on first use.
//
// Returns the raw map untouched when the cache fails to load — the
// LLM still gets the data, just with hash keys.
func (fc *FieldCache) Resolve(ctx context.Context, raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return raw
	}
	if err := fc.Load(ctx); err != nil {
		return raw
	}
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if f, ok := fc.byKey[k]; ok {
			out[f.Name] = v
		} else {
			out[k] = v
		}
	}
	return out
}

// Reload resets the cache so the next access refetches. Concurrent
// callers in flight at the moment of Reload may still observe stale
// results; the next batch sees fresh data.
func (fc *FieldCache) Reload() {
	fc.mu.Lock()
	fc.once = sync.Once{}
	fc.loaded = false
	fc.err = nil
	fc.byKey = nil
	fc.byName = nil
	fc.mu.Unlock()
}
