package chat

import (
	"encoding/json"
	"net/http"
	"testing"

	"lazymind/core/common/orm"
)

func TestArchiveFolderKindsAreIndependent(t *testing.T) {
	db := recoveryTestDB(t)
	ids := map[string]string{}
	for _, kind := range []string{"dialog", "task"} {
		w, r := recoveryRequest(http.MethodPost, "/", map[string]any{"name": "Research", "kind": kind}, nil)
		CreateConversationArchiveFolder(w, r)
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", kind, w.Code, w.Body.String())
		}
		var body struct {
			Folder struct {
				ID string `json:"id"`
			} `json:"folder"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		ids[kind] = body.Folder.ID
	}
	for _, kind := range []string{"dialog", "task"} {
		w, r := recoveryRequest(http.MethodGet, "/?kind="+kind, nil, nil)
		ListConversationArchiveFolders(w, r)
		var body struct {
			Folders []struct {
				ID string `json:"id"`
			} `json:"folders"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Folders) != 1 || body.Folders[0].ID != ids[kind] {
			t.Fatalf("%s folders: %s", kind, w.Body.String())
		}
	}
	seedRecoveryConversation(t, db, "task", true)
	w, r := recoveryRequest(http.MethodPost, "/", map[string]any{"folder_id": ids["dialog"]}, map[string]string{"conversation_id": "task"})
	ArchiveConversation(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-kind archive: %d %s", w.Code, w.Body.String())
	}
	var c orm.Conversation
	if err := db.First(&c, "id=?", "task").Error; err != nil {
		t.Fatal(err)
	}
	if c.ArchivedAt != nil {
		t.Fatal("cross-kind request archived conversation")
	}
	w, r = recoveryRequest(http.MethodPost, "/", map[string]any{"folder_id": ids["task"]}, map[string]string{"conversation_id": "task"})
	ArchiveConversation(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("own-kind archive: %d %s", w.Code, w.Body.String())
	}
	w, r = recoveryRequest(http.MethodDelete, "/?move_to_folder_id="+ids["dialog"], nil, map[string]string{"folder_id": ids["task"]})
	DeleteConversationArchiveFolder(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-kind move: %d %s", w.Code, w.Body.String())
	}
	if err := db.First(&c, "id=?", "task").Error; err != nil {
		t.Fatal(err)
	}
	if c.ArchiveFolderID == nil || *c.ArchiveFolderID != ids["task"] {
		t.Fatal("failed cross-kind move changed membership")
	}
	w, r = recoveryRequest(http.MethodPost, "/", map[string]any{"name": "Bad", "kind": "other"}, nil)
	CreateConversationArchiveFolder(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid kind: %d", w.Code)
	}
}

func TestArchiveDeleteDoesNotAffectOtherKind(t *testing.T) {
	db := recoveryTestDB(t)
	for _, task := range []bool{false, true} {
		id := "dialog"
		if task {
			id = "task"
		}
		seedRecoveryConversation(t, db, id, task)
		w, r := recoveryRequest(http.MethodPost, "/", nil, map[string]string{"conversation_id": id})
		ArchiveConversation(w, r)
		if w.Code != http.StatusOK {
			t.Fatal(w.Body.String())
		}
	}
	w, r := recoveryRequest(http.MethodDelete, "/", nil, map[string]string{"name": "task"})
	DeleteConversation(w, r)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	w, r = recoveryRequest(http.MethodGet, "/?kind=dialog", nil, nil)
	ListArchivedConversations(w, r)
	var body struct {
		Items []struct {
			ID string `json:"conversation_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ID != "dialog" {
		t.Fatalf("dialog affected: %s", w.Body.String())
	}
}
