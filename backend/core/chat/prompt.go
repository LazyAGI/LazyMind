package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	corestore "lazymind/core/store"
)

const (
	promptNameMaxLen    = 100
	promptContentMaxLen = 800
)

func promptNameFromPath(r *http.Request) string {
	raw := common.PathVar(r, "name")
	raw = strings.TrimPrefix(raw, "prompts/")
	raw = strings.TrimPrefix(raw, "/")
	return raw
}

func conversationIDFromPath(r *http.Request) string {
	return common.PathVar(r, "conversation_id")
}

func conversationNameFromPath(r *http.Request) string {
	return common.PathVar(r, "name")
}

func writePromptJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// CreatePrompt text POST /api/v1/prompts
func CreatePrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"display_name"`
		Content     string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	displayName := strings.TrimSpace(body.DisplayName)
	content := body.Content
	if utf8.RuneCountInString(displayName) > promptNameMaxLen {
		common.ReplyErr(w, "name too long", http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(content) > promptContentMaxLen {
		common.ReplyErr(w, "content too long", http.StatusBadRequest)
		return
	}
	if displayName == "" || strings.TrimSpace(content) == "" {
		common.ReplyErr(w, "display_name and content required", http.StatusBadRequest)
		return
	}

	userID := corestore.UserID(r)
	userName := corestore.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var promptExisted int64
	if err := corestore.DB().
		Model(&orm.Prompt{}).
		Where("create_user_id = ? AND name = ? AND deleted_at IS NULL", userID, displayName).
		Count(&promptExisted).Error; err != nil {
		common.ReplyErr(w, "query prompts failed", http.StatusInternalServerError)
		return
	}
	if promptExisted > 0 {
		common.ReplyErr(w, "prompt name already exists", http.StatusConflict)
		return
	}

	now := time.Now().UTC()
	p := orm.Prompt{
		ID:      newID("p_"),
		Name:    displayName,
		Content: content,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := corestore.DB().Create(&p).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "prompt existed", err), http.StatusConflict)
		return
	}

	writePromptJSON(w, http.StatusOK, map[string]any{
		"name":         "prompts/" + p.ID,
		"id":           p.ID,
		"content":      p.Content,
		"display_name": p.Name,
		"is_default":   false,
	})
}

func PolishPrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content      string `json:"content"`
		UserInstruct string `json:"user_instruct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	content := strings.TrimSpace(body.Content)
	userInstruct := strings.TrimSpace(body.UserInstruct)
	if content == "" || userInstruct == "" {
		common.ReplyErr(w, "content and user_instruct required", http.StatusBadRequest)
		return
	}

	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	db := corestore.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "load llm config failed", http.StatusInternalServerError)
		return
	}
	polished, err := algo.GeneratePolish(r.Context(), algo.PolishGenerateRequest{
		Content:      content,
		UserInstruct: userInstruct,
		LLMConfig:    llmConfig,
	})
	if err != nil {
		common.ReplyErr(w, "prompt polish failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writePromptJSON(w, http.StatusOK, map[string]any{"content": polished})
}

// UpdatePrompt text PATCH /api/v1/prompts/{name}
func UpdatePrompt(w http.ResponseWriter, r *http.Request) {
	promptID := promptNameFromPath(r)
	if promptID == "" {
		common.ReplyErr(w, "invalid prompt name", http.StatusBadRequest)
		return
	}
	var body struct {
		DisplayName string `json:"display_name"`
		Content     string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	displayName := strings.TrimSpace(body.DisplayName)
	content := body.Content
	if displayName == "" && content == "" {
		common.ReplyErr(w, "display_name/content required", http.StatusBadRequest)
		return
	}
	if displayName != "" && utf8.RuneCountInString(displayName) > promptNameMaxLen {
		common.ReplyErr(w, "name too long", http.StatusBadRequest)
		return
	}
	if content != "" && utf8.RuneCountInString(content) > promptContentMaxLen {
		common.ReplyErr(w, "content too long", http.StatusBadRequest)
		return
	}

	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var p orm.Prompt
	if err := corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).First(&p).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "prompt not found", err), http.StatusNotFound)
		return
	}

	updates := map[string]any{"updated_at": time.Now().UTC()}
	if content != "" {
		updates["content"] = content
	}
	if displayName != "" {
		updates["name"] = displayName
	}
	if err := corestore.DB().Model(&orm.Prompt{}).Where("id = ? AND create_user_id = ?", promptID, userID).Updates(updates).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "update failed", err), http.StatusInternalServerError)
		return
	}
	_ = corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).First(&p).Error

	var dpCount int64
	_ = corestore.DB().Model(&orm.DefaultPrompt{}).
		Where("create_user_id = ? AND prompt_id = ?", userID, promptID).
		Count(&dpCount).Error

	writePromptJSON(w, http.StatusOK, map[string]any{
		"name":         "prompts/" + p.ID,
		"id":           p.ID,
		"content":      p.Content,
		"display_name": p.Name,
		"is_default":   dpCount > 0,
	})
}

