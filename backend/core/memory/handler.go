package memory

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	appLog "lazymind/core/log"
	"lazymind/core/modelconfig"
	"lazymind/core/resourcechange"
	"lazymind/core/resourceupdate"
	"lazymind/core/store"
)

type generateRequest struct {
	UserInstruct string `json:"user_instruct"`
}

type upsertRequest struct {
	Content *string `json:"content"`
	AutoEvo *bool   `json:"auto_evo"`
}

type draftPreviewResponse struct {
	ReviewResultID     string `json:"review_result_id"`
	ReviewStatus       string `json:"review_status"`
	DraftStatus        string `json:"draft_status"`
	DraftSourceVersion int64  `json:"draft_source_version"`
	CurrentContent     string `json:"current_content"`
	DraftContent       string `json:"draft_content"`
	Diff               string `json:"diff"`
}

const maxManagedContentChars = 1500

func payloadForLog(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func compactManagedContent(content string) string {
	return strings.Join(strings.Fields(content), "")
}

func validateManagedContentLength(content string) error {
	if utf8.RuneCountInString(compactManagedContent(content)) > maxManagedContentChars {
		return errors.New("content exceeds 1500 characters after removing whitespace")
	}
	return nil
}

func upsertManagedMemoryContent(r *http.Request, db *gorm.DB, userID, userName string, req upsertRequest, clearDraft bool) (*orm.SystemMemory, error) {
	existing, err := evolution.LoadSystemMemory(r.Context(), db, userID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	now := time.Now()
	resolvedAutoEvo := true
	if req.AutoEvo != nil {
		resolvedAutoEvo = *req.AutoEvo
	} else if existing != nil {
		resolvedAutoEvo = existing.AutoEvo
	}
	if existing == nil {
		content := ""
		if req.Content != nil {
			content = *req.Content
		}
		row := orm.SystemMemory{
			ID:                 evolution.NewID(),
			UserID:             userID,
			Content:            content,
			Version:            1,
			AutoEvo:            resolvedAutoEvo,
			AutoEvoApplyStatus: evolution.AutoEvoApplyStatusIdle,
			AutoEvoError:       "",
			UpdatedBy:          userID,
			UpdatedByName:      userName,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		row.ContentHash = evolution.HashSystemMemory(row)
		createValues := map[string]any{
			"id":                    row.ID,
			"user_id":               row.UserID,
			"content":               row.Content,
			"content_hash":          row.ContentHash,
			"version":               row.Version,
			"auto_evo":              row.AutoEvo,
			"auto_evo_apply_status": row.AutoEvoApplyStatus,
			"auto_evo_error":        row.AutoEvoError,
			"updated_by":            row.UpdatedBy,
			"updated_by_name":       row.UpdatedByName,
			"created_at":            row.CreatedAt,
			"updated_at":            row.UpdatedAt,
		}
		change := resourcechange.ContentChange{
			ResourceType:  orm.ResourceUpdateResourceTypeMemory,
			ResourceID:    row.ID,
			UserID:        userID,
			FromVersion:   0,
			ToVersion:     row.Version,
			BeforeContent: "",
			AfterContent:  row.Content,
			Source: resourcechange.Source{
				ChangeSource: resourcechange.ChangeSourceDirectSave,
				ChangedAt:    now,
			},
		}
		if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			return resourcechange.CreateIntoModel(r.Context(), tx, &orm.SystemMemory{}, createValues, change)
		}); err != nil {
			return nil, err
		}
		return &row, nil
	}

	newContent := existing.Content
	if req.Content != nil {
		newContent = *req.Content
	}
	hashRow := *existing
	hashRow.Content = newContent
	update := map[string]any{
		"content":         newContent,
		"content_hash":    evolution.HashSystemMemory(hashRow),
		"version":         existing.Version + 1,
		"updated_by":      userID,
		"updated_by_name": userName,
		"updated_at":      now,
	}
	if req.AutoEvo != nil {
		update["auto_evo"] = resolvedAutoEvo
		update["auto_evo_generation"] = gorm.Expr("auto_evo_generation + 1")
		update["auto_evo_apply_status"] = evolution.AutoEvoApplyStatusIdle
		update["auto_evo_error"] = ""
		if resolvedAutoEvo {
			update["auto_evo_finished_at"] = nil
		} else {
			update["auto_evo_started_at"] = nil
			update["auto_evo_finished_at"] = now
		}
	}
	if clearDraft {
		update["draft_content"] = ""
		update["draft_source_version"] = 0
		update["draft_status"] = ""
		update["draft_updated_at"] = nil
		update["ext"] = evolution.WithDraftSuggestionIDs(existing.Ext, nil)
	}
	change := resourcechange.ContentChange{
		ResourceType:  orm.ResourceUpdateResourceTypeMemory,
		ResourceID:    existing.ID,
		UserID:        userID,
		FromVersion:   existing.Version,
		ToVersion:     existing.Version + 1,
		BeforeContent: existing.Content,
		AfterContent:  newContent,
		Source: resourcechange.Source{
			ChangeSource: resourcechange.ChangeSourceDirectSave,
			ChangedAt:    now,
		},
	}
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		_, err := resourcechange.UpdateModel(r.Context(), tx, &orm.SystemMemory{}, func(query *gorm.DB) *gorm.DB {
			return query.Where("id = ? AND version = ?", existing.ID, existing.Version)
		}, update, change)
		return err
	}); err != nil {
		return nil, err
	}
	existing.Content = newContent
	existing.ContentHash = evolution.HashSystemMemory(*existing)
	existing.Version++
	if req.AutoEvo != nil {
		existing.AutoEvo = resolvedAutoEvo
		existing.AutoEvoGeneration++
		existing.AutoEvoApplyStatus = evolution.AutoEvoApplyStatusIdle
		existing.AutoEvoError = ""
	}
	existing.UpdatedBy = userID
	existing.UpdatedByName = userName
	existing.UpdatedAt = now
	if clearDraft {
		existing.DraftContent = ""
		existing.DraftSourceVersion = 0
		existing.DraftStatus = ""
		existing.DraftUpdatedAt = nil
		existing.Ext = evolution.WithDraftSuggestionIDs(existing.Ext, nil)
	}
	return existing, nil
}

