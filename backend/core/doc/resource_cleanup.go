package doc

import (
	"context"
	"encoding/json"
	"lazymind/core/common/orm"
)

// Resource visibility is already committed; cleanup errors never undo the move/delete.
func (s *ResourceService) cleanupResourceTree(ctx context.Context, caller DatasetCatalogCaller, tree resourceTree) (string, error) {
	if tree.root.Kind == "knowledge_base" {
		if err := deleteScanSourceForDataset(ctx, tree.dataset.ID); err != nil {
			return "failed", err
		}
		if err := deleteDatasetAlgoKB(ctx, &tree.dataset, caller.UserID); err != nil {
			return "failed", err
		}
	} else {
		if err := deleteExternalDocsWithContext(ctx, tree.dataset.ID, tree.dataset.KbID, tree.rows); err != nil {
			return "failed", err
		}
	}
	if err := deleteUploadedFilesWithDB(ctx, s.db, tree.dataset.TenantID, tree.dataset.ID, tree.rows); err != nil {
		return "failed", err
	}
	return "submitted", nil
}
func (s *ResourceService) updateReceiptResult(ctx context.Context, op ResourceOperationContext, result ResourceOperationResult) {
	if op.OperationKey == "" {
		return
	}
	raw, _ := json.Marshal(result)
	_ = s.db.WithContext(ctx).Model(&orm.ResourceOperationReceipt{}).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, op.OperationKey).Update("result", raw).Error
}
