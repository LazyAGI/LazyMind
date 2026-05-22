package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/lazymind/scan_control_plane/internal/model"
	"github.com/lazymind/scan_control_plane/internal/sourcelayout"
)

func (s *Store) ListSourceDocuments(ctx context.Context, sourceID string, req model.ListSourceDocumentsRequest) (model.SourceDocumentsResponse, error) {
	resp := model.SourceDocumentsResponse{
		Items: []model.SourceDocumentItem{},
	}
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		return resp, fmt.Errorf("tenant_id is required")
	}
	var src sourceEntity
	if err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", strings.TrimSpace(sourceID), tenantID).
		Take(&src).Error; err != nil {
		return resp, err
	}

	page, pageSize := normalizePageAndSize(req.Page, req.PageSize)
	resp.Page = page
	resp.PageSize = pageSize

	docQuery := s.db.WithContext(ctx).
		Model(&documentEntity{}).
		Where("tenant_id = ? AND source_id = ?", tenantID, src.ID)
	docQuery = applyTransientPathFilter(docQuery, "source_object_id")
	docQuery = applyVisibleDocumentFilter(docQuery, "parse_status")
	docQuery = applySourceDocumentListVisibilityFilter(docQuery, "current_version_id", "core_document_id")

	keyword := strings.TrimSpace(req.Keyword)
	if keyword != "" {
		pattern := "%" + keyword + "%"
		if s.db.Dialector.Name() == "postgres" {
			docQuery = docQuery.Where("source_object_id ILIKE ?", pattern)
		} else {
			docQuery = docQuery.Where("LOWER(source_object_id) LIKE ?", strings.ToLower(pattern))
		}
	}

	if parseStates := splitCSV(req.ParseState); len(parseStates) > 0 {
		docQuery = docQuery.Where("parse_status IN ?", parseStates)
	}

	updateType := normalizeUpdateTypeFilter(req.UpdateType)
	if updateType == "" {
		if err := docQuery.Count(&resp.Total).Error; err != nil {
			return resp, err
		}
	}

	snapshotUpdates, snapshotUpdatesAvailable, err := s.sourceDocumentUpdateOverrides(ctx, src)
	if err != nil {
		return resp, err
	}

	offset := (page - 1) * pageSize
	var docs []documentEntity
	docQuery = docQuery.Order("updated_at DESC, id DESC")
	if updateType == "" {
		docQuery = docQuery.Offset(offset).Limit(pageSize)
	}
	if err := docQuery.Find(&docs).Error; err != nil {
		return resp, err
	}
	stateRows, err := s.sourceDocumentStatesForSource(ctx, src.ID)
	if err != nil {
		return resp, err
	}
	statesByPath := make(map[string]sourceDocumentStateView, len(stateRows))
	for _, row := range stateRows {
		statesByPath[filepath.Clean(strings.TrimSpace(row.Path))] = sourceDocumentStateViewFromEntity(row)
	}

	docIDs := make([]int64, 0, len(docs))
	for _, doc := range docs {
		docIDs = append(docIDs, doc.ID)
	}
	latestTasksByDocID, err := s.latestParseTasksByDocumentIDs(ctx, docIDs)
	if err != nil {
		return resp, err
	}
	metadata, err := s.sourceDocumentDisplayMetadata(ctx, src, sourceDocumentMetaInputsFromEntities(docs), latestTasksByDocID)
	if err != nil {
		return resp, err
	}

	for _, doc := range docs {
		latestTask := latestTasksByDocID[doc.ID]
		_, hasLatestTask := latestTasksByDocID[doc.ID]
		update := effectiveDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask)
		update = documentUpdateTypeWithSnapshotOverride(doc.SourceObjectID, update, doc.DesiredVersionID, doc.ParseStatus, latestTask, hasLatestTask, snapshotUpdates, snapshotUpdatesAvailable)
		var hasUpdate *bool
		switch update {
		case "NEW", "MODIFIED", "DELETED":
			v := true
			hasUpdate = &v
		case "UNCHANGED":
			v := false
			hasUpdate = &v
		}
		displayMeta := metadata[doc.ID]
		knowledgeBasePresent := sourceDocumentHasKnowledgeBaseRelation(doc.CurrentVersionID, doc.CoreDocumentID)
		item := model.SourceDocumentItem{
			DocumentID:              doc.ID,
			SourceCreateUserID:      strings.TrimSpace(src.CreateUserID),
			Name:                    filepath.Base(doc.SourceObjectID),
			Path:                    doc.SourceObjectID,
			Directory:               filepath.Base(filepath.Dir(doc.SourceObjectID)),
			HasUpdate:               hasUpdate,
			UpdateType:              update,
			UpdateDesc:              updateTypeDescription(update),
			ParseState:              effectiveSourceDocumentParseState(doc.ParseStatus, doc.DesiredVersionID, latestTask),
			FileType:                fileTypeFromPath(doc.SourceObjectID),
			SizeBytes:               displayMeta.SizeBytes,
			SourceUpdatedAt:         displayMeta.SourceUpdatedAt,
			LastSyncedAt:            displayMeta.LastSyncedAt,
			CoreDatasetID:           latestTask.CoreDatasetID,
			CoreTaskID:              latestTask.CoreTaskID,
			ScanOrchestrationStatus: latestTask.ScanOrchestrationStatus,
			DesiredVersionID:        doc.DesiredVersionID,
			CurrentVersionID:        doc.CurrentVersionID,
			ParseTaskID:             latestTask.TaskID,
			ParseTaskAction:         latestTask.TaskAction,
			ParseTaskTargetVersion:  latestTask.TargetVersionID,
			KnowledgeBasePresent:    &knowledgeBasePresent,
		}
		if state, ok := statesByPath[filepath.Clean(strings.TrimSpace(doc.SourceObjectID))]; ok {
			item = applyStateToDocumentItem(item, state)
		}
		resp.Items = append(resp.Items, item)
	}
	existingItemPaths := make(map[string]struct{}, len(resp.Items))
	for _, item := range resp.Items {
		existingItemPaths[filepath.Clean(strings.TrimSpace(item.Path))] = struct{}{}
	}
	for _, row := range stateRows {
		state := sourceDocumentStateViewFromEntity(row)
		path := filepath.Clean(strings.TrimSpace(state.Path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := existingItemPaths[path]; ok {
			continue
		}
		if !sourceDocumentStateVisibleInSourceDocuments(state) {
			continue
		}
		if updateType := stateUpdateType(state); updateType != "NEW" && updateType != "DELETED" && updateType != "MODIFIED" {
			continue
		}
		resp.Items = append(resp.Items, sourceStateOnlyDocumentItem(src, state))
	}
	if updateType != "" {
		filtered := make([]model.SourceDocumentItem, 0, len(resp.Items))
		for _, item := range resp.Items {
			if normalizeUpdateTypeFilter(item.UpdateType) == updateType {
				filtered = append(filtered, item)
			}
		}
		resp.Total = int64(len(filtered))
		start := offset
		if start > len(filtered) {
			start = len(filtered)
		}
		end := start + pageSize
		if end > len(filtered) {
			end = len(filtered)
		}
		resp.Items = filtered[start:end]
	}

	summary, latest, err := s.buildSourceDocumentsSummary(ctx, src, snapshotUpdates, snapshotUpdatesAvailable)
	if err != nil {
		return resp, err
	}

	agentOnline := false
	if strings.TrimSpace(src.AgentID) != "" {
		var agent agentEntity
		if err := s.db.WithContext(ctx).Take(&agent, "agent_id = ?", src.AgentID).Error; err == nil {
			agentOnline = strings.ToUpper(strings.TrimSpace(agent.Status)) != "OFFLINE"
		}
	}

	resp.Source = model.SourceDocumentsSource{
		ID:                      src.ID,
		Name:                    src.Name,
		RootPath:                src.RootPath,
		WatchEnabled:            src.WatchEnabled,
		AgentID:                 src.AgentID,
		AgentOnline:             agentOnline,
		UpdateTrackingSupported: true,
		LastSyncedAt:            latest,
	}
	resp.Summary = summary
	if updateType == "" {
		resp.Total = resp.Summary.TotalDocumentCount
	}
	return resp, nil
}

type sourceDocumentSummaryDoc struct {
	DocumentID       int64
	SourceObjectID   string
	ParseStatus      string
	DesiredVersionID string
	CurrentVersionID string
	CoreDocumentID   string
	LastModifiedAt   *time.Time
	OriginType       string
	OriginPlatform   string
	OriginRef        string
	UpdatedAt        time.Time
}

func (s *Store) buildSourceDocumentsSummary(ctx context.Context, src sourceEntity, snapshotUpdates map[string]string, snapshotUpdatesAvailable bool) (model.SourceDocumentsSummary, *time.Time, error) {
	if snapshotUpdates == nil && !snapshotUpdatesAvailable {
		updates, available, err := s.sourceDocumentUpdateOverrides(ctx, src)
		if err != nil {
			return model.SourceDocumentsSummary{}, nil, err
		}
		snapshotUpdates = updates
		snapshotUpdatesAvailable = available
	}

	var summaryDocs []sourceDocumentSummaryDoc
	if err := s.db.WithContext(ctx).
		Table("documents").
		Select("id AS document_id, source_object_id, parse_status, desired_version_id, current_version_id, core_document_id, last_modified_at, origin_type, origin_platform, origin_ref, updated_at").
		Where("tenant_id = ? AND source_id = ?", src.TenantID, src.ID).
		Scopes(func(db *gorm.DB) *gorm.DB {
			db = applyTransientPathFilter(db, "source_object_id")
			db = applyVisibleDocumentFilter(db, "parse_status")
			return applySourceDocumentListVisibilityFilter(db, "current_version_id", "core_document_id")
		}).
		Scan(&summaryDocs).Error; err != nil {
		return model.SourceDocumentsSummary{}, nil, err
	}

	var (
		parsedCount int64
		newCount    int64
		modCount    int64
		delCount    int64
		latest      *time.Time
		storage     int64
	)
	summaryInputs := make([]sourceDocumentMetaInput, 0, len(summaryDocs))
	summaryDocIDs := make([]int64, 0, len(summaryDocs))
	for _, doc := range summaryDocs {
		summaryInputs = append(summaryInputs, sourceDocumentMetaInput{
			DocumentID:     doc.DocumentID,
			SourceObjectID: doc.SourceObjectID,
			LastModifiedAt: doc.LastModifiedAt,
			UpdatedAt:      doc.UpdatedAt,
			OriginType:     doc.OriginType,
			OriginPlatform: doc.OriginPlatform,
			OriginRef:      doc.OriginRef,
		})
		summaryDocIDs = append(summaryDocIDs, doc.DocumentID)
	}
	summaryLatestTasks, err := s.latestParseTasksByDocumentIDs(ctx, summaryDocIDs)
	if err != nil {
		return model.SourceDocumentsSummary{}, nil, err
	}
	summaryMetadata, err := s.sourceDocumentDisplayMetadata(ctx, src, summaryInputs, summaryLatestTasks)
	if err != nil {
		return model.SourceDocumentsSummary{}, nil, err
	}
	for _, meta := range summaryMetadata {
		storage += meta.SizeBytes
		if meta.LastSyncedAt != nil && (latest == nil || meta.LastSyncedAt.After(*latest)) {
			t := meta.LastSyncedAt.UTC()
			latest = &t
		}
	}
	for _, doc := range summaryDocs {
		latestTask := summaryLatestTasks[doc.DocumentID]
		_, hasLatestTask := summaryLatestTasks[doc.DocumentID]
		update := effectiveDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask)
		update = documentUpdateTypeWithSnapshotOverride(doc.SourceObjectID, update, doc.DesiredVersionID, doc.ParseStatus, latestTask, hasLatestTask, snapshotUpdates, snapshotUpdatesAvailable)
		switch update {
		case "NEW":
			newCount++
		case "MODIFIED":
			modCount++
		case "DELETED":
			delCount++
		}
		if strings.TrimSpace(doc.CurrentVersionID) != "" {
			parsedCount++
		}
		if latest == nil {
			updated := doc.UpdatedAt.UTC()
			latest = &updated
		}
	}

	stateRows, err := s.sourceDocumentStatesForSource(ctx, src.ID)
	if err != nil {
		return model.SourceDocumentsSummary{}, nil, err
	}
	stateSummary := summarizeSourceDocumentStates(stateRows, len(summaryDocs), parsedCount, storage)
	seenSummaryPaths := make(map[string]struct{}, len(summaryDocs))
	for _, doc := range summaryDocs {
		path := filepath.Clean(strings.TrimSpace(doc.SourceObjectID))
		if path == "" || path == "." {
			continue
		}
		seenSummaryPaths[path] = struct{}{}
	}
	if (sourcelayout.IsCloudOriginType(src.DefaultOriginType) && snapshotUpdatesAvailable) || len(stateRows) == 0 {
		stateSummary.NewCount = newCount
		stateSummary.ModifiedCount = modCount
		stateSummary.DeletedCount = delCount
		stateSummary.PendingPullCount = newCount + modCount + delCount
	}
	return model.SourceDocumentsSummary{
		ParsedDocumentCount: parsedCount,
		StorageBytes:        storage,
		TotalDocumentCount:  stateSummary.TotalDocumentCount,
		NewCount:            stateSummary.NewCount,
		ModifiedCount:       stateSummary.ModifiedCount,
		DeletedCount:        stateSummary.DeletedCount,
		PendingPullCount:    stateSummary.PendingPullCount,
	}, latest, nil
}

