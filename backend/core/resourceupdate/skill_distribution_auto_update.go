package resourceupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	skillbuiltin "lazymind/core/skillv2/builtin"
	skilldistribution "lazymind/core/skillv2/distribution"
	skillrevision "lazymind/core/skillv2/revision"
	skillservice "lazymind/core/skillv2/service"
)

type skillDistributionAutoUpdateResult struct {
	Applied       int
	PendingReview int
}

type skillDistributionAutoUpdater struct {
	db          *gorm.DB
	catalogPath func() string
	apply       func(context.Context, string, string) (skilldistribution.AutoApplyResult, error)
}

type revisionDraftCommitter struct {
	service *skillrevision.Service
}

func (committer revisionDraftCommitter) CommitDraft(ctx context.Context, skillID, userID string, draftVersion int64) (string, error) {
	response, err := committer.service.CommitDraft(ctx, skillrevision.CommitDraftRequest{
		SkillID: skillID, UserID: userID, DraftVersion: draftVersion,
	})
	return response.RevisionID, err
}

func newSkillDistributionAutoUpdater(db *gorm.DB) *skillDistributionAutoUpdater {
	root := skillObjectRootForSkillV2Bridge()
	distributions := skilldistribution.NewService(skilldistribution.ServiceDeps{
		DB:       db,
		Blobs:    skillservice.NewBlobStore(db, skillservice.NewLocalObjectStore(root)),
		Provider: skillbuiltin.DistributionProvider{},
	})
	committer := revisionDraftCommitter{service: skillrevision.NewService(skillrevision.ServiceDeps{
		DB: db, BlobStore: skillrevision.NewBlobStore(db, skillrevision.NewLocalObjectStore(root)),
	})}
	return &skillDistributionAutoUpdater{
		db:          db,
		catalogPath: skillbuiltin.CatalogPath,
		apply: func(ctx context.Context, skillID, userID string) (skilldistribution.AutoApplyResult, error) {
			return distributions.AutoApply(ctx, skilldistribution.PrepareRequest{SkillID: skillID, UserID: userID}, committer)
		},
	}
}

func (updater *skillDistributionAutoUpdater) RunOnce(ctx context.Context, limit int) (skillDistributionAutoUpdateResult, error) {
	var result skillDistributionAutoUpdateResult
	if updater == nil || updater.db == nil || updater.catalogPath == nil || updater.apply == nil {
		return result, nil
	}
	catalogPath := strings.TrimSpace(updater.catalogPath())
	if catalogPath == "" {
		return result, nil
	}
	catalog, err := skillbuiltin.LoadCatalog(catalogPath)
	if err != nil {
		return result, err
	}
	latestByUID := make(map[string]string, len(catalog.Skills))
	for _, entry := range catalog.Skills {
		latestByUID[entry.UID] = entry.ArchiveSHA256
	}

	var rows []struct {
		SkillID              string `gorm:"column:skill_id"`
		UserID               string `gorm:"column:user_id"`
		BuiltinSkillUID      string `gorm:"column:builtin_skill_uid"`
		CurrentArchiveSHA256 string `gorm:"column:current_archive_sha256"`
		PendingArchiveSHA256 string `gorm:"column:pending_archive_sha256"`
		Conflicts            []byte `gorm:"column:conflicts"`
	}
	if err := updater.db.WithContext(ctx).
		Table("skills AS s").
		Select("s.id AS skill_id, s.owner_user_id AS user_id, s.origin_builtin_skill_uid AS builtin_skill_uid, COALESCE(b.current_archive_sha256, '') AS current_archive_sha256, COALESCE(b.pending_archive_sha256, '') AS pending_archive_sha256, COALESCE(b.conflicts, '[]') AS conflicts").
		Joins("LEFT JOIN skill_distribution_bindings AS b ON b.skill_id = s.id").
		Where("s.auto_evo = ? AND s.deleted_at IS NULL AND s.origin_builtin_skill_uid <> ''", true).
		Order("s.owner_user_id ASC, s.id ASC").
		Find(&rows).Error; err != nil {
		return result, err
	}

	var runErr error
	processed := 0
	for _, row := range rows {
		latestArchive, found := latestByUID[row.BuiltinSkillUID]
		if !found || (row.PendingArchiveSHA256 == "" && row.CurrentArchiveSHA256 == latestArchive) {
			continue
		}
		if row.PendingArchiveSHA256 != "" {
			var conflicts []skilldistribution.Conflict
			if err := json.Unmarshal(row.Conflicts, &conflicts); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("%w: skill_id=%s", err, row.SkillID))
				continue
			}
			if len(conflicts) > 0 {
				continue
			}
		}
		if processed >= limit {
			break
		}
		processed++
		applied, err := updater.apply(ctx, row.SkillID, row.UserID)
		if errors.Is(err, skilldistribution.ErrDraftActive) {
			continue
		}
		if err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("%w: skill_id=%s", err, row.SkillID))
			continue
		}
		if applied.Applied {
			result.Applied++
		}
		if applied.PendingReview {
			result.PendingReview++
		}
	}
	return result, runErr
}
