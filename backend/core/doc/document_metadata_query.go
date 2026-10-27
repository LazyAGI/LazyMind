package doc

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"sort"
	"strings"
)

func loadMergedDocumentsWithDB(ctx context.Context, db, lazyDB *gorm.DB, docIDs []string, datasetID, keyword, pid string, applyPIDFilter bool, limit, offset int) ([]mergedDocRow, int64, error) {
	if len(docIDs) == 0 {
		return []mergedDocRow{}, 0, nil
	}

	var docs []orm.Document
	docQuery := db.WithContext(ctx).
		Where("deleted_at IS NULL").
		Where("(id IN ? OR lazyllm_doc_id IN ?)", docIDs, docIDs)
	if datasetID != "" {
		docQuery = docQuery.Where("dataset_id = ?", datasetID)
	}
	if applyPIDFilter {
		docQuery = docQuery.Where("COALESCE(p_id, '') = ?", pid)
	}
	if err := docQuery.Order("updated_at DESC").Find(&docs).Error; err != nil {
		return nil, 0, err
	}
	if len(docs) == 0 {
		return []mergedDocRow{}, 0, nil
	}

	datasetIDs := make([]string, 0, len(docs))
	datasetSeen := make(map[string]struct{}, len(docs))
	externalIDs := make([]string, 0, len(docs))
	for _, doc := range docs {
		if doc.DatasetID != "" {
			if _, ok := datasetSeen[doc.DatasetID]; !ok {
				datasetSeen[doc.DatasetID] = struct{}{}
				datasetIDs = append(datasetIDs, doc.DatasetID)
			}
		}
		if extID := strings.TrimSpace(doc.LazyllmDocID); extID != "" {
			externalIDs = append(externalIDs, extID)
		}
	}

	datasetDisplayByID := make(map[string]string, len(datasetIDs))
	if len(datasetIDs) > 0 {
		var datasets []orm.Dataset
		if err := db.WithContext(ctx).
			Where("id IN ? AND deleted_at IS NULL", datasetIDs).
			Find(&datasets).Error; err != nil {
			return nil, 0, err
		}
		for _, ds := range datasets {
			datasetDisplayByID[ds.ID] = strings.TrimSpace(ds.DisplayName)
		}
	}

	baseByExternalID := make(map[string]readonlyorm.LazyLLMDocRow, len(externalIDs))
	if len(externalIDs) > 0 {
		var baseRows []readonlyorm.LazyLLMDocRow
		baseQuery := lazyDB.WithContext(ctx).
			Table((readonlyorm.LazyLLMDocRow{}).TableName()).
			Where("doc_id IN ?", externalIDs)
		if keyword != "" && strings.TrimSpace(datasetID) == "" {
			like := "%" + strings.ToLower(strings.ReplaceAll(keyword, "%", "\\%")) + "%"
			baseQuery = baseQuery.Where("LOWER(filename) LIKE ? OR LOWER(path) LIKE ?", like, like)
		}
		if err := baseQuery.Find(&baseRows).Error; err != nil {
			return nil, 0, err
		}
		for _, row := range baseRows {
			baseByExternalID[row.DocID] = row
		}
	}

	latestTaskStatusByExternalID := make(map[string]string, len(externalIDs))
	if len(externalIDs) > 0 {
		var extTasks []readonlyorm.LazyLLMDocServiceTaskRow
		if err := lazyDB.WithContext(ctx).
			Table((readonlyorm.LazyLLMDocServiceTaskRow{}).TableName()).
			Where("doc_id IN ?", externalIDs).
			Order("updated_at DESC").
			Find(&extTasks).Error; err != nil {
			return nil, 0, err
		}
		for _, task := range extTasks {
			if _, ok := latestTaskStatusByExternalID[task.DocID]; !ok {
				latestTaskStatusByExternalID[task.DocID] = strings.TrimSpace(task.Status)
			}
		}
	}

	coreIDs := make([]string, 0, len(docs))
	docByID := make(map[string]orm.Document, len(docs))
	for _, doc := range docs {
		coreIDs = append(coreIDs, doc.ID)
		docByID[doc.ID] = doc
	}
	latestTaskDataSourceByExternalID := make(map[string]string, len(externalIDs))
	if len(coreIDs) > 0 {
		var coreTasks []orm.Task
		if err := db.WithContext(ctx).
			Where("doc_id IN ? AND deleted_at IS NULL", coreIDs).
			Order("updated_at DESC").
			Find(&coreTasks).Error; err != nil {
			return nil, 0, err
		}
		for _, task := range coreTasks {
			doc, ok := docByID[task.DocID]
			if !ok {
				continue
			}
			extID := strings.TrimSpace(doc.LazyllmDocID)
			if extID == "" {
				continue
			}
			if _, ok := latestTaskDataSourceByExternalID[extID]; ok {
				continue
			}
			var ext taskExt
			_ = json.Unmarshal(task.Ext, &ext)
			if s := strings.TrimSpace(ext.DataSourceType); s != "" {
				latestTaskDataSourceByExternalID[extID] = s
			}
		}
	}

	rows := make([]mergedDocRow, 0, len(docs))
	likeKeyword := strings.ToLower(strings.TrimSpace(keyword))
	for _, doc := range docs {
		datasetDisplay := datasetDisplayByID[doc.DatasetID]
		extID := strings.TrimSpace(doc.LazyllmDocID)
		if extID == "" {
			row := mergedDocRowFromCoreOnlyWithDatasetDisplay(doc, datasetDisplay)
			if likeKeyword != "" && !mergedDocMatchesKeyword(row, likeKeyword) {
				continue
			}
			rows = append(rows, row)
			continue
		}

		base, ok := baseByExternalID[extID]
		if !ok {
			row := mergedDocRowFromCoreOnlyWithDatasetDisplay(doc, datasetDisplay)
			if likeKeyword != "" && !mergedDocMatchesKeyword(row, likeKeyword) {
				continue
			}
			rows = append(rows, row)
			continue
		}

		displayName := strings.TrimSpace(base.Filename)
		if strings.TrimSpace(doc.DisplayName) != "" {
			displayName = strings.TrimSpace(doc.DisplayName)
		}
		documentSize := int64(0)
		if base.SizeBytes != nil {
			documentSize = int64(*base.SizeBytes)
		}
		relPath := relativePathFromFullPath(base.Path)
		documentStage := strings.TrimSpace(base.UploadStatus)
		if taskStatus, ok := latestTaskStatusByExternalID[extID]; ok && strings.TrimSpace(taskStatus) != "" {
			documentStage = strings.TrimSpace(taskStatus)
		}
		dataSourceType := strings.TrimSpace(latestTaskDataSourceByExternalID[extID])
		if dataSourceType == "" && strings.TrimSpace(base.SourceType) != "" {
			dataSourceType = dataSourceTypeFromSourceType(base.SourceType)
		}
		if dataSourceType == "" {
			dataSourceType = "LOCAL_FILE"
		}
		var dExt documentExt
		_ = json.Unmarshal(doc.Ext, &dExt)
		if dExt.FileSize > 0 {
			documentSize = dExt.FileSize
		}
		if strings.TrimSpace(dExt.RelativePath) != "" {
			relPath = strings.TrimSpace(dExt.RelativePath)
		}
		docType := resolveDocumentType(doc.DocumentType, firstNonEmpty(dExt.OriginalFilename, base.Filename))
		if strings.TrimSpace(dExt.StoredPath) != "" {
			base.Path = strings.TrimSpace(dExt.StoredPath)
		}
		row := mergedDocRow{
			DocID:            doc.ID,
			Filename:         base.Filename,
			Path:             base.Path,
			Ext:              doc.Ext,
			DatasetID:        doc.DatasetID,
			DatasetDisplay:   datasetDisplay,
			BaseCreatedAt:    firstNonZeroTime(doc.CreatedAt, base.CreatedAt),
			BaseUpdatedAt:    firstNonZeroTime(doc.UpdatedAt, base.UpdatedAt),
			DisplayName:      displayName,
			PID:              doc.PID,
			Tags:             doc.Tags,
			FileID:           doc.FileID,
			Creator:          doc.CreateUserName,
			DocumentSize:     documentSize,
			DataSourceType:   dataSourceType,
			Type:             docType,
			RelPath:          relPath,
			DocumentStage:    documentStage,
			PDFConvertResult: strings.TrimSpace(doc.PDFConvertResult),
		}
		if row.BaseCreatedAt.IsZero() {
			row.BaseCreatedAt = doc.CreatedAt
		}
		if row.BaseUpdatedAt.IsZero() {
			row.BaseUpdatedAt = doc.UpdatedAt
		}
		if likeKeyword != "" && !mergedDocMatchesKeyword(row, likeKeyword) {
			continue
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].BaseUpdatedAt.After(rows[j].BaseUpdatedAt) })
	total := int64(len(rows))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 20
	}
	if offset >= len(rows) {
		return []mergedDocRow{}, total, nil
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end], total, nil
}
