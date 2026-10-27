package coreadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

type fakeDocumentDatasetService struct {
	get doc.DatasetGetRequest
	err error
}

func (s *fakeDocumentDatasetService) ListDatasets(context.Context, doc.DatasetListRequest) (doc.DatasetListResult, error) {
	return doc.DatasetListResult{}, nil
}
func (s *fakeDocumentDatasetService) GetDataset(_ context.Context, request doc.DatasetGetRequest) (doc.Dataset, error) {
	s.get = request
	return doc.Dataset{DatasetID: request.DatasetID}, s.err
}

func TestKnowledgeDocumentFiltersBeforePagination(t *testing.T) {
	if got := mapDocumentSummary(doc.DocumentMetadata{RelativePath: "team/reports"}); got.RelativePath != "team/reports" {
		t.Fatalf("relative directory missing: %+v", got)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.Document{}); err != nil {
		t.Fatal(err)
	}
	rows := []orm.Document{
		{ID: "a", DatasetID: "ds", DisplayName: "Report_100%.pdf", Ext: []byte(`{"relative_path":"Team/2026!"}`)},
		{ID: "b", DatasetID: "ds", DisplayName: "Report_100%.docx", Ext: []byte(`{"relative_path":"Team/2026!"}`)},
		{ID: "c", DatasetID: "ds", DisplayName: "ReportX1000.pdf", Ext: []byte(`{"relative_path":"Team/2026!"}`)},
		{ID: "d", DatasetID: "ds", DisplayName: "Report_100%.pdf", Ext: []byte(`{"relative_path":"Other","stored_path":"/private/Team/2026!"}`)},
		{ID: "e", DatasetID: "other", DisplayName: "Report_100%.pdf", Ext: []byte(`{"relative_path":"Team/2026!"}`)},
		{ID: "f", DatasetID: "ds", DisplayName: "Report_100%.pdf", DocumentType: "FOLDER", Ext: []byte(`{"relative_path":"Team/2026!"}`)},
		{ID: "g", DatasetID: "ds", DisplayName: "without-path"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	datasets := &fakeDocumentDatasetService{}
	reader, err := NewKnowledgeDocumentReader(db, datasets, &fakeCoreDocumentService{})
	if err != nil {
		t.Fatal(err)
	}
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "user"}}
	query := capability.KnowledgeDocumentListQuery{KnowledgeID: "ds", Name: "report_100%", Path: "team/2026!", Limit: 1}
	for offset, want := range []string{"a", "b"} {
		query.Offset = offset
		page, err := reader.ListKnowledgeDocuments(context.Background(), call, query)
		if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != want {
			t.Fatalf("offset %d: page=%+v err=%v", offset, page, err)
		}
	}
	query.Offset, query.Path = 0, "/private/Team"
	page, err := reader.ListKnowledgeDocuments(context.Background(), call, query)
	if err != nil || page.Total != 0 {
		t.Fatalf("host path leaked into filtering: %+v %v", page, err)
	}
	datasets.err = errors.New("access denied")
	if _, err := reader.ListKnowledgeDocuments(context.Background(), call, query); err == nil {
		t.Fatal("dataset authorization failure ignored")
	}
}

type fakeCoreDocumentService struct{}

func (*fakeCoreDocumentService) GetDocumentMetadata(_ context.Context, request doc.DocumentGetRequest) (doc.DocumentMetadata, error) {
	return doc.DocumentMetadata{ID: request.DocumentID, DatasetID: request.DatasetID, Name: "name-" + request.DocumentID, Tags: []string{}}, nil
}
func (*fakeCoreDocumentService) GetDocument(_ context.Context, request doc.DocumentReadRequest) (doc.DocumentReadResult, error) {
	return doc.DocumentReadResult{
		Metadata: doc.DocumentMetadata{ID: request.DocumentID, DatasetID: request.DatasetID, Name: "document"},
		Content:  &doc.DocumentContent{Text: "content", MIMEType: "text/plain"},
	}, nil
}

