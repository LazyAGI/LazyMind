package doc

import (
	"context"
	"strings"

	"lazymind/core/common/orm"
)

func (s *ResourceService) snapshotPreviews(ctx context.Context, trees []resourceTree) ([]ResourcePreview, error) {
	previews := []ResourcePreview{}
	seen := map[ResourceRef]bool{}
	for _, tree := range trees {
		rows := map[string]orm.Document{}
		for _, row := range tree.rows {
			rows[row.ID] = row
		}
		paths := map[string]string{"": tree.dataset.DisplayName}
		visiting := map[string]bool{}
		var pathFor func(string) (string, error)
		pathFor = func(id string) (string, error) {
			if path, ok := paths[id]; ok {
				return path, nil
			}
			if visiting[id] {
				return "", resourceErr("CONFLICT", "resource ancestry contains a cycle")
			}
			if len(rows) > s.budget.MaxResources {
				return "", s.tooLarge("preview_resources", int64(len(rows)), int64(s.budget.MaxResources))
			}
			visiting[id] = true
			row, ok := rows[id]
			if !ok {
				var err error
				row, err = resourceDocument(s.db.WithContext(ctx), ResourceRef{Kind: "folder", KnowledgeBaseID: tree.dataset.ID, FolderID: id})
				if err != nil {
					return "", err
				}
				rows[id] = row
			}
			parent, err := pathFor(row.PID)
			if err != nil {
				return "", err
			}
			paths[id] = strings.TrimSuffix(parent, "/") + "/" + row.DisplayName
			delete(visiting, id)
			return paths[id], nil
		}
		if tree.root.Kind == "knowledge_base" && !seen[tree.root] {
			seen[tree.root] = true
			previews = append(previews, ResourcePreview{Ref: tree.root, Name: tree.dataset.DisplayName, Path: tree.dataset.DisplayName})
		}
		for _, row := range tree.rows {
			ref := resourceRef(row)
			if seen[ref] {
				continue
			}
			path, err := pathFor(row.ID)
			if err != nil {
				return nil, err
			}
			seen[ref] = true
			previews = append(previews, ResourcePreview{Ref: ref, Name: row.DisplayName, Path: path})
		}
	}
	return previews, nil
}
