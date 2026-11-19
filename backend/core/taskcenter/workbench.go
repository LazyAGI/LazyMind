package taskcenter

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const defaultWorkbenchPageSize = 4

var workbenchCategories = []string{"attention", "running", "paused", "failed", "recent", "canceled"}

type workbenchGroup struct {
	Items []taskResponse `json:"items"`
	Total int            `json:"total"`
	Page  int            `json:"page"`
}

func workbenchCategory(task orm.TaskCenterTask, now time.Time) string {
	switch task.Status {
	case "pending", "waiting", "waiting_inputs":
		return "attention"
	case "running", "paused", "canceled":
		return task.Status
	case "failed", "interrupted":
		return "failed"
	case "succeeded", "completed":
		finished := task.UpdatedAt
		if task.FinishedAt != nil {
			finished = *task.FinishedAt
		}
		if !finished.Before(now.Add(-7*24*time.Hour)) && !finished.After(now) {
			return "recent"
		}
	}
	return ""
}

// Workbench resolves each matching task once and only enriches the displayed
// previews. Counts and cards share the same live status and seven-day boundary.
func Workbench(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "user not found", http.StatusUnauthorized)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "db unavailable", http.StatusInternalServerError)
		return
	}
	q := r.URL.Query()
	category := strings.TrimSpace(q.Get("category"))
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize < 1 {
		pageSize = defaultWorkbenchPageSize
	}
	pageSize = min(pageSize, 100)
	groups := make(map[string]*workbenchGroup, len(workbenchCategories))
	for _, key := range workbenchCategories {
		groups[key] = &workbenchGroup{Items: []taskResponse{}, Page: 1}
	}
	if category != "" && groups[category] == nil {
		common.ReplyErr(w, "invalid workbench category", http.StatusBadRequest)
		return
	}
	rows, err := loadTaskRows(r.Context(), db, userID, strings.TrimSpace(q.Get("task_type")), strings.TrimSpace(q.Get("keyword")))
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	groupRows := make(map[string][]taskListRow, len(groups))
	now := time.Now().UTC()
	for _, row := range rows {
		task, err := resolveTaskForResponse(r.Context(), db, row.TaskCenterTask)
		if err != nil {
			common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		key := workbenchCategory(task, now)
		if key != "" {
			row.TaskCenterTask = task
			groupRows[key] = append(groupRows[key], row)
		}
	}
	for key, group := range groups {
		matching := groupRows[key]
		group.Total = len(matching)
		if key == category {
			group.Page = min(page, max(1, (group.Total+pageSize-1)/pageSize))
		}
		start := (group.Page - 1) * pageSize
		for _, row := range matching[start:min(start+pageSize, group.Total)] {
			group.Items = append(group.Items, taskListResponse(r.Context(), db, row, row.TaskCenterTask))
		}
	}
	common.ReplyJSON(w, map[string]any{"groups": groups, "page_size": pageSize})
}
