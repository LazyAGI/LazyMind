package service

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"

	skillsearch "lazymind/core/skillv2/search"
)

const (
	skillDraftStatusPendingConfirm = "pending_confirm"
	skillDraftStatusPending        = "pending"
	skillDraftStatusAutoPending    = "auto_pending"
)

func NewSkillService(deps SkillServiceDeps) *SkillService {
	clock := deps.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &SkillService{
		db:          deps.DB,
		uploadStore: deps.UploadStore,
		downloader:  deps.Downloader,
		blobStore:   deps.BlobStore,
		clock:       clock,
	}
}

func (s *SkillService) CreateSkill(ctx context.Context, req CreateSkillRequest) (CreateSkillResponse, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Category = strings.TrimSpace(req.Category)
	req.Description = strings.TrimSpace(req.Description)
	if err := validateSkillIdentity(req.Name, req.Category); err != nil {
		return CreateSkillResponse{}, err
	}
	files, sourceRefType, sourceRefID, err := s.filesFromSource(ctx, req.OwnerUserID, req.Source)
	if err != nil {
		return CreateSkillResponse{}, err
	}
	if err := validateSkillFiles(files); err != nil {
		return CreateSkillResponse{}, err
	}
	if err := validateSkillPackageMetadata(req.Name, req.Category, req.Description, files); err != nil {
		return CreateSkillResponse{}, err
	}

	skillID := newID()
	revisionID := newID()
	now := s.clock.Now()
	tags, _ := json.Marshal(req.Tags)
	enabled := true
	if req.IsEnabled != nil {
		enabled = *req.IsEnabled
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&skillRow{
			ID:                    skillID,
			OwnerUserID:           req.OwnerUserID,
			OwnerUserName:         req.OwnerUserName,
			CreateUserID:          req.CreateUserID,
			CreateUserName:        req.CreateUserName,
			Category:              req.Category,
			SkillName:             req.Name,
			OriginBuiltinSkillUID: strings.TrimSpace(req.OriginBuiltinSkillUID),
			Description:           req.Description,
			Tags:                  tags,
			RelativeRoot:          path.Join(req.Category, req.Name),
			SkillMDPath:           "SKILL.md",
			HeadRevisionID:        &revisionID,
			Version:               1,
			AutoEvo:               req.AutoEvo,
			AutoEvoApplyStatus:    "idle",
			IsEnabled:             enabled,
			UpdateStatus:          "up_to_date",
			CreatedAt:             now,
			UpdatedAt:             now,
		}).Error; err != nil {
			return err
		}
		if !enabled {
			if err := tx.WithContext(ctx).Model(&skillRow{}).Where("id = ?", skillID).Update("is_enabled", false).Error; err != nil {
				return err
			}
		}
		if err := s.createRevision(ctx, tx, revisionSpec{
			ID:            revisionID,
			SkillID:       skillID,
			RevisionNo:    1,
			ChangeSource:  "create",
			SourceRefType: sourceRefType,
			SourceRefID:   sourceRefID,
			Files:         files,
			CreatedBy:     req.CreateUserID,
		}); err != nil {
			return err
		}
		if err := s.resetDraft(tx, skillID, revisionID); err != nil {
			return err
		}
		return skillsearch.RebuildSkillTx(ctx, tx, skillID, now)
	})
	if err != nil {
		return CreateSkillResponse{}, err
	}
	return CreateSkillResponse{SkillID: skillID, HeadRevisionID: revisionID}, nil
}