func (s *Store) ListSourceDocumentOverviews(ctx context.Context, sources []model.Source) (map[string]model.SourceDocumentsResponse, error) {
	sourceByID := make(map[string]model.Source, len(sources))
	sourceIDsRaw := make([]string, 0, len(sources))
	agentIDsRaw := make([]string, 0, len(sources))
	for _, src := range sources {
		src.ID = strings.TrimSpace(src.ID)
		if src.ID == "" {
			continue
		}
		if _, exists := sourceByID[src.ID]; exists {
			continue
		}
		sourceByID[src.ID] = src
		sourceIDsRaw = append(sourceIDsRaw, src.ID)
		if agentID := strings.TrimSpace(src.AgentID); agentID != "" {
			agentIDsRaw = append(agentIDsRaw, agentID)
		}
	}

	sourceIDs := uniqueTrimmedStrings(sourceIDsRaw)
	result := make(map[string]model.SourceDocumentsResponse, len(sourceIDs))
	if len(sourceIDs) == 0 {
		return result, nil
	}

	agentOnlineByID := make(map[string]bool)
	if agentIDs := uniqueTrimmedStrings(agentIDsRaw); len(agentIDs) > 0 {
		var agents []agentEntity
		if err := s.db.WithContext(ctx).Where("agent_id IN ?", agentIDs).Find(&agents).Error; err != nil {
			return nil, err
		}
		for _, agent := range agents {
			agentOnlineByID[agent.AgentID] = strings.ToUpper(strings.TrimSpace(agent.Status)) != "OFFLINE"
		}
	}

	for _, sourceID := range sourceIDs {
		src := sourceByID[sourceID]
		agentID := strings.TrimSpace(src.AgentID)
		result[sourceID] = model.SourceDocumentsResponse{
			Source: model.SourceDocumentsSource{
				ID:                      sourceID,
				Name:                    src.Name,
				RootPath:                src.RootPath,
				WatchEnabled:            src.WatchEnabled,
				AgentID:                 src.AgentID,
				AgentOnline:             agentOnlineByID[agentID],
				UpdateTrackingSupported: true,
			},
			Items:    []model.SourceDocumentItem{},
			Page:     1,
			PageSize: 1,
		}
	}

	type sourceDocumentOverviewSummaryRow struct {
		SourceID            string `gorm:"column:source_id"`
		TotalDocumentCount  int64  `gorm:"column:total_document_count"`
		ParsedDocumentCount int64  `gorm:"column:parsed_document_count"`
		NewCount            int64  `gorm:"column:new_count"`
		ModifiedCount       int64  `gorm:"column:modified_count"`
		DeletedCount        int64  `gorm:"column:deleted_count"`
	}
	var summaryRows []sourceDocumentOverviewSummaryRow
	summaryQuery := s.db.WithContext(ctx).
		Table("documents").
		Select(`
				source_id,
				COUNT(*) AS total_document_count,
				SUM(CASE WHEN COALESCE(current_version_id, '') <> '' THEN 1 ELSE 0 END) AS parsed_document_count,
			SUM(CASE WHEN UPPER(COALESCE(parse_status, '')) <> 'DELETED' AND COALESCE(desired_version_id, '') <> '' AND COALESCE(current_version_id, '') = '' THEN 1 ELSE 0 END) AS new_count,
			SUM(CASE WHEN UPPER(COALESCE(parse_status, '')) <> 'DELETED' AND COALESCE(desired_version_id, '') <> '' AND COALESCE(current_version_id, '') <> '' AND desired_version_id <> current_version_id THEN 1 ELSE 0 END) AS modified_count,
			SUM(CASE WHEN UPPER(COALESCE(parse_status, '')) = 'DELETED' THEN 1 ELSE 0 END) AS deleted_count`).
		Where("source_id IN ?", sourceIDs).
		Scopes(func(db *gorm.DB) *gorm.DB {
			db = applyTransientPathFilter(db, "source_object_id")
			db = applyVisibleDocumentFilter(db, "parse_status")
			return applySourceDocumentListVisibilityFilter(db, "current_version_id", "core_document_id")
		}).
		Group("source_id")
	if err := summaryQuery.Scan(&summaryRows).Error; err != nil {
		return nil, err
	}
	statesBySourceID, err := s.sourceDocumentStatesForSources(ctx, sourceIDs)
	if err != nil {
		return nil, err
	}
	summaryRowsBySourceID := make(map[string]sourceDocumentOverviewSummaryRow, len(summaryRows))
	for _, row := range summaryRows {
		summaryRowsBySourceID[row.SourceID] = row
	}
	for _, sourceID := range sourceIDs {
		src := sourceByID[sourceID]
		row := summaryRowsBySourceID[sourceID]
		resp := result[sourceID]
		var summary model.SourceDocumentsSummary
		if sourcelayout.IsCloudOriginType(src.DefaultOriginType) || !src.WatchEnabled {
			var err error
			summary, _, err = s.buildSourceDocumentsSummary(ctx, sourceEntity{
				ID:                    src.ID,
				TenantID:              src.TenantID,
				RootPath:              src.RootPath,
				WatchEnabled:          src.WatchEnabled,
				DefaultOriginType:     src.DefaultOriginType,
				DefaultOriginPlatform: src.DefaultOriginPlatform,
			}, nil, false)
			if err != nil {
				return nil, err
			}
			if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
				mergeHiddenPendingSourceStateCounts(&summary, statesBySourceID[sourceID])
			}
		} else {
			summary = summarizeSourceDocumentStates(statesBySourceID[sourceID], int(row.TotalDocumentCount), row.ParsedDocumentCount, 0)
			if len(statesBySourceID[sourceID]) == 0 {
				summary = model.SourceDocumentsSummary{
					ParsedDocumentCount: row.ParsedDocumentCount,
					StorageBytes:        0,
					TotalDocumentCount:  row.TotalDocumentCount,
					NewCount:            row.NewCount,
					ModifiedCount:       row.ModifiedCount,
					DeletedCount:        row.DeletedCount,
					PendingPullCount:    row.NewCount + row.ModifiedCount + row.DeletedCount,
				}
			}
		}
		resp.Total = summary.TotalDocumentCount
		resp.Summary = summary
		result[sourceID] = resp
	}

	type sourceDocumentOverviewDocRow struct {
		ID               int64      `gorm:"column:id"`
		TenantID         string     `gorm:"column:tenant_id"`
		SourceID         string     `gorm:"column:source_id"`
		SourceObjectID   string     `gorm:"column:source_object_id"`
		CoreDocumentID   string     `gorm:"column:core_document_id"`
		CurrentVersionID string     `gorm:"column:current_version_id"`
		DesiredVersionID string     `gorm:"column:desired_version_id"`
		LastModifiedAt   *time.Time `gorm:"column:last_modified_at"`
		ParseStatus      string     `gorm:"column:parse_status"`
		OriginType       string     `gorm:"column:origin_type"`
		OriginPlatform   string     `gorm:"column:origin_platform"`
		OriginRef        string     `gorm:"column:origin_ref"`
		UpdatedAt        time.Time  `gorm:"column:updated_at"`
	}
	docQuery := s.db.WithContext(ctx).
		Model(&documentEntity{}).
		Select(`
				documents.id,
				documents.tenant_id,
				documents.source_id,
				documents.source_object_id,
				documents.core_document_id,
				documents.current_version_id,
				documents.desired_version_id,
				documents.last_modified_at,
			documents.parse_status,
			documents.origin_type,
			documents.origin_platform,
			documents.origin_ref,
			documents.updated_at`).
		Where("source_id IN ?", sourceIDs)
	docQuery = applyTransientPathFilter(docQuery, "source_object_id")
	docQuery = applyVisibleDocumentFilter(docQuery, "parse_status")
	docQuery = applySourceDocumentListVisibilityFilter(docQuery, "current_version_id", "core_document_id")

	var docRows []sourceDocumentOverviewDocRow
	if err := docQuery.Order("source_id ASC, updated_at DESC, id DESC").Scan(&docRows).Error; err != nil {
		return nil, err
	}

	docIDs := make([]int64, 0, len(docRows))
	for _, doc := range docRows {
		docIDs = append(docIDs, doc.ID)
	}
	latestTasksByDocID, err := s.latestParseTasksByDocumentIDs(ctx, docIDs)
	if err != nil {
		return nil, err
	}
	updateOverridesBySourceID := make(map[string]map[string]string, len(sourceIDs))
	updateOverridesAvailableBySourceID := make(map[string]bool, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		src := sourceByID[sourceID]
		updates, available, err := s.sourceDocumentUpdateOverrides(ctx, sourceEntity{
			ID:                    src.ID,
			TenantID:              src.TenantID,
			RootPath:              src.RootPath,
			WatchEnabled:          src.WatchEnabled,
			DefaultOriginType:     src.DefaultOriginType,
			DefaultOriginPlatform: src.DefaultOriginPlatform,
		})
		if err != nil {
			return nil, err
		}
		updateOverridesBySourceID[sourceID] = updates
		updateOverridesAvailableBySourceID[sourceID] = available
	}
	overviewMetaBySourceID := make(map[string]map[int64]sourceDocumentDisplayMeta, len(sourceIDs))
	rowsBySourceID := make(map[string][]sourceDocumentOverviewDocRow, len(sourceIDs))
	for _, doc := range docRows {
		rowsBySourceID[doc.SourceID] = append(rowsBySourceID[doc.SourceID], doc)
	}
	for sourceID, rows := range rowsBySourceID {
		src := sourceByID[sourceID]
		inputs := make([]sourceDocumentMetaInput, 0, len(rows))
		for _, row := range rows {
			inputs = append(inputs, sourceDocumentMetaInput{
				DocumentID:     row.ID,
				SourceObjectID: row.SourceObjectID,
				LastModifiedAt: row.LastModifiedAt,
				UpdatedAt:      row.UpdatedAt,
				OriginType:     row.OriginType,
				OriginPlatform: row.OriginPlatform,
				OriginRef:      row.OriginRef,
			})
		}
		meta, err := s.sourceDocumentDisplayMetadata(ctx, sourceEntity{
			ID:                    src.ID,
			TenantID:              src.TenantID,
			RootPath:              src.RootPath,
			WatchEnabled:          src.WatchEnabled,
			DefaultOriginType:     src.DefaultOriginType,
			DefaultOriginPlatform: src.DefaultOriginPlatform,
		}, inputs, latestTasksByDocID)
		if err != nil {
			return nil, err
		}
		overviewMetaBySourceID[sourceID] = meta
	}

	for _, doc := range docRows {
		resp := result[doc.SourceID]
		latestTask := latestTasksByDocID[doc.ID]
		_, hasLatestTask := latestTasksByDocID[doc.ID]
		update := effectiveDocumentUpdateType(doc.DesiredVersionID, doc.CurrentVersionID, doc.ParseStatus, latestTask, hasLatestTask)
		update = documentUpdateTypeWithSnapshotOverride(doc.SourceObjectID, update, doc.DesiredVersionID, doc.ParseStatus, latestTask, hasLatestTask, updateOverridesBySourceID[doc.SourceID], updateOverridesAvailableBySourceID[doc.SourceID])
		var hasUpdate *bool
		switch update {
		case "NEW", "MODIFIED", "DELETED":
			v := true
			hasUpdate = &v
		case "UNCHANGED":
			v := false
			hasUpdate = &v
		}
		displayMeta := overviewMetaBySourceID[doc.SourceID][doc.ID]
		knowledgeBasePresent := sourceDocumentHasKnowledgeBaseRelation(doc.CurrentVersionID, doc.CoreDocumentID)
		resp.Source.LastSyncedAt = displayMeta.LastSyncedAt
		resp.Items = append(resp.Items, model.SourceDocumentItem{
			DocumentID:              doc.ID,
			SourceCreateUserID:      strings.TrimSpace(sourceByID[doc.SourceID].CreateUserID),
			Name:                    filepath.Base(doc.SourceObjectID),
			Path:                    doc.SourceObjectID,
			Directory:               filepath.Base(filepath.Dir(doc.SourceObjectID)),
			HasUpdate:               hasUpdate,
			UpdateType:              update,
			UpdateDesc:              updateTypeDescription(update),
			ParseState:              effectiveSourceDocumentParseState(doc.ParseStatus, doc.DesiredVersionID, latestTask),
			FileType:                fileTypeFromPath(doc.SourceObjectID),
			SizeBytes:               displayMeta.SizeBytes,
			SourceUpdatedAt:         displayMeta.SourceUpdatedAt,
			LastSyncedAt:            displayMeta.LastSyncedAt,
			CoreDatasetID:           latestTask.CoreDatasetID,
			CoreTaskID:              latestTask.CoreTaskID,
			ScanOrchestrationStatus: latestTask.ScanOrchestrationStatus,
			DesiredVersionID:        doc.DesiredVersionID,
			CurrentVersionID:        doc.CurrentVersionID,
			ParseTaskID:             latestTask.TaskID,
			ParseTaskAction:         latestTask.TaskAction,
			ParseTaskTargetVersion:  latestTask.TargetVersionID,
			KnowledgeBasePresent:    &knowledgeBasePresent,
		})
		result[doc.SourceID] = resp
	}

	return result, nil
}

