package evalset

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

type Repository struct {
	db *gorm.DB
}

type ListFilter struct {
	Keyword    string
	DatasetIDs []string
	Page       int
	PageSize   int
}

type EvalSetUpdate struct {
	Name        *string
	Description *string
	DatasetIDs  *[]string
	GroupID     *string
}

func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) List(ctx context.Context, userID string, groupIDs []string, filter ListFilter) ([]orm.EvalSet, int64, error) {
	aclIDs, err := r.accessibleEvalSetIDs(ctx, userID, groupIDs)
	if err != nil {
		return nil, 0, err
	}

	q := r.db.WithContext(ctx).Model(&orm.EvalSet{}).Where("status = ?", StatusActive)
	accessParts := []string{"owner_id = ?"}
	accessArgs := []any{userID}
	if len(groupIDs) > 0 {
		accessParts = append(accessParts, "group_id IN ?")
		accessArgs = append(accessArgs, groupIDs)
	}
	if len(aclIDs) > 0 {
		accessParts = append(accessParts, "id IN ?")
		accessArgs = append(accessArgs, aclIDs)
	}
	q = q.Where("("+strings.Join(accessParts, " OR ")+")", accessArgs...)

	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		q = q.Where("(name LIKE ? OR description LIKE ?)", like, like)
	}
	var rows []orm.EvalSet
	if err := q.Order("updated_at DESC").Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	if len(filter.DatasetIDs) > 0 {
		rows = filterRowsByDatasetIDs(rows, filter.DatasetIDs)
	}
	total := int64(len(rows))
	start := (filter.Page - 1) * filter.PageSize
	if start >= len(rows) {
		return []orm.EvalSet{}, total, nil
	}
	end := start + filter.PageSize
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], total, nil
}

