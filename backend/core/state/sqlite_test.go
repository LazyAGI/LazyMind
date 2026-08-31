package state

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSQLiteStoreLPop(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()

	received, err := store.LPop(ctx, "stop")
	if err != nil || received {
		t.Fatalf("empty lpop received=%v err=%v, want false/nil", received, err)
	}
	if err := store.LPush(ctx, "stop", []byte("1"), 0); err != nil {
		t.Fatalf("lpush: %v", err)
	}
	received, err = store.LPop(ctx, "stop")
	if err != nil || !received {
		t.Fatalf("signal lpop received=%v err=%v, want true/nil", received, err)
	}
	received, err = store.LPop(ctx, "stop")
	if err != nil || received {
		t.Fatalf("consumed lpop received=%v err=%v, want false/nil", received, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	received, err = store.LPop(cancelled, "stop")
	if received || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lpop received=%v err=%v, want false/context canceled", received, err)
	}
}

func TestSQLiteStoreLTrimKeepsRequestedRange(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	defer store.Close()

	for _, value := range []string{"a", "b", "c", "d"} {
		if err := store.RPush(ctx, "history", []byte(value), 0); err != nil {
			t.Fatalf("rpush %s: %v", value, err)
		}
	}

	if err := store.LTrim(ctx, "history", -2, -1); err != nil {
		t.Fatalf("ltrim last two: %v", err)
	}
	got, err := store.LRange(ctx, "history", 0, -1)
	if err != nil {
		t.Fatalf("lrange: %v", err)
	}
	if want := []string{"c", "d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after first trim got %v want %v", got, want)
	}

	if err := store.RPush(ctx, "history", []byte("e"), 0); err != nil {
		t.Fatalf("rpush e: %v", err)
	}
	if err := store.LTrim(ctx, "history", 1, 1); err != nil {
		t.Fatalf("ltrim middle: %v", err)
	}
	got, err = store.LRange(ctx, "history", 0, -1)
	if err != nil {
		t.Fatalf("lrange after second trim: %v", err)
	}
	if want := []string{"d"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after second trim got %v want %v", got, want)
	}
}
