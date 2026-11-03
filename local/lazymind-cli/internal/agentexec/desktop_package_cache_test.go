package agentexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPackageCacheTTLAndFailureBackoff(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		err        error
		ttl        time.Duration
	}{
		{"found", `[{"Name":"OpenAI.Codex","InstallLocation":"fixture","Executables":["Codex.exe"]}]`, nil, time.Minute},
		{"empty", `[]`, nil, 10 * time.Second},
		{"null", `null`, nil, 10 * time.Second},
		{"failure", ``, errors.New("query failed"), 10 * time.Second},
		{"timeout", ``, context.DeadlineExceeded, 10 * time.Second},
		{"invalid-json", `{`, nil, 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			cache := desktopPackageCache{t.TempDir(), func() time.Time { return now }}
			calls := 0
			query := func(context.Context, []string) ([]byte, error) { calls++; return []byte(tc.body), tc.err }
			first, err := cache.query(context.Background(), []string{"OpenAI.Codex"}, query)
			wantError := tc.err != nil || tc.name == "invalid-json"
			if (err != nil) != wantError {
				t.Fatalf("first query error: %v", err)
			}
			if len(first) > 0 {
				first[0] = 'x'
			}
			now = now.Add(tc.ttl - time.Nanosecond)
			second, err := cache.query(context.Background(), []string{" OPENAI.CODEX ", "openai.codex"}, query)
			if (err != nil) != wantError || calls != 1 {
				t.Fatalf("cache miss: calls=%d err=%v", calls, err)
			}
			if len(second) > 0 && second[0] == 'x' {
				t.Fatal("caller mutated cached result")
			}
			now = now.Add(time.Nanosecond)
			_, _ = cache.query(context.Background(), []string{"OpenAI.Codex"}, query)
			if calls != 2 {
				t.Fatalf("expiry did not retry: %d", calls)
			}
		})
	}
}

func TestPackageCacheKeysAndInvalidation(t *testing.T) {
	cache := desktopPackageCache{t.TempDir(), time.Now}
	calls := 0
	query := func(_ context.Context, names []string) ([]byte, error) {
		calls++
		if !reflect.DeepEqual(names, normalizedPackageNames(names)) {
			t.Fatal("unnormalized query")
		}
		return []byte(`[]`), nil
	}
	for _, names := range [][]string{{" B ", "A", "a"}, {"a", "b"}, {"c"}} {
		if _, err := cache.query(context.Background(), names, query); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("package sets collided or did not normalize: %d", calls)
	}
	if err := cache.invalidate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.query(context.Background(), []string{"a", "b"}, query); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("explicit refresh did not invalidate")
	}
	if _, err := cache.query(context.Background(), []string{" "}, query); err != nil || calls != 3 {
		t.Fatal("empty names launched query")
	}
}

func TestPackageCacheCoalescesIndependentClients(t *testing.T) {
	directory := t.TempDir()
	var calls atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			cache := desktopPackageCache{directory, time.Now}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, err := cache.query(ctx, []string{"OpenAI.Codex"}, func(context.Context, []string) ([]byte, error) { calls.Add(1); return []byte(`[]`), nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("spawned %d queries", calls.Load())
	}
}

func TestPackageCacheCancellationAndRefreshDuringQuery(t *testing.T) {
	cache := desktopPackageCache{t.TempDir(), time.Now}
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := cache.query(context.Background(), []string{"a"}, func(context.Context, []string) ([]byte, error) { close(started); <-release; return []byte(`[]`), nil })
		done <- err
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := cache.query(ctx, []string{"a"}, func(context.Context, []string) ([]byte, error) {
		t.Error("waiting client launched query")
		return nil, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("lock wait: %v", err)
	}
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- cache.invalidate(context.Background()) }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-refreshDone; err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err = cache.query(context.Background(), []string{"a"}, func(context.Context, []string) ([]byte, error) { calls++; return []byte(`[]`), nil })
	if err != nil || calls != 1 {
		t.Fatalf("in-flight query repopulated invalidated cache: %d %v", calls, err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, err := cache.query(ctx2, []string{"a"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cache read: %v", err)
	}
}

func TestPackageCacheCorruptionAndUnavailableStorage(t *testing.T) {
	directory := t.TempDir()
	cache := desktopPackageCache{directory, time.Now}
	path := filepath.Join(directory, packageCacheKey([]string{"a"})+".json")
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	query := func(context.Context, []string) ([]byte, error) { return []byte(`[]`), nil }
	if _, err := cache.query(context.Background(), []string{"a"}, query); err != nil {
		t.Fatal(err)
	}
	cache.directory = path // a file cannot be a cache directory
	if _, err := cache.query(context.Background(), []string{"a"}, func(context.Context, []string) ([]byte, error) {
		t.Error("launched despite unavailable cache")
		return nil, nil
	}); err == nil {
		t.Fatal("expected storage error")
	}
}

func TestPackageCacheSharesDeadlineAndRecoversAfterTimeout(t *testing.T) {
	now := time.Now()
	cache := desktopPackageCache{t.TempDir(), func() time.Time { return now }}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := cache.query(ctx, []string{"a"}, func(ctx context.Context, _ []string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query deadline: %v", err)
	}
	_, err = cache.query(context.Background(), []string{"a"}, func(context.Context, []string) ([]byte, error) {
		t.Error("retried during timeout backoff")
		return nil, nil
	})
	if !errors.Is(err, errPackageQueryBackoff) {
		t.Fatalf("backoff: %v", err)
	}
	now = now.Add(10 * time.Second)
	if _, err := cache.query(context.Background(), []string{"a"}, func(context.Context, []string) ([]byte, error) { return []byte(`[]`), nil }); err != nil {
		t.Fatalf("failed to recover: %v", err)
	}
}
