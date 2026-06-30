package evalset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"lazymind/core/acl"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

var (
	errEvalSetItemNotFound = errors.New("eval set item not found")
	errNoItemSelected      = errors.New("请先选择样本")
)

type ListEvalSetItemsFilter struct {
	Keyword      string
	QuestionType string
	Source       string
	Page         int
	PageSize     int
	OrderBy      string
}

const invalidReferenceScanBatchSize = 500

func (s *Service) requireEvalSetPermission(ctx context.Context, id, userID string, groupIDs []string, want string) (*orm.EvalSet, error) {
	row, err := s.repo.GetActive(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errEvalSetNotFound
		}
		return nil, err
	}
	perms := evalSetPermissionsForUser(row, userID, groupIDs)
	if want == acl.PermissionEvalSetRead && hasPermission(perms, acl.PermissionEvalSetWrite) {
		return row, nil
	}
	if !hasPermission(perms, want) {
		return nil, errForbidden
	}
	return row, nil
}

func (s *Service) ListItems(ctx context.Context, evalSet *orm.EvalSet, filter ListEvalSetItemsFilter) (*ListEvalSetItemsResponse, error) {
	normalizedFilter, err := normalizeListEvalSetItemsFilter(filter)
	if err != nil {
		return nil, err
	}
	filter = normalizedFilter

	items, total, err := s.repo.ListItems(ctx, evalSet.ID, evalSet.ShardID, filter)
	if err != nil {
		return nil, err
	}
	referenceDocIDs, err := s.repo.KnowledgeBaseReferenceDocIDs(
		ctx,
		parseDatasetIDsJSON(evalSet.DatasetIDs),
		collectReferenceDocIDs(items),
	)
	if err != nil {
		return nil, err
	}
	return &ListEvalSetItemsResponse{
		Items:    itemResponses(items, referenceDocIDs),
		Total:    total,
		Page:     filter.Page,
		PageSize: filter.PageSize,
	}, nil
}

func (s *Service) ListInvalidReferenceItems(ctx context.Context, evalSet *orm.EvalSet, filter ListEvalSetItemsFilter) (*ListEvalSetItemsResponse, error) {
	normalizedFilter, err := normalizeListEvalSetItemsFilter(filter)
	if err != nil {
		return nil, err
	}
	filter = normalizedFilter

	datasetIDs := parseDatasetIDsJSON(evalSet.DatasetIDs)
	offset := (filter.Page - 1) * filter.PageSize
	candidateOffset := 0
	invalidSeen := 0
	collected := make([]orm.EvalSetItem, 0, filter.PageSize+1)

	for len(collected) <= filter.PageSize {
		batch, err := s.repo.ListReferenceDocCandidateItems(
			ctx,
			evalSet.ID,
			evalSet.ShardID,
			filter,
			candidateOffset,
			invalidReferenceScanBatchSize,
		)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		candidateOffset += len(batch)

		referenceDocIDs, err := s.repo.KnowledgeBaseReferenceDocIDs(
			ctx,
			datasetIDs,
			collectReferenceDocIDs(batch),
		)
		if err != nil {
			return nil, err
		}
		for _, item := range batch {
			if !hasInvalidReferenceDoc(item.ReferenceDocIDs, referenceDocIDs) {
				continue
			}
			invalidSeen++
			if invalidSeen <= offset {
				continue
			}
			collected = append(collected, item)
			if len(collected) > filter.PageSize {
				break
			}
		}
		if len(batch) < invalidReferenceScanBatchSize {
			break
		}
	}

	hasMore := len(collected) > filter.PageSize
	if hasMore {
		collected = collected[:filter.PageSize]
	}

	referenceDocIDs, err := s.repo.KnowledgeBaseReferenceDocIDs(
		ctx,
		datasetIDs,
		collectReferenceDocIDs(collected),
	)
	if err != nil {
		return nil, err
	}
	return &ListEvalSetItemsResponse{
		Items:    itemResponses(collected, referenceDocIDs),
		Total:    int64(invalidSeen),
		Page:     filter.Page,
		PageSize: filter.PageSize,
		HasMore:  hasMore,
	}, nil
}

