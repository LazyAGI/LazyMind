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
	"lazymind/core/store"
)

type suggestionRequest struct {
	SessionID   string                        `json:"session_id"`
	Suggestions []evolution.SuggestionPayload `json:"suggestions"`
}

type generateRequest struct {
	SuggestionIDs []string `json:"suggestion_ids"`
	UserInstruct  string   `json:"user_instruct"`
}

type upsertRequest struct {
	Content *string `json:"content"`
	AutoEvo *bool   `json:"auto_evo"`
}

const errAutoEvoTaskRunning = "auto_evo task is running"

type draftPreviewResponse struct {
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

func upsertManagedMemoryContent(r *http.Request, db *gorm.DB, userID, userName, content string, autoEvo *bool, clearDraft bool) (*orm.SystemMemory, error) {
	existing, err := evolution.LoadSystemMemory(r.Context(), db, userID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, err
	}

	now := time.Now()
	resolvedAutoEvo := true
	if autoEvo != nil {
		resolvedAutoEvo = *autoEvo
	} else if existing != nil {
		resolvedAutoEvo = existing.AutoEvo
	}
	if existing == nil {
		row := orm.SystemMemory{
			ID:                 evolution.NewID(),
			UserID:             userID,
			Content:            content,
			ContentHash:        evolution.HashContent(content),
			Version:            1,
			AutoEvo:            resolvedAutoEvo,
			AutoEvoApplyStatus: evolution.AutoEvoApplyStatusIdle,
			AutoEvoError:       "",
			UpdatedBy:          userID,
			UpdatedByName:      userName,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := db.WithContext(r.Context()).Model(&orm.SystemMemory{}).Create(map[string]any{
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
		}).Error; err != nil {
			return nil, err
		}
		return &row, nil
	}

	update := map[string]any{
		"content":         content,
		"content_hash":    evolution.HashContent(content),
		"version":         existing.Version + 1,
		"updated_by":      userID,
		"updated_by_name": userName,
		"updated_at":      now,
	}
	if autoEvo != nil {
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
	if err := db.WithContext(r.Context()).
		Model(&orm.SystemMemory{}).
		Where("id = ? AND version = ?", existing.ID, existing.Version).
		Updates(update).Error; err != nil {
		return nil, err
	}
	existing.Content = content
	existing.ContentHash = evolution.HashContent(content)
	existing.Version++
	if autoEvo != nil {
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
	if req.Content == nil {
		common.ReplyErr(w, "content required", http.StatusBadRequest)
		return
	}
	content := *req.Content
	if err := validateManagedContentLength(content); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}

	existing, err := evolution.LoadSystemMemory(r.Context(), db, userID)
	if err != nil && err != gorm.ErrRecordNotFound {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	pendingDraft := existing != nil && strings.TrimSpace(existing.DraftStatus) == "pending_confirm"
	if existing != nil && req.AutoEvo != nil && evolution.HasAutoEvoWorker(evolution.AutoEvoWorkerKey(evolution.ResourceTypeMemory, existing.ID)) {
		common.ReplyErr(w, errAutoEvoTaskRunning, http.StatusConflict)
		return
	}
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
		row, err = upsertManagedMemoryContent(r, db, userID, userName, content, req.AutoEvo, false)
		if err != nil {
			common.ReplyErr(w, "update memory failed", http.StatusInternalServerError)
			return
		}
	}

	if req.AutoEvo != nil && *req.AutoEvo {
		if err := evolution.EnsureManagedMemoryAutoEvolutionScheduled(*row); err != nil {
			appLog.Logger.Warn().Err(err).Str("route", "/memory").Msg("auto_evo schedule on upsert failed")
		}
	}

	suggestionStatus, err := evolution.ManagedSuggestionStatusForResource(r.Context(), db, userID, evolution.ResourceTypeMemory)
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, evolution.NewManagedStateItem(evolution.ResourceTypeMemory, row, suggestionStatus))
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
	if strings.TrimSpace(row.DraftStatus) != "pending_confirm" {
		common.ReplyErr(w, "memory draft not found", http.StatusNotFound)
		return
	}

	diff, err := evolution.BuildContentDiff(row.Content, row.DraftContent)
	if err != nil {
		common.ReplyErr(w, "build memory diff failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, draftPreviewResponse{
		DraftStatus:        row.DraftStatus,
		DraftSourceVersion: row.DraftSourceVersion,
		CurrentContent:     row.Content,
		DraftContent:       row.DraftContent,
		Diff:               diff,
	})
}

func Suggestion(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	var req suggestionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	appLog.Logger.Info().
		Str("route", "/memory/suggestion").
		Str("session_id", req.SessionID).
		Int("suggestion_count", len(req.Suggestions)).
		Msg("internal memory mutation request received")
	if req.SessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	if len(req.Suggestions) == 0 || len(req.Suggestions) > 5 {
		common.ReplyErr(w, "suggestions length must be between 1 and 5", http.StatusBadRequest)
		return
	}
	for _, item := range req.Suggestions {
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Content) == "" {
			common.ReplyErr(w, "suggestion title/content required", http.StatusBadRequest)
			return
		}
	}

	userID, userName, err := evolution.ResolveSessionUser(r.Context(), db, req.SessionID)
	if err != nil || strings.TrimSpace(userID) == "" {
		appLog.Logger.Warn().
			Err(err).
			Str("route", "/memory/suggestion").
			Str("session_id", req.SessionID).
			Msg("internal memory suggestion request rejected: unable to resolve session user")
		common.ReplyErr(w, "unable to resolve session user", http.StatusBadRequest)
		return
	}
	resource, err := evolution.EnsureSystemMemory(r.Context(), db, userID, userName)
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	resourceKey := evolution.SystemResourceKey(evolution.ResourceTypeMemory)
	snapshot, err := evolution.FindSnapshot(r.Context(), db, req.SessionID, evolution.ResourceTypeMemory, resourceKey)
	if err != nil {
		common.ReplyErr(w, "session snapshot not found", http.StatusNotFound)
		return
	}

	status := evolution.SuggestionStatusPendingReview
	invalidReason := ""
	currentHash := firstNonEmpty(strings.TrimSpace(resource.ContentHash), evolution.HashContent(resource.Content))
	if currentHash != snapshot.SnapshotHash {
		status = evolution.SuggestionStatusInvalid
		invalidReason = "snapshot hash mismatch"
	}

	rows := make([]orm.ResourceSuggestion, 0, len(req.Suggestions))
	resp := make([]evolution.RecordedSuggestion, 0, len(req.Suggestions))
	for _, item := range req.Suggestions {
		row := evolution.BuildSuggestionRecord(userID, evolution.ResourceTypeMemory, resourceKey, evolution.SuggestionActionModify, req.SessionID, status)
		row.SnapshotHash = snapshot.SnapshotHash
		row.Title = strings.TrimSpace(item.Title)
		row.Content = strings.TrimSpace(item.Content)
		row.Reason = strings.TrimSpace(item.Reason)
		row.InvalidReason = invalidReason
		rows = append(rows, row)
		resp = append(resp, evolution.RecordedSuggestion{
			ID:            row.ID,
			Status:        row.Status,
			InvalidReason: row.InvalidReason,
		})
	}
	if err := db.WithContext(r.Context()).Create(&rows).Error; err != nil {
		appLog.Logger.Error().
			Err(err).
			Str("route", "/memory/suggestion").
			Str("session_id", req.SessionID).
			Str("user_id", userID).
			Msg("internal memory suggestion request failed to persist")
		common.ReplyErr(w, "create suggestions failed", http.StatusInternalServerError)
		return
	}
	appLog.Logger.Info().
		Str("route", "/memory/suggestion").
		Str("session_id", req.SessionID).
		Str("user_id", userID).
		Int("created_count", len(rows)).
		Msg("internal memory suggestion request persisted")

	if resource.AutoEvo && status != evolution.SuggestionStatusInvalid {
		if err := evolution.EnsureManagedMemoryAutoEvolutionScheduled(*resource); err != nil {
			appLog.Logger.Warn().
				Err(err).
				Str("route", "/memory/suggestion").
				Str("session_id", req.SessionID).
				Str("user_id", userID).
				Msg("auto_evo schedule failed, suggestions kept for manual review")
		}
	}
	common.ReplyOK(w, map[string]any{"items": resp})
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
	req.SuggestionIDs = compactIDs(req.SuggestionIDs)
	req.UserInstruct = strings.TrimSpace(req.UserInstruct)
	if len(req.SuggestionIDs) == 0 && req.UserInstruct == "" {
		common.ReplyErr(w, "suggestion_ids or user_instruct required", http.StatusBadRequest)
		return
	}

	row, err := evolution.EnsureSystemMemory(r.Context(), db, userID, userName)
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	useDraft := len(req.SuggestionIDs) == 0 && req.UserInstruct != ""
	content, err := memoryGenerateBaseContent(*row, useDraft)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
		return
	}

	var suggestions []orm.ResourceSuggestion
	if len(req.SuggestionIDs) > 0 {
		suggestions, err = evolution.LoadApprovedSuggestions(r.Context(), db, userID, evolution.ResourceTypeMemory, evolution.SystemResourceKey(evolution.ResourceTypeMemory), req.SuggestionIDs)
		if err != nil {
			common.ReplyErr(w, "query suggestions failed", http.StatusInternalServerError)
			return
		}
		if len(suggestions) == 0 {
			common.ReplyErr(w, "no accepted suggestions found", http.StatusBadRequest)
			return
		}
	}

	algoReq := algo.MemoryGenerateRequest{
		Content:      content,
		Suggestions:  toAlgoSuggestions(suggestions),
		UserInstruct: req.UserInstruct,
	}
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
	ids := suggestionIDs(suggestions)
	if useDraft && len(ids) == 0 {
		ids = evolution.DraftSuggestionIDs(row.Ext)
	}
	ext := evolution.WithDraftSuggestionIDs(row.Ext, ids)
	update := map[string]any{
		"draft_content":        generated,
		"draft_source_version": row.Version,
		"draft_status":         "pending_confirm",
		"draft_updated_at":     now,
		"updated_by":           userID,
		"updated_by_name":      userName,
		"updated_at":           now,
		"ext":                  ext,
	}
	if err := db.WithContext(r.Context()).Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
		common.ReplyErr(w, "update memory draft failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"draft_status":         "pending_confirm",
		"draft_source_version": row.Version,
		"draft_content":        generated,
		"suggestion_ids":       ids,
	})
}

