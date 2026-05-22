package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	appLog "lazymind/core/log"
	"lazymind/core/store"
)

type suggestionRequest struct {
	SessionID   string                        `json:"session_id"`
	ID          string                        `json:"id"`
	SkillID     string                        `json:"skill_id"`
	Category    string                        `json:"category"`
	SkillName   string                        `json:"skill_name"`
	Suggestions []evolution.SuggestionPayload `json:"suggestions"`
}

type createRequest struct {
	SessionID string `json:"session_id"`
	Category  string `json:"category"`
	SkillName string `json:"skill_name"`
	Content   string `json:"content"`
}

type removeRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Category  string `json:"category"`
	SkillName string `json:"skill_name"`
	Reason    string `json:"reason"`
}

var (
	errSuggestionSkillNotFound    = errors.New("skill not found")
	errSuggestionSnapshotNotFound = errors.New("session snapshot not found")
)

func payloadForLog(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
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
	req.ID = strings.TrimSpace(req.ID)
	req.SkillID = strings.TrimSpace(req.SkillID)
	req.Category = strings.TrimSpace(req.Category)
	req.SkillName = strings.TrimSpace(req.SkillName)
	skillID := firstNonEmpty(req.SkillID, req.ID)
	appLog.Logger.Info().
		Str("route", "/skill/suggestion").
		Str("session_id", req.SessionID).
		Str("skill_id", skillID).
		Str("category", req.Category).
		Str("skill_name", req.SkillName).
		Int("suggestion_count", len(req.Suggestions)).
		Msg("internal skill mutation request received")
	if req.SessionID == "" || (skillID == "" && (req.Category == "" || req.SkillName == "")) {
		appLog.Logger.Warn().
			Str("route", "/skill/suggestion").
			Str("session_id", req.SessionID).
			Str("skill_id", skillID).
			Str("category", req.Category).
			Str("skill_name", req.SkillName).
			Msg("internal skill suggestion request rejected: missing required fields")
		common.ReplyErr(w, "session_id and id or category/skill_name required", http.StatusBadRequest)
		return
	}
	if len(req.Suggestions) == 0 || len(req.Suggestions) > 5 {
		appLog.Logger.Warn().
			Str("route", "/skill/suggestion").
			Str("session_id", req.SessionID).
			Int("suggestion_count", len(req.Suggestions)).
			Msg("internal skill suggestion request rejected: invalid suggestion count")
		common.ReplyErr(w, "suggestions length must be between 1 and 5", http.StatusBadRequest)
		return
	}
	for _, item := range req.Suggestions {
		if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Content) == "" {
			common.ReplyErr(w, "suggestion title/content required", http.StatusBadRequest)
			return
		}
	}

	userID, _, err := evolution.ResolveSessionUser(r.Context(), db, req.SessionID)
	if err != nil || strings.TrimSpace(userID) == "" {
		appLog.Logger.Warn().
			Err(err).
			Str("route", "/skill/suggestion").
			Str("session_id", req.SessionID).
			Msg("internal skill suggestion request rejected: unable to resolve session user")
		common.ReplyErr(w, "unable to resolve session user", http.StatusBadRequest)
		return
	}

	state, snapshot, err := resolveSuggestionSkillSnapshot(r.Context(), db, req, userID, skillID)
	if err != nil {
		if errors.Is(err, errSuggestionSkillNotFound) {
			common.ReplyErr(w, "skill not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, errSuggestionSnapshotNotFound) {
			common.ReplyErr(w, "session snapshot not found", http.StatusNotFound)
			return
		}
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	resourceKey := evolution.SkillSuggestionResourceKey(*state.Resource)

	status := evolution.SuggestionStatusPendingReview
	invalidReason := ""
	if state.ContentHash != snapshot.SnapshotHash {
		status = evolution.SuggestionStatusInvalid
		invalidReason = "snapshot hash mismatch"
	}

	rows := make([]orm.ResourceSuggestion, 0, len(req.Suggestions))
	result := make([]evolution.RecordedSuggestion, 0, len(req.Suggestions))
	for _, item := range req.Suggestions {
		row := evolution.BuildSuggestionRecord(userID, evolution.ResourceTypeSkill, resourceKey, evolution.SuggestionActionModify, req.SessionID, status)
		row.Category = strings.TrimSpace(state.Resource.Category)
		row.ParentSkillName = strings.TrimSpace(state.Resource.ParentSkillName)
		if row.ParentSkillName == "" {
			row.ParentSkillName = strings.TrimSpace(state.Resource.SkillName)
		}
		row.SkillName = strings.TrimSpace(state.Resource.SkillName)
		row.FileExt = firstNonEmpty(strings.TrimSpace(state.Resource.FileExt), "md")
		row.RelativePath = state.RelativePath
		row.SnapshotHash = snapshot.SnapshotHash
		row.Title = strings.TrimSpace(item.Title)
		row.Content = strings.TrimSpace(item.Content)
		row.Reason = strings.TrimSpace(item.Reason)
		row.InvalidReason = invalidReason
		rows = append(rows, row)
		result = append(result, evolution.RecordedSuggestion{
			ID:            row.ID,
			Status:        row.Status,
			InvalidReason: row.InvalidReason,
		})
	}
	if err := db.WithContext(r.Context()).Create(&rows).Error; err != nil {
		appLog.Logger.Error().
			Err(err).
			Str("route", "/skill/suggestion").
			Str("session_id", req.SessionID).
			Str("user_id", userID).
			Str("category", req.Category).
			Str("skill_name", req.SkillName).
			Msg("internal skill suggestion request failed to persist")
		common.ReplyErr(w, "create suggestions failed", http.StatusInternalServerError)
		return
	}
	appLog.Logger.Info().
		Str("route", "/skill/suggestion").
		Str("session_id", req.SessionID).
		Str("user_id", userID).
		Str("category", req.Category).
		Str("skill_name", req.SkillName).
		Int("created_count", len(rows)).
		Msg("internal skill suggestion request persisted")

	if state.Resource.AutoEvo && status != evolution.SuggestionStatusInvalid {
		if scheduleErr := ensureSkillAutoEvolutionScheduled(*state.Resource); scheduleErr != nil {
			appLog.Logger.Warn().
				Err(scheduleErr).
				Str("route", "/skill/suggestion").
				Str("session_id", req.SessionID).
				Str("user_id", userID).
				Str("category", req.Category).
				Str("skill_name", req.SkillName).
				Msg("auto_evo schedule failed, suggestions kept for manual review")
		}
	}
	common.ReplyOK(w, map[string]any{"items": result})
}

func resolveSuggestionSkillSnapshot(ctx context.Context, db *gorm.DB, req suggestionRequest, userID, skillID string) (*evolution.SkillState, *orm.ResourceSessionSnapshot, error) {
	var state *evolution.SkillState
	var err error
	if skillID != "" {
		state, err = evolution.LoadSkillStateByResourceKey(ctx, db, userID, skillID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errSuggestionSkillNotFound
		}
	} else {
		snapshot, snapshotErr := evolution.FindSkillSnapshotByIdentity(ctx, db, req.SessionID, userID, req.Category, req.SkillName)
		if snapshotErr != nil {
			if errors.Is(snapshotErr, gorm.ErrRecordNotFound) {
				return nil, nil, errSuggestionSnapshotNotFound
			}
			return nil, nil, snapshotErr
		}
		state, err = evolution.LoadSkillStateByResourceKey(ctx, db, userID, snapshot.ResourceKey)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, errSuggestionSkillNotFound
			}
			return nil, nil, err
		}
		return state, snapshot, nil
	}
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := evolution.FindSnapshot(ctx, db, req.SessionID, evolution.ResourceTypeSkill, evolution.SkillSuggestionResourceKey(*state.Resource))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errSuggestionSnapshotNotFound
		}
		return nil, nil, err
	}
	return state, snapshot, nil
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
	if err := createParentSkillWithContent(r.Context(), db, userID, userName, createReq, req.Content, description); err != nil {
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

func Remove(w http.ResponseWriter, r *http.Request) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}

	var req removeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.ID = strings.TrimSpace(req.ID)
	req.Category = strings.TrimSpace(req.Category)
	req.SkillName = strings.TrimSpace(req.SkillName)
	req.Reason = strings.TrimSpace(req.Reason)
	userID, err := resolveRemoveRequestUser(r.Context(), db, r, req)
	if err != nil {
		appLog.Logger.Warn().
			Err(err).
			Str("route", "/skill/remove").
			Str("session_id", req.SessionID).
			Str("skill_id", req.ID).
			Msg("skill remove request rejected: unable to resolve user")
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	appLog.Logger.Info().
		Str("route", "/skill/remove").
		Str("user_id", userID).
		Str("skill_id", req.ID).
		Str("payload", payloadForLog(req)).
		Msg("skill remove request received")

	skillRow, err := loadRemoveSkill(r.Context(), db, userID, req)
	if err != nil {
		if errors.Is(err, errRemoveTargetRequired) {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "skill not found", http.StatusNotFound)
			return
		}
		appLog.Logger.Error().
			Err(err).
			Str("route", "/skill/remove").
			Str("user_id", userID).
			Str("skill_id", req.ID).
			Str("category", req.Category).
			Str("skill_name", req.SkillName).
			Msg("internal skill remove request failed to query skill")
		common.ReplyErr(w, "query skill failed", http.StatusInternalServerError)
		return
	}
	req.ID = firstNonEmpty(req.ID, strings.TrimSpace(skillRow.ID))
	req.Category = firstNonEmpty(req.Category, strings.TrimSpace(skillRow.Category))
	req.SkillName = firstNonEmpty(req.SkillName, strings.TrimSpace(skillRow.SkillName))

	if req.ID == "" {
		appLog.Logger.Warn().
			Str("route", "/skill/remove").
			Str("user_id", userID).
			Str("category", req.Category).
			Str("skill_name", req.SkillName).
			Msg("skill remove request rejected: missing resolved id")
		common.ReplyErr(w, "id required", http.StatusBadRequest)
		return
	}

	status := evolution.SuggestionStatusPendingReview
	invalidReason := ""
	resourceKey := evolution.SkillSuggestionResourceKey(skillRow)
	snapshotHash := strings.TrimSpace(skillRow.ContentHash)
	if snapshotHash == "" {
		snapshotHash = evolution.HashContent(skillRow.Content)
	}

	now := time.Now()
	title := fmt.Sprintf("删除技能: %s/%s", req.Category, req.SkillName)
	parentSkillName := firstNonEmpty(strings.TrimSpace(skillRow.ParentSkillName), strings.TrimSpace(skillRow.SkillName))

	var existingRow orm.ResourceSuggestion
	upsertErr := db.WithContext(r.Context()).
		Where("user_id = ? AND resource_type = ? AND action = ? AND resource_key = ? AND status = ?",
			userID, evolution.ResourceTypeSkill, evolution.SuggestionActionRemove,
			resourceKey, evolution.SuggestionStatusPendingReview).
		Take(&existingRow).Error

	var row orm.ResourceSuggestion
	if upsertErr == nil {
		row = existingRow
		update := map[string]any{
			"content":        req.Reason,
			"reason":         req.Reason,
			"snapshot_hash":  snapshotHash,
			"status":         status,
			"invalid_reason": invalidReason,
			"title":          title,
			"updated_at":     now,
		}
		if err := db.WithContext(r.Context()).Model(&orm.ResourceSuggestion{}).Where("id = ?", row.ID).Updates(update).Error; err != nil {
			appLog.Logger.Error().
				Err(err).
				Str("route", "/skill/remove").
				Str("session_id", req.SessionID).
				Str("user_id", userID).
				Str("suggestion_id", row.ID).
				Msg("internal skill remove request failed to update existing suggestion")
			common.ReplyErr(w, "update suggestion failed", http.StatusInternalServerError)
			return
		}
		row.Content = req.Reason
		row.Reason = req.Reason
		row.SnapshotHash = snapshotHash
		row.Status = status
		row.InvalidReason = invalidReason
		row.Title = title
		row.UpdatedAt = now
		appLog.Logger.Info().
			Str("route", "/skill/remove").
			Str("session_id", req.SessionID).
			Str("user_id", userID).
			Str("suggestion_id", row.ID).
			Msg("internal skill remove request updated existing suggestion")
	} else if errors.Is(upsertErr, gorm.ErrRecordNotFound) {
		row = evolution.BuildSuggestionRecord(userID, evolution.ResourceTypeSkill, resourceKey, evolution.SuggestionActionRemove, req.SessionID, status)
		row.Category = req.Category
		row.ParentSkillName = parentSkillName
		row.SkillName = strings.TrimSpace(skillRow.SkillName)
		row.FileExt = firstNonEmpty(strings.TrimSpace(skillRow.FileExt), "md")
		row.RelativePath = filepath.ToSlash(strings.TrimSpace(skillRow.RelativePath))
		row.SnapshotHash = snapshotHash
		row.Title = title
		row.Content = req.Reason
		row.Reason = req.Reason
		row.InvalidReason = invalidReason
		if err := db.WithContext(r.Context()).Create(&row).Error; err != nil {
			appLog.Logger.Error().
				Err(err).
				Str("route", "/skill/remove").
				Str("session_id", req.SessionID).
				Str("user_id", userID).
				Str("category", req.Category).
				Str("skill_name", req.SkillName).
				Msg("internal skill remove request failed to persist suggestion")
			common.ReplyErr(w, "create suggestion failed", http.StatusInternalServerError)
			return
		}
		appLog.Logger.Info().
			Str("route", "/skill/remove").
			Str("session_id", req.SessionID).
			Str("user_id", userID).
			Str("suggestion_id", row.ID).
			Msg("internal skill remove request created suggestion")
	} else {
		appLog.Logger.Error().
			Err(upsertErr).
			Str("route", "/skill/remove").
			Str("user_id", userID).
			Msg("internal skill remove request failed to query existing suggestion")
		common.ReplyErr(w, "query suggestion failed", http.StatusInternalServerError)
		return
	}

	result := evolution.RecordedSuggestion{
		ID:            row.ID,
		Status:        row.Status,
		InvalidReason: row.InvalidReason,
	}

	if skillRow.AutoEvo && status != evolution.SuggestionStatusInvalid {
		if err := disableSkillAutoEvoForPendingRemove(r.Context(), db, skillRow); err != nil {
			appLog.Logger.Error().
				Err(err).
				Str("route", "/skill/remove").
				Str("session_id", req.SessionID).
				Str("user_id", userID).
				Str("skill_id", skillRow.ID).
				Msg("auto_evo disable failed for pending remove suggestion")
		} else {
			appLog.Logger.Info().
				Str("route", "/skill/remove").
				Str("session_id", req.SessionID).
				Str("user_id", userID).
				Str("skill_id", skillRow.ID).
				Str("suggestion_id", row.ID).
				Msg("auto_evo disabled for pending remove suggestion")
		}
	}

	common.ReplyOK(w, map[string]any{"items": []evolution.RecordedSuggestion{result}})
}

