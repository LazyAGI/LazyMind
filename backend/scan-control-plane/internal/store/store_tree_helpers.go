package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/model"
	"github.com/lazymind/scan_control_plane/internal/sourcelayout"
	"gorm.io/gorm"
)

func collectTreeFilePaths(items []model.TreeNode) []string {
	out := make([]string, 0, 64)
	seen := make(map[string]struct{}, 64)
	var walk func(nodes []model.TreeNode)
	walk = func(nodes []model.TreeNode) {
		for _, node := range nodes {
			if node.IsDir {
				if len(node.Children) > 0 {
					walk(node.Children)
				}
				continue
			}
			p := strings.TrimSpace(node.Key)
			if p != "" && !isTransientSourceFilePath(p, false) {
				if _, ok := seen[p]; !ok {
					seen[p] = struct{}{}
					out = append(out, p)
				}
			}
			if len(node.Children) > 0 {
				walk(node.Children)
			}
		}
	}
	walk(items)
	return out
}

func collectAllTreePaths(items []model.TreeNode) []string {
	out := make([]string, 0, 64)
	seen := make(map[string]struct{}, 64)
	var walk func(nodes []model.TreeNode)
	walk = func(nodes []model.TreeNode) {
		for _, node := range nodes {
			p := strings.TrimSpace(node.Key)
			if p != "" {
				p = filepath.Clean(p)
				if p != "" && p != "." {
					if _, ok := seen[p]; !ok {
						seen[p] = struct{}{}
						out = append(out, p)
					}
				}
			}
			if len(node.Children) > 0 {
				walk(node.Children)
			}
		}
	}
	walk(items)
	return out
}

func CollectTreeFilePaths(items []model.TreeNode) []string {
	return collectTreeFilePaths(items)
}

func applySourceDocumentStatesToTree(items []model.TreeNode, states map[string]sourceDocumentStateView) []model.TreeNode {
	out := make([]model.TreeNode, 0, len(items))
	for _, node := range items {
		item := node
		if len(item.Children) > 0 {
			item.Children = applySourceDocumentStatesToTree(item.Children, states)
		}
		if state, ok := states[filepath.Clean(strings.TrimSpace(item.Key))]; ok {
			item = applyStateToTreeNode(item, state)
		}
		out = append(out, item)
	}
	return out
}

func (s *Store) treeDocumentRowsByPath(ctx context.Context, sourceID string, paths []string) (map[string]treeDocumentRow, error) {
	out := make(map[string]treeDocumentRow, len(paths))
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(paths) == 0 {
		return out, nil
	}
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	if len(normalized) == 0 {
		return out, nil
	}
	treeToObject, err := s.cloudTreePathsToObjectPathsIncludingDeleted(ctx, sourceID, normalized, true)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	queryPaths := append([]string(nil), normalized...)
	if len(treeToObject) > 0 {
		for _, objectPath := range treeToObject {
			path := filepath.Clean(strings.TrimSpace(objectPath))
			if path == "" || path == "." {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			queryPaths = append(queryPaths, path)
		}
	}

	var docs []treeDocumentRow
	if err := s.db.WithContext(ctx).
		Table("documents").
		Select("id, source_object_id, desired_version_id, current_version_id, parse_status").
		Where("source_id = ? AND source_object_id IN ?", sourceID, queryPaths).
		Scan(&docs).Error; err != nil {
		return nil, err
	}
	for _, doc := range docs {
		path := filepath.Clean(strings.TrimSpace(doc.SourceObjectID))
		if path == "" || path == "." {
			continue
		}
		out[path] = doc
		for treePath, objectPath := range treeToObject {
			if filepath.Clean(strings.TrimSpace(objectPath)) == path {
				out[treePath] = doc
			}
		}
	}
	return out, nil
}

func (s *Store) latestParseTasksForTreeDocumentRows(ctx context.Context, docs map[string]treeDocumentRow) (map[int64]parseTaskDocJoin, error) {
	docIDs := make([]int64, 0, len(docs))
	seen := make(map[int64]struct{}, len(docs))
	for _, doc := range docs {
		if doc.ID <= 0 {
			continue
		}
		if _, ok := seen[doc.ID]; ok {
			continue
		}
		seen[doc.ID] = struct{}{}
		docIDs = append(docIDs, doc.ID)
	}
	return s.latestParseTasksByDocumentIDs(ctx, docIDs)
}

func applyWatchTreeNodeStates(items []model.TreeNode, diffByPath map[string]string, docMap map[string]treeDocumentRow, queueMap map[int64]parseTaskDocJoin) []model.TreeNode {
	out := make([]model.TreeNode, 0, len(items))
	for _, node := range items {
		item := node
		if item.IsDir {
			item.Children = applyWatchTreeNodeStates(item.Children, diffByPath, docMap, queueMap)
			v := false
			item.Selectable = &v
			item.StatusSource = "UNKNOWN"
			out = append(out, item)
			continue
		}
		if len(item.Children) > 0 {
			item.Children = applyWatchTreeNodeStates(item.Children, diffByPath, docMap, queueMap)
		}
		v := true
		item.Selectable = &v
		path := strings.TrimSpace(item.Key)
		doc, hasDoc := docMap[path]
		latestTask, hasLatestTask := queueMap[doc.ID]
		docUpdate := "UNKNOWN"
		if hasDoc {
			docUpdate = inferDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus)
		}
		updateType := watchTreeNodeUpdateType(path, doc, hasDoc, latestTask, hasLatestTask, diffByPath)
		item.UpdateType = updateType
		item.UpdateDesc = updateTypeDescription(updateType)
		if hasDoc && (documentUpdateShouldWinSnapshot(docUpdate, doc.DesiredVersionID, doc.ParseStatus, latestTask, hasLatestTask) || updateType == docUpdate) {
			item.StatusSource = "DOCUMENTS"
		} else if _, ok := diffByPath[filepath.Clean(strings.TrimSpace(path))]; ok {
			item.StatusSource = "SNAPSHOT"
		} else if hasDoc {
			item.StatusSource = "DOCUMENTS"
		} else {
			item.StatusSource = "UNKNOWN"
		}
		switch updateType {
		case "NEW", "MODIFIED", "DELETED":
			has := true
			item.HasUpdate = &has
		case "UNCHANGED":
			has := false
			item.HasUpdate = &has
		default:
			item.HasUpdate = nil
		}
		if hasDoc && hasLatestTask {
			item.ParseQueueState = effectiveLatestParseTaskState(doc.DesiredVersionID, latestTask)
		}
		out = append(out, item)
	}
	return out
}

