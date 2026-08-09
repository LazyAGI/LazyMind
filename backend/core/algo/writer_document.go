package algo

import (
	"context"
	"encoding/json"
	"fmt"
)

type WriterDocumentSyncRequest struct {
	WorkflowID     string          `json:"workflow_id"`
	RevisionID     string          `json:"revision_id"`
	TreeHash       string          `json:"tree_hash,omitempty"`
	UserID         string          `json:"user_id,omitempty"`
	SourceDocument  json.RawMessage `json:"source_document"`
	RevisedDocument json.RawMessage `json:"revised_document"`
	MediaAssets     json.RawMessage `json:"media_assets"`
	MarkdownContent string          `json:"markdown_content"`
	TargetDocument  json.RawMessage `json:"target_document"`
	Title           string          `json:"title"`
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
	arguments := map[string]any{}
	if len(req.SourceDocument) > 0 {
		arguments["source_document"] = req.SourceDocument
	}
	if len(req.RevisedDocument) > 0 {
		arguments["revised_document"] = req.RevisedDocument
	}
	if len(req.MediaAssets) > 0 {
		arguments["media_assets"] = req.MediaAssets
	}
	if req.MarkdownContent != "" {
		arguments["markdown_content"] = req.MarkdownContent
	}
	if len(req.TargetDocument) > 0 {
		arguments["target_document"] = req.TargetDocument
	}
	if req.Title != "" {
		arguments["title"] = req.Title
	}
	action, status, err := InvokeWorkflowAction(ctx, WorkflowActionInvokeRequest{
		WorkflowID: req.WorkflowID,
		RevisionID: req.RevisionID,
		TreeHash:   req.TreeHash,
		UserID:     req.UserID,
		Action:     "sync_document",
		Phase:      "execute",
		Slot:       "draft_document",
		Arguments:  arguments,
		ToolConfig: req.ToolConfig,
	})
	if err != nil {
		return nil, status, err
	}
	var response WriterDocumentSyncResponse
	if err := json.Unmarshal(action.Result, &response); err != nil {
		return nil, status, fmt.Errorf("decode sync_document action response: %w", err)
	}
	return &response, status, nil
}
