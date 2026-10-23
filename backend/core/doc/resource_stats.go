package doc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// The caller owns the transaction. Operation budgets apply to mutated resources,
// not to unrelated documents that contribute to an ancestor's derived statistics.
func refreshFolderStats(tx *gorm.DB, affected map[string][]string) error {
	datasets := make([]string, 0, len(affected))
	for id, parents := range affected {
		for _, parent := range parents {
			if strings.TrimSpace(parent) != "" {
				datasets = append(datasets, id)
				break
			}
		}
	}
	sort.Strings(datasets)
	for _, datasetID := range datasets {
		// Serialize legacy post-commit refreshes with resource mutations before reading.
		if err := tx.Model(&orm.Dataset{}).Where("id = ? AND deleted_at IS NULL", datasetID).UpdateColumn("updated_at", gorm.Expr("updated_at")).Error; err != nil {
			return err
		}
		var rows []orm.Document
		if err := tx.Select("id", "dataset_id", "p_id", "display_name", "document_type", "ext", "resource_version").Where("dataset_id = ? AND deleted_at IS NULL", datasetID).Find(&rows).Error; err != nil {
			return err
		}
		byID := make(map[string]orm.Document, len(rows))
		children := make(map[string][]string)
		for _, row := range rows {
			byID[row.ID] = row
			children[row.PID] = append(children[row.PID], row.ID)
		}
		affectedFolders := map[string]bool{}
		for _, parent := range affected[datasetID] {
			path := map[string]bool{}
			for parent != "" {
				if path[parent] {
					return resourceErr("CONFLICT", "folder tree contains a cycle")
				}
				if affectedFolders[parent] {
					break
				}
				path[parent] = true
				row, ok := byID[parent]
				if !ok {
					break
				}
				if !isFolderLikeDocument(row) {
					return resourceErr("CONFLICT", "parent is not a folder")
				}
				affectedFolders[parent] = true
				parent = row.PID
			}
		}
		memo := map[string]folderStats{}
		visiting := map[string]bool{}
		var calculate func(string) (folderStats, error)
		calculate = func(id string) (folderStats, error) {
			if value, ok := memo[id]; ok {
				return value, nil
			}
			if visiting[id] {
				return folderStats{}, resourceErr("CONFLICT", "folder tree contains a cycle")
			}
			visiting[id] = true
			var value folderStats
			for _, childID := range children[id] {
				child := byID[childID]
				if isFolderLikeDocument(child) {
					nested, err := calculate(childID)
					if err != nil {
						return value, err
					}
					value.ChildFolderCount++
					value.RecursiveFolderCount += 1 + nested.RecursiveFolderCount
					value.RecursiveDocumentCount += nested.RecursiveDocumentCount
					value.RecursiveFileSize += nested.RecursiveFileSize
				} else {
					value.ChildDocumentCount++
					value.RecursiveDocumentCount++
					value.RecursiveFileSize += treeBytes([]orm.Document{child})
				}
			}
			delete(visiting, id)
			memo[id] = value
			return value, nil
		}
		ids := make([]string, 0, len(affectedFolders))
		for id := range affectedFolders {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			value, err := calculate(id)
			if err != nil {
				return err
			}
			row := byID[id]
			ext := map[string]any{}
			if len(row.Ext) > 0 {
				if err := json.Unmarshal(row.Ext, &ext); err != nil {
					return fmt.Errorf("folder %s metadata: %w", id, err)
				}
			}
			if ext == nil {
				ext = map[string]any{}
			}
			before, err := json.Marshal(ext)
			if err != nil {
				return err
			}
			ext["file_size"], ext["recursive_file_size"] = value.RecursiveFileSize, value.RecursiveFileSize
			ext["child_document_count"], ext["child_folder_count"] = value.ChildDocumentCount, value.ChildFolderCount
			ext["recursive_document_count"], ext["recursive_folder_count"] = value.RecursiveDocumentCount, value.RecursiveFolderCount
			raw, err := json.Marshal(ext)
			if err != nil {
				return err
			}
			if bytes.Equal(before, raw) {
				continue
			}
			result := tx.Model(&orm.Document{}).Where("id = ? AND dataset_id = ? AND resource_version = ? AND deleted_at IS NULL", id, datasetID, row.ResourceVersion).Updates(map[string]any{"ext": raw, "updated_at": time.Now().UTC(), "resource_version": gorm.Expr("resource_version + 1")})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return resourceErr("CONFLICT", "folder changed during statistics refresh")
			}
		}
	}
	return nil
}