func normalizeListEvalSetItemsFilter(filter ListEvalSetItemsFilter) (ListEvalSetItemsFilter, error) {
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.QuestionType = strings.TrimSpace(filter.QuestionType)
	filter.Source = strings.TrimSpace(filter.Source)
	filter.OrderBy = strings.TrimSpace(filter.OrderBy)
	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 {
		filter.PageSize = 20
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	if filter.OrderBy == "" {
		filter.OrderBy = "created_at_desc"
	}
	if filter.OrderBy != "created_at_desc" && filter.OrderBy != "updated_at_desc" {
		return ListEvalSetItemsFilter{}, errors.New("unsupported order_by")
	}
	if filter.Source != "" && !isValidItemSource(filter.Source) {
		return ListEvalSetItemsFilter{}, errors.New("invalid source")
	}
	return filter, nil
}

func (s *Service) ListEvalSetQuestionTypes(ctx context.Context, evalSet *orm.EvalSet) (*QuestionTypeOptionsResponse, error) {
	values, err := s.repo.ListEvalSetQuestionTypes(ctx, evalSet.ID, evalSet.ShardID)
	if err != nil {
		return nil, err
	}
	items := make([]QuestionTypeOption, 0, len(values))
	for _, value := range values {
		items = append(items, QuestionTypeOption{Value: value, Label: value})
	}
	return &QuestionTypeOptionsResponse{Items: items}, nil
}

func (s *Service) CreateItem(ctx context.Context, evalSetID string, req CreateEvalSetItemRequest, userID, userName string) (*EvalSetItemResponse, error) {
	normalized, err := normalizeCreateItemRequest(req)
	if err != nil {
		return nil, err
	}
	item, err := s.repo.CreateItem(ctx, evalSetID, normalized, userID, userName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errEvalSetNotFound
		}
		return nil, err
	}
	return s.itemResponseForEvalSet(ctx, evalSetID, item)
}

func (s *Service) UpdateItem(ctx context.Context, evalSetID, itemID string, req UpdateEvalSetItemRequest) (*EvalSetItemResponse, error) {
	if !hasUpdateItemField(req) {
		return nil, errors.New("at least one field required")
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, errors.New("item_id required")
	}
	item, err := s.repo.UpdateItem(ctx, evalSetID, itemID, req)
	if err != nil {
		if errors.Is(err, errEvalSetItemNotFound) {
			return nil, errEvalSetItemNotFound
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errEvalSetNotFound
		}
		return nil, err
	}
	return s.itemResponseForEvalSet(ctx, evalSetID, item)
}

func (s *Service) DeleteItem(ctx context.Context, evalSetID, itemID string) error {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return errors.New("item_id required")
	}
	if err := s.repo.DeleteItem(ctx, evalSetID, itemID); err != nil {
		if errors.Is(err, errEvalSetItemNotFound) {
			return errEvalSetItemNotFound
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errEvalSetNotFound
		}
		return err
	}
	return nil
}

func (s *Service) BatchDeleteItems(ctx context.Context, evalSetID string, req BatchDeleteEvalSetItemsRequest) (int64, error) {
	itemIDs := normalizeItemIDs(req.ItemIDs)
	if len(itemIDs) == 0 {
		return 0, errNoItemSelected
	}
	deletedCount, err := s.repo.BatchDeleteItems(ctx, evalSetID, itemIDs)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errEvalSetNotFound
		}
		return 0, err
	}
	return deletedCount, nil
}

func (r *Repository) ListItems(ctx context.Context, evalSetID, shardID string, filter ListEvalSetItemsFilter) ([]orm.EvalSetItem, int64, error) {
	q := r.evalSetItemsQuery(ctx, evalSetID, shardID, filter)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []orm.EvalSetItem
	err := q.Order(listItemsOrderBy(filter)).
		Offset((filter.Page - 1) * filter.PageSize).
		Limit(filter.PageSize).
		Find(&rows).Error
	return rows, total, err
}