func (s *SkillService) PatchSkill(ctx context.Context, req PatchSkillRequest) (PatchSkillResponse, error) {
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if err := validatePathSegment(name); err != nil {
			return PatchSkillResponse{}, err
		}
		req.Name = &name
	}
	if req.Category != nil {
		category := strings.TrimSpace(*req.Category)
		if err := validatePathSegment(category); err != nil {
			return PatchSkillResponse{}, err
		}
		req.Category = &category
	}
	if req.Description != nil {
		description := strings.TrimSpace(*req.Description)
		req.Description = &description
	}
	var out PatchSkillResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skill skillRow
		if err := tx.Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", req.SkillID, req.UserID).Take(&skill).Error; err != nil {
			return err
		}

		if req.Source == nil {
			updates := map[string]any{"updated_at": s.clock.Now()}
			headRevisionID := ""
			if skill.HeadRevisionID != nil {
				headRevisionID = *skill.HeadRevisionID
			}
			committedDraftRevisionID := ""
			metadataChanged :=
				(req.Name != nil && *req.Name != skill.SkillName) ||
					(req.Category != nil && *req.Category != skill.Category) ||
					(req.Description != nil && *req.Description != skill.Description)
			if metadataChanged {
				var draftEntries int64
				if err := tx.Model(&skillDraftEntryRow{}).Where("skill_id = ?", req.SkillID).Count(&draftEntries).Error; err != nil {
					return err
				}
				if draftEntries > 0 {
					return fmt.Errorf("cannot update skill metadata while draft overlay exists")
				}
				if skill.HeadRevisionID == nil {
					return fmt.Errorf("skill has no head revision")
				}
				files, err := s.filesForRevision(ctx, tx, *skill.HeadRevisionID)
				if err != nil {
					return err
				}
				content, ok := files["SKILL.md"]
				if !ok {
					return fmt.Errorf("skill package must contain SKILL.md")
				}
				nextName := valueOr(req.Name, skill.SkillName)
				nextCategory := valueOr(req.Category, skill.Category)
				nextDescription := valueOr(req.Description, skill.Description)
				files = map[string][]byte{
					"SKILL.md": []byte(rewriteSkillMDFrontmatter(string(content), nextName, nextCategory, nextDescription)),
				}
				revisionID, err := s.commitFilesAsNewHead(ctx, tx, req.SkillID, req.UserID, "metadata_update", files)
				if err != nil {
					return err
				}
				headRevisionID = revisionID
				if err := s.resetDraft(tx, req.SkillID, revisionID); err != nil {
					return err
				}
			}
			if req.Name != nil {
				updates["skill_name"] = *req.Name
				updates["relative_root"] = path.Join(valueOr(req.Category, skill.Category), *req.Name)
			}
			if req.Category != nil {
				updates["category"] = *req.Category
				updates["relative_root"] = path.Join(*req.Category, valueOr(req.Name, skill.SkillName))
			}
			if req.Description != nil {
				updates["description"] = *req.Description
			}
			if req.Tags != nil {
				tags, _ := json.Marshal(*req.Tags)
				updates["tags"] = tags
			}
			if req.AutoEvo != nil {
				updates["auto_evo"] = *req.AutoEvo
				updates["auto_evo_generation"] = gorm.Expr("auto_evo_generation + 1")
				updates["auto_evo_apply_status"] = "idle"
				updates["auto_evo_error"] = ""
				if *req.AutoEvo {
					updates["auto_evo_finished_at"] = nil
				} else {
					updates["auto_evo_started_at"] = nil
					updates["auto_evo_finished_at"] = s.clock.Now()
				}
			}
			if req.AutoEvo != nil && !skill.AutoEvo && *req.AutoEvo {
				if err := markPendingSkillDraftAuto(ctx, tx, req.SkillID, s.clock.Now()); err != nil {
					return err
				}
			}
			if req.IsEnabled != nil {
				if *req.IsEnabled {
					shouldPrepareEnable := !skill.IsEnabled
					if !shouldPrepareEnable {
						if err := ensurePublishedSkillMD(ctx, tx, skill); err != nil {
							shouldPrepareEnable = true
						}
					}
					if shouldPrepareEnable {
						revisionID, committed, err := s.prepareEnableSkill(ctx, tx, skill, req.UserID)
						if err != nil {
							return err
						}
						headRevisionID = revisionID
						if committed {
							committedDraftRevisionID = revisionID
							updates["head_revision_id"] = revisionID
							updates["version"] = gorm.Expr("version + 1")
						}
					}
				}
				updates["is_enabled"] = *req.IsEnabled
			}
			if err := tx.Model(&skillRow{}).Where("id = ? AND deleted_at IS NULL", req.SkillID).Updates(updates).Error; err != nil {
				return err
			}
			if committedDraftRevisionID != "" {
				if err := s.resetDraft(tx, req.SkillID, committedDraftRevisionID); err != nil {
					return err
				}
			}
			if err := skillsearch.RebuildSkillTx(ctx, tx, req.SkillID, s.clock.Now()); err != nil {
				return err
			}
			out = PatchSkillResponse{SkillID: req.SkillID, HeadRevisionID: headRevisionID}
			return nil
		}

		var draftEntries int64
		if err := tx.Model(&skillDraftEntryRow{}).Where("skill_id = ?", req.SkillID).Count(&draftEntries).Error; err != nil {
			return err
		}
		if draftEntries > 0 {
			return fmt.Errorf("cannot replace source while draft overlay exists")
		}
		files, sourceRefType, sourceRefID, err := s.filesFromSource(ctx, skill.OwnerUserID, *req.Source)
		if err != nil {
			return err
		}
		if err := validateSkillFiles(files); err != nil {
			return err
		}
		nextName := skill.SkillName
		nextCategory := skill.Category
		nextDescription := skill.Description
		if req.Name != nil {
			nextName = *req.Name
		}
		if req.Category != nil {
			nextCategory = *req.Category
		}
		if req.Description != nil {
			nextDescription = *req.Description
		}
		if err := validateSkillPackageMetadata(nextName, nextCategory, nextDescription, files); err != nil {
			return err
		}
		parentID := ""
		if skill.HeadRevisionID != nil {
			parentID = *skill.HeadRevisionID
		}
		nextNo, err := s.nextRevisionNo(tx, req.SkillID)
		if err != nil {
			return err
		}
		revisionID := newID()
		if err := s.createRevision(ctx, tx, revisionSpec{
			ID:               revisionID,
			SkillID:          req.SkillID,
			ParentRevisionID: parentID,
			RevisionNo:       nextNo,
			ChangeSource:     "direct_import",
			SourceRefType:    sourceRefType,
			SourceRefID:      sourceRefID,
			Files:            files,
			CreatedBy:        req.UserID,
		}); err != nil {
			return err
		}
		updates := map[string]any{
			"head_revision_id": revisionID,
			"version":          gorm.Expr("version + 1"),
			"updated_at":       s.clock.Now(),
		}
		if req.Name != nil {
			updates["skill_name"] = nextName
		}
		if req.Category != nil {
			updates["category"] = nextCategory
		}
		if req.Name != nil || req.Category != nil {
			updates["relative_root"] = path.Join(nextCategory, nextName)
		}
		if req.Description != nil {
			updates["description"] = *req.Description
		}
		if req.Tags != nil {
			tags, _ := json.Marshal(*req.Tags)
			updates["tags"] = tags
		}
		if req.AutoEvo != nil {
			updates["auto_evo"] = *req.AutoEvo
			updates["auto_evo_generation"] = gorm.Expr("auto_evo_generation + 1")
			updates["auto_evo_apply_status"] = "idle"
			updates["auto_evo_error"] = ""
			if *req.AutoEvo {
				updates["auto_evo_finished_at"] = nil
			} else {
				updates["auto_evo_started_at"] = nil
				updates["auto_evo_finished_at"] = s.clock.Now()
			}
		}
		if req.IsEnabled != nil {
			updates["is_enabled"] = *req.IsEnabled
		}
		if err := tx.Model(&skillRow{}).Where("id = ? AND deleted_at IS NULL", req.SkillID).Updates(updates).Error; err != nil {
			return err
		}
		if err := s.resetDraft(tx, req.SkillID, revisionID); err != nil {
			return err
		}
		if err := skillsearch.RebuildSkillTx(ctx, tx, req.SkillID, s.clock.Now()); err != nil {
			return err
		}
		out = PatchSkillResponse{SkillID: req.SkillID, HeadRevisionID: revisionID}
		return nil
	})
	return out, err
}

func (s *SkillService) DeleteSkill(ctx context.Context, req DeleteSkillRequest) error {
	return s.TrashSkill(ctx, req)
}

func (s *SkillService) ListTrashedSkills(ctx context.Context, req ListSkillsRequest) (ListSkillsResponse, error) {
	var rows []skillRow
	if err := s.db.WithContext(ctx).Where("owner_user_id = ? AND deleted_at IS NOT NULL", req.UserID).Order("deleted_at DESC, updated_at DESC, id ASC").Find(&rows).Error; err != nil {
		return ListSkillsResponse{}, err
	}
	items := make([]SkillSummary, 0, len(rows))
	for _, row := range rows {
		summary, err := s.summaryFor(ctx, row)
		if err != nil {
			return ListSkillsResponse{}, err
		}
		items = append(items, summary)
	}
	return ListSkillsResponse{Items: items}, nil
}

func (s *SkillService) TrashSkill(ctx context.Context, req DeleteSkillRequest) error {
	now := s.clock.Now()
	updates := map[string]any{
		"deleted_at": now,
		"updated_at": now,
	}
	if req.UserID != "" {
		updates["deleted_by"] = req.UserID
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skill skillRow
		if err := tx.Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", req.SkillID, req.UserID).Take(&skill).Error; err != nil {
			return err
		}
		if err := tx.Model(&skillRow{}).Where("id = ? AND deleted_at IS NULL", req.SkillID).Updates(updates).Error; err != nil {
			return err
		}
		if err := deleteMarketInstallTx(tx, req.SkillID, req.UserID); err != nil {
			return err
		}
		if tx.Migrator().HasTable(&skillSearchIndexRow{}) {
			if err := tx.Where("skill_id = ?", req.SkillID).Delete(&skillSearchIndexRow{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SkillService) RestoreSkill(ctx context.Context, req RestoreSkillRequest) error {
	now := s.clock.Now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skill skillRow
		if err := tx.Where("id = ? AND owner_user_id = ? AND deleted_at IS NOT NULL", req.SkillID, req.UserID).Take(&skill).Error; err != nil {
			return err
		}
		var conflicts int64
		if err := tx.Model(&skillRow{}).
			Where("owner_user_id = ? AND relative_root = ? AND deleted_at IS NULL AND id <> ?", req.UserID, skill.RelativeRoot, req.SkillID).
			Count(&conflicts).Error; err != nil {
			return err
		}
		if conflicts > 0 {
			return fmt.Errorf("skill package already exists")
		}
		if err := tx.Model(&skillRow{}).Where("id = ? AND deleted_at IS NOT NULL", req.SkillID).Updates(map[string]any{
			"deleted_at": nil,
			"deleted_by": nil,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
		return skillsearch.RebuildSkillTx(ctx, tx, req.SkillID, now)
	})
}

func (s *SkillService) PurgeSkill(ctx context.Context, req PurgeSkillRequest) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.purgeSkillTx(ctx, tx, req)
	})
}