type sourceDocumentMetaInput struct {
	DocumentID     int64
	SourceObjectID string
	LastModifiedAt *time.Time
	UpdatedAt      time.Time
	OriginType     string
	OriginPlatform string
	OriginRef      string
}

type sourceDocumentDisplayMeta struct {
	SizeBytes       int64
	SourceUpdatedAt *time.Time
	LastSyncedAt    *time.Time
}

func sourceDocumentMetaInputsFromEntities(docs []documentEntity) []sourceDocumentMetaInput {
	inputs := make([]sourceDocumentMetaInput, 0, len(docs))
	for _, doc := range docs {
		inputs = append(inputs, sourceDocumentMetaInput{
			DocumentID:     doc.ID,
			SourceObjectID: doc.SourceObjectID,
			LastModifiedAt: doc.LastModifiedAt,
			UpdatedAt:      doc.UpdatedAt,
			OriginType:     doc.OriginType,
			OriginPlatform: doc.OriginPlatform,
			OriginRef:      doc.OriginRef,
		})
	}
	return inputs
}

func (s *Store) sourceDocumentDisplayMetadata(ctx context.Context, src sourceEntity, docs []sourceDocumentMetaInput, latestTasks map[int64]parseTaskDocJoin) (map[int64]sourceDocumentDisplayMeta, error) {
	result := make(map[int64]sourceDocumentDisplayMeta, len(docs))
	if len(docs) == 0 {
		return result, nil
	}
	for _, doc := range docs {
		result[doc.DocumentID] = sourceDocumentDisplayMeta{
			SourceUpdatedAt: normalizedTimePtr(doc.LastModifiedAt),
			LastSyncedAt:    sourceDocumentLastSyncedAt(doc, latestTasks[doc.DocumentID]),
		}
	}
	if isCloudSourceForDisplay(src, docs) {
		if err := s.applyCloudDocumentDisplayMetadata(ctx, src.ID, docs, result); err != nil {
			return nil, err
		}
		return result, nil
	}
	if err := s.applyLocalDocumentDisplayMetadata(ctx, src, docs, result); err != nil {
		return nil, err
	}
	return result, nil
}

func sourceDocumentLastSyncedAt(doc sourceDocumentMetaInput, task parseTaskDocJoin) *time.Time {
	if t := normalizedTimePtr(task.FinishedAt); t != nil {
		return t
	}
	if t := normalizedTimePtr(task.SubmitAt); t != nil {
		return t
	}
	if !task.UpdatedAt.IsZero() {
		t := task.UpdatedAt.UTC()
		return &t
	}
	if !doc.UpdatedAt.IsZero() {
		t := doc.UpdatedAt.UTC()
		return &t
	}
	return nil
}

func normalizedTimePtr(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	v := t.UTC()
	return &v
}

func isCloudSourceForDisplay(src sourceEntity, docs []sourceDocumentMetaInput) bool {
	if strings.EqualFold(strings.TrimSpace(src.DefaultOriginType), string(model.OriginTypeCloudSync)) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(src.DefaultOriginPlatform), "FEISHU") {
		return true
	}
	for _, doc := range docs {
		if strings.EqualFold(strings.TrimSpace(doc.OriginType), string(model.OriginTypeCloudSync)) {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(doc.OriginPlatform), "FEISHU") {
			return true
		}
	}
	return false
}

func (s *Store) applyCloudDocumentDisplayMetadata(ctx context.Context, sourceID string, docs []sourceDocumentMetaInput, result map[int64]sourceDocumentDisplayMeta) error {
	originRefs := make([]string, 0, len(docs))
	paths := make([]string, 0, len(docs))
	seenRefs := make(map[string]struct{}, len(docs))
	seenPaths := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		if ref := strings.TrimSpace(doc.OriginRef); ref != "" {
			if _, ok := seenRefs[ref]; !ok {
				seenRefs[ref] = struct{}{}
				originRefs = append(originRefs, ref)
			}
		}
		if path := strings.TrimSpace(doc.SourceObjectID); path != "" {
			if _, ok := seenPaths[path]; !ok {
				seenPaths[path] = struct{}{}
				paths = append(paths, path)
			}
		}
	}
	if len(originRefs) == 0 && len(paths) == 0 {
		return nil
	}
	query := s.db.WithContext(ctx).
		Model(&cloudObjectIndexEntity{}).
		Where("source_id = ?", strings.TrimSpace(sourceID))
	if len(originRefs) > 0 && len(paths) > 0 {
		query = query.Where("(external_object_id IN ? OR local_abs_path IN ? OR local_rel_path IN ? OR external_path IN ?)", originRefs, paths, paths, paths)
	} else if len(originRefs) > 0 {
		query = query.Where("external_object_id IN ?", originRefs)
	} else {
		query = query.Where("(local_abs_path IN ? OR local_rel_path IN ? OR external_path IN ?)", paths, paths, paths)
	}
	var rows []cloudObjectIndexEntity
	if err := query.Find(&rows).Error; err != nil {
		return err
	}
	byRef := make(map[string]cloudObjectIndexEntity, len(rows))
	byPath := make(map[string]cloudObjectIndexEntity, len(rows)*3)
	for _, row := range rows {
		if ref := strings.TrimSpace(row.ExternalObjectID); ref != "" {
			byRef[ref] = row
		}
		for _, path := range []string{row.LocalAbsPath, row.LocalRelPath, row.ExternalPath} {
			cleaned := filepath.Clean(strings.TrimSpace(path))
			if cleaned != "" && cleaned != "." {
				byPath[cleaned] = row
			}
		}
	}
	for _, doc := range docs {
		row, ok := byRef[strings.TrimSpace(doc.OriginRef)]
		if !ok {
			row, ok = byPath[filepath.Clean(strings.TrimSpace(doc.SourceObjectID))]
		}
		if !ok {
			continue
		}
		meta := result[doc.DocumentID]
		meta.SizeBytes = row.SizeBytes
		if t := normalizedTimePtr(row.ExternalModifiedAt); t != nil {
			meta.SourceUpdatedAt = t
		}
		if t := normalizedTimePtr(row.LastSyncedAt); t != nil {
			meta.LastSyncedAt = t
		}
		result[doc.DocumentID] = meta
	}
	return nil
}

func (s *Store) applyLocalDocumentDisplayMetadata(ctx context.Context, src sourceEntity, docs []sourceDocumentMetaInput, result map[int64]sourceDocumentDisplayMeta) error {
	items, err := s.latestSourceSnapshotItemsForDisplay(ctx, src)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	for _, doc := range docs {
		item, ok := items[filepath.Clean(strings.TrimSpace(doc.SourceObjectID))]
		if !ok {
			continue
		}
		meta := result[doc.DocumentID]
		meta.SizeBytes = item.SizeBytes
		if item.ModTime != nil && !item.ModTime.IsZero() {
			mt := item.ModTime.UTC()
			meta.SourceUpdatedAt = &mt
		}
		result[doc.DocumentID] = meta
	}
	return nil
}

func (s *Store) latestSourceSnapshotItemsForDisplay(ctx context.Context, src sourceEntity) (map[string]sourceFileSnapshotItemEntity, error) {
	sourceID := strings.TrimSpace(src.ID)
	if sourceID == "" {
		return map[string]sourceFileSnapshotItemEntity{}, nil
	}
	var relation sourceSnapshotRelationEntity
	if err := s.db.WithContext(ctx).Take(&relation, "source_id = ?", sourceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return map[string]sourceFileSnapshotItemEntity{}, nil
		}
		return nil, err
	}
	display := make(map[string]sourceFileSnapshotItemEntity)
	if committedID := strings.TrimSpace(relation.LastCommittedSnapshotID); committedID != "" {
		items, _, err := s.snapshotItemsForDiffBase(ctx, sourceID, committedID)
		if err != nil {
			return nil, err
		}
		for path, item := range filterTransientSnapshotItems(items) {
			display[path] = item
		}
	}
	if previewID := strings.TrimSpace(relation.LastPreviewSnapshotID); previewID != "" {
		preview, err := s.loadSnapshotByID(ctx, previewID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if err == nil && strings.EqualFold(strings.TrimSpace(preview.SnapshotType), "PREVIEW") && (preview.ConsumedAt == nil || src.WatchEnabled) {
			items, err := s.snapshotItemsByPath(ctx, previewID)
			if err != nil {
				return nil, err
			}
			for path, item := range filterTransientSnapshotItems(items) {
				display[path] = item
			}
			return display, nil
		}
	}
	return display, nil
}

func effectiveSourceDocumentParseState(documentStatus, desiredVersion string, latestTask parseTaskDocJoin) string {
	documentState := strings.ToUpper(strings.TrimSpace(documentStatus))
	if documentState == "" {
		documentState = "PENDING"
	}

	taskState := effectiveLatestParseTaskState(desiredVersion, latestTask)
	if taskState == "" {
		return documentState
	}

	switch taskState {
	case "PENDING", "RETRY_WAITING", "RUNNING", "STAGING", "SUBMITTED":
		return taskState
	case "SUBMIT_FAILED", "FAILED":
		return taskState
	case "SUCCEEDED":
		if documentState == "QUEUED" || documentState == "PENDING" || documentState == "RUNNING" {
			return taskState
		}
	}
	return documentState
}

func effectiveLatestParseTaskState(desiredVersion string, latestTask parseTaskDocJoin) string {
	taskState := strings.ToUpper(strings.TrimSpace(latestTask.ScanOrchestrationStatus))
	if taskState == "" {
		taskState = strings.ToUpper(strings.TrimSpace(latestTask.Status))
	}
	if taskState == "" {
		return ""
	}

	taskTargetVersion := strings.TrimSpace(latestTask.TargetVersionID)
	currentDesiredVersion := strings.TrimSpace(desiredVersion)
	if taskTargetVersion != "" && currentDesiredVersion != "" && taskTargetVersion != currentDesiredVersion {
		return ""
	}
	return taskState
}

func (s *Store) nonWatchSourceDocumentUpdateOverrides(ctx context.Context, src sourceEntity) (map[string]string, bool, error) {
	if src.WatchEnabled {
		return nil, false, nil
	}
	return s.sourceDocumentUpdateOverrides(ctx, src)
}

