package doc

import (
	"context"
	"errors"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKnowledgeCreateFolderPreviewHasNoWritesAndBindsDestination(t *testing.T) {
	s, db, op := resourceFixture(t)
	req := CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"a/b", "a/c"}}
	snap, e := s.SnapshotCreateFolders(context.Background(), op, req)
	if e != nil || snap.FolderCount != 3 {
		t.Fatalf("%+v %v", snap, e)
	}
	var n int64
	db.Model(&orm.Document{}).Count(&n)
	if n != 0 {
		t.Fatal("preflight wrote folders")
	}
	if _, e = s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"other"}}); e != nil {
		t.Fatal(e)
	}
	op.ExpectedSnapshot = snap.Digest
	if _, e = s.CreateFolders(context.Background(), op, req); e == nil {
		t.Fatal("stale confirmation accepted")
	}
}

func TestKnowledgeUploadMixedBatchAndReplay(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	s.originals, _ = NewOriginalService(OriginalServiceDeps{DB: db.DB, SubmitProcessing: func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error) {
		return StartTaskResult{Status: "STARTED"}, nil
	}})
	dir := t.TempDir()
	good := filepath.Join(dir, "good.txt")
	if e := os.WriteFile(good, []byte("sample"), 0600); e != nil {
		t.Fatal(e)
	}
	req := LocalUploadRequest{KnowledgeBaseID: "source", Files: []LocalUploadItem{{Path: good}, {Path: filepath.Join(dir, "unsupported.zzz")}, {Path: filepath.Join(dir, "missing.txt")}, {Path: good}}}
	op.OperationKey = "mixed"
	result, e := s.UploadLocalBatch(ctx, op, req)
	if e != nil || result.Summary.Succeeded != 1 || result.Summary.Failed != 3 {
		t.Fatalf("%+v %v", result, e)
	}
	if result.Items[1].Error.Code != "UNSUPPORTED_FORMAT" || result.Items[2].Error.Code != "FILE_NOT_FOUND" || result.Items[3].Error.Code != "CONFLICT" {
		t.Fatalf("wrong errors %+v", result.Items)
	}
	if result.ProcessingSummary["submitted"] != 1 || result.ProcessingSummary["failed"] != 0 {
		t.Fatalf("upload and parsing counts mixed: %+v", result.ProcessingSummary)
	}
	again, e := s.UploadLocalBatch(ctx, op, req)
	if e != nil || again.Items[0].ResourceRef != result.Items[0].ResourceRef {
		t.Fatalf("replay %+v %v", again, e)
	}
	var n int64
	db.Model(&orm.Document{}).Where("deleted_at IS NULL").Count(&n)
	if n != 1 {
		t.Fatalf("replay duplicated documents: %d", n)
	}
	db.Model(&orm.UploadedFile{}).Where("dataset_id = '' AND deleted_at IS NULL").Count(&n)
	if n != 0 {
		t.Fatalf("temporary acquisitions leaked: %d", n)
	}
	req.Files = req.Files[:1]
	if _, e = s.UploadLocalBatch(ctx, op, req); e == nil {
		t.Fatal("changed batch input accepted")
	}
}

func TestKnowledgeReparseKeepsIDAndJoinsActiveTask(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	row := orm.Document{ID: "broken", DatasetID: "source", DisplayName: "broken.pdf", DocumentType: "PDF", ResourceVersion: 1, Ext: []byte(`{}`), BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	if e := db.Create(&row).Error; e != nil {
		t.Fatal(e)
	}
	state := newDocumentProcessingState("source", "broken", now)
	state.ParseStatus = "failed"
	state.ParseErrorMessage = "invalid pdf"
	if e := db.Create(&state).Error; e != nil {
		t.Fatal(e)
	}
	calls := 0
	s.submitReparse = func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error) {
		calls++
		return StartTaskResult{Status: "STARTED"}, nil
	}
	req := ReparseDocumentsRequest{KnowledgeBaseID: "source", DocumentIDs: []string{"broken"}}
	op.OperationKey = "retry"
	result, e := s.Reparse(ctx, op, req)
	if e != nil || len(result.Items) != 1 || result.Items[0].TaskID == "" || result.Items[0].ResourceRef.DocumentID != "broken" || result.Items[0].ProcessingStatus != "submitted" {
		t.Fatalf("%+v %v", result, e)
	}
	if _, e = s.Reparse(ctx, op, req); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal("replay resubmitted")
	}
	op.OperationKey = "join"
	joined, e := s.Reparse(ctx, op, req)
	if e != nil || joined.Items[0].ResourceStatus != "skipped" || joined.Items[0].TaskID != result.Items[0].TaskID || calls != 1 {
		t.Fatalf("join %+v %v", joined, e)
	}
	var n int64
	db.Model(&orm.Task{}).Count(&n)
	if n != 1 {
		t.Fatalf("created %d tasks", n)
	}
	op.Caller.UserID = "readonly"
	op.OperationKey = "denied"
	if _, e = s.Reparse(ctx, op, req); e == nil {
		t.Fatal("foreign retry allowed")
	}
}

