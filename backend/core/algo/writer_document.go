package algo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"lazymind/core/common"
)

type WriterDocumentSyncRequest struct {
	SourceDocument  json.RawMessage `json:"source_document"`
	RevisedDocument json.RawMessage `json:"revised_document"`
	ToolConfig      map[string]any  `json:"tool_config"`
}

type WriterDocumentSyncResponse struct {
	Success           bool            `json:"success"`
	Changed           bool            `json:"changed"`
	FeishuSynced      bool            `json:"feishu_synced"`
	PatchResult       json.RawMessage `json:"patch_result"`
	PersistedDocument json.RawMessage `json:"persisted_document"`
}

func SyncWriterDocument(
	ctx context.Context,
	req WriterDocumentSyncRequest,
) (*WriterDocumentSyncResponse, int, error) {
	var response WriterDocumentSyncResponse
	err := common.ApiPost(
		ctx,
		common.JoinURL(common.ChatServiceEndpoint(), "/api/writer/documents:sync"),
		req, nil, &response, 2*time.Minute,
	)
	status := 0
	var httpErr *common.HTTPError
	if errors.As(err, &httpErr) {
		status = httpErr.StatusCode
	}
	return &response, status, err
}
