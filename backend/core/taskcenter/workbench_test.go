package taskcenter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestWorkbenchCategoryAndRecentBoundary(t *testing.T) {
	now := time.Now().UTC()
	for status, want := range map[string]string{
		"pending": "attention", "waiting": "attention", "waiting_inputs": "attention",
		"running": "running", "paused": "paused", "failed": "failed", "interrupted": "failed",
		"succeeded": "recent", "completed": "recent", "canceled": "canceled", "skipped": "",
	} {
		if got := workbenchCategory(orm.TaskCenterTask{Status: status, UpdatedAt: now}, now); got != want {
			t.Errorf("%s: got %q want %q", status, got, want)
		}
	}
	for _, tc := range []struct {
		age  time.Duration
		want string
	}{
		{7 * 24 * time.Hour, "recent"}, {7*24*time.Hour + time.Second, ""}, {-time.Second, ""},
	} {
		finished := now.Add(-tc.age)
		if got := workbenchCategory(orm.TaskCenterTask{Status: "succeeded", UpdatedAt: now, FinishedAt: &finished}, now); got != tc.want {
			t.Errorf("finished %v: got %q want %q", finished, got, tc.want)
		}
	}
}

func TestWorkbenchSummariesPreviewsAndPagination(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskCenterTask{}, &orm.Conversation{}, &orm.UserSchedule{}, &orm.WorkflowSession{}, &orm.SubAgentTask{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC().Add(-time.Minute)
	var tasks []orm.TaskCenterTask
	for i := range 1000 {
		tasks = append(tasks, orm.TaskCenterTask{ID: fmt.Sprintf("done-%04d", i), UserID: "owner", TaskType: "background_chat", Status: "succeeded", CreatedAt: now, UpdatedAt: now})
	}
	for i := range 6 {
		title := fmt.Sprintf("matching %d", i)
		tasks = append(tasks, orm.TaskCenterTask{ID: fmt.Sprintf("waiting-%d", i), UserID: "owner", TaskType: "background_chat", Status: "waiting", Title: &title, CreatedAt: now.Add(-time.Hour), UpdatedAt: now})
	}
	title := "matching failed"
	tasks = append(tasks,
		orm.TaskCenterTask{ID: "failed", UserID: "owner", TaskType: "scheduled", Status: "failed", Title: &title, CreatedAt: now, UpdatedAt: now},
		orm.TaskCenterTask{ID: "other-user", UserID: "other", TaskType: "background_chat", Status: "waiting", CreatedAt: now, UpdatedAt: now},
		orm.TaskCenterTask{ID: "archived", UserID: "owner", TaskType: "background_chat", Status: "waiting", CreatedAt: now, UpdatedAt: now, ArchivedAt: &now},
	)
	if err := db.CreateInBatches(tasks, 50).Error; err != nil {
		t.Fatal(err)
	}
	listReads, stepReads := 0, 0
	if err := db.Callback().Query().After("gorm:query").Register("count-workbench-reads", func(tx *gorm.DB) {
		if tx.Statement.Table == "tct" {
			listReads++
		}
		if tx.Statement.Table == "sub_agent_tasks" {
			stepReads++
		}
	}); err != nil {
		t.Fatal(err)
	}
	read := func(query string, expectedPageSize ...int) map[string]*workbenchGroup {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/task-center/workbench"+query, nil)
		req.Header.Set("X-User-Id", "owner")
		rec := httptest.NewRecorder()
		Workbench(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var response struct {
			Groups   map[string]*workbenchGroup `json:"groups"`
			PageSize int                        `json:"page_size"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		wantSize := 4
		if len(expectedPageSize) > 0 {
			wantSize = expectedPageSize[0]
		}
		if response.PageSize != wantSize || len(response.Groups) != 6 {
			t.Fatalf("unexpected response: %s", rec.Body.String())
		}
		return response.Groups
	}
	groups := read("")
	if groups["recent"].Total != 1000 || len(groups["recent"].Items) != 4 || groups["attention"].Total != 6 || len(groups["attention"].Items) != 4 {
		t.Fatalf("incorrect totals/previews: %#v", groups)
	}
	if listReads != 1 || stepReads != 9 {
		t.Fatalf("expected one list read and only nine preview enrichments, got %d/%d", listReads, stepReads)
	}
	groups = read("?category=attention&page=2")
	if groups["attention"].Page != 2 || len(groups["attention"].Items) != 2 || groups["attention"].Items[0].ID != "waiting-1" {
		t.Fatalf("incorrect second page: %#v", groups["attention"])
	}
	groups = read("?keyword=matching")
	if groups["attention"].Total != 6 || groups["failed"].Total != 1 || groups["recent"].Total != 0 {
		t.Fatal("search must span all categories")
	}
	groups = read("?keyword=matching&task_type=scheduled")
	if groups["attention"].Total != 0 || groups["failed"].Total != 1 {
		t.Fatal("source filter not applied")
	}
	groups = read("?category=attention&page=999999")
	if groups["attention"].Page != 2 {
		t.Fatal("page should clamp to the last nonempty page")
	}
	groups = read("?page_size=6", 6)
	if len(groups["attention"].Items) != 6 || len(groups["recent"].Items) != 6 {
		t.Fatal("larger pages must return all requested previews")
	}
	groups = read("?category=attention&page=2&page_size=3", 3)
	if groups["attention"].Page != 2 || len(groups["attention"].Items) != 3 || groups["attention"].Items[0].ID != "waiting-2" {
		t.Fatalf("incorrect adaptive second page: %#v", groups["attention"])
	}
	groups = read("?category=attention&page=999&page_size=6", 6)
	if groups["attention"].Page != 1 || len(groups["attention"].Items) != 6 {
		t.Fatal("page clamping must use the requested page size")
	}
	groups = read("?page_size=999", 100)
	if len(groups["recent"].Items) != 100 {
		t.Fatal("page size must be bounded")
	}
	for _, invalid := range []string{"0", "-1", "invalid"} {
		read("?page_size=" + invalid)
	}
}