func watchTreeNodeUpdateType(path string, doc treeDocumentRow, hasDoc bool, latestTask parseTaskDocJoin, hasLatestTask bool, diffByPath map[string]string) string {
	snapshotUpdate := snapshotUpdateTypeWithDocumentState(diffByPath[filepath.Clean(strings.TrimSpace(path))], doc, hasDoc, latestTask, hasLatestTask)
	docUpdate := "UNKNOWN"
	if hasDoc {
		docUpdate = inferDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus)
		if documentUpdateShouldWinSnapshot(docUpdate, doc.DesiredVersionID, doc.ParseStatus, latestTask, hasLatestTask) {
			return docUpdate
		}
	}
	if snapshotUpdate == "NEW" || snapshotUpdate == "MODIFIED" {
		return snapshotUpdate
	}
	if snapshotUpdate != "" {
		return snapshotUpdate
	}
	return docUpdate
}

func applySnapshotTreeNodeStates(items []model.TreeNode, diffByPath map[string]string, docMap map[string]treeDocumentRow, queueMap map[int64]parseTaskDocJoin) []model.TreeNode {
	out := make([]model.TreeNode, 0, len(items))
	for _, node := range items {
		item := node
		if item.IsDir {
			item.Children = applySnapshotTreeNodeStates(item.Children, diffByPath, docMap, queueMap)
			v := false
			item.Selectable = &v
			item.StatusSource = "UNKNOWN"
			out = append(out, item)
			continue
		}
		if len(item.Children) > 0 {
			item.Children = applySnapshotTreeNodeStates(item.Children, diffByPath, docMap, queueMap)
		}
		v := true
		item.Selectable = &v
		path := strings.TrimSpace(item.Key)
		doc, hasDoc := docMap[path]
		latestTask, hasLatestTask := queueMap[doc.ID]
		docUpdate := "UNKNOWN"
		if hasDoc {
			docUpdate = effectiveDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask)
		}
		updateType := snapshotUpdateTypeWithDocumentState(diffByPath[path], doc, hasDoc, latestTask, hasLatestTask)
		statusSource := "SNAPSHOT"
		if hasDoc && documentUpdateShouldOverrideSnapshotChange(updateType, docUpdate, doc.DesiredVersionID, doc.ParseStatus, latestTask, hasLatestTask) {
			updateType = docUpdate
			statusSource = "DOCUMENTS"
		}
		if updateType == "" {
			updateType = "UNKNOWN"
		}
		item.UpdateType = updateType
		item.UpdateDesc = updateTypeDescription(updateType)
		item.StatusSource = statusSource
		switch updateType {
		case "NEW", "MODIFIED", "DELETED":
			has := true
			item.HasUpdate = &has
		case "UNCHANGED":
			has := false
			item.HasUpdate = &has
		default:
			item.HasUpdate = nil
		}
		if hasDoc && hasLatestTask {
			item.ParseQueueState = effectiveLatestParseTaskState(doc.DesiredVersionID, latestTask)
		}
		out = append(out, item)
	}
	return out
}

