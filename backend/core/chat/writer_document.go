package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
	"lazymind/core/workflow"

	"gorm.io/gorm"
)

type writerDocumentSyncBody struct {
	BaseRevision    int             `json:"base_revision"`
	SourceDocument  json.RawMessage `json:"source_document"`
	RevisedDocument json.RawMessage `json:"revised_document"`
	// Mode controls versioning: "draft" updates the selected human artifact in
	// place when possible; "checkpoint" (default) always creates a new revision.
	Mode string `json:"mode"`
}

type writerDocumentWriteBackBody struct {
	BaseRevision int    `json:"base_revision"`
	Slot         string `json:"slot"`
	// Legacy client fields remain accepted, but the selected server-side
	// revision and synchronized baseline are authoritative.
	SourceDocument  json.RawMessage `json:"source_document"`
	RevisedDocument json.RawMessage `json:"revised_document"`
}

type writerDocumentSaveBody struct {
	BaseRevision int             `json:"base_revision"`
	Document     json.RawMessage `json:"document"`
	Slot         string          `json:"slot"`
}

type writerDocumentRenderBody struct {
	Slot string `json:"slot"`
}

type selectedWriterArtifact struct {
	Revision orm.WorkflowSlotRevision
	Value    json.RawMessage
}

type writerWriteBackArtifact struct {
	Format   string
	Document json.RawMessage
	Markdown string
	Title    string
}

func writerDocumentSlot(slot string) (string, bool) {
	if slot == "" {
		return "draft_document", true
	}
	return slot, slot == "outline_document" || slot == "flat_draft_document" || slot == "draft_document"
}

func writerDocumentRenderSlot(slot string) (string, bool) {
	if slot == "source_document" {
		return slot, true
	}
	return writerDocumentSlot(slot)
}

