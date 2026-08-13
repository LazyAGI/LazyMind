package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
	"lazymind/core/subagent"

	"gorm.io/gorm"
)

type artifactActionPreviewBody struct {
	Action       string         `json:"action"`
	BaseRevision int            `json:"base_revision"`
	Input        map[string]any `json:"input"`
}

func PreviewArtifactAction(w http.ResponseWriter, r *http.Request) {
	target, body, ok := prepareArtifactActionPreview(w, r)
	if !ok {
		return
	}
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), target.db, store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "load model config failed", http.StatusInternalServerError)
		return
	}
	userID := store.UserID(r)
	response, status, err := algo.InvokeWorkflowAction(r.Context(), algo.WorkflowActionInvokeRequest{
		WorkflowID:    target.session.WorkflowID,
		RevisionID:    target.session.WorkflowRevisionID,
		TreeHash:      target.session.WorkflowTreeHash,
		UserID:        userID,
		Action:        body.Action,
		Phase:         "preview",
		Slot:          target.revision.SlotID,
		Artifact:      target.artifact,
		Arguments:     body.Input,
		ArtifactStore: target.artifactStore,
		LLMConfig:     llmConfig,
	})
	if err != nil {
		replyWorkflowActionError(w, status, err)
		return
	}
	var result map[string]any
	if json.Unmarshal(response.Result, &result) != nil {
		common.ReplyErr(w, "invalid workflow action response", http.StatusBadGateway)
		return
	}
	result["status"] = "ready"
	result["action"] = body.Action
	result["base_revision"] = body.BaseRevision
	common.ReplyOK(w, result)
}

type artifactActionTarget struct {
	db            *gorm.DB
	session       *orm.WorkflowSession
	revision      *orm.WorkflowSlotRevision
	artifact      json.RawMessage
	artifactStore string
}

func prepareArtifactActionPreview(
	w http.ResponseWriter, r *http.Request,
) (*artifactActionTarget, artifactActionPreviewBody, bool) {
	var body artifactActionPreviewBody
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Action == "" ||
		body.BaseRevision <= 0 || body.Input == nil {
		common.ReplyErr(w, "invalid artifact action preview request", http.StatusBadRequest)
		return nil, body, false
	}
	target, ok := prepareArtifactActionTarget(w, r, body.BaseRevision)
	return target, body, ok
}

func prepareArtifactActionTarget(
	w http.ResponseWriter, r *http.Request,
	baseRevision int,
) (*artifactActionTarget, bool) {
	sessionID, slotID := common.PathVar(r, "session_id"), common.PathVar(r, "slot_id")
	listIndex, err := strconv.Atoi(common.PathVar(r, "list_index"))
	if err != nil || listIndex < -1 || sessionID == "" || slotID == "" {
		common.ReplyErr(w, "invalid artifact action target", http.StatusBadRequest)
		return nil, false
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return nil, false
	}
	session, err := GetSession(r.Context(), db, sessionID)
	if err != nil || session == nil || session.Dismissed {
		common.ReplyErr(w, "workflow session not found", http.StatusNotFound)
		return nil, false
	}
	userID := store.UserID(r)
	if session.CreateUserID != "" && userID != "" && session.CreateUserID != userID {
		common.ReplyErr(w, "workflow session not found", http.StatusNotFound)
		return nil, false
	}
	var index *int
	if listIndex >= 0 {
		index = &listIndex
	}
	revision, artifact, taskID, err := loadSelectedArtifactValue(
		r.Context(), db, sessionID, slotID, index,
	)
	if err != nil {
		common.ReplyErr(w, "selected artifact not found", http.StatusNotFound)
		return nil, false
	}
	if revision.Revision != baseRevision {
		common.ReplyErrWithData(w, "revision conflict", map[string]any{
			"code":             "REVISION_CONFLICT",
			"current_revision": revision.Revision,
		}, http.StatusConflict)
		return nil, false
	}
	return &artifactActionTarget{
		db: db, session: session, revision: revision, artifact: artifact,
		artifactStore: subagent.WorkspacePath(userID, taskID),
	}, true
}

func loadSelectedArtifactValue(
	ctx context.Context, db *gorm.DB,
	sessionID, slotID string, listIndex *int,
) (*orm.WorkflowSlotRevision, json.RawMessage, string, error) {
	var revision orm.WorkflowSlotRevision
	q := db.WithContext(ctx).Where(
		"session_id = ? AND slot_id = ? AND selected = ?", sessionID, slotID, true,
	)
	if listIndex == nil {
		q = q.Where("list_index IS NULL")
	} else {
		q = q.Where("list_index = ?", *listIndex)
	}
	if err := q.First(&revision).Error; err != nil {
		return nil, nil, "", err
	}
	value, err := LoadSlotRevisionValue(ctx, db, revision)
	if err != nil {
		return nil, nil, "", err
	}
	taskID, err := loadSlotRevisionTaskID(ctx, db, revision)
	if err != nil {
		return nil, nil, "", err
	}
	return &revision, value, taskID, nil
}

func replyWorkflowActionError(w http.ResponseWriter, status int, err error) {
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
	}
	var httpErr *common.HTTPError
	if errors.As(err, &httpErr) && len(httpErr.Body) > 0 {
		var upstream struct {
			Detail map[string]any `json:"detail"`
		}
		if json.Unmarshal(httpErr.Body, &upstream) == nil && upstream.Detail != nil {
			message := "workflow artifact action failed"
			if status == http.StatusConflict {
				message = "revision conflict"
			} else if status == http.StatusUnprocessableEntity {
				message = "invalid artifact action preview request"
			}
			common.ReplyErrWithData(w, message, upstream.Detail, status)
			return
		}
	}
	common.ReplyErrWithData(w, "workflow artifact action failed", map[string]any{
		"detail": err.Error(),
	}, status)
}
