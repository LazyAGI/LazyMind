package chat

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

type writerDocumentSyncBody struct {
	BaseRevision    int             `json:"base_revision"`
	SourceDocument  json.RawMessage `json:"source_document"`
	RevisedDocument json.RawMessage `json:"revised_document"`
	// Mode controls versioning: "draft" updates the selected human artifact in
	// place when possible; "checkpoint" (default) always creates a new revision.
	Mode string `json:"mode"`
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