func (s *Store) filterPathsByUpdatedOnly(ctx context.Context, sourceID string, paths []string) ([]string, int, error) {
	if len(paths) == 0 {
		return nil, 0, nil
	}
	stateByPath, err := s.sourceDocumentStateByPaths(ctx, sourceID, paths)
	if err != nil {
		return nil, 0, err
	}
	docMap, err := s.treeDocumentRowsByPath(ctx, sourceID, paths)
	if err != nil {
		return nil, 0, err
	}
	queueMap, err := s.latestParseTasksForTreeDocumentRows(ctx, docMap)
	if err != nil {
		return nil, 0, err
	}
	filtered := make([]string, 0, len(paths))
	ignored := 0
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if updateType := pendingSourceStateUpdateType(stateByPath[path]); updateType != "" {
			filtered = append(filtered, path)
			continue
		}
		doc, ok := docMap[path]
		if !ok {
			// No document record yet, treat as NEW.
			filtered = append(filtered, path)
			continue
		}
		latestTask, hasLatestTask := queueMap[doc.ID]
		updateType := effectiveDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask)
		if updateType == "NEW" || updateType == "MODIFIED" || updateType == "DELETED" {
			filtered = append(filtered, path)
			continue
		}
		ignored++
	}
	return filtered, ignored, nil
}

func snapshotUpdateTypeWithDocumentState(rawUpdate string, doc treeDocumentRow, hasDoc bool, latestTask parseTaskDocJoin, hasLatestTask bool) string {
	updateType := normalizeSnapshotUpdateType(rawUpdate)
	if updateType == "NEW" && hasDoc && documentSettledForSnapshotNew(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask) {
		return "UNCHANGED"
	}
	return updateType
}

func filterPathsByDiff(paths []string, diffByPath map[string]string, docMap map[string]treeDocumentRow, queueMap map[int64]parseTaskDocJoin, stateByPath map[string]sourceDocumentStateView, selectionTakenAt time.Time) ([]string, int) {
	filtered := make([]string, 0, len(paths))
	ignored := 0
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if updateType := pendingSourceStateUpdateType(stateByPath[path]); updateType != "" {
			if !selectionTakenAt.IsZero() && stateByPath[path].LastDetectedAt.After(selectionTakenAt.UTC()) {
				filtered = append(filtered, path)
				continue
			}
			switch normalizeSnapshotUpdateType(diffByPath[path]) {
			case "NEW", "MODIFIED", "UNCHANGED":
				filtered = append(filtered, path)
				continue
			}
			filtered = append(filtered, path)
			continue
		}
		doc, hasDoc := docMap[path]
		latestTask, hasLatestTask := queueMap[doc.ID]
		if hasDoc {
			docUpdate := effectiveDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask)
			if documentUpdateShouldWinSnapshot(docUpdate, doc.DesiredVersionID, doc.ParseStatus, latestTask, hasLatestTask) {
				filtered = append(filtered, path)
				continue
			}
		}
		updateType := snapshotUpdateTypeWithDocumentState(diffByPath[path], doc, hasDoc, latestTask, hasLatestTask)
		if updateType == "NEW" || updateType == "MODIFIED" || updateType == "DELETED" {
			filtered = append(filtered, path)
			continue
		}
		ignored++
	}
	return filtered, ignored
}

func effectiveSelectionDiff(paths []string, diffByPath map[string]string, docMap map[string]treeDocumentRow, queueMap map[int64]parseTaskDocJoin, stateByPath map[string]sourceDocumentStateView) map[string]string {
	out := make(map[string]string, len(diffByPath)+len(paths))
	for path, updateType := range diffByPath {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			continue
		}
		out[path] = normalizeSnapshotUpdateType(updateType)
	}
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		doc, hasDoc := docMap[path]
		latestTask, hasLatestTask := queueMap[doc.ID]
		update := snapshotUpdateTypeWithDocumentState(out[path], doc, hasDoc, latestTask, hasLatestTask)
		if hasDoc {
			docUpdate := effectiveDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask)
			if documentUpdateShouldOverrideSnapshotChange(update, docUpdate, doc.DesiredVersionID, doc.ParseStatus, latestTask, hasLatestTask) {
				update = docUpdate
			}
		}
		if update == "" {
			update = "UNKNOWN"
		}
		if sourceUpdate := stateUpdateType(stateByPath[path]); sourceUpdate != "" && sourceStateShouldOverrideUpdate(sourceUpdate, update) {
			update = sourceUpdate
		}
		out[path] = normalizeSnapshotUpdateType(update)
	}
	return normalizeSnapshotUpdateOverrides(out)
}

func documentUpdateShouldOverrideSnapshotChange(snapshotUpdate, docUpdate, desiredVersionID, parseStatus string, latestTask parseTaskDocJoin, hasLatestTask bool) bool {
	if !documentUpdateShouldWinSnapshot(docUpdate, desiredVersionID, parseStatus, latestTask, hasLatestTask) {
		return false
	}
	switch normalizeSnapshotUpdateType(snapshotUpdate) {
	case "NEW", "MODIFIED":
		return strings.EqualFold(strings.TrimSpace(docUpdate), "MODIFIED")
	default:
		return true
	}
}

