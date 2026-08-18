package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func recoveryTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db := orm.MigrateTestDB(t,
		&orm.Conversation{},
		&orm.ConversationArchiveFolder{},
		&orm.ChatHistory{},
		&orm.MultiAnswersChatHistory{},
		&orm.ConversationArtifact{},
		&orm.ConversationIdleEvent{},
		&orm.EpisodeMemory{},
		&orm.SkillV2Draft{},
		&orm.TaskCenterTask{},
	)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	return db
}

func seedRecoveryConversation(t *testing.T, db *orm.DB, id string, task bool) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: id, DisplayName: "Conversation " + id, ChannelID: "default", IsTaskConv: task,
		BaseModel: orm.BaseModel{
			CreateUserID: "u1", CreateUserName: "User 1", CreatedAt: now, UpdatedAt: now,
		},
	}).Error; err != nil {
		t.Fatalf("seed conversation: %v", err)
	}
}

func recoveryRequest(method, path string, body any, vars map[string]string) (*httptest.ResponseRecorder, *http.Request) {
	var payload bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&payload).Encode(body)
	}
	req := httptest.NewRequest(method, path, &payload)
	req.Header.Set("X-User-Id", "u1")
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	return httptest.NewRecorder(), req
}

func TestConversationArchiveFolderLifecycle(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "conv-archive", false)

	createRec, createReq := recoveryRequest(http.MethodPost, "/conversation-archive-folders", map[string]any{"name": "Product Design"}, nil)
	CreateConversationArchiveFolder(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create folder status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var folder orm.ConversationArchiveFolder
	if err := db.Where("user_id = ?", "u1").First(&folder).Error; err != nil {
		t.Fatalf("load folder: %v", err)
	}

	archiveRec, archiveReq := recoveryRequest(
		http.MethodPost, "/conversations/conv-archive:archive",
		map[string]any{"folder_id": folder.ID}, map[string]string{"name": "conv-archive:archive"},
	)
	ArchiveConversation(archiveRec, archiveReq)
	if archiveRec.Code != http.StatusOK {
		t.Fatalf("archive status=%d body=%s", archiveRec.Code, archiveRec.Body.String())
	}
	var conversation orm.Conversation
	if err := db.First(&conversation, "id = ?", "conv-archive").Error; err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if conversation.ArchivedAt == nil || conversation.ArchiveFolderID == nil || *conversation.ArchiveFolderID != folder.ID {
		t.Fatalf("unexpected archive state: %#v", conversation)
	}

	duplicateRec, duplicateReq := recoveryRequest(http.MethodPost, "/conversation-archive-folders", map[string]any{"name": "  product design  "}, nil)
	CreateConversationArchiveFolder(duplicateRec, duplicateReq)
	if duplicateRec.Code != http.StatusConflict {
		t.Fatalf("case-insensitive duplicate status=%d body=%s", duplicateRec.Code, duplicateRec.Body.String())
	}

	moveRec, moveReq := recoveryRequest(
		http.MethodPost, "/conversations/conv-archive:archive",
		map[string]any{"folder_id": "unfiled"}, map[string]string{"name": "conv-archive:archive"},
	)
	ArchiveConversation(moveRec, moveReq)
	if moveRec.Code != http.StatusOK {
		t.Fatalf("move to unfiled status=%d body=%s", moveRec.Code, moveRec.Body.String())
	}
	if err := db.First(&conversation, "id = ?", "conv-archive").Error; err != nil {
		t.Fatalf("reload conversation: %v", err)
	}
	if conversation.ArchiveFolderID != nil || conversation.ArchivedAt == nil {
		t.Fatalf("expected archived unfiled conversation, got %#v", conversation)
	}
}

