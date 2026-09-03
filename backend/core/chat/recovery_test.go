package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/state"
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
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "recovery-state.db"))
	if err != nil {
		t.Fatalf("open recovery state store: %v", err)
	}
	store.Init(db.DB, nil, stateStore)
	t.Cleanup(func() {
		store.Init(nil, nil, nil)
		_ = stateStore.Close()
	})
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

func seedRecoveryChildConversation(t *testing.T, db *orm.DB, id, parentID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{
		ID: id, DisplayName: "Conversation " + id, ChannelID: "default",
		ParentConversationID: &parentID, RelationType: conversationRelationSidechat,
		BaseModel: orm.BaseModel{
			CreateUserID: "u1", CreateUserName: "User 1", CreatedAt: now, UpdatedAt: now,
		},
	}).Error; err != nil {
		t.Fatalf("seed child conversation: %v", err)
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

func TestConversationArchiveFolderRenameAndDeleteLifecycle(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "conv-folder-lifecycle", false)

	createFolder := func(name string) orm.ConversationArchiveFolder {
		t.Helper()
		rec, req := recoveryRequest(http.MethodPost, "/conversation-archive-folders", map[string]any{"name": name}, nil)
		CreateConversationArchiveFolder(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create folder %q status=%d body=%s", name, rec.Code, rec.Body.String())
		}
		var folder orm.ConversationArchiveFolder
		if err := db.Where("user_id = ? AND normalized_name = ?", "u1", strings.ToLower(name)).First(&folder).Error; err != nil {
			t.Fatalf("load folder %q: %v", name, err)
		}
		return folder
	}

	source := createFolder("Source")
	target := createFolder("Target")

	renameRec, renameReq := recoveryRequest(
		http.MethodPatch,
		"/conversation-archive-folders/"+source.ID,
		map[string]any{"name": "  Renamed Source  "},
		map[string]string{"folder_id": source.ID},
	)
	UpdateConversationArchiveFolder(renameRec, renameReq)
	if renameRec.Code != http.StatusOK {
		t.Fatalf("rename folder status=%d body=%s", renameRec.Code, renameRec.Body.String())
	}
	if err := db.First(&source, "id = ?", source.ID).Error; err != nil {
		t.Fatalf("reload renamed folder: %v", err)
	}
	if source.Name != "Renamed Source" || source.NormalizedName != "renamed source" {
		t.Fatalf("unexpected renamed folder: %#v", source)
	}

	duplicateRec, duplicateReq := recoveryRequest(
		http.MethodPatch,
		"/conversation-archive-folders/"+source.ID,
		map[string]any{"name": "target"},
		map[string]string{"folder_id": source.ID},
	)
	UpdateConversationArchiveFolder(duplicateRec, duplicateReq)
	if duplicateRec.Code != http.StatusConflict {
		t.Fatalf("duplicate rename status=%d body=%s", duplicateRec.Code, duplicateRec.Body.String())
	}

	archiveRec, archiveReq := recoveryRequest(
		http.MethodPost,
		"/conversations/conv-folder-lifecycle:archive",
		map[string]any{"folder_id": source.ID},
		map[string]string{"name": "conv-folder-lifecycle:archive"},
	)
	ArchiveConversation(archiveRec, archiveReq)
	if archiveRec.Code != http.StatusOK {
		t.Fatalf("archive into source status=%d body=%s", archiveRec.Code, archiveRec.Body.String())
	}

	deleteRec, deleteReq := recoveryRequest(
		http.MethodDelete,
		"/conversation-archive-folders/"+source.ID,
		nil,
		map[string]string{"folder_id": source.ID},
	)
	DeleteConversationArchiveFolder(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusConflict {
		t.Fatalf("delete non-empty folder without target status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}

	selfRec, selfReq := recoveryRequest(
		http.MethodDelete,
		"/conversation-archive-folders/"+source.ID+"?move_to_folder_id="+source.ID,
		nil,
		map[string]string{"folder_id": source.ID},
	)
	DeleteConversationArchiveFolder(selfRec, selfReq)
	if selfRec.Code != http.StatusBadRequest {
		t.Fatalf("delete folder into itself status=%d body=%s", selfRec.Code, selfRec.Body.String())
	}

	deleteRec, deleteReq = recoveryRequest(
		http.MethodDelete,
		"/conversation-archive-folders/"+source.ID+"?move_to_folder_id="+target.ID,
		nil,
		map[string]string{"folder_id": source.ID},
	)
	DeleteConversationArchiveFolder(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete folder with target status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	var sourceCount int64
	db.Model(&orm.ConversationArchiveFolder{}).Where("id = ?", source.ID).Count(&sourceCount)
	var conversation orm.Conversation
	if err := db.First(&conversation, "id = ?", "conv-folder-lifecycle").Error; err != nil {
		t.Fatalf("reload moved conversation: %v", err)
	}
	if sourceCount != 0 || conversation.ArchiveFolderID == nil || *conversation.ArchiveFolderID != target.ID || conversation.ArchivedAt == nil {
		t.Fatalf("unexpected delete/move result: source_count=%d conversation=%#v", sourceCount, conversation)
	}

	empty := createFolder("Empty")
	emptyRec, emptyReq := recoveryRequest(
		http.MethodDelete,
		"/conversation-archive-folders/"+empty.ID,
		nil,
		map[string]string{"folder_id": empty.ID},
	)
	DeleteConversationArchiveFolder(emptyRec, emptyReq)
	if emptyRec.Code != http.StatusOK {
		t.Fatalf("delete empty folder status=%d body=%s", emptyRec.Code, emptyRec.Body.String())
	}
}

func TestConversationArchiveFolderDeleteToUnfiledAndOwnership(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "conv-folder-unfiled", false)

	createRec, createReq := recoveryRequest(http.MethodPost, "/conversation-archive-folders", map[string]any{"name": "Source"}, nil)
	CreateConversationArchiveFolder(createRec, createReq)
	var source orm.ConversationArchiveFolder
	if err := db.Where("user_id = ?", "u1").First(&source).Error; err != nil {
		t.Fatalf("load source folder: %v", err)
	}

	archiveRec, archiveReq := recoveryRequest(
		http.MethodPost,
		"/conversations/conv-folder-unfiled:archive",
		map[string]any{"folder_id": source.ID},
		map[string]string{"name": "conv-folder-unfiled:archive"},
	)
	ArchiveConversation(archiveRec, archiveReq)

	otherRec, otherReq := recoveryRequest(http.MethodPost, "/conversation-archive-folders", map[string]any{"name": "Other"}, nil)
	otherReq.Header.Set("X-User-Id", "u2")
	CreateConversationArchiveFolder(otherRec, otherReq)
	var otherFolder orm.ConversationArchiveFolder
	if err := db.Where("user_id = ?", "u2").First(&otherFolder).Error; err != nil {
		t.Fatalf("load other user's folder: %v", err)
	}

	crossUserRec, crossUserReq := recoveryRequest(
		http.MethodDelete,
		"/conversation-archive-folders/"+source.ID+"?move_to_folder_id="+otherFolder.ID,
		nil,
		map[string]string{"folder_id": source.ID},
	)
	DeleteConversationArchiveFolder(crossUserRec, crossUserReq)
	if crossUserRec.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete target status=%d body=%s", crossUserRec.Code, crossUserRec.Body.String())
	}

	deleteRec, deleteReq := recoveryRequest(
		http.MethodDelete,
		"/conversation-archive-folders/"+source.ID+"?move_to_folder_id=unfiled",
		nil,
		map[string]string{"folder_id": source.ID},
	)
	DeleteConversationArchiveFolder(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete to unfiled status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	var conversation orm.Conversation
	if err := db.First(&conversation, "id = ?", "conv-folder-unfiled").Error; err != nil {
		t.Fatalf("reload unfiled conversation: %v", err)
	}
	if conversation.ArchiveFolderID != nil || conversation.ArchivedAt == nil {
		t.Fatalf("expected archived unfiled conversation, got %#v", conversation)
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

func TestRestoreChildConversationRequiresActiveParent(t *testing.T) {
	t.Run("active parent permits child-only restore", func(t *testing.T) {
		db := recoveryTestDB(t)
		seedRecoveryConversation(t, db, "parent-active", false)
		seedRecoveryChildConversation(t, db, "child-active", "parent-active")

		trashRec, trashReq := recoveryRequest(http.MethodDelete, "/conversations/child-active", nil, map[string]string{"name": "child-active"})
		DeleteConversation(trashRec, trashReq)
		if trashRec.Code != http.StatusOK {
			t.Fatalf("trash child status=%d body=%s", trashRec.Code, trashRec.Body.String())
		}

		restoreRec, restoreReq := recoveryRequest(http.MethodPost, "/conversations/child-active:restore", nil, map[string]string{"name": "child-active:restore"})
		RestoreConversation(restoreRec, restoreReq)
		if restoreRec.Code != http.StatusOK {
			t.Fatalf("restore child status=%d body=%s", restoreRec.Code, restoreRec.Body.String())
		}

		for _, id := range []string{"parent-active", "child-active"} {
			var conversation orm.Conversation
			if err := db.First(&conversation, "id = ?", id).Error; err != nil || conversation.DeletedAt != nil || conversation.ArchivedAt != nil {
				t.Fatalf("conversation %s should be active: %#v err=%v", id, conversation, err)
			}
		}
	})

	t.Run("trashed parent rejects child restore and root restore remains cascading", func(t *testing.T) {
		db := recoveryTestDB(t)
		seedRecoveryConversation(t, db, "parent-trashed", false)
		seedRecoveryChildConversation(t, db, "child-trashed", "parent-trashed")

		trashRec, trashReq := recoveryRequest(http.MethodDelete, "/conversations/parent-trashed", nil, map[string]string{"name": "parent-trashed"})
		DeleteConversation(trashRec, trashReq)
		if trashRec.Code != http.StatusOK {
			t.Fatalf("trash family status=%d body=%s", trashRec.Code, trashRec.Body.String())
		}

		childRestoreRec, childRestoreReq := recoveryRequest(http.MethodPost, "/conversations/child-trashed:restore", nil, map[string]string{"name": "child-trashed:restore"})
		RestoreConversation(childRestoreRec, childRestoreReq)
		if childRestoreRec.Code != http.StatusConflict {
			t.Fatalf("restore child with trashed parent status=%d body=%s", childRestoreRec.Code, childRestoreRec.Body.String())
		}

		rootRestoreRec, rootRestoreReq := recoveryRequest(http.MethodPost, "/conversations/parent-trashed:restore", nil, map[string]string{"name": "parent-trashed:restore"})
		RestoreConversation(rootRestoreRec, rootRestoreReq)
		if rootRestoreRec.Code != http.StatusOK {
			t.Fatalf("restore family status=%d body=%s", rootRestoreRec.Code, rootRestoreRec.Body.String())
		}
		for _, id := range []string{"parent-trashed", "child-trashed"} {
			var conversation orm.Conversation
			if err := db.First(&conversation, "id = ?", id).Error; err != nil || conversation.DeletedAt != nil {
				t.Fatalf("conversation %s should be restored with family: %#v err=%v", id, conversation, err)
			}
		}
	})

	t.Run("root restore leaves a previously trashed child in trash", func(t *testing.T) {
		db := recoveryTestDB(t)
		seedRecoveryConversation(t, db, "parent-independent-child", false)
		seedRecoveryChildConversation(t, db, "child-independent-trash", "parent-independent-child")

		trashChildRec, trashChildReq := recoveryRequest(http.MethodDelete, "/conversations/child-independent-trash", nil, map[string]string{"name": "child-independent-trash"})
		DeleteConversation(trashChildRec, trashChildReq)
		if trashChildRec.Code != http.StatusOK {
			t.Fatalf("trash child status=%d body=%s", trashChildRec.Code, trashChildRec.Body.String())
		}
		childDeletedAt := time.Now().UTC().Add(-time.Hour)
		if err := db.Model(&orm.Conversation{}).Where("id = ?", "child-independent-trash").
			Update("deleted_at", childDeletedAt).Error; err != nil {
			t.Fatalf("stabilize child trash timestamp: %v", err)
		}

		trashParentRec, trashParentReq := recoveryRequest(http.MethodDelete, "/conversations/parent-independent-child", nil, map[string]string{"name": "parent-independent-child"})
		DeleteConversation(trashParentRec, trashParentReq)
		if trashParentRec.Code != http.StatusOK {
			t.Fatalf("trash parent status=%d body=%s", trashParentRec.Code, trashParentRec.Body.String())
		}
		restoreParentRec, restoreParentReq := recoveryRequest(http.MethodPost, "/conversations/parent-independent-child:restore", nil, map[string]string{"name": "parent-independent-child:restore"})
		RestoreConversation(restoreParentRec, restoreParentReq)
		if restoreParentRec.Code != http.StatusOK {
			t.Fatalf("restore parent status=%d body=%s", restoreParentRec.Code, restoreParentRec.Body.String())
		}

		var parent, child orm.Conversation
		if err := db.Where("id = ?", "parent-independent-child").Take(&parent).Error; err != nil || parent.DeletedAt != nil {
			t.Fatalf("parent was not restored: %#v err=%v", parent, err)
		}
		if err := db.Where("id = ?", "child-independent-trash").Take(&child).Error; err != nil || child.DeletedAt == nil {
			t.Fatalf("independently trashed child was restored: %#v err=%v", child, err)
		}
	})

	t.Run("archived parent rejects child restore", func(t *testing.T) {
		db := recoveryTestDB(t)
		seedRecoveryConversation(t, db, "parent-archived", false)
		seedRecoveryChildConversation(t, db, "child-archived", "parent-archived")

		archiveRec, archiveReq := recoveryRequest(http.MethodPost, "/conversations/parent-archived:archive", nil, map[string]string{"name": "parent-archived:archive"})
		ArchiveConversation(archiveRec, archiveReq)
		if archiveRec.Code != http.StatusOK {
			t.Fatalf("archive family status=%d body=%s", archiveRec.Code, archiveRec.Body.String())
		}
		trashRec, trashReq := recoveryRequest(http.MethodDelete, "/conversations/child-archived", nil, map[string]string{"name": "child-archived"})
		DeleteConversation(trashRec, trashReq)
		if trashRec.Code != http.StatusOK {
			t.Fatalf("trash archived child status=%d body=%s", trashRec.Code, trashRec.Body.String())
		}

		restoreRec, restoreReq := recoveryRequest(http.MethodPost, "/conversations/child-archived:restore", nil, map[string]string{"name": "child-archived:restore"})
		RestoreConversation(restoreRec, restoreReq)
		if restoreRec.Code != http.StatusConflict {
			t.Fatalf("restore child with archived parent status=%d body=%s", restoreRec.Code, restoreRec.Body.String())
		}
		var child orm.Conversation
		if err := db.First(&child, "id = ?", "child-archived").Error; err != nil || child.DeletedAt == nil {
			t.Fatalf("child should remain trashed: %#v err=%v", child, err)
		}
	})
}

func TestEnsureConversationUnarchivesAndRejectsTrash(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "conv-ensure", false)
	now := time.Now().UTC()
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "conv-ensure").
		Updates(map[string]any{"archived_at": now}).Error; err != nil {
		t.Fatalf("archive conversation: %v", err)
	}
	conversation, _, err := ensureConversation(context.Background(), db.DB, "conv-ensure", "", nil, nil, "u1", "User 1", false, "", nil, nil)
	if err != nil || conversation.ArchivedAt != nil {
		t.Fatalf("ensure archived conversation: conversation=%#v err=%v", conversation, err)
	}

	if err := db.Model(&orm.Conversation{}).Where("id = ?", "conv-ensure").
		Updates(map[string]any{"deleted_at": now}).Error; err != nil {
		t.Fatalf("trash conversation: %v", err)
	}
	if _, _, err := ensureConversation(context.Background(), db.DB, "conv-ensure", "", nil, nil, "u1", "User 1", false, "", nil, nil); !errors.Is(err, errConversationInTrash) {
		t.Fatalf("ensure trashed conversation error=%v", err)
	}
}
