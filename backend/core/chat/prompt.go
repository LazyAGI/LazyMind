package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	corestore "lazymind/core/store"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	promptNameMaxLen    = 100
	promptContentMaxLen = 800
)

var promptCategories = map[string]struct{}{
	"general":                {},
	"document_processing":    {},
	"information_extraction": {},
	"structured_analysis":    {},
	"report_generation":      {},
	"data_analysis":          {},
	"custom":                 {},
}

type presetPrompt struct {
	ID        string
	Category  string
	NameZH    string
	ContentZH string
	NameEN    string
	ContentEN string
}

var presetPrompts = []presetPrompt{
	{
		ID:        "preset-general-qa",
		Category:  "general",
		NameZH:    "通用问答助手",
		ContentZH: "请根据我提供的文档内容，简洁明了地回答我的问题。如果文档中没有相关信息，请直接告知。",
		NameEN:    "General Q&A Assistant",
		ContentEN: "Answer my questions concisely based on the documents I provide. If the documents contain no relevant information, say so directly.",
	},
	{
		ID:        "preset-document-summary",
		Category:  "document_processing",
		NameZH:    "文档摘要提取",
		ContentZH: "请帮我总结一下这份文档的核心内容，并列出其中的关键要点。",
		NameEN:    "Document Summary",
		ContentEN: "Summarize the core content of this document and list the key points.",
	},
	{
		ID:        "preset-structured-extraction",
		Category:  "information_extraction",
		NameZH:    "结构化信息提取",
		ContentZH: "请从文档中提取出所有的日期、参与方和主要结论，并以表格的形式呈现。",
		NameEN:    "Structured Information Extraction",
		ContentEN: "Extract all dates, parties involved, and main conclusions from the document and present them in a table.",
	},
}

type promptItemResponse struct {
	Name        string     `json:"name"`
	ID          string     `json:"id"`
	Content     string     `json:"content"`
	DisplayName string     `json:"display_name"`
	Category    string     `json:"category"`
	Source      string     `json:"source"`
	IsFavorite  bool       `json:"is_favorite"`
	UsageCount  int64      `json:"usage_count"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   *time.Time `json:"created_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
}

type promptFacetResponse struct {
	Scopes     map[string]int64 `json:"scopes"`
	Categories map[string]int64 `json:"categories"`
}

type promptListResponse struct {
	Prompts          []promptItemResponse     `json:"prompts"`
	CustomCategories []promptCategoryResponse `json:"custom_categories"`
	NextPageToken    string                   `json:"next_page_token"`
	Total            int64                    `json:"total"`
	Facets           promptFacetResponse      `json:"facets"`
}

