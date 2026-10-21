package knowledge_market

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func taskPageQuery(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	values := []int{1, 10}
	for i, name := range []string{"page", "page_size"} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || (i == 1 && value > 100) {
				common.ReplyAppErr(w, common.NewAppError(http.StatusBadRequest, common.ErrCodeInvalidParams, "Invalid pagination"))
				return 0, 0, false
			}
			values[i] = value
		}
	}
	return values[0], values[1], true
}

func clampTaskPage(page, size, total int) int {
	last := max(1, (total+size-1)/size)
	return min(page, last)
}

// MarketTaskFiles returns only this job's files. Frozen historical rows must
// never be reconstructed from the dataset's current execution state.
func MarketTaskFiles(w http.ResponseWriter, r *http.Request) {
	db, ok := requireDB(w)
	if !ok {
		return
	}
	page, size, ok := taskPageQuery(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "failed"
	}
	counts := map[string]int{"success": 0, "failed": 0, "canceled": 0, "unfinished": 0}
	if _, valid := counts[status]; !valid {
		common.ReplyAppErr(w, common.NewAppError(http.StatusBadRequest, common.ErrCodeInvalidParams, "Invalid file status"))
		return
	}
	userID := common.UserID(r)
	if userID == "" {
		common.ReplyAppErr(w, common.NewAppError(http.StatusUnauthorized, common.ErrCodeUnauthorized, "Authentication required"))
		return
	}
	var job orm.AsyncJob
	err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND job_type IN ?", common.PathVar(r, "job_id"), userID, marketJobTypes).Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyAppErr(w, common.NewAppError(http.StatusNotFound, common.ErrCodeResourceAbsent, "Task not found"))
		return
	}
	if err != nil {
		replyServiceError(w, err)
		return
	}
	install, err := loadInstall(r, db, userID, job.ResourceID)
	if err != nil {
		replyServiceError(w, err)
		return
	}
	_, parse := taskProgress(r, db, job, install)
	// A retry may freeze history while the live file states are being read.
	// Prefer that committed snapshot before returning the old job's file page.
	var latest orm.AsyncJob
	if err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ?", job.ID, userID).Take(&latest).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyAppErr(w, common.NewAppError(http.StatusNotFound, common.ErrCodeResourceAbsent, "Task not found"))
		} else {
			replyServiceError(w, err)
		}
		return
	}
	if !bytes.Equal(latest.ResultJSON, job.ResultJSON) {
		_, parse = taskProgress(r, db, latest, install)
	}
	counts["success"], counts["failed"], counts["canceled"] = parse.Done, parse.Failed, parse.Canceled
	counts["unfinished"] = parse.Pending + parse.Parsing + parse.Unknown
	items := make([]doc.MarketTaskFile, 0)
	for _, file := range parse.Files {
		if file.Status == status {
			items = append(items, file)
		}
	}
	total := counts[status]
	page = clampTaskPage(page, size, total)
	available := parse.Files != nil && len(parse.Files) == parse.Total
	if available {
		start := min((page-1)*size, len(items))
		items = items[start:min(start+size, len(items))]
	} else {
		items = []doc.MarketTaskFile{}
	}
	common.ReplyOK(w, map[string]any{"items": items, "page": page, "page_size": size, "total": total, "counts": counts, "file_details_available": available})
}
