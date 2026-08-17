package coreadapter

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

type fakeDocumentDatasetService struct{ get doc.DatasetGetRequest }

func (s *fakeDocumentDatasetService) ListDatasets(context.Context, doc.DatasetListRequest) (doc.DatasetListResult, error) {
	return doc.DatasetListResult{}, nil
}
func (s *fakeDocumentDatasetService) GetDataset(_ context.Context, request doc.DatasetGetRequest) (doc.Dataset, error) {
	s.get = request
	return doc.Dataset{DatasetID: request.DatasetID}, nil
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
