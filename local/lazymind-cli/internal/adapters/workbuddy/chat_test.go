package workbuddy

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"lazymind/agentconnector/internal/agentexec"
)

func TestLoginOpensDiscoveredCodeBuddyInteractively(t *testing.T) {
	name, body := "codebuddy", "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		name, body = "codebuddy.cmd", "@echo off\r\nexit /b 0\r\n"
	}
	binary := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
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
	if !agentexec.SameExecutable(opened, resolved) {
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