func (r *Repository) ListReferenceDocCandidateItems(ctx context.Context, evalSetID, shardID string, filter ListEvalSetItemsFilter, offset, limit int) ([]orm.EvalSetItem, error) {
	var rows []orm.EvalSetItem
	err := r.evalSetItemsQuery(ctx, evalSetID, shardID, filter).
		Where("reference_doc_ids <> ''").
		Order(listItemsOrderBy(filter)).
		Offset(offset).
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func (r *Repository) evalSetItemsQuery(ctx context.Context, evalSetID, shardID string, filter ListEvalSetItemsFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&orm.EvalSetItem{}).
		Where("shard_id = ? AND eval_set_id = ?", shardID, evalSetID)
	if filter.Keyword != "" {
		like := containsLikePattern(filter.Keyword)
		q = q.Where("(question LIKE ? ESCAPE '!' OR ground_truth LIKE ? ESCAPE '!')", like, like)
	}
	if filter.QuestionType != "" {
		q = q.Where("question_type = ?", filter.QuestionType)
	}
	if filter.Source != "" {
		q = q.Where("source = ?", filter.Source)
	}
	return q
}

func listItemsOrderBy(filter ListEvalSetItemsFilter) string {
	if filter.OrderBy == "updated_at_desc" {
		return "updated_at DESC, id DESC"
	}
	return "created_at DESC, id DESC"
}

func (r *Repository) ListEvalSetQuestionTypes(ctx context.Context, evalSetID, shardID string) ([]string, error) {
	var values []string
	err := r.db.WithContext(ctx).Model(&orm.EvalSetItem{}).
		Distinct("question_type").
		Where("shard_id = ? AND eval_set_id = ?", shardID, evalSetID).
		Where("question_type <> ''").
		Order("question_type ASC").
		Pluck("question_type", &values).Error
	return values, err
}

func (r *Repository) KnowledgeBaseReferenceDocIDs(ctx context.Context, datasetIDs, documentIDs []string) (map[string]struct{}, error) {
	datasetIDs = normalizeDatasetIDs(datasetIDs)
	documentIDs = normalizeItemIDs(documentIDs)
	out := make(map[string]struct{}, len(documentIDs))
	if len(datasetIDs) == 0 || len(documentIDs) == 0 {
		return out, nil
	}

	var rows []orm.Document
	if err := r.db.WithContext(ctx).
		Select("id").
		Where("id IN ? AND dataset_id IN ?", documentIDs, datasetIDs).
		Where("deleted_at IS NULL").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = struct{}{}
	}
	return out, nil
}

func (s *Service) itemResponseForEvalSet(ctx context.Context, evalSetID string, item *orm.EvalSetItem) (*EvalSetItemResponse, error) {
	evalSet, err := s.repo.GetActive(ctx, evalSetID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errEvalSetNotFound
		}
		return nil, err
	}
	referenceDocIDs, err := s.repo.KnowledgeBaseReferenceDocIDs(
		ctx,
		parseDatasetIDsJSON(evalSet.DatasetIDs),
		splitListIDs(item.ReferenceDocIDs),
	)
	if err != nil {
		return nil, err
	}
	return itemResponse(item, referenceDocIDs), nil
}

