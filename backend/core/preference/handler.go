package preference

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
	"lazymind/core/filediff"
	appLog "lazymind/core/log"
	"lazymind/core/modelconfig"
	"lazymind/core/resourcechange"
	"lazymind/core/resourcefs"
	"lazymind/core/resourceupdate"
	"lazymind/core/store"
)

type generateRequest struct {
	UserInstruct string `json:"user_instruct"`
}

type upsertRequest struct {
	Content       *string `json:"content"`
	AgentPersona  *string `json:"agent_persona"`
	PreferredName *string `json:"preferred_name"`
	ResponseStyle *string `json:"response_style"`
	AutoEvo       *bool   `json:"auto_evo"`
}

type draftPreviewResponse struct {
	ReviewResultID     string             `json:"review_result_id"`
	ReviewStatus       string             `json:"review_status"`
	DraftStatus        string             `json:"draft_status"`
	DraftSourceVersion int64              `json:"draft_source_version"`
	CurrentContent     string             `json:"current_content"`
	DraftContent       string             `json:"draft_content"`
	Diff               string             `json:"diff"`
	FileDiff           *filediff.FileDiff `json:"file_diff,omitempty"`
	RevisionID         string             `json:"revision_id,omitempty"`
	DraftVersion       int64              `json:"draft_version,omitempty"`
	ReviewID           string             `json:"review_id,omitempty"`
	ReviewVersion      int64              `json:"review_version,omitempty"`
	CanUndo            bool               `json:"can_undo"`
	PendingCount       int                `json:"pending_count"`
	AcceptedCount      int                `json:"accepted_count"`
	RejectedCount      int                `json:"rejected_count"`
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

func upsertManagedPreferenceContent(r *http.Request, db *gorm.DB, userID, userName string, req upsertRequest, clearDraft bool) (*orm.SystemUserPreference, error) {
	existing, err := evolution.LoadSystemUserPreference(r.Context(), db, userID)
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
		row := orm.SystemUserPreference{
			ID:                 evolution.NewID(),
			UserID:             userID,
			Content:            stringFromPtr(req.Content),
			AgentPersona:       stringFromPtr(req.AgentPersona),
			PreferredName:      stringFromPtr(req.PreferredName),
			ResponseStyle:      stringFromPtr(req.ResponseStyle),
			Version:            1,
			AutoEvo:            resolvedAutoEvo,
			AutoEvoApplyStatus: evolution.AutoEvoApplyStatusIdle,
			AutoEvoError:       "",
			UpdatedBy:          userID,
			UpdatedByName:      userName,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		row.ContentHash = evolution.HashSystemUserPreference(row)
		createValues := map[string]any{
			"id":                    row.ID,
			"user_id":               row.UserID,
			"content":               row.Content,
			"agent_persona":         row.AgentPersona,
			"preferred_name":        row.PreferredName,
			"response_style":        row.ResponseStyle,
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
			ResourceType:  orm.ResourceUpdateResourceTypeUserPreference,
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
			return resourcechange.CreateIntoModel(r.Context(), tx, &orm.SystemUserPreference{}, createValues, change)
		}); err != nil {
			return nil, err
		}
		return &row, nil
	}

	newContent := existing.Content
	newAgentPersona := existing.AgentPersona
	newPreferredName := existing.PreferredName
	newResponseStyle := existing.ResponseStyle
	if req.Content != nil {
		newContent = *req.Content
	}
	if req.AgentPersona != nil {
		newAgentPersona = *req.AgentPersona
	}
	if req.PreferredName != nil {
		newPreferredName = *req.PreferredName
	}
	if req.ResponseStyle != nil {
		newResponseStyle = *req.ResponseStyle
	}
	hashRow := *existing
	hashRow.Content = newContent
	hashRow.AgentPersona = newAgentPersona
	hashRow.PreferredName = newPreferredName
	hashRow.ResponseStyle = newResponseStyle
	update := map[string]any{
		"content":         newContent,
		"agent_persona":   newAgentPersona,
		"preferred_name":  newPreferredName,
		"response_style":  newResponseStyle,
		"content_hash":    evolution.HashSystemUserPreference(hashRow),
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
		ResourceType:  orm.ResourceUpdateResourceTypeUserPreference,
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
		_, err := resourcechange.UpdateModel(r.Context(), tx, &orm.SystemUserPreference{}, func(query *gorm.DB) *gorm.DB {
			return query.Where("id = ? AND version = ?", existing.ID, existing.Version)
		}, update, change)
		return err
	}); err != nil {
		return nil, err
	}
	existing.Content = newContent
	existing.AgentPersona = newAgentPersona
	existing.PreferredName = newPreferredName
	existing.ResponseStyle = newResponseStyle
	existing.ContentHash = evolution.HashSystemUserPreference(*existing)
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

