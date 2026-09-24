package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/userenv"
)

type environmentInputResult struct {
	Status  string `json:"status"`
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Enabled *bool  `json:"enabled,omitempty"`
}

func invalidEnvironmentInput() *common.AppError {
	return common.ResolveAppError("environment input expired or unavailable; request a new input card", http.StatusConflict)
}

// SubmitEnvironmentInput deliberately bypasses query, structured answers, autosave and tool arguments.
func SubmitEnvironmentInput(w http.ResponseWriter, r *http.Request) {
	owner, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	var req struct {
		HistoryID string `json:"history_id"`
		AskID     string `json:"ask_id"`
		Value     string `json:"value"`
		Cancel    bool   `json:"cancel"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		common.ReplyErr(w, "invalid json", http.StatusBadRequest)
		return
	}
	if !req.Cancel && (strings.TrimSpace(req.Value) == "" || strings.ContainsRune(req.Value, 0) || strings.TrimSpace(req.Value) == "<redacted>") {
		common.ReplyErr(w, "invalid environment variable value", http.StatusBadRequest)
		return
	}
	convID := mux.Vars(r)["name"]
	result := environmentInputResult{}
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var conv orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL AND archived_at IS NULL", convID, owner).First(&conv).Error; err != nil {
			return invalidEnvironmentInput()
		}
		var history orm.ChatHistory
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND conversation_id = ?", req.HistoryID, convID).First(&history).Error; err != nil {
			return invalidEnvironmentInput()
		}
		var ext map[string]any
		if json.Unmarshal(history.Ext, &ext) != nil || ext["fork_read_only"] == true {
			return invalidEnvironmentInput()
		}
		pendingBytes, _ := json.Marshal(ext["ask_pending"])
		var pending AskPendingEvent
		if json.Unmarshal(pendingBytes, &pending) != nil || pending.AskID != req.AskID || pending.EnvInput == nil || req.AskID == "" {
			return invalidEnvironmentInput()
		}
		// A lost response can be retried without applying the submitted value a second time.
		if ext["ask_answered"] == true {
			raw, _ := json.Marshal(ext["env_input_result"])
			if json.Unmarshal(raw, &result) != nil || result.Status == "" {
				return invalidEnvironmentInput()
			}
			return nil
		}
		var latest orm.ChatHistory
		if err := tx.Where("conversation_id = ?", convID).Order("seq DESC").First(&latest).Error; err != nil || latest.ID != history.ID {
			return invalidEnvironmentInput()
		}
		target := pending.EnvInput
		name, err := userenv.NormalizeName(target.Name)
		if err != nil || name != target.Name || (target.Scope != "user" && target.Scope != "conversation") {
			return invalidEnvironmentInput()
		}
		result = environmentInputResult{Status: "canceled", Name: name, Scope: target.Scope}
		if target.Scope == "conversation" {
			result.Status, err = submitSessionEnvironmentInput(r.Context(), convID, req.AskID, req.Value, req.Cancel)
			if err != nil {
				return err
			}
		} else if !req.Cancel {
			var saved userenv.Variable
			if target.ID == "" {
				description := ""
				if target.Description != nil {
					description = *target.Description
				}
				saved, err = userenv.Create(tx, owner, userenv.CreateRequest{Name: name, Value: req.Value, Description: description, Enabled: target.Enabled})
			} else {
				if target.ExpectedUpdatedAt == nil {
					return invalidEnvironmentInput()
				}
				saved, err = userenv.Patch(tx, owner, target.ID, userenv.PatchRequest{
					Name: &name, Value: &req.Value, Description: target.Description, Enabled: target.Enabled, ExpectedUpdatedAt: target.ExpectedUpdatedAt,
				})
			}
			result.Enabled = &saved.Enabled
			if err != nil {
				return err
			}
			result.Status = "configured"
		}
		ext["ask_answered"] = true
		ext["env_input_result"] = result
		delete(ext, "ask_saved_answers")
		updated, err := json.Marshal(ext)
		if err != nil {
			return err
		}
		update := tx.Model(&orm.ChatHistory{}).Where("id = ? AND CAST(ext AS TEXT) = ?", history.ID, string(history.Ext)).Update("ext", updated)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return userenv.ErrConflict
		}
		return nil
	})
	if err != nil {
		replyUserEnvError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func submitSessionEnvironmentInput(ctx context.Context, conversationID, askID, value string, cancel bool) (string, error) {
	payload := map[string]any{"conversation_id": conversationID, "ask_id": askID, "cancel": cancel}
	if !cancel {
		payload["value"] = value
	}
	body, _ := json.Marshal(payload)
	defer clear(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		common.JoinURL(common.ChatServiceEndpoint(), "/api/chat/session-env:input"), bytes.NewReader(body))
	if err != nil {
		return "", invalidEnvironmentInput()
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range authServiceInternalHeaders() {
		req.Header.Set(key, value)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return "", invalidEnvironmentInput()
	}
	defer response.Body.Close()
	var result struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&result) != nil || !result.OK ||
		(result.Status != "configured" && result.Status != "canceled") {
		return "", invalidEnvironmentInput()
	}
	return result.Status, nil
}
