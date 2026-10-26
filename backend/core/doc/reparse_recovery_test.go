package doc

import (
	"context"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func TestReparseResumesCommittedReceiptWithoutAnotherTask(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	row := orm.Document{ID: "interrupted", DatasetID: "source", DisplayName: "paper.pdf", DocumentType: "PDF", ResourceVersion: 1, Ext: []byte(`{}`), BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	state := newDocumentProcessingState("source", row.ID, now)
	state.ParseStatus = "failed"
	if err := db.Create(&state).Error; err != nil {
		t.Fatal(err)
	}
	req := ReparseDocumentsRequest{KnowledgeBaseID: "source", DocumentIDs: []string{row.ID}}
	op.OperationKey = "interrupted-reparse"
	var firstTask string
	s.submitReparse = func(_ context.Context, _ DatasetCatalogCaller, _, task string) (StartTaskResult, error) {
		firstTask = task
		panic("process stopped after receipt commit")
	}
	func() { defer func() { _ = recover() }(); _, _ = s.Reparse(ctx, op, req) }()
	if firstTask == "" {
		t.Fatal("did not commit task")
	}
	calls := 0
	s.submitReparse = func(_ context.Context, _ DatasetCatalogCaller, _, task string) (StartTaskResult, error) {
		calls++
		if task != firstTask {
			t.Fatal("recovery created another task")
		}
		return StartTaskResult{Status: "STARTED"}, nil
	}
	for i := 0; i < 2; i++ {
		result, err := s.Reparse(ctx, op, req)
		if err != nil || result.Items[0].ProcessingStatus != "submitted" {
			t.Fatalf("%+v %v", result, err)
		}
	}
	var count int64
	db.Model(&orm.Task{}).Where("doc_id = ?", row.ID).Count(&count)
	if count != 1 || calls != 1 {
		t.Fatalf("tasks=%d submissions=%d", count, calls)
	}
}