func TestKnowledgeMoveProcessingSummaryMatchesSubmittedReceipt(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	s.originals, _ = NewOriginalService(OriginalServiceDeps{DB: db.DB, SubmitProcessing: func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error) {
		return StartTaskResult{Status: "STARTED"}, nil
	}})
	file, err := s.originals.AcquireOriginal(ctx, op.Caller, AcquireOriginalRequest{Filename: "move.txt", ContentType: "text/plain", Reader: strings.NewReader("move receipt")})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := s.originals.ImportOriginal(ctx, op.Caller, ImportOriginalRequest{FileRef: file.FileRef, DatasetID: "source"})
	if err != nil {
		t.Fatal(err)
	}
	op.OperationKey = "move-processing-summary"
	req := MoveResourcesRequest{Sources: []ResourceRef{resourceRef(bound.Document)}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}}
	for i := 0; i < 2; i++ {
		result, err := s.Move(ctx, op, req)
		if err != nil || len(result.Items) != 1 || result.Items[0].ProcessingStatus != "submitted" || result.ProcessingSummary["submitted"] != 1 || result.ProcessingSummary["not_submitted"] != 0 {
			t.Fatalf("call %d: receipt summary diverged from item: %+v %v", i, result, err)
		}
	}
}

func TestKnowledgeFailedEnsureDoesNotRetry(t *testing.T) {
	_, db, op := resourceFixture(t)
	now := time.Now().UTC()
	row := orm.Document{ID: "failed", DatasetID: "source", DisplayName: "failed.pdf", DocumentType: "PDF", Ext: []byte(`{}`), BaseModel: orm.BaseModel{CreatedAt: now, UpdatedAt: now}}
	if e := db.Create(&row).Error; e != nil {
		t.Fatal(e)
	}
	state := newDocumentProcessingState("source", "failed", now)
	state.ParseStatus = "failed"
	state.ParseErrorCode = "INVALID_PDF"
	state.ParseErrorMessage = "Invalid PDF structure"
	if e := db.Create(&state).Error; e != nil {
		t.Fatal(e)
	}
	svc, e := NewDocumentService(DocumentServiceDeps{DB: db.DB})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		res, e := svc.EnsureDocumentParsed(httptest.NewRequest("POST", "/", nil), EnsureDocumentParsedRequest{UserID: op.Caller.UserID, DatasetID: "source", DocumentID: "failed", Caller: op.Caller})
		if e != nil || res.Status != "failed" || res.Error == nil || res.Error.Message != "Invalid PDF structure" {
			t.Fatalf("%+v %v", res, e)
		}
	}
	var n int64
	db.Model(&orm.Task{}).Count(&n)
	if n != 0 {
		t.Fatalf("opening failure created %d tasks", n)
	}
}

func TestKnowledgeDiagnosticRedactsSecretsAndClearsOnSuccess(t *testing.T) {
	msg := SafeProcessingMessage("fetch https://host/file?sig=secret failed token=private Authorization: Bearer bearer-value {\"api_key\":\"json-value\"}\nTraceback (most recent call last):\nstack")
	for _, text := range []string{"secret", "private", "bearer-value", "json-value", "Traceback", "stack"} {
		if strings.Contains(msg, text) {
			t.Fatalf("leaked %s: %s", text, msg)
		}
	}
	if processingDiagnostic(orm.DocumentProcessingState{ParseStatus: "succeeded"}, orm.Task{}, "SUCCESS", "OLD", errors.New("old failure").Error()) != nil {
		t.Fatal("success retained failure")
	}
}

