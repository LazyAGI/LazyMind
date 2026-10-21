package doc

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"strings"
	"time"
)

// resourceMoveExpectation carries the durable batch preflight into the root
// transaction. A fresh preparation snapshot cannot replace these expectations.
type resourceMoveExpectation struct {
	RootDigest        string
	DestinationDigest string
}

func (s *ResourceService) moveAtomic(ctx context.Context, op ResourceOperationContext, req MoveResourcesRequest, expected *resourceMoveExpectation) (ResourceOperationResult, error) {
	if req.PageMetadata != nil && (len(req.Sources) != 1 || req.Sources[0].KnowledgeBaseID != req.Destination.KnowledgeBaseID || op.OperationKey != "") {
		return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "combined page metadata requires a single same-library move without an operation key")
	}
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceOperationResult{}, err
	}
	if out, found, err := s.replay(ctx, op, "knowledge.resource.move", req); found || err != nil {
		return out, err
	}
	if req.Destination.Kind != "knowledge_base" && req.Destination.Kind != "folder" {
		return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "destination must be knowledge base or folder")
	}
	for _, ref := range req.Sources {
		if ref.Kind != "folder" && ref.Kind != "document" {
			return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "knowledge bases cannot be moved")
		}
	}
	trees, err := s.trees(ctx, s.db.WithContext(ctx), op, req.Sources)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	destination, destinationFolder, err := s.loadMoveDestination(ctx, s.db, op, req.Destination)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	if op.ExpectedSnapshot != "" && resourceMoveSnapshotDigest(trees, destination, destinationFolder) != op.ExpectedSnapshot {
		return ResourceOperationResult{}, resourceErr("CONFLICT", "source or destination snapshot changed; repeat preflight")
	}
	pid := destinationFolder.ID
	destinationSnapshot := resourceHash([]any{destination, destinationFolder})
	snapshot := treeSnapshot(trees).Digest
	prepared := map[string]*PreparedOriginalBinding{}
	bindings := map[string][]OriginalImportResult{}
	ids := map[string]string{}
	cross := false
	names := map[string]bool{}
	for _, tree := range trees {
		root := tree.rows[0]
		if names[root.DisplayName] {
			return ResourceOperationResult{}, resourceErr("CONFLICT", "selected roots have the same destination name")
		}
		names[root.DisplayName] = true
		name := root.DisplayName
		if req.PageMetadata != nil && req.PageMetadata.Name != nil {
			name = strings.TrimSpace(*req.PageMetadata.Name)
		}
		if err = resourceNameConflict(s.db, destination.ID, pid, name, root.ID); err != nil {
			return ResourceOperationResult{}, err
		}
		for _, row := range tree.rows {
			if tree.dataset.ID == destination.ID && row.ID == pid {
				return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "cannot move into self or descendant")
			}
		}
		if tree.dataset.ID != destination.ID {
			cross = true
		}
	}
	prepareDuration := s.budget.MaxPrepareDuration
	if cross {
		bytes := treeSnapshot(trees).TotalBytes
		if bytes > s.budget.MaxBytes {
			return ResourceOperationResult{}, s.tooLarge("bytes", bytes, s.budget.MaxBytes)
		}
		estimated := time.Duration(float64(bytes) / float64(s.budget.CopyBytesPerSecond) * float64(time.Second))
		// Leave the request enough time to commit and reconcile its durable receipt.
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline) - 3*time.Second
			if remaining < prepareDuration {
				prepareDuration = remaining
			}
		}
		if prepareDuration <= 0 || estimated > prepareDuration {
			err := s.tooLarge("prepare_milliseconds", max(int64(1), estimated.Milliseconds()), max(int64(0), prepareDuration.Milliseconds()))
			err.Details["reserved_receipt_milliseconds"] = int64(3000)
			return ResourceOperationResult{}, err
		}
		if s.originals == nil {
			return ResourceOperationResult{}, resourceErr("UNAVAILABLE", "original service unavailable")
		}
	}
	defer func() {
		for _, p := range prepared {
			_ = s.originals.CleanupPreparedBinding(p)
		}
	}()
	prepareCtx, cancel := context.WithTimeout(ctx, prepareDuration)
	defer cancel()
	for _, tree := range trees {
		if tree.dataset.ID == destination.ID {
			continue
		}
		for _, row := range tree.rows {
			if isFolderLikeDocument(row) {
				ids[row.ID] = newDocID()
				continue
			}
			p, e := s.originals.PrepareDocumentOriginalBinding(prepareCtx, op.Caller, row, destination.ID, pid)
			if e != nil {
				return ResourceOperationResult{}, e
			}
			prepared[row.ID] = p
		}
	}
	committed := false
	out, err := s.mutate(ctx, op, "knowledge.resource.move", req, func(tx *gorm.DB) (ResourceOperationResult, error) {
		var out ResourceOperationResult
		current, e := s.trees(ctx, tx, op, req.Sources)
		if e != nil {
			return out, e
		}
		if treeSnapshot(current).Digest != snapshot {
			return out, resourceErr("CONFLICT", "source tree changed during preparation")
		}
		if expected != nil && (len(current) != 1 || resourceRootDigest(current[0]) != expected.RootDigest) {
			return out, resourceErr("CONFLICT", "root members or metadata changed after batch preflight")
		}
		target, targetFolder, e := s.loadMoveDestination(ctx, tx, op, req.Destination)
		if e != nil {
			return out, e
		}
		if resourceHash([]any{target, targetFolder}) != destinationSnapshot {
			return out, resourceErr("CONFLICT", "destination changed during preparation")
		}
		if expected != nil && resourceMoveDestinationDigest(target, targetFolder) != expected.DestinationDigest {
			return out, resourceErr("CONFLICT", "destination changed after batch preflight")
		}
		locked := map[string]bool{}
		allDatasets := []orm.Dataset{target}
		for _, tree := range current {
			allDatasets = append(allDatasets, tree.dataset)
		}
		for _, ds := range allDatasets {
			if !locked[ds.ID] {
				if e = lockResourceDataset(tx, ds); e != nil {
					return out, e
				}
				locked[ds.ID] = true
			}
		}
		affected := map[string][]string{}
		for _, tree := range current {
			root := tree.rows[0]
			name := root.DisplayName
			if req.PageMetadata != nil && req.PageMetadata.Name != nil {
				name = strings.TrimSpace(*req.PageMetadata.Name)
			}
			if e = resourceNameConflict(tx, target.ID, pid, name, root.ID); e != nil {
				return out, e
			}
			item := resourceItem(root, "moved")
			oldRef := item.ResourceRef
			item.PreviousRef = &oldRef
			item.ParentID = pid
			item.IDMapping = map[string]string{}
			for _, row := range tree.rows {
				if isFolderLikeDocument(row) {
					item.FolderCount++
				} else {
					item.DocumentCount++
				}
			}
			if tree.dataset.ID == target.ID {
				updates := map[string]any{"p_id": pid, "updated_at": time.Now().UTC(), "resource_version": gorm.Expr("resource_version + 1")}
				if req.PageMetadata != nil {
					changes, before, after, e := resourceDocumentChanges(tx, root, *req.PageMetadata, pid)
					if e != nil {
						return out, e
					}
					for key, value := range changes {
						updates[key] = value
					}
					item.Before, item.After = before, after
					if name, ok := changes["display_name"].(string); ok {
						item.Name = name
					}
				}
				r := tx.Model(&orm.Document{}).Where("id = ? AND resource_version = ? AND deleted_at IS NULL", root.ID, root.ResourceVersion).Updates(updates)
				if r.Error != nil {
					return out, r.Error
				}
				if r.RowsAffected != 1 {
					return out, resourceErr("CONFLICT", "source changed")
				}
				item.IDMapping[root.ID] = root.ID
			} else {
				for _, row := range tree.rows {
					targetPID := pid
					if row.ID != root.ID {
						targetPID = ids[row.PID]
						if targetPID == "" {
							return out, resourceErr("CONFLICT", "missing parent in source tree")
						}
					}
					if isFolderLikeDocument(row) {
						oldID := row.ID
						row.ID = ids[oldID]
						row.DatasetID = target.ID
						row.PID = targetPID
						row.LazyllmDocID = ""
						row.ResourceVersion = 1
						row.CreatedAt = time.Now().UTC()
						row.UpdatedAt = row.CreatedAt
						row.CreateUserID = op.Caller.UserID
						row.CreateUserName = op.Caller.UserName
						if e = tx.Create(&row).Error; e != nil {
							return out, e
						}
						item.IDMapping[oldID] = row.ID
					} else {
						p := prepared[row.ID]
						p.SetParentID(targetPID)
						binding, e := s.originals.BindOriginalTx(ctx, tx, op.Caller, p)
						if e != nil {
							return out, e
						}
						bindings[root.ID] = append(bindings[root.ID], binding)
						ids[row.ID] = binding.Document.ID
						item.IDMapping[row.ID] = binding.Document.ID
					}
				}
				if e = deleteResourceRows(tx, tree.rows); e != nil {
					return out, e
				}
				item.ResourceRef.KnowledgeBaseID = target.ID
				if item.ResourceRef.Kind == "folder" {
					item.ResourceRef.FolderID = ids[root.ID]
				} else {
					item.ResourceRef.DocumentID = ids[root.ID]
				}
				item.ProcessingStatus = "not_submitted"
				if target.ProcessingLevel == ProcessingLevelStored {
					item.ProcessingStatus = "not_requested"
				}
				item.CleanupStatus = "not_submitted"
				item.EntryURL = "/lib/knowledge/detail/" + target.ID + "?document=" + ids[root.ID]
			}
			affected[tree.dataset.ID] = append(affected[tree.dataset.ID], root.PID)
			affected[target.ID] = append(affected[target.ID], pid)
			out.Items = append(out.Items, item)
		}
		if e := refreshFolderStats(tx, affected); e != nil {
			return out, e
		}
		committed = true
		return out, nil
	})
	if resourceOutcomeUnconfirmed(err) {
		prepared = nil
		return out, err
	}
	if err == nil && committed {
		for _, item := range out.Items {
			if item.PreviousRef != nil && ids[item.PreviousRef.ID()] != "" && ids[item.PreviousRef.ID()] != item.ResourceRef.ID() {
				return out, nil
			}
		}
	}
	if err == nil && committed {
		prepared = nil
		for i := range out.Items {
			item := &out.Items[i]
			if item.PreviousRef == nil {
				continue
			}
			results := bindings[item.PreviousRef.ID()]
			if len(results) == 0 || item.ProcessingStatus == "not_requested" {
				continue
			}
			state := "not_requested"
			failed := false
			for _, binding := range results {
				submitted := s.originals.SubmitOriginalProcessing(ctx, op.Caller, binding)
				if submitted.SubmissionStatus == "failed" {
					failed = true
					continue
				}
				if submitted.SubmissionStatus != "not_requested" {
					state = submitted.SubmissionStatus
				}
			}
			if failed {
				state = "failed"
			}
			item.ProcessingStatus = state
		}
		for i, tree := range trees {
			if tree.dataset.ID == destination.ID {
				continue
			}
			state, cleanupErr := s.cleanupResourceTree(ctx, op.Caller, tree)
			out.Items[i].CleanupStatus = state
			if cleanupErr != nil {
				out.Items[i].Error = resourceErr("CLEANUP_FAILED", cleanupErr.Error())
			}
		}
		summarizeResourceResult(&out)
		if op.OperationKey != "" {
			raw, _ := json.Marshal(out)
			_ = s.db.WithContext(ctx).Model(&orm.ResourceOperationReceipt{}).Where("tenant_id = ? AND user_id = ? AND operation_key = ?", resourceTenant(op.Caller.TenantID), op.Caller.UserID, op.OperationKey).Update("result", raw).Error
		}
	}
	return out, err
}
