package asyncjob

import (
	"context"
	"testing"
)

// TestRegisterAndLookup stores and retrieves a handler by job type.
func TestRegisterAndLookup(t *testing.T) {
	resetRegistryForTest()

	handler := func(ctx context.Context, job Job, reporter Reporter) (Result, error) {
		return Result{}, nil
	}
	Register("test-type", handler)

	h, ok := lookupHandler("test-type")
	if !ok {
		t.Fatal("handler should be found after registration")
	}
	if h == nil {
		t.Fatal("handler should not be nil")
	}
}

// TestRegisterEmptyJobType panics with empty job type.
func TestRegisterEmptyJobType(t *testing.T) {
	resetRegistryForTest()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty job type")
		}
	}()
	Register("", func(ctx context.Context, job Job, reporter Reporter) (Result, error) {
		return Result{}, nil
	})
}

// TestRegisterNilHandler panics with nil handler.
func TestRegisterNilHandler(t *testing.T) {
	resetRegistryForTest()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil handler")
		}
	}()
	Register("test-type", nil)
}

// TestLookupHandlerNotFound returns false for unregistered job type.
func TestLookupHandlerNotFound(t *testing.T) {
	resetRegistryForTest()

	_, ok := lookupHandler("unknown-type")
	if ok {
		t.Fatal("should not find unregistered handler")
	}
}

// TestRegisterOverwriteReplaces existing handler for same job type.
func TestRegisterOverwriteReplaces(t *testing.T) {
	resetRegistryForTest()

	called := false
	h1 := func(ctx context.Context, job Job, reporter Reporter) (Result, error) {
		return Result{}, nil
	}
	h2 := func(ctx context.Context, job Job, reporter Reporter) (Result, error) {
		called = true
		return Result{}, nil
	}

	Register("overwrite-type", h1)
	Register("overwrite-type", h2)

	h, ok := lookupHandler("overwrite-type")
	if !ok {
		t.Fatal("handler should exist")
	}
	// Call to verify it's the second handler
	h(context.Background(), Job{}, nil)
	if !called {
		t.Fatal("second handler should have replaced first")
	}
}