func enableManagedMemoryAutoEvoWithDiscardedDraft(r *http.Request, db *gorm.DB, row *orm.SystemMemory, userID, userName string) (*orm.SystemMemory, error) {
	now := time.Now()
	update := map[string]any{
		"auto_evo":              true,
		"auto_evo_generation":   gorm.Expr("auto_evo_generation + 1"),
		"auto_evo_apply_status": evolution.AutoEvoApplyStatusIdle,
		"auto_evo_error":        "",
		"auto_evo_finished_at":  nil,
		"draft_content":         "",
		"draft_source_version":  0,
		"draft_status":          "",
		"draft_updated_at":      nil,
		"updated_by":            userID,
		"updated_by_name":       userName,
		"updated_at":            now,
		"ext":                   evolution.WithDraftSuggestionIDs(row.Ext, nil),
	}
	if err := db.WithContext(r.Context()).
		Model(&orm.SystemMemory{}).
		Where("id = ?", row.ID).
		Updates(update).Error; err != nil {
		return nil, err
	}
	row.AutoEvo = true
	row.AutoEvoGeneration++
	row.AutoEvoApplyStatus = evolution.AutoEvoApplyStatusIdle
	row.AutoEvoError = ""
	row.AutoEvoFinishedAt = nil
	row.DraftContent = ""
	row.DraftSourceVersion = 0
	row.DraftStatus = ""
	row.DraftUpdatedAt = nil
	row.UpdatedBy = userID
	row.UpdatedByName = userName
	row.UpdatedAt = now
	row.Ext = evolution.WithDraftSuggestionIDs(row.Ext, nil)
	return row, nil
}

