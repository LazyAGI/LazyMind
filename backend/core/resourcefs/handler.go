package resourcefs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/common"
	appLog "lazymind/core/log"
	"lazymind/core/modelconfig"
	"lazymind/core/preferencefile"
	"lazymind/core/store"
)

const maxManagedContentChars = 1500

type AutoEvoEnabledScannerFunc func(context.Context, *gorm.DB, string, string, string) error

var AutoEvoEnabledScanner AutoEvoEnabledScannerFunc

func GetFile(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	refType := FileRefType(strings.TrimSpace(r.URL.Query().Get("ref")))
	if refType == "" {
		refType = FileRefHead
	}
	resp, err := service.ReadFile(r.Context(), ReadFileRequest{
		Ref:        ref,
		RefType:    refType,
		RevisionID: strings.TrimSpace(r.URL.Query().Get("revision_id")),
	})
	if err != nil {
		replyError(w, err)
		return
	}
	if ref.ResourceType == ResourceTypeUserPreference {
		parsed, err := preferencefile.ParseFileContent(resp.Content)
		if err != nil {
			replyError(w, err)
			return
		}
		resp.Content = parsed.Content
		resp.AgentPersona = parsed.AgentPersona
		resp.PreferredName = parsed.PreferredName
		resp.ResponseStyle = parsed.ResponseStyle
	}
	common.ReplyOK(w, resp)
}

