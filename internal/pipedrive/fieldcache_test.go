package pipedrive

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFieldCache_LazyAndOnce(t *testing.T) {
	var calls atomic.Int64
	fields := []Field{
		{Key: "abc123", Name: "Account Manager"},
		{Key: "title", Name: "Title"},
	}
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls.Add(1)
		return fields, nil
	})

	// Concurrent first use must trigger only one fetch.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = fc.Resolve(context.Background(), map[string]any{"abc123": "Alice"})
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("fetch invoked %d times; want 1", got)
	}
}

func TestFieldCache_FetchErrorDoesNotPanic(t *testing.T) {
	wantErr := errors.New("upstream down")
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		return nil, wantErr
	})
	if err := fc.Load(context.Background()); !errors.Is(err, wantErr) {
		t.Errorf("Load err = %v; want %v", err, wantErr)
	}
	// Resolve must pass through raw map untouched on a failed cache.
	raw := map[string]any{"abc123": 42}
	got := fc.Resolve(context.Background(), raw)
	if v, ok := got["abc123"]; !ok || v != 42 {
		t.Errorf("Resolve fallthrough lost data: got %v", got)
	}
}

func TestFieldCache_Resolve(t *testing.T) {
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		return []Field{
			{Key: "abc", Name: "Account Manager"},
			{Key: "def", Name: "Renewal Date"},
		}, nil
	})

	in := map[string]any{
		"abc":     "Alice",
		"def":     "2026-12-01",
		"unknown": "passthrough",
	}
	got := fc.Resolve(context.Background(), in)

	if got["Account Manager"] != "Alice" {
		t.Errorf("missing Account Manager: %v", got)
	}
	if got["Renewal Date"] != "2026-12-01" {
		t.Errorf("missing Renewal Date: %v", got)
	}
	if got["unknown"] != "passthrough" {
		t.Errorf("unknown-key passthrough lost: %v", got)
	}
	// Original input must not be mutated.
	if _, ok := in["Account Manager"]; ok {
		t.Error("Resolve mutated the input map")
	}
}

func TestFieldCache_ResolveEmpty(t *testing.T) {
	// Empty input doesn't trigger a fetch.
	var calls atomic.Int64
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls.Add(1)
		return nil, nil
	})
	got := fc.Resolve(context.Background(), nil)
	if got != nil {
		t.Errorf("Resolve(nil) = %v; want nil", got)
	}
	got = fc.Resolve(context.Background(), map[string]any{})
	if len(got) != 0 {
		t.Errorf("Resolve(empty) = %v; want empty", got)
	}
	if calls.Load() != 0 {
		t.Errorf("empty Resolve triggered %d fetches; want 0", calls.Load())
	}
}

func TestFieldCache_Reload(t *testing.T) {
	calls := 0
	fields := []Field{{Key: "v1", Name: "First"}}
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls++
		return fields, nil
	})

	if got := fc.Resolve(context.Background(), map[string]any{"v1": "x"}); got["First"] != "x" {
		t.Fatalf("first load: name resolution lost: %v", got)
	}

	// Mutate the source then reload.
	fields = []Field{{Key: "v2", Name: "Second"}}
	fc.Reload()

	if got := fc.Resolve(context.Background(), map[string]any{"v2": "y"}); got["Second"] != "y" {
		t.Errorf("post-reload: name resolution lost: %v", got)
	}
	if got := fc.Resolve(context.Background(), map[string]any{"v1": "stale"}); got["First"] == "stale" {
		t.Error("post-reload: stale v1 still resolves")
	}
	if calls != 2 {
		t.Errorf("fetch invocation count = %d; want 2 (one before Reload, one after)", calls)
	}
}
