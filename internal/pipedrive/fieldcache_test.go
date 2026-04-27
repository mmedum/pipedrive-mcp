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

func TestFieldCache_Count(t *testing.T) {
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		return []Field{
			{Key: "a", Name: "A"},
			{Key: "b", Name: "B"},
			{Key: "c", Name: "C"},
		}, nil
	})

	// Pre-load: 0.
	if got := fc.Count(); got != 0 {
		t.Errorf("Count before load = %d; want 0", got)
	}
	if err := fc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := fc.Count(); got != 3 {
		t.Errorf("Count after load = %d; want 3", got)
	}
	// Post-Reload (before next Load): cache is empty again.
	fc.Reload()
	if got := fc.Count(); got != 0 {
		t.Errorf("Count after Reload (no Load) = %d; want 0", got)
	}
}

func TestFieldCache_CountAfterFetchError(t *testing.T) {
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		return nil, errors.New("upstream down")
	})
	_ = fc.Load(context.Background())
	if got := fc.Count(); got != 0 {
		t.Errorf("Count after failed load = %d; want 0", got)
	}
}

// TestFieldCache_CountDoesNotPoisonOnce regresses the bug where
// Count() armed the once with a no-op closure: a Count call racing
// with a concurrent first Load could seal the once and skip the
// real fetch, leaving byKey nil and silently breaking Resolve.
//
// The fetcher blocks on a channel until released, letting us hold a
// Load in flight while we call Count many times in parallel. After
// release the Load must observe the populated byKey, not the
// poisoned no-op.
func TestFieldCache_CountDoesNotPoisonOnce(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int64
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls.Add(1)
		<-release
		return []Field{{Key: "k1", Name: "N1"}, {Key: "k2", Name: "N2"}}, nil
	})

	// Kick off a Load that will block in the fetcher.
	loadDone := make(chan error, 1)
	go func() { loadDone <- fc.Load(context.Background()) }()

	// Race a flurry of Count() calls against the in-flight Load.
	// Pre-fix these would seal the once and the Load would return
	// nil with byKey still nil. With the fix, Count returns 0
	// (loaded flag is false) and the Load proceeds untouched.
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := fc.Count(); got != 0 {
				t.Errorf("Count during in-flight load = %d; want 0", got)
			}
		}()
	}
	wg.Wait()

	// Release the fetcher; the Load now completes for real.
	close(release)
	if err := <-loadDone; err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("fetcher invocations = %d; want 1", got)
	}
	if got := fc.Count(); got != 2 {
		t.Errorf("Count after Load completed = %d; want 2", got)
	}
	resolved := fc.Resolve(context.Background(), map[string]any{"k1": "v1"})
	if resolved["N1"] != "v1" {
		t.Errorf("Resolve did not surface populated byKey: %#v", resolved)
	}
}

func TestFieldCache_ReloadDuringInFlightLoad(t *testing.T) {
	// Regression: Reload used to overwrite the FieldCache's sync.Once
	// while a Load was mid-flight, corrupting the once's internal mutex
	// state and panicking with "unlock of unlocked mutex" on the
	// in-flight goroutine. The fix swaps the active entry instead so
	// in-flight callers complete safely on their own once.
	started := make(chan struct{})
	gate := make(chan struct{})
	released := make(chan struct{})
	var startedOnce sync.Once
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		// Only the first fetch (the in-flight one) signals + waits
		// for the gate; the post-reload sanity Load below runs the
		// fetch a second time and must not block.
		startedOnce.Do(func() {
			close(started)
			<-gate
		})
		return []Field{{Key: "abc", Name: "AccountManager"}}, nil
	})

	go func() {
		_ = fc.Load(context.Background())
		close(released)
	}()
	// Wait for the load goroutine to enter fetch — no polling, no
	// timing assumptions. The signal makes the test deterministic
	// even on heavily-loaded CI boxes.
	<-started
	// Reload while the in-flight load is parked in fetch.
	fc.Reload()
	// Release the in-flight load — it must finish without panicking.
	close(gate)
	<-released
	// Sanity: a fresh Load after the dance still works (post-Reload
	// entry loads cleanly).
	if err := fc.Load(context.Background()); err != nil {
		t.Errorf("post-reload Load failed: %v", err)
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