func TestKnowledgeContentValidationReportsTheActualReason(t *testing.T) {
	s, db, op := resourceFixture(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	s.originals, _ = NewOriginalService(OriginalServiceDeps{DB: db.DB})
	path := filepath.Join(t.TempDir(), "incomplete.pdf")
	if e := os.WriteFile(path, []byte("%PDF-1.7\nincomplete"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := s.UploadLocalBatch(context.Background(), op, LocalUploadRequest{KnowledgeBaseID: "source", Files: []LocalUploadItem{{Path: path}}})
	if e != nil || len(result.Items) != 1 || result.Items[0].Error == nil || result.Items[0].Error.Code != "INVALID_ARGUMENT" || result.Items[0].Error.Message != "PDF is incomplete" {
		t.Fatalf("%+v %v", result, e)
	}
}

func TestKnowledgeInterruptedUploadResumesOnlyUncommittedFiles(t *testing.T) {
	s, db, op := resourceFixture(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	s.originals, _ = NewOriginalService(OriginalServiceDeps{DB: db.DB, SubmitProcessing: func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error) {
		calls++
		if calls == 1 {
			cancel()
		}
		return StartTaskResult{Status: "STARTED"}, nil
	}})
	req := LocalUploadRequest{KnowledgeBaseID: "source"}
	for _, name := range []string{"first.txt", "second.txt"} {
		path := filepath.Join(t.TempDir(), name)
		if e := os.WriteFile(path, []byte("fixture"), 0600); e != nil {
			t.Fatal(e)
		}
		req.Files = append(req.Files, LocalUploadItem{Path: path})
	}
	op.OperationKey = "interrupted"
	first, e := s.UploadLocalBatch(ctx, op, req)
	if e != nil || first.Summary.Succeeded != 1 || calls != 1 {
		t.Fatalf("%+v %v calls=%d", first, e, calls)
	}
	again, e := s.UploadLocalBatch(context.Background(), op, req)
	if e != nil || again.Summary.Succeeded != 2 || calls != 2 {
		t.Fatalf("%+v %v calls=%d", again, e, calls)
	}
	if again.Items[0].ResourceRef != first.Items[0].ResourceRef {
		t.Fatal("resume duplicated first document")
	}
	var count int64
	db.Model(&orm.Document{}).Where("deleted_at IS NULL").Count(&count)
	if count != 2 {
		t.Fatalf("documents=%d", count)
	}
}

func TestKnowledgeBatchCapacityAndReadOnlyDenyBeforeWrites(t *testing.T) {
	s, db, op := resourceFixture(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	s.originals, _ = NewOriginalService(OriginalServiceDeps{DB: db.DB})
	path := filepath.Join(t.TempDir(), "large.txt")
	if e := os.WriteFile(path, []byte("12345678"), 0600); e != nil {
		t.Fatal(e)
	}
	s.budget.MaxBytes = 4
	_, e := s.UploadLocalBatch(context.Background(), op, LocalUploadRequest{KnowledgeBaseID: "source", Files: []LocalUploadItem{{Path: path}}})
	var re *ResourceError
	if !errors.As(e, &re) || re.Code != "OPERATION_TOO_LARGE" {
		t.Fatalf("%v", e)
	}
	if e = db.AutoMigrate(&orm.ACLModel{}, &orm.UserGroupModel{}, &orm.ACLGroupModel{}); e != nil {
		t.Fatal(e)
	}
	if e = db.Create(&orm.ACLModel{ResourceType: acl.ResourceTypeDB, ResourceID: "source", GranteeType: acl.GranteeUser, TargetID: "reader", Permission: acl.PermissionDatasetRead}).Error; e != nil {
		t.Fatal(e)
	}
	op.Caller.UserID = "reader"
	if _, e = s.dataset(context.Background(), db.DB, op, "source", acl.PermissionDatasetRead); e != nil {
		t.Fatalf("reader cannot read: %v", e)
	}
	if _, e = s.Reparse(context.Background(), op, ReparseDocumentsRequest{KnowledgeBaseID: "source", DocumentIDs: []string{"any"}}); e == nil {
		t.Fatal("reader reparsed")
	}
	if _, e = s.UploadLocalBatch(context.Background(), op, LocalUploadRequest{KnowledgeBaseID: "source", Files: []LocalUploadItem{{Path: path}}}); e == nil {
		t.Fatal("reader uploaded")
	}
	var count int64
	db.Model(&orm.Document{}).Count(&count)
	if count != 0 {
		t.Fatal("rejected request wrote documents")
	}
	db.Model(&orm.Task{}).Count(&count)
	if count != 0 {
		t.Fatal("rejected request created tasks")
	}
}
