package workbuddy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

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
	authenticationFile := filepath.Join(t.TempDir(), "authenticated")
	openInteractiveCommand = func(path string) error {
		opened = path
		return os.WriteFile(authenticationFile, []byte("authenticated"), 0o600)
	}

	if err := login(context.Background(), binary, authenticationFile); err != nil {
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

func TestLoginWaitsForCodeBuddyAuthentication(t *testing.T) {
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
	openInteractiveCommand = func(string) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := login(ctx, binary, filepath.Join(t.TempDir(), "missing-auth")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("login error=%v, want deadline exceeded", err)
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
