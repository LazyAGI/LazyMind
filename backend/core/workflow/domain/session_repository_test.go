package domain

import (
	"context"
	"testing"
	"time"
)

func TestLegacySchemaWorkflowRoundTrip(t *testing.T) {
	db := openCapabilityDB(t)
	if err := db.Exec(`CREATE TABLE plugin_sessions (
		id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, plugin_id TEXT NOT NULL,
		plugin_ref TEXT NOT NULL, plugin_revision_id TEXT NOT NULL, status TEXT NOT NULL,
		state_version INTEGER NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	want := &Session{ID: "s1", ConversationID: "c1", WorkflowID: "w1", WorkflowRef: "builtin:w1", WorkflowRevision: "r1", Status: "active", StateVersion: 2, CreatedAt: now, UpdatedAt: now}
	if err := WriteSession(context.Background(), db, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSession(context.Background(), db, want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowID != want.WorkflowID || got.WorkflowRef != want.WorkflowRef || got.OriginHost != "lazymind" {
		t.Fatalf("legacy round trip = %#v", got)
	}
}

func TestExpandedReaderAcceptsUnbackfilledRow(t *testing.T) {
	db := openCapabilityDB(t)
	if err := db.Exec(`CREATE TABLE plugin_sessions (
		id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, plugin_id TEXT NOT NULL,
		plugin_ref TEXT NOT NULL, plugin_revision_id TEXT NOT NULL, status TEXT NOT NULL,
		state_version INTEGER NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
		origin_host TEXT, origin_ref TEXT, controller_host TEXT)`).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO plugin_sessions
		(id, conversation_id, plugin_id, plugin_ref, plugin_revision_id, status, state_version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, "s1", "", "w1", "builtin:w1", "r1", "active", 0, now, now).Error; err != nil {
		t.Fatal(err)
	}
	got, err := ReadSession(context.Background(), db, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginHost != "lazymind" || got.ControllerHost != "lazymind" || got.OriginRef != "" {
		t.Fatalf("unbackfilled defaults = %#v", got)
	}
}
