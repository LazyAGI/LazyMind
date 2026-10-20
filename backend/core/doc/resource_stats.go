package doc

import (
	"bytes"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"time"
)

// Recalculate only affected ancestors, with a bounded traversal of each subtree.
func (s *ResourceService) refreshAncestorStats(tx *gorm.DB, datasetID string, parents ...string) error {
	seen := map[string]bool{}
	for _, parent := range parents {
		for parent != "" && !seen[parent] {
			if len(seen) >= s.budget.MaxResources {
				return s.tooLarge("ancestor_depth", int64(len(seen)+1), int64(s.budget.MaxResources))
			}
			seen[parent] = true
			folder, err := resourceDocument(tx, ResourceRef{Kind: "folder", KnowledgeBaseID: datasetID, FolderID: parent})
			if err != nil {
				return err
			}
			queue := []string{parent}
			rows := []orm.Document{}
			visited := map[string]bool{parent: true}
			for len(queue) > 0 {
				var children []orm.Document
				if err = tx.Where("dataset_id = ? AND p_id IN ? AND deleted_at IS NULL", datasetID, queue).Limit(s.budget.MaxResources - len(rows) + 1).Find(&children).Error; err != nil {
					return err
				}
				if len(rows)+len(children) > s.budget.MaxResources {
					return s.tooLarge("ancestor_subtree_resources", int64(len(rows)+len(children)), int64(s.budget.MaxResources))
				}
				queue = nil
				for _, child := range children {
					if visited[child.ID] {
						return resourceErr("CONFLICT", "folder tree contains a cycle")
					}
					visited[child.ID] = true
					rows = append(rows, child)
					if isFolderLikeDocument(child) {
						queue = append(queue, child.ID)
					}
				}
			}
			var ext map[string]any
			_ = json.Unmarshal(folder.Ext, &ext)
			if ext == nil {
				ext = map[string]any{}
			}
			directDocs, directFolders, docs, folders := 0, 0, 0, 0
			var totalBytes int64
			for _, row := range rows {
				if isFolderLikeDocument(row) {
					folders++
					if row.PID == parent {
						directFolders++
					}
				} else {
					docs++
					totalBytes += treeBytes([]orm.Document{row})
					if row.PID == parent {
						directDocs++
					}
				}
			}
			ext["file_size"] = totalBytes
			ext["child_document_count"] = directDocs
			ext["child_folder_count"] = directFolders
			ext["recursive_document_count"] = docs
			ext["recursive_folder_count"] = folders
			ext["recursive_file_size"] = totalBytes
			raw, _ := json.Marshal(ext)
			if bytes.Equal(raw, folder.Ext) {
				parent = folder.PID
				continue
			}
			if err = tx.Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL", folder.ID, datasetID).Updates(map[string]any{"ext": raw, "updated_at": time.Now().UTC(), "resource_version": gorm.Expr("resource_version + 1")}).Error; err != nil {
				return err
			}
			parent = folder.PID
		}
	}
	return nil
}
