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
		{Key: "abc123", Name: "Account Manager", FieldType: "user", EditFlag: true},
		{Key: "title", Name: "Title", FieldType: "varchar", EditFlag: false},
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
			_, _ = fc.NameOf(context.Background(), "abc123")
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("fetch invoked %d times; want 1", got)
	}

	// NameOf returns expected mapping, false for unknown keys.
	if name, ok := fc.NameOf(context.Background(), "abc123"); !ok || name != "Account Manager" {
		t.Errorf("NameOf(abc123) = (%q,%v); want (Account Manager,true)", name, ok)
	}
	if _, ok := fc.NameOf(context.Background(), "no-such-key"); ok {
		t.Error("NameOf for unknown key returned ok=true")
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
	// NameOf must return ok=false on a failed cache.
	if _, ok := fc.NameOf(context.Background(), "abc123"); ok {
		t.Error("NameOf returned ok=true after failed load")
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

	if name, _ := fc.NameOf(context.Background(), "v1"); name != "First" {
		t.Fatalf("first load: name = %q", name)
	}

	// Mutate the source then reload.
	fields = []Field{{Key: "v2", Name: "Second"}}
	fc.Reload()

	if name, _ := fc.NameOf(context.Background(), "v2"); name != "Second" {
		t.Errorf("post-reload: NameOf(v2) = %q; want Second", name)
	}
	if _, ok := fc.NameOf(context.Background(), "v1"); ok {
		t.Error("post-reload: stale v1 still present")
	}
	if calls != 2 {
		t.Errorf("fetch invocation count = %d; want 2 (one before Reload, one after)", calls)
	}
}
