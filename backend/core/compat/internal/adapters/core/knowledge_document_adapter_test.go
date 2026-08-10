package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/compat/contract"
	compatknowledge "lazymind/core/compat/knowledge"
	"lazymind/core/doc"
)

type fakeDocumentService struct {
	documentReq doc.DocumentReadRequest
	documentRes doc.DocumentReadResult
	documentErr error
	metadataReq doc.DocumentGetRequest
	contentReq  doc.DocumentContentRequest
	chunksReq   doc.DocumentChunksRequest
	metadataRes doc.DocumentMetadata
	contentRes  doc.DocumentContent
	chunksRes   doc.DocumentChunksResult
	metadataErr error
	contentErr  error
	chunksErr   error
}

func (s *fakeDocumentService) GetDocument(ctx context.Context, req doc.DocumentReadRequest) (doc.DocumentReadResult, error) {
	s.documentReq = req
	if s.documentErr != nil {
		return doc.DocumentReadResult{}, s.documentErr
	}
	return s.documentRes, nil
}

func (s *fakeDocumentService) GetDocumentMetadata(ctx context.Context, req doc.DocumentGetRequest) (doc.DocumentMetadata, error) {
	s.metadataReq = req
	if s.metadataErr != nil {
		return doc.DocumentMetadata{}, s.metadataErr
	}
	return s.metadataRes, nil
}

func (s *fakeDocumentService) ReadDocumentContent(ctx context.Context, req doc.DocumentContentRequest) (doc.DocumentContent, error) {
	s.contentReq = req
	if s.contentErr != nil {
		return doc.DocumentContent{}, s.contentErr
	}
	return s.contentRes, nil
}

func (s *fakeDocumentService) ListDocumentChunks(ctx context.Context, req doc.DocumentChunksRequest) (doc.DocumentChunksResult, error) {
	s.chunksReq = req
	if s.chunksErr != nil {
		return doc.DocumentChunksResult{}, s.chunksErr
	}
	return s.chunksRes, nil
}

func TestKnowledgeDocumentAdapterMapsMetadata(t *testing.T) {
	now := time.Date(2026, 7, 29, 16, 0, 0, 0, time.UTC)
	service := &fakeDocumentService{metadataRes: doc.DocumentMetadata{
		ID:          "doc-1",
		DatasetID:   "ds-1",
		Name:        "Spec",
		Source:      "LOCAL_FILE",
		Tags:        []string{"api"},
		ParseStatus: "SUCCEEDED",
		MIMEType:    "text/plain",
		SizeBytes:   42,
		CreatedAt:   now,
		UpdatedAt:   now.Add(time.Minute),
		CreatedBy:   "Alice",
		OriginalFile: &doc.DocumentFileRef{
			FileName:    "spec.txt",
			DownloadURL: "/datasets/ds-1/documents/doc-1:download",
		},
	}}
	adapter := mustKnowledgeDocumentAdapter(t, service)
	got, err := adapter.GetDocumentMetadata(context.Background(), contract.CallContext{UserID: " user-1 "}, compatknowledge.GetDocumentMetadataInput{
		KnowledgeID: " ds-1 ",
		DocumentID:  " doc-1 ",
	})
	if err != nil {
		t.Fatalf("GetDocumentMetadata returned error: %v", err)
	}
	if service.metadataReq.UserID != "user-1" || service.metadataReq.DatasetID != "ds-1" || service.metadataReq.DocumentID != "doc-1" || service.metadataReq.Caller.UserID != "user-1" {
		t.Fatalf("unexpected service req: %#v", service.metadataReq)
	}
	if got.ID != "doc-1" || got.KnowledgeID != "ds-1" || got.Name != "Spec" || got.Source != "LOCAL_FILE" {
		t.Fatalf("unexpected metadata mapping: %#v", got)
	}
	if got.OriginalFile == nil || got.OriginalFile.FileName != "spec.txt" || got.OriginalFile.DownloadURL != "/datasets/ds-1/documents/doc-1:download" {
		t.Fatalf("unexpected file ref: %#v", got.OriginalFile)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal mapped metadata: %v", err)
	}
	for _, forbidden := range []string{"lazyllm_doc_id", "FileSystemPath", "StoredPath", "ParseStoredPath", "SourceStoredPath", "/tmp/server/path"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("metadata leaks forbidden field %q: %s", forbidden, raw)
		}
	}
}

