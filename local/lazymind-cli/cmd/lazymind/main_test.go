package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInternalSessionSetAndClear(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOME", home)
	input := `{
  "server_url": "http://127.0.0.1:8090",
  "access_token": "access",
  "refresh_token": "refresh"
}`
	var output bytes.Buffer
	if err := runInternalSession([]string{"set"}, strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(home, "credentials.json"))
	if err != nil || !bytes.Contains(body, []byte(`"access_token": "access"`)) {
		t.Fatalf("credentials=%s err=%v", body, err)
	}
	if err := runInternalSession([]string{"clear"}, strings.NewReader(""), &output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("credentials were not cleared: %v", err)
	}
}

func TestContextWithOwnerProcessCancelsWhenParentChanges(t *testing.T) {
	var parentPID atomic.Int64
	parentPID.Store(123)
	ctx, cancel, err := contextWithOwnerProcess(
		context.Background(),
		123,
		func() int { return int(parentPID.Load()) },
		time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	parentPID.Store(1)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("owner context was not canceled after the parent process changed")
	}
}

func TestContextWithOwnerProcessRejectsNonParentPID(t *testing.T) {
	_, _, err := contextWithOwnerProcess(
		context.Background(),
		123,
		func() int { return 456 },
		time.Millisecond,
	)
	if err == nil || !strings.Contains(err.Error(), "not the current parent") {
		t.Fatalf("error = %v, want current-parent validation", err)
	}
}
