package state

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

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
