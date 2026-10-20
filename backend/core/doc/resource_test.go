package doc

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"os"
	"strings"
	"testing"
	"time"
)

func TestResourceMetadataRejectsMissingAndWrongFields(t *testing.T) {
	db := newDocumentTestDB(t)
	installDatasetCatalogScanTransport(t)
	if err := db.AutoMigrate(&orm.ResourceOperationReceipt{}); err != nil {
		t.Fatal(err)
	}
	s, err := NewResourceService(ResourceServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	op := ResourceOperationContext{Caller: DatasetCatalogCaller{UserID: "user-1", Permissions: []string{"document.write"}}}
	name := "new"
	_, err = s.UpdateMetadata(context.Background(), op, UpdateMetadataRequest{Items: []ResourceMetadataItem{{Target: ResourceRef{Kind: "document", KnowledgeBaseID: "missing", DocumentID: "missing"}, Changes: ResourceMetadataChanges{Name: &name}}}})
	if err == nil {
		t.Fatal("missing resource accepted")
	}
	var n int64
	db.Model(&orm.Document{}).Count(&n)
	if n != 0 {
		t.Fatal("PATCH created document")
	}
}

func resourceFixture(t *testing.T) (*ResourceService, *orm.DB, ResourceOperationContext) {
	t.Helper()
	db := newDocumentTestDB(t)
	installOriginalSourceTransport(t)
	if err := db.AutoMigrate(&orm.ResourceOperationReceipt{}, &orm.DocumentProcessingState{}); err != nil {
		t.Fatal(err)
	}
	seedDocumentListDataset(t, db, "source", "user-1")
	seedDocumentListDataset(t, db, "target", "user-1")
	s, err := NewResourceService(ResourceServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, ResourceOperationContext{Caller: DatasetCatalogCaller{UserID: "user-1", Permissions: []string{"document.write"}}}
}
func TestResourceFoldersReplayConflictAndRollback(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	op.OperationKey = "folders"
	req := CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"papers/empty", "papers/notes"}}
	first, err := s.CreateFolders(ctx, op, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.CreateFolders(ctx, op, req)
	if err != nil || again.Items[0].ResourceRef != first.Items[0].ResourceRef {
		t.Fatalf("replay lost ID: %+v %v", again, err)
	}
	req.Folders = []string{"different"}
	if _, err = s.CreateFolders(ctx, op, req); err == nil {
		t.Fatal("changed idempotency parameters accepted")
	}
	op.OperationKey = ""
	_, err = s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"new", "papers"}})
	if err == nil {
		t.Fatal("conflicting folder accepted")
	}
	var n int64
	db.Model(&orm.Document{}).Where("display_name = ?", "new").Count(&n)
	if n != 0 {
		t.Fatal("partial folder transaction committed")
	}
}
func TestResourceMoveRootAndRejectCycle(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	created, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"parent/child"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, child := created.Items[0].ResourceRef, created.Items[1].ResourceRef
	if _, err = s.Move(ctx, op, MoveResourcesRequest{Sources: []ResourceRef{parent}, Destination: child}); err == nil {
		t.Fatal("cycle accepted")
	}
	result, err := s.Move(ctx, op, MoveResourcesRequest{Sources: []ResourceRef{child}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "source"}})
	if err != nil {
		t.Fatal(err)
	}
	var row orm.Document
	db.First(&row, "id = ?", child.ID())
	if row.PID != "" || result.Items[0].ParentID != "" {
		t.Fatal("move to root ignored empty parent")
	}
}
func TestResourceSnapshotRejectsNewDescendantAndDeleteRetainsHistory(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	created, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"parent"}})
	if err != nil {
		t.Fatal(err)
	}
	root := created.Items[0].ResourceRef
	snap, err := s.Snapshot(ctx, op, []ResourceRef{root})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", ParentID: root.ID(), Folders: []string{"new"}})
	if err != nil {
		t.Fatal(err)
	}
	op.ExpectedSnapshot = snap.Digest
	if _, err = s.Delete(ctx, op, DeleteResourcesRequest{Targets: []ResourceRef{root}}); err == nil {
		t.Fatal("stale confirmation accepted")
	}
	op.ExpectedSnapshot = ""
	op.OperationKey = "delete"
	result, err := s.Delete(ctx, op, DeleteResourcesRequest{Targets: []ResourceRef{root}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Items[0].FolderCount != 2 || result.Items[0].CleanupStatus != "submitted" {
		t.Fatalf("bad receipt %+v", result)
	}
	again, err := s.Delete(ctx, op, DeleteResourcesRequest{Targets: []ResourceRef{root}})
	if err != nil || again.Items[0].FolderCount != 2 {
		t.Fatalf("deleted replay %+v %v", again, err)
	}
	var n int64
	db.Model(&orm.Document{}).Where("deleted_at IS NULL").Count(&n)
	if n != 0 {
		t.Fatal("deleted children remain active")
	}
}
func TestResourceMetadataTypeTenantAndScope(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	created, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"folder"}})
	if err != nil {
		t.Fatal(err)
	}
	desc := "forbidden"
	if _, err = s.UpdateMetadata(ctx, op, UpdateMetadataRequest{Items: []ResourceMetadataItem{{Target: created.Items[0].ResourceRef, Changes: ResourceMetadataChanges{Description: &desc}}}}); err == nil {
		t.Fatal("folder description accepted")
	}
	op.AllowedDatasetIDs = []string{}
	if _, err = s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"denied"}}); err == nil {
		t.Fatal("empty scope allowed")
	}
	op.AllowedDatasetIDs = nil
	db.Model(&orm.Dataset{}).Where("id = ?", "source").Update("tenant_id", "other")
	if _, err = s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"denied"}}); err == nil {
		t.Fatal("cross tenant allowed")
	}
}
func TestResourceTreeBudgetLeavesSourceUntouched(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	created, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"parent/one", "parent/two"}})
	if err != nil {
		t.Fatal(err)
	}
	s.budget.MaxResources = 2
	s.budget.Calibrated = true
	_, err = s.Move(ctx, op, MoveResourcesRequest{Sources: []ResourceRef{created.Items[0].ResourceRef}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}})
	var typed *ResourceError
	if !errors.As(err, &typed) || typed.Code != "OPERATION_TOO_LARGE" {
		t.Fatalf("expected budget error, got %v", err)
	}
	var n int64
	db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "source").Count(&n)
	if n != 3 {
		t.Fatal("budget failure changed source")
	}
}
func TestResourceCrossMovePreservesTreeOriginalAndReceipt(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	originals, err := NewOriginalService(OriginalServiceDeps{DB: db.DB, SubmitProcessing: func(context.Context, DatasetCatalogCaller, string, string) (StartTaskResult, error) {
		return StartTaskResult{Status: "STARTED"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.originals = originals
	s.budget.Calibrated = true
	created, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"root/empty"}})
	if err != nil {
		t.Fatal(err)
	}
	root := created.Items[0].ResourceRef
	file, err := originals.AcquireOriginal(ctx, op.Caller, AcquireOriginalRequest{Filename: "note.txt", ContentType: "text/plain", Reader: strings.NewReader("immutable original")})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := originals.ImportOriginal(ctx, op.Caller, ImportOriginalRequest{FileRef: file.FileRef, DatasetID: "source", ParentID: root.ID()})
	if err != nil {
		t.Fatal(err)
	}
	op.OperationKey = "cross"
	request := MoveResourcesRequest{Sources: []ResourceRef{root, resourceRef(bound.Document)}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}}
	result, err := s.Move(ctx, op, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || len(result.Items[0].IDMapping) != 3 {
		t.Fatalf("tree mapping %+v", result)
	}
	if result.Items[0].DocumentCount != 1 || result.Items[0].FolderCount != 2 || result.Items[0].ProcessingStatus != "submitted" {
		t.Fatalf("bad state %+v", result)
	}
	newID := result.Items[0].IDMapping[bound.Document.ID]
	var target orm.Document
	if err = db.First(&target, "id = ?", newID).Error; err != nil {
		t.Fatal(err)
	}
	var ext documentExt
	if err = json.Unmarshal(target.Ext, &ext); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ext.StoredPath)
	if err != nil || string(data) != "immutable original" {
		t.Fatalf("original not retained: %s %v", data, err)
	}
	var active int64
	db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "source").Count(&active)
	if active != 0 {
		t.Fatal("source still active")
	}
	again, err := s.Move(ctx, op, request)
	if err != nil || again.Items[0].IDMapping[bound.Document.ID] != newID {
		t.Fatalf("move replay lost target: %+v %v", again, err)
	}
}

