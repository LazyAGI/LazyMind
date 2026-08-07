package resourcechange

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestRecordContentChangeSkipsUnchangedContent(t *testing.T) {
	db := newResourceChangeTestDB(t)
	change := testContentChange("memory-1", "same", "same", time.Now())
	if err := RecordContentChange(context.Background(), db, change); err != nil {
		t.Fatalf("record unchanged content: %v", err)
	}
	assertVersionCount(t, db, "memory-1", 0)
}

func TestRecordContentChangePersistsDiff(t *testing.T) {
	db := newResourceChangeTestDB(t)
	change := testContentChange("memory-1", "old memory\n", "new memory\n", time.Now())
	if err := RecordContentChange(context.Background(), db, change); err != nil {
		t.Fatalf("record content change: %v", err)
	}

	var row orm.ResourceVersion
	if err := db.Where("resource_id = ?", "memory-1").Take(&row).Error; err != nil {
		t.Fatalf("query resource version: %v", err)
	}
	if row.ChangeSource != ChangeSourceDirectSave {
		t.Fatalf("unexpected change_source %q", row.ChangeSource)
	}
	if !strings.Contains(row.Diff, "-old memory") || !strings.Contains(row.Diff, "+new memory") {
		t.Fatalf("expected diff to include old and new content, got %q", row.Diff)
	}
}

func TestRecordContentChangePrunesToThirtyVersions(t *testing.T) {
	db := newResourceChangeTestDB(t)
	base := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	for i := 0; i < MaxVersionsPerResource+5; i++ {
		change := testContentChange("memory-1", "before", "after-"+time.Duration(i).String(), base.Add(time.Duration(i)*time.Minute))
		if err := RecordContentChange(context.Background(), db, change); err != nil {
			t.Fatalf("record content change %d: %v", i, err)
		}
	}
	assertVersionCount(t, db, "memory-1", MaxVersionsPerResource)

	var rows []orm.ResourceVersion
	if err := db.Where("resource_id = ?", "memory-1").Order("created_at ASC").Find(&rows).Error; err != nil {
		t.Fatalf("query versions: %v", err)
	}
	if rows[0].CreatedAt.Before(base.Add(5 * time.Minute)) {
		t.Fatalf("expected oldest kept row to be after prune boundary, got %s", rows[0].CreatedAt)
	}
}

