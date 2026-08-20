package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/store"
	"lazymind/core/subagent"
	workflowstore "lazymind/core/workflow/store"

	"gopkg.in/yaml.v3"
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
	actionWorkflow, err := resolveArtifactActionWorkflow(r.Context(), target, body.Action)
	if err != nil {
		common.ReplyErr(w, "workflow artifact action failed", http.StatusInternalServerError)
		return
	}
	response, status, err := algo.InvokeWorkflowAction(r.Context(), algo.WorkflowActionInvokeRequest{
		WorkflowID:    actionWorkflow.workflowID,
		RevisionID:    actionWorkflow.revisionID,
		TreeHash:      actionWorkflow.treeHash,
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
	result["action_revision_id"] = actionWorkflow.revisionID
	common.ReplyOK(w, result)
}

type artifactActionResult struct {
	Artifact struct {
		ContentType string          `json:"content_type"`
		Value       json.RawMessage `json:"value"`
		Caption     *string         `json:"caption"`
	} `json:"artifact"`
}

// ExecuteArtifactAction commits a workflow-owned preview and records the
// resulting slot value as a human revision. Clients can only pass the opaque
// commit data emitted by preview.
func ExecuteArtifactAction(w http.ResponseWriter, r *http.Request) {
	target, body, ok := prepareArtifactActionPreview(w, r)
	if !ok {
		return
	}
	llmConfig, err := modelconfig.LoadLLMConfig(r.Context(), target.db, store.UserID(r))
	if err != nil {
		common.ReplyErr(w, "load model config failed", http.StatusInternalServerError)
		return
	}
	actionWorkflow, err := resolveArtifactActionWorkflow(r.Context(), target, body.Action)
	if err != nil {
		common.ReplyErr(w, "workflow artifact action failed", http.StatusInternalServerError)
		return
	}
	response, status, err := algo.InvokeWorkflowAction(r.Context(), algo.WorkflowActionInvokeRequest{
		WorkflowID:    actionWorkflow.workflowID,
		RevisionID:    actionWorkflow.revisionID,
		TreeHash:      actionWorkflow.treeHash,
		UserID:        store.UserID(r),
		Action:        body.Action,
		Phase:         "execute",
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
	var actionResult artifactActionResult
	var result map[string]any
	if json.Unmarshal(response.Result, &actionResult) != nil ||
		json.Unmarshal(response.Result, &result) != nil ||
		actionResult.Artifact.ContentType == "" || len(actionResult.Artifact.Value) == 0 {
		common.ReplyErr(w, "invalid workflow action response", http.StatusBadGateway)
		return
	}

	cardinality := "single"
	if target.revision.ListIndex != nil {
		cardinality = "list"
	}
	expected := body.BaseRevision
	newRevision, err := WriteSlotRevisionWithHumanArtifact(
		r.Context(), target.db,
		target.revision.SessionID, target.revision.SlotID, target.revision.Slot,
		target.revision.StepID, target.revision.Attempt, cardinality,
		target.revision.ListIndex, actionResult.Artifact.ContentType,
		resolveValuePaths(actionResult.Artifact.Value), actionResult.Artifact.Caption,
		&expected,
	)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			common.ReplyErrWithData(w, "revision conflict", map[string]any{
				"code": "REVISION_CONFLICT",
			}, http.StatusConflict)
			return
		}
		common.ReplyErr(w, "workflow artifact action failed", http.StatusInternalServerError)
		return
	}
	NotifyWorkflowArtifactUpdated(
		r.Context(), target.db, newRevision.SessionID, newRevision.StepID,
		newRevision.SlotID, newRevision.Slot, newRevision.Revision,
		newRevision.ListIndex, "human",
	)
	result["status"] = "applied"
	result["action"] = body.Action
	result["base_revision"] = body.BaseRevision
	result["revision"] = newRevision.Revision
	result["action_revision_id"] = actionWorkflow.revisionID
	common.ReplyOK(w, result)
}

type artifactActionTarget struct {
	db            *gorm.DB
	session       *orm.WorkflowSession
	revision      *orm.WorkflowSlotRevision
	artifact      json.RawMessage
	artifactStore string
}

type artifactActionWorkflowVersion struct {
	workflowID string
	revisionID string
	treeHash   string
}

// resolveArtifactActionWorkflow keeps workflow execution/history pinned unless
// the active package explicitly declares revision_policy: head for this human
// artifact action. The policy lives in the package rather than a workflow-id
// branch, allowing a current editor to handle artifacts from older revisions.
func resolveArtifactActionWorkflow(
	ctx context.Context, target *artifactActionTarget, action string,
) (artifactActionWorkflowVersion, error) {
	pinned := artifactActionWorkflowVersion{
		workflowID: target.session.WorkflowID,
		revisionID: target.session.WorkflowRevisionID,
		treeHash:   target.session.WorkflowTreeHash,
	}
	if target.db == nil {
		return pinned, nil
	}
	var resource orm.WorkflowResource
	query := target.db.WithContext(ctx).Where(
		"status = 'active' AND (owner_user_id = ? OR owner_user_id = '')",
		target.session.CreateUserID,
	)
	if target.session.WorkflowRef != "" {
		query = query.Where("plugin_ref = ?", target.session.WorkflowRef) // workflow-naming: persistence
	} else {
		query = query.Where("plugin_id = ?", target.session.WorkflowID) // workflow-naming: persistence
	}
	err := query.Take(&resource).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return pinned, nil
		}
		return artifactActionWorkflowVersion{}, fmt.Errorf("load artifact action head revision: %w", err)
	}
	pkg, err := workflowstore.New(target.db).GetWorkflowPackage(
		ctx, target.session.CreateUserID, resource.WorkflowRef, resource.HeadRevisionID,
	)
	if err != nil {
		if errors.Is(err, workflowstore.ErrNotFound) {
			return pinned, nil
		}
		return artifactActionWorkflowVersion{}, fmt.Errorf("load artifact action head revision: %w", err)
	}
	var manifest struct {
		ArtifactActions map[string]struct {
			RevisionPolicy string `yaml:"revision_policy"`
		} `yaml:"artifact_actions"`
	}
	if err := yaml.Unmarshal(pkg.Files["workflow.yaml"], &manifest); err != nil {
		return artifactActionWorkflowVersion{}, fmt.Errorf("parse artifact action policy: %w", err)
	}
	if manifest.ArtifactActions[action].RevisionPolicy != "head" {
		return pinned, nil
	}
	var revision orm.WorkflowRevision
	if err := target.db.WithContext(ctx).Where("id = ?", resource.HeadRevisionID).Take(&revision).Error; err != nil {
		return artifactActionWorkflowVersion{}, fmt.Errorf("load artifact action head revision: %w", err)
	}
	if resource.HeadRevisionID == "" || revision.TreeHash == "" {
		return artifactActionWorkflowVersion{}, fmt.Errorf("artifact action head revision is incomplete")
	}
	return artifactActionWorkflowVersion{
		workflowID: resource.WorkflowID,
		revisionID: resource.HeadRevisionID,
		treeHash:   revision.TreeHash,
	}, nil
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
