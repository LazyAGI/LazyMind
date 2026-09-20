package algo

import (
	"context"
	"encoding/json"
	"fmt"
)

type WriterDocumentSyncRequest struct {
	WorkflowID      string          `json:"workflow_id"`
	RevisionID      string          `json:"revision_id"`
	TreeHash        string          `json:"tree_hash,omitempty"`
	UserID          string          `json:"user_id,omitempty"`
	SourceDocument  json.RawMessage `json:"source_document"`
	RevisedDocument json.RawMessage `json:"revised_document"`
	MediaAssets     json.RawMessage `json:"media_assets"`
	MarkdownContent string          `json:"markdown_content"`
	TargetDocument  json.RawMessage `json:"target_document"`
	Title           string          `json:"title"`
	Adapter         string          `json:"adapter"`
	Template        string          `json:"template,omitempty"`
	ToolConfig      map[string]any  `json:"tool_config"`
}

type WriterDocumentSyncResponse struct {
	Success           bool            `json:"success"`
	Changed           bool            `json:"changed"`
	ProviderSynced    bool            `json:"provider_synced"`
	PatchResult       json.RawMessage `json:"patch_result"`
	PersistedDocument json.RawMessage `json:"persisted_document"`
	Representation    string          `json:"representation"`
	Provider          string          `json:"provider"`
	WriteResult       json.RawMessage `json:"write_result"`
	TargetDocument    json.RawMessage `json:"target_document"`
}

func SyncWriterDocument(
	ctx context.Context,
	req WriterDocumentSyncRequest,
) (*WriterDocumentSyncResponse, int, error) {
	if len(req.SourceDocument) == 0 {
		return convertAndWriteWriterDocument(ctx, req)
	}
	arguments := map[string]any{}
	arguments["source_document"] = req.SourceDocument
	arguments["revised_document"] = req.RevisedDocument
	if len(req.MediaAssets) > 0 {
		arguments["media_assets"] = req.MediaAssets
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

func convertAndWriteWriterDocument(
	ctx context.Context,
	req WriterDocumentSyncRequest,
) (*WriterDocumentSyncResponse, int, error) {
	if req.Adapter == "" {
		return nil, 0, fmt.Errorf("provider is required for document conversion")
	}
	artifact := req.RevisedDocument
	if req.MarkdownContent != "" {
		artifact, _ = json.Marshal(req.MarkdownContent)
	}
	if len(artifact) == 0 {
		return nil, 0, fmt.Errorf("document content is required for conversion")
	}
	convertArguments := map[string]any{"provider": req.Adapter}
	if req.Template != "" {
		convertArguments["template"] = req.Template
	}
	if len(req.TargetDocument) > 0 {
		convertArguments["target_document"] = req.TargetDocument
	}
	if len(req.MediaAssets) > 0 {
		convertArguments["media_assets"] = req.MediaAssets
	}
	converted, status, err := InvokeWorkflowAction(ctx, WorkflowActionInvokeRequest{
		WorkflowID: req.WorkflowID, RevisionID: req.RevisionID, TreeHash: req.TreeHash,
		UserID: req.UserID, Action: "convert_document", Phase: "execute",
		Slot: "draft_document", Artifact: artifact, Arguments: convertArguments,
		ToolConfig: req.ToolConfig,
	})
	if err != nil {
		return nil, status, err
	}
	var convertedDocument map[string]any
	if err := json.Unmarshal(converted.Result, &convertedDocument); err != nil {
		return nil, status, fmt.Errorf("decode convert_document action response: %w", err)
	}
	writeArguments := map[string]any{"converted_document": convertedDocument}
	if len(req.TargetDocument) > 0 {
		writeArguments["target_document"] = req.TargetDocument
	}
	if len(req.MediaAssets) > 0 {
		writeArguments["media_assets"] = req.MediaAssets
	}
	if req.Title != "" {
		writeArguments["title"] = req.Title
	}
	written, status, err := InvokeWorkflowAction(ctx, WorkflowActionInvokeRequest{
		WorkflowID: req.WorkflowID, RevisionID: req.RevisionID, TreeHash: req.TreeHash,
		UserID: req.UserID, Action: "write_document", Phase: "execute",
		Slot: "draft_document", Arguments: writeArguments, ToolConfig: req.ToolConfig,
	})
	if err != nil {
		return nil, status, err
	}
	var response WriterDocumentSyncResponse
	if err := json.Unmarshal(written.Result, &response); err != nil {
		return nil, status, fmt.Errorf("decode write_document action response: %w", err)
	}
	return &response, status, nil
}