func (s *Store) deletedDocumentPathSet(ctx context.Context, sourceID string, paths []string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	normalized := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	if len(normalized) == 0 {
		return out, nil
	}
	var rows []struct {
		SourceObjectID string
	}
	query := s.db.WithContext(ctx).
		Table("documents").
		Select("source_object_id").
		Where("source_id = ? AND source_object_id IN ?", sourceID, normalized)
	query = applyPendingDeletedDocumentFilter(query, "parse_status")
	query = applyTransientPathFilter(query, "source_object_id")
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		path := filepath.Clean(strings.TrimSpace(row.SourceObjectID))
		if path == "" || path == "." {
			continue
		}
		out[path] = struct{}{}
	}
	return out, nil
}

func collectDeletedPathsFromDiff(diffByPath map[string]string, currentPaths []string) []string {
	currentSet := make(map[string]struct{}, len(currentPaths))
	for _, path := range currentPaths {
		currentSet[filepath.Clean(strings.TrimSpace(path))] = struct{}{}
	}
	out := make([]string, 0, len(diffByPath))
	for path, state := range diffByPath {
		if strings.ToUpper(strings.TrimSpace(state)) != "DELETED" {
			continue
		}
		cleanPath := filepath.Clean(strings.TrimSpace(path))
		if isTransientSourceFilePath(cleanPath, false) {
			continue
		}
		if _, ok := currentSet[cleanPath]; ok {
			continue
		}
		out = append(out, cleanPath)
	}
	return out
}

func resolveCloudObjectLocalPath(rootPath string, row cloudObjectIndexEntity) string {
	if raw := strings.TrimSpace(row.LocalAbsPath); raw != "" {
		clean := filepath.Clean(raw)
		if clean != "" && clean != "." {
			return clean
		}
	}

	relative := strings.TrimSpace(row.LocalRelPath)
	if relative == "" {
		relative = strings.TrimSpace(row.ExternalPath)
	}
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		return ""
	}
	if relative == "" {
		return rootPath
	}
	relative = filepath.Clean(relative)
	if relative == "." || relative == string(filepath.Separator) {
		return rootPath
	}
	relative = strings.TrimPrefix(relative, string(filepath.Separator))
	if relative == "" || relative == "." {
		return rootPath
	}
	return filepath.Clean(filepath.Join(rootPath, relative))
}

func cloudObjectIsDirectory(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "folder", "directory", "dir", "wiki", "space":
		return true
	default:
		return false
	}
}

func cloudObjectDisplayTitle(path string, row cloudObjectIndexEntity, isDir bool) string {
	title := strings.TrimSpace(row.ExternalName)
	if !isDir {
		base := strings.TrimSpace(filepath.Base(filepath.Clean(path)))
		parent := strings.TrimSpace(filepath.Base(filepath.Dir(filepath.Clean(path))))
		if title == "" && base != "" && base != "." && base != string(filepath.Separator) {
			title = base
		} else if base != "" && strings.EqualFold(parent, title) {
			title = base
		}
	}
	if title == "" {
		title = nodeTitleFromPath(path)
	}
	return title
}

func cloudObjectProviderBool(row cloudObjectIndexEntity, key string) bool {
	meta := decodeMapJSON(row.ProviderMetaJSON)
	if meta == nil {
		return false
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y":
			return true
		default:
			return false
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	default:
		return strings.EqualFold(strings.TrimSpace(fmt.Sprintf("%v", raw)), "true")
	}
}

func cloudObjectWikiPageWithChildren(row cloudObjectIndexEntity) bool {
	switch strings.ToLower(strings.TrimSpace(row.ExternalKind)) {
	case "doc", "docx":
	default:
		return false
	}
	return cloudObjectProviderBool(row, "has_child")
}

func cloudObjectWikiPage(row cloudObjectIndexEntity) bool {
	switch strings.ToLower(strings.TrimSpace(row.ExternalKind)) {
	case "doc", "docx":
	default:
		return false
	}
	if cloudObjectWikiPageWithChildren(row) {
		return true
	}
	meta := decodeMapJSON(row.ProviderMetaJSON)
	for _, key := range []string{"node_token", "space_id", "obj_token", "obj_type", "wiki_token"} {
		if strings.TrimSpace(fmt.Sprintf("%v", meta[key])) != "" && strings.TrimSpace(fmt.Sprintf("%v", meta[key])) != "<nil>" {
			return true
		}
	}
	return false
}

func cloudObjectWikiPageShouldFold(objectPath string, row cloudObjectIndexEntity) bool {
	if !cloudObjectWikiPage(row) {
		return false
	}
	if cloudObjectWikiPageWithChildren(row) {
		return true
	}
	cleanPath := filepath.Clean(strings.TrimSpace(objectPath))
	base := filepath.Base(cleanPath)
	parent := filepath.Base(filepath.Dir(cleanPath))
	if base == "." || parent == "." || parent == string(filepath.Separator) {
		return false
	}
	return strings.EqualFold(strings.TrimSuffix(base, filepath.Ext(base)), parent)
}

func cloudObjectTreePath(objectPath string, row cloudObjectIndexEntity) string {
	objectPath = filepath.Clean(strings.TrimSpace(objectPath))
	if objectPath == "" || objectPath == "." {
		return objectPath
	}
	if !cloudObjectWikiPageShouldFold(objectPath, row) {
		return objectPath
	}
	parent := filepath.Clean(filepath.Dir(objectPath))
	if parent == "" || parent == "." {
		return objectPath
	}
	return parent
}

