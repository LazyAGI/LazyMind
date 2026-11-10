package academic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestSourceImportHistoryUsesSavedOriginalAndObservedStage(t *testing.T) {
	db := sourceTestDB(t)
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Task{}, &orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	previous := store.DB()
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(previous, previous, nil) })
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "")
	source := SourceContext{ConversationID: "chat", HistoryID: "answer"}
	owner := orm.BaseModel{CreateUserID: "alice"}
	batch := newSourceImportBatch(sourceImportCaller(orm.PaperImportBatch{CreatedBy: "alice"}), source, "papers", "batch", "key", "hash", 4, false)
	for _, row := range []any{
		&orm.Dataset{ID: "papers", Scene: "academic_papers", ProcessingLevel: "chunked", ProcessingRevision: 1, BaseModel: owner},
		&orm.Conversation{ID: "chat", BaseModel: owner},
		&orm.ChatHistory{ID: "answer", ConversationID: "chat", RetrievalResult: json.RawMessage(`[]`)},
		&batch,
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ id, taskState, level string }{
		{"original", "SUCCEEDED", ""}, {"parsed", "SUCCEEDED", "parsed"},
		{"failed", "FAILED", "parsed"}, {"unknown", "UNKNOWN", "parsed"},
	} {
		path := filepath.Join(t.TempDir(), test.id+".pdf")
		if err := os.WriteFile(path, []byte("%PDF original"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, row := range []any{
			&orm.Document{ID: test.id, DatasetID: "papers", DocumentType: "PDF", Ext: marshal(map[string]string{"stored_path": path}), BaseModel: owner},
			&orm.Task{ID: "task-" + test.id, DatasetID: "papers", DocID: test.id, TaskType: "TASK_TYPE_PARSE_UPLOADED", Ext: marshal(map[string]any{"task_state": test.taskState, "processing_level": test.level, "processing_revision": 1}), BaseModel: owner},
			&orm.DocumentProcessingState{DatasetID: "papers", DocumentID: test.id, ParseStatus: "pending", ChunkStatus: "pending", IndexStatus: "pending"},
			&orm.PaperImportItem{ID: test.id, BatchID: batch.ID, TitleSnapshot: test.id, DocumentID: test.id, Status: "succeeded", Stage: "processing", ReferenceIDsJSON: json.RawMessage(`[]`), PresenceSnapshotJSON: json.RawMessage(`{}`), SelectedCandidateJSON: json.RawMessage(`{}`), ErrorDetailsJSON: json.RawMessage(`{}`)},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/academic/imports?conversation_id=chat&history_id=answer", nil)
	req.Header.Set("X-User-Id", "alice")
	rec := httptest.NewRecorder()
	ListImports(rec, req)
	var body struct{ Items []SourceImportView }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 || len(body.Items) != 4 {
		t.Fatal(rec.Code, rec.Body.String(), err)
	}
	want := map[string]string{"original": "not_requested", "parsed": "ready", "failed": "failed", "unknown": "unknown"}
	for _, row := range body.Items {
		if row.Status != "added" || row.ProcessingStatus != want[row.ItemID] ||
			row.Retryable != (row.ItemID == "failed") || row.StateUnavailable != (row.ItemID == "unknown") {
			t.Fatalf("incorrect import history: %+v", row)
		}
		if row.ItemID == "parsed" && row.ProcessingLevel != "parsed" {
			t.Fatalf("parse-only task inherited collection default: %+v", row)
		}
	}
}