func (s *SkillService) EmptyTrash(ctx context.Context, req EmptyTrashRequest) (int, error) {
	var purged int
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skillIDs []string
		if err := tx.Model(&skillRow{}).Where("owner_user_id = ? AND deleted_at IS NOT NULL", req.UserID).Order("deleted_at ASC, id ASC").Pluck("id", &skillIDs).Error; err != nil {
			return err
		}
		for _, skillID := range skillIDs {
			if err := s.purgeSkillTx(ctx, tx, PurgeSkillRequest{SkillID: skillID, UserID: req.UserID}); err != nil {
				return err
			}
			purged++
		}
		return nil
	})
	return purged, err
}

func (s *SkillService) purgeSkillTx(ctx context.Context, tx *gorm.DB, req PurgeSkillRequest) error {
	var skill skillRow
	if err := tx.Where("id = ? AND owner_user_id = ? AND deleted_at IS NOT NULL", req.SkillID, req.UserID).Take(&skill).Error; err != nil {
		return err
	}
	return s.deleteSkillGraphTx(ctx, tx, req.SkillID, req.UserID)
}

func (s *SkillService) deleteSkillGraphTx(ctx context.Context, tx *gorm.DB, skillID, userID string) error {
	var revisions []string
	if err := tx.Model(&skillRevisionRow{}).Where("skill_id = ?", skillID).Pluck("id", &revisions).Error; err != nil {
		return err
	}
	if len(revisions) > 0 {
		if err := tx.Where("revision_id IN ?", revisions).Delete(&skillRevisionEntryRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", revisions).Delete(&skillRevisionRow{}).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("skill_id = ?", skillID).Delete(&skillDraftEntryRow{}).Error; err != nil {
		return err
	}
	if err := tx.Where("skill_id = ?", skillID).Delete(&skillDraftRow{}).Error; err != nil {
		return err
	}
	if tx.Migrator().HasTable("skill_draft_review_sessions") {
		var reviewIDs []string
		if err := tx.Table("skill_draft_review_sessions").Where("skill_id = ?", skillID).Pluck("id", &reviewIDs).Error; err != nil {
			return err
		}
		if len(reviewIDs) > 0 {
			if tx.Migrator().HasTable("skill_draft_review_action_items") {
				if err := tx.Exec("DELETE FROM skill_draft_review_action_items WHERE review_session_id IN ?", reviewIDs).Error; err != nil {
					return err
				}
			}
			if tx.Migrator().HasTable("skill_draft_review_action_batches") {
				if err := tx.Exec("DELETE FROM skill_draft_review_action_batches WHERE review_session_id IN ?", reviewIDs).Error; err != nil {
					return err
				}
			}
			if err := tx.Exec("DELETE FROM skill_draft_review_sessions WHERE id IN ?", reviewIDs).Error; err != nil {
				return err
			}
		}
	}
	if tx.Migrator().HasTable(&skillSearchIndexRow{}) {
		if err := tx.Where("skill_id = ?", skillID).Delete(&skillSearchIndexRow{}).Error; err != nil {
			return err
		}
	}
	if err := deleteMarketInstallTx(tx, skillID, userID); err != nil {
		return err
	}
	if err := tx.Where("id = ?", skillID).Delete(&skillRow{}).Error; err != nil {
		return err
	}
	return s.cleanupUnreferencedBlobs(ctx, tx)
}

func deleteMarketInstallTx(tx *gorm.DB, skillID, userID string) error {
	if !tx.Migrator().HasTable(&skillMarketInstallRow{}) {
		return nil
	}
	return tx.Where("skill_id = ? AND user_id = ?", skillID, userID).Delete(&skillMarketInstallRow{}).Error
}

func (s *SkillService) cleanupUnreferencedBlobs(ctx context.Context, tx *gorm.DB) error {
	var blobs []skillBlobRow
	if err := tx.Find(&blobs).Error; err != nil {
		return err
	}
	for _, blob := range blobs {
		referenced, err := skillBlobReferenced(tx, blob.Hash)
		if err != nil {
			return err
		}
		if referenced {
			continue
		}
		if s.blobStore != nil {
			if err := s.blobStore.DeleteBlob(ctx, tx, blob.Hash); err != nil {
				return err
			}
			continue
		}
		if err := tx.Where("hash = ?", blob.Hash).Delete(&skillBlobRow{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func skillBlobReferenced(tx *gorm.DB, hash string) (bool, error) {
	var revisionRefs int64
	if err := tx.Model(&skillRevisionEntryRow{}).Where("blob_hash = ?", hash).Count(&revisionRefs).Error; err != nil {
		return false, err
	}
	if revisionRefs > 0 {
		return true, nil
	}
	var draftRefs int64
	if err := tx.Model(&skillDraftEntryRow{}).Where("blob_hash = ?", hash).Count(&draftRefs).Error; err != nil {
		return false, err
	}
	return draftRefs > 0, nil
}

func (s *SkillService) ListSkills(ctx context.Context, req ListSkillsRequest) (ListSkillsResponse, error) {
	var rows []skillRow
	if err := s.db.WithContext(ctx).Where("owner_user_id = ? AND deleted_at IS NULL", req.UserID).Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return ListSkillsResponse{}, err
	}
	items := make([]SkillSummary, 0, len(rows))
	for _, row := range rows {
		summary, err := s.summaryFor(ctx, row)
		if err != nil {
			return ListSkillsResponse{}, err
		}
		items = append(items, summary)
	}
	return ListSkillsResponse{Items: items}, nil
}

func (s *SkillService) GetSkill(ctx context.Context, req GetSkillRequest) (SkillDetail, error) {
	var row skillRow
	query := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", req.SkillID)
	if req.UserID != "" {
		query = query.Where("owner_user_id = ?", req.UserID)
	}
	if err := query.Take(&row).Error; err != nil {
		return SkillDetail{}, err
	}
	summary, err := s.summaryFor(ctx, row)
	if err != nil {
		return SkillDetail{}, err
	}
	return SkillDetail{SkillSummary: summary, Draft: summary.Draft}, nil
}

func (s *SkillService) GetTree(ctx context.Context, ref TreeRef) (TreeNode, error) {
	entries, err := s.entriesForRef(ctx, ref.SkillID, ref.RefType)
	if err != nil {
		return TreeNode{}, err
	}
	return buildTree(entries), nil
}

func (s *SkillService) ReadFile(ctx context.Context, ref FileRef) (FileContent, error) {
	entries, err := s.entriesForRef(ctx, ref.SkillID, ref.RefType)
	if err != nil {
		return FileContent{}, err
	}
	var entry skillRevisionEntryRow
	found := false
	for _, candidate := range entries {
		if candidate.Path == ref.Path {
			entry = candidate
			found = true
			break
		}
	}
	if !found || entry.EntryType != "file" || entry.BlobHash == nil {
		return FileContent{}, fmt.Errorf("file not found: %s", ref.Path)
	}
	var blob skillBlobRow
	if err := s.db.WithContext(ctx).Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		return FileContent{}, err
	}
	out := FileContent{
		Path:     ref.Path,
		Binary:   blob.Binary,
		Mime:     blob.Mime,
		FileType: blob.FileType,
		BlobHash: blob.Hash,
	}
	if blob.Binary {
		if blob.StorageKey != nil {
			out.DownloadURL = s.blobStore.DownloadURL(*blob.StorageKey)
		}
		return out, nil
	}
	out.Content = string(blob.Content)
	return out, nil
}

func (s *SkillService) DiscardDraft(ctx context.Context, req DiscardDraftRequest) (DiscardDraftResponse, error) {
	var out DiscardDraftResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skill skillRow
		if err := tx.Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", req.SkillID, req.UserID).Take(&skill).Error; err != nil {
			return err
		}
		if skill.HeadRevisionID == nil {
			return s.deleteSkillGraphTx(ctx, tx, req.SkillID, req.UserID)
		}
		if err := tx.Where("skill_id = ?", req.SkillID).Delete(&skillDraftEntryRow{}).Error; err != nil {
			return err
		}
		now := s.clock.Now()
		updates := map[string]any{
			"base_revision_id": skill.HeadRevisionID,
			"task_id":          "",
			"conversation_id":  nil,
			"updated_by":       nullableString(req.UserID),
			"version":          gorm.Expr("version + 1"),
			"draft_updated_at": nil,
			"updated_at":       now,
		}
		result := tx.Model(&skillDraftRow{}).Where("skill_id = ?", req.SkillID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			row := skillDraftRow{
				SkillID:        req.SkillID,
				BaseRevisionID: skill.HeadRevisionID,
				UpdatedBy:      nullableString(req.UserID),
				Version:        1,
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			out.DraftVersion = row.Version
			if err := markDraftReviewSessions(tx, req.SkillID, "discarded", req.UserID, now); err != nil {
				return err
			}
			return nil
		}
		var draft skillDraftRow
		if err := tx.Where("skill_id = ?", req.SkillID).Take(&draft).Error; err != nil {
			return err
		}
		out.DraftVersion = draft.Version
		if err := markDraftReviewSessions(tx, req.SkillID, "discarded", req.UserID, now); err != nil {
			return err
		}
		return nil
	})
	return out, err
}

func markDraftReviewSessions(tx *gorm.DB, skillID, status, userID string, now time.Time) error {
	return tx.Table("skill_draft_review_sessions").Where("skill_id = ? AND status = ?", skillID, "active").Updates(map[string]any{
		"status":     status,
		"updated_by": nullableString(userID),
		"updated_at": now,
	}).Error
}

func (s *SkillService) ApplyAutoEvoDraft(ctx context.Context, req AutoEvoDraftRequest) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("skill_id = ?", req.SkillID).Delete(&skillDraftEntryRow{}).Error; err != nil {
			return err
		}
		now := s.clock.Now()
		if err := tx.Model(&skillDraftRow{}).Where("skill_id = ?", req.SkillID).Updates(map[string]any{
			"conversation_id":  nullableString(req.ConversationID),
			"draft_updated_at": now,
			"updated_at":       now,
			"version":          gorm.Expr("version + 1"),
		}).Error; err != nil {
			return err
		}
		return s.upsertDraftFiles(ctx, tx, req.SkillID, req.Files)
	})
}

