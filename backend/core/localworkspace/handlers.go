package localworkspace

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func rejectUnlessEnabled(w http.ResponseWriter) bool {
	if Enabled() {
		return false
	}
	common.ReplyAppErr(w, ModeError())
	return true
}

func Revoke(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) {
		return
	}
	var body struct {
		Version int64 `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Version < 1 {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	id, userID := mux.Vars(r)["workspace_id"], store.UserID(r)
	now := time.Now().UTC()
	var conversationIDs []string
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&orm.LocalWorkspace{}).
			Where("id = ? AND create_user_id = ? AND status = ? AND version = ?", id, userID, StatusActive, body.Version).
			Updates(map[string]any{"status": StatusRevoked, "version": gorm.Expr("version + 1"), "revoked_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var row orm.LocalWorkspace
			err := tx.Where("id = ? AND create_user_id = ?", id, userID).First(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return Error("workspace_not_found", 404, "resource not found")
			}
			if err != nil {
				return err
			}
			return Error("binding_conflict", 409, "conflict")
		}
		return tx.Model(&orm.ConversationWorkspaceBinding{}).Where("workspace_id = ?", id).
			Pluck("conversation_id", &conversationIDs).Error
	})
	if replyError(w, err) {
		return
	}
	stopFailed := 0
	for _, conversationID := range conversationIDs {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := requestConversationStop(stopCtx, userID, conversationID)
		cancel()
		if err != nil {
			stopFailed++
		}
	}
	common.ReplyOK(w, map[string]any{"workspace_id": id, "status": StatusRevoked,
		"version": body.Version + 1, "affected_task_count": len(conversationIDs),
		"stop_requested": len(conversationIDs) > 0, "stop_failed_count": stopFailed})
}

func UpdateConversationPermission(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) {
		return
	}
	var body struct {
		PermissionMode string `json:"permission_mode"`
		Version        int64  `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	body.PermissionMode = strings.TrimSpace(body.PermissionMode)
	if !ValidPermissionMode(body.PermissionMode) || body.Version < 1 {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	conversationID, userID := mux.Vars(r)["conversation_id"], store.UserID(r)
	err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var conversation orm.Conversation
		err := tx.Where("id = ? AND create_user_id = ?", conversationID, userID).First(&conversation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Error("workspace_not_found", 404, "resource not found")
		}
		if err != nil {
			return err
		}
		if !conversation.IsTaskConv {
			return ModeError()
		}
		var binding orm.ConversationWorkspaceBinding
		if err := tx.Where("conversation_id = ?", conversationID).First(&binding).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return Error("workspace_not_found", 404, "resource not found")
			}
			return err
		}
		if _, err := ResolveActiveForBinding(r.Context(), tx, userID, binding.WorkspaceID); err != nil {
			return err
		}
		result := tx.Model(&orm.ConversationWorkspaceBinding{}).
			Where("conversation_id = ? AND permission_version = ?", conversationID, body.Version).
			Updates(map[string]any{"permission_mode": body.PermissionMode,
				"permission_version": gorm.Expr("permission_version + 1"), "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return Error("binding_conflict", 409, "conflict")
		}
		return nil
	})
	if replyError(w, err) {
		return
	}
	common.ReplyOK(w, map[string]any{"permission_mode": body.PermissionMode,
		"permission_version": body.Version + 1, "effective_at": "next_request"})
}

func replyError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var app *common.AppError
	if errors.As(err, &app) {
		common.ReplyAppErr(w, app)
	} else {
		common.ReplyErr(w, "internal server error", 500)
	}
	return true
}

