package taskcenter

import (
	"fmt"
	"net/http"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func workflowCursor(db *gorm.DB, owner string) (int64, error) {
	var cursor int64
	err := db.Model(&orm.WorkflowEvent{}).Select("COALESCE(MAX(id), 0)").
		Where("owner_user_id = ?", owner).Scan(&cursor).Error
	return cursor, err
}

// WorkflowChanges multiplexes invalidations for the owner's workflows. Clients
// reload their authoritative list on connect/reconnect, so no event replay or
// per-task SSE connections are needed here.
func WorkflowChanges(w http.ResponseWriter, r *http.Request) {
	owner := common.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		common.ReplyErr(w, "stream unavailable", http.StatusInternalServerError)
		return
	}
	db := store.DB().WithContext(r.Context())
	cursor, err := workflowCursor(db, owner)
	if err != nil {
		common.ReplyErr(w, "workflow state unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := fmt.Fprint(w, "event: changed\ndata: {}\n\n"); err != nil {
		return
	}
	flusher.Flush()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			next, err := workflowCursor(db, owner)
			if err != nil {
				return
			} // closing the stream makes the client reconnect and reload
			if next == cursor {
				continue
			}
			cursor = next
			if _, err := fmt.Fprint(w, "event: changed\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