func (s *Store) sourceDocumentUpdateOverrides(ctx context.Context, src sourceEntity) (map[string]string, bool, error) {
	if src.WatchEnabled && !sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		return nil, false, nil
	}
	var relation sourceSnapshotRelationEntity
	if err := s.db.WithContext(ctx).Take(&relation, "source_id = ?", src.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.mergeCloudDeletedObjectUpdateOverrides(ctx, src, nil, false)
		}
		return nil, false, err
	}

	if previewID := strings.TrimSpace(relation.LastPreviewSnapshotID); previewID != "" {
		preview, err := s.loadSnapshotByID(ctx, previewID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, err
		}
		if err == nil && strings.EqualFold(strings.TrimSpace(preview.SnapshotType), "PREVIEW") && preview.ConsumedAt == nil {
			diff, err := s.diffBySnapshotID(ctx, preview)
			if err != nil {
				return nil, false, err
			}
			return s.mergeCloudDeletedObjectUpdateOverrides(ctx, src, normalizeSnapshotUpdateOverrides(diff), true)
		}
	}

	if committedID := strings.TrimSpace(relation.LastCommittedSnapshotID); committedID != "" {
		if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
			updates, available, err := s.cloudSourceDocumentUpdateOverridesFromIndex(ctx, src, committedID)
			if err != nil {
				return nil, false, err
			}
			if available {
				return s.mergeCloudDeletedObjectUpdateOverrides(ctx, src, updates, true)
			}
		}
		items, _, err := s.snapshotItemsForDiffBase(ctx, src.ID, committedID)
		if err != nil {
			return nil, false, err
		}
		updates := make(map[string]string, len(items))
		for rawPath, item := range items {
			if item.IsDir {
				continue
			}
			path := filepath.Clean(strings.TrimSpace(rawPath))
			if path == "" || path == "." {
				continue
			}
			updates[path] = "UNCHANGED"
		}
		if len(updates) > 0 {
			return s.mergeCloudDeletedObjectUpdateOverrides(ctx, src, updates, true)
		}
	}

	return s.mergeCloudDeletedObjectUpdateOverrides(ctx, src, nil, false)
}

func (s *Store) cloudSourceDocumentUpdateOverridesFromIndex(ctx context.Context, src sourceEntity, committedSnapshotID string) (map[string]string, bool, error) {
	committedSnapshotID = strings.TrimSpace(committedSnapshotID)
	if committedSnapshotID == "" {
		return nil, false, nil
	}
	baseItems, _, err := s.snapshotItemsForDiffBase(ctx, src.ID, committedSnapshotID)
	if err != nil {
		return nil, false, err
	}
	baseItems, err = s.cloudSnapshotItemsForObjectDiff(ctx, src.ID, baseItems)
	if err != nil {
		return nil, false, err
	}
	baseItems = filterTransientSnapshotItems(baseItems)

	var rows []cloudObjectIndexEntity
	if err := s.db.WithContext(ctx).
		Where("source_id = ?", src.ID).
		Find(&rows).Error; err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}

	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	currentItems := make(map[string]sourceFileSnapshotItemEntity, len(rows))
	for _, row := range rows {
		if row.IsDeleted || cloudObjectIsDirectory(row.ExternalKind) {
			continue
		}
		path := filepath.Clean(resolveCloudObjectLocalPath(mirrorRoot, row))
		if path == "" || path == "." || isTransientSourceFilePath(path, false) {
			continue
		}
		checksum := strings.TrimSpace(row.ExternalVersion)
		if checksum == "" {
			checksum = strings.TrimSpace(row.Checksum)
		}
		if checksum == "" {
			if base, ok := baseItems[path]; ok {
				checksum = strings.TrimSpace(base.Checksum)
			}
		}
		item := sourceFileSnapshotItemEntity{
			Path:      path,
			IsDir:     false,
			SizeBytes: row.SizeBytes,
			Checksum:  checksum,
		}
		if row.ExternalModifiedAt != nil && !row.ExternalModifiedAt.IsZero() {
			mt := row.ExternalModifiedAt.UTC()
			item.ModTime = &mt
		}
		currentItems[path] = item
	}
	return normalizeSnapshotUpdateOverrides(diffSnapshotMaps(baseItems, currentItems)), true, nil
}

func (s *Store) mergeCloudDeletedObjectUpdateOverrides(ctx context.Context, src sourceEntity, updates map[string]string, available bool) (map[string]string, bool, error) {
	if !sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		return updates, available, nil
	}
	deleted, err := s.cloudDeletedObjectUpdateOverrides(ctx, src)
	if err != nil {
		return nil, false, err
	}
	if len(deleted) == 0 {
		return updates, available, nil
	}
	if updates == nil {
		updates = make(map[string]string, len(deleted))
	}
	for path, update := range deleted {
		updates[path] = update
	}
	return updates, true, nil
}

func (s *Store) cloudDeletedObjectUpdateOverrides(ctx context.Context, src sourceEntity) (map[string]string, error) {
	out := make(map[string]string)
	sourceID := strings.TrimSpace(src.ID)
	if sourceID == "" {
		return out, nil
	}
	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	if mirrorRoot == "" || mirrorRoot == "." {
		return out, nil
	}
	var rows []cloudObjectIndexEntity
	if err := s.db.WithContext(ctx).
		Where("source_id = ?", sourceID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return out, nil
	}

	activePaths := make(map[string]struct{}, len(rows)*2)
	for _, row := range rows {
		if row.IsDeleted || cloudObjectIsDirectory(row.ExternalKind) {
			continue
		}
		for _, path := range cloudObjectDocumentUpdatePaths(mirrorRoot, row) {
			activePaths[path] = struct{}{}
		}
	}
	for _, row := range rows {
		if !row.IsDeleted || cloudObjectIsDirectory(row.ExternalKind) {
			continue
		}
		for _, path := range cloudObjectDocumentUpdatePaths(mirrorRoot, row) {
			if _, active := activePaths[path]; active {
				continue
			}
			out[path] = "DELETED"
		}
	}
	return out, nil
}

func cloudObjectDocumentUpdatePaths(mirrorRoot string, row cloudObjectIndexEntity) []string {
	objectPath := filepath.Clean(strings.TrimSpace(resolveCloudObjectLocalPath(mirrorRoot, row)))
	if objectPath == "" || objectPath == "." || isTransientSourceFilePath(objectPath, false) {
		return nil
	}
	paths := []string{objectPath}
	treePath := filepath.Clean(strings.TrimSpace(cloudObjectTreePath(objectPath, row)))
	if treePath != "" && treePath != "." && treePath != objectPath && !isTransientSourceFilePath(treePath, false) {
		paths = append(paths, treePath)
	}
	return paths
}

func normalizeSnapshotUpdateOverrides(diff map[string]string) map[string]string {
	updates := make(map[string]string, len(diff))
	for rawPath, rawUpdate := range diff {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		update := normalizeSnapshotUpdateType(rawUpdate)
		if path == "" || path == "." || update == "" {
			continue
		}
		updates[path] = update
	}
	return updates
}

func documentUpdateTypeWithSnapshotOverride(path, fallback, desiredVersionID, parseStatus string, latestTask parseTaskDocJoin, hasLatestTask bool, updates map[string]string, available bool) string {
	if documentUpdateShouldWinSnapshot(fallback, desiredVersionID, parseStatus, latestTask, hasLatestTask) {
		return fallback
	}
	if !available {
		return fallback
	}
	if update, ok := updates[filepath.Clean(strings.TrimSpace(path))]; ok {
		if update == "NEW" && strings.EqualFold(strings.TrimSpace(fallback), "UNCHANGED") {
			return "UNCHANGED"
		}
		return update
	}
	return fallback
}

func documentUpdateShouldWinSnapshot(updateType, desiredVersionID, parseStatus string, latestTask parseTaskDocJoin, hasLatestTask bool) bool {
	switch strings.ToUpper(strings.TrimSpace(updateType)) {
	case "MODIFIED", "DELETED":
	default:
		return false
	}
	status := strings.ToUpper(strings.TrimSpace(parseStatus))
	switch status {
	case "PENDING", "QUEUED", "RUNNING", "DELETED":
		return true
	case "FAILED", "SUBMIT_FAILED", "RETRY_WAITING":
		return true
	}
	if !hasLatestTask {
		return false
	}
	targetVersion := strings.TrimSpace(latestTask.TargetVersionID)
	if targetVersion == "" || targetVersion != strings.TrimSpace(desiredVersionID) {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(latestTask.ScanOrchestrationStatus)) {
	case "PENDING", "RETRY_WAITING", "RUNNING", "STAGING", "SUBMITTED", "SUBMIT_FAILED", "FAILED":
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(latestTask.Status)) {
	case "PENDING", "RETRY_WAITING", "RUNNING", "STAGING", "SUBMITTED", "SUBMIT_FAILED", "FAILED":
		return true
	default:
		return false
	}
}

func normalizeSnapshotUpdateType(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "NEW":
		return "NEW"
	case "MODIFIED":
		return "MODIFIED"
	case "DELETED":
		return "DELETED"
	case "UNCHANGED", "NONE":
		return "UNCHANGED"
	default:
		return ""
	}
}

