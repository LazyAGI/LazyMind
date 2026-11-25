package main

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsStartupIdentityBlocksWritesUntilRelease(t *testing.T) {
	paths, app, helper := startupIdentityTestInstallation(t)
	_, release, err := watchStartupIdentityInputs(paths)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer release()
		for _, file := range []string{app, helper} {
			if err := os.WriteFile(file, []byte("other"), 0600); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
				t.Fatalf("overwriting %s must be blocked, got %v", file, err)
			}
		}
	}()
	for _, file := range []string{app, helper} {
		if err := os.WriteFile(file, []byte("other"), 0600); err != nil {
			t.Fatalf("write after releasing %s: %v", file, err)
		}
	}
}