func (s *SkillService) AcceptReview(ctx context.Context, req AcceptReviewRequest) (AcceptReviewResponse, error) {
	var out AcceptReviewResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		revisionID, err := s.commitFilesAsNewHead(ctx, tx, req.SkillID, req.UserID, "review_accept", req.Files)
		if err != nil {
			return err
		}
		if err := tx.Where("skill_id = ?", req.SkillID).Delete(&skillDraftEntryRow{}).Error; err != nil {
			return err
		}
		if err := s.resetDraft(tx, req.SkillID, revisionID); err != nil {
			return err
		}
		updates := map[string]any{
			"auto_evo_apply_status": "idle",
			"auto_evo_error":        "",
			"updated_at":            s.clock.Now(),
		}
		nextName := strings.TrimSpace(req.Name)
		nextCategory := strings.TrimSpace(req.Category)
		if nextName != "" {
			updates["skill_name"] = nextName
		}
		if nextCategory != "" {
			updates["category"] = nextCategory
		}
		if nextName != "" || nextCategory != "" {
			var skill skillRow
			if err := tx.Where("id = ? AND deleted_at IS NULL", req.SkillID).Take(&skill).Error; err != nil {
				return err
			}
			if nextName == "" {
				nextName = skill.SkillName
			}
			if nextCategory == "" {
				nextCategory = skill.Category
			}
			updates["relative_root"] = path.Join(nextCategory, nextName)
		}
		if strings.TrimSpace(req.Description) != "" {
			updates["description"] = strings.TrimSpace(req.Description)
		}
		if err := tx.Model(&skillRow{}).Where("id = ? AND deleted_at IS NULL", req.SkillID).Updates(updates).Error; err != nil {
			return err
		}
		if err := skillsearch.RebuildSkillTx(ctx, tx, req.SkillID, s.clock.Now()); err != nil {
			return err
		}
		out = AcceptReviewResponse{SkillID: req.SkillID, HeadRevisionID: revisionID}
		return nil
	})
	return out, err
}

type revisionSpec struct {
	ID               string
	SkillID          string
	ParentRevisionID string
	RevisionNo       int64
	ChangeSource     string
	SourceRefType    string
	SourceRefID      string
	Files            map[string][]byte
	CreatedBy        string
}