func Upsert(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var req upsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if !hasMemoryUpsertField(req) {
		common.ReplyErr(w, "content or auto_evo required", http.StatusBadRequest)
		return
	}
	if req.Content != nil {
		if err := validateManagedContentLength(*req.Content); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	existing, err := evolution.LoadSystemMemory(r.Context(), db, userID)
	if err != nil && err != gorm.ErrRecordNotFound {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	pendingDraft := existing != nil && strings.TrimSpace(existing.DraftStatus) == "pending_confirm"
	if pendingDraft && (req.AutoEvo == nil || !*req.AutoEvo) {
		common.ReplyErr(w, "memory draft already pending_confirm", http.StatusConflict)
		return
	}

	var row *orm.SystemMemory
	if pendingDraft {
		row, err = enableManagedMemoryAutoEvoWithDiscardedDraft(r, db, existing, userID, userName)
		if err != nil {
			common.ReplyErr(w, "update memory failed", http.StatusInternalServerError)
			return
		}
	} else {
		row, err = upsertManagedMemoryContent(r, db, userID, userName, req, false)
		if err != nil {
			common.ReplyErr(w, "update memory failed", http.StatusInternalServerError)
			return
		}
	}

	if req.AutoEvo != nil && *req.AutoEvo {
		if existing != nil && !existing.AutoEvo {
			if err := resourceupdate.ScanPendingResultsForResource(r.Context(), db, orm.ResourceUpdateResourceTypeMemory, userID, row.ID); err != nil {
				appLog.Logger.Warn().Err(err).
					Str("component", "resource_update").
					Str("event", "resource_update.auto_evo_enabled.scan_failed").
					Str("resource_type", orm.ResourceUpdateResourceTypeMemory).
					Str("resource_id", row.ID).
					Str("route", "/memory").
					Str("user_id", userID).
					Str("memory_id", row.ID).
					Str("reason", "auto_evo_enabled_scan_failed").
					Msg("resource update scan on upsert failed")
			}
		}
	}

	reviewStatus, err := evolution.ManagedReviewStatusForResource(r.Context(), db, userID, evolution.ResourceTypeMemory)
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	item := evolution.NewManagedStateItem(evolution.ResourceTypeMemory, row, reviewStatus)
	if summary, err := resourcechange.LatestSummaryForResource(r.Context(), db, userID, orm.ResourceUpdateResourceTypeMemory, row.ID); err == nil {
		item.LatestVersionChange = summary
	}
	common.ReplyOK(w, item)
}

func DraftPreview(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	row, err := evolution.LoadSystemMemory(r.Context(), db, userID)
	if err == gorm.ErrRecordNotFound {
		common.ReplyErr(w, "memory not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	result, err := resourceupdate.LatestPendingMemoryReviewResult(r.Context(), db, userID, orm.ResourceUpdateResourceTypeMemory)
	if err != nil {
		resourceupdate.ReplyReviewError(w, err, "memory draft")
		return
	}

	diff, err := evolution.BuildContentDiff(row.Content, result.Content)
	if err != nil {
		common.ReplyErr(w, "build memory diff failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, draftPreviewResponse{
		ReviewResultID:     result.ID,
		ReviewStatus:       result.ReviewStatus,
		DraftStatus:        result.ReviewStatus,
		DraftSourceVersion: row.Version,
		CurrentContent:     row.Content,
		DraftContent:       result.Content,
		Diff:               diff,
	})
}

func Generate(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.UserInstruct = strings.TrimSpace(req.UserInstruct)
	if req.UserInstruct == "" {
		common.ReplyErr(w, "user_instruct required", http.StatusBadRequest)
		return
	}

	row, err := evolution.EnsureSystemMemory(r.Context(), db, userID, userName)
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	content, err := memoryGenerateBaseContent(*row)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
		return
	}

	algoReq := algo.ManagedGenerateRequest{
		Content:      content,
		UserInstruct: req.UserInstruct,
	}
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "load llm config failed", http.StatusInternalServerError)
		return
	}
	algoReq.LLMConfig = llmConfig
	appLog.Logger.Info().
		Str("route", "/memory:generate").
		Str("memory_id", row.ID).
		Str("user_id", userID).
		Str("payload", payloadForLog(algoReq)).
		Msg("requesting external memory generate")
	generated, err := algo.GenerateMemory(r.Context(), algoReq)
	if err != nil {
		common.ReplyErr(w, "memory generate failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	now := time.Now()
	update := map[string]any{
		"draft_content":        generated,
		"draft_source_version": row.Version,
		"draft_status":         "pending_confirm",
		"draft_updated_at":     now,
		"updated_by":           userID,
		"updated_by_name":      userName,
		"updated_at":           now,
		"ext":                  evolution.WithDraftSuggestionIDs(row.Ext, nil),
	}
	if err := db.WithContext(r.Context()).Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
		common.ReplyErr(w, "update memory draft failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"draft_status":         "pending_confirm",
		"draft_source_version": row.Version,
		"draft_content":        generated,
	})
}

func memoryGenerateBaseContent(row orm.SystemMemory) (string, error) {
	if strings.TrimSpace(row.DraftStatus) == "pending_confirm" {
		return row.DraftContent, nil
	}
	return row.Content, nil
}

func Confirm(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	result, err := resourceupdate.LatestPendingMemoryReviewResult(r.Context(), db, userID, orm.ResourceUpdateResourceTypeMemory)
	if err != nil {
		resourceupdate.ReplyReviewError(w, err, "memory draft")
		return
	}
	if _, err := resourceupdate.AcceptMemoryReviewResultByID(r.Context(), db, userID, result.ID); err != nil {
		resourceupdate.ReplyReviewError(w, err, "confirm memory draft")
		return
	}
	row, err := evolution.LoadSystemMemory(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"content": row.Content,
		"version": row.Version,
	})
}

func Discard(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}

	result, err := resourceupdate.LatestPendingMemoryReviewResult(r.Context(), db, userID, orm.ResourceUpdateResourceTypeMemory)
	if err != nil {
		resourceupdate.ReplyReviewError(w, err, "memory draft")
		return
	}
	if _, err := resourceupdate.RejectMemoryReviewResultByID(r.Context(), db, userID, result.ID); err != nil {
		resourceupdate.ReplyReviewError(w, err, "discard memory draft")
		return
	}
	common.ReplyOK(w, map[string]any{"discarded": true})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func hasMemoryUpsertField(req upsertRequest) bool {
	return req.Content != nil ||
		req.AutoEvo != nil
}