func TestResourceConcurrentFolderNameHasOneWinner(t *testing.T) {
	s, db, op := resourceFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"contended"}})
			results <- err
		}()
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			success++
		}
	}
	var n int64
	db.Model(&orm.Document{}).Where("dataset_id = ? AND display_name = ? AND deleted_at IS NULL", "source", "contended").Count(&n)
	if success != 1 || n != 1 {
		t.Fatalf("concurrent conflict: successes=%d rows=%d", success, n)
	}
}
func TestResourceSourcePolicyRunsBeforeTransaction(t *testing.T) {
	s, _, op := resourceFixture(t)
	calls := 0
	s.sourceAccess = func(context.Context, DatasetCatalogCaller, string, string) error { calls++; return nil }
	_, err := s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"one/two"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("source policy called %d times; transaction must reuse prepared policy", calls)
	}
}
func TestResourceCrossMovePreparationFailureKeepsWholeTree(t *testing.T) {
	s, db, op := resourceFixture(t)
	ctx := context.Background()
	s.budget.Calibrated = true
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	originals, err := NewOriginalService(OriginalServiceDeps{DB: db.DB})
	if err != nil {
		t.Fatal(err)
	}
	s.originals = originals
	created, err := s.CreateFolders(ctx, op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"root/empty"}})
	if err != nil {
		t.Fatal(err)
	}
	seedDocumentListDoc(t, db, "source", "missing-original", "missing.txt", time.Now().UTC(), "user-1", nil)
	db.Model(&orm.Document{}).Where("id = ?", "missing-original").Update("p_id", created.Items[0].ResourceRef.ID())
	_, err = s.Move(ctx, op, MoveResourcesRequest{Sources: []ResourceRef{created.Items[0].ResourceRef}, Destination: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: "target"}})
	if err == nil {
		t.Fatal("missing original accepted")
	}
	var source, target int64
	db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "source").Count(&source)
	db.Model(&orm.Document{}).Where("dataset_id = ? AND deleted_at IS NULL", "target").Count(&target)
	if source != 3 || target != 0 {
		t.Fatalf("partial tree move: source=%d target=%d", source, target)
	}
}