var errRemoveTargetRequired = errors.New("id or category/skill_name required")

func resolveRemoveRequestUser(ctx context.Context, db *gorm.DB, r *http.Request, req removeRequest) (string, error) {
	if userID := strings.TrimSpace(store.UserID(r)); userID != "" {
		return userID, nil
	}
	if req.SessionID == "" {
		return "", errors.New("session_id required")
	}
	userID, _, err := evolution.ResolveSessionUser(ctx, db, req.SessionID)
	if err != nil || strings.TrimSpace(userID) == "" {
		if err != nil {
			return "", fmt.Errorf("unable to resolve session user: %w", err)
		}
		return "", errors.New("unable to resolve session user")
	}
	return strings.TrimSpace(userID), nil
}

func loadRemoveSkill(ctx context.Context, db *gorm.DB, userID string, req removeRequest) (orm.SkillResource, error) {
	if req.ID != "" {
		var skillRow orm.SkillResource
		err := db.WithContext(ctx).Where("owner_user_id = ? AND id = ?", userID, req.ID).Take(&skillRow).Error
		return skillRow, err
	}
	if req.Category == "" || req.SkillName == "" {
		return orm.SkillResource{}, errRemoveTargetRequired
	}
	state, err := evolution.LoadParentSkillState(ctx, db, userID, req.Category, req.SkillName)
	if err != nil {
		return orm.SkillResource{}, err
	}
	return *state.Resource, nil
}

func init() {
	evolution.ApplyRemoveSuggestion = applyRemoveSuggestion
}

func applyRemoveSuggestion(ctx context.Context, db *gorm.DB, suggestion orm.ResourceSuggestion) error {
	var skill orm.SkillResource
	err := db.WithContext(ctx).
		Where("owner_user_id = ? AND id = ?",
			strings.TrimSpace(suggestion.UserID),
			strings.TrimSpace(suggestion.ResourceKey),
		).
		Take(&skill).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	return DeleteSkill(ctx, db, suggestion.UserID, skill.ID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