func (s *Store) ListSourceDocumentCoreRefs(ctx context.Context, sourceID, tenantID string) ([]SourceDocumentCoreRef, error) {
	sourceID = strings.TrimSpace(sourceID)
	tenantID = strings.TrimSpace(tenantID)
	if sourceID == "" {
		return nil, fmt.Errorf("source_id is required")
	}
	if tenantID == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	var src sourceEntity
	if err := s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ?", sourceID, tenantID).
		Take(&src).Error; err != nil {
		return nil, err
	}
	sub := s.db.WithContext(ctx).
		Table("parse_tasks").
		Select("MAX(id) AS max_id, document_id").
		Group("document_id")
	rows := make([]SourceDocumentCoreRef, 0, 128)
	if err := s.db.WithContext(ctx).
		Table("documents d").
		Select(`
			d.id AS document_id,
			? AS source_create_user_id,
			d.source_object_id AS source_object_id,
			d.parse_status AS parse_status,
			d.desired_version_id AS desired_version_id,
			d.current_version_id AS current_version_id,
			d.updated_at AS updated_at,
			pt.id AS task_id,
			pt.task_action AS task_action,
			pt.target_version_id AS target_version_id,
			pt.core_dataset_id AS core_dataset_id,
			d.core_document_id AS core_document_id,
			pt.core_task_id AS core_task_id,
			pt.scan_orchestration_status AS scan_orchestration_status
		`, strings.TrimSpace(src.CreateUserID)).
		Joins("LEFT JOIN (?) latest ON latest.document_id = d.id", sub).
		Joins("LEFT JOIN parse_tasks pt ON pt.id = latest.max_id").
		Where("d.tenant_id = ? AND d.source_id = ?", tenantID, src.ID).
		Scopes(func(db *gorm.DB) *gorm.DB {
			return applyTransientPathFilter(db, "d.source_object_id")
		}).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Store) BuildTreeUpdateState(ctx context.Context, sourceID string, items []model.TreeNode, fileStats map[string]model.TreeFileStat) ([]model.TreeNode, string, error) {
	var src sourceEntity
	if err := s.db.WithContext(ctx).Take(&src, "id = ?", strings.TrimSpace(sourceID)).Error; err != nil {
		return nil, "", err
	}
	scopeRoots := collectSourceTreeScopeRoots(src, items)
	filePaths := collectTreeFilePaths(items)
	displayToObjectPaths := map[string]string{}
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) && len(filePaths) > 0 {
		mapped, err := s.cloudTreePathsToObjectPathsIncludingDeleted(ctx, src.ID, filePaths, true)
		if err != nil {
			return nil, "", err
		}
		displayToObjectPaths = mapped
	}
	objectFilePaths := treeDisplayPathsToObjectPaths(filePaths, displayToObjectPaths)
	pathMap := make(map[string]treeDocumentRow)
	queueMap := make(map[int64]parseTaskDocJoin)
	if len(filePaths) > 0 {
		docs, err := s.treeDocumentRowsByPath(ctx, src.ID, filePaths)
		if err != nil {
			return nil, "", err
		}
		pathMap = docs
		latestTasks, err := s.latestParseTasksForTreeDocumentRows(ctx, docs)
		if err != nil {
			return nil, "", err
		}
		for docID, task := range latestTasks {
			queueMap[docID] = task
		}
	}

	diffByPath, err := s.previewTreeDiff(ctx, src, scopeRoots, filePaths, fileStats)
	if err != nil {
		return nil, "", err
	}
	if src.WatchEnabled {
		compareScopeRoots, err := s.cloudCompareScopeRoots(ctx, src, scopeRoots)
		if err != nil {
			return nil, "", err
		}
		deletedPaths := collectDeletedPathsFromDiff(diffByPath, filePaths)
		if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
			missingDocumentPaths, err := s.missingDocumentPaths(ctx, src.ID, compareScopeRoots, objectFilePaths)
			if err != nil {
				return nil, "", err
			}
			deletedPaths = append(deletedPaths, missingDocumentPaths...)
		}
		pendingDeletedPaths, err := s.deletedDocumentPaths(ctx, src.ID, compareScopeRoots, objectFilePaths)
		if err != nil {
			return nil, "", err
		}
		deletedPaths = append(deletedPaths, pendingDeletedPaths...)
		deletedPaths = s.displayPathsForCloudObjects(ctx, src, deletedPaths)
		addDeletedPathsToDiff(diffByPath, deletedPaths)
		stateDeletedPaths, err := s.sourceDeletedDocumentStatePaths(ctx, src.ID, compareScopeRoots)
		if err != nil {
			return nil, "", err
		}
		stateDeletedPaths = s.displayPathsForCloudObjects(ctx, src, stateDeletedPaths)
		stateDeletedPaths = excludeCurrentTreePaths(stateDeletedPaths, filePaths)
		addDeletedPathsToDiff(diffByPath, stateDeletedPaths)
		deletedTitleByPath, err := s.cloudTreeTitleByPath(ctx, src, append(append([]string{}, deletedPaths...), stateDeletedPaths...))
		if err != nil {
			return nil, "", err
		}
		updated := applyWatchTreeNodeStates(items, diffByPath, pathMap, queueMap)
		updated = addDeletedNodes(updated, deletedPaths, deletedTreeRootPath(src, items, scopeRoots), "DOCUMENTS", pathMap, queueMap, deletedTitleByPath)
		updated = addDeletedNodes(updated, stateDeletedPaths, deletedTreeRootPath(src, items, scopeRoots), "SOURCE_DOCUMENT_STATES", pathMap, queueMap, deletedTitleByPath)
		treePaths := collectAllTreePaths(updated)
		if states, err := s.sourceDocumentStateByPaths(ctx, src.ID, treePaths); err != nil {
			return nil, "", err
		} else {
			updated = applySourceDocumentStatesToTree(updated, states)
			tokenDiff := effectiveSelectionDiff(treePaths, diffByPath, pathMap, queueMap, states)
			selectionToken, err := encodeReadOnlySelectionToken(src.ID, tokenDiff, time.Now().UTC())
			if err != nil {
				return nil, "", err
			}
			return updated, selectionToken, nil
		}
	}

	deletedPaths := collectDeletedPathsFromDiff(diffByPath, filePaths)
	compareScopeRoots, err := s.cloudCompareScopeRoots(ctx, src, scopeRoots)
	if err != nil {
		return nil, "", err
	}
	missingDocumentPaths, err := s.missingDocumentPaths(ctx, src.ID, compareScopeRoots, objectFilePaths)
	if err != nil {
		return nil, "", err
	}
	missingDocumentPaths = s.displayPathsForCloudObjects(ctx, src, missingDocumentPaths)
	deletedPaths = append(deletedPaths, missingDocumentPaths...)
	addDeletedPathsToDiff(diffByPath, deletedPaths)
	stateDeletedPaths, err := s.sourceDeletedDocumentStatePaths(ctx, src.ID, compareScopeRoots)
	if err != nil {
		return nil, "", err
	}
	stateDeletedPaths = s.displayPathsForCloudObjects(ctx, src, stateDeletedPaths)
	stateDeletedPaths = excludeCurrentTreePaths(stateDeletedPaths, filePaths)
	addDeletedPathsToDiff(diffByPath, stateDeletedPaths)
	deletedTitleByPath, err := s.cloudTreeTitleByPath(ctx, src, append(append([]string{}, deletedPaths...), stateDeletedPaths...))
	if err != nil {
		return nil, "", err
	}
	updated := applySnapshotTreeNodeStates(items, diffByPath, pathMap, queueMap)
	updated = addDeletedNodes(updated, deletedPaths, deletedTreeRootPath(src, items, scopeRoots), "SNAPSHOT", pathMap, queueMap, deletedTitleByPath)
	updated = addDeletedNodes(updated, stateDeletedPaths, deletedTreeRootPath(src, items, scopeRoots), "SOURCE_DOCUMENT_STATES", pathMap, queueMap, deletedTitleByPath)
	treePaths := collectAllTreePaths(updated)
	if states, err := s.sourceDocumentStateByPaths(ctx, src.ID, treePaths); err != nil {
		return nil, "", err
	} else {
		updated = applySourceDocumentStatesToTree(updated, states)
		tokenDiff := effectiveSelectionDiff(treePaths, diffByPath, pathMap, queueMap, states)
		selectionToken, err := encodeReadOnlySelectionToken(src.ID, tokenDiff, time.Now().UTC())
		if err != nil {
			return nil, "", err
		}
		return updated, selectionToken, nil
	}
}

func addDeletedPathsToDiff(diffByPath map[string]string, paths []string) {
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." || isTransientSourceFilePath(path, false) {
			continue
		}
		diffByPath[path] = "DELETED"
	}
}

func treeDisplayPathsToObjectPaths(paths []string, displayToObject map[string]string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		if rawObjectPath := strings.TrimSpace(displayToObject[path]); rawObjectPath != "" {
			if objectPath := filepath.Clean(rawObjectPath); objectPath != "" && objectPath != "." {
				path = objectPath
			}
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func (s *Store) cloudCompareScopeRoots(ctx context.Context, src sourceEntity, scopeRoots []string) ([]string, error) {
	if !sourcelayout.IsCloudOriginType(src.DefaultOriginType) || len(scopeRoots) == 0 {
		return scopeRoots, nil
	}
	mappedRoots, err := s.cloudTreePathsToObjectPathsIncludingDeleted(ctx, src.ID, scopeRoots, true)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(scopeRoots))
	for _, rawRoot := range scopeRoots {
		root := filepath.Clean(strings.TrimSpace(rawRoot))
		if root == "" || root == "." {
			continue
		}
		if rawObjectRoot := strings.TrimSpace(mappedRoots[root]); rawObjectRoot != "" {
			if objectRoot := filepath.Clean(rawObjectRoot); objectRoot != "" && objectRoot != "." {
				out = append(out, objectRoot)
				continue
			}
		}
		out = append(out, root)
	}
	return out, nil
}

func (s *Store) displayPathsForCloudObjects(ctx context.Context, src sourceEntity, paths []string) []string {
	if !sourcelayout.IsCloudOriginType(src.DefaultOriginType) || len(paths) == 0 {
		return paths
	}
	mapped, err := s.cloudObjectPathsToTreePathsIncludingDeleted(ctx, src.ID, paths, true)
	if err != nil {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		if rawDisplayPath := strings.TrimSpace(mapped[path]); rawDisplayPath != "" {
			if displayPath := filepath.Clean(rawDisplayPath); displayPath != "" && displayPath != "." {
				out = append(out, displayPath)
				continue
			}
		}
		out = append(out, path)
	}
	return out
}

func (s *Store) cloudTreeTitleByPath(ctx context.Context, src sourceEntity, paths []string) (map[string]string, error) {
	out := make(map[string]string)
	if !sourcelayout.IsCloudOriginType(src.DefaultOriginType) || len(paths) == 0 {
		return out, nil
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
	if err := s.db.WithContext(ctx).
		Where("source_id = ?", src.ID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		objectPath := resolveCloudObjectLocalPath(rootPath, row)
		if objectPath == "" {
			continue
		}
		objectPath = filepath.Clean(objectPath)
		treePath := filepath.Clean(cloudObjectTreePath(objectPath, row))
		if treePath == "" || treePath == "." {
			continue
		}
		if _, ok := wanted[objectPath]; ok {
			out[objectPath] = cloudObjectDisplayTitle(treePath, row, false)
		}
		if _, ok := wanted[treePath]; ok {
			out[treePath] = cloudObjectDisplayTitle(treePath, row, false)
		}
	}
	return out, nil
}

func excludeCurrentTreePaths(paths []string, currentPaths []string) []string {
	if len(paths) == 0 || len(currentPaths) == 0 {
		return paths
	}
	currentSet := make(map[string]struct{}, len(currentPaths))
	for _, rawPath := range currentPaths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		currentSet[path] = struct{}{}
	}
	out := make([]string, 0, len(paths))
	for _, rawPath := range paths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if _, ok := currentSet[path]; ok {
			continue
		}
		out = append(out, rawPath)
	}
	return out
}

func normalizeCloudRequestPathsWithSelection(rawPaths []string, root string, diffByPath map[string]string) ([]string, []string, int) {
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	if cleanRoot == "" || cleanRoot == "." {
		return nil, nil, len(rawPaths)
	}
	unique := make(map[string]struct{}, len(rawPaths))
	paths := make([]string, 0, len(rawPaths))
	recoveredFromSelection := make([]string, 0)
	skipped := 0
	for _, raw := range rawPaths {
		path := filepath.Clean(strings.TrimSpace(raw))
		if path == "" || path == "." || isTransientSourceFilePath(path, false) {
			skipped++
			continue
		}
		if path == cleanRoot || strings.HasPrefix(path, cleanRoot+string(filepath.Separator)) {
			if _, ok := unique[path]; !ok {
				unique[path] = struct{}{}
				paths = append(paths, path)
			}
			continue
		}
		if _, ok := diffByPath[path]; ok {
			if _, exists := unique[path]; !exists {
				unique[path] = struct{}{}
				recoveredFromSelection = append(recoveredFromSelection, path)
			}
			continue
		}
		skipped++
	}
	return paths, recoveredFromSelection, skipped
}

func deletedTreeRootPath(src sourceEntity, items []model.TreeNode, scopeRoots []string) string {
	if root := collectTreeDisplayRoot(items); root != "" {
		return root
	}
	if len(scopeRoots) == 1 {
		root := filepath.Clean(strings.TrimSpace(scopeRoots[0]))
		if root != "" && root != "." {
			return root
		}
	}
	if len(scopeRoots) > 1 {
		if common := commonScopeRoot(scopeRoots); common != "" {
			return common
		}
	}
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		return filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	}
	return filepath.Clean(strings.TrimSpace(src.RootPath))
}

func collectSourceTreeScopeRoots(src sourceEntity, items []model.TreeNode) []string {
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		if root := collectTreeDisplayRoot(items); root != "" {
			return []string{root}
		}
		return []string{filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))}
	}
	return collectTreeScopeRoots(items)
}

func collectTreeDisplayRoot(items []model.TreeNode) string {
	parents := make([]string, 0, len(items))
	for _, item := range items {
		key := filepath.Clean(strings.TrimSpace(item.Key))
		if key == "" || key == "." {
			continue
		}
		parent := filepath.Clean(filepath.Dir(key))
		if parent == "" || parent == "." {
			continue
		}
		parents = append(parents, parent)
	}
	return commonScopeRoot(parents)
}

func (s *Store) previewTreeDiff(ctx context.Context, src sourceEntity, scopeRoots []string, filePaths []string, fileStats map[string]model.TreeFileStat) (map[string]string, error) {
	currentItems := make(map[string]sourceFileSnapshotItemEntity, len(filePaths))
	displayByComparePath := make(map[string]string, len(filePaths))
	treeToObject := map[string]string{}
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		mapped, err := s.cloudTreePathsToObjectPathsIncludingDeleted(ctx, src.ID, filePaths, true)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		treeToObject = mapped
	}
	seen := make(map[string]struct{}, len(filePaths))
	for _, rawPath := range filePaths {
		displayPath := filepath.Clean(strings.TrimSpace(rawPath))
		if displayPath == "" || displayPath == "." || isTransientSourceFilePath(displayPath, false) {
			continue
		}
		comparePath := displayPath
		if objectPath := strings.TrimSpace(treeToObject[displayPath]); objectPath != "" {
			comparePath = filepath.Clean(objectPath)
		}
		if comparePath == "" || comparePath == "." || isTransientSourceFilePath(comparePath, false) {
			continue
		}
		if _, ok := seen[comparePath]; ok {
			continue
		}
		seen[comparePath] = struct{}{}
		displayByComparePath[comparePath] = displayPath
		stat := fileStats[displayPath]
		if strings.TrimSpace(stat.Path) == "" {
			stat.Path = displayPath
		}
		item := sourceFileSnapshotItemEntity{
			Path:      comparePath,
			IsDir:     stat.IsDir,
			SizeBytes: stat.Size,
			Checksum:  strings.TrimSpace(stat.Checksum),
		}
		if stat.ModTime != nil && !stat.ModTime.IsZero() {
			mt := stat.ModTime.UTC()
			item.ModTime = &mt
		}
		currentItems[comparePath] = item
	}

	var relation sourceSnapshotRelationEntity
	if err := s.db.WithContext(ctx).Take(&relation, "source_id = ?", src.ID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	baseItems, _, err := s.snapshotItemsForDiffBase(ctx, src.ID, relation.LastCommittedSnapshotID)
	if err != nil {
		return nil, err
	}
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		baseItems, err = s.cloudSnapshotItemsForObjectDiff(ctx, src.ID, baseItems)
		if err != nil {
			return nil, err
		}
	}
	compareScopeRoots := scopeRoots
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		mappedRoots, err := s.cloudCompareScopeRoots(ctx, src, scopeRoots)
		if err != nil {
			return nil, err
		}
		compareScopeRoots = mappedRoots
	}
	if len(compareScopeRoots) > 0 {
		filtered := make(map[string]sourceFileSnapshotItemEntity, len(baseItems))
		for path, item := range baseItems {
			if isTransientSourceFilePath(path, item.IsDir) {
				continue
			}
			if pathInScope(path, compareScopeRoots) {
				filtered[path] = item
			}
		}
		baseItems = filtered
	} else {
		baseItems = filterTransientSnapshotItems(baseItems)
	}
	diff := diffSnapshotMaps(baseItems, currentItems)
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		diff = s.cloudTreeDisplayDiff(ctx, src.ID, diff, displayByComparePath)
	}
	return diff, nil
}