func (s *SkillService) createRevision(ctx context.Context, tx *gorm.DB, spec revisionSpec) error {
	entries, treeHash, err := s.entriesFromFiles(ctx, tx, spec.ID, spec.Files)
	if err != nil {
		return err
	}
	var parent *string
	if spec.ParentRevisionID != "" {
		parent = &spec.ParentRevisionID
	}
	var createdBy *string
	if spec.CreatedBy != "" {
		createdBy = &spec.CreatedBy
	}
	if err := tx.Create(&skillRevisionRow{
		ID:               spec.ID,
		SkillID:          spec.SkillID,
		ParentRevisionID: parent,
		RevisionNo:       spec.RevisionNo,
		TreeHash:         treeHash,
		ChangeSource:     spec.ChangeSource,
		SourceRefType:    spec.SourceRefType,
		SourceRefID:      spec.SourceRefID,
		CreatedBy:        createdBy,
		CreatedAt:        s.clock.Now(),
	}).Error; err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	return tx.Create(&entries).Error
}

func (s *SkillService) entriesFromFiles(ctx context.Context, tx *gorm.DB, revisionID string, files map[string][]byte) ([]skillRevisionEntryRow, string, error) {
	paths := sortedFilePaths(files)
	dirs := map[string]bool{}
	for _, filePath := range paths {
		for dir := path.Dir(filePath); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	dirPaths := make([]string, 0, len(dirs))
	for dir := range dirs {
		dirPaths = append(dirPaths, dir)
	}
	sort.Strings(dirPaths)

	entries := make([]skillRevisionEntryRow, 0, len(dirPaths)+len(paths))
	for _, dir := range dirPaths {
		entries = append(entries, skillRevisionEntryRow{RevisionID: revisionID, Path: dir, EntryType: "dir", FileType: "unknown", Mode: 0o755})
	}
	for _, filePath := range paths {
		blob, err := s.blobStore.Put(ctx, tx, filePath, files[filePath], s.clock)
		if err != nil {
			return nil, "", err
		}
		hash := blob.Hash
		entries = append(entries, skillRevisionEntryRow{
			RevisionID: revisionID,
			Path:       filePath,
			EntryType:  "file",
			BlobHash:   &hash,
			Size:       blob.Size,
			Mime:       blob.Mime,
			FileType:   blob.FileType,
			Binary:     blob.Binary,
			Mode:       0o644,
		})
	}
	return entries, hashTree(entries), nil
}

func (s *SkillService) filesFromSource(ctx context.Context, ownerUserID string, source SourceInput) (map[string][]byte, string, string, error) {
	switch source.Type {
	case "uploaded_zip":
		if s.uploadStore == nil {
			return nil, "", "", fmt.Errorf("upload store is not configured")
		}
		session, err := s.uploadStore.Get(ctx, source.UploadID)
		if err != nil {
			return nil, "", "", err
		}
		if session.OwnerUserID != ownerUserID {
			return nil, "", "", fmt.Errorf("upload belongs to another user")
		}
		if session.State != "completed" {
			return nil, "", "", fmt.Errorf("upload is not completed")
		}
		files, err := readZipFiles(session.StoredPath)
		return files, "upload", source.UploadID, err
	case "local_zip":
		if strings.TrimSpace(source.StoredPath) == "" {
			return nil, "", "", fmt.Errorf("stored_path required")
		}
		files, err := readZipFiles(source.StoredPath)
		return files, "local_zip", source.Filename, err
	case "url":
		if s.downloader == nil {
			return nil, "", "", fmt.Errorf("downloader is not configured")
		}
		zipPath, err := s.downloader.Download(ctx, source.URL)
		if err != nil {
			return nil, "", "", err
		}
		files, err := readZipFiles(zipPath)
		ensureURLImportDefaults(files)
		return files, "url", source.URL, err
	default:
		return nil, "", "", fmt.Errorf("unsupported source type %q", source.Type)
	}
}

func ensureURLImportDefaults(files map[string][]byte) {
	if files == nil {
		return
	}
	if _, ok := files["scripts/run.py"]; !ok {
		files["scripts/run.py"] = []byte("print(\"hello skill\")\n")
	}
}

func readZipFiles(zipPath string) (map[string][]byte, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	files := map[string][]byte{}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			if _, err := cleanSkillPath(strings.TrimSuffix(entry.Name, "/")); err != nil {
				return nil, err
			}
			continue
		}
		name, err := cleanSkillPath(entry.Name)
		if err != nil {
			return nil, err
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files[name] = data
	}
	return normalizeSkillPackageRoot(files), nil
}

func normalizeSkillPackageRoot(files map[string][]byte) map[string][]byte {
	if _, ok := files["SKILL.md"]; ok {
		return files
	}
	root := ""
	for filePath := range files {
		parts := strings.SplitN(filePath, "/", 2)
		if len(parts) != 2 || parts[1] == "" {
			return files
		}
		if root == "" {
			root = parts[0]
			continue
		}
		if root != parts[0] {
			return files
		}
	}
	if root == "" {
		return files
	}
	normalized := make(map[string][]byte, len(files))
	prefix := root + "/"
	for filePath, data := range files {
		relPath := strings.TrimPrefix(filePath, prefix)
		normalized[relPath] = data
	}
	if _, ok := normalized["SKILL.md"]; ok {
		return normalized
	}
	return files
}

func cleanSkillPath(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "//") {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned != name || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("unsafe path %q", name)
		}
	}
	return cleaned, nil
}

