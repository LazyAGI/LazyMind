package chat

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

type archiveReceiptForTest struct {
	ConversationID string    `json:"conversation_id"`
	NewlyArchived  bool      `json:"newly_archived"`
	ArchivedAt     time.Time `json:"archived_at"`
}

func TestBatchArchiveReceiptAndConditionalUndo(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "root", true)
	seedRecoveryChildConversation(t, db, "child", "root")
	pin := time.Date(2026, 10, 19, 7, 0, 0, 123456000, time.UTC)
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "root").Update("pinned_at", pin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.TaskCenterTask{ID: "task", UserID: "u1", ConversationID: "root", TaskType: "background_chat", Status: "running"}).Error; err != nil {
		t.Fatal(err)
	}
	w, r := recoveryRequest(http.MethodPost, "/", map[string]any{"only_if_active": true}, map[string]string{"conversation_id": "root"})
	ArchiveConversation(w, r)
	var receipt archiveReceiptForTest
	if json.Unmarshal(w.Body.Bytes(), &receipt) != nil || w.Code != 200 || !receipt.NewlyArchived || receipt.ArchivedAt.IsZero() || receipt.ConversationID != "root" {
		t.Fatalf("invalid receipt: %d %s", w.Code, w.Body.String())
	}
	var saved orm.Conversation
	if err := db.First(&saved, "id = ?", "root").Error; err != nil || saved.ArchivedAt == nil || !saved.ArchivedAt.Equal(receipt.ArchivedAt) {
		t.Fatalf("receipt differs from persisted value: %v %#v", err, saved)
	}
	// A second batch must not count or overwrite the first archive.
	w, r = recoveryRequest(http.MethodPost, "/", map[string]any{"only_if_active": true}, map[string]string{"conversation_id": "root"})
	ArchiveConversation(w, r)
	var repeated archiveReceiptForTest
	if json.Unmarshal(w.Body.Bytes(), &repeated) != nil || w.Code != 200 || repeated.NewlyArchived || !receipt.ArchivedAt.Equal(repeated.ArchivedAt) {
		t.Fatalf("batch overwrote an archive: %d %s", w.Code, w.Body.String())
	}
	for i := 0; i < 2; i++ {
		w, r = recoveryRequest(http.MethodPost, "/", map[string]any{"expected_archived_at": receipt.ArchivedAt.Format(time.RFC3339Nano)}, map[string]string{"conversation_id": "root"})
		UnarchiveConversation(w, r)
		if w.Code != 200 {
			t.Fatalf("undo attempt %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	for _, id := range []string{"root", "child"} {
		var restored orm.Conversation
		if err := db.First(&restored, "id = ?", id).Error; err != nil || restored.ArchivedAt != nil || restored.ArchiveFolderID != nil {
			t.Fatalf("not restored: %s %v %#v", id, err, restored)
		}
		if id == "root" && (restored.PinnedAt == nil || !restored.PinnedAt.Equal(pin)) {
			t.Fatal("undo changed pinning")
		}
	}
	var task orm.TaskCenterTask
	if err := db.First(&task, "id = ?", "task").Error; err != nil || task.ArchivedAt != nil || task.Status != "canceled" {
		t.Fatalf("task restore resumed execution or stayed hidden: %v %#v", err, task)
	}
}

func TestArchiveUndoRejectsNewerArchiveAndInvalidVersion(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "root", false)
	old := time.Date(2026, 10, 19, 8, 0, 0, 123456000, time.UTC)
	newer := old.Add(time.Second)
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "root").Update("archived_at", newer).Error; err != nil {
		t.Fatal(err)
	}
	w, r := recoveryRequest(http.MethodPost, "/", map[string]any{"expected_archived_at": old.Format(time.RFC3339Nano)}, map[string]string{"conversation_id": "root"})
	UnarchiveConversation(w, r)
	var result struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 409 || result.Code != 2004815 {
		t.Fatalf("stale undo accepted: %d %s", w.Code, w.Body.String())
	}
	for _, value := range []any{"invalid", "", 123, true} {
		w, r = recoveryRequest(http.MethodPost, "/", map[string]any{"expected_archived_at": value}, map[string]string{"conversation_id": "root"})
		UnarchiveConversation(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid version accepted: %#v %d", value, w.Code)
		}
	}
	for _, owner := range []string{"other-user", "u1"} {
		if owner == "u1" {
			if err := db.Model(&orm.Conversation{}).Where("id = ?", "root").Update("deleted_at", newer).Error; err != nil {
				t.Fatal(err)
			}
		}
		w, r = recoveryRequest(http.MethodPost, "/", map[string]any{"expected_archived_at": newer.Format(time.RFC3339Nano)}, map[string]string{"conversation_id": "root"})
		r.Header.Set("X-User-Id", owner)
		UnarchiveConversation(w, r)
		if w.Code != 404 {
			t.Fatalf("restored trash or foreign conversation: %s %d", owner, w.Code)
		}
	}
}

