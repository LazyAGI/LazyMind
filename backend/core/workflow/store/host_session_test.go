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

func TestCreateHostSessionRegistersTaskCenterOnce(t *testing.T) {
	for _, taskType := range []string{"", "background_chat", "scheduled"} {
		t.Run("trigger_"+taskType, func(t *testing.T) {
			repo := testRepo(t)
			createTestConversation(t, repo, "conversation", "owner")
			if taskType != "" {
				now := time.Now().UTC().Add(-time.Minute)
				if err := repo.db.Create(&orm.TaskCenterTask{ID: "trigger", UserID: "owner", ConversationID: "conversation",
					TaskType: taskType, Status: "running", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if _, _, err := repo.CreateHostSession(t.Context(), "owner", "session", "conversation", "lazymind", "conversation", "lazymind",
					WorkflowPackage{WorkflowID: "writer", WorkflowRef: "builtin:writer", RevisionID: "rev"}); err != nil {
					t.Fatal(err)
				}
			}
			var tasks []orm.TaskCenterTask
			if err := repo.db.Find(&tasks).Error; err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 1 || tasks[0].WorkflowSessionID == nil || *tasks[0].WorkflowSessionID != "session" {
				t.Fatalf("session not registered exactly once: %#v", tasks)
			}
			if taskType != "" && (tasks[0].ID != "trigger" || tasks[0].TaskType != taskType) {
				t.Fatalf("trigger identity changed: %#v", tasks[0])
			}
		})
	}
}

func TestCreateHostSessionRollsBackWhenTaskRegistrationFails(t *testing.T) {
	repo := testRepo(t)
	createTestConversation(t, repo, "conversation", "owner")
	if err := repo.db.Migrator().DropTable(&orm.TaskCenterTask{}); err != nil {
		t.Fatal(err)
	}
	_, _, err := repo.CreateHostSession(t.Context(), "owner", "session", "conversation", "lazymind", "conversation", "lazymind",
		WorkflowPackage{WorkflowID: "writer", RevisionID: "rev"})
	if err == nil {
		t.Fatal("session creation must fail if task registration fails")
	}
	var count int64
	if err := repo.db.Model(&orm.WorkflowSession{}).Where("id = ?", "session").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unregistered session was not rolled back")
	}
}

func TestCreateHostSessionPersistsConversationBinding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.Conversation{}, &orm.TaskCenterTask{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "conversation-1", BaseModel: orm.BaseModel{
		CreateUserID: "owner", CreatedAt: now, UpdatedAt: now,
	}}).Error; err != nil {
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
		{ID: "external-1", ConversationID: "conversation-1", OriginHost: "external-agent", ControllerHost: "external-agent", WorkflowID: "writer", Status: "active", CreateUserID: "owner", CreatedAt: start, UpdatedAt: start},
		{ID: "external-2", ConversationID: "conversation-2", OriginHost: "external-agent", ControllerHost: "external-agent", WorkflowID: "image", Status: "stopped", CreateUserID: "owner", CreatedAt: start, UpdatedAt: start.Add(time.Minute)},
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
	scopedContext := WithConversationScope(context.Background(), "conversation-1")
	scoped, err := repo.ListExternalSessions(scopedContext, "owner", SessionListQuery{})
	if err != nil || len(scoped.Sessions) != 1 || scoped.Sessions[0].SessionID != "external-1" ||
		scoped.Sessions[0].ConversationID != "conversation-1" {
		t.Fatalf("conversation-scoped list: %+v err=%v", scoped, err)
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
	if err := repo.db.Create(&orm.WorkflowSession{ID: "session-1", ConversationID: "conversation-1", OriginHost: "external-agent", ControllerHost: "external-agent",
		WorkflowID: "writer", Status: "active", StateVersion: 4, CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Create(&orm.WorkflowSessionStep{ID: "attempt-1", SessionID: "session-1", StepID: "draft",
		TaskID: "attempt-1", Status: "running", Validity: "effective", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	wrongConversation := WithConversationScope(context.Background(), "conversation-2")
	if _, err := repo.SetSessionStopped(wrongConversation, "owner", "session-1", "wrong-scope", true); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("cross-conversation stop error=%v", err)
	}
	version, err := repo.SetSessionStopped(context.Background(), "owner", "session-1", "stop-1", true)
	if err != nil || version != 5 {
		t.Fatalf("stop: version=%d err=%v", version, err)
	}
	replayed, err := repo.SetSessionStopped(context.Background(), "owner", "session-1", "stop-1", true)
	if err != nil || replayed != 5 {
		t.Fatalf("replay: version=%d err=%v", replayed, err)
	}
	if _, err := repo.SetSessionStopped(wrongConversation, "owner", "session-1", "stop-1", true); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("cross-conversation replay error=%v", err)
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