// SyncWriterDocument writes an edited WriterDocument to Feishu, then commits
// the provider-confirmed document as a human artifact revision.
func SyncWriterDocument(w http.ResponseWriter, r *http.Request) {
	sessionID, slotID := common.PathVar(r, "session_id"), common.PathVar(r, "slot_id")
	listIndex, err := strconv.Atoi(common.PathVar(r, "list_index"))
	if err != nil || listIndex < -1 || sessionID == "" || slotID == "" {
		common.ReplyErr(w, "invalid request", http.StatusBadRequest)
		return
	}

	var body writerDocumentSyncBody
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.BaseRevision <= 0 ||
		len(body.SourceDocument) == 0 || len(body.RevisedDocument) == 0 {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = "checkpoint"
	}
	if mode != "draft" && mode != "checkpoint" {
		common.ReplyErr(w, "invalid mode: must be draft or checkpoint", http.StatusBadRequest)
		return
	}

	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	session, err := workflow.GetSession(ctx, db, sessionID)
	if err != nil || session == nil || session.WorkflowID != "writer-workflow" || session.Dismissed ||
		(session.CreateUserID != "" && session.CreateUserID != userID) {
		common.ReplyErr(w, "writer session not found", http.StatusNotFound)
		return
	}
	var index *int
	if listIndex >= 0 {
		index = &listIndex
	}
	var current orm.WorkflowSlotRevision
	query := db.WithContext(ctx).Where(
		"session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true,
	)
	if index == nil {
		query = query.Where("list_index IS NULL")
	} else {
		query = query.Where("list_index = ?", listIndex)
	}
	if query.First(&current).Error != nil {
		common.ReplyErr(w, "slot revision not found", http.StatusNotFound)
		return
	}
	if current.Revision != body.BaseRevision {
		common.ReplyErrWithData(w, "revision conflict", map[string]any{
			"current_revision": current.Revision,
		}, http.StatusConflict)
		return
	}

	toolConfig, err := loadChatToolConfig(ctx, db, userID)
	if err != nil {
		common.ReplyErr(w, "load Feishu authorization failed", http.StatusBadGateway)
		return
	}
	credential := toolConfig["feishu"]
	if credential == nil {
		common.ReplyErr(w, "Feishu authorization required", http.StatusUnauthorized)
		return
	}
	result, status, err := algo.SyncWriterDocument(ctx, algo.WriterDocumentSyncRequest{
		WorkflowID: session.WorkflowID, RevisionID: session.WorkflowRevisionID,
		TreeHash: session.WorkflowTreeHash, UserID: userID,
		SourceDocument: body.SourceDocument, RevisedDocument: body.RevisedDocument,
		ToolConfig: map[string]any{"feishu": credential},
	})
	if err != nil {
		common.ReplyErrWithData(w, "writer document sync failed", map[string]any{
			"status": "sync_failed", "feishu_synced": false, "artifact_saved": false,
			"detail": err.Error(),
		}, writerSyncStatus(status))
		return
	}
	if !result.Success || !result.FeishuSynced || len(result.PersistedDocument) == 0 {
		common.ReplyErr(w, "writer document sync failed", http.StatusBadGateway)
		return
	}
	// Draft with no Feishu delta: nothing to persist. Checkpoint still wants a
	// versioned snapshot even when the provider reports no_change.
	if !result.Changed && mode != "checkpoint" {
		writerSyncReply(w, "no_change", current.Revision, false, result)
		return
	}

	artifact, err := json.Marshal(map[string]any{
		"schema":         "lazyllm.tools.writer.data_models.writer_ir.WriterDocument",
		"schema_version": "0.1",
		"data":           result.PersistedDocument,
		"meta": map[string]any{
			"created_by": "writer-sync-api", "created_at": time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
	if err != nil {
		common.ReplyErr(w, "marshal WriterDocument artifact failed", http.StatusInternalServerError)
		return
	}

	var revision *orm.WorkflowSlotRevision
	if mode == "draft" {
		updated, ok, updateErr := workflow.UpdateSelectedHumanArtifactValue(
			ctx, db, sessionID, slotID, index, "json", artifact, nil,
		)
		if updateErr != nil {
			common.ReplyErrWithData(w, "artifact save failed", map[string]any{
				"status": "artifact_save_failed", "feishu_synced": true, "artifact_saved": false,
				"patch_result": result.PatchResult, "document": result.PersistedDocument,
			}, http.StatusInternalServerError)
			return
		}
		if ok {
			revision = updated
		}
	}
	if revision == nil {
		cardinality := "single"
		if current.ListIndex != nil {
			cardinality = "list"
		}
		created, createErr := workflow.WriteSlotRevisionWithHumanArtifact(
			ctx, db, sessionID, slotID, current.Slot, current.StepID, current.Attempt,
			cardinality, index, "json", artifact, nil,
		)
		if createErr != nil {
			common.ReplyErrWithData(w, "artifact save failed", map[string]any{
				"status": "artifact_save_failed", "feishu_synced": true, "artifact_saved": false,
				"patch_result": result.PatchResult, "document": result.PersistedDocument,
			}, http.StatusInternalServerError)
			return
		}
		revision = created
	}
	workflow.NotifyWorkflowArtifactUpdated(
		ctx, db, sessionID, revision.StepID, revision.SlotID, revision.Slot,
		revision.Revision, revision.ListIndex, "human",
	)
	writerSyncReply(w, "synced", revision.Revision, true, result)
}

// RenderWriterDocument renders a source, outline, or draft with automatic numbering.
// It returns the original representation and its materialized document.
func RenderWriterDocument(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	var body writerDocumentRenderBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	slot, ok := writerDocumentRenderSlot(body.Slot)
	if !ok {
		common.ReplyErr(w, "slot must be source_document, outline_document, flat_draft_document, or draft_document", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	session, err := workflow.GetSession(ctx, db, sessionID)
	if err != nil || session == nil || session.WorkflowID != "writer-workflow" || session.Dismissed ||
		(session.CreateUserID != "" && session.CreateUserID != userID) {
		common.ReplyErr(w, "writer session not found", http.StatusNotFound)
		return
	}
	draft, err := loadSelectedWriterArtifact(ctx, db, sessionID, slot)
	if err != nil {
		common.ReplyErr(w, "active "+slot+" not found", http.StatusNotFound)
		return
	}
	response, status, err := algo.InvokeWorkflowAction(ctx, algo.WorkflowActionInvokeRequest{
		WorkflowID: session.WorkflowID,
		RevisionID: session.WorkflowRevisionID,
		TreeHash:   session.WorkflowTreeHash,
		UserID:     userID,
		Action:     "render_document",
		Phase:      "execute",
		Slot:       slot,
		Artifact:   draft.Value,
		Arguments:  map[string]any{},
	})
	if err != nil {
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		common.ReplyErrWithData(w, "render writer document failed", map[string]any{
			"detail": err.Error(),
		}, status)
		return
	}
	var result map[string]any
	if json.Unmarshal(response.Result, &result) != nil {
		common.ReplyErr(w, "invalid render response", http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, result)
}

// SaveWriterDocument saves an IR or Markdown edit as a new revision.
// It returns the re-materialized document and its representation.
func SaveWriterDocument(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	var body writerDocumentSaveBody
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.BaseRevision <= 0 ||
		len(body.Document) == 0 {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	slot, ok := writerDocumentSlot(body.Slot)
	if !ok {
		common.ReplyErr(w, "slot must be outline_document, flat_draft_document, or draft_document", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	session, err := workflow.GetSession(ctx, db, sessionID)
	if err != nil || session == nil || session.WorkflowID != "writer-workflow" || session.Dismissed ||
		(session.CreateUserID != "" && session.CreateUserID != userID) {
		common.ReplyErr(w, "writer session not found", http.StatusNotFound)
		return
	}
	draft, err := loadSelectedWriterArtifact(ctx, db, sessionID, slot)
	if err != nil {
		common.ReplyErr(w, "active "+slot+" not found", http.StatusNotFound)
		return
	}
	if draft.Revision.Revision != body.BaseRevision {
		common.ReplyErrWithData(w, "revision conflict", map[string]any{
			"code":             "REVISION_CONFLICT",
			"current_revision": draft.Revision.Revision,
		}, http.StatusConflict)
		return
	}
	editedArtifact, err := json.Marshal(map[string]json.RawMessage{"data": body.Document})
	if err != nil {
		common.ReplyErr(w, "invalid document", http.StatusBadRequest)
		return
	}
	response, status, err := algo.InvokeWorkflowAction(ctx, algo.WorkflowActionInvokeRequest{
		WorkflowID: session.WorkflowID,
		RevisionID: session.WorkflowRevisionID,
		TreeHash:   session.WorkflowTreeHash,
		UserID:     userID,
		Action:     "save_document",
		Phase:      "execute",
		Slot:       slot,
		Artifact:   editedArtifact,
		Arguments:  map[string]any{"base_artifact": draft.Value},
	})
	if err != nil {
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		common.ReplyErrWithData(w, "writer document sync failed", map[string]any{
			"detail": err.Error(),
		}, status)
		return
	}
	var result map[string]any
	if json.Unmarshal(response.Result, &result) != nil {
		common.ReplyErr(w, "invalid workflow action response", http.StatusBadGateway)
		return
	}
	sourceValue, ok := result["source_document"]
	if !ok {
		common.ReplyErr(w, "invalid workflow action response", http.StatusBadGateway)
		return
	}
	schema := "lazyllm.tools.writer.data_models.writer_ir.WriterDocument"
	if _, isMarkdown := sourceValue.(string); isMarkdown {
		schema = "text/markdown"
	}
	artifact, err := json.Marshal(map[string]any{
		"schema":         schema,
		"schema_version": "0.1",
		"data":           sourceValue,
		"meta": map[string]any{
			"created_by": "writer-document-save-api",
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"title":      result["title"],
		},
	})
	if err != nil {
		common.ReplyErr(w, "marshal writerdocument artifact failed", http.StatusInternalServerError)
		return
	}
	revision, err := workflow.WriteSlotRevisionWithHumanArtifact(
		ctx, db, sessionID, draft.Revision.SlotID, draft.Revision.Slot,
		draft.Revision.StepID, draft.Revision.Attempt, "single", nil,
		"json", artifact, nil,
		&body.BaseRevision,
	)
	if err != nil {
		if errors.Is(err, workflow.ErrConflict) || errors.Is(err, gorm.ErrRecordNotFound) {
			currentRevision := 0
			if current, currentErr := loadSelectedWriterArtifact(ctx, db, sessionID, slot); currentErr == nil {
				currentRevision = current.Revision.Revision
			}
			common.ReplyErrWithData(w, "revision conflict", map[string]any{
				"code":             "REVISION_CONFLICT",
				"current_revision": currentRevision,
			}, http.StatusConflict)
			return
		}
		common.ReplyErrWithData(w, "artifact save failed", map[string]any{
			"detail": err.Error(),
		}, http.StatusInternalServerError)
		return
	}
	workflow.NotifyWorkflowArtifactUpdated(
		ctx, db, sessionID, revision.StepID, revision.SlotID, revision.Slot,
		revision.Revision, revision.ListIndex, "human",
	)
	reply := map[string]any{
		"revision": revision.Revision,
		"title":    result["title"],
	}
	if representation, ok := result["representation"]; ok {
		reply["representation"] = representation
	}
	if document, ok := result["document"]; ok {
		reply["document"] = document
	}
	common.ReplyOK(w, reply)
}

// WriteBackWriterDocument writes the active IR or Markdown draft to Feishu and
// saves the provider-confirmed IR as a new revision.
func WriteBackWriterDocument(w http.ResponseWriter, r *http.Request) {
	sessionID := common.PathVar(r, "session_id")
	if sessionID == "" {
		common.ReplyErr(w, "session_id required", http.StatusBadRequest)
		return
	}
	var body writerDocumentWriteBackBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Logger.Warn().Err(err).Str("session_id", sessionID).
			Int64("content_length", r.ContentLength).
			Msg("decode writer document write-back request failed")
		common.ReplyErr(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.BaseRevision <= 0 {
		log.Logger.Warn().Str("session_id", sessionID).Int("base_revision", body.BaseRevision).
			Msg("invalid writer document write-back base revision")
		common.ReplyErr(w, "base_revision must be greater than zero", http.StatusBadRequest)
		return
	}
	slot, ok := writerDocumentSlot(body.Slot)
	if !ok || slot == "outline_document" {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	ctx := r.Context()
	session, err := workflow.GetSession(ctx, db, sessionID)
	if err != nil || session == nil || session.WorkflowID != "writer-workflow" || session.Dismissed {
		common.ReplyErr(w, "writer session not found", http.StatusNotFound)
		return
	}
	userID := store.UserID(r)
	if session.CreateUserID != "" && userID != "" && session.CreateUserID != userID {
		common.ReplyErr(w, "writer session not found", http.StatusNotFound)
		return
	}
	draft, err := loadSelectedWriterArtifact(ctx, db, sessionID, slot)
	if err != nil {
		common.ReplyErr(w, "active "+slot+" not found", http.StatusNotFound)
		return
	}
	if draft.Revision.Revision != body.BaseRevision {
		common.ReplyErrWithData(w, "revision conflict", map[string]any{
			"current_revision": draft.Revision.Revision,
		}, http.StatusConflict)
		return
	}
	activeDraft, err := loadWriterWriteBackArtifact(draft.Value)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if writerArtifactRevisionSynced(draft) {
		common.ReplyErrWithData(w, "current "+slot+" revision is already synchronized", map[string]any{
			"status": "already_synced", "current_revision": draft.Revision.Revision,
		}, http.StatusConflict)
		return
	}

	toolConfig, err := loadChatToolConfig(ctx, db, userID)
	if err != nil {
		common.ReplyErr(w, "load Feishu authorization failed", http.StatusBadGateway)
		return
	}
	credential := toolConfig["feishu"]
	if credential == nil {
		common.ReplyErrWithData(w, "invalid request", map[string]any{
			"status":   "feishu_configuration_required",
			"provider": "feishu",
		}, http.StatusBadRequest)
		return
	}
	syncRequest := algo.WriterDocumentSyncRequest{
		WorkflowID: session.WorkflowID, RevisionID: session.WorkflowRevisionID,
		TreeHash: session.WorkflowTreeHash, UserID: userID,
		ToolConfig: map[string]any{"feishu": credential},
	}
	mediaSlot := "resolved_media_assets"
	if slot == "flat_draft_document" {
		mediaSlot = "flat_resolved_media_assets"
	}
	if activeDraft.Format == "markdown" {
		target, targetErr := loadSelectedWriterArtifact(ctx, db, sessionID, "target_document")
		if targetErr == nil {
			syncRequest.TargetDocument, err = writerArtifactData(target.Value, false)
			if err != nil {
				common.ReplyErr(w, "invalid target_document: "+err.Error(), http.StatusConflict)
				return
			}
		} else if targetErr != gorm.ErrRecordNotFound {
			common.ReplyErr(w, "load target_document failed", http.StatusInternalServerError)
			return
		}
		syncRequest.MarkdownContent = activeDraft.Markdown
		syncRequest.Title = activeDraft.Title
		mediaAssets, mediaErr := loadSelectedWriterArtifact(ctx, db, sessionID, mediaSlot)
		if mediaErr == nil {
			syncRequest.MediaAssets, mediaErr = writerArtifactData(mediaAssets.Value, false)
			if mediaErr != nil {
				common.ReplyErr(w, "invalid resolved_media_assets", http.StatusConflict)
				return
			}
		} else if !errors.Is(mediaErr, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "load resolved_media_assets failed", http.StatusInternalServerError)
			return
		}
	} else {
		revisedDocument, normalizeErr := normalizeWriterDocumentForSync(activeDraft.Document)
		if normalizeErr != nil {
			common.ReplyErr(w, "invalid current WriterDocument: "+normalizeErr.Error(), http.StatusBadRequest)
			return
		}
		mediaAssets, mediaErr := loadSelectedWriterArtifact(ctx, db, sessionID, mediaSlot)
		if mediaErr == nil {
			syncRequest.MediaAssets, mediaErr = writerArtifactData(mediaAssets.Value, false)
			if mediaErr != nil {
				common.ReplyErr(w, "invalid resolved_media_assets", http.StatusConflict)
				return
			}
		} else if !errors.Is(mediaErr, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "load resolved_media_assets failed", http.StatusInternalServerError)
			return
		}
		if writerDocumentIsUnbound(revisedDocument) {
			syncRequest.RevisedDocument = revisedDocument
		} else {
			baseline, baselineErr := loadWriterWriteBackBaseline(
				ctx, db, sessionID, slot, draft.Revision.Revision,
			)
			if baselineErr != nil {
				common.ReplyErrWithData(w, "initial Feishu write-back has not completed", map[string]any{
					"status": "baseline_not_found", "current_revision": draft.Revision.Revision,
				}, http.StatusConflict)
				return
			}
			baselineDocument, baselineErr := writerArtifactData(baseline.Value, true)
			if baselineErr != nil {
				common.ReplyErr(w, "invalid synchronized WriterDocument baseline", http.StatusConflict)
				return
			}
			baselineDocument, baselineErr = normalizeWriterDocumentForSync(baselineDocument)
			if baselineErr != nil {
				common.ReplyErr(w, "invalid synchronized WriterDocument baseline", http.StatusConflict)
				return
			}
			revisedDocument, normalizeErr = preserveExistingWriterImageBlocks(baselineDocument, revisedDocument)
			if normalizeErr != nil {
				common.ReplyErr(w, "invalid current WriterDocument: "+normalizeErr.Error(), http.StatusBadRequest)
				return
			}
			if pairErr := validateWriterWriteBackPair(baselineDocument, revisedDocument); pairErr != nil {
				common.ReplyErr(w, pairErr.Error(), http.StatusConflict)
				return
			}
			syncRequest.SourceDocument = baselineDocument
			syncRequest.RevisedDocument = revisedDocument
		}
	}
	result, status, err := algo.SyncWriterDocument(ctx, syncRequest)
	if err != nil {
		common.ReplyErrWithData(w, "writer document write-back failed", map[string]any{
			"status": "write_back_failed", "feishu_synced": false,
			"detail": err.Error(),
		}, writerSyncStatus(status))
		return
	}
	if !result.Success || !result.FeishuSynced || len(result.PersistedDocument) == 0 {
		common.ReplyErr(w, "writer document write-back failed", http.StatusBadGateway)
		return
	}

	artifact, err := json.Marshal(map[string]any{
		"schema":         "lazyllm.tools.writer.data_models.writer_ir.WriterDocument",
		"schema_version": "0.1",
		"data":           result.PersistedDocument,
		"meta": map[string]any{
			"created_by": "writer-write-back-api",
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
			"lazymind_provider_sync": map[string]any{
				"confirmed": true,
				"provider":  "feishu",
				"source":    "manual",
			},
		},
	})
	if err != nil {
		common.ReplyErr(w, "marshal WriterDocument artifact failed", http.StatusInternalServerError)
		return
	}
	revision, err := workflow.WriteSlotRevisionWithHumanArtifact(
		ctx, db, sessionID, draft.Revision.SlotID, draft.Revision.Slot,
		draft.Revision.StepID, draft.Revision.Attempt, "single", nil,
		"json", artifact, nil,
	)
	if err != nil {
		common.ReplyErrWithData(w, "artifact save failed", map[string]any{
			"status": "artifact_save_failed", "feishu_synced": true,
			"artifact_saved": false,
		}, http.StatusInternalServerError)
		return
	}
	if err := db.WithContext(ctx).Model(&orm.WorkflowSlotRevision{}).
		Where("id = ?", revision.ID).
		Update("change_source", "provider_sync").Error; err != nil {
		common.ReplyErrWithData(w, "artifact sync state save failed", map[string]any{
			"status": "artifact_state_save_failed", "feishu_synced": true,
			"artifact_saved": true,
		}, http.StatusInternalServerError)
		return
	}
	revision.ChangeSource = "provider_sync"
	workflow.NotifyWorkflowArtifactUpdated(
		ctx, db, sessionID, revision.StepID, revision.SlotID, revision.Slot,
		revision.Revision, revision.ListIndex, "provider_sync",
	)
	common.ReplyOK(w, map[string]any{
		"status": "synced", "revision": revision.Revision,
		"feishu_synced": true, "artifact_saved": true,
		"patch_result": result.PatchResult,
		"document":     result.PersistedDocument,
	})
}

func loadSelectedWriterArtifact(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	slotID string,
) (*selectedWriterArtifact, error) {
	var revision orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND selected = ? AND list_index IS NULL",
			sessionID, slotID, true).
		First(&revision).Error; err != nil {
		return nil, err
	}
	return loadWriterArtifactRevision(ctx, db, revision)
}

func loadWriterArtifactRevision(
	ctx context.Context,
	db *gorm.DB,
	revision orm.WorkflowSlotRevision,
) (*selectedWriterArtifact, error) {
	value, err := workflow.LoadSlotRevisionValue(ctx, db, revision)
	if err != nil {
		return nil, err
	}
	return &selectedWriterArtifact{Revision: revision, Value: value}, nil
}

func loadLatestSyncedWriterArtifact(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	slot string,
	beforeRevision int,
) (*selectedWriterArtifact, error) {
	var revisions []orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND revision < ?",
			sessionID, slot, beforeRevision).
		Order("revision DESC").
		Find(&revisions).Error; err != nil {
		return nil, err
	}
	for _, revision := range revisions {
		artifact, err := loadWriterArtifactRevision(ctx, db, revision)
		if err != nil {
			continue
		}
		if writerArtifactRevisionSynced(artifact) {
			return artifact, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

// loadWriterWriteBackBaseline prefers the latest provider-confirmed draft.  The
// first manual write-back has no such draft yet, so its source_document is the
// authoritative Feishu baseline instead.
func loadWriterWriteBackBaseline(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	slot string,
	beforeRevision int,
) (*selectedWriterArtifact, error) {
	baseline, err := loadLatestSyncedWriterArtifact(ctx, db, sessionID, slot, beforeRevision)
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		return baseline, err
	}
	return loadSelectedWriterArtifact(ctx, db, sessionID, "source_document")
}

type writerDocumentIdentity struct {
	DocumentID      string         `json:"document_id"`
	ProviderBinding map[string]any `json:"provider_binding"`
}

func writerDocumentIsUnbound(document json.RawMessage) bool {
	var identity writerDocumentIdentity
	return json.Unmarshal(document, &identity) == nil && len(identity.ProviderBinding) == 0
}

func validateWriterWriteBackPair(source, revised json.RawMessage) error {
	var sourceDoc, revisedDoc writerDocumentIdentity
	if json.Unmarshal(source, &sourceDoc) != nil || json.Unmarshal(revised, &revisedDoc) != nil {
		return fmt.Errorf("invalid WriterDocument state")
	}
	if sourceDoc.DocumentID == "" || sourceDoc.DocumentID != revisedDoc.DocumentID {
		return fmt.Errorf("WriterDocument identity does not match synchronized baseline")
	}
	provider, _ := sourceDoc.ProviderBinding["provider"].(string)
	externalID, _ := sourceDoc.ProviderBinding["document_id"].(string)
	if provider != "feishu" || externalID == "" {
		return fmt.Errorf("synchronized baseline is not bound to a Feishu document")
	}
	revisedProvider, _ := revisedDoc.ProviderBinding["provider"].(string)
	revisedExternalID, _ := revisedDoc.ProviderBinding["document_id"].(string)
	if revisedProvider != provider || revisedExternalID != externalID {
		return fmt.Errorf("current WriterDocument Feishu binding does not match baseline")
	}
	return nil
}

// normalizeWriterDocumentForSync converts editor-compatible rich-text span
// styles to the Writer IR wire contract. The editor accepts legacy string
// arrays (and stype), while the algorithm model requires style to be an
// object. Manual write-back reads revisions on the server, so it cannot rely
// on the equivalent frontend normalization performed for client payloads.
func normalizeWriterDocumentForSync(document json.RawMessage) (json.RawMessage, error) {
	var record map[string]any
	if err := json.Unmarshal(document, &record); err != nil {
		return nil, err
	}
	blocks, ok := record["blocks"].([]any)
	if !ok {
		return nil, fmt.Errorf("blocks must be an array")
	}
	for _, block := range blocks {
		normalizeWriterBlockForSync(block)
	}
	return json.Marshal(record)
}

// preserveExistingWriterImageBlocks keeps provider-owned image blocks byte-for-byte
// equivalent to their synchronized baseline. The Writer revision tool accepts new
// images, but deliberately rejects any update to an existing image block. Human
// editor saves can otherwise add placeholder newlines or spans to an untouched
// image caption while the user is editing surrounding text.
func preserveExistingWriterImageBlocks(source, revised json.RawMessage) (json.RawMessage, error) {
	var sourceDocument, revisedDocument map[string]any
	if err := json.Unmarshal(source, &sourceDocument); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(revised, &revisedDocument); err != nil {
		return nil, err
	}
	sourceBlocks, ok := sourceDocument["blocks"].([]any)
	if !ok {
		return nil, fmt.Errorf("source blocks must be an array")
	}
	revisedBlocks, ok := revisedDocument["blocks"].([]any)
	if !ok {
		return nil, fmt.Errorf("blocks must be an array")
	}
	baselineImages := make(map[string]map[string]any)
	collectWriterImageBlocks(sourceBlocks, baselineImages)
	preserveWriterImageBlocks(revisedBlocks, baselineImages)
	return json.Marshal(revisedDocument)
}

func collectWriterImageBlocks(blocks []any, images map[string]map[string]any) {
	for _, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "image" {
			if nodeID, _ := block["node_id"].(string); nodeID != "" {
				images[nodeID] = block
			}
		}
		if children, ok := block["children"].([]any); ok {
			collectWriterImageBlocks(children, images)
		}
	}
}

func preserveWriterImageBlocks(blocks []any, baselineImages map[string]map[string]any) {
	for index, value := range blocks {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if block["type"] == "image" {
			if nodeID, _ := block["node_id"].(string); nodeID != "" {
				if baseline, ok := baselineImages[nodeID]; ok {
					blocks[index] = baseline
					continue
				}
			}
		}
		if children, ok := block["children"].([]any); ok {
			preserveWriterImageBlocks(children, baselineImages)
		}
	}
}

func normalizeWriterBlockForSync(value any) {
	block, ok := value.(map[string]any)
	if !ok {
		return
	}
	if block["type"] == "image" {
		if content, ok := block["content"].(string); ok {
			block["content"] = strings.TrimLeft(content, "\r\n")
		}
	}
	if spans, ok := block["spans"].([]any); ok {
		for _, value := range spans {
			span, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if block["type"] == "image" {
				if text, ok := span["text"].(string); ok {
					span["text"] = strings.TrimLeft(text, "\r\n")
				}
			}
			style := span["style"]
			if style == nil {
				style = span["stype"]
			}
			if values, ok := style.([]any); ok {
				normalized := make(map[string]any, len(values))
				for _, value := range values {
					name, ok := value.(string)
					if !ok || name == "" {
						continue
					}
					switch name {
					case "strong":
						name = "bold"
					case "code":
						name = "inline_code"
					}
					normalized[name] = true
				}
				span["style"] = normalized
			} else if _, ok := style.(map[string]any); ok {
				span["style"] = style
			} else if style == nil {
				span["style"] = map[string]any{}
			}
			delete(span, "stype")
		}
	}
	if children, ok := block["children"].([]any); ok {
		for _, child := range children {
			normalizeWriterBlockForSync(child)
		}
	}
}

func writerArtifactRevisionSynced(artifact *selectedWriterArtifact) bool {
	if artifact == nil {
		return false
	}
	if artifact.Revision.ChangeSource == "provider_sync" {
		return true
	}
	return (artifact.Revision.ChangeSource == "ai" || artifact.Revision.ChangeSource == "host") &&
		writerArtifactEnvelopeProviderSynced(artifact.Value)
}

func writerArtifactEnvelopeProviderSynced(value json.RawMessage) bool {
	var record map[string]json.RawMessage
	if json.Unmarshal(value, &record) != nil {
		return false
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(record["meta"], &metadata) == nil {
		var marker struct {
			Confirmed bool `json:"confirmed"`
		}
		if json.Unmarshal(metadata["lazymind_provider_sync"], &marker) == nil && marker.Confirmed {
			return true
		}
	}
	var path string
	_ = json.Unmarshal(record["path"], &path)
	if path == "" || strings.ToLower(filepath.Ext(path)) != ".lmd" {
		return false
	}
	cleanPath := filepath.Clean(path)
	if !writerArtifactPathAllowed(cleanPath) {
		return false
	}
	content, err := os.ReadFile(cleanPath)
	return err == nil && writerArtifactEnvelopeProviderSynced(content)
}

func writerArtifactData(value json.RawMessage, requireLMD bool) (json.RawMessage, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(value, &record); err != nil {
		return nil, fmt.Errorf("invalid writer artifact")
	}
	if data := record["data"]; len(data) > 0 {
		return data, nil
	}
	if len(record["document_id"]) > 0 || len(record["uri"]) > 0 || len(record["doc_id"]) > 0 {
		return value, nil
	}
	var path string
	_ = json.Unmarshal(record["path"], &path)
	if path == "" {
		return nil, fmt.Errorf("writer artifact has no local path")
	}
	if requireLMD && strings.ToLower(filepath.Ext(path)) != ".lmd" {
		// TODO(writing-2.0): Convert Markdown to IR on its first Feishu write-back,
		// resolve/create the destination, then use the provider-confirmed IR as the
		// baseline for all later revisions.
		return nil, fmt.Errorf("active draft_document must be an .lmd artifact")
	}
	cleanPath := filepath.Clean(path)
	if !writerArtifactPathAllowed(cleanPath) {
		return nil, fmt.Errorf("writer artifact path is outside allowed storage")
	}
	content, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read writer artifact: %w", err)
	}
	return writerArtifactData(content, false)
}

func loadWriterWriteBackArtifact(value json.RawMessage) (*writerWriteBackArtifact, error) {
	var record struct {
		Schema   string          `json:"schema"`
		Data     json.RawMessage `json:"data"`
		Path     string          `json:"path"`
		Filename string          `json:"filename"`
		Meta     struct {
			Title string `json:"title"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(value, &record); err != nil {
		return nil, fmt.Errorf("invalid writer artifact")
	}
	path := record.Path
	if path == "" {
		if record.Schema == "text/markdown" {
			var markdown string
			if json.Unmarshal(record.Data, &markdown) != nil || strings.TrimSpace(markdown) == "" {
				return nil, fmt.Errorf("active draft_document Markdown is empty")
			}
			title := record.Meta.Title
			if title == "" {
				if heading, ok := strings.CutPrefix(strings.TrimSpace(strings.SplitN(markdown, "\n", 2)[0]), "# "); ok {
					title = strings.TrimSpace(heading)
				}
			}
			return &writerWriteBackArtifact{
				Format: "markdown", Markdown: markdown, Title: title,
			}, nil
		}
		document, err := writerArtifactData(value, false)
		if err != nil {
			return nil, err
		}
		return &writerWriteBackArtifact{Format: "lmd", Document: document}, nil
	}

	cleanPath := filepath.Clean(path)
	if !writerArtifactPathAllowed(cleanPath) {
		return nil, fmt.Errorf("writer artifact path is outside allowed storage")
	}
	content, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read writer artifact: %w", err)
	}
	extension := strings.ToLower(filepath.Ext(cleanPath))
	switch extension {
	case ".md", ".markdown":
		if strings.TrimSpace(string(content)) == "" {
			return nil, fmt.Errorf("active draft_document Markdown is empty")
		}
		filename := record.Filename
		if filename == "" {
			filename = filepath.Base(cleanPath)
		}
		return &writerWriteBackArtifact{
			Format: "markdown", Markdown: string(content),
			Title: strings.TrimSuffix(filename, filepath.Ext(filename)),
		}, nil
	case ".lmd":
		document, dataErr := writerArtifactData(content, false)
		if dataErr != nil {
			return nil, dataErr
		}
		return &writerWriteBackArtifact{Format: "lmd", Document: document}, nil
	default:
		return nil, fmt.Errorf("active draft_document must be an .lmd or .md artifact")
	}
}

func writerArtifactPathAllowed(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	roots := []string{os.Getenv("LAZYMIND_SUBAGENT_WORKSPACE"), "/var/lib/lazymind/uploads"}
	for _, root := range roots {
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(filepath.Clean(root), path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func writerSyncReply(
	w http.ResponseWriter,
	status string,
	revision int,
	artifactSaved bool,
	result *algo.WriterDocumentSyncResponse,
) {
	common.ReplyOK(w, map[string]any{
		"status": status, "revision": revision, "feishu_synced": true,
		"artifact_saved": artifactSaved, "patch_result": result.PatchResult,
		"document": result.PersistedDocument,
	})
}

func writerSyncStatus(status int) int {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return http.StatusBadRequest
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict:
		return status
	default:
		return http.StatusBadGateway
	}
}