func TestConversationArchiveFolderValidationAndIsolation(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "conv-isolation", false)

	for name, body := range map[string]string{
		"blank":    "   ",
		"too-long": strings.Repeat("文", 31),
	} {
		t.Run(name, func(t *testing.T) {
			rec, req := recoveryRequest(http.MethodPost, "/conversation-archive-folders", map[string]any{"name": body}, nil)
			CreateConversationArchiveFolder(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	otherRec, otherReq := recoveryRequest(http.MethodPost, "/conversation-archive-folders", map[string]any{"name": "Private"}, nil)
	otherReq.Header.Set("X-User-Id", "u2")
	CreateConversationArchiveFolder(otherRec, otherReq)
	if otherRec.Code != http.StatusCreated {
		t.Fatalf("other user create status=%d body=%s", otherRec.Code, otherRec.Body.String())
	}
	var otherFolder orm.ConversationArchiveFolder
	if err := db.Where("user_id = ?", "u2").First(&otherFolder).Error; err != nil {
		t.Fatalf("load other folder: %v", err)
	}

	archiveRec, archiveReq := recoveryRequest(
		http.MethodPost,
		"/conversations/conv-isolation:archive",
		map[string]any{"folder_id": otherFolder.ID},
		map[string]string{"name": "conv-isolation:archive"},
	)
	ArchiveConversation(archiveRec, archiveReq)
	if archiveRec.Code != http.StatusNotFound {
		t.Fatalf("cross-user archive status=%d body=%s", archiveRec.Code, archiveRec.Body.String())
	}
}

func TestConversationTrashRestoreAndPurgeLinksTaskCenterLifecycle(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "conv-task", true)
	now := time.Now().UTC()
	if err := db.Create(&orm.TaskCenterTask{
		ID: "task-1", UserID: "u1", ConversationID: "conv-task", TaskType: "background_chat",
		Status: "running", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed task: %v", err)
	}

	trashRec, trashReq := recoveryRequest(http.MethodDelete, "/conversations/conv-task", nil, map[string]string{"name": "conv-task"})
	DeleteConversation(trashRec, trashReq)
	if trashRec.Code != http.StatusOK {
		t.Fatalf("trash status=%d body=%s", trashRec.Code, trashRec.Body.String())
	}
	var task orm.TaskCenterTask
	if err := db.First(&task, "id = ?", "task-1").Error; err != nil || task.ArchivedAt == nil || task.ArchivedReason != "conversation_trash" || task.Status != "canceled" {
		t.Fatalf("conversation trash did not archive and cancel task: task=%#v err=%v", task, err)
	}

	restoreRec, restoreReq := recoveryRequest(http.MethodPost, "/conversations/conv-task:restore", nil, map[string]string{"name": "conv-task:restore"})
	RestoreConversation(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", restoreRec.Code, restoreRec.Body.String())
	}
	var conversation orm.Conversation
	if err := db.First(&conversation, "id = ?", "conv-task").Error; err != nil || conversation.DeletedAt != nil || conversation.ArchivedAt != nil {
		t.Fatalf("unexpected restored conversation: %#v err=%v", conversation, err)
	}
	var restoredTask orm.TaskCenterTask
	if err := db.First(&restoredTask, "id = ?", "task-1").Error; err != nil || restoredTask.ArchivedAt != nil || restoredTask.ArchivedReason != "" || restoredTask.Status != "canceled" {
		t.Fatalf("restore did not revive canceled task history: task=%#v err=%v", restoredTask, err)
	}

	archiveRec, archiveReq := recoveryRequest(http.MethodPost, "/conversations/conv-task:archive", nil, map[string]string{"name": "conv-task:archive"})
	ArchiveConversation(archiveRec, archiveReq)
	if archiveRec.Code != http.StatusOK {
		t.Fatalf("archive before trash status=%d body=%s", archiveRec.Code, archiveRec.Body.String())
	}
	trashRec, trashReq = recoveryRequest(http.MethodDelete, "/conversations/conv-task", nil, map[string]string{"name": "conv-task"})
	DeleteConversation(trashRec, trashReq)
	if err := db.First(&task, "id = ?", "task-1").Error; err != nil || task.ArchivedReason != "conversation_trash" {
		t.Fatalf("archived task did not transition to trash reason: task=%#v err=%v", task, err)
	}
	restoreRec, restoreReq = recoveryRequest(http.MethodPost, "/conversations/conv-task:restore", nil, map[string]string{"name": "conv-task:restore"})
	RestoreConversation(restoreRec, restoreReq)
	if err := db.First(&restoredTask, "id = ?", "task-1").Error; err != nil || restoredTask.ArchivedAt != nil {
		t.Fatalf("restore after archive-to-trash did not revive task history: task=%#v err=%v", restoredTask, err)
	}

	trashRec, trashReq = recoveryRequest(http.MethodDelete, "/conversations/conv-task", nil, map[string]string{"name": "conv-task"})
	DeleteConversation(trashRec, trashReq)
	purgeRec, purgeReq := recoveryRequest(http.MethodDelete, "/conversations/conv-task:purge", nil, map[string]string{"name": "conv-task:purge"})
	PurgeConversation(purgeRec, purgeReq)
	if purgeRec.Code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", purgeRec.Code, purgeRec.Body.String())
	}
	var conversationCount, taskCount int64
	db.Model(&orm.Conversation{}).Where("id = ?", "conv-task").Count(&conversationCount)
	db.Model(&orm.TaskCenterTask{}).Where("id = ?", "task-1").Count(&taskCount)
	if conversationCount != 0 || taskCount != 1 {
		t.Fatalf("counts after purge: conversation=%d task=%d", conversationCount, taskCount)
	}
	if err := db.First(&task, "id = ?", "task-1").Error; err != nil || task.ArchivedAt == nil || task.ArchivedReason != "conversation_purged" {
		t.Fatalf("purge did not preserve hidden task audit: task=%#v err=%v", task, err)
	}
}

func TestEnsureConversationUnarchivesAndRejectsTrash(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "conv-ensure", false)
	now := time.Now().UTC()
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "conv-ensure").
		Updates(map[string]any{"archived_at": now}).Error; err != nil {
		t.Fatalf("archive conversation: %v", err)
	}
	conversation, _, err := ensureConversation(context.Background(), db.DB, "conv-ensure", "", nil, nil, "u1", "User 1", nil)
	if err != nil || conversation.ArchivedAt != nil {
		t.Fatalf("ensure archived conversation: conversation=%#v err=%v", conversation, err)
	}

	if err := db.Model(&orm.Conversation{}).Where("id = ?", "conv-ensure").
		Updates(map[string]any{"deleted_at": now}).Error; err != nil {
		t.Fatalf("trash conversation: %v", err)
	}
	if _, _, err := ensureConversation(context.Background(), db.DB, "conv-ensure", "", nil, nil, "u1", "User 1", nil); !errors.Is(err, errConversationInTrash) {
		t.Fatalf("ensure trashed conversation error=%v", err)
	}
}
