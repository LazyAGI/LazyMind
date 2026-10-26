package paperlibrary

import (
	"errors"
	"strings"
)

// ValidateRelations checks the active graph. Orphaned historical attachments
// may remain referenced by annotations and archived papers after removal.
func ValidateRelations(graph Graph) error {
	folders := map[string]Entity{}
	names := map[string]bool{}
	for _, folder := range Objects(graph, "folders") {
		id, parent, name := Text(folder, "id"), Text(folder, "parentId"), Text(folder, "name")
		if raw, exists := folder["parentId"]; exists && raw != nil {
			if _, ok := raw.(string); !ok {
				return errors.New("目录 parentId 无效")
			}
		}
		if name == "" {
			return errors.New("目录名称不能为空")
		}
		key := parent + "\x00" + strings.ToLower(name)
		if names[key] {
			return errors.New("同一目录下不能存在同名目录")
		}
		names[key] = true
		folders[id] = folder
	}
	for id, folder := range folders {
		seen := map[string]bool{id: true}
		for parent := Text(folder, "parentId"); parent != ""; {
			ancestor, exists := folders[parent]
			if !exists {
				return errors.New("上级目录不存在")
			}
			if seen[parent] {
				return errors.New("目录层级不能包含循环")
			}
			seen[parent] = true
			parent = Text(ancestor, "parentId")
		}
	}
	attachments := map[string]Entity{}
	for _, attachment := range Objects(graph, "attachments") {
		attachments[Text(attachment, "id")] = attachment
	}
	documentOwner := map[string]string{}
	for _, paper := range Objects(graph, "papers") {
		id := Text(paper, "id")
		if folder := Text(paper, "folderId"); folder != "" && folders[folder] == nil {
			return errors.New("论文所属目录不存在")
		}
		seen := map[string]bool{}
		for _, attachmentID := range StringList(paper["attachmentIds"]) {
			attachment := attachments[attachmentID]
			if seen[attachmentID] || attachment == nil || Text(attachment, "paperId") != id {
				return errors.New("论文附件引用无效")
			}
			seen[attachmentID] = true
			if document := Text(attachment, "contentRef"); document != "" {
				if owner := documentOwner[document]; owner != "" && owner != id {
					return errors.New("同一 PDF 不能关联到多篇论文")
				}
				documentOwner[document] = id
			}
		}
	}
	return nil
}