func cloudTreePathToObjectPath(ctx context.Context, s *Store, sourceID, treePath, mirrorRoot string) (string, error) {
	return cloudTreePathToObjectPathIncludingDeleted(ctx, s, sourceID, treePath, mirrorRoot, false)
}

type cloudTreeObjectRef struct {
	ObjectPath       string
	ExternalObjectID string
}

func cloudTreePathToObjectPathIncludingDeleted(ctx context.Context, s *Store, sourceID, treePath, mirrorRoot string, includeDeleted bool) (string, error) {
	ref, err := cloudTreePathToObjectRefIncludingDeleted(ctx, s, sourceID, treePath, mirrorRoot, includeDeleted)
	if err != nil {
		return "", err
	}
	return ref.ObjectPath, nil
}

func cloudTreePathToObjectRefIncludingDeleted(ctx context.Context, s *Store, sourceID, treePath, mirrorRoot string, includeDeleted bool) (cloudTreeObjectRef, error) {
	if s == nil {
		return cloudTreeObjectRef{}, nil
	}
	sourceID = strings.TrimSpace(sourceID)
	treePath = filepath.Clean(strings.TrimSpace(treePath))
	mirrorRoot = filepath.Clean(strings.TrimSpace(mirrorRoot))
	if sourceID == "" || treePath == "" || treePath == "." || mirrorRoot == "" || mirrorRoot == "." {
		return cloudTreeObjectRef{}, nil
	}
	var rows []cloudObjectIndexEntity
	query := s.db.WithContext(ctx).Where("source_id = ?", sourceID)
	if !includeDeleted {
		query = query.Where("is_deleted = ?", false)
	}
	if err := query.Find(&rows).Error; err != nil {
		return cloudTreeObjectRef{}, err
	}
	bestScore := -1
	var best cloudTreeObjectRef
	for _, row := range rows {
		objectPath := resolveCloudObjectLocalPath(mirrorRoot, row)
		if objectPath == "" {
			continue
		}
		objectPath = filepath.Clean(objectPath)
		if cloudObjectTreePath(objectPath, row) == treePath {
			score := cloudTreeObjectRefScore(treePath, objectPath, row)
			if score <= bestScore {
				continue
			}
			bestScore = score
			best = cloudTreeObjectRef{
				ObjectPath:       objectPath,
				ExternalObjectID: strings.TrimSpace(row.ExternalObjectID),
			}
		}
	}
	return best, nil
}

func cloudTreeObjectRefScore(treePath, objectPath string, row cloudObjectIndexEntity) int {
	score := 0
	if !cloudObjectIsDirectory(row.ExternalKind) {
		score += 100
	}
	if cloudObjectWikiPageShouldFold(objectPath, row) {
		score += 20
	}
	if filepath.Clean(strings.TrimSpace(objectPath)) != filepath.Clean(strings.TrimSpace(treePath)) {
		score += 10
	}
	if strings.TrimSpace(row.ExternalObjectID) != "" {
		score++
	}
	return score
}

func (s *Store) cloudTreePathsToObjectPaths(ctx context.Context, sourceID string, paths []string) (map[string]string, error) {
	return s.cloudTreePathsToObjectPathsIncludingDeleted(ctx, sourceID, paths, false)
}

func (s *Store) cloudTreePathsToObjectPathsIncludingDeleted(ctx context.Context, sourceID string, paths []string, includeDeleted bool) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(paths) == 0 {
		return out, nil
	}
	var src sourceEntity
	if err := s.db.WithContext(ctx).Take(&src, "id = ?", sourceID).Error; err != nil {
		return nil, err
	}
	rootPath := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	if rootPath == "" || rootPath == "." {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		wanted[path] = struct{}{}
	}
	if len(wanted) == 0 {
		return out, nil
	}
	for treePath := range wanted {
		objectPath, err := cloudTreePathToObjectPathIncludingDeleted(ctx, s, sourceID, treePath, rootPath, includeDeleted)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(objectPath) != "" {
			out[treePath] = objectPath
		}
	}
	return out, nil
}

func (s *Store) cloudTreePathsToObjectRefsIncludingDeleted(ctx context.Context, sourceID string, paths []string, includeDeleted bool) (map[string]cloudTreeObjectRef, error) {
	out := make(map[string]cloudTreeObjectRef, len(paths))
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(paths) == 0 {
		return out, nil
	}
	var src sourceEntity
	if err := s.db.WithContext(ctx).Take(&src, "id = ?", sourceID).Error; err != nil {
		return nil, err
	}
	rootPath := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	if rootPath == "" || rootPath == "." {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		wanted[path] = struct{}{}
	}
	for treePath := range wanted {
		ref, err := cloudTreePathToObjectRefIncludingDeleted(ctx, s, sourceID, treePath, rootPath, includeDeleted)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(ref.ObjectPath) != "" {
			out[treePath] = ref
		}
	}
	return out, nil
}