type promptStateResponse struct {
	ID         string     `json:"id"`
	IsFavorite bool       `json:"is_favorite"`
	UsageCount int64      `json:"usage_count"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

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

func validPromptCategory(category string) bool {
	_, ok := promptCategories[category]
	return ok
}

func validPromptCategoryForUser(userID, category string) bool {
	if validPromptCategory(category) {
		return true
	}
	var count int64
	return corestore.DB().Model(&orm.PromptCategory{}).
		Where("id = ? AND create_user_id = ?", category, userID).
		Count(&count).Error == nil && count > 0
}

func presetPromptByID(id string) (presetPrompt, bool) {
	for _, prompt := range presetPrompts {
		if prompt.ID == id {
			return prompt, true
		}
	}
	return presetPrompt{}, false
}

func localizedPresetItem(prompt presetPrompt, locale string, state orm.PromptUserState) promptItemResponse {
	displayName := prompt.NameZH
	content := prompt.ContentZH
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		displayName = prompt.NameEN
		content = prompt.ContentEN
	}
	return promptItemResponse{
		Name:        "prompts/" + prompt.ID,
		ID:          prompt.ID,
		Content:     content,
		DisplayName: displayName,
		Category:    prompt.Category,
		Source:      "preset",
		IsFavorite:  state.IsFavorite,
		UsageCount:  state.UsageCount,
		LastUsedAt:  state.LastUsedAt,
	}
}

func customPromptItem(prompt orm.Prompt, state orm.PromptUserState) promptItemResponse {
	createdAt := prompt.CreatedAt
	updatedAt := prompt.UpdatedAt
	return promptItemResponse{
		Name:        "prompts/" + prompt.ID,
		ID:          prompt.ID,
		Content:     prompt.Content,
		DisplayName: prompt.Name,
		Category:    prompt.Category,
		Source:      "custom",
		IsFavorite:  state.IsFavorite,
		UsageCount:  state.UsageCount,
		LastUsedAt:  state.LastUsedAt,
		CreatedAt:   &createdAt,
		UpdatedAt:   &updatedAt,
	}
}

func loadPromptStates(userID string) (map[string]orm.PromptUserState, error) {
	var states []orm.PromptUserState
	if err := corestore.DB().Where("create_user_id = ?", userID).Find(&states).Error; err != nil {
		return nil, err
	}
	result := make(map[string]orm.PromptUserState, len(states))
	for _, state := range states {
		result[state.PromptID] = state
	}
	return result, nil
}

func promptExistsForUser(userID, promptID string) bool {
	if _, ok := presetPromptByID(promptID); ok {
		return true
	}
	var count int64
	return corestore.DB().Model(&orm.Prompt{}).
		Where("id = ? AND create_user_id = ?", promptID, userID).
		Count(&count).Error == nil && count > 0
}

func upsertPromptFavorite(userID, userName, promptID string, favorite bool) error {
	now := time.Now().UTC()
	state := orm.PromptUserState{
		ID:             newID("pus_"),
		PromptID:       promptID,
		IsFavorite:     favorite,
		CreateUserID:   userID,
		CreateUserName: userName,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return corestore.DB().Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "create_user_id"}, {Name: "prompt_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"is_favorite": favorite,
			"updated_at":  now,
			"deleted_at":  nil,
		}),
	}).Create(&state).Error
}

func findPromptItem(userID, promptID, locale string) (promptItemResponse, error) {
	states, err := loadPromptStates(userID)
	if err != nil {
		return promptItemResponse{}, err
	}
	if preset, ok := presetPromptByID(promptID); ok {
		return localizedPresetItem(preset, locale, states[promptID]), nil
	}
	var prompt orm.Prompt
	if err := corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).First(&prompt).Error; err != nil {
		return promptItemResponse{}, err
	}
	return customPromptItem(prompt, states[promptID]), nil
}

func CreatePrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"display_name"`
		Content     string `json:"content"`
		Category    string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	displayName := strings.TrimSpace(body.DisplayName)
	content := body.Content
	category := strings.TrimSpace(body.Category)
	if category == "" {
		category = "custom"
	}
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
	if !validPromptCategoryForUser(userID, category) {
		common.ReplyErr(w, "invalid category", http.StatusBadRequest)
		return
	}
	var existing int64
	if err := corestore.DB().Model(&orm.Prompt{}).
		Where("create_user_id = ? AND name = ? AND deleted_at IS NULL", userID, displayName).
		Count(&existing).Error; err != nil {
		common.ReplyErr(w, "query prompts failed", http.StatusInternalServerError)
		return
	}
	if existing > 0 {
		common.ReplyErr(w, "prompt name already exists", http.StatusConflict)
		return
	}

	now := time.Now().UTC()
	prompt := orm.Prompt{
		ID:       newID("p_"),
		Name:     displayName,
		Content:  content,
		Category: category,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := corestore.DB().Create(&prompt).Error; err != nil {
		common.ReplyErr(w, "create prompt failed", http.StatusConflict)
		return
	}
	writePromptJSON(w, http.StatusOK, customPromptItem(prompt, orm.PromptUserState{}))
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

func UpdatePrompt(w http.ResponseWriter, r *http.Request) {
	promptID := promptNameFromPath(r)
	if _, ok := presetPromptByID(promptID); ok {
		common.ReplyErr(w, "preset prompt is read only", http.StatusForbidden)
		return
	}
	var body struct {
		DisplayName string `json:"display_name"`
		Content     string `json:"content"`
		Category    string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, fmt.Sprintf("%s: %v", "invalid body", err), http.StatusBadRequest)
		return
	}
	displayName := strings.TrimSpace(body.DisplayName)
	content := body.Content
	category := strings.TrimSpace(body.Category)
	if displayName == "" && content == "" && category == "" {
		common.ReplyErr(w, "display_name/content/category required", http.StatusBadRequest)
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
	if category != "" && !validPromptCategoryForUser(userID, category) {
		common.ReplyErr(w, "invalid category", http.StatusBadRequest)
		return
	}
	var prompt orm.Prompt
	if err := corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).First(&prompt).Error; err != nil {
		common.ReplyErr(w, "prompt not found", http.StatusNotFound)
		return
	}
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if displayName != "" {
		updates["name"] = displayName
	}
	if content != "" {
		updates["content"] = content
	}
	if category != "" {
		updates["category"] = category
	}
	if err := corestore.DB().Model(&prompt).Updates(updates).Error; err != nil {
		common.ReplyErr(w, "update prompt failed", http.StatusConflict)
		return
	}
	_ = corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).First(&prompt).Error
	states, _ := loadPromptStates(userID)
	writePromptJSON(w, http.StatusOK, customPromptItem(prompt, states[promptID]))
}