func (r *Repository) CreateItem(ctx context.Context, evalSetID string, req CreateEvalSetItemRequest, userID, userName string) (*orm.EvalSetItem, error) {
	var created orm.EvalSetItem
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		evalSet, shard, err := lockEvalSetAndShard(ctx, tx, evalSetID)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		created = orm.EvalSetItem{
			ShardID:                   evalSet.ShardID,
			ID:                        newEvalSetItemID(),
			EvalSetID:                 evalSet.ID,
			CaseID:                    req.CaseID,
			Question:                  req.Question,
			GroundTruth:               req.GroundTruth,
			QuestionType:              req.QuestionType,
			GenerateReason:            req.GenerateReason,
			KeyPoints:                 req.KeyPoints,
			ReferenceChunkIDs:         req.ReferenceChunkIDs,
			ReferenceContext:          req.ReferenceContext,
			AlgorithmReferenceContext: algorithmReferenceContextFromFrontend(req.ReferenceContext),
			ReferenceDoc:              req.ReferenceDoc,
			ReferenceDocIDs:           req.ReferenceDocIDs,
			Source:                    SourceManual,
			CreateUserID:              userID,
			CreateUserName:            userName,
			CreatedAt:                 now,
			UpdatedAt:                 now,
		}
		if req.IsDeleted != nil {
			created.IsDeleted = *req.IsDeleted
		}
		created.EstimatedBytes = estimateEvalSetItemBytes(&created)
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.EvalSet{}).
			Where("id = ? AND status = ?", evalSet.ID, StatusActive).
			Update("item_count", evalSet.ItemCount+1).Error; err != nil {
			return err
		}
		return updateShardCounters(tx, shard, 1, created.EstimatedBytes, now)
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (r *Repository) UpdateItem(ctx context.Context, evalSetID, itemID string, req UpdateEvalSetItemRequest) (*orm.EvalSetItem, error) {
	var updated orm.EvalSetItem
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		evalSet, shard, err := lockEvalSetAndShard(ctx, tx, evalSetID)
		if err != nil {
			return err
		}

		var locked orm.EvalSetItem
		if err := withUpdateLock(tx.WithContext(ctx)).
			Where("shard_id = ? AND eval_set_id = ? AND id = ?", evalSet.ShardID, evalSet.ID, itemID).
			First(&locked).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errEvalSetItemNotFound
			}
			return err
		}

		updated = locked
		applyUpdateItemRequest(&updated, req)
		if err := validateItemFields(&updated); err != nil {
			return err
		}
		now := time.Now().UTC()
		updated.UpdatedAt = now
		newBytes := estimateEvalSetItemBytes(&updated)
		values := map[string]any{
			"case_id":                     updated.CaseID,
			"question":                    updated.Question,
			"ground_truth":                updated.GroundTruth,
			"question_type":               updated.QuestionType,
			"generate_reason":             updated.GenerateReason,
			"key_points":                  updated.KeyPoints,
			"reference_chunk_ids":         updated.ReferenceChunkIDs,
			"reference_context":           updated.ReferenceContext,
			"algorithm_reference_context": updated.AlgorithmReferenceContext,
			"reference_doc":               updated.ReferenceDoc,
			"reference_doc_ids":           updated.ReferenceDocIDs,
			"is_deleted":                  updated.IsDeleted,
			"estimated_bytes":             newBytes,
			"updated_at":                  now,
		}
		if err := tx.Model(&orm.EvalSetItem{}).
			Where("shard_id = ? AND eval_set_id = ? AND id = ?", evalSet.ShardID, evalSet.ID, itemID).
			Updates(values).Error; err != nil {
			return err
		}
		if err := updateShardCounters(tx, shard, 0, newBytes-locked.EstimatedBytes, now); err != nil {
			return err
		}
		return tx.Where("shard_id = ? AND eval_set_id = ? AND id = ?", evalSet.ShardID, evalSet.ID, itemID).
			First(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (r *Repository) DeleteItem(ctx context.Context, evalSetID, itemID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		evalSet, shard, err := lockEvalSetAndShard(ctx, tx, evalSetID)
		if err != nil {
			return err
		}

		var item orm.EvalSetItem
		if err := withUpdateLock(tx.WithContext(ctx)).
			Where("shard_id = ? AND eval_set_id = ? AND id = ?", evalSet.ShardID, evalSet.ID, itemID).
			First(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errEvalSetItemNotFound
			}
			return err
		}
		if err := tx.Where("shard_id = ? AND eval_set_id = ? AND id = ?", evalSet.ShardID, evalSet.ID, itemID).
			Delete(&orm.EvalSetItem{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.EvalSet{}).
			Where("id = ? AND status = ?", evalSet.ID, StatusActive).
			Update("item_count", subtractInt64(evalSet.ItemCount, 1)).Error; err != nil {
			return err
		}
		return updateShardCounters(tx, shard, -1, -item.EstimatedBytes, time.Now().UTC())
	})
}