func TestKnowledgeDocumentAdapterAggregatesDocumentRead(t *testing.T) {
	total := int32(2)
	service := &fakeDocumentService{documentRes: doc.DocumentReadResult{
		Metadata: doc.DocumentMetadata{ID: "doc-1", DatasetID: "ds-1", Name: "Spec"},
		Content:  &doc.DocumentContent{MIMEType: "text/plain", Text: "hello", Truncated: true},
		Chunks:   &doc.DocumentChunksResult{Chunks: []doc.DocumentChunk{{ID: "chunk-1", Text: "part", Number: 1}}, TotalSize: total, NextPageToken: "next"},
	}}
	adapter := mustKnowledgeDocumentAdapter(t, service)
	got, err := adapter.GetDocument(context.Background(), contract.CallContext{UserID: " user-1 "}, compatknowledge.GetDocumentInput{
		KnowledgeID:    " ds-1 ",
		DocumentID:     " doc-1 ",
		IncludeContent: true,
		IncludeChunks:  true,
		ChunksPage:     contract.PageRequest{PageSize: 500, PageToken: "tok"},
	})
	if err != nil {
		t.Fatalf("GetDocument returned error: %v", err)
	}
	if service.documentReq.UserID != "user-1" || service.documentReq.DatasetID != "ds-1" || service.documentReq.DocumentID != "doc-1" || !service.documentReq.IncludeContent || !service.documentReq.IncludeChunks {
		t.Fatalf("unexpected aggregate request: %#v", service.documentReq)
	}
	if service.documentReq.PageSize != contract.MaxPageSize || service.documentReq.PageToken != "tok" {
		t.Fatalf("unexpected aggregate page: %#v", service.documentReq)
	}
	if got.Document.Content == nil || got.Document.Content.Text != "hello" || len(got.Document.Chunks) != 1 || got.Document.ChunksPage == nil || got.Document.ChunksPage.NextPageToken != "next" {
		t.Fatalf("unexpected aggregate mapping: %#v", got.Document)
	}
}

func TestKnowledgeDocumentAdapterMapsContent(t *testing.T) {
	service := &fakeDocumentService{contentRes: doc.DocumentContent{MIMEType: "text/plain", Text: "hello", Truncated: true}}
	adapter := mustKnowledgeDocumentAdapter(t, service)
	got, err := adapter.ReadDocumentContent(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.ReadDocumentContentInput{
		KnowledgeID: "ds-1",
		DocumentID:  "doc-1",
	})
	if err != nil {
		t.Fatalf("ReadDocumentContent returned error: %v", err)
	}
	if service.contentReq.UserID != "user-1" || service.contentReq.DatasetID != "ds-1" || service.contentReq.DocumentID != "doc-1" {
		t.Fatalf("unexpected service req: %#v", service.contentReq)
	}
	if got.MIMEType != "text/plain" || got.Text != "hello" || !got.Truncated {
		t.Fatalf("unexpected content mapping: %#v", got)
	}
}

func TestKnowledgeDocumentAdapterMapsChunksAndPaging(t *testing.T) {
	service := &fakeDocumentService{chunksRes: doc.DocumentChunksResult{
		Chunks:        []doc.DocumentChunk{{ID: "chunk-1", Text: "part", Number: 1}},
		TotalSize:     12,
		NextPageToken: "next",
	}}
	adapter := mustKnowledgeDocumentAdapter(t, service)
	got, err := adapter.ListDocumentChunks(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.ListDocumentChunksInput{
		KnowledgeID: "ds-1",
		DocumentID:  "doc-1",
		Page:        contract.PageRequest{PageSize: 500, PageToken: "tok"},
	})
	if err != nil {
		t.Fatalf("ListDocumentChunks returned error: %v", err)
	}
	if service.chunksReq.PageSize != contract.MaxPageSize || service.chunksReq.PageToken != "tok" {
		t.Fatalf("unexpected chunk req: %#v", service.chunksReq)
	}
	if len(got.Chunks) != 1 || got.Chunks[0] != (compatknowledge.DocumentChunk{ID: "chunk-1", Text: "part", Number: 1}) {
		t.Fatalf("unexpected chunks: %#v", got.Chunks)
	}
	if got.Page.Total == nil || *got.Page.Total != 12 || got.Page.NextPageToken != "next" {
		t.Fatalf("unexpected page: %#v", got.Page)
	}
}