func DeletePrompt(w http.ResponseWriter, r *http.Request) {
	promptID := promptNameFromPath(r)
	if _, ok := presetPromptByID(promptID); ok {
		common.ReplyErr(w, "preset prompt is read only", http.StatusForbidden)
		return
	}
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	result := corestore.DB().Where("id = ? AND create_user_id = ?", promptID, userID).Delete(&orm.Prompt{})
	if result.Error != nil {
		common.ReplyErr(w, "delete prompt failed", http.StatusInternalServerError)
		return
	}
	if result.RowsAffected == 0 {
		common.ReplyErr(w, "prompt not found", http.StatusNotFound)
		return
	}
	_ = corestore.DB().Unscoped().Where("create_user_id = ? AND prompt_id = ?", userID, promptID).Delete(&orm.PromptUserState{}).Error
	writePromptJSON(w, http.StatusOK, nil)
}

func GetPrompt(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	item, err := findPromptItem(userID, promptNameFromPath(r), r.URL.Query().Get("locale"))
	if err != nil {
		common.ReplyErr(w, "prompt not found", http.StatusNotFound)
		return
	}
	writePromptJSON(w, http.StatusOK, item)
}

func promptMatchesKeyword(item promptItemResponse, keyword string) bool {
	if keyword == "" {
		return true
	}
	searchable := strings.ToLower(item.DisplayName + "\n" + item.Content)
	return strings.Contains(searchable, keyword)
}

func promptMatchesScope(item promptItemResponse, scope string) bool {
	switch scope {
	case "recent":
		return item.UsageCount > 0
	case "favorite":
		return item.IsFavorite
	case "custom":
		return item.Source == "custom"
	default:
		return true
	}
}

func sortPromptItems(items []promptItemResponse, sortBy string) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		switch sortBy {
		case "usage_desc":
			if left.UsageCount != right.UsageCount {
				return left.UsageCount > right.UsageCount
			}
		case "name_asc":
			return strings.ToLower(left.DisplayName) < strings.ToLower(right.DisplayName)
		default:
			if left.UpdatedAt != nil || right.UpdatedAt != nil {
				if left.UpdatedAt == nil {
					return false
				}
				if right.UpdatedAt == nil {
					return true
				}
				if !left.UpdatedAt.Equal(*right.UpdatedAt) {
					return left.UpdatedAt.After(*right.UpdatedAt)
				}
			}
		}
		return left.DisplayName < right.DisplayName
	})
}