func TestConcurrentBatchArchiveCountsOnce(t *testing.T) {
	db := recoveryTestDB(t)
	seedRecoveryConversation(t, db, "root", false)
	start := make(chan struct{})
	type result struct {
		status  int
		body    string
		receipt archiveReceiptForTest
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			w, r := recoveryRequest(http.MethodPost, "/", map[string]any{"only_if_active": true}, map[string]string{"conversation_id": "root"})
			ArchiveConversation(w, r)
			var receipt archiveReceiptForTest
			_ = json.Unmarshal(w.Body.Bytes(), &receipt)
			results <- result{w.Code, w.Body.String(), receipt}
		}()
	}
	close(start)
	count := 0
	var at time.Time
	for i := 0; i < 2; i++ {
		r := <-results
		if r.status != 200 || r.receipt.ArchivedAt.IsZero() {
			t.Fatalf("archive: %d %s", r.status, r.body)
		}
		if r.receipt.NewlyArchived {
			count++
		}
		if !at.IsZero() && !at.Equal(r.receipt.ArchivedAt) {
			t.Fatal("concurrent batch changed the archive version")
		}
		at = r.receipt.ArchivedAt
	}
	if count != 1 {
		t.Fatalf("counted one conversation %d times", count)
	}
}

func TestArchiveUndoDoesNotOverrideLaterFolderMove(t *testing.T) {
	for _, deleteFolder := range []bool{false, true} {
		t.Run(map[bool]string{false: "move_conversation", true: "delete_folder_and_move_contents"}[deleteFolder], func(t *testing.T) {
			db := recoveryTestDB(t)
			seedRecoveryConversation(t, db, "root", false)
			for _, id := range []string{"first", "second"} {
				if err := db.Create(&orm.ConversationArchiveFolder{ID: id, UserID: "u1", Name: id, NormalizedName: id}).Error; err != nil {
					t.Fatal(err)
				}
			}
			w, r := recoveryRequest(http.MethodPost, "/", map[string]any{"folder_id": "first", "only_if_active": true}, map[string]string{"conversation_id": "root"})
			ArchiveConversation(w, r)
			var receipt archiveReceiptForTest
			if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || w.Code != 200 {
				t.Fatalf("archive: %v %d %s", err, w.Code, w.Body.String())
			}
			if deleteFolder {
				w, r = recoveryRequest(http.MethodDelete, "/?move_to_folder_id=second", nil, map[string]string{"folder_id": "first"})
				DeleteConversationArchiveFolder(w, r)
			} else {
				w, r = recoveryRequest(http.MethodPost, "/", map[string]any{"folder_id": "second"}, map[string]string{"conversation_id": "root"})
				ArchiveConversation(w, r)
			}
			if w.Code != 200 {
				t.Fatalf("move: %d %s", w.Code, w.Body.String())
			}
			w, r = recoveryRequest(http.MethodPost, "/", map[string]any{"expected_archived_at": receipt.ArchivedAt.Format(time.RFC3339Nano)}, map[string]string{"conversation_id": "root"})
			UnarchiveConversation(w, r)
			var saved orm.Conversation
			if w.Code != 409 {
				t.Fatalf("old undo overrode a later move: %d %s", w.Code, w.Body.String())
			}
			if err := db.First(&saved, "id = ?", "root").Error; err != nil || saved.ArchivedAt == nil || saved.ArchiveFolderID == nil || *saved.ArchiveFolderID != "second" {
				t.Fatalf("later move lost: %v %#v", err, saved)
			}
		})
	}
}