func TestKnowledgeDocumentReaderUsesDatasetACLAndExcludesFolders(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:capability-docs?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.Document{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rows := []orm.Document{
		{ID: "doc-1", DatasetID: "ds-1", DisplayName: "one", DocumentType: "FILE", BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}},
		{ID: "folder-1", DatasetID: "ds-1", DisplayName: "folder", DocumentType: "FOLDER", BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}},
		{ID: "doc-other", DatasetID: "ds-2", DisplayName: "other", DocumentType: "FILE", BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	datasets := &fakeDocumentDatasetService{}
	reader, err := NewKnowledgeDocumentReader(db, datasets, &fakeCoreDocumentService{})
	if err != nil {
		t.Fatal(err)
	}
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "user-1", TenantID: "tenant-1"}}
	result, err := reader.ListKnowledgeDocuments(context.Background(), call, capability.KnowledgeDocumentListQuery{KnowledgeID: "ds-1", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if datasets.get.UserID != "user-1" || datasets.get.Caller.TenantID != "tenant-1" {
		t.Fatalf("dataset ACL request = %#v", datasets.get)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != "doc-1" {
		t.Fatalf("document page = %#v", result)
	}
}

type observedDocumentService struct{ fakeCoreDocumentService }

func (*observedDocumentService) GetDocumentMetadata(_ context.Context, req doc.DocumentGetRequest) (doc.DocumentMetadata, error) {
	return doc.DocumentMetadata{ID: req.DocumentID, DatasetID: req.DatasetID, ParseStatus: "SUCCESS", ProcessingLevel: "indexed", ProcessingState: orm.DocumentProcessingState{ParseStatus: "succeeded", ChunkStatus: "succeeded", IndexStatus: "succeeded"}}, nil
}
func (s *observedDocumentService) GetDocument(ctx context.Context, req doc.DocumentReadRequest) (doc.DocumentReadResult, error) {
	metadata, err := s.GetDocumentMetadata(ctx, doc.DocumentGetRequest{DocumentID: req.DocumentID, DatasetID: req.DatasetID})
	return doc.DocumentReadResult{Metadata: metadata}, err
}

func TestDocumentListUsesObservedStagesInsteadOfPersistedPending(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.Document{}, &orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{&orm.Document{ID: "indexed", DatasetID: "ds", DisplayName: "new indexed original"}, &orm.DocumentProcessingState{DatasetID: "ds", DocumentID: "indexed", ParseStatus: "pending", ChunkStatus: "pending", IndexStatus: "pending"}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	service := &observedDocumentService{}
	reader, err := NewKnowledgeDocumentReader(db, &fakeDocumentDatasetService{}, service)
	if err != nil {
		t.Fatal(err)
	}
	stateReads := 0
	const callback = "test:adapter-processing-state-read"
	db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "document_processing_states" {
			stateReads++
		}
	})
	defer db.Callback().Query().Remove(callback)
	page, err := reader.ListKnowledgeDocuments(context.Background(), capability.InvocationContext{Principal: capability.Principal{UserID: "user"}}, capability.KnowledgeDocumentListQuery{KnowledgeID: "ds", Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].ParseStatus != "SUCCESS" || page.Items[0].ProcessingStatus != "ready" {
		t.Fatalf("observed indexed success lost: %+v %v", page, err)
	}
	detail, err := reader.GetKnowledgeDocument(context.Background(), capability.InvocationContext{Principal: capability.Principal{UserID: "user"}}, capability.GetKnowledgeDocumentInput{KnowledgeID: "ds", DocumentID: "indexed"})
	if err != nil || detail.Document.ProcessingStatus != "ready" {
		t.Fatalf("get loses observed indexed stages: %+v %v", detail, err)
	}
	if stateReads != 0 {
		t.Fatalf("adapter re-read persisted stage state %d times", stateReads)
	}
	metadata, _ := service.GetDocumentMetadata(context.Background(), doc.DocumentGetRequest{DatasetID: "ds", DocumentID: "indexed"})
	if got := mapDocumentSummary(metadata).ProcessingStatus; got != "ready" {
		t.Fatalf("get mapper loses observed state: %s", got)
	}
}