func (s *Store) cloudObjectPathsToTreePathsIncludingDeleted(ctx context.Context, sourceID string, paths []string, includeDeleted bool) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(paths) == 0 {
		return out, nil
	}
	var src sourceEntity
	if err := s.db.WithContext(ctx).Take(&src, "id = ?", sourceID).Error; err != nil {
		return nil, err
	}
	rootPath := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	if rootPath == "" || rootPath == "." {
		return out, nil
	}
	wanted := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		wanted[path] = struct{}{}
	}
	if len(wanted) == 0 {
		return out, nil
	}
	var rows []cloudObjectIndexEntity
	query := s.db.WithContext(ctx).Where("source_id = ?", sourceID)
	if !includeDeleted {
		query = query.Where("is_deleted = ?", false)
	}
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		objectPath := resolveCloudObjectLocalPath(rootPath, row)
		if objectPath == "" {
			continue
		}
		objectPath = filepath.Clean(objectPath)
		if _, ok := wanted[objectPath]; !ok {
			continue
		}
		treePath := filepath.Clean(cloudObjectTreePath(objectPath, row))
		if treePath == "" || treePath == "." {
			continue
		}
		out[objectPath] = treePath
	}
	return out, nil
}

func treeRelativeDepth(rootPath, targetPath string) int {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	targetPath = filepath.Clean(strings.TrimSpace(targetPath))
	if rootPath == "" || targetPath == "" || rootPath == "." || targetPath == "." {
		return -1
	}
	rel, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return -1
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return 0
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return -1
	}
	parts := strings.Split(rel, string(filepath.Separator))
	depth := 0
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." {
			continue
		}
		depth++
	}
	return depth
}

func ensureCloudAncestorNodes(nodeMap map[string]*model.TreeNode, childMap map[string]map[string]struct{}, rootPath, targetPath string, maxDepth int) {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	targetPath = filepath.Clean(strings.TrimSpace(targetPath))
	if rootPath == "" || targetPath == "" || rootPath == "." || targetPath == "." {
		return
	}
	rel, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) <= 1 {
		return
	}
	maxAncestorDepth := len(parts) - 1
	if maxAncestorDepth > maxDepth {
		maxAncestorDepth = maxDepth
	}
	current := rootPath
	for i := 0; i < maxAncestorDepth; i++ {
		part := strings.TrimSpace(parts[i])
		if part == "" || part == "." {
			continue
		}
		current = filepath.Clean(filepath.Join(current, part))
		ensureCloudNode(nodeMap, childMap, current, true, part, "")
	}
}

func ensureCloudNode(nodeMap map[string]*model.TreeNode, childMap map[string]map[string]struct{}, path string, isDir bool, title, externalFileID string) {
	ensureCloudNodeWithParent(nodeMap, childMap, path, "", isDir, title, externalFileID)
}

func ensureCloudNodeWithParent(nodeMap map[string]*model.TreeNode, childMap map[string]map[string]struct{}, path, parentPath string, isDir bool, title, externalFileID string) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = nodeTitleFromPath(path)
	}
	node, ok := nodeMap[path]
	if !ok {
		node = &model.TreeNode{
			Title: title,
			Key:   path,
			IsDir: isDir,
		}
		if !isDir {
			node.ExternalFileID = strings.TrimSpace(externalFileID)
		}
		nodeMap[path] = node
	} else {
		if !isDir {
			node.IsDir = false
			if strings.TrimSpace(externalFileID) != "" {
				node.ExternalFileID = strings.TrimSpace(externalFileID)
			}
		} else if node.IsDir {
			node.IsDir = true
			node.ExternalFileID = ""
		}
		if strings.TrimSpace(node.Title) == "" || node.Title == nodeTitleFromPath(path) {
			node.Title = title
		}
	}
	parent := filepath.Clean(strings.TrimSpace(parentPath))
	if parent == "" || parent == "." {
		parent = filepath.Clean(filepath.Dir(path))
	}
	if parent == "" || parent == "." {
		parent = string(filepath.Separator)
	}
	if _, ok := childMap[parent]; !ok {
		childMap[parent] = make(map[string]struct{}, 4)
	}
	childMap[parent][path] = struct{}{}
}

func buildCloudTreeNodes(rootPath string, nodeMap map[string]*model.TreeNode, childMap map[string]map[string]struct{}) []model.TreeNode {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		rootPath = string(filepath.Separator)
	}
	var walk func(parent string) []model.TreeNode
	walk = func(parent string) []model.TreeNode {
		childrenSet, ok := childMap[parent]
		if !ok || len(childrenSet) == 0 {
			return nil
		}
		keys := make([]string, 0, len(childrenSet))
		for key := range childrenSet {
			if _, exists := nodeMap[key]; !exists {
				continue
			}
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			left := nodeMap[keys[i]]
			right := nodeMap[keys[j]]
			if left.IsDir != right.IsDir {
				return left.IsDir
			}
			leftTitle := strings.ToLower(strings.TrimSpace(left.Title))
			rightTitle := strings.ToLower(strings.TrimSpace(right.Title))
			if leftTitle == rightTitle {
				return left.Key < right.Key
			}
			return leftTitle < rightTitle
		})
		out := make([]model.TreeNode, 0, len(keys))
		for _, key := range keys {
			base := nodeMap[key]
			if base == nil {
				continue
			}
			item := *base
			if len(childMap[item.Key]) > 0 {
				item.Children = walk(item.Key)
			}
			out = append(out, item)
		}
		return out
	}
	return walk(rootPath)
}