func memoryGenerateBaseContent(row orm.SystemMemory, useDraft bool) (string, error) {
	if !useDraft {
		return row.Content, nil
	}
	if strings.TrimSpace(row.DraftStatus) != "pending_confirm" {
		return "", errors.New("memory draft not found")
	}
	return row.DraftContent, nil
}

func Confirm(w http.ResponseWriter, r *http.Request) {
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

	row, err := evolution.EnsureSystemMemory(r.Context(), db, userID, userName)
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(row.DraftStatus) != "pending_confirm" {
		common.ReplyErr(w, "memory draft not found", http.StatusNotFound)
		return
	}
	if row.Version != row.DraftSourceVersion {
		common.ReplyErr(w, "memory draft version conflict", http.StatusConflict)
		return
	}

	ids := evolution.DraftSuggestionIDs(row.Ext)
	now := time.Now()
	newContent := row.DraftContent
	newHash := evolution.HashContent(newContent)
	newExt := evolution.WithDraftSuggestionIDs(row.Ext, nil)
	update := map[string]any{
		"content":              newContent,
		"content_hash":         newHash,
		"version":              row.Version + 1,
		"draft_content":        "",
		"draft_source_version": 0,
		"draft_status":         "",
		"draft_updated_at":     nil,
		"updated_by":           userID,
		"updated_by_name":      userName,
		"updated_at":           now,
		"ext":                  newExt,
	}
	if err := db.WithContext(r.Context()).Model(&orm.SystemMemory{}).Where("id = ? AND version = ?", row.ID, row.Version).Updates(update).Error; err != nil {
		common.ReplyErr(w, "confirm memory draft failed", http.StatusInternalServerError)
		return
	}
	if err := evolution.UpdateSuggestionStatus(r.Context(), db, ids, evolution.SuggestionStatusApplied); err != nil {
		common.ReplyErr(w, "update suggestion status failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"content": newContent,
		"version": row.Version + 1,
	})
}

