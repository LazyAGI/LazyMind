package resourcefs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/evolution"
	"lazymind/core/preferencefile"
	"lazymind/core/store"
)

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
		Content              string  `json:"content"`
		AgentPersona         *string `json:"agent_persona"`
		PreferredName        *string `json:"preferred_name"`
		ResponseStyle        *string `json:"response_style"`
		ExpectedDraftVersion int64   `json:"expected_draft_version"`
		ConversationID       string  `json:"conversation_id"`
		TaskID               string  `json:"task_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	content := req.Content
	if ref.ResourceType == ResourceTypeUserPreference {
		patched, err := patchUserPreferenceDraftContent(r.Context(), service, ref, preferencefile.PreferencePatch{
			Content:       &req.Content,
			AgentPersona:  req.AgentPersona,
			PreferredName: req.PreferredName,
			ResponseStyle: req.ResponseStyle,
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
	})
	if err != nil {
		replyError(w, err)
		return
	}
	if err := syncBusinessColumns(r.Context(), store.DB(), ref, resp.Content, resp.RevisionNo); err != nil {
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
	})
	if err != nil {
		replyError(w, err)
		return
	}
	if err := syncBusinessColumns(r.Context(), store.DB(), ref, resp.Content, resp.RevisionNo); err != nil {
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

func syncBusinessColumns(ctx context.Context, db *gorm.DB, ref ResourceRef, content string, revisionNo int64) error {
	now := time.Now()
	switch ref.ResourceType {
	case ResourceTypeMemory:
		row, err := evolution.EnsureSystemMemory(ctx, db, ref.UserID, "")
		if err != nil {
			return err
		}
		row.Content = content
		row.Version = revisionNo
		return db.WithContext(ctx).Model(&orm.SystemMemory{}).Where("id = ?", row.ID).Updates(map[string]any{
			"content":              content,
			"content_hash":         evolution.HashSystemMemory(*row),
			"version":              revisionNo,
			"draft_content":        "",
			"draft_source_version": 0,
			"draft_status":         "",
			"draft_updated_at":     nil,
			"updated_by":           ref.UserID,
			"updated_at":           now,
		}).Error
	case ResourceTypeUserPreference:
		parsed, err := preferencefile.ParseFileContent(content)
		if err != nil {
			return err
		}
		row, err := evolution.EnsureSystemUserPreference(ctx, db, ref.UserID, "")
		if err != nil {
			return err
		}
		row.Content = parsed.Content
		row.AgentPersona = parsed.AgentPersona
		row.PreferredName = parsed.PreferredName
		row.ResponseStyle = parsed.ResponseStyle
		row.Version = revisionNo
		return db.WithContext(ctx).Model(&orm.SystemUserPreference{}).Where("id = ?", row.ID).Updates(map[string]any{
			"content":              parsed.Content,
			"agent_persona":        parsed.AgentPersona,
			"preferred_name":       parsed.PreferredName,
			"response_style":       parsed.ResponseStyle,
			"content_hash":         evolution.HashSystemUserPreference(*row),
			"version":              revisionNo,
			"draft_content":        "",
			"draft_source_version": 0,
			"draft_status":         "",
			"draft_updated_at":     nil,
			"updated_by":           ref.UserID,
			"updated_at":           now,
		}).Error
	default:
		return ErrInvalidResourceType
	}
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
