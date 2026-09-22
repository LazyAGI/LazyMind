package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/localworkspace"
	"lazymind/core/store"
)

func TestProjectCreationInheritanceTrashAndRestore(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	ctx := context.Background()
	uid := "user-1"
	root := projectWorkspacePath(t)
	path := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	grant, err := localworkspace.Register(ctx, db.DB, uid, localworkspace.RegisterInput{DisplayName: "directory", CanonicalPath: root, Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(id string, raw map[string]any) error {
		_, _, err := ensureConversationWithWorkspace(ctx, db.DB, id, id, nil, nil, uid, uid, false, "", nil, nil, raw)
		return err
	}
	if err := create("first", map[string]any{"workspace_id": grant.WorkspaceID, "project_name": "Named"}); err != nil {
		t.Fatal(err)
	}
	var member orm.ConversationGroupMember
	if err := db.Where("conversation_id=?", "first").Take(&member).Error; err != nil {
		t.Fatal(err)
	}
	projectID := member.GroupID
	if err := create("second", map[string]any{"group_id": projectID}); err != nil {
		t.Fatal(err)
	}
	member = orm.ConversationGroupMember{}
	if err := db.Where("conversation_id=?", "second").Take(&member).Error; err != nil || member.GroupID != projectID {
		t.Fatalf("project entry failed: %+v %v", member, err)
	}
	group := orm.ConversationGroup{ID: "ordinary", UserID: uid, Name: "ordinary", NormalizedName: "ordinary"}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := create("conflict", map[string]any{"group_id": group.ID, "workspace_id": grant.WorkspaceID}); err == nil {
		t.Fatal("conflicting group and workspace accepted")
	}
	var count int64
	db.Model(&orm.Conversation{}).Where("id=?", "conflict").Count(&count)
	if count != 0 {
		t.Fatal("conflict left a conversation")
	}
	// All creation paths use the same inheritance helper; forks keep their own lifecycle.
	if err := conversationgroup.UserTransaction(ctx, db.DB, uid, func(tx *gorm.DB) error {
		if err := tx.Create(&orm.Conversation{ID: "fork", DisplayName: "fork", BaseModel: orm.BaseModel{CreateUserID: uid}}).Error; err != nil {
			return err
		}
		return conversationgroup.InheritProject(ctx, tx, uid, "first", "fork")
	}); err != nil {
		t.Fatal(err)
	}
	member = orm.ConversationGroupMember{}
	if err := db.Where("conversation_id=?", "fork").Take(&member).Error; err != nil || member.GroupID != projectID {
		t.Fatalf("inheritance: %+v %v", member, err)
	}
	if err := db.Model(&orm.Conversation{}).Where("id=?", "second").Update("archived_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	request := sidechatRequest(http.MethodDelete, "/api/core/conversation-groups/"+projectID, uid, "", map[string]string{"group_id": projectID})
	response := httptest.NewRecorder()
	DeleteConversationGroup(response, request)
	if response.Code != 200 {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	db.Model(&orm.Conversation{}).Where("id IN ? AND deleted_at IS NOT NULL", []string{"first", "second", "fork"}).Count(&count)
	if count != 3 {
		t.Fatalf("trashed %d conversations", count)
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "keep" {
		t.Fatal("project deletion touched local files")
	}
	request = sidechatRequest(http.MethodPost, "/api/core/conversations/first:restore", uid, "", map[string]string{"name": "first", "conversation_id": "first"})
	response = httptest.NewRecorder()
	RestoreConversation(response, request)
	if response.Code != 200 {
		t.Fatalf("restore: %d %s", response.Code, response.Body.String())
	}
	var project orm.ConversationGroup
	if err := db.Where("id=?", projectID).Take(&project).Error; err != nil || project.DeletedAt != nil {
		t.Fatalf("project not restored: %+v %v", project, err)
	}
	db.Model(&orm.Conversation{}).Where("id IN ? AND deleted_at IS NOT NULL", []string{"second", "fork"}).Count(&count)
	if count != 2 {
		t.Fatal("restore revived unrelated conversations")
	}
	if err := create("reused", map[string]any{"workspace_id": grant.WorkspaceID}); err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.ConversationGroup{}).Where("kind=?", conversationgroup.KindProject).Count(&count)
	if count != 1 {
		t.Fatalf("created duplicate project: %d", count)
	}
	// A replacement at the same path must not inherit the original project.
	if err := os.Rename(root, root+"-previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	root = canonicalProjectWorkspacePath(t, root)
	replacement, err := localworkspace.Register(ctx, db.DB, uid, localworkspace.RegisterInput{DisplayName: "replacement", CanonicalPath: root, Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := create("replacement", map[string]any{"workspace_id": replacement.WorkspaceID}); err == nil {
		t.Fatal("replacement inherited original project")
	}
	for _, model := range []any{&orm.Conversation{}, &orm.ConversationWorkspaceBinding{}} {
		column := "conversation_id"
		if _, ok := model.(*orm.Conversation); ok {
			column = "id"
		}
		if err := db.Model(model).Where(column+"=?", "replacement").Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("conflict failed to roll back %T: count=%d err=%v", model, count, err)
		}
	}
}

func projectWorkspacePath(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	return canonicalProjectWorkspacePath(t, root)
}

func canonicalProjectWorkspacePath(t *testing.T, root string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
