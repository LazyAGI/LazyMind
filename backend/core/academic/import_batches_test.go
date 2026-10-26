package academic

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestImportOperationViewsPageInDatabaseAndKeepDefaultContract(t *testing.T) {
	db := paperLibraryTestDB(t)
	now := time.Now().UTC()
	work := orm.AcademicWork{ID: "work", CanonicalTitle: "Paper", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]any{})}
	if e := db.Create(&work).Error; e != nil {
		t.Fatal(e)
	}
	for i, status := range []string{"succeeded", "failed", "pending"} {
		id := []string{"done", "failed", "active"}[i]
		batch := orm.PaperImportBatch{ID: id, EntryType: "paper_link", TargetDatasetID: "papers", CreatedBy: "owner", Status: status, SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{}), CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if e := db.Create(&batch).Error; e != nil {
			t.Fatal(e)
		}
		for j := 0; j < 5; j++ {
			item := orm.PaperImportItem{ID: id + string(rune('a'+j)), BatchID: id, AcademicWorkID: "work", TitleSnapshot: "Paper", Status: status, Stage: "done", ResultKind: "metadata_only", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(map[string]any{}), ErrorDetailsJSON: marshal(map[string]any{}), CreatedAt: now.Add(time.Duration(j) * time.Millisecond)}
			if status != "succeeded" {
				item.ResultKind = ""
				item.Stage = "acquire"
			}
			if e := db.Create(&item).Error; e != nil {
				t.Fatal(e)
			}
		}
	}
	w := libraryRequest(t, "GET", "/imports?view=operations&limit=1", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page struct {
		Items        []ImportOperation `json:"items"`
		Next         string            `json:"next_cursor"`
		Notification struct {
			Active      int `json:"active"`
			NeedsAction int `json:"needs_action"`
		} `json:"notification"`
	}
	json.Unmarshal(w.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Summary.Total != 5 || page.Next == "" || page.Notification.Active != 1 || page.Notification.NeedsAction != 1 {
		t.Fatalf("incomplete operations or totals: %+v", page)
	}
	w = libraryRequest(t, "GET", "/imports/done?view=operations&limit=2", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var detail struct {
		Operation ImportOperation       `json:"operation"`
		Items     []ImportOperationItem `json:"items"`
		Next      string                `json:"next_cursor"`
	}
	json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Items) != 2 || detail.Operation.Summary.Total != 5 || detail.Next == "" {
		t.Fatalf("detail paging truncated summary: %+v", detail)
	}
	w = libraryRequest(t, "GET", "/imports/done", "papers", "owner", nil)
	var original struct {
		Batch orm.PaperImportBatch  `json:"batch"`
		Items []orm.PaperImportItem `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &original)
	if w.Code != 200 || original.Batch.ID != "done" || len(original.Items) != 5 {
		t.Fatal("main batch status contract changed", w.Body.String())
	}
}
func TestImportOperationsPreserveFailureAndRecognizeLaterResolution(t *testing.T) {
	db := paperLibraryTestDB(t)
	path := filepath.Join(t.TempDir(), "paper.pdf")
	if e := os.WriteFile(path, []byte("%PDF fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, row := range []any{
		&orm.AcademicWork{ID: "work", CanonicalTitle: "Paper", AuthorsJSON: marshal([]string{}), ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]any{})},
		&orm.Document{ID: "later-pdf", DatasetID: "papers", DisplayName: "paper.pdf", Ext: marshal(map[string]string{"stored_path": path})},
		&orm.AcademicWorkDocument{AcademicWorkID: "work", DatasetID: "papers", DocumentID: "later-pdf", SourceProvider: "arxiv", SourceVersion: "v1"},
		&orm.PaperImportBatch{ID: "failed", EntryType: "paper_link", TargetDatasetID: "papers", CreatedBy: "owner", Status: "failed", SourceContextJSON: marshal(map[string]any{}), SourceDocumentIDsJSON: marshal([]string{}), PolicySnapshotJSON: marshal(map[string]any{})},
		&orm.PaperImportItem{ID: "failed-item", BatchID: "failed", AcademicWorkID: "work", Status: "failed", Stage: "acquire", ResultKind: "failed", ReferenceIDsJSON: marshal([]string{}), PresenceSnapshotJSON: marshal(map[string]any{}), SelectedCandidateJSON: marshal(FulltextCandidate{SourceProvider: "arxiv", Version: "v1"}), ErrorDetailsJSON: marshal(map[string]any{"message": "old failure"})},
	} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
	w := libraryRequest(t, "GET", "/imports/failed?view=operations", "papers", "owner", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var detail struct {
		Operation ImportOperation       `json:"operation"`
		Items     []ImportOperationItem `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &detail)
	if len(detail.Items) != 1 || detail.Items[0].ResultKind != "failed" || !detail.Items[0].ResolvedLater || detail.Items[0].CanRetry || detail.Operation.Summary.NeedsAction != 0 {
		t.Fatalf("historical failure or current resolution lost: %+v", detail)
	}
}