func TestKnowledgeDocumentAdapterMapsDocumentServiceErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want contract.ErrorCode
	}{
		{name: "invalid", err: &doc.DocumentServiceError{Code: doc.DocumentServiceInvalidArgument, Message: "bad"}, want: contract.InvalidArgument},
		{name: "not found", err: &doc.DocumentServiceError{Code: doc.DocumentServiceNotFound, Message: "missing"}, want: contract.NotFound},
		{name: "forbidden", err: &doc.DocumentServiceError{Code: doc.DocumentServiceForbidden, Message: "forbidden"}, want: contract.NotFound},
		{name: "unavailable", err: &doc.DocumentServiceError{Code: doc.DocumentServiceUnavailable, Message: "db"}, want: contract.BackendUnavailable},
		{name: "unsupported", err: &doc.DocumentServiceError{Code: doc.DocumentServiceUnsupported, Message: "unsupported"}, want: contract.Unsupported},
		{name: "internal", err: &doc.DocumentServiceError{Code: doc.DocumentServiceInternal, Message: "internal"}, want: contract.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := mustKnowledgeDocumentAdapter(t, &fakeDocumentService{metadataErr: tt.err})
			_, err := adapter.GetDocumentMetadata(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.GetDocumentMetadataInput{
				KnowledgeID: "ds-1",
				DocumentID:  "doc-1",
			})
			if code, ok := contract.CodeOf(err); !ok || code != tt.want {
				t.Fatalf("code = %v, %v; want %s", code, ok, tt.want)
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("mapped err = %v, want wrapping %v", err, tt.err)
			}
		})
	}
}

func TestKnowledgeDocumentAdapterValidation(t *testing.T) {
	adapter := mustKnowledgeDocumentAdapter(t, &fakeDocumentService{})
	_, err := adapter.GetDocumentMetadata(context.Background(), contract.CallContext{UserID: ""}, compatknowledge.GetDocumentMetadataInput{KnowledgeID: "ds-1", DocumentID: "doc-1"})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("missing user code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
	_, err = adapter.GetDocumentMetadata(context.Background(), contract.CallContext{UserID: "user"}, compatknowledge.GetDocumentMetadataInput{DocumentID: "doc-1"})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("missing knowledge code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
	_, err = adapter.GetDocumentMetadata(context.Background(), contract.CallContext{UserID: "user"}, compatknowledge.GetDocumentMetadataInput{KnowledgeID: "ds-1"})
	if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
		t.Fatalf("missing document code = %v, %v; want INVALID_ARGUMENT", code, ok)
	}
}

func TestKnowledgeDocumentAdapterValidationForContentAndChunks(t *testing.T) {
	adapter := mustKnowledgeDocumentAdapter(t, &fakeDocumentService{})
	contentCases := []struct {
		name    string
		callCtx contract.CallContext
		input   compatknowledge.ReadDocumentContentInput
	}{
		{name: "content user", callCtx: contract.CallContext{}, input: compatknowledge.ReadDocumentContentInput{KnowledgeID: "ds-1", DocumentID: "doc-1"}},
		{name: "content knowledge", callCtx: contract.CallContext{UserID: "user"}, input: compatknowledge.ReadDocumentContentInput{DocumentID: "doc-1"}},
		{name: "content document", callCtx: contract.CallContext{UserID: "user"}, input: compatknowledge.ReadDocumentContentInput{KnowledgeID: "ds-1"}},
	}
	for _, tt := range contentCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := adapter.ReadDocumentContent(context.Background(), tt.callCtx, tt.input)
			if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
				t.Fatalf("code = %v, %v; want INVALID_ARGUMENT", code, ok)
			}
		})
	}
	chunkCases := []struct {
		name    string
		callCtx contract.CallContext
		input   compatknowledge.ListDocumentChunksInput
	}{
		{name: "chunks user", callCtx: contract.CallContext{}, input: compatknowledge.ListDocumentChunksInput{KnowledgeID: "ds-1", DocumentID: "doc-1"}},
		{name: "chunks knowledge", callCtx: contract.CallContext{UserID: "user"}, input: compatknowledge.ListDocumentChunksInput{DocumentID: "doc-1"}},
		{name: "chunks document", callCtx: contract.CallContext{UserID: "user"}, input: compatknowledge.ListDocumentChunksInput{KnowledgeID: "ds-1"}},
	}
	for _, tt := range chunkCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := adapter.ListDocumentChunks(context.Background(), tt.callCtx, tt.input)
			if code, ok := contract.CodeOf(err); !ok || code != contract.InvalidArgument {
				t.Fatalf("code = %v, %v; want INVALID_ARGUMENT", code, ok)
			}
		})
	}
}