func (s *Store) cloudSnapshotItemsForObjectDiff(ctx context.Context, sourceID string, items map[string]sourceFileSnapshotItemEntity) (map[string]sourceFileSnapshotItemEntity, error) {
	if len(items) == 0 {
		return items, nil
	}
	paths := make([]string, 0, len(items))
	for path := range items {
		paths = append(paths, path)
	}
	treeToObject, err := s.cloudTreePathsToObjectPathsIncludingDeleted(ctx, sourceID, paths, true)
	if err != nil {
		return nil, err
	}
	out := make(map[string]sourceFileSnapshotItemEntity, len(items))
	for rawPath, item := range items {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		comparePath := path
		if objectPath := strings.TrimSpace(treeToObject[path]); objectPath != "" {
			comparePath = filepath.Clean(objectPath)
		}
		item.Path = comparePath
		if existing, ok := out[comparePath]; ok {
			if cloudSnapshotItemForDiffShouldReplace(existing, item, path, comparePath) {
				out[comparePath] = item
			}
			continue
		}
		out[comparePath] = item
	}
	return out, nil
}

func cloudSnapshotItemForDiffShouldReplace(existing, candidate sourceFileSnapshotItemEntity, candidateOriginalPath, comparePath string) bool {
	if filepath.Clean(strings.TrimSpace(candidateOriginalPath)) == filepath.Clean(strings.TrimSpace(comparePath)) {
		return true
	}
	if strings.TrimSpace(existing.Checksum) == "" && strings.TrimSpace(candidate.Checksum) != "" {
		return true
	}
	if existing.SizeBytes == 0 && candidate.SizeBytes != 0 {
		return true
	}
	if existing.ModTime == nil && candidate.ModTime != nil {
		return true
	}
	return false
}

func (s *Store) cloudTreeDisplayDiff(ctx context.Context, sourceID string, diff map[string]string, displayByComparePath map[string]string) map[string]string {
	out := make(map[string]string, len(diff))
	missingDisplayPaths := make([]string, 0, len(diff))
	for rawPath, update := range diff {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		if displayPath := strings.TrimSpace(displayByComparePath[path]); displayPath != "" {
			out[filepath.Clean(displayPath)] = update
			continue
		}
		missingDisplayPaths = append(missingDisplayPaths, path)
	}
	if len(missingDisplayPaths) == 0 {
		return out
	}
	objectToTree, err := s.cloudObjectPathsToTreePathsIncludingDeleted(ctx, sourceID, missingDisplayPaths, true)
	if err != nil {
		for _, path := range missingDisplayPaths {
			out[path] = diff[path]
		}
		return out
	}
	for _, path := range missingDisplayPaths {
		displayPath := filepath.Clean(strings.TrimSpace(objectToTree[path]))
		if displayPath == "" || displayPath == "." {
			displayPath = path
		}
		if _, exists := out[displayPath]; exists {
			continue
		}
		out[displayPath] = diff[path]
	}
	return out
}

const readOnlySelectionTokenPrefix = "sel_ro_"

type readOnlySelectionTokenPayload struct {
	Version   int               `json:"v"`
	SourceID  string            `json:"source_id"`
	TakenAt   int64             `json:"taken_at"`
	ExpiresAt int64             `json:"expires_at"`
	Diff      map[string]string `json:"diff"`
}

func encodeReadOnlySelectionToken(sourceID string, diffByPath map[string]string, now time.Time) (string, error) {
	payload := readOnlySelectionTokenPayload{
		Version:   1,
		SourceID:  strings.TrimSpace(sourceID),
		TakenAt:   now.UTC().UnixNano(),
		ExpiresAt: now.UTC().Add(selectionTokenTTL).Unix(),
		Diff:      normalizeSnapshotUpdateOverrides(diffByPath),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return readOnlySelectionTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeReadOnlySelectionToken(token string, now time.Time) (readOnlySelectionTokenPayload, bool, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, readOnlySelectionTokenPrefix) {
		return readOnlySelectionTokenPayload{}, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, readOnlySelectionTokenPrefix))
	if err != nil {
		return readOnlySelectionTokenPayload{}, true, err
	}
	var payload readOnlySelectionTokenPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return readOnlySelectionTokenPayload{}, true, err
	}
	if payload.Version != 1 || strings.TrimSpace(payload.SourceID) == "" {
		return readOnlySelectionTokenPayload{}, true, fmt.Errorf("invalid read-only selection token")
	}
	if payload.ExpiresAt > 0 && !now.UTC().Before(time.Unix(payload.ExpiresAt, 0).UTC()) {
		return readOnlySelectionTokenPayload{}, true, fmt.Errorf("expired read-only selection token")
	}
	payload.Diff = normalizeSnapshotUpdateOverrides(payload.Diff)
	return payload, true, nil
}

func (s *Store) createPreviewSnapshotAndDiff(ctx context.Context, src sourceEntity, scopeRoots []string, filePaths []string, fileStats map[string]model.TreeFileStat, selectionToken string) (map[string]string, error) {
	currentItems := make([]sourceFileSnapshotItemEntity, 0, len(filePaths))
	now := time.Now().UTC()
	seen := make(map[string]struct{}, len(filePaths))
	for _, rawPath := range filePaths {
		path := filepath.Clean(strings.TrimSpace(rawPath))
		if path == "" || path == "." {
			continue
		}
		if isTransientSourceFilePath(path, false) {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		stat := fileStats[path]
		if strings.TrimSpace(stat.Path) == "" {
			stat.Path = path
		}
		item := sourceFileSnapshotItemEntity{
			Path:      path,
			IsDir:     stat.IsDir,
			SizeBytes: stat.Size,
			Checksum:  strings.TrimSpace(stat.Checksum),
		}
		if stat.ModTime != nil && !stat.ModTime.IsZero() {
			mt := stat.ModTime.UTC()
			item.ModTime = &mt
		}
		currentItems = append(currentItems, item)
	}

	var relation sourceSnapshotRelationEntity
	if err := s.db.WithContext(ctx).Take(&relation, "source_id = ?", src.ID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		relation = sourceSnapshotRelationEntity{SourceID: src.ID}
	}

	baseItems, baseSnapshotID, err := s.snapshotItemsForDiffBase(ctx, src.ID, relation.LastCommittedSnapshotID)
	if err != nil {
		return nil, err
	}
	previewSnapshotID := sourceSnapshotID()
	expiresAt := now.Add(selectionTokenTTL)
	preview := sourceFileSnapshotEntity{
		SnapshotID:     previewSnapshotID,
		SourceID:       src.ID,
		TenantID:       src.TenantID,
		SnapshotType:   "PREVIEW",
		BaseSnapshotID: baseSnapshotID,
		SelectionToken: strings.TrimSpace(selectionToken),
		ExpiresAt:      &expiresAt,
		FileCount:      int64(len(currentItems)),
		CreatedAt:      now,
	}

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&preview).Error; err != nil {
			return err
		}
		if len(currentItems) > 0 {
			rows := make([]sourceFileSnapshotItemEntity, 0, len(currentItems))
			for _, item := range currentItems {
				item.SnapshotID = previewSnapshotID
				rows = append(rows, item)
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "source_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"last_preview_snapshot_id": previewSnapshotID,
				"updated_at":               now,
			}),
		}).Create(&sourceSnapshotRelationEntity{
			SourceID:              src.ID,
			LastPreviewSnapshotID: previewSnapshotID,
			UpdatedAt:             now,
		}).Error
	}); err != nil {
		return nil, err
	}

	if len(scopeRoots) > 0 {
		filtered := make(map[string]sourceFileSnapshotItemEntity, len(baseItems))
		for path, item := range baseItems {
			if isTransientSourceFilePath(path, item.IsDir) {
				continue
			}
			if pathInScope(path, scopeRoots) {
				filtered[path] = item
			}
		}
		baseItems = filtered
	} else {
		baseItems = filterTransientSnapshotItems(baseItems)
	}
	currentMap := make(map[string]sourceFileSnapshotItemEntity, len(currentItems))
	for _, item := range currentItems {
		currentMap[item.Path] = item
	}
	return diffSnapshotMaps(baseItems, currentMap), nil
}

func sourceSnapshotID() string {
	return fmt.Sprintf("ss_%d", time.Now().UTC().UnixNano())
}

func parseTaskIdempotencyKey(documentID int64, targetVersionID, taskAction string) string {
	return fmt.Sprintf(
		"doc:%d|ver:%s|action:%s",
		documentID,
		strings.TrimSpace(targetVersionID),
		normalizeTaskAction(taskAction),
	)
}

func normalizeTaskAction(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case taskActionDelete:
		return taskActionDelete
	case taskActionReparse:
		return taskActionReparse
	default:
		return taskActionCreate
	}
}

func inferTaskActionForDocument(doc documentEntity) string {
	if strings.EqualFold(strings.TrimSpace(doc.ParseStatus), "DELETED") {
		return taskActionDelete
	}
	if strings.TrimSpace(doc.CoreDocumentID) != "" {
		return taskActionReparse
	}
	return taskActionCreate
}

func diffSnapshotMaps(baseItems map[string]sourceFileSnapshotItemEntity, currentItems map[string]sourceFileSnapshotItemEntity) map[string]string {
	diff := make(map[string]string, len(baseItems)+len(currentItems))
	for path, current := range currentItems {
		base, ok := baseItems[path]
		if !ok {
			diff[path] = "NEW"
			continue
		}
		if snapshotItemChanged(base, current) {
			diff[path] = "MODIFIED"
			continue
		}
		diff[path] = "UNCHANGED"
	}
	for path := range baseItems {
		if _, ok := currentItems[path]; !ok {
			diff[path] = "DELETED"
		}
	}
	return diff
}

func snapshotItemChanged(base, current sourceFileSnapshotItemEntity) bool {
	if strings.TrimSpace(base.Checksum) != "" && strings.TrimSpace(current.Checksum) != "" {
		return strings.TrimSpace(base.Checksum) != strings.TrimSpace(current.Checksum)
	}
	if base.SizeBytes != current.SizeBytes {
		return true
	}
	if base.ModTime == nil && current.ModTime == nil {
		return false
	}
	if base.ModTime == nil || current.ModTime == nil {
		return true
	}
	return !base.ModTime.UTC().Equal(current.ModTime.UTC())
}

func (s *Store) snapshotItemsByPath(ctx context.Context, snapshotID string) (map[string]sourceFileSnapshotItemEntity, error) {
	return s.snapshotItemsByPathDB(s.db.WithContext(ctx), snapshotID)
}

