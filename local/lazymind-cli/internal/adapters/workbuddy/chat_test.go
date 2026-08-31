package workbuddy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoginOpensDiscoveredCodeBuddyInteractively(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "codebuddy")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	previous := openInteractiveCommand
	t.Cleanup(func() { openInteractiveCommand = previous })
	opened := ""
	openInteractiveCommand = func(path string) error {
		opened = path
		return nil
	}

	if err := Login(context.Background(), binary); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if opened != resolved {
		t.Fatalf("opened=%q want %q", opened, resolved)
	}
}

func TestAvailabilityRequiresCodeBuddyAuthenticationFile(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "Tencent-Cloud.coding-copilot.info")
	runner := &ChatRunner{auth: auth}
	if ready, reason := runner.Availability(); ready || reason == "" {
		t.Fatalf("signed-out availability=(%v, %q)", ready, reason)
	}
	if err := os.WriteFile(auth, []byte("authenticated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ready, reason := runner.Availability(); !ready || reason != "" {
		t.Fatalf("signed-in availability=(%v, %q)", ready, reason)
	}
}