func TestKnowledgeDocumentAdapterForDBsUsesSeparateLazyDB(t *testing.T) {
	coreDB := newKnowledgeDocumentCoreDB(t, "core")
	lazyDB := newKnowledgeDocumentLazyDB(t, "lazy")
	installKnowledgeAdapterScanTransport(t)
	now := time.Date(2026, 7, 29, 17, 0, 0, 0, time.UTC)
	seedKnowledgeDocumentCoreRows(t, coreDB, "ds-1", "doc-1", "lazy-doc-1", "user-1", "", now)
	lazySize := 987
	if err := lazyDB.Create(&readonlyorm.LazyLLMDocRow{
		DocID:        "lazy-doc-1",
		Filename:     "lazy-title.md",
		Path:         "/readonly/lazy-title.md",
		UploadStatus: "READY_FROM_LAZY",
		SourceType:   "FILE_SYSTEM",
		SizeBytes:    &lazySize,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("create lazy row: %v", err)
	}
	adapter, err := NewKnowledgeDocumentAdapterForDBs(coreDB.DB, lazyDB.DB)
	if err != nil {
		t.Fatalf("NewKnowledgeDocumentAdapterForDBs: %v", err)
	}

	got, err := adapter.GetDocumentMetadata(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.GetDocumentMetadataInput{
		KnowledgeID: "ds-1",
		DocumentID:  "doc-1",
	})
	if err != nil {
		t.Fatalf("GetDocumentMetadata: %v", err)
	}
	if got.ID != "doc-1" || got.KnowledgeID != "ds-1" {
		t.Fatalf("core identity not mapped from core DB: %#v", got)
	}
	if got.Name != "lazy-title.md" || got.Source != "FILE_SYSTEM" || got.SizeBytes != int64(lazySize) || got.ParseStatus != "READY_FROM_LAZY" {
		t.Fatalf("readonly fields not mapped from lazy DB: %#v", got)
	}
}

func TestKnowledgeDocumentAdapterForDBSingleDBMode(t *testing.T) {
	db := newKnowledgeDocumentCombinedDB(t, "single")
	installKnowledgeAdapterScanTransport(t)
	now := time.Date(2026, 7, 29, 17, 30, 0, 0, time.UTC)
	seedKnowledgeDocumentCoreRows(t, db, "ds-1", "doc-1", "lazy-doc-1", "user-1", "", now)
	size := 123
	if err := db.Create(&readonlyorm.LazyLLMDocRow{
		DocID:        "lazy-doc-1",
		Filename:     "single-title.md",
		Path:         "/readonly/single-title.md",
		UploadStatus: "READY",
		SourceType:   "FILE_SYSTEM",
		SizeBytes:    &size,
		CreatedAt:    now,
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("create readonly row: %v", err)
	}
	adapter, err := NewKnowledgeDocumentAdapterForDB(db.DB)
	if err != nil {
		t.Fatalf("NewKnowledgeDocumentAdapterForDB: %v", err)
	}
	got, err := adapter.GetDocumentMetadata(context.Background(), contract.CallContext{UserID: "user-1"}, compatknowledge.GetDocumentMetadataInput{
		KnowledgeID: "ds-1",
		DocumentID:  "doc-1",
	})
	if err != nil {
		t.Fatalf("GetDocumentMetadata: %v", err)
	}
	if got.Name != "single-title.md" || got.SizeBytes != int64(size) {
		t.Fatalf("single DB readonly mapping = %#v", got)
	}
}

func TestKnowledgeDocumentAdapterRejectsNilDependencies(t *testing.T) {
	if _, err := NewKnowledgeDocumentAdapter(nil); err == nil {
		t.Fatalf("NewKnowledgeDocumentAdapter nil service error = nil, want error")
	}
	if _, err := NewKnowledgeDocumentAdapterForDB(nil); err == nil {
		t.Fatalf("NewKnowledgeDocumentAdapterForDB nil db error = nil, want error")
	}
	db := newKnowledgeDocumentCoreDB(t, "nil")
	if _, err := NewKnowledgeDocumentAdapterForDBs(nil, db.DB); err == nil {
		t.Fatalf("NewKnowledgeDocumentAdapterForDBs nil core db error = nil, want error")
	}
	if _, err := NewKnowledgeDocumentAdapterForDBs(db.DB, nil); err == nil {
		t.Fatalf("NewKnowledgeDocumentAdapterForDBs nil lazy db error = nil, want error")
	}
}

func mustKnowledgeDocumentAdapter(t *testing.T, service DocumentService) *KnowledgeDocumentAdapter {
	t.Helper()
	adapter, err := NewKnowledgeDocumentAdapter(service)
	if err != nil {
		t.Fatalf("NewKnowledgeDocumentAdapter: %v", err)
	}
	return adapter
}

func newKnowledgeDocumentCoreDB(t *testing.T, name string) *orm.DB {
	t.Helper()
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "main")
	dsn := fmt.Sprintf("file:%s_%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), name, time.Now().UnixNano())
	db, err := orm.Connect(orm.DriverSQLite, dsn)
	if err != nil {
		t.Fatalf("connect core sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.Dataset{}, &orm.Document{}, &orm.Task{}, &orm.ACLModel{}, &orm.UserGroupModel{}); err != nil {
		t.Fatalf("auto migrate core: %v", err)
	}
	acl.InitStore(db)
	return db
}

func newKnowledgeDocumentLazyDB(t *testing.T, name string) *orm.DB {
	t.Helper()
	t.Setenv("LAZYMIND_READONLY_SCHEMA", "main")
	dsn := fmt.Sprintf("file:%s_%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), name, time.Now().UnixNano())
	db, err := orm.Connect(orm.DriverSQLite, dsn)
	if err != nil {
		t.Fatalf("connect lazy sqlite: %v", err)
	}
	if err := db.AutoMigrate(&readonlyorm.LazyLLMDocRow{}, &readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatalf("auto migrate lazy: %v", err)
	}
	return db
}

func newKnowledgeDocumentCombinedDB(t *testing.T, name string) *orm.DB {
	t.Helper()
	db := newKnowledgeDocumentCoreDB(t, name)
	if err := db.AutoMigrate(&readonlyorm.LazyLLMDocRow{}, &readonlyorm.LazyLLMDocServiceTaskRow{}); err != nil {
		t.Fatalf("auto migrate readonly: %v", err)
	}
	return db
}

func seedKnowledgeDocumentCoreRows(t *testing.T, db *orm.DB, datasetID, documentID, lazyDocID, userID, displayName string, now time.Time) {
	t.Helper()
	if err := db.Create(&orm.Dataset{
		ID:          datasetID,
		DisplayName: datasetID,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID + " name",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create dataset: %v", err)
	}
	if err := db.Create(&orm.Document{
		ID:           documentID,
		LazyllmDocID: lazyDocID,
		DatasetID:    datasetID,
		DisplayName:  displayName,
		Tags:         []byte(`[]`),
		Ext:          []byte(`{"content_type":"text/plain"}`),
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID + " name",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}).Error; err != nil {
		t.Fatalf("create document: %v", err)
	}
}