func Discard(w http.ResponseWriter, r *http.Request) {
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

	row, err := evolution.EnsureSystemMemory(r.Context(), db, userID, userName)
	if err != nil {
		common.ReplyErr(w, "query memory failed", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(row.DraftStatus) != "pending_confirm" {
		common.ReplyErr(w, "memory draft not found", http.StatusNotFound)
		return
	}

	now := time.Now()
	update := map[string]any{
		"draft_content":        "",
		"draft_source_version": 0,
		"draft_status":         "",
		"draft_updated_at":     nil,
		"updated_by":           userID,
		"updated_by_name":      userName,
		"updated_at":           now,
		"ext":                  evolution.WithDraftSuggestionIDs(row.Ext, nil),
	}
	if err := db.WithContext(r.Context()).Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
		common.ReplyErr(w, "discard memory draft failed", http.StatusInternalServerError)
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

func compactIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func suggestionIDs(rows []orm.ResourceSuggestion) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.ID) != "" {
			out = append(out, strings.TrimSpace(row.ID))
		}
	}
	return out
}

func toAlgoSuggestions(rows []orm.ResourceSuggestion) []algo.Suggestion {
	out := make([]algo.Suggestion, 0, len(rows))
	for _, row := range rows {
		out = append(out, algo.Suggestion{
			Title:   row.Title,
			Content: row.Content,
			Reason:  row.Reason,
		})
	}
	return out
}
