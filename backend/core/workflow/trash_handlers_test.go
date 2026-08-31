package workflow

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
)

func workflowTrashRequest(method, path, draftID string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"draft_id": draftID})
	return httptest.NewRecorder(), req
}

func TestWorkflowTrashRestoreAndPurgeLogicalUnit(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	draftID := "b17f6f2a-7d80-46d9-a0c9-2ab48b9ec15f"
	draft := orm.WorkflowDraft{
		ID: draftID, Name: "产品评审", WorkflowID: "product-review", CreatedBy: "user-1",
		Version: 1, ScriptsContent: "{}", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	resource := orm.WorkflowResource{
		ID: "resource-1", WorkflowRef: "user:user-1:product-review", WorkflowID: "product-review",
		OwnerUserID: "user-1", OwnerScope: "user-1", RelativeRoot: "workflows/user-1/product-review",
		Name: "产品评审", HeadRevisionID: "revision-1", Version: 1, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{
		ID: "revision-1", WorkflowResourceID: resource.ID, RevisionNo: 1, TreeHash: "tree-1",
		CreatedBy: "user-1", CreatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	hash := "blob-1"
	if err := db.Create(&orm.WorkflowBlob{Hash: hash, Size: 4, Content: []byte("test"), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevisionEntry{
		RevisionID: "revision-1", Path: "workflow.yaml", BlobHash: &hash, Size: 4,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserWorkflowSetting{
		UserID: "user-1", WorkflowRef: resource.WorkflowRef, Enabled: true, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	deleteRec, deleteReq := workflowTrashRequest(http.MethodDelete, "/workflow-drafts/"+draftID, draftID)
	DeleteWorkflowDraft(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("trash status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if err := db.First(&draft, "id = ?", draftID).Error; err != nil || draft.DeletedAt == nil || draft.PublishedStatusBeforeTrash != "active" {
		t.Fatalf("draft trash state=%#v err=%v", draft, err)
	}
	if err := db.First(&resource, "id = ?", resource.ID).Error; err != nil || resource.Status != "archived" {
		t.Fatalf("resource trash state=%#v err=%v", resource, err)
	}

	restoreRec, restoreReq := workflowTrashRequest(http.MethodPost, "/workflow-drafts/"+draftID+":restore", draftID)
	RestoreWorkflowDraft(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", restoreRec.Code, restoreRec.Body.String())
	}
	var restoredDraft orm.WorkflowDraft
	if err := db.First(&restoredDraft, "id = ?", draftID).Error; err != nil || restoredDraft.DeletedAt != nil || restoredDraft.PublishedStatusBeforeTrash != "" {
		t.Fatalf("draft restore state=%#v err=%v", restoredDraft, err)
	}
	var restoredResource orm.WorkflowResource
	if err := db.First(&restoredResource, "id = ?", resource.ID).Error; err != nil || restoredResource.Status != "active" {
		t.Fatalf("resource restore state=%#v err=%v", restoredResource, err)
	}

	deleteRec, deleteReq = workflowTrashRequest(http.MethodDelete, "/workflow-drafts/"+draftID, draftID)
	DeleteWorkflowDraft(deleteRec, deleteReq)
	purgeRec, purgeReq := workflowTrashRequest(http.MethodDelete, "/workflow-drafts/"+draftID+":purge", draftID)
	PurgeWorkflowDraft(purgeRec, purgeReq)
	if purgeRec.Code != http.StatusOK {
		t.Fatalf("purge status=%d body=%s", purgeRec.Code, purgeRec.Body.String())
	}
	checks := []struct {
		model any
		where string
		arg   any
	}{
		{&orm.WorkflowDraft{}, "id = ?", draftID},
		{&orm.WorkflowResource{}, "id = ?", resource.ID},
		{&orm.WorkflowRevision{}, "id = ?", "revision-1"},
		{&orm.WorkflowRevisionEntry{}, "revision_id = ?", "revision-1"},
		{&orm.WorkflowBlob{}, "hash = ?", hash},
		{&orm.UserWorkflowSetting{}, "plugin_ref = ?", resource.WorkflowRef}, // workflow-naming: persistence
	}
	for _, check := range checks {
		var count int64
		if err := db.Model(check.model).Where(check.where, check.arg).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("remaining %T count=%d err=%v", check.model, count, err)
		}
	}
}

func TestWorkflowTrashConflictAndPurgePreserveReplacement(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	trashed := orm.WorkflowDraft{
		ID: "old-draft", Name: "旧工作流", WorkflowID: "shared-workflow", CreatedBy: "user-1",
		Version: 1, ScriptsContent: "{}", DeletedAt: &now, TrashExpiresAt: ptrTime(now.Add(30 * 24 * time.Hour)),
		PublishedStatusBeforeTrash: "active", CreatedAt: now, UpdatedAt: now,
	}
	replacement := orm.WorkflowDraft{
		ID: "new-draft", Name: "新工作流", WorkflowID: "shared-workflow", CreatedBy: "user-1",
		Version: 1, ScriptsContent: "{}", CreatedAt: now, UpdatedAt: now,
	}
	for _, draft := range []*orm.WorkflowDraft{&trashed, &replacement} {
		if err := db.Create(draft).Error; err != nil {
			t.Fatal(err)
		}
	}
	resource := orm.WorkflowResource{
		ID: "shared-resource", WorkflowRef: "user:user-1:shared-workflow", WorkflowID: "shared-workflow",
		OwnerUserID: "user-1", OwnerScope: "user-1", RelativeRoot: "workflows/user-1/shared-workflow",
		Name: "新工作流", Version: 1, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}

	restoreRec, restoreReq := workflowTrashRequest(http.MethodPost, "/workflow-drafts/old-draft:restore", trashed.ID)
	RestoreWorkflowDraft(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusConflict {
		t.Fatalf("restore conflict status=%d body=%s", restoreRec.Code, restoreRec.Body.String())
	}

	purgeRec, purgeReq := workflowTrashRequest(http.MethodDelete, "/workflow-drafts/old-draft:purge", trashed.ID)
	PurgeWorkflowDraft(purgeRec, purgeReq)
	if purgeRec.Code != http.StatusOK {
		t.Fatalf("purge old draft status=%d body=%s", purgeRec.Code, purgeRec.Body.String())
	}
	var draftCount, resourceCount int64
	db.Model(&orm.WorkflowDraft{}).Where("id = ?", replacement.ID).Count(&draftCount)
	db.Model(&orm.WorkflowResource{}).Where("id = ?", resource.ID).Count(&resourceCount)
	if draftCount != 1 || resourceCount != 1 {
		t.Fatalf("replacement after purge: draft=%d resource=%d", draftCount, resourceCount)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }
