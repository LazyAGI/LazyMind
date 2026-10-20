package doc

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"sort"
	"time"
)

type resourceTree struct {
	root    ResourceRef
	dataset orm.Dataset
	rows    []orm.Document
}

func (s *ResourceService) tree(ctx context.Context, tx *gorm.DB, op ResourceOperationContext, ref ResourceRef) (resourceTree, error) {
	tree := resourceTree{root: ref}
	ds, err := s.dataset(ctx, tx, op, ref.KnowledgeBaseID, acl.PermissionDatasetWrite)
	if err != nil {
		return tree, err
	}
	tree.dataset = ds
	if ref.Kind == "knowledge_base" {
		err = tx.Where("dataset_id = ? AND deleted_at IS NULL", ds.ID).Order("id").Limit(s.budget.MaxResources + 1).Find(&tree.rows).Error
		if len(tree.rows) > s.budget.MaxResources {
			return tree, s.tooLarge("resources", int64(len(tree.rows)), int64(s.budget.MaxResources))
		}
		return tree, err
	}
	row, err := resourceDocument(tx, ref)
	if err != nil {
		return tree, err
	}
	tree.rows = []orm.Document{row}
	seen := map[string]bool{row.ID: true}
	parents := []string{}
	if isFolderLikeDocument(row) {
		parents = append(parents, row.ID)
	}
	for len(parents) > 0 {
		var children []orm.Document
		err = tx.Where("dataset_id = ? AND p_id IN ? AND deleted_at IS NULL", ds.ID, parents).Order("id").Limit(s.budget.MaxResources - len(tree.rows) + 1).Find(&children).Error
		if err != nil {
			return tree, err
		}
		if len(tree.rows)+len(children) > s.budget.MaxResources {
			return tree, s.tooLarge("resources", int64(len(tree.rows)+len(children)), int64(s.budget.MaxResources))
		}
		parents = nil
		for _, child := range children {
			if seen[child.ID] {
				return tree, resourceErr("CONFLICT", "resource tree contains a cycle")
			}
			seen[child.ID] = true
			tree.rows = append(tree.rows, child)
			if isFolderLikeDocument(child) {
				parents = append(parents, child.ID)
			}
		}
	}
	return tree, nil
}
func (s *ResourceService) tooLarge(dimension string, actual, limit int64) *ResourceError {
	return &ResourceError{Code: "OPERATION_TOO_LARGE", Message: "operation exceeds synchronous execution budget", Details: map[string]any{"dimension": dimension, "actual": actual, "limit": limit, "suggestions": []string{"narrow_scope", "split_by_subfolder"}}}
}
func treeBytes(rows []orm.Document) int64 {
	var total int64
	for _, row := range rows {
		var ext documentExt
		_ = json.Unmarshal(row.Ext, &ext)
		total += ext.FileSize
	}
	return total
}
func treeSnapshot(trees []resourceTree) ResourceSnapshot {
	var snap ResourceSnapshot
	type snapshotRow struct {
		Dataset string
		Version int64
		Rows    []orm.Document
	}
	parts := []snapshotRow{}
	seen := map[string]bool{}
	for _, tree := range trees {
		rows := append([]orm.Document(nil), tree.rows...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
		parts = append(parts, snapshotRow{tree.dataset.ID, tree.dataset.ResourceVersion, rows})
		for _, row := range rows {
			if seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			snap.Resources = append(snap.Resources, resourceRef(row))
			if isFolderLikeDocument(row) {
				snap.FolderCount++
			} else {
				snap.DocumentCount++
				snap.TotalBytes += treeBytes([]orm.Document{row})
			}
		}
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].Dataset != parts[j].Dataset {
			return parts[i].Dataset < parts[j].Dataset
		}
		return resourceHash(parts[i].Rows) < resourceHash(parts[j].Rows)
	})
	snap.Digest = resourceHash(parts)
	return snap
}
func (s *ResourceService) trees(ctx context.Context, tx *gorm.DB, op ResourceOperationContext, refs []ResourceRef) ([]resourceTree, error) {
	if len(refs) == 0 {
		return nil, resourceErr("INVALID_ARGUMENT", "resources required")
	}
	if len(refs) > s.budget.MaxResources {
		return nil, s.tooLarge("roots", int64(len(refs)), int64(s.budget.MaxResources))
	}
	trees := []resourceTree{}
	seen := map[string]bool{}
	uniqueRows := map[string]bool{}
	for _, ref := range refs {
		key := ref.KnowledgeBaseID + "/" + ref.ID()
		if seen[key] {
			continue
		}
		seen[key] = true
		tree, err := s.tree(ctx, tx, op, ref)
		if err != nil {
			return nil, err
		}
		for _, row := range tree.rows {
			uniqueRows[row.ID] = true
		}
		if len(uniqueRows) > s.budget.MaxResources {
			return nil, s.tooLarge("resources", int64(len(uniqueRows)), int64(s.budget.MaxResources))
		}
		trees = append(trees, tree)
	}
	out := []resourceTree{}
	for i, tree := range trees {
		nested := false
		for j, other := range trees {
			if i == j || tree.dataset.ID != other.dataset.ID {
				continue
			}
			if other.root.Kind == "knowledge_base" {
				nested = true
				break
			}
			for _, row := range other.rows {
				if row.ID == tree.root.ID() {
					nested = true
					break
				}
			}
		}
		if !nested {
			out = append(out, tree)
		}
	}
	return out, nil
}
func (s *ResourceService) Snapshot(ctx context.Context, op ResourceOperationContext, refs []ResourceRef) (ResourceSnapshot, error) {
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceSnapshot{}, err
	}
	trees, err := s.trees(ctx, s.db.WithContext(ctx), op, refs)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	for _, tree := range trees {
		if tree.root.Kind == "knowledge_base" && tree.dataset.CreateUserID != op.Caller.UserID {
			return ResourceSnapshot{}, resourceErr("FORBIDDEN", "whole knowledge base deletion requires owner")
		}
		if err := s.sourceAccess(ctx, op.Caller, tree.dataset.ID, "DELETE"); err != nil {
			return ResourceSnapshot{}, err
		}
	}
	snapshot := treeSnapshot(trees)
	snapshot.PreviewResources, err = s.snapshotPreviews(ctx, trees)
	return snapshot, err
}
func verifyResourceSnapshot(op ResourceOperationContext, trees []resourceTree) error {
	if op.ExpectedSnapshot != "" && op.ExpectedSnapshot != treeSnapshot(trees).Digest {
		return resourceErr("CONFLICT", "resource snapshot changed; repeat preflight")
	}
	return nil
}
func (s *ResourceService) Delete(ctx context.Context, op ResourceOperationContext, req DeleteResourcesRequest) (ResourceOperationResult, error) {
	committedTrees := []resourceTree{}
	result, err := s.mutate(ctx, op, "knowledge.resource.delete", req, func(tx *gorm.DB) (ResourceOperationResult, error) {
		var out ResourceOperationResult
		trees, err := s.trees(ctx, tx, op, req.Targets)
		if err != nil {
			return out, err
		}
		if err = verifyResourceSnapshot(op, trees); err != nil {
			return out, err
		}
		locked := map[string]bool{}
		for _, tree := range trees {
			ds := tree.dataset
			if ds.CreateUserID != op.Caller.UserID && tree.root.Kind == "knowledge_base" {
				return out, resourceErr("FORBIDDEN", "whole knowledge base deletion requires owner")
			}
			if err = s.checkSource(ctx, tx, op.Caller, ds.ID, "DELETE"); err != nil {
				return out, err
			}
			if !locked[ds.ID] {
				if err = lockResourceDataset(tx, ds); err != nil {
					return out, err
				}
				locked[ds.ID] = true
			}
			if err = deleteResourceRows(tx, tree.rows); err != nil {
				return out, err
			}
			item := ResourceOperationItem{ResourceRef: tree.root, Name: ds.DisplayName, ResourceStatus: "deleted", CleanupStatus: "not_submitted", EntryURL: "/lib/knowledge/detail/" + ds.ID}
			if tree.root.Kind == "knowledge_base" {
				now := time.Now().UTC()
				if err = tx.Model(&orm.Dataset{}).Where("id = ? AND deleted_at IS NULL", ds.ID).Updates(map[string]any{"deleted_at": now, "updated_at": now}).Error; err != nil {
					return out, err
				}
				if err = cleanupEvalSetDatasetReferences(ctx, tx, ds.ID, now); err != nil {
					return out, err
				}
				if err = tx.Where("dataset_id = ?", ds.ID).Delete(&orm.DefaultDataset{}).Error; err != nil {
					return out, err
				}
			} else {
				item.Name = tree.rows[0].DisplayName
				item.ParentID = tree.rows[0].PID
			}
			for _, row := range tree.rows {
				if isFolderLikeDocument(row) {
					item.FolderCount++
				} else {
					item.DocumentCount++
				}
			}
			if tree.root.Kind != "knowledge_base" {
				if err = s.refreshAncestorStats(tx, ds.ID, tree.rows[0].PID); err != nil {
					return out, err
				}
			}
			out.Items = append(out.Items, item)
		}
		committedTrees = trees
		return out, nil
	})
	if err != nil {
		return result, err
	}
	for i, tree := range committedTrees {
		state, cleanupErr := s.cleanupResourceTree(ctx, op.Caller, tree)
		result.Items[i].CleanupStatus = state
		if cleanupErr != nil {
			result.Items[i].Error = resourceErr("CLEANUP_FAILED", cleanupErr.Error())
		}
	}
	s.updateReceiptResult(ctx, op, result)
	return result, nil
}
func deleteResourceRows(tx *gorm.DB, rows []orm.Document) error {
	now := time.Now().UTC()
	for _, row := range rows {
		r := tx.Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND resource_version = ? AND deleted_at IS NULL", row.ID, row.DatasetID, row.ResourceVersion).Updates(map[string]any{"deleted_at": now, "updated_at": now, "tags": json.RawMessage(`[]`), "resource_version": gorm.Expr("resource_version + 1")})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return resourceErr("CONFLICT", "source changed during operation")
		}
		if err := tx.Where("dataset_id = ? AND document_id = ?", row.DatasetID, row.ID).Delete(&orm.DocumentProcessingState{}).Error; err != nil {
			return err
		}
	}
	// Retain historical tasks. Active publication is fenced by the deleted document.
	return nil
}