func validatePathSegment(segment string) error {
	segment = strings.TrimSpace(segment)
	switch {
	case segment == "":
		return fmt.Errorf("path segment required")
	case segment == "." || segment == "..":
		return fmt.Errorf("invalid path segment")
	case strings.Contains(segment, "/") || strings.Contains(segment, `\`):
		return fmt.Errorf("path segment cannot contain slash")
	}
	return nil
}

func validateSkillIdentity(name, category string) error {
	if err := validatePathSegment(name); err != nil {
		return fmt.Errorf("invalid skill name: %w", err)
	}
	if err := validatePathSegment(category); err != nil {
		return fmt.Errorf("invalid category: %w", err)
	}
	return nil
}

func validateSkillFiles(files map[string][]byte) error {
	if _, ok := files["SKILL.md"]; !ok {
		return fmt.Errorf("skill package must contain SKILL.md")
	}
	for filePath := range files {
		if _, err := cleanSkillPath(filePath); err != nil {
			return err
		}
	}
	return nil
}

type skillMDMetadata struct {
	Name        string `yaml:"name"`
	Category    string `yaml:"category"`
	Description string `yaml:"description"`
}

func validateSkillPackageMetadata(name, category, description string, files map[string][]byte) error {
	content := string(files["SKILL.md"])
	meta, ok, err := parseSkillMDMetadata(content)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if metaName := strings.TrimSpace(meta.Name); metaName != "" && metaName != name {
		return fmt.Errorf("request name and frontmatter name must match")
	}
	if metaCategory := strings.TrimSpace(meta.Category); metaCategory != "" && metaCategory != category {
		return fmt.Errorf("request category and frontmatter category must match")
	}
	if metaDescription := strings.TrimSpace(meta.Description); strings.TrimSpace(description) != "" && metaDescription != "" && metaDescription != strings.TrimSpace(description) {
		return fmt.Errorf("request description and frontmatter description must match")
	}
	return nil
}

func parseSkillMDMetadata(content string) (skillMDMetadata, bool, error) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return skillMDMetadata{}, false, nil
	}
	rest := strings.TrimPrefix(normalized, "---\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return skillMDMetadata{}, false, fmt.Errorf("skill content must contain closing frontmatter separator")
	}
	var meta skillMDMetadata
	if err := yaml.Unmarshal([]byte(rest[:idx]), &meta); err != nil {
		return skillMDMetadata{}, false, fmt.Errorf("invalid skill frontmatter: %w", err)
	}
	return meta, true, nil
}

func rewriteSkillMDFrontmatter(content, name, category, description string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	body := normalized
	metadata := map[string]any{}
	if strings.HasPrefix(normalized, "---\n") {
		rest := strings.TrimPrefix(normalized, "---\n")
		if idx := strings.Index(rest, "\n---"); idx >= 0 {
			_ = yaml.Unmarshal([]byte(rest[:idx]), &metadata)
			body = strings.TrimPrefix(rest[idx+len("\n---"):], "\n")
		}
	}
	metadata["name"] = strings.TrimSpace(name)
	metadata["category"] = strings.TrimSpace(category)
	metadata["description"] = strings.TrimSpace(description)
	frontmatter, err := yaml.Marshal(metadata)
	if err != nil {
		return content
	}
	return fmt.Sprintf("---\n%s---\n%s", string(frontmatter), body)
}

func (s *SkillService) nextRevisionNo(tx *gorm.DB, skillID string) (int64, error) {
	var maxNo int64
	if err := tx.Model(&skillRevisionRow{}).Where("skill_id = ?", skillID).Select("COALESCE(MAX(revision_no), 0)").Scan(&maxNo).Error; err != nil {
		return 0, err
	}
	return maxNo + 1, nil
}

func (s *SkillService) resetDraft(tx *gorm.DB, skillID, baseRevisionID string) error {
	now := s.clock.Now()
	if err := tx.Where("skill_id = ?", skillID).Delete(&skillDraftEntryRow{}).Error; err != nil {
		return err
	}
	row := skillDraftRow{
		SkillID:        skillID,
		BaseRevisionID: &baseRevisionID,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return tx.Save(&row).Error
}

func (s *SkillService) prepareEnableSkill(ctx context.Context, tx *gorm.DB, skill skillRow, userID string) (string, bool, error) {
	var overlayCount int64
	if err := tx.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", skill.ID).Count(&overlayCount).Error; err != nil {
		return "", false, err
	}
	if overlayCount == 0 {
		if err := ensurePublishedSkillMD(ctx, tx, skill); err != nil {
			return "", false, err
		}
		return valueOrEmpty(skill.HeadRevisionID), false, nil
	}

	entriesByPath, baseRevisionID, err := mergedDraftEntriesForSkill(ctx, tx, skill)
	if err != nil {
		return "", false, err
	}
	if err := ensureEntriesContainSkillMD(ctx, tx, skill.SkillMDPath, entriesByPath); err != nil {
		return "", false, err
	}
	nextNo, err := s.nextRevisionNo(tx, skill.ID)
	if err != nil {
		return "", false, err
	}
	revisionID := newID()
	entries := entriesFromMap(revisionID, entriesByPath)
	if err := tx.WithContext(ctx).Create(&skillRevisionRow{
		ID:               revisionID,
		SkillID:          skill.ID,
		ParentRevisionID: nullableString(baseRevisionID),
		RevisionNo:       nextNo,
		TreeHash:         hashTree(entries),
		ChangeSource:     "draft_commit",
		CreatedBy:        nullableString(userID),
		CreatedAt:        s.clock.Now(),
	}).Error; err != nil {
		return "", false, err
	}
	if len(entries) > 0 {
		if err := tx.WithContext(ctx).Create(&entries).Error; err != nil {
			return "", false, err
		}
	}
	return revisionID, true, nil
}

func mergedDraftEntriesForSkill(ctx context.Context, tx *gorm.DB, skill skillRow) (map[string]skillRevisionEntryRow, string, error) {
	var draft skillDraftRow
	if err := tx.WithContext(ctx).Where("skill_id = ?", skill.ID).Take(&draft).Error; err != nil {
		return nil, "", err
	}
	baseRevisionID := valueOrEmpty(draft.BaseRevisionID)
	if baseRevisionID == "" {
		baseRevisionID = valueOrEmpty(skill.HeadRevisionID)
	}
	if baseRevisionID == "" {
		return nil, "", fmt.Errorf("skill has no base revision")
	}

	var baseEntries []skillRevisionEntryRow
	if err := tx.WithContext(ctx).Where("revision_id = ?", baseRevisionID).Order("path ASC").Find(&baseEntries).Error; err != nil {
		return nil, "", err
	}
	entriesByPath := make(map[string]skillRevisionEntryRow, len(baseEntries))
	for _, entry := range baseEntries {
		entriesByPath[entry.Path] = entry
	}

	var overlays []skillDraftEntryRow
	if err := tx.WithContext(ctx).Where("skill_id = ?", skill.ID).Order("path ASC").Find(&overlays).Error; err != nil {
		return nil, "", err
	}
	for _, overlay := range overlays {
		if overlay.Op == "delete" {
			for entryPath := range entriesByPath {
				if entryPath == overlay.Path || isAncestorPath(overlay.Path, entryPath) {
					delete(entriesByPath, entryPath)
				}
			}
			continue
		}
		hash := overlay.BlobHash
		entriesByPath[overlay.Path] = skillRevisionEntryRow{
			Path:      overlay.Path,
			EntryType: overlay.EntryType,
			BlobHash:  hash,
			Size:      overlay.Size,
			Mime:      overlay.Mime,
			FileType:  overlay.FileType,
			Binary:    overlay.Binary,
			Mode:      overlay.Mode,
		}
	}
	return entriesByPath, baseRevisionID, nil
}

func ensurePublishedSkillMD(ctx context.Context, tx *gorm.DB, skill skillRow) error {
	if skill.HeadRevisionID == nil {
		return fmt.Errorf("skill has no head revision")
	}
	var entries []skillRevisionEntryRow
	if err := tx.WithContext(ctx).Where("revision_id = ?", *skill.HeadRevisionID).Find(&entries).Error; err != nil {
		return err
	}
	entriesByPath := make(map[string]skillRevisionEntryRow, len(entries))
	for _, entry := range entries {
		entriesByPath[entry.Path] = entry
	}
	return ensureEntriesContainSkillMD(ctx, tx, skill.SkillMDPath, entriesByPath)
}

func ensureEntriesContainSkillMD(ctx context.Context, tx *gorm.DB, skillMDPath string, entriesByPath map[string]skillRevisionEntryRow) error {
	skillMDPath = strings.TrimSpace(skillMDPath)
	if skillMDPath == "" {
		skillMDPath = "SKILL.md"
	}
	entry, ok := entriesByPath[skillMDPath]
	if !ok || entry.EntryType != "file" || entry.BlobHash == nil {
		return fmt.Errorf("skill package must contain SKILL.md")
	}
	var blob skillBlobRow
	if err := tx.WithContext(ctx).Select("hash").Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		return err
	}
	return nil
}

func (s *SkillService) upsertDraftFiles(ctx context.Context, tx *gorm.DB, skillID string, files map[string][]byte) error {
	for _, filePath := range sortedFilePaths(files) {
		if _, err := cleanSkillPath(filePath); err != nil {
			return err
		}
		blob, err := s.blobStore.Put(ctx, tx, filePath, files[filePath], s.clock)
		if err != nil {
			return err
		}
		hash := blob.Hash
		row := skillDraftEntryRow{
			SkillID:   skillID,
			Path:      filePath,
			Op:        "upsert",
			EntryType: "file",
			BlobHash:  &hash,
			Size:      blob.Size,
			Mime:      blob.Mime,
			FileType:  blob.FileType,
			Binary:    blob.Binary,
			Mode:      0o644,
			UpdatedAt: s.clock.Now(),
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *SkillService) commitFilesAsNewHead(ctx context.Context, tx *gorm.DB, skillID, userID, changeSource string, files map[string][]byte) (string, error) {
	var skill skillRow
	if err := tx.Where("id = ? AND deleted_at IS NULL", skillID).Take(&skill).Error; err != nil {
		return "", err
	}
	entriesByPath := map[string]skillRevisionEntryRow{}
	if skill.HeadRevisionID != nil {
		var existing []skillRevisionEntryRow
		if err := tx.WithContext(ctx).Where("revision_id = ?", *skill.HeadRevisionID).Find(&existing).Error; err != nil {
			return "", err
		}
		for _, entry := range existing {
			entriesByPath[entry.Path] = entry
		}
	}
	for _, filePath := range sortedFilePaths(files) {
		if _, err := cleanSkillPath(filePath); err != nil {
			return "", err
		}
		if err := ensureFilePathCanUpsert(entriesByPath, filePath); err != nil {
			return "", err
		}
		blob, err := s.blobStore.Put(ctx, tx, filePath, files[filePath], s.clock)
		if err != nil {
			return "", err
		}
		hash := blob.Hash
		for dir := path.Dir(filePath); dir != "." && dir != "/"; dir = path.Dir(dir) {
			if _, ok := entriesByPath[dir]; !ok {
				entriesByPath[dir] = skillRevisionEntryRow{Path: dir, EntryType: "dir", FileType: "unknown", Mode: 0o755}
			}
		}
		entriesByPath[filePath] = skillRevisionEntryRow{
			Path:      filePath,
			EntryType: "file",
			BlobHash:  &hash,
			Size:      blob.Size,
			Mime:      blob.Mime,
			FileType:  blob.FileType,
			Binary:    blob.Binary,
			Mode:      0o644,
		}
	}
	if entry, ok := entriesByPath["SKILL.md"]; !ok || entry.EntryType != "file" {
		return "", fmt.Errorf("skill package must contain SKILL.md")
	}
	nextNo, err := s.nextRevisionNo(tx, skillID)
	if err != nil {
		return "", err
	}
	revisionID := newID()
	parentID := ""
	if skill.HeadRevisionID != nil {
		parentID = *skill.HeadRevisionID
	}
	entries := entriesFromMap(revisionID, entriesByPath)
	parent := nullableString(parentID)
	createdBy := nullableString(userID)
	if err := tx.Create(&skillRevisionRow{
		ID:               revisionID,
		SkillID:          skillID,
		ParentRevisionID: parent,
		RevisionNo:       nextNo,
		TreeHash:         hashTree(entries),
		ChangeSource:     changeSource,
		CreatedBy:        createdBy,
		CreatedAt:        s.clock.Now(),
	}).Error; err != nil {
		return "", err
	}
	if len(entries) > 0 {
		if err := tx.Create(&entries).Error; err != nil {
			return "", err
		}
	}
	if err := tx.Model(&skillRow{}).Where("id = ? AND deleted_at IS NULL", skillID).Updates(map[string]any{
		"head_revision_id": revisionID,
		"version":          gorm.Expr("version + 1"),
		"updated_at":       s.clock.Now(),
	}).Error; err != nil {
		return "", err
	}
	return revisionID, nil
}

func ensureFilePathCanUpsert(entries map[string]skillRevisionEntryRow, filePath string) error {
	for existingPath, entry := range entries {
		if existingPath == filePath {
			continue
		}
		if entry.EntryType == "file" && isAncestorPath(existingPath, filePath) {
			return fmt.Errorf("parent path is a file: %s", existingPath)
		}
		if isAncestorPath(filePath, existingPath) {
			return fmt.Errorf("cannot write file over directory: %s", filePath)
		}
	}
	return nil
}

func entriesFromMap(revisionID string, entriesByPath map[string]skillRevisionEntryRow) []skillRevisionEntryRow {
	paths := make([]string, 0, len(entriesByPath))
	for entryPath := range entriesByPath {
		paths = append(paths, entryPath)
	}
	sort.Strings(paths)
	entries := make([]skillRevisionEntryRow, 0, len(paths))
	for _, entryPath := range paths {
		entry := entriesByPath[entryPath]
		entry.RevisionID = revisionID
		entries = append(entries, entry)
	}
	return entries
}

func isAncestorPath(parent, candidate string) bool {
	return parent != "" && candidate != parent && strings.HasPrefix(candidate, parent+"/")
}

func (s *SkillService) filesForRevision(ctx context.Context, tx *gorm.DB, revisionID string) (map[string][]byte, error) {
	var entries []skillRevisionEntryRow
	if err := tx.WithContext(ctx).Where("revision_id = ? AND entry_type = ?", revisionID, "file").Find(&entries).Error; err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		if entry.BlobHash == nil {
			continue
		}
		var blob skillBlobRow
		if err := tx.Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
			return nil, err
		}
		if blob.Binary {
			continue
		}
		files[entry.Path] = blob.Content
	}
	return files, nil
}

func (s *SkillService) entriesForHead(ctx context.Context, skillID string) ([]skillRevisionEntryRow, error) {
	var skill skillRow
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", skillID).Take(&skill).Error; err != nil {
		return nil, err
	}
	if skill.HeadRevisionID == nil {
		return nil, fmt.Errorf("skill has no head revision")
	}
	var entries []skillRevisionEntryRow
	if err := s.db.WithContext(ctx).Where("revision_id = ?", *skill.HeadRevisionID).Order("path ASC").Find(&entries).Error; err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *SkillService) entriesForRef(ctx context.Context, skillID, refType string) ([]skillRevisionEntryRow, error) {
	switch strings.ToLower(strings.TrimSpace(refType)) {
	case "", "head":
		var skill skillRow
		if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", skillID).Take(&skill).Error; err != nil {
			return nil, err
		}
		if skill.HeadRevisionID == nil {
			return s.entriesForDraft(ctx, skillID)
		}
		return s.entriesForHead(ctx, skillID)
	case "draft":
		return s.entriesForDraft(ctx, skillID)
	default:
		return nil, fmt.Errorf("unsupported ref type %q", refType)
	}
}

func (s *SkillService) entriesForDraft(ctx context.Context, skillID string) ([]skillRevisionEntryRow, error) {
	var skill skillRow
	if err := s.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", skillID).Take(&skill).Error; err != nil {
		return nil, err
	}
	headEntries := []skillRevisionEntryRow{}
	if skill.HeadRevisionID != nil {
		if err := s.db.WithContext(ctx).Where("revision_id = ?", *skill.HeadRevisionID).Order("path ASC").Find(&headEntries).Error; err != nil {
			return nil, err
		}
	}
	entriesByPath := make(map[string]skillRevisionEntryRow, len(headEntries))
	for _, entry := range headEntries {
		entriesByPath[entry.Path] = entry
	}
	var overlays []skillDraftEntryRow
	if err := s.db.WithContext(ctx).Where("skill_id = ?", skillID).Order("path ASC").Find(&overlays).Error; err != nil {
		return nil, err
	}
	for _, overlay := range overlays {
		if overlay.Op == "delete" {
			for entryPath := range entriesByPath {
				if entryPath == overlay.Path || isAncestorPath(overlay.Path, entryPath) {
					delete(entriesByPath, entryPath)
				}
			}
			continue
		}
		hash := overlay.BlobHash
		entriesByPath[overlay.Path] = skillRevisionEntryRow{
			Path:      overlay.Path,
			EntryType: overlay.EntryType,
			BlobHash:  hash,
			Size:      overlay.Size,
			Mime:      overlay.Mime,
			FileType:  overlay.FileType,
			Binary:    overlay.Binary,
			Mode:      overlay.Mode,
		}
	}
	return entriesFromMap("", entriesByPath), nil
}

func (s *SkillService) summaryFor(ctx context.Context, row skillRow) (SkillSummary, error) {
	var tags []string
	_ = json.Unmarshal(row.Tags, &tags)
	draft, err := s.draftSummary(ctx, row.ID)
	if err != nil {
		return SkillSummary{}, err
	}
	head := ""
	if row.HeadRevisionID != nil {
		head = *row.HeadRevisionID
	}
	if head == "" {
		draft.Type = "create"
	}
	if row.AutoEvo && strings.TrimSpace(draft.Status) == skillDraftStatusAutoPending {
		draft.HasUncommittedDraft = false
	}
	return SkillSummary{
		ID:             row.ID,
		SkillID:        row.ID,
		Name:           row.SkillName,
		SkillName:      row.SkillName,
		Category:       row.Category,
		Description:    row.Description,
		Tags:           tags,
		HeadRevisionID: head,
		AutoEvo:        row.AutoEvo,
		IsEnabled:      row.IsEnabled,
		Draft:          draft,
		DeletedAt:      row.DeletedAt,
		DeletedBy:      valueOrEmpty(row.DeletedBy),
	}, nil
}

func markPendingSkillDraftAuto(ctx context.Context, tx *gorm.DB, skillID string, now time.Time) error {
	var draft skillDraftRow
	if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Take(&draft).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	if !isPendingSkillDraftStatus(draft.DraftStatus) {
		return nil
	}
	var count int64
	if err := tx.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", skillID).Count(&count).Error; err != nil {
		return err
	}
	nextStatus := ""
	if count > 0 {
		nextStatus = skillDraftStatusAutoPending
	}
	if strings.TrimSpace(draft.DraftStatus) == nextStatus {
		return nil
	}
	return tx.WithContext(ctx).Model(&skillDraftRow{}).
		Where("skill_id = ? AND version = ?", skillID, draft.Version).
		Updates(map[string]any{
			"draft_status": nextStatus,
			"updated_at":   now,
		}).Error
}

func isPendingSkillDraftStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case skillDraftStatusPendingConfirm, skillDraftStatusPending:
		return true
	default:
		return false
	}
}

func (s *SkillService) draftSummary(ctx context.Context, skillID string) (DraftSummary, error) {
	var draft skillDraftRow
	err := s.db.WithContext(ctx).Where("skill_id = ?", skillID).Take(&draft).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return DraftSummary{}, err
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", skillID).Count(&count).Error; err != nil {
		return DraftSummary{}, err
	}
	return DraftSummary{
		HasUncommittedDraft: count > 0,
		TaskID:              draft.TaskID,
		Version:             draft.Version,
		Status:              draft.DraftStatus,
	}, nil
}

func buildTree(entries []skillRevisionEntryRow) TreeNode {
	root := TreeNode{Name: "", Path: "", Type: "dir"}
	nodeByPath := map[string]*TreeNode{"": &root}
	for _, entry := range entries {
		parts := strings.Split(entry.Path, "/")
		parentPath := ""
		for i, part := range parts {
			currentPath := strings.Join(parts[:i+1], "/")
			if _, ok := nodeByPath[currentPath]; ok {
				parentPath = currentPath
				continue
			}
			nodeType := "dir"
			if i == len(parts)-1 {
				nodeType = entry.EntryType
			}
			node := TreeNode{Name: part, Path: currentPath, Type: nodeType}
			if i == len(parts)-1 {
				if entry.BlobHash != nil {
					node.BlobHash = *entry.BlobHash
				}
				node.Size = entry.Size
				node.Mime = entry.Mime
				node.FileType = entry.FileType
				node.Binary = entry.Binary
			}
			parent := nodeByPath[parentPath]
			parent.Children = append(parent.Children, node)
			nodeByPath[currentPath] = &parent.Children[len(parent.Children)-1]
			parentPath = currentPath
		}
	}
	sortTree(root.Children)
	return root
}

func sortTree(nodes []TreeNode) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	for i := range nodes {
		sortTree(nodes[i].Children)
	}
}

func sortedFilePaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return paths
}

func hashTree(entries []skillRevisionEntryRow) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		hash := ""
		if entry.BlobHash != nil {
			hash = *entry.BlobHash
		}
		lines = append(lines, entry.Path+"\x00"+entry.EntryType+"\x00"+hash)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func classifyFile(filePath string, data []byte) (string, string, bool) {
	ext := strings.ToLower(path.Ext(filePath))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown", "markdown", false
	case ".png":
		return "image/png", "image", true
	case ".jpg", ".jpeg":
		return "image/jpeg", "image", true
	case ".gif":
		return "image/gif", "image", true
	case ".webp":
		return "image/webp", "image", true
	case ".py", ".txt", ".json", ".yaml", ".yml", ".toml", ".js", ".ts", ".css", ".html":
		return "text/plain", "text", false
	}
	if utf8.Valid(data) {
		return "text/plain", "text", false
	}
	return "application/octet-stream", "binary", true
}

func valueOr(ptr *string, fallback string) string {
	if ptr == nil {
		return fallback
	}
	return *ptr
}

func valueOrEmpty(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func newID() string {
	if id, err := uuid.NewRandom(); err == nil {
		return id.String()
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
