package conversationgroup

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectsReuseDirectoriesAndStayOutsideOrganizer(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	ctx := context.Background()
	uid := "owner"
	root := projectTestWorkspacePath(t)
	grant, err := registerProjectTestWorkspace(ctx, t, db.DB, uid, "shared", root)
	if err != nil {
		t.Fatal(err)
	}
	var first orm.ConversationGroup
	ensure := func(workspaceID, name string) orm.ConversationGroup {
		t.Helper()
		var result orm.ConversationGroup
		if err := UserTransaction(ctx, db.DB, uid, func(tx *gorm.DB) error {
			var err error
			result, err = EnsureProject(ctx, tx, uid, workspaceID, name, false)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first = ensure(grant.WorkspaceID, "custom")
	if got := ensure(grant.WorkspaceID, "ignored"); got.ID != first.ID || got.Name != "custom" {
		t.Fatalf("project changed: %+v", got)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	subGrant, err := registerProjectTestWorkspace(ctx, t, db.DB, uid, "shared", sub)
	if err != nil {
		t.Fatal(err)
	}
	if got := ensure(subGrant.WorkspaceID, "subdirectory"); got.ID == first.ID {
		t.Fatal("subdirectory reused parent project")
	}
	group := orm.ConversationGroup{ID: "group", UserID: uid, Name: "ordinary", NormalizedName: "ordinary"}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"project-member", "free", "legacy"} {
		if err := db.Create(&orm.Conversation{ID: id, DisplayName: id, BaseModel: orm.BaseModel{CreateUserID: uid}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"project-member", "legacy"} {
		if err := db.Create(&orm.ConversationWorkspaceBinding{ConversationID: id, WorkspaceID: grant.WorkspaceID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := UserTransaction(ctx, db.DB, uid, func(tx *gorm.DB) error { return AttachNewConversation(ctx, tx, uid, "project-member", first.ID) }); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"", group.ID, first.ID} {
		if err := MoveConversation(ctx, db.DB, uid, "project-member", target, CreatedByUser, ""); err == nil {
			t.Fatalf("project member moved to %q", target)
		}
	}
	if err := MoveConversation(ctx, db.DB, uid, "free", first.ID, CreatedByUser, ""); err == nil {
		t.Fatal("free conversation accepted by project")
	}
	snapshot, locks, err := buildSnapshot(ctx, db.DB, "run", uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Groups) != 1 || snapshot.Groups[0].ID != group.ID || len(locks) != 2 {
		t.Fatalf("projects leaked into snapshot: %+v %+v", snapshot, locks)
	}
	// Active organization must not prevent creating, renaming, or restoring a project.
	run := orm.ConversationOrganizerRun{ID: "run", UserID: uid, Status: "running", SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&first).Update("deleted_at", time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	if got := ensure(grant.WorkspaceID, "recreated"); got.ID == first.ID || got.DeletedAt != nil {
		t.Fatal("deleted project reused")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := localworkspace.Register(ctx, db.DB, uid, localworkspace.RegisterInput{DisplayName: "file", CanonicalPath: file, Source: "local"}); err == nil {
		t.Fatal("file accepted as directory")
	}
}

func TestManualProjectCreationAndRenameDuringOrganizer(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	const uid = "manual-project-owner"
	root := projectTestWorkspacePath(t)
	grant, err := registerProjectTestWorkspace(t.Context(), t, db.DB, uid, "local", root)
	if err != nil {
		t.Fatal(err)
	}
	run := orm.ConversationOrganizerRun{ID: "active-run", UserID: uid, Status: "running", SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`)}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	invoke := func(handler http.HandlerFunc, method string, body any, id string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/", bytes.NewReader(raw))
		req.Header.Set("X-User-Id", uid)
		req = mux.SetURLVars(req, map[string]string{"group_id": id})
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	created := invoke(CreateGroup, http.MethodPost, map[string]any{"kind": KindProject, "workspace_id": grant.WorkspaceID}, "")
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var payload struct {
		Group GroupDTO `json:"group"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Group.Kind != KindProject || payload.Group.Name != filepath.Base(root) {
		t.Fatalf("unexpected project: %+v", payload.Group)
	}
	renamed := invoke(UpdateGroup, http.MethodPatch, map[string]any{"name": "renamed"}, payload.Group.ID)
	if renamed.Code != 200 {
		t.Fatalf("rename: %d %s", renamed.Code, renamed.Body.String())
	}
	for _, forbidden := range []map[string]any{
		{"name": "renamed", "scope": "scope"},
		{"name": "renamed", "workspace_id": grant.WorkspaceID},
		{"name": "renamed", "kind": KindGroup},
	} {
		if got := invoke(UpdateGroup, http.MethodPatch, forbidden, payload.Group.ID); got.Code != 400 {
			t.Fatalf("accepted immutable field: %d %s", got.Code, got.Body.String())
		}
	}
	if got := invoke(CreateGroup, http.MethodPost, map[string]any{"kind": KindProject, "name": "missing directory"}, ""); got.Code != 400 {
		t.Fatalf("accepted missing directory: %d", got.Code)
	}
}

func TestConcurrentProjectCreationReusesIdentity(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	grant, err := registerProjectTestWorkspace(t.Context(), t, db.DB, "owner", "local", projectTestWorkspacePath(t))
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	for i := 0; i < cap(results); i++ {
		go func() {
			<-start
			var project orm.ConversationGroup
			err := UserTransaction(t.Context(), db.DB, "owner", func(tx *gorm.DB) error {
				var err error
				project, err = EnsureProject(t.Context(), tx, "owner", grant.WorkspaceID, "project", false)
				return err
			})
			results <- result{project.ID, err}
		}()
	}
	close(start)
	var projectID string
	for i := 0; i < cap(results); i++ {
		got := <-results
		if got.err != nil {
			t.Errorf("create: %v", got.err)
			continue
		}
		if projectID == "" {
			projectID = got.id
		}
		if got.id != projectID {
			t.Errorf("duplicate identities: %s and %s", projectID, got.id)
		}
	}
	var count int64
	if err := db.Model(&orm.ConversationGroup{}).Where("user_id=? AND kind=?", "owner", KindProject).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("project count=%d error=%v", count, err)
	}
}

func TestProjectNamesAreIndependentOfGroups(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	const uid = "names-owner"
	grant, err := localworkspace.Register(t.Context(), db.DB, uid, localworkspace.RegisterInput{DisplayName: "local", CanonicalPath: t.TempDir(), Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(handler http.HandlerFunc, method, id string, body any, status int) GroupDTO {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/", bytes.NewReader(raw))
		req.Header.Set("X-User-Id", uid)
		req = mux.SetURLVars(req, map[string]string{"group_id": id})
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != status {
			t.Fatalf("%s %s: %d %s", method, id, rec.Code, rec.Body.String())
		}
		var payload struct {
			Group GroupDTO `json:"group"`
		}
		if status < 300 {
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
		}
		return payload.Group
	}
	for _, task := range []bool{false, true} {
		invoke(CreateGroup, "POST", "", map[string]any{"name": "Shared", "is_task_conv": task}, 201)
	}
	project := invoke(CreateGroup, "POST", "", map[string]any{"kind": "project", "name": "shared", "workspace_id": grant.WorkspaceID}, 201)
	invoke(UpdateGroup, "PATCH", project.ID, map[string]any{"name": "SHARED"}, 200)
	invoke(UpdateGroup, "PATCH", project.ID, map[string]any{"name": "Project"}, 200)
	group := invoke(CreateGroup, "POST", "", map[string]any{"name": "Other"}, 201)
	invoke(UpdateGroup, "PATCH", group.ID, map[string]any{"name": "PROJECT"}, 200)
	invoke(CreateGroup, "POST", "", map[string]any{"name": "project"}, 409)
	invoke(UpdateGroup, "PATCH", group.ID, map[string]any{"name": "Other"}, 200)
	invoke(CreateGroup, "POST", "", map[string]any{"name": "project"}, 201)
	// Reusing a directory and restoring its project ignore names held by groups.
	err = UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error {
		reused, err := EnsureProject(t.Context(), tx, uid, grant.WorkspaceID, "", false)
		if err == nil && reused.ID != project.ID {
			t.Fatalf("reused wrong project: %s", reused.ID)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Model(&orm.ConversationGroup{}).Where("id=?", project.ID).Update("deleted_at", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Conversation{ID: "history", BaseModel: orm.BaseModel{CreateUserID: uid, DeletedAt: &now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationGroupMember{ConversationID: "history", UserID: uid, GroupID: project.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error { return RestoreProjects(tx, uid, []string{"history"}) }); err != nil {
		t.Fatal(err)
	}
	var restored orm.ConversationGroup
	if err := db.Where("id=?", project.ID).Take(&restored).Error; err != nil || restored.DeletedAt != nil {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	invoke(CreateGroup, "POST", "", map[string]any{"kind": "project", "name": "Another", "workspace_id": grant.WorkspaceID}, 409)
}

func TestDeletedProjectRecreationPreservesHistoryAndRestoreIsAtomic(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	const uid = "recreate-owner"
	grant, err := localworkspace.Register(t.Context(), db.DB, uid, localworkspace.RegisterInput{DisplayName: "folder", CanonicalPath: t.TempDir(), Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	var old, fresh orm.ConversationGroup
	err = UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error {
		var err error
		old, err = EnsureProject(t.Context(), tx, uid, grant.WorkspaceID, "old", false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, row := range []any{
		&orm.Conversation{ID: "historic", BaseModel: orm.BaseModel{CreateUserID: uid, DeletedAt: &now}},
		&orm.ConversationGroupMember{ConversationID: "historic", UserID: uid, GroupID: old.ID, Revision: 3},
		&orm.ConversationWorkspaceBinding{ConversationID: "historic", WorkspaceID: grant.WorkspaceID},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&old).Update("deleted_at", now).Error; err != nil {
		t.Fatal(err)
	}
	// Historical grants use a legacy identity and must never be rewritten.
	if err := db.Model(&orm.LocalWorkspace{}).Where("id=?", grant.WorkspaceID).Update("directory_identity", "legacy-identity").Error; err != nil {
		t.Fatal(err)
	}
	current, err := localworkspace.Register(t.Context(), db.DB, uid, localworkspace.RegisterInput{DisplayName: "folder", CanonicalPath: grant.Path, Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	err = UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error {
		var err error
		fresh, err = ensureProject(t.Context(), tx, uid, current.WorkspaceID, "new", false, true)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var reused orm.ConversationGroup
	err = UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error {
		var err error
		reused, err = EnsureProject(t.Context(), tx, uid, current.WorkspaceID, "ignored", false)
		return err
	})
	if err != nil || reused.ID != fresh.ID {
		t.Fatalf("automatic association did not reuse active project: %v", err)
	}
	if fresh.ID == old.ID || fresh.Name != "new" {
		t.Fatalf("reused historical project: %+v", fresh)
	}
	var count int64
	db.Model(&orm.ConversationGroupMember{}).Where("group_id=?", fresh.ID).Count(&count)
	if count != 0 {
		t.Fatal("new project inherited members")
	}
	err = UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error {
		_, err := ensureProject(t.Context(), tx, uid, current.WorkspaceID, "ignored", false, true)
		return err
	})
	if err == nil {
		t.Fatal("explicit duplicate accepted")
	}
	err = UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error {
		if err := tx.Model(&orm.Conversation{}).Where("id=?", "historic").Update("deleted_at", nil).Error; err != nil {
			return err
		}
		return RestoreProjects(tx, uid, []string{"historic"})
	})
	if err == nil {
		t.Fatal("restore ignored occupied directory")
	}
	var conv orm.Conversation
	db.Where("id=?", "historic").Take(&conv)
	if conv.DeletedAt == nil {
		t.Fatal("restore left a partial change")
	}
	if err := db.Model(&fresh).Update("deleted_at", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error { return RestoreProjects(tx, uid, []string{"historic"}) }); err != nil {
		t.Fatal(err)
	}
	var restored orm.ConversationGroup
	db.Where("id=?", old.ID).Take(&restored)
	if restored.DeletedAt != nil || *restored.WorkspaceID != grant.WorkspaceID {
		t.Fatal("restore changed historical binding")
	}
}

func TestConcurrentExplicitProjectCreation(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	const uid = "concurrent-project"
	grant, err := localworkspace.Register(t.Context(), db.DB, uid, localworkspace.RegisterInput{DisplayName: "folder", CanonicalPath: t.TempDir(), Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			results <- UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error {
				_, err := ensureProject(t.Context(), tx, uid, grant.WorkspaceID, "project", false, true)
				return err
			})
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			successes++
		} else if err.Error() == projectError("directory_in_use", 409).Error() {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", successes, conflicts)
	}
}

func projectTestWorkspacePath(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func registerProjectTestWorkspace(ctx context.Context, t *testing.T, db *gorm.DB, uid, name, root string) (localworkspace.PublicWorkspace, error) {
	t.Helper()
	return localworkspace.Register(ctx, db, uid, localworkspace.RegisterInput{
		DisplayName:   name,
		CanonicalPath: root,
		Source:        "local",
	})
}