func (r *Repository) accessibleEvalSetIDs(ctx context.Context, userID string, groupIDs []string) ([]string, error) {
	q := r.db.WithContext(ctx).Model(&orm.ACLModel{}).
		Distinct("resource_id").
		Where("resource_type = ?", acl.ResourceTypeEvalSet).
		Where("permission IN ?", []string{acl.PermissionEvalSetRead, acl.PermissionEvalSetWrite}).
		Where("expires_at IS NULL OR expires_at > ?", time.Now())

	if len(groupIDs) > 0 {
		q = q.Where(
			"(grantee_type = ? AND target_id = ?) OR (grantee_type IN ? AND target_id IN ?)",
			acl.GranteeUser,
			userID,
			[]string{acl.GranteeGroup, acl.GranteeTenant},
			groupIDs,
		)
	} else {
		q = q.Where("grantee_type = ? AND target_id = ?", acl.GranteeUser, userID)
	}

	var ids []string
	if err := q.Pluck("resource_id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func (r *Repository) GetActive(ctx context.Context, id string) (*orm.EvalSet, error) {
	var row orm.EvalSet
	if err := r.db.WithContext(ctx).Where("id = ? AND status = ?", id, StatusActive).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *Repository) Create(ctx context.Context, req CreateEvalSetRequest, userID, userName string) (*orm.EvalSet, error) {
	var created orm.EvalSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		shard, err := r.allocateShard(ctx, tx)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		created = orm.EvalSet{
			ID:             newEvalSetID(),
			Name:           req.Name,
			Description:    req.Description,
			DatasetIDs:     datasetIDsJSON(req.DatasetIDs),
			OwnerID:        userID,
			GroupID:        req.GroupID,
			ShardID:        shard.ID,
			Status:         StatusActive,
			ItemCount:      0,
			CreateUserID:   userID,
			CreateUserName: userName,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		if err := insertACLRows(tx, created.ID, acl.GranteeUser, userID, userID, now); err != nil {
			return err
		}
		if req.GroupID != "" {
			if err := insertACLRows(tx, created.ID, acl.GranteeGroup, req.GroupID, userID, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

func (r *Repository) allocateShard(ctx context.Context, tx *gorm.DB) (*orm.EvalSetShard, error) {
	var shard orm.EvalSetShard
	q := withUpdateLock(tx.WithContext(ctx)).
		Where("status = ? AND actual_rows < row_open_threshold AND estimated_bytes < size_open_threshold_bytes", ShardStatusOpen).
		Order("created_at ASC")
	if err := q.First(&shard).Error; err == nil {
		return &shard, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	shard = orm.EvalSetShard{
		ID:                     newShardID(),
		Status:                 ShardStatusOpen,
		RowLimit:               DefaultShardRowLimit,
		RowOpenThreshold:       DefaultShardRowOpenThreshold,
		SizeLimitBytes:         DefaultShardSizeLimitBytes,
		SizeOpenThresholdBytes: DefaultShardSizeOpenThreshold,
		ActualRows:             0,
		EstimatedBytes:         0,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	if err := tx.WithContext(ctx).Create(&shard).Error; err != nil {
		return nil, err
	}
	if tx.Dialector.Name() == "postgres" {
		if err := tx.WithContext(ctx).Exec(createPartitionSQL(shard.ID)).Error; err != nil {
			return nil, err
		}
	}
	return &shard, nil
}

func (r *Repository) Update(ctx context.Context, id string, update EvalSetUpdate, userID string) (*orm.EvalSet, error) {
	var updated orm.EvalSet
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked orm.EvalSet
		if err := withUpdateLock(tx.WithContext(ctx)).
			Where("id = ? AND status = ?", id, StatusActive).
			First(&locked).Error; err != nil {
			return err
		}

		values := map[string]any{"updated_at": time.Now().UTC()}
		if update.Name != nil {
			values["name"] = *update.Name
		}
		if update.Description != nil {
			values["description"] = *update.Description
		}
		if update.DatasetIDs != nil {
			values["dataset_ids"] = datasetIDsJSON(*update.DatasetIDs)
		}
		if update.GroupID != nil {
			values["group_id"] = *update.GroupID
			if locked.GroupID != *update.GroupID {
				if locked.GroupID != "" {
					if err := tx.Where(
						"resource_type = ? AND resource_id = ? AND grantee_type IN ? AND target_id = ?",
						acl.ResourceTypeEvalSet,
						id,
						[]string{acl.GranteeGroup, acl.GranteeTenant},
						locked.GroupID,
					).Delete(&orm.ACLModel{}).Error; err != nil {
						return err
					}
				}
				if *update.GroupID != "" {
					if err := insertACLRows(tx, id, acl.GranteeGroup, *update.GroupID, userID, time.Now().UTC()); err != nil {
						return err
					}
				}
			}
		}

		if err := tx.Model(&orm.EvalSet{}).Where("id = ?", id).Updates(values).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND status = ?", id, StatusActive).First(&updated).Error
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

func (r *Repository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var evalSet orm.EvalSet
		if err := withUpdateLock(tx.WithContext(ctx)).
			Where("id = ? AND status = ?", id, StatusActive).
			First(&evalSet).Error; err != nil {
			return err
		}

		var shard orm.EvalSetShard
		if err := withUpdateLock(tx.WithContext(ctx)).
			Where("id = ?", evalSet.ShardID).
			First(&shard).Error; err != nil {
			return err
		}

		var stats struct {
			Count int64
			Bytes int64
		}
		if err := tx.Model(&orm.EvalSetItem{}).
			Select("COUNT(*) AS count, COALESCE(SUM(estimated_bytes), 0) AS bytes").
			Where("shard_id = ? AND eval_set_id = ?", evalSet.ShardID, evalSet.ID).
			Scan(&stats).Error; err != nil {
			return err
		}
		if err := tx.Where("shard_id = ? AND eval_set_id = ?", evalSet.ShardID, evalSet.ID).
			Delete(&orm.EvalSetItem{}).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_type = ? AND resource_id = ?", acl.ResourceTypeEvalSet, evalSet.ID).
			Delete(&orm.ACLModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", evalSet.ID).Delete(&orm.EvalSet{}).Error; err != nil {
			return err
		}

		actualRows := shard.ActualRows - stats.Count
		if actualRows < 0 {
			actualRows = 0
		}
		estimatedBytes := shard.EstimatedBytes - stats.Bytes
		if estimatedBytes < 0 {
			estimatedBytes = 0
		}
		updates := map[string]any{
			"actual_rows":     actualRows,
			"estimated_bytes": estimatedBytes,
			"updated_at":      time.Now().UTC(),
		}
		if shard.Status == ShardStatusSealed &&
			actualRows < shard.RowOpenThreshold &&
			estimatedBytes < shard.SizeOpenThresholdBytes {
			updates["status"] = ShardStatusOpen
			updates["sealed_at"] = nil
		}
		return tx.Model(&orm.EvalSetShard{}).Where("id = ?", shard.ID).Updates(updates).Error
	})
}

func (r *Repository) DatasetNames(ctx context.Context, datasetIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(datasetIDs))
	if len(datasetIDs) == 0 {
		return out, nil
	}

	var kbs []orm.KBModel
	if err := r.db.WithContext(ctx).Where("id IN ?", datasetIDs).Find(&kbs).Error; err != nil {
		return nil, err
	}
	for _, kb := range kbs {
		out[kb.ID] = kb.Name
	}

	missing := make([]string, 0)
	for _, id := range datasetIDs {
		if _, ok := out[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}

	var datasets []orm.Dataset
	if err := r.db.WithContext(ctx).
		Select("id, display_name").
		Where("id IN ? AND deleted_at IS NULL", missing).
		Find(&datasets).Error; err != nil {
		return nil, err
	}
	for _, ds := range datasets {
		out[ds.ID] = ds.DisplayName
	}
	return out, nil
}

func (r *Repository) ListKBOptions(ctx context.Context) ([]orm.KBModel, error) {
	var rows []orm.KBModel
	err := r.db.WithContext(ctx).Order("name ASC, id ASC").Find(&rows).Error
	return rows, err
}

func insertACLRows(tx *gorm.DB, evalSetID, granteeType, targetID, createdBy string, now time.Time) error {
	if strings.TrimSpace(targetID) == "" {
		return nil
	}
	rows := []orm.ACLModel{
		{
			ResourceType: acl.ResourceTypeEvalSet,
			ResourceID:   evalSetID,
			GranteeType:  granteeType,
			TargetID:     targetID,
			Permission:   acl.PermissionEvalSetRead,
			CreatedBy:    createdBy,
			CreatedAt:    now,
		},
		{
			ResourceType: acl.ResourceTypeEvalSet,
			ResourceID:   evalSetID,
			GranteeType:  granteeType,
			TargetID:     targetID,
			Permission:   acl.PermissionEvalSetWrite,
			CreatedBy:    createdBy,
			CreatedAt:    now,
		},
	}
	return tx.Create(&rows).Error
}

func withUpdateLock(db *gorm.DB) *gorm.DB {
	switch db.Dialector.Name() {
	case "postgres", "mysql":
		return db.Clauses(clause.Locking{Strength: "UPDATE"})
	default:
		return db
	}
}
