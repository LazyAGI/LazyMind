package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/state"
)

func TestSQLiteTaskStreamUsesTemporaryNDJSONFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv(subagentStreamDirEnv, filepath.Join(root, "streams"))
	stateStore, err := state.NewSQLiteStore(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatalf("create sqlite state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	events := []TaskEvent{
		{Type: "think", TaskID: "../../task-1", Think: "first"},
		{Type: "text", TaskID: "../../task-1", Text: "second"},
	}
	for _, event := range events {
		if err := AppendStreamEvent(context.Background(), stateStore, event.TaskID, event); err != nil {
			t.Fatalf("append stream event: %v", err)
		}
	}

	path := localTaskStreamPath("../../task-1")
	if filepath.Dir(path) != localTaskStreamDir() {
		t.Fatalf("stream escaped configured directory: %q", path)
	}
	if filepath.Ext(path) != ".ndjson" {
		t.Fatalf("unexpected stream extension: %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat stream file: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("stream file is accessible beyond owner: %v", info.Mode().Perm())
	}

	raw, err := StreamEventsFrom(context.Background(), stateStore, "../../task-1", 1)
	if err != nil {
		t.Fatalf("read stream events: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("expected one event from offset 1, got %d", len(raw))
	}
	var got TaskEvent
	if err := json.Unmarshal([]byte(raw[0]), &got); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if got.Type != "text" || got.Text != "second" {
		t.Fatalf("unexpected event: %#v", got)
	}

	rows, err := stateStore.LRange(context.Background(), taskStreamKey("../../task-1"), 0, -1)
	if err != nil {
		t.Fatalf("read sqlite stream rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no subagent stream rows in sqlite, got %d", len(rows))
	}
}

func TestExpiredLocalTaskStreamIsRemoved(t *testing.T) {
	root := t.TempDir()
	t.Setenv(subagentStreamDirEnv, filepath.Join(root, "streams"))
	stateStore, err := state.NewSQLiteStore(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatalf("create sqlite state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	if err := AppendStreamEvent(context.Background(), stateStore, "task-expired", TaskEvent{Type: "text"}); err != nil {
		t.Fatalf("append stream event: %v", err)
	}
	path := localTaskStreamPath("task-expired")
	expired := time.Now().Add(-taskStreamExpire - time.Minute)
	if err := os.Chtimes(path, expired, expired); err != nil {
		t.Fatalf("age stream file: %v", err)
	}
	exists, err := StreamExists(context.Background(), stateStore, "task-expired")
	if err != nil {
		t.Fatalf("check stream existence: %v", err)
	}
	if exists {
		t.Fatal("expected expired stream to be absent")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected expired file removal, stat error: %v", err)
	}
}

func TestSQLiteTaskStreamMigratesLegacyRowsBeforeAppending(t *testing.T) {
	root := t.TempDir()
	t.Setenv(subagentStreamDirEnv, filepath.Join(root, "streams"))
	stateStore, err := state.NewSQLiteStore(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatalf("create sqlite state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	ctx := context.Background()
	taskID := "legacy-task"
	legacy := []TaskEvent{
		{Type: "think", TaskID: taskID, Think: "legacy-1"},
		{Type: "text", TaskID: taskID, Text: "legacy-2"},
	}
	for _, event := range legacy {
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("encode legacy event: %v", err)
		}
		if err := stateStore.RPush(ctx, taskStreamKey(taskID), body, taskStreamExpire); err != nil {
			t.Fatalf("seed legacy event: %v", err)
		}
	}

	if err := AppendStreamEvent(ctx, stateStore, taskID, TaskEvent{
		Type: "text", TaskID: taskID, Text: "new-3",
	}); err != nil {
		t.Fatalf("append event after migration: %v", err)
	}
	raw, err := StreamEventsFrom(ctx, stateStore, taskID, 0)
	if err != nil {
		t.Fatalf("read migrated stream: %v", err)
	}
	if len(raw) != 3 {
		t.Fatalf("expected three migrated events, got %d", len(raw))
	}
	wantText := []string{"legacy-1", "legacy-2", "new-3"}
	for index, body := range raw {
		var event TaskEvent
		if err := json.Unmarshal([]byte(body), &event); err != nil {
			t.Fatalf("decode event %d: %v", index, err)
		}
		got := event.Text
		if got == "" {
			got = event.Think
		}
		if got != wantText[index] {
			t.Fatalf("event %d: got %q, want %q", index, got, wantText[index])
		}
	}
	rows, err := stateStore.LRange(ctx, taskStreamKey(taskID), 0, -1)
	if err != nil {
		t.Fatalf("read legacy sqlite rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected migrated sqlite rows to be deleted, got %d", len(rows))
	}
}