func TestResourceSharedWriterCanUploadButCannotDeleteLibrary(t *testing.T) {
	s, db, op := resourceFixture(t)
	if err := db.AutoMigrate(&orm.ACLModel{}, &orm.UserGroupModel{}, &orm.ACLGroupModel{}); err != nil {
		t.Fatal(err)
	}
	acl.InitStore(db)
	if err := db.Create(&orm.ACLModel{ResourceType: acl.ResourceTypeDB, ResourceID: "source", GranteeType: acl.GranteeUser, TargetID: "collaborator", Permission: acl.PermissionDatasetWrite}).Error; err != nil {
		t.Fatal(err)
	}
	op.Caller.UserID = "collaborator"
	if _, err := s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"shared"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete(context.Background(), op, DeleteResourcesRequest{Targets: []ResourceRef{{Kind: "knowledge_base", KnowledgeBaseID: "source"}}}); err == nil {
		t.Fatal("writer deleted whole library")
	}
}
func TestResourceFailedTransactionHasNoReceipt(t *testing.T) {
	s, db, op := resourceFixture(t)
	op.OperationKey = "failure"
	if err := db.Callback().Create().Before("gorm:create").Register("test:reject-resource-create", func(tx *gorm.DB) {
		if tx.Statement.Table == "documents" {
			tx.AddError(errors.New("injected database failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("test:reject-resource-create")
	if _, err := s.CreateFolders(context.Background(), op, CreateFoldersRequest{KnowledgeBaseID: "source", Folders: []string{"no-commit"}}); err == nil {
		t.Fatal("injected failure did not abort")
	}
	var documents, receipts int64
	db.Model(&orm.Document{}).Count(&documents)
	db.Model(&orm.ResourceOperationReceipt{}).Count(&receipts)
	if documents != 0 || receipts != 0 {
		t.Fatalf("failed operation committed rows: docs=%d receipts=%d", documents, receipts)
	}
}
