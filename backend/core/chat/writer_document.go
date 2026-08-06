package chat

import (
	"context"
	"encoding/json"
	"fmt"
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
	BaseRevision int `json:"base_revision"`
}

type selectedWriterArtifact struct {
	Revision orm.WorkflowSlotRevision
	Value    json.RawMessage
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

	toolConfig, err := loadChatToolConfig(ctx, db, store.UserID(r))
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

// WriteBackWriterDocument applies the active draft's IR delta to Feishu and
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
	draft, err := loadSelectedWriterArtifact(ctx, db, sessionID, "draft_document")
	if err != nil {
		common.ReplyErr(w, "active draft_document not found", http.StatusNotFound)
		return
	}
	if draft.Revision.Revision != body.BaseRevision {
		common.ReplyErrWithData(w, "revision conflict", map[string]any{
			"current_revision": draft.Revision.Revision,
		}, http.StatusConflict)
		return
	}
	revisedDocument, err := writerArtifactData(draft.Value, true)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	revisedDocument, err = normalizeWriterDocumentForSync(revisedDocument)
	if err != nil {
		common.ReplyErr(w, "invalid current WriterDocument: "+err.Error(), http.StatusBadRequest)
		return
	}
	if writerArtifactRevisionSynced(draft) {
		common.ReplyErrWithData(w, "current WriterDocument revision is already synchronized", map[string]any{
			"status": "already_synced", "current_revision": draft.Revision.Revision,
		}, http.StatusConflict)
		return
	}
	baseline, err := loadLatestSyncedWriterArtifact(
		ctx, db, sessionID, draft.Revision.Revision,
	)
	if err != nil {
		common.ReplyErrWithData(w, "initial Feishu write-back has not completed", map[string]any{
			"status": "baseline_not_found", "current_revision": draft.Revision.Revision,
		}, http.StatusConflict)
		return
	}
	baselineDocument, err := writerArtifactData(baseline.Value, true)
	if err != nil {
		common.ReplyErr(w, "invalid synchronized WriterDocument baseline", http.StatusConflict)
		return
	}
	baselineDocument, err = normalizeWriterDocumentForSync(baselineDocument)
	if err != nil {
		common.ReplyErr(w, "invalid synchronized WriterDocument baseline", http.StatusConflict)
		return
	}
	if err := validateWriterWriteBackPair(baselineDocument, revisedDocument); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
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
		SourceDocument: baselineDocument, RevisedDocument: revisedDocument,
		ToolConfig: map[string]any{"feishu": credential},
	})
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
	if revision.HumanArtifactID != nil {
		var artifact orm.WorkflowHumanArtifact
		if err := db.WithContext(ctx).Where("id = ?", *revision.HumanArtifactID).
			First(&artifact).Error; err != nil {
			return nil, err
		}
		return &selectedWriterArtifact{Revision: revision, Value: artifact.Value}, nil
	}
	if revision.ArtifactSeq != nil {
		var step orm.WorkflowSessionStep
		if err := db.WithContext(ctx).
			Where("session_id = ? AND step_id = ? AND attempt = ?",
				revision.SessionID, revision.StepID, revision.Attempt).
			First(&step).Error; err != nil {
			return nil, err
		}
		var artifact orm.SubAgentArtifact
		if err := db.WithContext(ctx).
			Where("task_id = ? AND slot = ? AND seq = ? AND hidden = ?",
				step.TaskID, revision.Slot, *revision.ArtifactSeq, false).
			First(&artifact).Error; err != nil {
			return nil, err
		}
		return &selectedWriterArtifact{Revision: revision, Value: artifact.Value}, nil
	}
	if len(revision.ContentSnapshot) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &selectedWriterArtifact{Revision: revision, Value: revision.ContentSnapshot}, nil
}

func loadLatestSyncedWriterArtifact(
	ctx context.Context,
	db *gorm.DB,
	sessionID string,
	beforeRevision int,
) (*selectedWriterArtifact, error) {
	var revisions []orm.WorkflowSlotRevision
	if err := db.WithContext(ctx).
		Where("session_id = ? AND slot_id = ? AND list_index IS NULL AND revision < ?",
			sessionID, "draft_document", beforeRevision).
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

type writerDocumentIdentity struct {
	DocumentID      string         `json:"document_id"`
	ProviderBinding map[string]any `json:"provider_binding"`
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

func normalizeWriterBlockForSync(value any) {
	block, ok := value.(map[string]any)
	if !ok {
		return
	}
	if spans, ok := block["spans"].([]any); ok {
		for _, value := range spans {
			span, ok := value.(map[string]any)
			if !ok {
				continue
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
	return artifact.Revision.ChangeSource == "ai" &&
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
