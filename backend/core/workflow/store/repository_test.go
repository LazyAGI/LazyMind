package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func testRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	if err := repo.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestAuthorizeConversationScopesWorkflowBindingToOwner(t *testing.T) {
	repo := testRepo(t)
	if err := repo.db.AutoMigrate(&orm.Conversation{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repo.db.Create(&orm.Conversation{ID: "conversation-1", BaseModel: orm.BaseModel{
		CreateUserID: "owner", CreatedAt: now, UpdatedAt: now,
	}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.AuthorizeConversation(t.Context(), "conversation-1", "owner"); err != nil {
		t.Fatalf("owner could not bind Workflow to conversation: %v", err)
	}
	if err := repo.AuthorizeConversation(t.Context(), "conversation-1", "other"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("another owner could bind Workflow to conversation: %v", err)
	}
}

func TestDeleteArtifactCreatesTombstoneAndPreservesHistory(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := repo.db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowHumanArtifact{},
		&orm.WorkflowSlotRevision{}); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Create(&orm.WorkflowSession{ID: "s1", CreateUserID: "u1",
		WorkflowID: "wf", Status: "active", StateVersion: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	caption := "original"
	if err := repo.db.Create(&orm.WorkflowHumanArtifact{ID: "h1", SessionID: "s1", Slot: "report",
		ContentType: "text/plain", Value: json.RawMessage(`{"text":"kept"}`), Caption: &caption,
		CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	humanID := "h1"
	if err := repo.db.Create(&orm.WorkflowSlotRevision{ID: "a1", SessionID: "s1", SlotID: "report",
		Slot: "report", StepID: "draft", Revision: 1, Selected: true, HumanArtifactID: &humanID,
		Validity: "effective", ChangeSource: "agent", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.DeleteArtifact(ctx, "u1", "a1", 1, "cmd-delete")
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Deleted || deleted.Revision != 2 || deleted.Validity != "deleted" || !deleted.Selected {
		t.Fatalf("unexpected tombstone: %#v", deleted)
	}
	original, err := repo.ReadArtifact(ctx, "u1", "a1")
	if err != nil || original.Selected || original.Deleted || string(original.Value) != `{"text":"kept"}` {
		t.Fatalf("history was not preserved: %#v err=%v", original, err)
	}
	if _, err := repo.DeleteArtifact(ctx, "u1", deleted.ID, 2, "cmd-again"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("repeated delete must conflict: %v", err)
	}
}

func TestPreparationIsOwnerScopedAndConsumedOnce(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	p1, created, err := repo.Prepare(ctx, "u1", "same", "writer", "workflow.v1", json.RawMessage(`{"x":1}`), json.RawMessage(`{"ready":true}`))
	if err != nil || !created {
		t.Fatalf("prepare: created=%v err=%v", created, err)
	}
	p2, created, err := repo.Prepare(ctx, "u1", "same", "writer", "workflow.v1", json.RawMessage(`{"x":1}`), json.RawMessage(`{"ready":true}`))
	if err != nil || created || p1.ID != p2.ID {
		t.Fatalf("idempotent prepare: %#v %v", p2, err)
	}
	if _, _, err := repo.Consume(ctx, p1.ID, "u2", "s1"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("owner check: %v", err)
	}
	got, consumed, err := repo.Consume(ctx, p1.ID, "u1", "s1")
	if err != nil || !consumed || got.SessionID != "s1" {
		t.Fatalf("consume: %#v %v", got, err)
	}
	got, consumed, err = repo.Consume(ctx, p1.ID, "u1", "other")
	if err != nil || consumed || got.SessionID != "s1" {
		t.Fatalf("second consume: %#v %v", got, err)
	}
}

func TestCreateHostSessionRejectsAnyExistingNonDismissedConversationSession(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	if err := repo.db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	workflow := WorkflowPackage{WorkflowID: "wf", WorkflowRef: "builtin:wf", RevisionID: "rev-1"}
	for _, status := range []string{"active", "waiting", "stopped", "failed", "completed"} {
		t.Run(status, func(t *testing.T) {
			conversationID := "conversation-" + status
			now := time.Now().UTC()
			existing := orm.WorkflowSession{ID: "existing-" + status, ConversationID: conversationID,
				WorkflowID: "wf", Status: status, CreateUserID: "u1", CreatedAt: now, UpdatedAt: now}
			if err := repo.db.Create(&existing).Error; err != nil {
				t.Fatal(err)
			}
			_, _, err := repo.CreateHostSession(ctx, "u1", "new-"+status, conversationID,
				"lazymind", conversationID, "lazymind", workflow)
			if !errors.Is(err, ErrSessionConflict) {
				t.Fatalf("status %q must block a second non-dismissed session: %v", status, err)
			}
		})
	}
}

func TestCreateHostSessionAllowsReplacementAfterDismiss(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	if err := repo.db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repo.db.Create(&orm.WorkflowSession{ID: "dismissed", ConversationID: "conversation",
		WorkflowID: "wf", Status: "stopped", Dismissed: true, CreateUserID: "u1",
		CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	created, ok, err := repo.CreateHostSession(ctx, "u1", "replacement", "conversation",
		"lazymind", "conversation", "lazymind",
		WorkflowPackage{WorkflowID: "wf", WorkflowRef: "builtin:wf", RevisionID: "rev-1"})
	if err != nil || !ok || created.ID != "replacement" {
		t.Fatalf("dismissed history must not block replacement: created=%#v ok=%v err=%v", created, ok, err)
	}
}

func TestAuthorizeSessionDistinguishesMissingFromWrongOwner(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	if err := repo.db.Exec(`CREATE TABLE plugin_sessions (id TEXT PRIMARY KEY, create_user_id TEXT NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Exec(`INSERT INTO plugin_sessions(id, create_user_id) VALUES ('s1','u1')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.AuthorizeSession(ctx, "missing", "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing session error=%v", err)
	}
	if err := repo.AuthorizeSession(ctx, "s1", "u2"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("wrong owner error=%v", err)
	}
	if err := repo.AuthorizeSession(ctx, "s1", "u1"); err != nil {
		t.Fatalf("owner authorization error=%v", err)
	}
}

func TestCommandExecutesOnceAndRejectsPayloadConflict(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	var calls atomic.Int32
	execute := func(_ *gorm.DB) (int, json.RawMessage, error) {
		calls.Add(1)
		return 200, json.RawMessage(`{"accepted":true}`), nil
	}
	first, created, err := repo.Command(ctx, "u1", "s1", "cmd", "workflow.v1", []byte(`{"step":"a"}`), execute)
	if err != nil || !created {
		t.Fatalf("first: %#v %v", first, err)
	}
	second, created, err := repo.Command(ctx, "u1", "s1", "cmd", "workflow.v1", []byte(`{"step":"a"}`), execute)
	if err != nil || created || string(second.ResponseJSON) != string(first.ResponseJSON) {
		t.Fatalf("replay: %#v %v", second, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("execute calls=%d", calls.Load())
	}
	events, err := repo.Replay(ctx, "s1", "u1", 0, 100)
	if err != nil || len(events) != 1 || events[0].CommandID != "cmd" || events[0].EventType != "workflow.patch" {
		t.Fatalf("durable command event=%#v err=%v", events, err)
	}
	_, _, err = repo.Command(ctx, "u1", "s1", "cmd", "workflow.v1", []byte(`{"step":"b"}`), execute)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict=%v", err)
	}
}

func TestConcurrentPreparationDeduplicates(t *testing.T) {
	repo := testRepo(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, _, err := repo.Prepare(context.Background(), "u", "key", "wf", "workflow.v1", json.RawMessage(`{}`), json.RawMessage(`{}`))
			if err != nil {
				t.Errorf("prepare: %v", err)
				return
			}
			ids <- p.ID
		}()
	}
	wg.Wait()
	close(ids)
	var want string
	for id := range ids {
		if want == "" {
			want = id
		}
		if id != want {
			t.Fatalf("ids differ: %q != %q", id, want)
		}
	}
}

func TestEventReplayUsesPersistentCursorAndOwner(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	for _, typ := range []string{"snapshot", "attempt.running", "artifact.saved"} {
		if err := repo.AppendEvent(ctx, &Event{SessionID: "s1", OwnerUserID: "u1", EventType: typ, PayloadJSON: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := repo.Replay(ctx, "s1", "u1", 0, 100)
	if err != nil || len(all) != 3 {
		t.Fatalf("all=%d err=%v", len(all), err)
	}
	replay, err := repo.Replay(ctx, "s1", "u1", all[0].ID, 100)
	if err != nil || len(replay) != 2 || replay[0].ID <= all[0].ID {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	other, err := repo.Replay(ctx, "s1", "u2", 0, 100)
	if err != nil || len(other) != 0 {
		t.Fatalf("owner leak: %#v %v", other, err)
	}
}

func TestAutomaticAttemptCountExcludesUserAuthorizedCommands(t *testing.T) {
	repo := testRepo(t)
	ctx := context.Background()
	now := time.Now().UTC()
	rows := []orm.WorkflowTransitionCommand{
		{CommandID: "initial", SessionID: "s1", TargetStepID: "draft", Operation: "execute", RetryOrigin: "automatic", Status: "accepted", ResponseJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now},
		{CommandID: "auto-1", SessionID: "s1", TargetStepID: "draft", Operation: "retry", RetryOrigin: "automatic", Status: "accepted", ResponseJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now},
		{CommandID: "user-1", SessionID: "s1", TargetStepID: "draft", Operation: "retry", RetryOrigin: "user", Status: "accepted", ResponseJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now},
	}
	if err := repo.db.AutoMigrate(&orm.WorkflowTransitionCommand{}); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	got, err := repo.AutomaticAttemptCount(ctx, "s1", "draft")
	if err != nil || got != 2 {
		t.Fatalf("automatic retries=%d err=%v, want 2", got, err)
	}
}
