package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/userenv"
)

// The deletion target comes only from a persisted card in authenticated chat
// history, never from model arguments or metadata supplied with the answer.
func submitUserEnvDeletion(ctx context.Context, db *gorm.DB, owner string, histories []orm.ChatHistory, structured any) (string, error) {
	payload, ok := structured.(map[string]any)
	if !ok {
		return "", nil
	}
	for index := len(histories) - 1; index >= 0; index-- {
		history := &histories[index]
		var ext map[string]any
		if json.Unmarshal(history.Ext, &ext) != nil {
			continue
		}
		pending, _ := ext["ask_pending"].(map[string]any)
		if pending == nil {
			continue
		}
		if pending["user_env_delete"] == nil {
			return "", nil
		}
		var continuation string
		var updated []byte
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var current orm.ChatHistory
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND conversation_id = ?", history.ID, history.ConversationID).First(&current).Error; err != nil {
				return err
			}
			ext = nil
			if err := json.Unmarshal(current.Ext, &ext); err != nil {
				return err
			}
			pending, _ = ext["ask_pending"].(map[string]any)
			if ext["ask_answered"] == true || pending == nil || !validAskSubmission(pending, payload) {
				return errors.New("environment variable deletion confirmation is invalid or already answered")
			}
			metadata, err := json.Marshal(pending["user_env_delete"])
			if err != nil {
				return err
			}
			var target UserEnvDeleteConfirmation
			if json.Unmarshal(metadata, &target) != nil || target.ID == "" || target.Name == "" || target.ExpectedUpdatedAt.IsZero() {
				return errors.New("environment variable deletion confirmation is invalid")
			}
			questions, _ := payload["questions"].([]any)
			if len(questions) != 1 {
				return errors.New("environment variable deletion requires an explicit confirmation answer")
			}
			question, _ := questions[0].(map[string]any)
			answer, _ := question["answer"].(map[string]any)
			choice, _ := answer["value"].(string)
			if question["type"] != "boolean" || (choice != "__ask_user_yes__" && choice != "__ask_user_no__") {
				return errors.New("environment variable deletion requires an explicit confirmation answer")
			}
			if choice == "__ask_user_yes__" {
				if err := userenv.Delete(tx, owner, target.ID, target.Name, &target.ExpectedUpdatedAt); err != nil {
					return err
				}
				continuation = fmt.Sprintf("The user confirmed the deletion card. Core has deleted user-level environment variable %s. Session overrides are unchanged. Report this outcome; do not request deletion again.", target.Name)
			} else {
				continuation = fmt.Sprintf("The user canceled deletion of user-level environment variable %s. No environment variables were changed. Report cancellation; do not request deletion again.", target.Name)
			}
			ext["ask_answered"] = true
			ext["ask_saved_answers"] = submittedAskAnswers(structured)
			updated, err = json.Marshal(ext)
			if err != nil {
				return err
			}
			return tx.Model(&current).Update("ext", updated).Error
		})
		if err != nil {
			return "", err
		}
		history.Ext = updated
		return continuation, nil
	}
	return "", nil
}

func userEnvRequestUserID(r *http.Request) string {
	return strings.TrimSpace(store.UserID(r))
}

func ensureUserEnvUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := userEnvRequestUserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing user id", http.StatusUnauthorized)
		return "", false
	}
	return userID, true
}

func userEnvID(r *http.Request) string {
	return strings.TrimSpace(mux.Vars(r)["id"])
}

func ListUserEnvironmentVariables(w http.ResponseWriter, r *http.Request) {
	userID, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	items, err := userenv.List(store.DB().WithContext(r.Context()), userID)
	if err != nil {
		replyUserEnvError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"items": items})
}

func CreateUserEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	userID, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	var req userenv.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid json", http.StatusBadRequest)
		return
	}
	result, err := userenv.Create(store.DB().WithContext(r.Context()), userID, req)
	if err != nil {
		replyUserEnvError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func PatchUserEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	userID, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	id := userEnvID(r)
	if id == "" {
		common.ReplyErr(w, "missing env id", http.StatusBadRequest)
		return
	}
	var req userenv.PatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid json", http.StatusBadRequest)
		return
	}
	result, err := userenv.Patch(store.DB().WithContext(r.Context()), userID, id, req)
	if err != nil {
		replyUserEnvError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func DeleteUserEnvironmentVariable(w http.ResponseWriter, r *http.Request) {
	userID, ok := ensureUserEnvUserID(w, r)
	if !ok {
		return
	}
	id := userEnvID(r)
	if id == "" {
		common.ReplyErr(w, "missing env id", http.StatusBadRequest)
		return
	}
	if err := userenv.Delete(store.DB().WithContext(r.Context()), userID, id, "", nil); err != nil {
		replyUserEnvError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{"deleted": true})
}

func applyUserEnvironmentRuntimeConfig(ctx context.Context, db *gorm.DB, userID string, body map[string]any) error {
	env, err := userenv.LoadEnabled(ctx, db, userID)
	if err != nil {
		return err
	}
	body["user_env_vars"] = env
	return nil
}

func replyUserEnvError(w http.ResponseWriter, err error) {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		common.ReplyAppErr(w, appErr)
		return
	}
	if errors.Is(err, userenv.ErrConflict) || userenv.IsDuplicate(err) {
		common.ReplyAppErr(w, common.NewAppError(http.StatusConflict, common.ErrCodeConflict, "Environment variable changed or name already exists; reload and retry"))
		return
	}
	common.ReplyAppErr(w, common.NewAppError(http.StatusInternalServerError, common.ErrCodeInternal, "Unable to access user environment variables; check the credential key configuration").WithDetail(map[string]string{
		"reason": "user_env_unavailable",
	}))
}
