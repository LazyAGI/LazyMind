package doc

import (
	"context"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/learning"
	"strings"
)

func knowledgeCreateDigest(ds orm.Dataset, refs []learning.CapabilityRef) string {
	return resourceHash([]any{ds.DisplayName, ds.Desc, ds.Scene, ds.ProcessingLevel, ds.Ext, refs})
}

// Creation preflight validates the same configuration as execution, without provisioning a KB.
func (s *ResourceService) SnapshotCreateKnowledge(ctx context.Context, op ResourceOperationContext, req CreateKnowledgeRequest) (ResourceSnapshot, error) {
	ds, refs, err := s.prepareKnowledgeCreate(ctx, op, req)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	return ResourceSnapshot{Digest: knowledgeCreateDigest(ds, refs), Creation: map[string]any{"name": ds.DisplayName, "description": ds.Desc, "scene": ds.Scene, "processing_level": ds.ProcessingLevel, "tags": parseDatasetTags(ds.Ext)}}, nil
}

func expandedFolderPaths(req CreateFoldersRequest, limit int) ([]string, error) {
	if len(req.Folders) == 0 {
		return nil, resourceErr("INVALID_ARGUMENT", "folders required")
	}
	if len(req.Folders) > limit {
		return nil, resourceErr("OPERATION_TOO_LARGE", "too many folders")
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, path := range req.Folders {
		if strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\:") {
			return nil, resourceErr("INVALID_ARGUMENT", "relative folder path required")
		}
		parts := strings.Split(path, "/")
		for i, name := range parts {
			if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name || len(name) > 512 {
				return nil, resourceErr("INVALID_ARGUMENT", "invalid folder path")
			}
			prefix := strings.Join(parts[:i+1], "/")
			if !seen[prefix] {
				seen[prefix] = true
				paths = append(paths, prefix)
			}
			if len(paths) > limit {
				return nil, resourceErr("OPERATION_TOO_LARGE", "too many expanded folders")
			}
		}
	}
	return paths, nil
}

func folderCreateDigest(ds orm.Dataset, req CreateFoldersRequest) string {
	return resourceHash([]any{ds.ID, ds.ResourceVersion, ds.DisplayName, req})
}

func (s *ResourceService) SnapshotCreateFolders(ctx context.Context, op ResourceOperationContext, req CreateFoldersRequest) (ResourceSnapshot, error) {
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceSnapshot{}, err
	}
	paths, err := expandedFolderPaths(req, s.budget.MaxResources)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	ds, err := s.dataset(ctx, s.db, op, req.KnowledgeBaseID, acl.PermissionDatasetUpload)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	if err = resourceParent(s.db, ds.ID, req.ParentID); err != nil {
		return ResourceSnapshot{}, err
	}
	for _, path := range paths {
		if !strings.Contains(path, "/") {
			if err = resourceNameConflict(s.db, ds.ID, req.ParentID, path, ""); err != nil {
				return ResourceSnapshot{}, err
			}
		}
	}
	root := ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: ds.ID}
	tree := resourceTree{dataset: ds, root: root}
	if req.ParentID != "" {
		row, e := resourceDocument(s.db, ResourceRef{Kind: "folder", KnowledgeBaseID: ds.ID, FolderID: req.ParentID})
		if e != nil {
			return ResourceSnapshot{}, e
		}
		tree.root = resourceRef(row)
		tree.rows = []orm.Document{row}
	}
	previews, err := s.snapshotPreviews(ctx, []resourceTree{tree})
	if err != nil {
		return ResourceSnapshot{}, err
	}
	fullPaths := make([]string, len(paths))
	for i, path := range paths {
		fullPaths[i] = previews[0].Path + "/" + path
	}
	return ResourceSnapshot{Digest: folderCreateDigest(ds, req), Destination: &previews[0], FolderCount: len(paths), Creation: map[string]any{"paths": fullPaths}}, nil
}
