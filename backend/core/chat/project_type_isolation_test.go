package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/localworkspace"
	"lazymind/core/store"
)

func TestProjectTypesHaveIndependentLifecycle(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	const uid = "project-types"
	grant, err := localworkspace.Register(t.Context(), db.DB, uid, localworkspace.RegisterInput{DisplayName: "shared", CanonicalPath: t.TempDir(), Source: "local"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(id string, task bool, raw map[string]any) error {
		_, _, err := ensureConversationWithWorkspace(t.Context(), db.DB, id, id, nil, nil, uid, uid, task, "", nil, nil, raw)
		return err
	}
	projects := map[bool]string{}
	for _, task := range []bool{false, true} {
		id := "normal"
		if task {
			id = "task"
		}
		if err := create(id, task, map[string]any{"workspace_id": grant.WorkspaceID, "project_name": "Same name"}); err != nil {
			t.Fatal(err)
		}
		var m orm.ConversationGroupMember
		if err := db.Where("conversation_id=?", id).Take(&m).Error; err != nil {
			t.Fatal(err)
		}
		projects[task] = m.GroupID
		if err := create(id+"-second", task, map[string]any{"group_id": m.GroupID}); err != nil {
			t.Fatal(err)
		}
		// Direct cross-type requests must fail atomically.
		if err := create(id+"-wrong", !task, map[string]any{"group_id": m.GroupID}); err == nil {
			t.Fatal("cross-type create accepted")
		}
		var count int64
		db.Model(&orm.Conversation{}).Where("id=?", id+"-wrong").Count(&count)
		if count != 0 {
			t.Fatal("failed creation left data")
		}
		// The lower-level membership path must enforce the same boundary.
		err := conversationgroup.UserTransaction(t.Context(), db.DB, uid, func(tx *gorm.DB) error {
			if err := tx.Create(&orm.Conversation{ID: id + "-inherit-wrong", IsTaskConv: !task, BaseModel: orm.BaseModel{CreateUserID: uid}}).Error; err != nil {
				return err
			}
			return conversationgroup.InheritProject(t.Context(), tx, uid, id, id+"-inherit-wrong")
		})
		if err == nil {
			t.Fatal("cross-type inheritance accepted")
		}
	}
	if err := create("normal", true, nil); err == nil {
		t.Fatal("existing ordinary conversation was converted into a task")
	}
	if projects[false] == projects[true] {
		t.Fatal("shared project identity")
	}
	invoke := func(handler http.HandlerFunc, method, path, body string, vars map[string]string) *httptest.ResponseRecorder {
		r := sidechatRequest(method, path, uid, body, vars)
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	for _, task := range []bool{false, true} {
		filter := "false"
		if task {
			filter = "true"
		}
		w := invoke(conversationgroup.ListGroups, "GET", "/?is_task_conv="+filter, "", nil)
		var body struct {
			Groups []conversationgroup.GroupDTO `json:"groups"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(body.Groups) != 1 || body.Groups[0].ID != projects[task] || body.Groups[0].MemberCount != 2 {
			t.Fatalf("typed list: %d %s", w.Code, w.Body.String())
		}
	}
	w := invoke(ArchiveConversation, "POST", "/", "", map[string]string{"name": "task", "conversation_id": "task"})
	if w.Code != 200 {
		t.Fatalf("archive: %d %s", w.Code, w.Body.String())
	}
	var normal orm.Conversation
	if err := db.Where("id=?", "normal").Take(&normal).Error; err != nil || normal.ArchivedAt != nil {
		t.Fatalf("normal archived: %+v %v", normal, err)
	}
	w = invoke(UnarchiveConversation, "POST", "/", "", map[string]string{"name": "task", "conversation_id": "task"})
	if w.Code != 200 {
		t.Fatalf("unarchive: %d %s", w.Code, w.Body.String())
	}
	w = invoke(conversationgroup.UpdateGroup, "PATCH", "/", `{"name":"Task renamed"}`, map[string]string{"group_id": projects[true]})
	if w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	w = invoke(conversationgroup.UpdateGroupPlacement, "PATCH", "/", `{"before_group_id":"`+projects[false]+`"}`, map[string]string{"group_id": projects[true]})
	if w.Code != 404 {
		t.Fatalf("cross-type order: %d %s", w.Code, w.Body.String())
	}
	w = invoke(DeleteConversationGroup, "DELETE", "/", "", map[string]string{"group_id": projects[true]})
	if w.Code != 200 {
		t.Fatalf("trash: %d %s", w.Code, w.Body.String())
	}
	var count int64
	db.Model(&orm.Conversation{}).Where("id IN ? AND deleted_at IS NULL", []string{"normal", "normal-second"}).Count(&count)
	if count != 2 {
		t.Fatal("task project deletion affected ordinary conversations")
	}
	var normalProject orm.ConversationGroup
	if err := db.Where("id=?", projects[false]).Take(&normalProject).Error; err != nil || normalProject.Name != "Same name" || normalProject.DeletedAt != nil {
		t.Fatalf("ordinary project changed: %+v %v", normalProject, err)
	}
	w = invoke(RestoreConversation, "POST", "/", "", map[string]string{"name": "task", "conversation_id": "task"})
	if w.Code != 200 {
		t.Fatalf("restore beside ordinary project: %d %s", w.Code, w.Body.String())
	}
}

func TestSameNameProjectsUseDirectoryIdentity(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	const uid = "same-name-projects"
	root := t.TempDir()
	projectIDs := map[string]bool{}
	for _, parent := range []string{"work", "personal"} {
		path := filepath.Join(root, parent, "foo")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		grant, err := localworkspace.Register(t.Context(), db.DB, uid, localworkspace.RegisterInput{DisplayName: "foo", CanonicalPath: path, Source: "local"})
		if err != nil {
			t.Fatal(err)
		}
		var projectID string
		for _, suffix := range []string{"first", "second"} {
			id := parent + suffix
			_, _, err := ensureConversationWithWorkspace(t.Context(), db.DB, id, id, nil, nil, uid, uid, false, "", nil, nil, map[string]any{"workspace_id": grant.WorkspaceID})
			if err != nil {
				t.Fatal(err)
			}
			var member orm.ConversationGroupMember
			if err := db.Where("conversation_id=?", id).Take(&member).Error; err != nil {
				t.Fatal(err)
			}
			if projectID != "" && projectID != member.GroupID {
				t.Fatal("same path created different projects")
			}
			projectID = member.GroupID
		}
		var project orm.ConversationGroup
		if err := db.Where("id=?", projectID).Take(&project).Error; err != nil {
			t.Fatal(err)
		}
		if project.Name != "foo" || project.ProjectPath == nil || *project.ProjectPath != grant.Path {
			t.Fatalf("project identity: %+v", project)
		}
		if projectIDs[projectID] {
			t.Fatal("different directories shared a project")
		}
		projectIDs[projectID] = true
	}
}
