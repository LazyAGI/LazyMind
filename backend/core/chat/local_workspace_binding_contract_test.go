package chat

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
)

func TestWorkspaceWorkCreationLocksBindingAndRollsBackUnauthorizedGrant(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := newPromptTestDB(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	registered, err := localworkspace.Register(context.Background(), db.DB, "owner", localworkspace.RegisterInput{
		DisplayName: "project", CanonicalPath: root, Source: "local",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, _, err := ensureConversationWithWorkspace(context.Background(), db.DB, "new-task", "task", nil, nil,
		"owner", "owner", true, "", nil, nil, map[string]any{
			"workspace_id": registered.WorkspaceID, "workspace_permission_mode": localworkspace.PermissionAlwaysAsk,
		})
	if err != nil {
		t.Fatal(err)
	}
	if !created.IsTaskConv {
		t.Fatal("workspace conversation was not stored as Work")
	}
	var createdBinding orm.ConversationWorkspaceBinding
	if err := db.Where("conversation_id = ?", created.ID).First(&createdBinding).Error; err != nil {
		t.Fatal(err)
	}
	if createdBinding.WorkspaceID != registered.WorkspaceID ||
		createdBinding.PermissionMode != localworkspace.PermissionAlwaysAsk || createdBinding.PermissionVersion != 1 {
		t.Fatalf("created binding=%+v", createdBinding)
	}

	// Seed metadata through ORM; native selection proof is exercised in task 2.
	grant := orm.LocalWorkspace{ID: "grant", CreateUserID: "owner", CanonicalPath: root, Status: "active", Version: 1, Source: "local"}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	// ResolveActiveForBinding must verify directory identity. Use the existing
	// package service fixture contract for the authorized creation case instead
	// of inventing a platform identity string here.
	_, _, err = ensureConversationWithWorkspace(context.Background(), db.DB, "denied-task", "task", nil, nil, "other", "other", true, "", nil, nil, map[string]any{"workspace_id": grant.ID})
	var app *common.AppError
	if !errors.As(err, &app) || app.HTTPStatus != 404 {
		t.Fatalf("cross-owner binding=%v", err)
	}
	var count int64
	if err := db.Table("conversations").Where("id = ?", "denied-task").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected grant created a conversation")
	}
	// Existing bindings must be checked before consulting a requested grant.
	conv := orm.Conversation{ID: "bound-task", IsTaskConv: true, BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: conv.ID, WorkspaceID: grant.ID, PermissionMode: localworkspace.PermissionAskAsNeeded, PermissionVersion: 1}).Error; err != nil {
		t.Fatal(err)
	}
	_, _, err = ensureConversationWithWorkspace(context.Background(), db.DB, conv.ID, "task", nil, nil, "owner", "owner", true, "", nil, nil, map[string]any{"workspace_id": "another-grant"})
	if !errors.As(err, &app) || app.HTTPStatus != 409 || app.Code != common.ResolveAppError("conflict", 409).Code {
		t.Fatalf("switch binding=%v", err)
	}
	detail, ok := app.Detail.(map[string]any)
	if !ok || detail["reason"] != "binding_locked" {
		t.Fatalf("switch reason=%+v", app)
	}
	var saved orm.ConversationWorkspaceBinding
	if err := db.Where("conversation_id = ?", conv.ID).First(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if saved.WorkspaceID != grant.ID {
		t.Fatalf("binding switched: %+v", saved)
	}
}
