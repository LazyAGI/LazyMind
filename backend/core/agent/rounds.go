package agent

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

type threadRoundResponse struct {
	RoundID          string    `json:"round_id"`
	ThreadID         string    `json:"thread_id"`
	TaskID           string    `json:"task_id,omitempty"`
	Status           string    `json:"status"`
	UserMessage      string    `json:"user_message,omitempty"`
	AssistantMessage string    `json:"assistant_message,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type threadHistoryResponse struct {
	ThreadID string                `json:"thread_id"`
	Rounds   []threadRoundResponse `json:"rounds"`
}

func GetThreadHistory(w http.ResponseWriter, r *http.Request) {
	listThreadHistory(w, r)
}

func ListThreadRounds(w http.ResponseWriter, r *http.Request) {
	listThreadHistory(w, r)
}

func listThreadHistory(w http.ResponseWriter, r *http.Request) {
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

	rounds, err := listThreadRounds(db, threadID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "list thread rounds failed", err), http.StatusInternalServerError)
		return
	}

	roundIDs := make([]string, 0, len(rounds))
	for _, round := range rounds {
		roundIDs = append(roundIDs, round.RoundID)
	}
	recordsByRound, err := listRoundRecords(db, roundIDs)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "list round records failed", err), http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, threadHistoryResponse{
		ThreadID: threadID,
		Rounds:   buildThreadRoundResponses(rounds, recordsByRound),
	})
}

func DeleteThreadHistory(w http.ResponseWriter, r *http.Request) {
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
	if err != nil {
		common.ReplyErrWithData(w, "fetch thread flow status failed", map[string]any{"detail": err.Error()}, http.StatusBadGateway)
		return
	}

	cancelRequested := false
	if isThreadFlowRunning(flowStatus) {
		if _, statusCode, err := postUpstreamProxy(r.Context(), r, threadActionURL(threadID, "cancel"), nil); err != nil {
			common.ReplyErrWithData(w, "cancel running thread failed", map[string]any{
				"detail":      err.Error(),
				"flow_status": flowStatus,
			}, statusCode)
			return
		}
		cancelRequested = true
	}

	if stream := activeStreams.get(threadID); stream != nil {
		if cancelRequested {
			common.ReplyErrWithData(w, "thread has active message stream", map[string]any{
				"thread_id":        threadID,
				"cancel_requested": true,
			}, http.StatusConflict)
			return
		}
		common.ReplyErr(w, "thread has active message stream", http.StatusConflict)
		return
	}

	result, err := deleteThreadHistory(db, threadID)
	if err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "delete thread history failed", err), http.StatusInternalServerError)
		return
	}
	if cancelRequested {
		result["cancel_requested"] = true
	}
	common.ReplyOK(w, result)
}

func createThreadRound(db *gorm.DB, threadID, requestHash string, requestBody []byte) (orm.AgentThreadRound, error) {
	now := time.Now().UTC()
	round := orm.AgentThreadRound{
		RoundID:        newStreamRecordID(),
		ThreadID:       threadID,
		RequestHash:    requestHash,
		Status:         "streaming",
		UserMessage:    extractUserMessageFromRequestBody(requestBody),
		RequestPayload: strings.TrimSpace(string(requestBody)),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return round, db.Create(&round).Error
}

func listThreadRounds(db *gorm.DB, threadID string) ([]orm.AgentThreadRound, error) {
	var rounds []orm.AgentThreadRound
	if err := db.Where("thread_id = ?", threadID).Order("created_at ASC").Find(&rounds).Error; err != nil {
		return nil, err
	}
	return rounds, nil
}

func buildThreadRoundResponses(rounds []orm.AgentThreadRound, recordsByRound map[string][]orm.AgentThreadRecord) []threadRoundResponse {
	items := make([]threadRoundResponse, 0, len(rounds))
	for _, round := range rounds {
		items = append(items, threadRoundResponse{
			RoundID:          round.RoundID,
			ThreadID:         round.ThreadID,
			TaskID:           round.TaskID,
			Status:           round.Status,
			UserMessage:      round.UserMessage,
			AssistantMessage: buildRoundAssistantMessage(recordsByRound[round.RoundID]),
			CreatedAt:        round.CreatedAt,
			UpdatedAt:        round.UpdatedAt,
		})
	}
	return items
}

func buildRoundAssistantMessage(records []orm.AgentThreadRecord) string {
	var thinking strings.Builder
	var answer strings.Builder
	for _, record := range records {
		delta := extractDeltaValue(recordPayloadValue(record))
		if delta == "" {
			continue
		}
		switch record.EventName {
		case "thinking_delta":
			thinking.WriteString(delta)
		case "answer_delta":
			answer.WriteString(delta)
		}
	}
	return thinking.String() + answer.String()
}

func extractDeltaValue(root any) string {
	switch value := root.(type) {
	case map[string]any:
		if child, ok := value["delta"]; ok {
			return stringifyDeltaValue(child)
		}
		for _, child := range value {
			if result := extractDeltaValue(child); result != "" {
				return result
			}
		}
	case []any:
		for _, child := range value {
			if result := extractDeltaValue(child); result != "" {
				return result
			}
		}
	}
	return ""
}

func stringifyDeltaValue(root any) string {
	switch value := root.(type) {
	case string:
		return value
	case float64, bool, int, int64, uint64:
		return fmt.Sprint(value)
	default:
		return ""
	}
}

func deleteThreadHistory(db *gorm.DB, threadID string) (map[string]any, error) {
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

		result["deleted_records"] = recordDeleted
		result["deleted_rounds"] = roundDeleted
		result["deleted_threads"] = threadDeleted
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