func WriteDraft(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	var req struct {
		Content              *string `json:"content"`
		ExpectedDraftVersion int64   `json:"expected_draft_version"`
		ConversationID       string  `json:"conversation_id"`
		TaskID               string  `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.Content == nil {
		common.ReplyErr(w, "content required", http.StatusBadRequest)
		return
	}
	content := stringFromPtr(req.Content)
	if req.Content != nil {
		if err := validateManagedContentLength(*req.Content); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if ref.ResourceType == ResourceTypeUserPreference {
		patched, err := patchUserPreferenceDraftContent(r.Context(), service, ref, preferencefile.PreferencePatch{
			Content: req.Content,
		})
		if err != nil {
			replyError(w, err)
			return
		}
		content = patched
	}
	resp, err := service.WriteDraft(r.Context(), WriteDraftRequest{
		Ref:                  ref,
		Content:              content,
		ExpectedDraftVersion: req.ExpectedDraftVersion,
		ConversationID:       req.ConversationID,
		TaskID:               req.TaskID,
		UpdatedBy:            ref.UserID,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func PatchMetadata(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	var req struct {
		AutoEvo       *bool   `json:"auto_evo"`
		AgentPersona  *string `json:"agent_persona"`
		PreferredName *string `json:"preferred_name"`
		ResponseStyle *string `json:"response_style"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.AutoEvo == nil && req.AgentPersona == nil && req.PreferredName == nil && req.ResponseStyle == nil {
		common.ReplyErr(w, "metadata field required", http.StatusBadRequest)
		return
	}
	if ref.ResourceType != ResourceTypeUserPreference &&
		(req.AgentPersona != nil || req.PreferredName != nil || req.ResponseStyle != nil) {
		common.ReplyErr(w, "user_preference metadata is only supported for user_preference", http.StatusBadRequest)
		return
	}
	for _, value := range []*string{req.AgentPersona, req.PreferredName, req.ResponseStyle} {
		if value != nil {
			if err := validateManagedContentLength(*value); err != nil {
				common.ReplyErr(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
	}
	if _, err := service.EnsureResource(r.Context(), ref, initialContent(ref.ResourceType)); err != nil {
		replyError(w, err)
		return
	}
	resp, err := service.UpdateMetadata(r.Context(), UpdateMetadataRequest{
		Ref:           ref,
		AutoEvo:       req.AutoEvo,
		AgentPersona:  req.AgentPersona,
		PreferredName: req.PreferredName,
		ResponseStyle: req.ResponseStyle,
		UpdatedBy:     ref.UserID,
		UpdatedByName: strings.TrimSpace(store.UserName(r)),
	})
	if err != nil {
		replyError(w, err)
		return
	}
	if resp.EnabledFromOff && resp.AutoEvo && AutoEvoEnabledScanner != nil {
		if scanErr := AutoEvoEnabledScanner(r.Context(), store.DB(), string(ref.ResourceType), ref.UserID, resp.ResourceID); scanErr != nil {
			appLog.Logger.Warn().Err(scanErr).
				Str("component", "resource_update").
				Str("event", "resource_update.auto_evo_enabled.scan_failed").
				Str("resource_type", string(ref.ResourceType)).
				Str("resource_id", resp.ResourceID).
				Str("route", "/personal-resource/{resource_type}").
				Str("user_id", ref.UserID).
				Msg("resource update scan on personal resource metadata patch failed")
		}
	}
	common.ReplyOK(w, resp)
}

func Generate(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	var req struct {
		UserInstruct string `json:"user_instruct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.UserInstruct = strings.TrimSpace(req.UserInstruct)
	if req.UserInstruct == "" {
		common.ReplyErr(w, "user_instruct required", http.StatusBadRequest)
		return
	}
	state, err := service.EnsureResource(r.Context(), ref, initialContent(ref.ResourceType))
	if err != nil {
		replyError(w, err)
		return
	}
	base, err := currentWritableContent(r.Context(), service, ref)
	if err != nil {
		replyError(w, err)
		return
	}
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), store.DB(), ref.UserID)
	if err != nil {
		common.ReplyErr(w, "load llm config failed", http.StatusInternalServerError)
		return
	}
	algoReq := algo.ManagedGenerateRequest{
		Content:      base.Content,
		UserInstruct: req.UserInstruct,
		LLMConfig:    llmConfig,
	}
	var generated string
	switch ref.ResourceType {
	case ResourceTypeMemory:
		generated, err = algo.GenerateMemory(r.Context(), algoReq)
	case ResourceTypeUserPreference:
		generated, err = algo.GenerateUserPreference(r.Context(), algoReq)
	default:
		err = ErrInvalidResourceType
	}
	if err != nil {
		common.ReplyErr(w, "personal resource generate failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	draftResp, err := service.WriteDraft(r.Context(), WriteDraftRequest{
		Ref:                  ref,
		Content:              generated,
		ExpectedDraftVersion: base.DraftVersion,
		UpdatedBy:            ref.UserID,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, map[string]any{
		"draft_status":         "pending_confirm",
		"draft_source_version": state.Version,
		"draft_content":        generated,
		"draft_version":        draftResp.DraftVersion,
	})
}

func patchUserPreferenceDraftContent(ctx context.Context, service *Service, ref ResourceRef, patch preferencefile.PreferencePatch) (string, error) {
	base, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		if !errors.Is(err, ErrDraftNotFound) {
			return "", err
		}
		base, err = service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefHead})
		if err != nil {
			return "", err
		}
	}
	next, _, err := preferencefile.PatchFileContent(base.Content, patch)
	return next, err
}

func initialContent(resourceType ResourceType) string {
	if resourceType == ResourceTypeUserPreference {
		return preferencefile.EmptyPreferenceFileContent()
	}
	return ""
}

func currentWritableContent(ctx context.Context, service *Service, ref ResourceRef) (FileResponse, error) {
	draft, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err == nil && strings.TrimSpace(draft.DraftStatus) == "pending_confirm" {
		return draft, nil
	}
	if err != nil && !errors.Is(err, ErrDraftNotFound) {
		return FileResponse{}, err
	}
	return service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefHead})
}

func validateManagedContentLength(content string) error {
	if utf8.RuneCountInString(strings.Join(strings.Fields(content), "")) > maxManagedContentChars {
		return errors.New("content exceeds 1500 characters after removing whitespace")
	}
	return nil
}

func stringFromPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func DraftPreview(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	resp, err := service.DraftPreview(r.Context(), DraftPreviewRequest{Ref: ref})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func ReviewAction(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	reviewID := strings.TrimSpace(mux.Vars(r)["review_id"])
	var req struct {
		ExpectedReviewVersion int64 `json:"expected_review_version"`
		Items                 []struct {
			Path     string `json:"path"`
			HunkID   string `json:"hunk_id"`
			Decision string `json:"decision"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	items := make([]ReviewActionItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, ReviewActionItem{
			Path:     strings.TrimSpace(item.Path),
			HunkID:   strings.TrimSpace(item.HunkID),
			Decision: strings.TrimSpace(item.Decision),
		})
	}
	resp, err := service.Action(r.Context(), ReviewActionRequest{
		Ref:                   ref,
		ReviewID:              reviewID,
		ExpectedReviewVersion: req.ExpectedReviewVersion,
		Items:                 items,
		UpdatedBy:             ref.UserID,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func ReviewUndo(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	reviewID := strings.TrimSpace(mux.Vars(r)["review_id"])
	var req struct {
		ExpectedReviewVersion int64 `json:"expected_review_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	resp, err := service.Undo(r.Context(), ReviewUndoRequest{
		Ref:                   ref,
		ReviewID:              reviewID,
		ExpectedReviewVersion: req.ExpectedReviewVersion,
		UpdatedBy:             ref.UserID,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func CommitDraft(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	var req struct {
		Message                string `json:"message"`
		SourceRefType          string `json:"source_ref_type"`
		SourceRefID            string `json:"source_ref_id"`
		ExpectedHeadRevisionID string `json:"expected_head_revision_id"`
		ExpectedDraftVersion   int64  `json:"expected_draft_version"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if err := validateDraftBeforeCommit(r.Context(), service, ref); err != nil {
		replyError(w, err)
		return
	}
	resp, err := service.CommitDraft(r.Context(), CommitDraftRequest{
		Ref:                    ref,
		Message:                req.Message,
		SourceRefType:          req.SourceRefType,
		SourceRefID:            req.SourceRefID,
		ExpectedHeadRevisionID: req.ExpectedHeadRevisionID,
		ExpectedDraftVersion:   req.ExpectedDraftVersion,
		CreatedBy:              ref.UserID,
		CreatedByName:          strings.TrimSpace(store.UserName(r)),
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func DiscardDraft(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	resp, err := service.DiscardDraft(r.Context(), ref)
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func ListRevisions(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	resp, err := service.ListRevisions(r.Context(), ListRevisionsRequest{Ref: ref})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func GetRevision(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	resp, err := service.GetRevision(r.Context(), ref, mux.Vars(r)["revision_id"])
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func Rollback(w http.ResponseWriter, r *http.Request) {
	service, ref, ok := requestServiceAndRef(w, r)
	if !ok {
		return
	}
	var req struct {
		RevisionID             string `json:"revision_id"`
		Message                string `json:"message"`
		ExpectedHeadRevisionID string `json:"expected_head_revision_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := validateRevisionBeforeRollback(r.Context(), service, ref, req.RevisionID); err != nil {
		replyError(w, err)
		return
	}
	resp, err := service.Rollback(r.Context(), RollbackRequest{
		Ref:                    ref,
		RevisionID:             req.RevisionID,
		Message:                req.Message,
		ExpectedHeadRevisionID: req.ExpectedHeadRevisionID,
		CreatedBy:              ref.UserID,
		CreatedByName:          strings.TrimSpace(store.UserName(r)),
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, resp)
}

func validateDraftBeforeCommit(ctx context.Context, service *Service, ref ResourceRef) error {
	if ref.ResourceType != ResourceTypeUserPreference {
		return nil
	}
	draft, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		return err
	}
	_, err = preferencefile.ParseFileContent(draft.Content)
	return err
}

func validateRevisionBeforeRollback(ctx context.Context, service *Service, ref ResourceRef, revisionID string) error {
	if ref.ResourceType != ResourceTypeUserPreference {
		return nil
	}
	revision, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefRevision, RevisionID: revisionID})
	if err != nil {
		return err
	}
	_, err = preferencefile.ParseFileContent(revision.Content)
	return err
}

func requestServiceAndRef(w http.ResponseWriter, r *http.Request) (*Service, ResourceRef, bool) {
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return nil, ResourceRef{}, false
	}
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return nil, ResourceRef{}, false
	}
	resourceType := ResourceType(strings.TrimSpace(mux.Vars(r)["resource_type"]))
	if _, err := FixedPath(resourceType); err != nil {
		common.ReplyErr(w, "invalid resource_type", http.StatusBadRequest)
		return nil, ResourceRef{}, false
	}
	return NewService(ServiceDeps{DB: db}), ResourceRef{UserID: userID, ResourceType: resourceType}, true
}

func replyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidResourceType), errors.Is(err, ErrInvalidPath):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrInvalidReview):
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, ErrResourceNotFound), errors.Is(err, ErrRevisionNotFound), errors.Is(err, ErrDraftNotFound), errors.Is(err, ErrReviewNotFound):
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, ErrConflict):
		common.ReplyErr(w, err.Error(), http.StatusConflict)
	case errors.Is(err, ErrUnsupported):
		common.ReplyErr(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
	}
}
