package algo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"lazymind/core/common"
)

type WorkflowActionInvokeRequest struct {
	WorkflowID    string          `json:"workflow_id"`
	RevisionID    string          `json:"revision_id"`
	TreeHash      string          `json:"tree_hash,omitempty"`
	UserID        string          `json:"user_id,omitempty"`
	Action        string          `json:"action"`
	Phase         string          `json:"phase"`
	Slot          string          `json:"slot"`
	Artifact      json.RawMessage `json:"artifact,omitempty"`
	Arguments     map[string]any  `json:"arguments"`
	ArtifactStore string          `json:"artifact_store,omitempty"`
	LLMConfig     map[string]any  `json:"llm_config,omitempty"`
	ToolConfig    map[string]any  `json:"tool_config,omitempty"`
}

type WorkflowActionInvokeResponse struct {
	Result json.RawMessage `json:"result"`
}

func InvokeWorkflowAction(
	ctx context.Context, req WorkflowActionInvokeRequest,
) (*WorkflowActionInvokeResponse, int, error) {
	var response WorkflowActionInvokeResponse
	err := common.ApiPost(
		ctx,
		common.JoinURL(common.ChatServiceEndpoint(), "/api/workflow/actions:invoke"),
		req, nil, &response, 2*time.Minute,
	)
	return &response, workflowActionHTTPStatus(err), err
}

func workflowActionHTTPStatus(err error) int {
	var httpErr *common.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	return 0
}
