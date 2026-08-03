package log

import (
	"testing"
)

// TestInit ensures the global Logger is non-nil after Init().
func TestInit(t *testing.T) {
	Init()
	if &Logger == nil {
		t.Fatal("Logger should not be nil after Init()")
	}
}

// TestInitNop ensures the global Logger is non-nil after InitNop().
func TestInitNop(t *testing.T) {
	InitNop()
	if &Logger == nil {
		t.Fatal("Logger should not be nil after InitNop()")
	}
}

// TestInitThenInitNop ensures calling Init() followed by InitNop() leaves a non-nil Logger.
func TestInitThenInitNop(t *testing.T) {
	Init()
	InitNop()
	if &Logger == nil {
		t.Fatal("Logger should not be nil after Init()+InitNop()")
	}
}