func addDeletedNodes(items []model.TreeNode, deletedPaths []string, rootPath, statusSource string, docMap map[string]treeDocumentRow, queueMap map[int64]parseTaskDocJoin, titleByPath map[string]string) []model.TreeNode {
	for _, path := range deletedPaths {
		items = insertDeletedNode(items, path, rootPath, statusSource, docMap, queueMap, titleByPath)
	}
	return items
}

func insertDeletedNode(nodes []model.TreeNode, filePath, rootPath, statusSource string, docMap map[string]treeDocumentRow, queueMap map[int64]parseTaskDocJoin, titleByPath map[string]string) []model.TreeNode {
	filePath = filepath.Clean(strings.TrimSpace(filePath))
	if filePath == "" || filePath == "." {
		return nodes
	}
	if findNodeByKey(nodes, filePath) >= 0 {
		return nodes
	}
	ancestors := buildAncestorPaths(filePath, rootPath)
	return ensureDeletedAtPath(nodes, ancestors, filePath, statusSource, docMap, queueMap, titleByPath)
}

func ensureDeletedAtPath(nodes []model.TreeNode, ancestors []string, filePath, statusSource string, docMap map[string]treeDocumentRow, queueMap map[int64]parseTaskDocJoin, titleByPath map[string]string) []model.TreeNode {
	if len(ancestors) == 0 {
		if findNodeByKey(nodes, filePath) >= 0 {
			return nodes
		}
		hasUpdate := true
		selectable := true
		node := model.TreeNode{
			Title:        treeTitleForPath(filePath, titleByPath),
			Key:          filePath,
			IsDir:        false,
			HasUpdate:    &hasUpdate,
			UpdateType:   "DELETED",
			UpdateDesc:   updateTypeDescription("DELETED"),
			Selectable:   &selectable,
			StatusSource: statusSource,
		}
		if doc, ok := docMap[filePath]; ok {
			if queue, ok := queueMap[doc.ID]; ok {
				node.ParseQueueState = effectiveLatestParseTaskState(doc.DesiredVersionID, queue)
			}
		}
		return append(nodes, node)
	}
	dirPath := ancestors[0]
	idx := findContainerNodeByKey(nodes, dirPath)
	if idx < 0 {
		selectable := false
		nodes = append(nodes, model.TreeNode{
			Title:        treeTitleForPath(dirPath, titleByPath),
			Key:          dirPath,
			IsDir:        true,
			UpdateType:   "UNCHANGED",
			UpdateDesc:   updateTypeDescription("UNCHANGED"),
			Selectable:   &selectable,
			StatusSource: "UNKNOWN",
		})
		idx = len(nodes) - 1
	}
	child := nodes[idx]
	if strings.TrimSpace(child.UpdateType) == "" || strings.EqualFold(strings.TrimSpace(child.UpdateType), "UNKNOWN") {
		child.UpdateType = "UNCHANGED"
		child.UpdateDesc = updateTypeDescription("UNCHANGED")
		if child.StatusSource == "" {
			child.StatusSource = "UNKNOWN"
		}
	}
	child.Children = ensureDeletedAtPath(child.Children, ancestors[1:], filePath, statusSource, docMap, queueMap, titleByPath)
	nodes[idx] = child
	return nodes
}

func treeTitleForPath(path string, titleByPath map[string]string) string {
	clean := filepath.Clean(strings.TrimSpace(path))
	if title := strings.TrimSpace(titleByPath[clean]); title != "" {
		return title
	}
	return nodeTitleFromPath(clean)
}

func buildAncestorPaths(filePath, rootPath string) []string {
	filePath = filepath.Clean(strings.TrimSpace(filePath))
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	dirPath := filepath.Clean(filepath.Dir(filePath))
	if dirPath == "." || dirPath == filePath {
		return nil
	}
	if rootPath == "" || rootPath == "." {
		return []string{dirPath}
	}
	if dirPath != rootPath && !strings.HasPrefix(dirPath, rootPath+string(filepath.Separator)) {
		return []string{dirPath}
	}
	if dirPath == rootPath {
		return nil
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(dirPath, rootPath), string(filepath.Separator))
	parts := strings.Split(rel, string(filepath.Separator))
	out := make([]string, 0, len(parts))
	cur := rootPath
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		out = append(out, cur)
	}
	return out
}

func findDirNodeByKey(nodes []model.TreeNode, key string) int {
	for i := range nodes {
		if nodes[i].Key == key && nodes[i].IsDir {
			return i
		}
	}
	return -1
}

func findContainerNodeByKey(nodes []model.TreeNode, key string) int {
	for i := range nodes {
		if nodes[i].Key == key {
			return i
		}
	}
	return -1
}