func List(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) {
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	query := db.WithContext(r.Context()).Where("create_user_id = ?", store.UserID(r))
	if !strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_inactive")), "true") {
		query = query.Where("status = ?", StatusActive)
	}
	if keyword := strings.TrimSpace(r.URL.Query().Get("query")); keyword != "" {
		like := "%" + strings.ToLower(keyword) + "%"
		query = query.Where("LOWER(display_name) LIKE ? OR LOWER(canonical_path) LIKE ?", like, like)
	}
	var rows []orm.LocalWorkspace
	if err := query.Order("last_used_at DESC").Limit(8).Find(&rows).Error; err != nil {
		common.ReplyErr(w, "list failed", 500)
		return
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	counts := map[string]int64{}
	if len(ids) > 0 {
		var grouped []struct {
			WorkspaceID string
			Count       int64
		}
		if err := db.Model(&orm.ConversationWorkspaceBinding{}).
			Select("workspace_id, COUNT(*) AS count").Where("workspace_id IN ?", ids).
			Group("workspace_id").Scan(&grouped).Error; err != nil {
			common.ReplyErr(w, "list failed", 500)
			return
		}
		for _, group := range grouped {
			counts[group.WorkspaceID] = group.Count
		}
	}
	items := make([]PublicWorkspace, 0, len(rows))
	for _, row := range rows {
		item := publicWorkspace(row)
		item.AffectedTaskCount = counts[row.ID]
		items = append(items, item)
	}
	common.ReplyOK(w, map[string]any{"items": items})
}

func ConversationBinding(w http.ResponseWriter, r *http.Request) {
	if rejectUnlessEnabled(w) {
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", 500)
		return
	}
	conversationID, userID := mux.Vars(r)["conversation_id"], store.UserID(r)
	var conversation orm.Conversation
	err := db.WithContext(r.Context()).Where("id = ? AND create_user_id = ?", conversationID, userID).First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyAppErr(w, Error("workspace_not_found", 404, "resource not found"))
		return
	}
	if err != nil {
		common.ReplyErr(w, "request failed", 500)
		return
	}
	var binding orm.ConversationWorkspaceBinding
	err = db.WithContext(r.Context()).Where("conversation_id = ?", conversationID).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyOK(w, map[string]any{"status": "none"})
		return
	}
	if err != nil {
		common.ReplyErr(w, "request failed", 500)
		return
	}
	var workspace orm.LocalWorkspace
	err = db.WithContext(r.Context()).Where("id = ? AND create_user_id = ?", binding.WorkspaceID, userID).First(&workspace).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyAppErr(w, Error("workspace_not_found", 404, "resource not found"))
		return
	}
	if err != nil {
		common.ReplyErr(w, "request failed", 500)
		return
	}
	item := publicWorkspace(workspace)
	if err := db.Model(&orm.ConversationWorkspaceBinding{}).Where("workspace_id = ?", workspace.ID).
		Count(&item.AffectedTaskCount).Error; err != nil {
		common.ReplyErr(w, "request failed", 500)
		return
	}
	common.ReplyOK(w, BindingView{Status: workspace.Status, WorkspaceID: workspace.ID,
		Workspace: &item, AffectedTaskCount: item.AffectedTaskCount,
		PermissionMode: binding.PermissionMode, PermissionVersion: binding.PermissionVersion})
}

func InternalRegister(w http.ResponseWriter, r *http.Request) {
	if rejectInternal(w, r) {
		return
	}
	var input RegisterInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input); err != nil {
		common.ReplyAppErr(w, Error("invalid_selection", 400, "invalid request"))
		return
	}
	workspace, err := Register(r.Context(), store.DB(), store.UserID(r), input)
	if replyError(w, err) {
		return
	}
	common.ReplyOK(w, workspace)
}

func InternalPrepareReauthorization(w http.ResponseWriter, r *http.Request) {
	if rejectInternal(w, r) {
		return
	}
	var workspace orm.LocalWorkspace
	err := store.DB().WithContext(r.Context()).Where("id = ? AND create_user_id = ?",
		mux.Vars(r)["workspace_id"], store.UserID(r)).First(&workspace).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = Error("workspace_not_found", 404, "resource not found")
	}
	if replyError(w, err) {
		return
	}
	common.ReplyOK(w, map[string]any{"workspace_id": workspace.ID, "display_name": workspace.DisplayName,
		"canonical_path": workspace.CanonicalPath})
}

func rejectInternal(w http.ResponseWriter, r *http.Request) bool {
	if rejectUnlessEnabled(w) {
		return true
	}
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_LOCAL_WORKSPACE_HOST_TOKEN"))
	actual := strings.TrimSpace(r.Header.Get("X-LazyMind-Local-Workspace-Token"))
	if expected == "" || len(expected) != len(actual) || subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		common.ReplyAppErr(w, Error("mode_forbidden", 403, "forbidden"))
		return true
	}
	return false
}
