package agent

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func DeleteThread(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	threadID := strings.TrimSpace(mux.Vars(r)["thread_id"])
	if _, err := loadUserThread(db, r, threadID); err != nil {
		replyThreadLoadError(w, err)
		return
	}

	flowStatus, err := fetchThreadFlowStatus(r.Context(), r, threadID)
	upstreamMissing := false
	if err != nil {
		if isThreadFlowNotFound(err) {
			upstreamMissing = true
		} else {
			common.ReplyErrWithData(w, "fetch thread flow status failed", map[string]any{"detail": err.Error()}, http.StatusBadGateway)
			return
		}
	}

	cancelRequested := false
	if !upstreamMissing && isThreadFlowRunning(flowStatus) {
		proxy, statusCode, err := newEvoClient(forwardedUpstreamHeaders(r)).PostCommand(r.Context(), threadID, "cancel", map[string]any{})
		if err != nil || statusCode < 200 || statusCode >= 300 {
			detail := ""
			if err != nil {
				detail = err.Error()
			} else if proxy != nil {
				detail = strings.TrimSpace(string(proxy.BodyBytes))
			}
			replyStatus := statusCode
			if replyStatus < 400 || replyStatus >= 500 {
				replyStatus = http.StatusBadGateway
			}
			common.ReplyErrWithData(w, "cancel running thread failed", map[string]any{
				"detail":      detail,
				"flow_status": flowStatus,
			}, replyStatus)
			return
		}
		cancelRequested = true
	}

	var upstreamDelete map[string]any
	if upstreamMissing {
		upstreamDelete = map[string]any{"missing": true}
	} else if err := newEvoClient(forwardedUpstreamHeaders(r)).DeleteThread(r.Context(), threadID, &upstreamDelete); err != nil {
		if isThreadFlowNotFound(err) {
			upstreamDelete = map[string]any{"missing": true}
		} else {
			common.ReplyErrWithData(w, "delete upstream thread failed", map[string]any{"detail": err.Error()}, http.StatusBadGateway)
			return
		}
	}
	result, err := deleteThreadLocalRows(db, threadID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "delete thread failed", err), http.StatusInternalServerError)
		return
	}
	if cancelRequested {
		result["cancel_requested"] = true
	}
	result["upstream"] = upstreamDelete
	common.ReplyOK(w, result)
}

func deleteThreadLocalRows(db *gorm.DB, threadID string) (map[string]any, error) {
	result := map[string]any{"thread_id": threadID}
	err := db.Transaction(func(tx *gorm.DB) error {
		var recordDeleted int64
		if deleted := tx.Where("thread_id = ?", threadID).Delete(&orm.AgentThreadRecord{}); deleted.Error != nil {
			return deleted.Error
		} else {
			recordDeleted = deleted.RowsAffected
		}

		var roundDeleted int64
		if deleted := tx.Where("thread_id = ?", threadID).Delete(&orm.AgentThreadRound{}); deleted.Error != nil {
			return deleted.Error
		} else {
			roundDeleted = deleted.RowsAffected
		}

		var threadDeleted int64
		if deleted := tx.Where("thread_id = ?", threadID).Delete(&orm.AgentThread{}); deleted.Error != nil {
			return deleted.Error
		} else {
			threadDeleted = deleted.RowsAffected
		}

		var activeDeleted int64
		if deleted := tx.Where("thread_id = ?", threadID).Delete(&orm.AgentUserActiveThread{}); deleted.Error != nil {
			return deleted.Error
		} else {
			activeDeleted = deleted.RowsAffected
		}

		result["deleted_records"] = recordDeleted
		result["deleted_rounds"] = roundDeleted
		result["deleted_threads"] = threadDeleted
		result["deleted_active_threads"] = activeDeleted
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