// SnapshotMove binds confirmation to source membership and the precise destination,
// without expanding the destination library's unrelated contents.
func (s *ResourceService) SnapshotMove(ctx context.Context, op ResourceOperationContext, req MoveResourcesRequest) (ResourceSnapshot, error) {
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceSnapshot{}, err
	}
	trees, err := s.trees(ctx, s.db.WithContext(ctx), op, req.Sources)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	target, folder, err := s.loadMoveDestination(ctx, s.db, op, req.Destination)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	snapshot := treeSnapshot(trees)
	snapshot.Digest = resourceMoveSnapshotDigest(trees, target, folder)
	snapshot.PreviewResources, err = s.snapshotPreviews(ctx, trees)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	destinationTree := resourceTree{root: req.Destination, dataset: target}
	if folder.ID != "" {
		destinationTree.rows = []orm.Document{folder}
	}
	preview, err := s.snapshotPreviews(ctx, []resourceTree{destinationTree})
	if err != nil {
		return ResourceSnapshot{}, err
	}
	if len(preview) > 0 {
		snapshot.Destination = &preview[0]
	}
	return snapshot, nil
}

func resourceMoveSnapshotDigest(trees []resourceTree, target orm.Dataset, folder orm.Document) string {
	return resourceHash([]any{treeSnapshot(trees).Digest, target, folder})
}
