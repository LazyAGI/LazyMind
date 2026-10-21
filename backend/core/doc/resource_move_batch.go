package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"sort"
	"time"
)

type resourceMoveBatch struct {
	Roots             []ResourceRef           `json:"roots"`
	RootDigests       []string                `json:"root_digests"`
	Items             []ResourceOperationItem `json:"items"`
	DestinationDigest string                  `json:"destination_digest"`
}

func resourceRootDigest(tree resourceTree) string {
	rows := append([]orm.Document(nil), tree.rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return resourceHash(rows)
}
func (s *ResourceService) loadMoveDestination(ctx context.Context, tx *gorm.DB, op ResourceOperationContext, ref ResourceRef) (orm.Dataset, orm.Document, error) {
	ds, err := s.dataset(ctx, tx, op, ref.KnowledgeBaseID, acl.PermissionDatasetUpload)
	var folder orm.Document
	if err != nil {
		return ds, folder, err
	}
	if ref.Kind == "folder" {
		folder, err = resourceDocument(tx, ref)
	} else if ref.Kind != "knowledge_base" {
		err = resourceErr("INVALID_ARGUMENT", "invalid destination")
	}
	return ds, folder, err
}
func resourceMoveDestinationDigest(ds orm.Dataset, folder orm.Document) string {
	return resourceHash([]any{ds.ID, ds.KbID, ds.DisplayName, ds.Desc, ds.Scene, ds.ProcessingLevel, ds.ProcessingRevision, ds.Ext, ds.TenantID, ds.CreateUserID, ds.DeletedAt, folder.ID, folder.DatasetID, folder.PID, folder.DisplayName, folder.DocumentType, folder.DeletedAt})
}
func (s *ResourceService) moveDestinationDigest(ctx context.Context, tx *gorm.DB, op ResourceOperationContext, ref ResourceRef) (string, error) {
	ds, folder, err := s.loadMoveDestination(ctx, tx, op, ref)
	if err != nil {
		return "", err
	}
	return resourceMoveDestinationDigest(ds, folder), nil
}

// Move commits each independent root separately. A durable, bounded header fixes
// normalized membership before the first root can disappear from its source.
func (s *ResourceService) Move(ctx context.Context, op ResourceOperationContext, req MoveResourcesRequest) (ResourceOperationResult, error) {
	if req.PageMetadata != nil && len(req.Sources) > 1 {
		return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "combined page metadata requires a single resource")
	}
	if len(req.Sources) <= 1 {
		// A prior multi-root attempt may already have committed children. Check
		// its fixed input before allowing a shorter retry to become a new command.
		if op.OperationKey != "" {
			var prior orm.ResourceOperationReceipt
			key := "move:" + resourceHash([]string{op.OperationKey})[:40] + ":header"
			err := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, key).First(&prior).Error
			if err == nil && (prior.Action != "knowledge.resource.move.header" || prior.ParameterHash != resourceOperationHash(op, req)) {
				return ResourceOperationResult{}, resourceErr("CONFLICT", "operation key was already used for different parameters")
			}
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return ResourceOperationResult{}, err
			}
		}
		return s.moveAtomic(ctx, op, req, nil)
	}
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceOperationResult{}, err
	}
	if saved, found, err := s.replay(ctx, op, "knowledge.resource.move", req); found || err != nil {
		return saved, err
	}
	for _, ref := range req.Sources {
		if ref.Kind != "folder" && ref.Kind != "document" {
			return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "only folders and documents can move")
		}
		if ref.KnowledgeBaseID != req.Destination.KnowledgeBaseID && !s.budget.Calibrated {
			return ResourceOperationResult{}, resourceErr("UNAVAILABLE", "cross-library move budget has not been calibrated for this deployment")
		}
	}
	parentOp := op
	if parentOp.OperationKey == "" {
		parentOp.OperationKey = newDocID()
	}
	digest := resourceOperationHash(op, req)
	prefix := "move:" + resourceHash([]string{parentOp.OperationKey})[:40]
	headerKey := prefix + ":header"
	var header resourceMoveBatch
	var receipt orm.ResourceOperationReceipt
	query := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, headerKey)
	err := query.First(&receipt).Error
	if err == nil {
		if receipt.Action != "knowledge.resource.move.header" || receipt.ParameterHash != digest {
			return ResourceOperationResult{}, resourceErr("CONFLICT", "operation key was already used for different parameters")
		}
		if err = json.Unmarshal(receipt.Result, &header); err != nil {
			return ResourceOperationResult{}, err
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		trees, err := s.trees(ctx, s.db, op, req.Sources)
		if err != nil {
			return ResourceOperationResult{}, err
		}
		target, folder, err := s.loadMoveDestination(ctx, s.db, op, req.Destination)
		if err != nil {
			return ResourceOperationResult{}, err
		}
		if op.ExpectedSnapshot != "" && resourceMoveSnapshotDigest(trees, target, folder) != op.ExpectedSnapshot {
			return ResourceOperationResult{}, resourceErr("CONFLICT", "resource snapshot changed; repeat preflight")
		}
		header.DestinationDigest = resourceMoveDestinationDigest(target, folder)
		for _, tree := range trees {
			header.Roots = append(header.Roots, tree.root)
			header.RootDigests = append(header.RootDigests, resourceRootDigest(tree))
			header.Items = append(header.Items, resourceItem(tree.rows[0], "not_attempted"))
		}
		raw, _ := json.Marshal(header)
		record := orm.ResourceOperationReceipt{TenantID: resourceTenant(op.Caller.TenantID), UserID: op.Caller.UserID, OperationKey: headerKey, Action: "knowledge.resource.move.header", ParameterHash: digest, Result: raw, CreatedAt: time.Now().UTC()}
		if op.ValidateBeforeCommit != nil {
			if err := op.ValidateBeforeCommit(ctx); err != nil {
				return ResourceOperationResult{}, err
			}
		}
		if err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if op.ValidateExecution != nil {
				if e := op.ValidateExecution(ctx, tx); e != nil {
					return e
				}
			}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error
		}); err != nil {
			return ResourceOperationResult{}, err
		}
		if err = s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, headerKey).First(&receipt).Error; err != nil {
			return ResourceOperationResult{}, err
		}
		if receipt.ParameterHash != digest {
			return ResourceOperationResult{}, resourceErr("CONFLICT", "operation key was already used for different parameters")
		}
		if err = json.Unmarshal(receipt.Result, &header); err != nil {
			return ResourceOperationResult{}, err
		}
	} else {
		return ResourceOperationResult{}, err
	}
	result := ResourceOperationResult{Action: "knowledge.resource.move", Status: "succeeded", Items: []ResourceOperationItem{}}
	for i, root := range header.Roots {
		child := op
		child.OperationKey = fmt.Sprintf("%s:root:%d", prefix, i)
		child.InputDigest = resourceHash([]any{digest, i, root})
		child.ExpectedSnapshot = ""
		request := MoveResourcesRequest{Sources: []ResourceRef{root}, Destination: req.Destination}
		if saved, found, e := s.replay(ctx, child, "knowledge.resource.move", request); found && e == nil {
			result.Items = append(result.Items, saved.Items...)
			continue
		} else if e != nil && ctx.Err() == nil {
			// Failure to read a child receipt is not evidence that it never committed.
			item := header.Items[i]
			item.ResourceStatus = "unconfirmed"
			item.Error = resourceErr("UNCONFIRMED", "could not read this root's operation receipt; verify before retrying")
			result.Items = append(result.Items, item)
			continue
		}
		item := header.Items[i]
		var outcome ResourceOperationResult
		var rootErr error
		if ctx.Err() != nil {
			item.ResourceStatus = "not_attempted"
			item.Error = resourceErr("CANCELED", "request ended before this root was attempted")
			result.Items = append(result.Items, item)
			continue
		}
		current, e := s.tree(ctx, s.db, op, root)
		if e != nil {
			rootErr = e
		} else if resourceRootDigest(current) != header.RootDigests[i] {
			rootErr = resourceErr("CONFLICT", "root members or metadata changed after batch preflight")
		}
		if rootErr == nil {
			destination, e := s.moveDestinationDigest(ctx, s.db, op, req.Destination)
			if e != nil {
				rootErr = e
			} else if destination != header.DestinationDigest {
				rootErr = resourceErr("CONFLICT", "destination changed after batch preflight")
			}
		}
		if rootErr == nil {
			outcome, rootErr = s.moveAtomic(ctx, child, request, &resourceMoveExpectation{RootDigest: header.RootDigests[i], DestinationDigest: header.DestinationDigest})
		}
		if rootErr != nil {
			item.ResourceStatus = "failed"
			if resourceOutcomeUnconfirmed(rootErr) {
				item.ResourceStatus = "unconfirmed"
				if len(outcome.Items) > 0 {
					item = outcome.Items[0]
				}
			}
			var typed *ResourceError
			if errors.As(rootErr, &typed) {
				item.Error = typed
			} else {
				item.Error = resourceErr("INTERNAL", rootErr.Error())
			}
			outcome = ResourceOperationResult{Action: "knowledge.resource.move", Status: item.ResourceStatus, Items: []ResourceOperationItem{item}}
			saved, e := s.storeResourceOutcome(child, request, outcome)
			if e == nil {
				outcome = saved
			}
		}
		result.Items = append(result.Items, outcome.Items...)
	}
	for _, item := range result.Items {
		result.Summary.Total++
		switch item.ResourceStatus {
		case "moved":
			result.Summary.Succeeded++
		case "unconfirmed":
			result.Summary.Unconfirmed++
		case "not_attempted", "skipped":
			result.Summary.Skipped++
		default:
			result.Summary.Failed++
		}
	}
	if result.Summary.Unconfirmed > 0 {
		result.Status = "unconfirmed"
	} else if result.Summary.Failed > 0 || result.Summary.Skipped > 0 {
		if result.Summary.Succeeded > 0 {
			result.Status = "partial"
		} else {
			result.Status = "failed"
		}
	}
	saved, err := s.storeResourceOutcome(parentOp, req, result)
	if err != nil {
		return result, nil
	}
	return saved, nil
}
func (s *ResourceService) storeResourceOutcome(op ResourceOperationContext, input any, result ResourceOperationResult) (ResourceOperationResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	record := orm.ResourceOperationReceipt{TenantID: resourceTenant(op.Caller.TenantID), UserID: op.Caller.UserID, OperationKey: op.OperationKey, Action: result.Action, ParameterHash: resourceOperationHash(op, input), Result: raw, CreatedAt: time.Now().UTC()}
	if err = s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
		return result, err
	}
	saved, _, err := s.replay(ctx, op, result.Action, input)
	return saved, err
}

