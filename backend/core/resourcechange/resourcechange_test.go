package resourcechange

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "resourcechange.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.ResourceVersion{}); err != nil {
		t.Fatalf("auto migrate resource versions: %v", err)
	}
	return db.DB
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