func ListPrompts(w http.ResponseWriter, r *http.Request) {
	userID := corestore.UserID(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	pageSize := 50
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 && value <= 1000 {
			pageSize = value
		}
	}
	start := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("page_token")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 0 {
			start = value
		}
	}
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if category != "" && !validPromptCategoryForUser(userID, category) {
		common.ReplyErr(w, "invalid category", http.StatusBadRequest)
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "recent" && scope != "favorite" && scope != "custom" {
		common.ReplyErr(w, "invalid scope", http.StatusBadRequest)
		return
	}
	sortBy := strings.TrimSpace(r.URL.Query().Get("sort"))
	if sortBy == "" {
		sortBy = "updated_desc"
	}
	if sortBy != "updated_desc" && sortBy != "usage_desc" && sortBy != "name_asc" {
		common.ReplyErr(w, "invalid sort", http.StatusBadRequest)
		return
	}
	keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
	states, err := loadPromptStates(userID)
	if err != nil {
		common.ReplyErr(w, "query prompt states failed", http.StatusInternalServerError)
		return
	}
	customCategories, err := listPromptCategories(userID)
	if err != nil {
		common.ReplyErr(w, "query prompt categories failed", http.StatusInternalServerError)
		return
	}
	var customPrompts []orm.Prompt
	if err := corestore.DB().Where("create_user_id = ?", userID).Find(&customPrompts).Error; err != nil {
		common.ReplyErr(w, "query prompts failed", http.StatusInternalServerError)
		return
	}
	allItems := make([]promptItemResponse, 0, len(presetPrompts)+len(customPrompts))
	for _, preset := range presetPrompts {
		allItems = append(allItems, localizedPresetItem(preset, r.URL.Query().Get("locale"), states[preset.ID]))
	}
	for _, prompt := range customPrompts {
		allItems = append(allItems, customPromptItem(prompt, states[prompt.ID]))
	}

	facets := promptFacetResponse{
		Scopes: map[string]int64{
			"all":      0,
			"recent":   0,
			"favorite": 0,
			"custom":   0,
		},
		Categories: map[string]int64{},
	}
	keywordItems := make([]promptItemResponse, 0, len(allItems))
	for _, item := range allItems {
		if !promptMatchesKeyword(item, keyword) {
			continue
		}
		keywordItems = append(keywordItems, item)
		facets.Scopes["all"]++
		facets.Categories[item.Category]++
		if item.UsageCount > 0 {
			facets.Scopes["recent"]++
		}
		if item.IsFavorite {
			facets.Scopes["favorite"]++
		}
		if item.Source == "custom" {
			facets.Scopes["custom"]++
		}
	}
	filtered := make([]promptItemResponse, 0, len(keywordItems))
	for _, item := range keywordItems {
		if category != "" && item.Category != category {
			continue
		}
		if promptMatchesScope(item, scope) {
			filtered = append(filtered, item)
		}
	}
	sortPromptItems(filtered, sortBy)
	total := len(filtered)
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	nextToken := ""
	if end < total {
		nextToken = strconv.Itoa(end)
	}
	writePromptJSON(w, http.StatusOK, promptListResponse{
		Prompts:          filtered[start:end],
		CustomCategories: customCategories,
		NextPageToken:    nextToken,
		Total:            int64(total),
		Facets:           facets,
	})
}

func setPromptFavorite(w http.ResponseWriter, r *http.Request, favorite bool) {
	promptID := promptNameFromPath(r)
	userID := corestore.UserID(r)
	userName := corestore.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	if !promptExistsForUser(userID, promptID) {
		common.ReplyErr(w, "prompt not found", http.StatusNotFound)
		return
	}
	if err := upsertPromptFavorite(userID, userName, promptID, favorite); err != nil {
		common.ReplyErr(w, "update favorite failed", http.StatusInternalServerError)
		return
	}
	var state orm.PromptUserState
	_ = corestore.DB().Where("create_user_id = ? AND prompt_id = ?", userID, promptID).First(&state).Error
	writePromptJSON(w, http.StatusOK, promptStateResponse{
		ID:         promptID,
		IsFavorite: state.IsFavorite,
		UsageCount: state.UsageCount,
		LastUsedAt: state.LastUsedAt,
	})
}

func FavoritePrompt(w http.ResponseWriter, r *http.Request) {
	setPromptFavorite(w, r, true)
}

func UnfavoritePrompt(w http.ResponseWriter, r *http.Request) {
	setPromptFavorite(w, r, false)
}

func UsePrompt(w http.ResponseWriter, r *http.Request) {
	promptID := promptNameFromPath(r)
	userID := corestore.UserID(r)
	userName := corestore.UserName(r)
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	if !promptExistsForUser(userID, promptID) {
		common.ReplyErr(w, "prompt not found", http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	state := orm.PromptUserState{
		ID:             newID("pus_"),
		PromptID:       promptID,
		UsageCount:     1,
		LastUsedAt:     &now,
		CreateUserID:   userID,
		CreateUserName: userName,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := corestore.DB().Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "create_user_id"}, {Name: "prompt_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"usage_count":  gorm.Expr("usage_count + ?", 1),
			"last_used_at": now,
			"updated_at":   now,
			"deleted_at":   nil,
		}),
	}).Create(&state).Error; err != nil {
		common.ReplyErr(w, "record prompt usage failed", http.StatusInternalServerError)
		return
	}
	_ = corestore.DB().Where("create_user_id = ? AND prompt_id = ?", userID, promptID).First(&state).Error
	writePromptJSON(w, http.StatusOK, promptStateResponse{
		ID:         promptID,
		IsFavorite: state.IsFavorite,
		UsageCount: state.UsageCount,
		LastUsedAt: state.LastUsedAt,
	})
}