// HasCommittedMove distinguishes a resumable committed batch from a header that
// has not changed any resource. It never creates receipts or changes resources.
func (s *ResourceService) HasCommittedMove(ctx context.Context, op ResourceOperationContext, req MoveResourcesRequest) (bool, error) {
	if err := resourceAccount(op.Caller); err != nil {
		return false, err
	}
	if _, found, err := s.replay(ctx, op, "knowledge.resource.move", req); found || err != nil {
		return found && err == nil, err
	}
	if op.OperationKey == "" {
		return false, nil
	}
	prefix := "move:" + resourceHash([]string{op.OperationKey})[:40]
	var receipt orm.ResourceOperationReceipt
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, prefix+":header").First(&receipt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	digest := resourceOperationHash(op, req)
	if receipt.Action != "knowledge.resource.move.header" || receipt.ParameterHash != digest {
		return false, resourceErr("CONFLICT", "operation key was already used for different parameters")
	}
	var header resourceMoveBatch
	if err = json.Unmarshal(receipt.Result, &header); err != nil {
		return false, err
	}
	if len(header.Roots) > s.budget.MaxResources {
		return false, s.tooLarge("roots", int64(len(header.Roots)), int64(s.budget.MaxResources))
	}
	for i, root := range header.Roots {
		child := op
		child.OperationKey = fmt.Sprintf("%s:root:%d", prefix, i)
		child.InputDigest = resourceHash([]any{digest, i, root})
		result, found, err := s.replay(ctx, child, "knowledge.resource.move", MoveResourcesRequest{Sources: []ResourceRef{root}, Destination: req.Destination})
		if err != nil {
			return false, err
		}
		if !found {
			continue
		}
		for _, item := range result.Items {
			if item.ResourceStatus == "moved" || item.ResourceStatus == "unconfirmed" {
				return true, nil
			}
		}
	}
	return false, nil
}
