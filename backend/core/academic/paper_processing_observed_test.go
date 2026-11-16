package academic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestPaperViewsUseRequestedProcessingInsteadOfDatasetDefault(t *testing.T) {
	for _, test := range []struct {
		name, level, taskState, wantParse, wantOperation, wantDocument string
		active                                                         int
	}{
		{"legacy_original_only", "", "SUCCEEDED", "ready", "ready", "ready", 0},
		{"explicit_stored", "stored", "SUCCEEDED", "ready", "ready", "ready", 0},
		{"parse_only_completed", "parsed", "SUCCEEDED", "ready", "ready", "ready", 0},
		{"queued_parse", "parsed", "UPLOADED", "parsing", "processing", "running", 1},
		{"running_parse", "parsed", "RUNNING", "parsing", "processing", "running", 1},
		{"failed_parse", "parsed", "FAILED", "failed", "processing_failed", "failed", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := paperLibraryTestDB(t)
			if err := db.Model(&orm.Dataset{}).Where("id='papers'").Updates(map[string]any{"processing_level": "indexed", "processing_revision": 1}).Error; err != nil {
				t.Fatal(err)
			}
			paper := importDraft(t, "papers", "metadata", libraryDraft("paper", "Paper", "10.1234/observed")).Data.Papers[0]
			db.Model(&orm.PaperImportItem{}).Where("library_paper_id=?", paper.ID).Updates(map[string]any{"status": "succeeded", "stage": "done", "result_kind": "metadata_only"})
			path := filepath.Join(t.TempDir(), "paper.pdf")
			if err := os.WriteFile(path, []byte("%PDF original"), 0600); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			for _, row := range []any{
				&orm.Document{ID: "pdf", DatasetID: "papers", DisplayName: "paper.pdf", Ext: marshal(map[string]string{"stored_path": path})},
				&orm.PaperLibraryAttachment{DatasetID: "papers", PaperID: paper.ID, ID: "attachment", DocumentID: "pdf", Name: "paper.pdf", Kind: "pdf", IsPrimary: true},
				&orm.DocumentProcessingState{DatasetID: "papers", DocumentID: "pdf", ParseStatus: "pending", ChunkStatus: "pending", IndexStatus: "pending", UpdatedAt: now},
				&orm.Task{ID: "task", DatasetID: "papers", DocID: "pdf", TaskType: "TASK_TYPE_PARSE_UPLOADED", Ext: marshal(map[string]any{"task_state": test.taskState, "processing_level": test.level, "processing_revision": 1}), BaseModel: orm.BaseModel{CreatedAt: now}},
				&orm.PaperImportBatch{ID: "upload", EntryType: "paper_link", TargetDatasetID: "papers", CreatedBy: "owner", Status: "succeeded", SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{})},
				&orm.PaperImportItem{ID: "upload-item", BatchID: "upload", LibraryPaperID: paper.ID, AcademicWorkID: paper.AcademicWorkID, DocumentID: "pdf", Status: "succeeded", Stage: "done", ResultKind: "imported", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{})},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			view := readLibrary(t, "papers")
			if view.Data.Papers[0].Parse != test.wantParse || view.Active != (test.active > 0) {
				t.Errorf("library parse=%s active=%v; want %s/%v", view.Data.Papers[0].Parse, view.Active, test.wantParse, test.active > 0)
			}
			w := libraryRequest(t, "GET", "/imports?view=operations", "papers", "owner", nil)
			var list struct {
				Notification struct{ Active int } `json:"notification"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || list.Notification.Active != test.active {
				t.Errorf("notification: %d %s", w.Code, w.Body.String())
			}
			w = libraryRequest(t, "GET", "/imports/upload?view=operations", "papers", "owner", nil)
			var detail struct {
				Items []ImportOperationItem `json:"items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || len(detail.Items) != 1 {
				t.Fatal(w.Code, w.Body.String())
			}
			item := detail.Items[0]
			if item.CurrentState != test.wantOperation || item.CurrentDocument == nil || item.CurrentDocument.ProcessingStatus != test.wantDocument {
				t.Errorf("operation state=%s document=%+v; want %s/%s", item.CurrentState, item.CurrentDocument, test.wantOperation, test.wantDocument)
			}
		})
	}
}
