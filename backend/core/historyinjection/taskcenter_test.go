package historyinjection

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestReconcileInjectedWorkflowTasksCreatesMissingTaskAndIsIdempotent(t *testing.T) {
	db := newTaskCenterTestDB(t)
	now := time.Now().UTC().Add(-time.Minute)
	conversation := orm.Conversation{ID: "conversation", DisplayName: "样例：学术研究与论文写作",
		BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	session := orm.WorkflowSession{ID: "session", ConversationID: conversation.ID, CreateUserID: "owner",
		WorkflowID: "academic_research_pipeline", Status: "completed", CreatedAt: now, UpdatedAt: now.Add(30 * time.Second)}
	for _, value := range []any{&conversation, &session} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	manifest := Manifest{ConversationID: conversation.ID, SessionIDs: []string{session.ID}, Title: conversation.DisplayName}
	owner := TargetOwner{ID: "owner"}
	for range 2 {
		if err := reconcileInjectedWorkflowTasks(t.Context(), db, manifest, owner); err != nil {
			t.Fatal(err)
		}
	}

	var tasks []orm.TaskCenterTask
	if err := db.Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].WorkflowSessionID == nil || *tasks[0].WorkflowSessionID != session.ID ||
		tasks[0].TaskType != "workflow_run" || tasks[0].Status != "succeeded" || tasks[0].FinishedAt == nil {
		t.Fatalf("reconciled tasks = %#v", tasks)
	}
}

func TestReconcileInjectedWorkflowTasksLinksExistingBackgroundTask(t *testing.T) {
	db := newTaskCenterTestDB(t)
	now := time.Now().UTC().Add(-time.Minute)
	conversation := orm.Conversation{ID: "conversation", DisplayName: "样例：AI 图片生成",
		BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	session := orm.WorkflowSession{ID: "session", ConversationID: conversation.ID, CreateUserID: "owner",
		WorkflowID: "image-workflow", Status: "completed", CreatedAt: now.Add(10 * time.Second), UpdatedAt: now.Add(30 * time.Second)}
	background := orm.TaskCenterTask{ID: "background", UserID: "owner", ConversationID: conversation.ID,
		TaskType: "background_chat", Status: "succeeded", CreatedAt: now, UpdatedAt: now.Add(30 * time.Second)}
	for _, value := range []any{&conversation, &session, &background} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	manifest := Manifest{ConversationID: conversation.ID, SessionIDs: []string{session.ID}, Title: conversation.DisplayName}
	if err := reconcileInjectedWorkflowTasks(t.Context(), db, manifest, TargetOwner{ID: "owner"}); err != nil {
		t.Fatal(err)
	}

	var tasks []orm.TaskCenterTask
	if err := db.Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != background.ID || tasks[0].WorkflowSessionID == nil || *tasks[0].WorkflowSessionID != session.ID {
		t.Fatalf("reconciled tasks = %#v", tasks)
	}
}

func newTaskCenterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.Conversation{}, &orm.WorkflowSession{}, &orm.TaskCenterTask{}); err != nil {
		t.Fatal(err)
	}
	return db
}