func (r *Repository) BatchDeleteItems(ctx context.Context, evalSetID string, itemIDs []string) (int64, error) {
	var deletedCount int64
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		evalSet, shard, err := lockEvalSetAndShard(ctx, tx, evalSetID)
		if err != nil {
			return err
		}

		var items []orm.EvalSetItem
		if err := withUpdateLock(tx.WithContext(ctx)).
			Where("shard_id = ? AND eval_set_id = ? AND id IN ?", evalSet.ShardID, evalSet.ID, itemIDs).
			Find(&items).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			deletedCount = 0
			return nil
		}

		ids := make([]string, 0, len(items))
		var deletedBytes int64
		for _, item := range items {
			ids = append(ids, item.ID)
			deletedBytes += item.EstimatedBytes
		}
		deletedCount = int64(len(items))
		if err := tx.Where("shard_id = ? AND eval_set_id = ? AND id IN ?", evalSet.ShardID, evalSet.ID, ids).
			Delete(&orm.EvalSetItem{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&orm.EvalSet{}).
			Where("id = ? AND status = ?", evalSet.ID, StatusActive).
			Update("item_count", subtractInt64(evalSet.ItemCount, deletedCount)).Error; err != nil {
			return err
		}
		return updateShardCounters(tx, shard, -deletedCount, -deletedBytes, time.Now().UTC())
	})
	return deletedCount, err
}

func lockEvalSetAndShard(ctx context.Context, tx *gorm.DB, evalSetID string) (*orm.EvalSet, *orm.EvalSetShard, error) {
	var evalSet orm.EvalSet
	if err := withUpdateLock(tx.WithContext(ctx)).
		Where("id = ? AND status = ?", evalSetID, StatusActive).
		First(&evalSet).Error; err != nil {
		return nil, nil, err
	}
	var shard orm.EvalSetShard
	if err := withUpdateLock(tx.WithContext(ctx)).
		Where("id = ?", evalSet.ShardID).
		First(&shard).Error; err != nil {
		return nil, nil, err
	}
	return &evalSet, &shard, nil
}

func updateShardCounters(tx *gorm.DB, shard *orm.EvalSetShard, rowDelta, byteDelta int64, now time.Time) error {
	actualRows := shard.ActualRows + rowDelta
	if actualRows < 0 {
		actualRows = 0
	}
	estimatedBytes := shard.EstimatedBytes + byteDelta
	if estimatedBytes < 0 {
		estimatedBytes = 0
	}
	updates := map[string]any{
		"actual_rows":     actualRows,
		"estimated_bytes": estimatedBytes,
		"updated_at":      now,
	}
	if actualRows >= shard.RowOpenThreshold || estimatedBytes >= shard.SizeOpenThresholdBytes {
		updates["status"] = ShardStatusSealed
		if shard.Status != ShardStatusSealed || shard.SealedAt == nil {
			updates["sealed_at"] = now
		}
	} else if shard.Status == ShardStatusSealed {
		updates["status"] = ShardStatusOpen
		updates["sealed_at"] = nil
	}
	return tx.Model(&orm.EvalSetShard{}).Where("id = ?", shard.ID).Updates(updates).Error
}

func normalizeCreateItemRequest(req CreateEvalSetItemRequest) (CreateEvalSetItemRequest, error) {
	req.CaseID = strings.TrimSpace(req.CaseID)
	req.Question = strings.TrimSpace(req.Question)
	req.GroundTruth = strings.TrimSpace(req.GroundTruth)
	req.QuestionType = strings.TrimSpace(req.QuestionType)
	req.GenerateReason = strings.TrimSpace(req.GenerateReason)
	req.KeyPoints = strings.TrimSpace(req.KeyPoints)
	req.ReferenceChunkIDs = strings.TrimSpace(req.ReferenceChunkIDs)
	req.ReferenceContext = strings.TrimSpace(req.ReferenceContext)
	req.ReferenceDoc = strings.TrimSpace(req.ReferenceDoc)
	req.ReferenceDocIDs = strings.TrimSpace(req.ReferenceDocIDs)
	if req.CaseID == "" {
		req.CaseID = "case_" + common.GenerateID()
	}
	item := orm.EvalSetItem{
		CaseID:            req.CaseID,
		Question:          req.Question,
		GroundTruth:       req.GroundTruth,
		QuestionType:      req.QuestionType,
		GenerateReason:    req.GenerateReason,
		KeyPoints:         req.KeyPoints,
		ReferenceChunkIDs: req.ReferenceChunkIDs,
		ReferenceContext:  req.ReferenceContext,
		ReferenceDoc:      req.ReferenceDoc,
		ReferenceDocIDs:   req.ReferenceDocIDs,
	}
	if err := validateItemFields(&item); err != nil {
		return CreateEvalSetItemRequest{}, err
	}
	return req, nil
}

