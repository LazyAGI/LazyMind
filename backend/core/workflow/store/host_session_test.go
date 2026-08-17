package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestCreateHostSessionPersistsConversationBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	session, created, err := repo.CreateHostSession(
		context.Background(), "owner", "session-1", "conversation-1",
		"lazymind", "conversation-1", "lazymind",
		WorkflowPackage{WorkflowID: "test-workflow", WorkflowRef: "builtin:test-workflow",
			RevisionID: "revision-1", RevisionNo: 1, TreeHash: "tree-1", GraphHash: "graph-1",
			GraphVersion: "3", CompiledGraph: []byte(`{"nodes":{}}`)},
	)
	if err != nil || !created {
		t.Fatalf("create session: created=%v err=%v", created, err)
	}
	if session.ConversationID != "conversation-1" || session.OriginRef != "conversation-1" {
		t.Fatalf("conversation binding lost: %#v", session)
	}
	if session.CreatedAt.After(time.Now().UTC()) {
		t.Fatalf("invalid creation time: %v", session.CreatedAt)
	}
}

func TestListExternalSessionsIsOwnerScopedAndCursorPaged(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	start := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	sessions := []orm.WorkflowSession{
		{ID: "external-1", OriginHost: "external-agent", ControllerHost: "external-agent", WorkflowID: "writer", Status: "active", CreateUserID: "owner", CreatedAt: start, UpdatedAt: start},
		{ID: "external-2", OriginHost: "external-agent", ControllerHost: "external-agent", WorkflowID: "image", Status: "stopped", CreateUserID: "owner", CreatedAt: start, UpdatedAt: start.Add(time.Minute)},
		{ID: "internal", OriginHost: "lazymind", ControllerHost: "lazymind", WorkflowID: "writer", Status: "active", CreateUserID: "owner", CreatedAt: start, UpdatedAt: start.Add(2 * time.Minute)},
		{ID: "other-owner", OriginHost: "external-agent", ControllerHost: "external-agent", WorkflowID: "writer", Status: "active", CreateUserID: "other", CreatedAt: start, UpdatedAt: start.Add(3 * time.Minute)},
		{ID: "dismissed", OriginHost: "external-agent", ControllerHost: "external-agent", WorkflowID: "writer", Status: "active", Dismissed: true, CreateUserID: "owner", CreatedAt: start, UpdatedAt: start.Add(4 * time.Minute)},
	}
	if err := db.Create(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListExternalSessions(context.Background(), "owner", SessionListQuery{PageSize: 1})
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].SessionID != "external-2" || page.NextPageToken == "" {
		t.Fatalf("first page: %+v err=%v", page, err)
	}
	next, err := repo.ListExternalSessions(context.Background(), "owner", SessionListQuery{PageSize: 1, PageToken: page.NextPageToken})
	if err != nil || len(next.Sessions) != 1 || next.Sessions[0].SessionID != "external-1" || next.NextPageToken != "" {
		t.Fatalf("next page: %+v err=%v", next, err)
	}
	filtered, err := repo.ListExternalSessions(context.Background(), "owner", SessionListQuery{Status: "active"})
	if err != nil || len(filtered.Sessions) != 1 || filtered.Sessions[0].SessionID != "external-1" {
		t.Fatalf("filtered: %+v err=%v", filtered, err)
	}
	if _, err := repo.ListExternalSessions(context.Background(), "owner", SessionListQuery{Status: "active", PageToken: page.NextPageToken}); !errors.Is(err, ErrInvalidSessionQuery) {
		t.Fatalf("cursor must be bound to filters: %v", err)
	}
}

func TestSessionLifecycleCommandIsIdempotentAndInterruptsAttempt(t *testing.T) {
	repo := testRepo(t)
	if err := repo.db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowOutbox{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := repo.db.Create(&orm.WorkflowSession{ID: "session-1", OriginHost: "external-agent", ControllerHost: "external-agent",
		WorkflowID: "writer", Status: "active", StateVersion: 4, CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Create(&orm.WorkflowSessionStep{ID: "attempt-1", SessionID: "session-1", StepID: "draft",
		TaskID: "attempt-1", Status: "running", Validity: "effective", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	version, err := repo.SetSessionStopped(context.Background(), "owner", "session-1", "stop-1", true)
	if err != nil || version != 5 {
		t.Fatalf("stop: version=%d err=%v", version, err)
	}
	replayed, err := repo.SetSessionStopped(context.Background(), "owner", "session-1", "stop-1", true)
	if err != nil || replayed != 5 {
		t.Fatalf("replay: version=%d err=%v", replayed, err)
	}
	var session orm.WorkflowSession
	if err := repo.db.First(&session, "id = ?", "session-1").Error; err != nil || session.Status != "stopped" || session.StateVersion != 5 {
		t.Fatalf("session after stop: %+v err=%v", session, err)
	}
	var attempt orm.WorkflowSessionStep
	if err := repo.db.First(&attempt, "id = ?", "attempt-1").Error; err != nil || attempt.Status != "interrupted" || attempt.TerminalCode != "WORKFLOW_STOPPED" {
		t.Fatalf("attempt after stop: %+v err=%v", attempt, err)
	}
	if _, err := repo.SetSessionStopped(context.Background(), "owner", "session-1", "stop-1", false); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same command with different action: %v", err)
	}
	version, err = repo.SetSessionStopped(context.Background(), "owner", "session-1", "resume-1", false)
	if err != nil || version != 6 {
		t.Fatalf("resume: version=%d err=%v", version, err)
	}
	if err := repo.db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetSessionStopped(context.Background(), "owner", "session-1", "stop-terminal", true); err == nil || err.Error() != "WORKFLOW_TERMINAL" {
		t.Fatalf("terminal session stop: %v", err)
	}
}