func (s *Store) GenerateTasksForSource(ctx context.Context, sourceID string, req model.GenerateTasksRequest) (resp model.GenerateTasksResponse, retErr error) {
	var src sourceEntity
	if err := s.db.WithContext(ctx).First(&src, "id = ?", strings.TrimSpace(sourceID)).Error; err != nil {
		return resp, err
	}
	now := time.Now().UTC()
	job := manualPullJobEntity{
		JobID:          manualPullJobID(),
		TenantID:       src.TenantID,
		SourceID:       src.ID,
		Status:         "RUNNING",
		Mode:           strings.TrimSpace(req.Mode),
		TriggerPolicy:  strings.TrimSpace(req.TriggerPolicy),
		SelectionToken: strings.TrimSpace(req.SelectionToken),
		UpdatedOnly:    req.UpdatedOnly,
		RequestedCount: len(req.Paths),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if job.Mode == "" {
		job.Mode = "partial"
	}
	if err := s.db.WithContext(ctx).Create(&job).Error; err != nil {
		return resp, err
	}
	resp.ManualPullJobID = job.JobID
	defer func() {
		updates := map[string]any{
			"accepted_count":          resp.AcceptedCount,
			"skipped_count":           resp.SkippedCount,
			"ignored_unchanged_count": resp.IgnoredUnchangedCount,
			"updated_at":              time.Now().UTC(),
		}
		finishedAt := time.Now().UTC()
		if retErr != nil {
			updates["status"] = "FAILED"
			updates["error_message"] = retErr.Error()
		} else {
			updates["status"] = "SUCCEEDED"
			updates["error_message"] = ""
		}
		updates["finished_at"] = &finishedAt
		if err := s.db.WithContext(ctx).Model(&manualPullJobEntity{}).Where("job_id = ?", job.JobID).Updates(updates).Error; err != nil && s.log != nil {
			s.log.Warn("finalize manual pull job failed", zap.String("job_id", job.JobID), zap.Error(err))
		}
	}()

	resp.RequestedCount = len(req.Paths)
	rootPathForRequest := strings.TrimSpace(src.RootPath)
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		rootPathForRequest = sourcelayout.CloudMirrorRoot(src.RootPath)
	}
	paths, invalid := normalizePathsUnderRoot(req.Paths, rootPathForRequest)
	selectionToken := strings.TrimSpace(req.SelectionToken)

	var (
		selectedPreview   *sourceFileSnapshotEntity
		diffByPath        map[string]string
		selectionDiffUsed bool
		selectionTakenAt  time.Time
	)
	if selectionToken != "" {
		if payload, ok, err := decodeReadOnlySelectionToken(selectionToken, now); ok {
			if err != nil {
				return resp, fmt.Errorf("invalid selection_token")
			}
			if strings.TrimSpace(payload.SourceID) != src.ID {
				return resp, fmt.Errorf("invalid selection_token")
			}
			diffByPath = payload.Diff
			if payload.TakenAt > 0 {
				selectionTakenAt = time.Unix(0, payload.TakenAt).UTC()
			}
			selectionDiffUsed = true
		} else {
			preview, err := s.loadUsablePreviewSnapshotBySelectionToken(ctx, src.ID, selectionToken, now)
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return resp, fmt.Errorf("invalid selection_token")
				}
				return resp, err
			}
			diff, err := s.diffBySnapshotID(ctx, preview)
			if err != nil {
				return resp, err
			}
			selectedPreview = &preview
			diffByPath = diff
			selectionTakenAt = preview.CreatedAt.UTC()
			selectionDiffUsed = true
		}
	} else if !src.WatchEnabled {
		var relation sourceSnapshotRelationEntity
		if err := s.db.WithContext(ctx).Take(&relation, "source_id = ?", src.ID).Error; err == nil {
			if strings.TrimSpace(relation.LastPreviewSnapshotID) != "" {
				preview, err := s.loadSnapshotByID(ctx, relation.LastPreviewSnapshotID)
				if err == nil && strings.EqualFold(strings.TrimSpace(preview.SnapshotType), "PREVIEW") && preview.ConsumedAt == nil {
					diff, err := s.diffBySnapshotID(ctx, preview)
					if err != nil {
						return resp, err
					}
					selectedPreview = &preview
					diffByPath = diff
					selectionTakenAt = preview.CreatedAt.UTC()
					selectionDiffUsed = true
				}
			}
		}
	}
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) && selectionDiffUsed {
		var recovered []string
		paths, recovered, invalid = normalizeCloudRequestPathsWithSelection(req.Paths, rootPathForRequest, diffByPath)
		if len(recovered) > 0 {
			for _, path := range recovered {
				paths = append(paths, path)
			}
		}
	}
	resp.SkippedCount += invalid

	if selectionDiffUsed && selectionToken != "" {
		knownDeletedPaths := map[string]struct{}{}
		if src.WatchEnabled {
			var err error
			knownDeletedPaths, err = s.deletedDocumentPathSet(ctx, src.ID, paths)
			if err != nil {
				return resp, err
			}
		}
		stateDeletedPaths, err := s.sourceDeletedDocumentStatePaths(ctx, src.ID, nil)
		if err != nil {
			return resp, err
		}
		for _, path := range stateDeletedPaths {
			knownDeletedPaths[filepath.Clean(strings.TrimSpace(path))] = struct{}{}
		}
		unknownPaths := make([]string, 0, len(paths))
		for _, path := range paths {
			if _, ok := diffByPath[path]; !ok {
				if _, deleted := knownDeletedPaths[path]; deleted {
					diffByPath[path] = "DELETED"
					continue
				}
				unknownPaths = append(unknownPaths, path)
			}
		}
		if len(unknownPaths) > 0 {
			return resp, fmt.Errorf("paths not found in selection snapshot: %s", strings.Join(unknownPaths, ", "))
		}
	}

	if req.UpdatedOnly || selectionDiffUsed {
		if selectionDiffUsed {
			docMap, err := s.treeDocumentRowsByPath(ctx, src.ID, paths)
			if err != nil {
				return resp, err
			}
			queueMap, err := s.latestParseTasksForTreeDocumentRows(ctx, docMap)
			if err != nil {
				return resp, err
			}
			stateByPath, err := s.sourceDocumentStateByPaths(ctx, src.ID, paths)
			if err != nil {
				return resp, err
			}
			filtered, ignored := filterPathsByDiff(paths, diffByPath, docMap, queueMap, stateByPath, selectionTakenAt)
			resp.IgnoredUnchangedCount = ignored
			resp.SkippedCount += ignored
			paths = filtered
		} else {
			filtered, ignored, err := s.filterPathsByUpdatedOnly(ctx, src.ID, paths)
			if err != nil {
				return resp, err
			}
			resp.IgnoredUnchangedCount = ignored
			resp.SkippedCount += ignored
			paths = filtered
		}
	}
	consumeSelectedPreview := func(tx *gorm.DB) error {
		if selectedPreview == nil {
			return nil
		}
		if err := s.consumeSelectionTokenTx(tx, selectedPreview.SnapshotID, now); err != nil {
			return err
		}
		if src.WatchEnabled {
			return nil
		}
		return s.createResidualPreviewFromSelectionTx(tx, src.ID, *selectedPreview, selectedPreview.BaseSnapshotID, now)
	}
	if len(paths) == 0 {
		if selectedPreview != nil {
			if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return consumeSelectedPreview(tx)
			}); err != nil {
				return resp, err
			}
		}
		return resp, nil
	}
	eventPaths := append([]string(nil), paths...)
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		pathMap, err := s.cloudTreePathsToObjectPathsIncludingDeleted(ctx, src.ID, paths, true)
		if err != nil {
			return resp, err
		}
		for i, path := range paths {
			if mapped := strings.TrimSpace(pathMap[filepath.Clean(strings.TrimSpace(path))]); mapped != "" {
				eventPaths[i] = filepath.Clean(mapped)
			}
		}
	}
	pathEventType := make(map[string]string, len(paths))
	for _, path := range paths {
		pathEventType[path] = "modified"
	}
	stateByPath, err := s.sourceDocumentStateByPaths(ctx, src.ID, paths)
	if err != nil {
		return resp, err
	}
	for _, path := range paths {
		if strings.EqualFold(pendingSourceStateUpdateType(stateByPath[path]), "DELETED") {
			if selectionDiffUsed {
				stateDetectedAfterSelection := !selectionTakenAt.IsZero() && stateByPath[path].LastDetectedAt.After(selectionTakenAt.UTC())
				switch normalizeSnapshotUpdateType(diffByPath[filepath.Clean(strings.TrimSpace(path))]) {
				case "NEW", "MODIFIED":
					if !stateDetectedAfterSelection || src.WatchEnabled {
						continue
					}
				}
			}
			pathEventType[path] = "deleted"
		}
	}
	if selectionDiffUsed {
		for _, path := range paths {
			if strings.EqualFold(strings.TrimSpace(diffByPath[path]), "DELETED") {
				pathEventType[path] = "deleted"
			}
		}
	}
	{
		previewCurrentPaths := map[string]struct{}{}
		if src.WatchEnabled && selectedPreview != nil {
			previewItems, err := s.snapshotItemsByPath(ctx, selectedPreview.SnapshotID)
			if err != nil {
				return resp, err
			}
			for rawPath, item := range previewItems {
				if item.IsDir {
					continue
				}
				path := filepath.Clean(strings.TrimSpace(rawPath))
				if path == "" || path == "." {
					continue
				}
				previewCurrentPaths[path] = struct{}{}
			}
		} else if src.WatchEnabled && selectionDiffUsed {
			for rawPath, update := range diffByPath {
				if strings.EqualFold(strings.TrimSpace(update), "DELETED") {
					continue
				}
				path := filepath.Clean(strings.TrimSpace(rawPath))
				if path == "" || path == "." {
					continue
				}
				previewCurrentPaths[path] = struct{}{}
			}
		}
		var rows []struct {
			SourceObjectID string
			ParseStatus    string
		}
		queryPaths := append([]string(nil), paths...)
		querySeen := make(map[string]struct{}, len(queryPaths)+len(eventPaths))
		for _, rawPath := range queryPaths {
			path := filepath.Clean(strings.TrimSpace(rawPath))
			if path != "" && path != "." {
				querySeen[path] = struct{}{}
			}
		}
		for _, rawPath := range eventPaths {
			path := filepath.Clean(strings.TrimSpace(rawPath))
			if path == "" || path == "." {
				continue
			}
			if _, ok := querySeen[path]; ok {
				continue
			}
			querySeen[path] = struct{}{}
			queryPaths = append(queryPaths, path)
		}
		if err := s.db.WithContext(ctx).
			Table("documents").
			Select("source_object_id, parse_status").
			Where("source_id = ? AND source_object_id IN ?", src.ID, queryPaths).
			Scan(&rows).Error; err != nil {
			return resp, err
		}
		pathByEventPath := make(map[string]string, len(paths))
		for i, rawPath := range paths {
			path := filepath.Clean(strings.TrimSpace(rawPath))
			if i < len(eventPaths) && strings.TrimSpace(eventPaths[i]) != "" {
				pathByEventPath[filepath.Clean(strings.TrimSpace(eventPaths[i]))] = path
			}
		}
		for _, row := range rows {
			if strings.EqualFold(strings.TrimSpace(row.ParseStatus), "DELETED") {
				path := filepath.Clean(strings.TrimSpace(row.SourceObjectID))
				if _, existsNow := previewCurrentPaths[path]; existsNow {
					continue
				}
				if requestPath := strings.TrimSpace(pathByEventPath[path]); requestPath != "" {
					path = filepath.Clean(requestPath)
				}
				pathEventType[path] = "deleted"
			}
		}
	}
	events := make([]model.FileEvent, 0, len(paths))
	eventOriginRefs := map[string]string{}
	if sourcelayout.IsCloudOriginType(src.DefaultOriginType) {
		refs, err := s.cloudTreePathsToObjectRefsIncludingDeleted(ctx, src.ID, paths, true)
		if err != nil {
			return resp, err
		}
		for treePath, ref := range refs {
			if strings.TrimSpace(ref.ExternalObjectID) != "" {
				eventOriginRefs[filepath.Clean(strings.TrimSpace(treePath))] = strings.TrimSpace(ref.ExternalObjectID)
			}
		}
	}
	for i, p := range paths {
		eventType := normalizeEventType(pathEventType[p])
		eventPath := p
		if i < len(eventPaths) && strings.TrimSpace(eventPaths[i]) != "" {
			eventPath = eventPaths[i]
		}
		events = append(events, model.FileEvent{
			SourceID:       src.ID,
			EventType:      eventType,
			Path:           eventPath,
			IsDir:          false,
			OccurredAt:     now.Add(time.Duration(i) * time.Nanosecond),
			TriggerPolicy:  strings.TrimSpace(req.TriggerPolicy),
			OriginType:     firstNonEmpty(src.DefaultOriginType, string(model.OriginTypeLocalFS)),
			OriginPlatform: firstNonEmpty(src.DefaultOriginPlatform, "LOCAL"),
			OriginRef:      eventOriginRefs[filepath.Clean(strings.TrimSpace(p))],
		})
	}
	mutations, err := s.BuildMutationsFromEvents(ctx, events)
	if err != nil {
		return resp, err
	}
	for i := range mutations {
		mutations[i].ManualSync = true
	}
	resp.AcceptedCount = len(mutations)
	resp.SkippedCount += len(paths) - len(mutations)
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, m := range mutations {
			if err := applyDocumentMutation(tx, m, s.log); err != nil {
				return err
			}
			if err := upsertSourceDocumentStateFromMutationTx(tx, m, s.log); err != nil {
				return err
			}
		}
		if err := consumeSelectedPreview(tx); err != nil {
			return err
		}
		if err := enqueueSourceCommand(tx, src.AgentID, model.CommandSnapshotSource, model.SourcePayload{
			SourceID: src.ID,
			TenantID: src.TenantID,
			RootPath: src.RootPath,
			Reason:   "UPLOAD_BASELINE",
		}); err != nil {
			return err
		}
		resp.BaselineSnapshotQueued = true
		return nil
	}); err != nil {
		return resp, err
	}
	scheduleAt := now.Add(time.Duration(len(mutations)+1) * time.Nanosecond)
	if _, err := s.ScheduleDueParses(ctx, scheduleAt); err != nil {
		return resp, err
	}
	return resp, nil
}

