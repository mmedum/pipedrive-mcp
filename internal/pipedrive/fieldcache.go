package pipedrive

import (
	"context"
	"sync"
)

// FieldCache lazy-loads Pipedrive field metadata for one resource type
// and resolves the 40-char custom-field hash keys back to their
// human-readable names. Loaded behind sync.Once: concurrent callers
// see one fetch, all subsequent callers observe the same error if the
// fetch failed. Reload() clears the cache so a refresh tool can
// trigger a refetch when fields have changed upstream.
type FieldCache struct {
	fetch func(context.Context) ([]Field, error)

	mu    sync.RWMutex
	once  sync.Once
	err   error
	byKey map[string]Field
}

// NewFieldCache wraps fetch; fetch is invoked at most once per Reload
// cycle and must return a stable snapshot of the resource's fields.
func NewFieldCache(fetch func(context.Context) ([]Field, error)) *FieldCache {
	return &FieldCache{fetch: fetch}
}

// Load triggers the underlying fetch on first call. Every subsequent
// caller sees the same error so the LLM-facing tool can decide
// whether to fall back to raw output.
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
		for _, f := range fields {
			fc.byKey[f.Key] = f
		}
	})
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.err
}

// Resolve returns a copy of raw with hash keys replaced by names.
// Unknown keys pass through verbatim so the LLM never silently loses
// data when the cache lags behind a freshly-created field; on cache
// load failure, raw is returned untouched.
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

// Reload clears the cache so the next access refetches. Concurrent
// callers in flight at the moment of Reload may still observe stale
// results; the next batch sees fresh data.
func (fc *FieldCache) Reload() {
	fc.mu.Lock()
	fc.once = sync.Once{}
	fc.err = nil
	fc.byKey = nil
	fc.mu.Unlock()
}