func enableManagedPreferenceAutoEvoWithDiscardedDraft(r *http.Request, db *gorm.DB, row *orm.SystemUserPreference, userID, userName string) (*orm.SystemUserPreference, error) {
	now := time.Now()
	newExt := evolution.WithDraftSuggestionIDs(row.Ext, nil)
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
		"ext":                   newExt,
	}
	if err := db.WithContext(r.Context()).
		Model(&orm.SystemUserPreference{}).
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
	row.Ext = newExt
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
	if !hasPreferenceUpsertField(req) {
		common.ReplyErr(w, "content, user_preference metadata, or auto_evo required", http.StatusBadRequest)
		return
	}
	if req.Content != nil {
		if err := validateManagedContentLength(*req.Content); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if req.AgentPersona != nil {
		if err := validateManagedContentLength(*req.AgentPersona); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if req.PreferredName != nil {
		if err := validateManagedContentLength(*req.PreferredName); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if req.ResponseStyle != nil {
		if err := validateManagedContentLength(*req.ResponseStyle); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if _, err := evolution.LoadSystemUserPreference(r.Context(), db, userID); err == gorm.ErrRecordNotFound {
		row, err := upsertManagedPreferenceContent(r, db, userID, userName, req, true)
		if err != nil {
			common.ReplyErr(w, "update user_preference failed", http.StatusInternalServerError)
			return
		}
		if _, _, _, err := ensurePreferenceResource(r.Context(), db, userID, userName); err != nil {
			common.ReplyErr(w, "initialize user_preference resource failed", http.StatusInternalServerError)
			return
		}
		reviewStatus, err := evolution.ManagedReviewStatusForResource(r.Context(), db, userID, evolution.ResourceTypeUserPreference)
		if err != nil {
			common.ReplyErr(w, "query user_preference failed", http.StatusInternalServerError)
			return
		}
		item := evolution.NewManagedStateItem(evolution.ResourceTypeUserPreference, row, reviewStatus)
		if summary, err := resourcechange.LatestSummaryForResource(r.Context(), db, userID, orm.ResourceUpdateResourceTypeUserPreference, row.ID); err == nil {
			item.LatestVersionChange = summary
		}
		common.ReplyOK(w, item)
		return
	} else if err != nil {
		common.ReplyErr(w, "query user_preference failed", http.StatusInternalServerError)
		return
	}

	existing, fsService, _, err := ensurePreferenceResource(r.Context(), db, userID, userName)
	if err != nil {
		common.ReplyErr(w, "query user_preference failed", http.StatusInternalServerError)
		return
	}
	draft, pendingDraft, err := preferenceDraftIsPending(r.Context(), fsService, userID)
	if err != nil {
		common.ReplyErr(w, "query user_preference draft failed", http.StatusInternalServerError)
		return
	}
	if pendingDraft && (req.AutoEvo == nil || !*req.AutoEvo) {
		common.ReplyErr(w, "user_preference draft already pending_confirm", http.StatusConflict)
		return
	}

	var row *orm.SystemUserPreference
	if pendingDraft {
		if _, err := fsService.DiscardDraft(r.Context(), preferenceResourceRef(userID)); err != nil {
			common.ReplyErr(w, "discard user_preference draft failed", http.StatusInternalServerError)
			return
		}
		row, err = enableManagedPreferenceAutoEvoWithDiscardedDraft(r, db, existing, userID, userName)
		if err != nil {
			common.ReplyErr(w, "update user_preference failed", http.StatusInternalServerError)
			return
		}
	} else if req.Content != nil || req.AgentPersona != nil || req.PreferredName != nil || req.ResponseStyle != nil {
		head, err := fsService.ReadFile(r.Context(), resourcefs.ReadFileRequest{Ref: preferenceResourceRef(userID), RefType: resourcefs.FileRefHead})
		if err != nil {
			common.ReplyErr(w, "query user_preference file failed", http.StatusInternalServerError)
			return
		}
		nextContent, parsed, err := PatchFileContent(head.Content, PreferencePatch{
			Content:       req.Content,
			AgentPersona:  req.AgentPersona,
			PreferredName: req.PreferredName,
			ResponseStyle: req.ResponseStyle,
		})
		if err != nil {
			common.ReplyErr(w, "invalid user_preference file", http.StatusBadRequest)
			return
		}
		draftResp, err := fsService.WriteDraft(r.Context(), resourcefs.WriteDraftRequest{
			Ref:                  preferenceResourceRef(userID),
			Content:              nextContent,
			ExpectedDraftVersion: draft.DraftVersion,
			UpdatedBy:            userID,
		})
		if err != nil {
			common.ReplyErr(w, "update user_preference draft failed", http.StatusInternalServerError)
			return
		}
		commit, err := fsService.CommitDraft(r.Context(), resourcefs.CommitDraftRequest{
			Ref:                  preferenceResourceRef(userID),
			Message:              "direct user_preference update",
			SourceRefType:        "user_preference",
			SourceRefID:          existing.ID,
			ExpectedDraftVersion: draftResp.DraftVersion,
			CreatedBy:            userID,
		})
		if err != nil {
			common.ReplyErr(w, "commit user_preference draft failed", http.StatusInternalServerError)
			return
		}
		parsed, err = ParseFileContent(commit.Content)
		if err != nil {
			common.ReplyErr(w, "invalid committed user_preference file", http.StatusInternalServerError)
			return
		}
		content := parsed.Content
		agentPersona := parsed.AgentPersona
		preferredName := parsed.PreferredName
		responseStyle := parsed.ResponseStyle
		syncReq := upsertRequest{
			Content:       &content,
			AgentPersona:  &agentPersona,
			PreferredName: &preferredName,
			ResponseStyle: &responseStyle,
			AutoEvo:       req.AutoEvo,
		}
		row, err = upsertManagedPreferenceContent(r, db, userID, userName, syncReq, true)
		if err != nil {
			common.ReplyErr(w, "update user_preference failed", http.StatusInternalServerError)
			return
		}
	} else {
		row, err = upsertManagedPreferenceContent(r, db, userID, userName, req, false)
		if err != nil {
			common.ReplyErr(w, "update user_preference failed", http.StatusInternalServerError)
			return
		}
	}

	if req.AutoEvo != nil && *req.AutoEvo {
		if existing != nil && !existing.AutoEvo {
			if err := resourceupdate.ScanPendingResultsForResource(r.Context(), db, orm.ResourceUpdateResourceTypeUserPreference, userID, row.ID); err != nil {
				appLog.Logger.Warn().Err(err).
					Str("component", "resource_update").
					Str("event", "resource_update.auto_evo_enabled.scan_failed").
					Str("resource_type", orm.ResourceUpdateResourceTypeUserPreference).
					Str("resource_id", row.ID).
					Str("route", "/user-preference").
					Str("user_id", userID).
					Str("preference_id", row.ID).
					Str("reason", "auto_evo_enabled_scan_failed").
					Msg("resource update scan on upsert failed")
			}
		}
	}

	reviewStatus, err := evolution.ManagedReviewStatusForResource(r.Context(), db, userID, evolution.ResourceTypeUserPreference)
	if err != nil {
		common.ReplyErr(w, "query user_preference failed", http.StatusInternalServerError)
		return
	}
	item := evolution.NewManagedStateItem(evolution.ResourceTypeUserPreference, row, reviewStatus)
	if summary, err := resourcechange.LatestSummaryForResource(r.Context(), db, userID, orm.ResourceUpdateResourceTypeUserPreference, row.ID); err == nil {
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

	_, fsService, _, err := ensurePreferenceResource(r.Context(), db, userID, store.UserName(r))
	if err != nil {
		common.ReplyErr(w, "query user_preference failed", http.StatusInternalServerError)
		return
	}
	preview, err := fsService.DraftPreview(r.Context(), resourcefs.DraftPreviewRequest{Ref: preferenceResourceRef(userID)})
	if err != nil {
		common.ReplyErr(w, "user_preference draft not found", http.StatusNotFound)
		return
	}
	if strings.TrimSpace(preview.DraftStatus) != "pending_confirm" {
		common.ReplyErr(w, "user_preference draft not found", http.StatusNotFound)
		return
	}
	diff, err := evolution.BuildContentDiff(preview.HeadContent, preview.DraftContent)
	if err != nil {
		common.ReplyErr(w, "build user_preference diff failed", http.StatusInternalServerError)
		return
	}

	common.ReplyOK(w, draftPreviewResponse{
		ReviewStatus:       preview.DraftStatus,
		DraftStatus:        preview.DraftStatus,
		DraftSourceVersion: preview.DraftVersion,
		CurrentContent:     preview.HeadContent,
		DraftContent:       preview.DraftContent,
		Diff:               diff,
		FileDiff:           &preview.Diff,
		RevisionID:         preview.BaseRevisionID,
		DraftVersion:       preview.DraftVersion,
		ReviewID:           preview.ReviewID,
		ReviewVersion:      preview.ReviewVersion,
		CanUndo:            preview.CanUndo,
		PendingCount:       preview.PendingCount,
		AcceptedCount:      preview.AcceptedCount,
		RejectedCount:      preview.RejectedCount,
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

	row, fsService, _, err := ensurePreferenceResource(r.Context(), db, userID, userName)
	if err != nil {
		common.ReplyErr(w, "query user_preference failed", http.StatusInternalServerError)
		return
	}
	base, err := preferenceCurrentWritableContent(r.Context(), fsService, userID)
	if err != nil {
		common.ReplyErr(w, "query user_preference content failed", http.StatusInternalServerError)
		return
	}

	algoReq := algo.ManagedGenerateRequest{
		Content:      base.Content,
		UserInstruct: req.UserInstruct,
	}
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, "load llm config failed", http.StatusInternalServerError)
		return
	}
	algoReq.LLMConfig = llmConfig
	appLog.Logger.Info().
		Str("route", "/user-preference:generate").
		Str("user_preference_id", row.ID).
		Str("user_id", userID).
		Str("payload", payloadForLog(algoReq)).
		Msg("requesting external user preference generate")
	generated, err := algo.GenerateUserPreference(r.Context(), algoReq)
	if err != nil {
		common.ReplyErr(w, "user_preference generate failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	draft, _, err := preferenceDraftIsPending(r.Context(), fsService, userID)
	if err != nil {
		common.ReplyErr(w, "query user_preference draft failed", http.StatusInternalServerError)
		return
	}
	draftResp, err := fsService.WriteDraft(r.Context(), resourcefs.WriteDraftRequest{
		Ref:                  preferenceResourceRef(userID),
		Content:              generated,
		ExpectedDraftVersion: draft.DraftVersion,
		UpdatedBy:            userID,
	})
	if err != nil {
		common.ReplyErr(w, "update user_preference draft failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"draft_status":         "pending_confirm",
		"draft_source_version": row.Version,
		"draft_content":        generated,
		"draft_version":        draftResp.DraftVersion,
	})
}

func preferenceGenerateBaseContent(row orm.SystemUserPreference) (string, error) {
	if strings.TrimSpace(row.DraftStatus) == "pending_confirm" {
		return row.DraftContent, nil
	}
	return evolution.FormatSystemUserPreferenceForChat(row), nil
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

	userName := strings.TrimSpace(store.UserName(r))
	_, fsService, _, err := ensurePreferenceResource(r.Context(), db, userID, userName)
	if err != nil {
		common.ReplyErr(w, "query user_preference failed", http.StatusInternalServerError)
		return
	}
	preview, err := fsService.DraftPreview(r.Context(), resourcefs.DraftPreviewRequest{Ref: preferenceResourceRef(userID)})
	if err != nil || strings.TrimSpace(preview.DraftStatus) != "pending_confirm" {
		common.ReplyErr(w, "user_preference draft not found", http.StatusNotFound)
		return
	}
	parsed, err := ParseFileContent(preview.DraftContent)
	if err != nil {
		common.ReplyErr(w, "invalid user_preference draft", http.StatusBadRequest)
		return
	}
	if _, err := fsService.CommitDraft(r.Context(), resourcefs.CommitDraftRequest{
		Ref:                  preferenceResourceRef(userID),
		Message:              "confirm user_preference draft",
		SourceRefType:        "user_preference",
		ExpectedDraftVersion: preview.DraftVersion,
		CreatedBy:            userID,
	}); err != nil {
		common.ReplyErr(w, "confirm user_preference draft failed", http.StatusInternalServerError)
		return
	}
	content := parsed.Content
	agentPersona := parsed.AgentPersona
	preferredName := parsed.PreferredName
	responseStyle := parsed.ResponseStyle
	row, err := upsertManagedPreferenceContent(r, db, userID, userName, upsertRequest{
		Content:       &content,
		AgentPersona:  &agentPersona,
		PreferredName: &preferredName,
		ResponseStyle: &responseStyle,
	}, true)
	if err != nil {
		common.ReplyErr(w, "sync user_preference failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{
		"content":        row.Content,
		"agent_persona":  row.AgentPersona,
		"preferred_name": row.PreferredName,
		"response_style": row.ResponseStyle,
		"version":        row.Version,
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

	_, fsService, _, err := ensurePreferenceResource(r.Context(), db, userID, store.UserName(r))
	if err != nil {
		common.ReplyErr(w, "query user_preference failed", http.StatusInternalServerError)
		return
	}
	if _, err := fsService.DiscardDraft(r.Context(), preferenceResourceRef(userID)); err != nil {
		common.ReplyErr(w, "discard user_preference draft failed", http.StatusInternalServerError)
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

func hasPreferenceUpsertField(req upsertRequest) bool {
	return req.Content != nil ||
		req.AgentPersona != nil ||
		req.PreferredName != nil ||
		req.ResponseStyle != nil ||
		req.AutoEvo != nil
}

func stringFromPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
