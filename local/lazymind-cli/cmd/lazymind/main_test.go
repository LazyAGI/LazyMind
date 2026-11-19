package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInternalWorkBuddyLoginHonorsCancellation(t *testing.T) {
	t.Setenv("LAZYMIND_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output, stderr bytes.Buffer
	err := runInternal(ctx, []string{"agent", "workbuddy", "login"}, &output, &stderr)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WorkBuddy login did not reach its cancellable adapter: %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("canceled login returned a success receipt")
	}
}

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

func TestInternalSessionConditionalClearPreservesNewLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOME", home)
	var output bytes.Buffer
	current := `{"server_url":"http://127.0.0.1:8090","access_token":"new","refresh_token":"new-refresh"}`
	if err := runInternalSession([]string{"set"}, strings.NewReader(current), &output); err != nil {
		t.Fatal(err)
	}
	old := `{"expected_session":{"server_url":"http://127.0.0.1:8090","access_token":"old","refresh_token":"old-refresh"}}`
	output.Reset()
	if err := runInternalSession([]string{"clear-if-current"}, strings.NewReader(old), &output); err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Cleared bool `json:"cleared"`
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Cleared {
		t.Fatalf("wrong clear receipt: %s", output.String())
	}
	if _, err := os.Stat(filepath.Join(home, "credentials.json")); err != nil {
		t.Fatal("new credentials were deleted")
	}
	output.Reset()
	if err := runInternalSession([]string{"clear-if-current"}, strings.NewReader(`{"expected_session":`+current+`}`), &output); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Cleared {
		t.Fatalf("explicit logout did not clear matching credentials: %s", output.String())
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