func TestListAndGetVersionsAreUserScoped(t *testing.T) {
	db := newResourceChangeTestDB(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now()
	for _, change := range []ContentChange{
		testContentChangeForUser("memory-1", "user-1", "old", "new", now),
		testContentChangeForUser("memory-2", "user-2", "old", "other", now),
	} {
		if err := RecordContentChange(context.Background(), db, change); err != nil {
			t.Fatalf("record content change: %v", err)
		}
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/core/resource-versions?resource_type=memory", nil)
	listReq.Header.Set("X-User-Id", "user-1")
	listRec := httptest.NewRecorder()
	ListVersions(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listRec.Code, listRec.Body.String())
	}
	var listResp struct {
		Data struct {
			Items []versionResponse `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Data.Items) != 1 || listResp.Data.Items[0].ResourceID != "memory-1" {
		t.Fatalf("unexpected list items: %#v", listResp.Data.Items)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/core/resource-versions/"+listResp.Data.Items[0].ID, nil)
	getReq = mux.SetURLVars(getReq, map[string]string{"version_id": listResp.Data.Items[0].ID})
	getReq.Header.Set("X-User-Id", "user-2")
	getRec := httptest.NewRecorder()
	GetVersion(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected other user to get 404, got %d body=%s", getRec.Code, getRec.Body.String())
	}
}

func newResourceChangeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, &orm.ResourceVersion{}).DB
}

func testContentChange(resourceID, beforeContent, afterContent string, changedAt time.Time) ContentChange {
	return testContentChangeForUser(resourceID, "user-1", beforeContent, afterContent, changedAt)
}

func testContentChangeForUser(resourceID, userID, beforeContent, afterContent string, changedAt time.Time) ContentChange {
	return ContentChange{
		ResourceType:  orm.ResourceUpdateResourceTypeMemory,
		ResourceID:    resourceID,
		UserID:        userID,
		FromVersion:   1,
		ToVersion:     2,
		BeforeContent: beforeContent,
		AfterContent:  afterContent,
		Source: Source{
			ChangeSource: ChangeSourceDirectSave,
			ChangedAt:    changedAt,
		},
	}
}

func assertVersionCount(t *testing.T, db *gorm.DB, resourceID string, want int64) {
	t.Helper()
	var got int64
	if err := db.Model(&orm.ResourceVersion{}).Where("resource_id = ?", resourceID).Count(&got).Error; err != nil {
		t.Fatalf("count resource versions: %v", err)
	}
	if got != want {
		t.Fatalf("expected %d resource versions, got %d", want, got)
	}
}

// TestBuildContentDiffIdentical returns empty diff for identical content.
func TestBuildContentDiffIdentical(t *testing.T) {
	diff, err := buildContentDiff("hello\nworld\n", "hello\nworld\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff != "" {
		t.Fatalf("expected empty diff, got %q", diff)
	}
}

// TestBuildContentDiffEmptyBefore renders diff with only new content.
func TestBuildContentDiffEmptyBefore(t *testing.T) {
	diff, err := buildContentDiff("", "new content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diff) == 0 {
		t.Fatal("expected non-empty diff")
	}
}

// TestBuildContentDiffEmptyAfter renders diff with only old content.
func TestBuildContentDiffEmptyAfter(t *testing.T) {
	diff, err := buildContentDiff("old content", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diff) == 0 {
		t.Fatal("expected non-empty diff")
	}
}

// TestLatestSummaryForResource returns the latest version summary.
func TestLatestSummaryForResource(t *testing.T) {
	db := newResourceChangeTestDB(t)
	ctx := context.Background()
	now := time.Now()
	// Insert two versions, the later one should be returned
	for i, bs := range []string{"before-v1", "before-v2"} {
		if err := RecordContentChange(ctx, db, ContentChange{
			ResourceType:  "memory",
			ResourceID:    "res-summary",
			UserID:        "user-1",
			FromVersion:   int64(i),
			ToVersion:     int64(i + 1),
			BeforeContent: bs,
			AfterContent:  "after-" + string(rune('a'+i)),
			Source: Source{
				ChangeSource: ChangeSourceDirectSave,
				ChangedAt:    now.Add(time.Duration(i) * time.Minute),
			},
		}); err != nil {
			t.Fatalf("record version %d: %v", i, err)
		}
	}

	summary, err := LatestSummaryForResource(ctx, db, "user-1", "memory", "res-summary")
	if err != nil {
		t.Fatalf("latest summary: %v", err)
	}
	if summary == nil {
		t.Fatal("expected non-nil summary")
	}
	if summary.ChangeSource != ChangeSourceDirectSave {
		t.Fatalf("change_source = %q", summary.ChangeSource)
	}
}

// TestLatestSummaryForResourceNotFound returns nil for nonexistent resource.
func TestLatestSummaryForResourceNotFound(t *testing.T) {
	db := newResourceChangeTestDB(t)
	summary, err := LatestSummaryForResource(context.Background(), db, "user-1", "memory", "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary != nil {
		t.Fatalf("expected nil summary, got %#v", summary)
	}
}

// TestLatestSummariesForResources batches multiple resource lookups.
func TestLatestSummariesForResources(t *testing.T) {
	db := newResourceChangeTestDB(t)
	ctx := context.Background()
	now := time.Now()
	for _, entry := range []struct{ rid, src string }{
		{"res-a", ChangeSourceDirectSave},
		{"res-b", ChangeSourceDraftConfirm},
	} {
		if err := RecordContentChange(ctx, db, testContentChangeForUser(entry.rid, "user-1", "before", "after", now)); err != nil {
			t.Fatalf("record %s: %v", entry.rid, err)
		}
	}

	summaries, err := LatestSummariesForResources(ctx, db, "user-1", "memory", []string{"res-a", "res-b", "res-c"})
	if err != nil {
		t.Fatalf("latest summaries: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("got %d summaries, want 2", len(summaries))
	}
	if _, ok := summaries["res-a"]; !ok {
		t.Fatal("res-a missing from summaries")
	}
	if _, ok := summaries["res-b"]; !ok {
		t.Fatal("res-b missing from summaries")
	}
}

// TestLatestSummariesForResourcesNilDB returns empty map.
func TestLatestSummariesForResourcesNilDB(t *testing.T) {
	summaries, err := LatestSummariesForResources(context.Background(), nil, "user-1", "memory", []string{"a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(summaries))
	}
}

// TestUpdateModel updates a row and records the content change.
func TestUpdateModel(t *testing.T) {
	db := newResourceChangeTestDB(t)
	ctx := context.Background()

	// Insert a seed row
	row := seedResourceVersion(t, db, "res-update", "before", "user-1")

	affected, err := UpdateModel(ctx, db, &orm.ResourceVersion{},
		func(q *gorm.DB) *gorm.DB { return q.Where("id = ?", row.ID) },
		map[string]any{"before_content": "after"},
		testContentChangeForUser("res-update", "user-1", "before", "after", time.Now()),
	)
	if err != nil {
		t.Fatalf("update model: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected = %d, want 1", affected)
	}

	// Verify the row was updated
	var updated orm.ResourceVersion
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("find updated row: %v", err)
	}
	if updated.BeforeContent != "after" {
		t.Fatalf("before_content = %q, want after", updated.BeforeContent)
	}
}

// TestUpdateModelNoRowsAffected returns 0 without error when nothing matches.
func TestUpdateModelNoRowsAffected(t *testing.T) {
	db := newResourceChangeTestDB(t)
	ctx := context.Background()

	affected, err := UpdateModel(ctx, db, &orm.ResourceVersion{},
		func(q *gorm.DB) *gorm.DB { return q.Where("id = ?", "nonexistent") },
		map[string]any{"before_content": "after"},
		testContentChangeForUser("res-none", "user-1", "before", "after", time.Now()),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if affected != 0 {
		t.Fatalf("affected = %d, want 0", affected)
	}
}

// TestDeleteModel deletes a row and records the content change.
func TestDeleteModel(t *testing.T) {
	db := newResourceChangeTestDB(t)
	ctx := context.Background()

	row := seedResourceVersion(t, db, "res-delete", "content", "user-1")

	affected, err := DeleteModel(ctx, db, &orm.ResourceVersion{},
		func(q *gorm.DB) *gorm.DB { return q.Where("id = ?", row.ID) },
		testContentChangeForUser("res-delete", "user-1", "content", "", time.Now()),
	)
	if err != nil {
		t.Fatalf("delete model: %v", err)
	}
	if affected != 1 {
		t.Fatalf("affected = %d, want 1", affected)
	}

	// Verify the row is gone
	var result orm.ResourceVersion
	err = db.Where("id = ?", row.ID).Take(&result).Error
	if err == nil {
		t.Fatal("expected record not found after delete")
	}
}

// TestDeleteModelNoRowsAffected returns 0 when nothing matches.
func TestDeleteModelNoRowsAffected(t *testing.T) {
	db := newResourceChangeTestDB(t)
	ctx := context.Background()

	affected, err := DeleteModel(ctx, db, &orm.ResourceVersion{},
		func(q *gorm.DB) *gorm.DB { return q.Where("id = ?", "nonexistent") },
		testContentChangeForUser("res-none", "user-1", "content", "", time.Now()),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if affected != 0 {
		t.Fatalf("affected = %d, want 0", affected)
	}
}

// seedResourceVersion inserts a ResourceVersion row for testing Update/Delete.
func seedResourceVersion(t *testing.T, db *gorm.DB, resourceID, content, userID string) orm.ResourceVersion {
	t.Helper()
	row := orm.ResourceVersion{
		ID:            resourceID + "-id",
		ResourceType:  "memory",
		ResourceID:    resourceID,
		UserID:        userID,
		ChangeSource:  ChangeSourceDirectSave,
		FromVersion:   1,
		ToVersion:     2,
		BeforeContent: content,
		AfterContent:  content,
		Diff:          "",
		CreatedAt:     time.Now(),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed version %s: %v", resourceID, err)
	}
	return row
}
