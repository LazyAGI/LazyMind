package workflow

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestEnrichSessionHeadRevisionNosKeepsPinnedRevision(t *testing.T) {
	db := newTestDB(t)
	if err := db.DB.AutoMigrate(&orm.WorkflowResource{}, &orm.WorkflowRevision{}); err != nil {
		t.Fatalf("migrate plugin versions: %v", err)
	}
	now := time.Now().UTC()
	resource := orm.WorkflowResource{ID: "resource-1", WorkflowRef: "user:writer", WorkflowID: "writer-plugin", Version: 15, CreatedAt: now, UpdatedAt: now}
	pinned := orm.WorkflowRevision{ID: "revision-12", WorkflowResourceID: resource.ID, RevisionNo: 12, TreeHash: "tree-12", CreatedAt: now}
	if err := db.DB.Create(&resource).Error; err != nil {
		t.Fatalf("create resource: %v", err)
	}
	if err := db.DB.Create(&pinned).Error; err != nil {
		t.Fatalf("create revision: %v", err)
	}
	session := toSessionDTO(&orm.WorkflowSession{ID: "session-1", WorkflowID: "writer-plugin", WorkflowRevisionID: pinned.ID, WorkflowRevisionNo: pinned.RevisionNo})
	got := enrichSessionHeadRevisionNos(context.Background(), db.DB, []sessionDTO{session})[0]
	if got.PinnedRevisionID != pinned.ID || got.PinnedRevisionNo != 12 {
		t.Fatalf("pinned revision changed: id=%q no=%d", got.PinnedRevisionID, got.PinnedRevisionNo)
	}
	if got.HeadRevisionNo == nil || *got.HeadRevisionNo != 15 {
		t.Fatalf("head_revision_no = %v, want 15", got.HeadRevisionNo)
	}
}
