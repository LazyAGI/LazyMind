package doc

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// Upload binds independently prepared originals in the same transaction as the operation receipt.
func (s *ResourceService) Upload(ctx context.Context, op ResourceOperationContext, requests []ImportOriginalRequest) (ResourceOperationResult, error) {
	var out ResourceOperationResult
	if err := resourceAccount(op.Caller); err != nil {
		return out, err
	}
	if len(requests) == 0 {
		return out, resourceErr("INVALID_ARGUMENT", "files required")
	}
	if len(requests) > s.budget.MaxResources {
		return out, s.tooLarge("files", int64(len(requests)), int64(s.budget.MaxResources))
	}
	if cached, found, err := s.replay(ctx, op, "knowledge.document.upload", requests); found || err != nil {
		return cached, err
	}
	if s.originals == nil {
		return out, resourceErr("UNAVAILABLE", "original service unavailable")
	}
	prepared := []*PreparedOriginalBinding{}
	defer func() {
		for _, p := range prepared {
			_ = s.originals.CleanupPreparedBinding(p)
		}
	}()
	for _, req := range requests {
		if op.AllowedDatasetIDs != nil && !containsAll(op.AllowedDatasetIDs, []string{req.DatasetID}) {
			return out, resourceErr("FORBIDDEN", "knowledge base outside run scope")
		}
		p, err := s.originals.PrepareOriginalBinding(ctx, op.Caller, req)
		if err != nil {
			return out, err
		}
		prepared = append(prepared, p)
	}
	extra := []ResourceAuthorizationRequest{}
	for _, p := range prepared {
		extra = append(extra, p.AuthorizationRequests()...)
	}
	ctx = context.WithValue(ctx, resourceAdditionalAuthorizationKey{}, extra)
	bindings := []OriginalImportResult{}
	committed := false
	result, err := s.mutate(ctx, op, "knowledge.document.upload", requests, func(tx *gorm.DB) (ResourceOperationResult, error) {
		var result ResourceOperationResult
		for _, p := range prepared {
			bound, e := s.originals.BindOriginalTx(ctx, tx, op.Caller, p)
			if e != nil {
				return result, e
			}
			bindings = append(bindings, bound)
			item := resourceItem(bound.Document, "created")
			item.TaskID = bound.Task.ID
			item.ProcessingStatus = "not_submitted"
			if bound.Skipped {
				item.ResourceStatus = "skipped"
			}
			result.Items = append(result.Items, item)
		}
		for _, req := range requests {
			if err := s.refreshAncestorStats(tx, req.DatasetID, req.ParentID); err != nil {
				return result, err
			}
		}
		committed = true
		return result, nil
	})
	if err != nil || !committed {
		if resourceOutcomeUnconfirmed(err) {
			prepared = nil
		}
		return result, err
	}
	if len(result.Items) != len(bindings) {
		return result, nil
	}
	for i, bound := range bindings {
		if result.Items[i].ResourceRef.ID() != bound.Document.ID {
			return result, nil
		}
	}
	for i, bound := range bindings {
		if bound.Skipped {
			_ = s.originals.CleanupPreparedBinding(prepared[i])
			continue
		}
		submitted := s.originals.SubmitOriginalProcessing(ctx, op.Caller, bound)
		result.Items[i].ProcessingStatus = submitted.SubmissionStatus
		if submitted.SubmissionError != "" {
			result.Items[i].Error = resourceErr("PROCESSING_FAILED", SafeProcessingMessage(submitted.SubmissionError))
		}
	}
	prepared = nil
	summarizeResourceResult(&result)
	if op.OperationKey != "" {
		raw, _ := json.Marshal(result)
		_ = s.db.WithContext(ctx).Model(&orm.ResourceOperationReceipt{}).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, op.OperationKey).Update("result", raw).Error
	}
	return result, nil
}
