package algo

import (
	"context"
	"encoding/json"
	"time"

	"lazymind/core/common"
)

type DocumentActionInvokeRequest struct {
	Reference     string          `json:"reference"`
	Phase         string          `json:"phase"`
	Artifact      json.RawMessage `json:"artifact"`
	Arguments     any             `json:"arguments"`
	ArtifactStore string          `json:"artifact_store"`
	ToolConfig    map[string]any  `json:"tool_config,omitempty"`
	LLMConfig     map[string]any  `json:"llm_config,omitempty"`
}

type DocumentActionInvokeResponse struct {
	Result json.RawMessage `json:"result"`
}

const DocumentActionTimeout = 2 * time.Minute

func InvokeDocumentAction(ctx context.Context, request DocumentActionInvokeRequest) (*DocumentActionInvokeResponse, int, error) {
	var response DocumentActionInvokeResponse
	err := common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), "/api/document/actions:invoke"), request, nil, &response, DocumentActionTimeout)
	if err != nil {
		return nil, workflowActionHTTPStatus(err), err
	}
	return &response, 0, nil
}
