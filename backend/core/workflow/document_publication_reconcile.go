package workflow

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/modelconfig"
)

// Reconciliation reads the fixed target. It never retries an external write.
func reconcileDocumentPublication(ctx context.Context, db *gorm.DB, owner string, op *DocumentPublicationOperation) (*DocumentPublicationOperation, error) {
	if op.Status != "outcome_unknown" || len(op.TargetDocument) == 0 || len(op.SourceValue) == 0 {
		return op, nil
	}
	config, err := modelconfig.LoadWriterProviderConnectionConfig(ctx, op.Provider, owner, op.ConnectionID)
	if err != nil {
		return op, nil
	}
	artifact, _ := json.Marshal(map[string]any{"data": op.SourceValue})
	convertArgs := map[string]any{"provider": op.Provider, "target_document": op.TargetDocument}
	if len(op.MediaAssets) > 0 {
		convertArgs["media_assets"] = op.MediaAssets
	}
	converted, _, err := algo.InvokeDocumentAction(ctx, algo.DocumentActionInvokeRequest{Reference: documentConvertReference, Phase: "preview", Artifact: artifact, Arguments: convertArgs, ToolConfig: config})
	if err != nil {
		return op, nil
	}
	args := map[string]any{"converted_document": converted.Result, "target_document": op.TargetDocument}
	if len(op.MediaAssets) > 0 {
		args["media_assets"] = op.MediaAssets
	}
	response, _, err := algo.InvokeDocumentAction(ctx, algo.DocumentActionInvokeRequest{Reference: "builtin:document.reconcile.v1", Phase: "preview", Arguments: args, ToolConfig: config})
	if err != nil {
		return op, nil
	}
	var result struct {
		State   string          `json:"state"`
		Target  json.RawMessage `json:"target_document"`
		Content json.RawMessage `json:"persisted_document"`
	}
	if json.Unmarshal(response.Result, &result) != nil || result.State != "confirmed" {
		return op, nil
	}
	// Absence alone is insufficient: a timed-out upstream request might still commit.
	raw, _ := json.Marshal(map[string]any{"success": true, "provider_synced": true, "provider": op.Provider, "representation": "markdown", "persisted_document": result.Content, "target_document": result.Target, "patch_result": map[string]any{"success": true}})
	receipt, err := publicationReceiptFromResult(op, raw, result.Target, false)
	if err != nil {
		return op, err
	}
	if err := ConfirmDocumentPublication(ctx, db, owner, op.ID, receipt); err != nil {
		return op, err
	}
	if _, err := FinalizeDocumentPublication(ctx, db, owner, op.ID); err != nil {
		return op, err
	}
	return GetDocumentPublication(ctx, db, owner, op.ID)
}
