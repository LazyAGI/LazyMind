package skill

import (
	"encoding/json"
	"net/http"
	"strings"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	appLog "lazymind/core/log"
	"lazymind/core/resourcechange"
	"lazymind/core/store"
)

type createRequest struct {
	SessionID string `json:"session_id"`
	Category  string `json:"category"`
	SkillName string `json:"skill_name"`
	Content   string `json:"content"`
}

func Create(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Category = strings.TrimSpace(req.Category)
	req.SkillName = strings.TrimSpace(req.SkillName)
	appLog.Logger.Info().
		Str("route", "/skill/create").
		Str("session_id", req.SessionID).
		Str("category", req.Category).
		Str("skill_name", req.SkillName).
		Msg("internal skill create request received")
	if req.SessionID == "" || req.Category == "" || req.SkillName == "" || strings.TrimSpace(req.Content) == "" {
		appLog.Logger.Warn().
			Str("route", "/skill/create").
			Str("session_id", req.SessionID).
			Str("category", req.Category).
			Str("skill_name", req.SkillName).
			Msg("internal skill create request rejected: missing required fields")
		common.ReplyErr(w, "session_id/category/skill_name/content required", http.StatusBadRequest)
		return
	}
	if err := validatePathSegment(req.SkillName); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validatePathSegment(req.Category); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	userID, userName, err := evolution.ResolveSessionUser(r.Context(), db, req.SessionID)
	if err != nil || strings.TrimSpace(userID) == "" {
		appLog.Logger.Warn().
			Err(err).
			Str("route", "/skill/create").
			Str("session_id", req.SessionID).
			Msg("internal skill create request rejected: unable to resolve session user")
		common.ReplyErr(w, "unable to resolve session user", http.StatusBadRequest)
		return
	}

	description, err := validateParentSkillContent(req.SkillName, "", req.Content)
	if err != nil {
		replySkillError(w, err)
		return
	}

	createReq := createSkillRequest{
		Name:        req.SkillName,
		Description: description,
		Category:    req.Category,
		Content:     req.Content,
	}
	if err := createParentSkillWithContent(r.Context(), db, userID, userName, createReq, req.Content, description, resourcechange.Source{
		ChangeSource:  resourcechange.ChangeSourceInternalDirect,
		SourceRefType: "session",
		SourceRefID:   req.SessionID,
	}); err != nil {
		replySkillError(w, err)
		return
	}

	relativePath := parentRelativePath(req.Category, req.SkillName)
	var row orm.SkillResource
	if err := db.WithContext(r.Context()).Where("owner_user_id = ? AND relative_path = ?", userID, relativePath).Take(&row).Error; err != nil {
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	item, err := getSkillDetail(r.Context(), db, userID, row.ID)
	if err != nil {
		appLog.Logger.Error().
			Err(err).
			Str("route", "/skill/create").
			Str("session_id", req.SessionID).
			Str("user_id", userID).
			Str("category", req.Category).
			Str("skill_name", req.SkillName).
			Msg("internal skill create succeeded but failed to load detail")
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	appLog.Logger.Info().
		Str("route", "/skill/create").
		Str("session_id", req.SessionID).
		Str("user_id", userID).
		Str("category", req.Category).
		Str("skill_name", req.SkillName).
		Str("skill_id", row.ID).
		Msg("internal skill create request created skill directly")
	common.ReplyOK(w, item)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