func findNodeByKey(nodes []model.TreeNode, key string) int {
	for i := range nodes {
		if nodes[i].Key == key {
			return i
		}
	}
	return -1
}

func nodeTitleFromPath(path string) string {
	name := filepath.Base(strings.TrimSpace(path))
	if name == "." || name == "/" || name == string(filepath.Separator) {
		return path
	}
	return name
}

func collectTreeScopeRoots(items []model.TreeNode) []string {
	dirRoots := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	fileParentRoots := make([]string, 0, len(items))
	for _, item := range items {
		key := filepath.Clean(strings.TrimSpace(item.Key))
		if key == "" || key == "." {
			continue
		}
		if item.IsDir {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			dirRoots = append(dirRoots, key)
			continue
		}
		if len(item.Children) > 0 {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			dirRoots = append(dirRoots, key)
			continue
		}
		parent := filepath.Clean(filepath.Dir(key))
		if parent == "" || parent == "." {
			continue
		}
		fileParentRoots = append(fileParentRoots, parent)
	}
	if len(fileParentRoots) > 0 {
		candidates := make([]string, 0, len(fileParentRoots)+len(dirRoots))
		candidates = append(candidates, fileParentRoots...)
		for _, dirRoot := range dirRoots {
			parent := filepath.Clean(filepath.Dir(dirRoot))
			if parent == "" || parent == "." {
				continue
			}
			candidates = append(candidates, parent)
		}
		if common := commonScopeRoot(candidates); common != "" {
			return []string{common}
		}
	}
	if len(dirRoots) > 0 {
		return dirRoots
	}
	return nil
}

func commonScopeRoot(paths []string) string {
	common := ""
	for _, raw := range paths {
		path := filepath.Clean(strings.TrimSpace(raw))
		if path == "" || path == "." {
			continue
		}
		if common == "" {
			common = path
			continue
		}
		for common != "" && common != "." && common != string(filepath.Separator) && !pathInScope(path, []string{common}) {
			parent := filepath.Clean(filepath.Dir(common))
			if parent == common {
				break
			}
			common = parent
		}
	}
	if common == "" || common == "." {
		return ""
	}
	return common
}

func pathInScope(path string, roots []string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return false
	}
	if len(roots) == 0 {
		return true
	}
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" || root == "." {
			continue
		}
		if root == string(filepath.Separator) {
			if strings.HasPrefix(path, string(filepath.Separator)) {
				return true
			}
			continue
		}
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (s *Store) deletedDocumentPaths(ctx context.Context, sourceID string, scopeRoots []string, currentPaths []string) ([]string, error) {
	var rows []struct {
		SourceObjectID string
	}
	currentSet := make(map[string]struct{}, len(currentPaths))
	for _, path := range currentPaths {
		currentSet[filepath.Clean(strings.TrimSpace(path))] = struct{}{}
	}
	query := s.db.WithContext(ctx).
		Table("documents").
		Select("source_object_id").
		Where("source_id = ?", sourceID)
	query = applyPendingDeletedDocumentFilter(query, "parse_status")
	query = applyTransientPathFilter(query, "source_object_id")
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		path := filepath.Clean(strings.TrimSpace(row.SourceObjectID))
		if path == "" || path == "." {
			continue
		}
		if len(scopeRoots) > 0 && !pathInScope(path, scopeRoots) {
			continue
		}
		if _, ok := currentSet[path]; ok {
			continue
		}
		out = append(out, path)
	}
	return out, nil
}

func (s *Store) missingDocumentPaths(ctx context.Context, sourceID string, scopeRoots []string, currentPaths []string) ([]string, error) {
	var rows []struct {
		SourceObjectID string
	}
	query := s.db.WithContext(ctx).
		Table("documents").
		Select("source_object_id").
		Where("source_id = ?", sourceID)
	var src sourceEntity
	if err := s.db.WithContext(ctx).Take(&src, "id = ?", strings.TrimSpace(sourceID)).Error; err == nil && sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		query = query.Where("UPPER(COALESCE(parse_status, '')) <> ?", "DELETED").
			Where("(COALESCE(current_version_id, '') <> '' OR COALESCE(core_document_id, '') <> '')")
	} else {
		query = query.
			Where("UPPER(COALESCE(parse_status, '')) IN ?", []string{"PENDING", "QUEUED", "RUNNING"}).
			Where("COALESCE(current_version_id, '') = '' AND COALESCE(core_document_id, '') = ''")
	}
	query = applyTransientPathFilter(query, "source_object_id")
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	currentSet := make(map[string]struct{}, len(currentPaths))
	for _, path := range currentPaths {
		currentSet[filepath.Clean(strings.TrimSpace(path))] = struct{}{}
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		path := filepath.Clean(strings.TrimSpace(row.SourceObjectID))
		if path == "" || path == "." {
			continue
		}
		if len(scopeRoots) > 0 && !pathInScope(path, scopeRoots) {
			continue
		}
		if _, ok := currentSet[path]; ok {
			continue
		}
		out = append(out, path)
	}
	return out, nil
}