func applyUpdateItemRequest(item *orm.EvalSetItem, req UpdateEvalSetItemRequest) {
	if req.CaseID != nil {
		item.CaseID = strings.TrimSpace(*req.CaseID)
		if item.CaseID == "" {
			item.CaseID = "case_" + common.GenerateID()
		}
	}
	if req.Question != nil {
		item.Question = strings.TrimSpace(*req.Question)
	}
	if req.GroundTruth != nil {
		item.GroundTruth = strings.TrimSpace(*req.GroundTruth)
	}
	if req.QuestionType != nil {
		item.QuestionType = strings.TrimSpace(*req.QuestionType)
	}
	if req.GenerateReason != nil {
		item.GenerateReason = strings.TrimSpace(*req.GenerateReason)
	}
	if req.KeyPoints != nil {
		item.KeyPoints = strings.TrimSpace(*req.KeyPoints)
	}
	if req.ReferenceChunkIDs != nil {
		item.ReferenceChunkIDs = strings.TrimSpace(*req.ReferenceChunkIDs)
	}
	if req.ReferenceContext != nil {
		item.ReferenceContext = strings.TrimSpace(*req.ReferenceContext)
		item.AlgorithmReferenceContext = algorithmReferenceContextFromFrontend(item.ReferenceContext)
	}
	if req.ReferenceDoc != nil {
		item.ReferenceDoc = strings.TrimSpace(*req.ReferenceDoc)
	}
	if req.ReferenceDocIDs != nil {
		item.ReferenceDocIDs = strings.TrimSpace(*req.ReferenceDocIDs)
	}
	if req.IsDeleted != nil {
		item.IsDeleted = *req.IsDeleted
	}
}

func validateItemFields(item *orm.EvalSetItem) error {
	if strings.TrimSpace(item.Question) == "" {
		return errors.New("question required")
	}
	if strings.TrimSpace(item.GroundTruth) == "" {
		return errors.New("ground_truth required")
	}
	if strings.TrimSpace(item.QuestionType) == "" {
		return errors.New("question_type required")
	}
	if utf8.RuneCountInString(item.QuestionType) > 128 {
		return errors.New("question_type too long")
	}
	if utf8.RuneCountInString(item.CaseID) > 255 {
		return errors.New("case_id too long")
	}
	return nil
}

func hasUpdateItemField(req UpdateEvalSetItemRequest) bool {
	return req.CaseID != nil ||
		req.Question != nil ||
		req.GroundTruth != nil ||
		req.QuestionType != nil ||
		req.GenerateReason != nil ||
		req.KeyPoints != nil ||
		req.ReferenceChunkIDs != nil ||
		req.ReferenceContext != nil ||
		req.ReferenceDoc != nil ||
		req.ReferenceDocIDs != nil ||
		req.IsDeleted != nil
}

func estimateEvalSetItemBytes(item *orm.EvalSetItem) int64 {
	var total int64
	fields := []string{
		item.ShardID,
		item.ID,
		item.EvalSetID,
		item.CaseID,
		item.Question,
		item.GroundTruth,
		item.QuestionType,
		item.GenerateReason,
		item.KeyPoints,
		item.ReferenceChunkIDs,
		item.ReferenceContext,
		item.AlgorithmReferenceContext,
		item.ReferenceDoc,
		item.ReferenceDocIDs,
		item.Source,
		item.SourceSessionID,
		item.SourceHistoryID,
		item.CreateUserID,
		item.CreateUserName,
	}
	for _, field := range fields {
		total += int64(len([]byte(field)))
	}
	return total
}