func (s *Store) ListManualPullJobs(ctx context.Context, sourceID string, req model.ListManualPullJobsRequest) (model.ListManualPullJobsResponse, error) {
	resp := model.ListManualPullJobsResponse{
		Items: []model.ManualPullJob{},
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return resp, fmt.Errorf("source_id is required")
	}
	var src sourceEntity
	if err := s.db.WithContext(ctx).Take(&src, "id = ?", sourceID).Error; err != nil {
		return resp, err
	}
	page, pageSize := normalizePageAndSize(req.Page, req.PageSize)
	resp.Page = page
	resp.PageSize = pageSize
	query := s.db.WithContext(ctx).
		Model(&manualPullJobEntity{}).
		Where("source_id = ?", src.ID)
	if statuses := splitCSV(req.Status); len(statuses) > 0 {
		query = query.Where("status IN ?", statuses)
	}
	if err := query.Count(&resp.Total).Error; err != nil {
		return resp, err
	}
	var rows []manualPullJobEntity
	offset := (page - 1) * pageSize
	if err := query.
		Order("created_at DESC, job_id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		return resp, err
	}
	resp.Items = make([]model.ManualPullJob, 0, len(rows))
	for _, row := range rows {
		resp.Items = append(resp.Items, toModelManualPullJob(row))
	}
	return resp, nil
}

func (s *Store) EnableSourceWatch(ctx context.Context, sourceID string, req model.EnableWatchRequest) (model.Source, error) {
	var src sourceEntity
	if err := s.db.WithContext(ctx).First(&src, "id = ?", strings.TrimSpace(sourceID)).Error; err != nil {
		return model.Source{}, err
	}
	now := time.Now().UTC()
	switch {
	case strings.TrimSpace(req.ReconcileSchedule) != "":
		reconcile, reconcileSchedule, err := normalizeReconcilePolicy(req.ReconcileSeconds, req.ReconcileSchedule, src.ReconcileSeconds)
		if err != nil {
			return model.Source{}, err
		}
		src.ReconcileSeconds = reconcile
		src.ReconcileSchedule = reconcileSchedule
	case req.ReconcileSeconds > 0:
		src.ReconcileSeconds = req.ReconcileSeconds
		src.ReconcileSchedule = ""
	}
	src.Status = string(model.SourceStatusEnabled)
	src.WatchEnabled = true
	src.WatchUpdatedAt = &now
	src.UpdatedAt = now

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&src).Error; err != nil {
			return err
		}
		return enqueueSourceCommand(tx, src.AgentID, model.CommandStartSource, model.SourcePayload{
			SourceID:          src.ID,
			TenantID:          src.TenantID,
			RootPath:          src.RootPath,
			SkipInitialScan:   true,
			ReconcileSeconds:  src.ReconcileSeconds,
			ReconcileSchedule: src.ReconcileSchedule,
		})
	}); err != nil {
		return model.Source{}, err
	}
	return toModelSource(src), nil
}

func (s *Store) DisableSourceWatch(ctx context.Context, sourceID string) (model.Source, bool, error) {
	var src sourceEntity
	if err := s.db.WithContext(ctx).First(&src, "id = ?", strings.TrimSpace(sourceID)).Error; err != nil {
		return model.Source{}, false, err
	}
	now := time.Now().UTC()
	src.Status = string(model.SourceStatusDisabled)
	src.WatchEnabled = false
	src.WatchUpdatedAt = &now
	src.UpdatedAt = now

	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&src).Error; err != nil {
			return err
		}
		if err := enqueueSourceCommand(tx, src.AgentID, model.CommandSnapshotSource, model.SourcePayload{
			SourceID: src.ID,
			TenantID: src.TenantID,
			RootPath: src.RootPath,
			Reason:   "WATCH_STOP_BASELINE",
		}); err != nil {
			return err
		}
		return enqueueSourceCommand(tx, src.AgentID, model.CommandStopSource, model.SourcePayload{
			SourceID: src.ID,
			TenantID: src.TenantID,
			RootPath: src.RootPath,
		})
	}); err != nil {
		return model.Source{}, false, err
	}
	return toModelSource(src), true, nil
}

func (s *Store) ExpediteTasksByPaths(ctx context.Context, sourceID string, req model.ExpediteTasksRequest) (model.ExpediteTasksResponse, error) {
	var resp model.ExpediteTasksResponse
	var src sourceEntity
	if err := s.db.WithContext(ctx).First(&src, "id = ?", strings.TrimSpace(sourceID)).Error; err != nil {
		return resp, err
	}
	paths, invalid := normalizePathsUnderRoot(req.Paths, src.RootPath)
	resp.SkippedCount += invalid
	if len(paths) == 0 {
		return resp, nil
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, p := range paths {
			var doc documentEntity
			err := tx.Where("tenant_id = ? AND source_id = ? AND source_object_id = ?", src.TenantID, src.ID, p).Take(&doc).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					resp.SkippedCount++
					continue
				}
				return err
			}
			taskAction := inferTaskActionForDocument(doc)
			if taskAction != taskActionDelete && strings.TrimSpace(doc.DesiredVersionID) == "" {
				resp.SkippedCount++
				continue
			}
			if taskAction == taskActionDelete && strings.TrimSpace(doc.CoreDocumentID) == "" {
				resp.SkippedCount++
				continue
			}
			targetVersion := strings.TrimSpace(doc.DesiredVersionID)
			if targetVersion == "" {
				targetVersion = fmt.Sprintf("v_%d", now.UTC().UnixNano())
			}
			idempotencyKey := parseTaskIdempotencyKey(doc.ID, targetVersion, taskAction)

			updateRes := tx.Model(&parseTaskEntity{}).
				Where("document_id = ? AND status IN ?", doc.ID, []string{"PENDING", "RETRY_WAITING"}).
				Updates(map[string]any{
					"task_action":               taskAction,
					"status":                    "PENDING",
					"scan_orchestration_status": "PENDING",
					"next_run_at":               now,
					"retry_count":               0,
					"target_version_id":         targetVersion,
					"idempotency_key":           idempotencyKey,
					"core_document_id":          strings.TrimSpace(doc.CoreDocumentID),
					"lease_owner":               "",
					"lease_until":               nil,
					"updated_at":                now,
				})
			if updateRes.Error != nil {
				return updateRes.Error
			}
			if updateRes.RowsAffected > 0 {
				resp.UpdatedExistingTaskCount++
				docUpdates := map[string]any{
					"next_parse_at": nil,
					"updated_at":    now,
				}
				if taskAction == taskActionDelete {
					docUpdates["parse_status"] = "DELETED"
				} else {
					docUpdates["parse_status"] = "QUEUED"
				}
				if err := tx.Model(&documentEntity{}).Where("id = ?", doc.ID).Updates(docUpdates).Error; err != nil {
					return err
				}
				continue
			}
			task := parseTaskEntity{
				TenantID:                doc.TenantID,
				DocumentID:              doc.ID,
				TaskAction:              taskAction,
				TargetVersionID:         targetVersion,
				IdempotencyKey:          idempotencyKey,
				OriginType:              firstNonEmpty(doc.OriginType, string(model.OriginTypeLocalFS)),
				OriginPlatform:          firstNonEmpty(doc.OriginPlatform, "LOCAL"),
				TriggerPolicy:           firstNonEmpty(doc.TriggerPolicy, string(model.TriggerPolicyIdleWindow)),
				CoreDocumentID:          strings.TrimSpace(doc.CoreDocumentID),
				Status:                  "PENDING",
				ScanOrchestrationStatus: "PENDING",
				NextRunAt:               now,
				RetryCount:              0,
				MaxRetryCount:           8,
				CreatedAt:               now,
				UpdatedAt:               now,
			}
			if err := tx.Create(&task).Error; err != nil {
				if !isUniqueConstraintError(err) {
					return err
				}
				retryRes := tx.Model(&parseTaskEntity{}).
					Where("document_id = ? AND status IN ?", doc.ID, []string{"PENDING", "RETRY_WAITING"}).
					Updates(map[string]any{
						"task_action":               taskAction,
						"status":                    "PENDING",
						"scan_orchestration_status": "PENDING",
						"next_run_at":               now,
						"retry_count":               0,
						"target_version_id":         targetVersion,
						"idempotency_key":           idempotencyKey,
						"core_document_id":          strings.TrimSpace(doc.CoreDocumentID),
						"lease_owner":               "",
						"lease_until":               nil,
						"updated_at":                now,
					})
				if retryRes.Error != nil {
					return retryRes.Error
				}
				if retryRes.RowsAffected == 0 {
					resp.SkippedCount++
					continue
				}
				resp.UpdatedExistingTaskCount++
			} else {
				resp.CreatedTaskCount++
			}
			docUpdates := map[string]any{
				"next_parse_at": nil,
				"updated_at":    now,
			}
			if taskAction == taskActionDelete {
				docUpdates["parse_status"] = "DELETED"
			} else {
				docUpdates["parse_status"] = "QUEUED"
			}
			if err := tx.Model(&documentEntity{}).Where("id = ?", doc.ID).Updates(docUpdates).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return resp, err
	}
	return resp, nil
}

func (s *Store) RequeueEnabledSourcesOnStartup(ctx context.Context) (int, error) {
	var enabled []sourceEntity
	if err := s.db.WithContext(ctx).Where("status IN ? AND watch_enabled = ?", []string{string(model.SourceStatusEnabled), string(model.SourceStatusDegraded)}, true).Find(&enabled).Error; err != nil {
		return 0, err
	}
	queued := 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, src := range enabled {
			if err := enqueueSourceCommand(tx, src.AgentID, model.CommandStartSource, model.SourcePayload{
				SourceID:          src.ID,
				TenantID:          src.TenantID,
				RootPath:          src.RootPath,
				SkipInitialScan:   true,
				ReconcileSeconds:  src.ReconcileSeconds,
				ReconcileSchedule: src.ReconcileSchedule,
			}); err != nil {
				return err
			}
			queued++
		}
		return nil
	})
	return queued, err
}

func enqueueSourceCommand(tx *gorm.DB, agentID string, typ model.CommandType, payload model.SourcePayload) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	nextRetry := now
	cmd := agentCommandEntity{
		AgentID:      agentID,
		Type:         string(typ),
		Payload:      string(raw),
		Status:       commandStatusPending,
		NextRetryAt:  &nextRetry,
		AttemptCount: 0,
		CreatedAt:    now,
	}
	return tx.Create(&cmd).Error
}

func enqueueScanCommand(tx *gorm.DB, agentID string, payload model.SourcePayload, mode string) error {
	raw, err := json.Marshal(map[string]any{
		"source_id": payload.SourceID,
		"tenant_id": payload.TenantID,
		"root_path": payload.RootPath,
		"mode":      mode,
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	nextRetry := now
	cmd := agentCommandEntity{
		AgentID:      agentID,
		Type:         string(model.CommandScanSource),
		Payload:      string(raw),
		Status:       commandStatusPending,
		AttemptCount: 0,
		NextRetryAt:  &nextRetry,
		CreatedAt:    now,
	}
	return tx.Create(&cmd).Error
}