// DeletePrompt text DELETE /api/v1/prompts/{name}
func DeletePrompt(w http.ResponseWriter, r *http.Request) {
	promptID := promptNameFromPath(r)
	if promptID == "" {
		common.ReplyErr(w, "invalid prompt name", http.StatusBadRequest)
		return
	}
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	_ = corestore.DB().Where("create_user_id = ? AND prompt_id = ?", userID, promptID).Delete(&orm.DefaultPrompt{}).Error
	if err := corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).Delete(&orm.Prompt{}).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "delete failed", err), http.StatusInternalServerError)
		return
	}
	writePromptJSON(w, http.StatusOK, nil)
}

// GetPrompt text GET /api/v1/prompts/{name}
func GetPrompt(w http.ResponseWriter, r *http.Request) {
	promptID := promptNameFromPath(r)
	if promptID == "" {
		common.ReplyErr(w, "invalid prompt name", http.StatusBadRequest)
		return
	}
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var p orm.Prompt
	if err := corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).First(&p).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "prompt not found", err), http.StatusNotFound)
		return
	}
	var dpCount int64
	_ = corestore.DB().Model(&orm.DefaultPrompt{}).
		Where("create_user_id = ? AND prompt_id = ?", userID, promptID).
		Count(&dpCount).Error

	writePromptJSON(w, http.StatusOK, map[string]any{
		"name":         "prompts/" + p.ID,
		"id":           p.ID,
		"content":      p.Content,
		"display_name": p.Name,
		"is_default":   dpCount > 0,
	})
}

// ListPrompts text GET /api/v1/prompts（text page_size、page_token）
func ListPrompts(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	pageSize := 50
	if s := r.URL.Query().Get("page_size"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 1000 {
			pageSize = n
		}
	}
	start := 0
	if tok := strings.TrimSpace(r.URL.Query().Get("page_token")); tok != "" {
		if n, err := strconv.Atoi(tok); err == nil && n >= 0 {
			start = n
		}
	}

	var dps []orm.DefaultPrompt
	_ = corestore.DB().Where("create_user_id = ?", userID).Find(&dps).Error
	defaultIDs := map[string]bool{}
	for _, dp := range dps {
		defaultIDs[dp.PromptID] = true
	}

	var ps []orm.Prompt
	if err := corestore.DB().Where("create_user_id = ?", userID).Order("created_at desc").Find(&ps).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "list failed", err), http.StatusInternalServerError)
		return
	}
	total := len(ps)

	outAll := make([]map[string]any, 0, total)
	for _, p := range ps {
		if defaultIDs[p.ID] {
			outAll = append(outAll, map[string]any{
				"name":         "prompts/" + p.ID,
				"id":           p.ID,
				"content":      p.Content,
				"display_name": p.Name,
				"is_default":   true,
			})
		}
	}
	for _, p := range ps {
		if !defaultIDs[p.ID] {
			outAll = append(outAll, map[string]any{
				"name":         "prompts/" + p.ID,
				"id":           p.ID,
				"content":      p.Content,
				"display_name": p.Name,
				"is_default":   false,
			})
		}
	}

	if start >= len(outAll) {
		writePromptJSON(w, http.StatusOK, map[string]any{
			"prompts":         []any{},
			"next_page_token": "",
			"total":           int64(total),
		})
		return
	}
	end := start + pageSize
	if end > len(outAll) {
		end = len(outAll)
	}
	next := ""
	if start+pageSize < total {
		next = strconv.Itoa(start + pageSize)
	}
	writePromptJSON(w, http.StatusOK, map[string]any{
		"prompts":         outAll[start:end],
		"next_page_token": next,
		"total":           int64(total),
	})
}

// SetDefaultPrompt text POST /api/v1/prompts/{name}:setDefault
func SetDefaultPrompt(w http.ResponseWriter, r *http.Request) {
	promptID := promptNameFromPath(r)
	if promptID == "" {
		common.ReplyErr(w, "invalid prompt name", http.StatusBadRequest)
		return
	}
	userID := corestore.UserID(r)
	userName := corestore.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var p orm.Prompt
	if err := corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).First(&p).Error; err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "prompt not found", err), http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	dp := orm.DefaultPrompt{
		PromptID:   promptID,
		PromptName: p.Name,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	_ = corestore.DB().Create(&dp).Error
	writePromptJSON(w, http.StatusOK, nil)
}

// UnsetDefaultPrompt text POST /api/v1/prompts/{name}:unsetDefault
func UnsetDefaultPrompt(w http.ResponseWriter, r *http.Request) {
	promptID := promptNameFromPath(r)
	if promptID == "" {
		common.ReplyErr(w, "invalid prompt name", http.StatusBadRequest)
		return
	}
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	_ = corestore.DB().Where("create_user_id = ? AND prompt_id = ?", userID, promptID).Delete(&orm.DefaultPrompt{}).Error
	writePromptJSON(w, http.StatusOK, nil)
}