func algorithmReferenceContextFromFrontend(referenceContext string) string {
	normalized := normalizeReferenceContextText(referenceContext)
	var payload struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		Parts   []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"parts"`
	}
	if err := json.Unmarshal([]byte(normalized), &payload); err != nil || payload.Type != "reference_context" {
		return normalized
	}
	parts := make([]string, 0, len(payload.Parts))
	for _, part := range payload.Parts {
		if part.Type != "chunk" && part.Type != "text" {
			continue
		}
		content := normalizeReferenceContextText(part.Content)
		if content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func normalizeReferenceContextText(value string) string {
	return strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
}

func itemResponses(items []orm.EvalSetItem, knowledgeBaseReferenceDocIDs map[string]struct{}) []EvalSetItemResponse {
	out := make([]EvalSetItemResponse, 0, len(items))
	for i := range items {
		out = append(out, *itemResponse(&items[i], knowledgeBaseReferenceDocIDs))
	}
	return out
}

func itemResponse(item *orm.EvalSetItem, knowledgeBaseReferenceDocIDs map[string]struct{}) *EvalSetItemResponse {
	referenceDocInvalid := hasInvalidReferenceDoc(item.ReferenceDocIDs, knowledgeBaseReferenceDocIDs)
	referenceChunkSelected := len(splitListIDs(item.ReferenceChunkIDs)) > 0
	return &EvalSetItemResponse{
		ID:                            item.ID,
		EvalSetID:                     item.EvalSetID,
		ShardID:                       item.ShardID,
		CaseID:                        item.CaseID,
		Question:                      item.Question,
		GroundTruth:                   item.GroundTruth,
		QuestionType:                  item.QuestionType,
		GenerateReason:                item.GenerateReason,
		KeyPoints:                     item.KeyPoints,
		ReferenceChunkIDs:             item.ReferenceChunkIDs,
		ReferenceContext:              item.ReferenceContext,
		ReferenceDoc:                  item.ReferenceDoc,
		ReferenceDocIDs:               item.ReferenceDocIDs,
		ReferenceDocFromKnowledgeBase: hasReferenceDocInSet(item.ReferenceDocIDs, knowledgeBaseReferenceDocIDs),
		ReferenceDocInvalid:           referenceDocInvalid,
		ReferenceChunkSelected:        referenceChunkSelected,
		ReferenceChunkInvalid:         referenceChunkSelected && referenceDocInvalid,
		IsDeleted:                     item.IsDeleted,
		Source:                        item.Source,
		SourceSessionID:               item.SourceSessionID,
		SourceHistoryID:               item.SourceHistoryID,
		CreatedBy:                     item.CreateUserID,
		CreatedByName:                 item.CreateUserName,
		CreatedAt:                     item.CreatedAt,
		UpdatedAt:                     item.UpdatedAt,
	}
}

func collectReferenceDocIDs(items []orm.EvalSetItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, splitListIDs(item.ReferenceDocIDs)...)
	}
	return normalizeItemIDs(out)
}

func splitListIDs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return normalizeItemIDs(strings.Split(raw, ","))
}

func hasReferenceDocInSet(raw string, ids map[string]struct{}) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range splitListIDs(raw) {
		if _, ok := ids[id]; ok {
			return true
		}
	}
	return false
}

func hasInvalidReferenceDoc(raw string, ids map[string]struct{}) bool {
	docIDs := splitListIDs(raw)
	if len(docIDs) == 0 {
		return false
	}
	for _, id := range docIDs {
		if _, ok := ids[id]; !ok {
			return true
		}
	}
	return false
}

func isValidItemSource(source string) bool {
	switch source {
	case SourceUpload, SourceManual, SourceFlowback:
		return true
	default:
		return false
	}
}

func normalizeItemIDs(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func subtractInt64(value, delta int64) int64 {
	out := value - delta
	if out < 0 {
		return 0
	}
	return out
}
