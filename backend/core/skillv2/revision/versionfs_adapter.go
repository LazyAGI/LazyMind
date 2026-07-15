package revision

import (
	"context"
	"time"

	"gorm.io/gorm"

	skillreview "lazymind/core/skillv2/review"
	skillsearch "lazymind/core/skillv2/search"
	"lazymind/core/versionfs"
)

type versionStore struct {
	service *Service
}

func (s versionStore) LoadHead(ctx context.Context, tx *gorm.DB, skillID string) (versionfs.HeadState, error) {
	var skill skillRow
	if err := tx.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return versionfs.HeadState{}, err
	}
	return versionfs.HeadState{RevisionID: valueOrEmpty(skill.HeadRevisionID)}, nil
}

func (s versionStore) LoadDraft(ctx context.Context, tx *gorm.DB, skillID string) (versionfs.DraftState, error) {
	var draft skillDraftRow
	if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Take(&draft).Error; err != nil {
		return versionfs.DraftState{}, err
	}
	return versionfs.DraftState{BaseRevisionID: valueOrEmpty(draft.BaseRevisionID), Version: draft.Version}, nil
}

func (s versionStore) HasDraftChanges(ctx context.Context, tx *gorm.DB, skillID string, draft versionfs.DraftState) (bool, error) {
	var count int64
	if err := tx.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", skillID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s versionStore) ClaimDraft(ctx context.Context, tx *gorm.DB, skillID string, draft versionfs.DraftState, userID string, now time.Time) (versionfs.DraftState, error) {
	updates := map[string]any{
		"version":          gorm.Expr("version + 1"),
		"updated_at":       now,
		"draft_updated_at": now,
	}
	if userID != "" {
		updates["updated_by"] = userID
	}
	result := tx.WithContext(ctx).Model(&skillDraftRow{}).Where("skill_id = ? AND version = ?", skillID, draft.Version).Updates(updates)
	if result.Error != nil {
		return versionfs.DraftState{}, result.Error
	}
	if result.RowsAffected != 1 {
		return versionfs.DraftState{}, versionfs.ErrStaleDraftVersion
	}
	draft.Version++
	return draft, nil
}

func (s versionStore) DraftEntries(ctx context.Context, tx *gorm.DB, skillID string, baseRevisionID string) (map[string]versionfs.Entry, error) {
	entries, err := mergedEntriesForDraft(ctx, tx, skillID, baseRevisionID)
	if err != nil {
		return nil, err
	}
	return toVersionEntries(entries), nil
}

func (s versionStore) RevisionEntries(ctx context.Context, tx *gorm.DB, skillID string, revisionID string) (map[string]versionfs.Entry, error) {
	entries, err := entriesForRevision(ctx, tx, skillID, revisionID)
	if err != nil {
		return nil, err
	}
	return toVersionEntries(entries), nil
}

func (s versionStore) EnsureBlobs(ctx context.Context, tx *gorm.DB, entries map[string]versionfs.Entry) error {
	return s.service.ensureEntryBlobs(ctx, tx, fromVersionEntries(entries))
}

func (s versionStore) NextRevisionNo(ctx context.Context, tx *gorm.DB, skillID string) (int64, error) {
	return nextRevisionNo(tx, skillID)
}

func (s versionStore) CreateRevision(ctx context.Context, tx *gorm.DB, revision versionfs.RevisionRecord, entries map[string]versionfs.Entry) error {
	parent := revision.ParentRevisionID
	createdBy := nullableString(revision.CreatedBy)
	row := skillRevisionRow{
		ID:               revision.ID,
		SkillID:          revision.ResourceID,
		ParentRevisionID: &parent,
		RevisionNo:       revision.RevisionNo,
		TreeHash:         revision.TreeHash,
		Message:          revision.Message,
		ChangeSource:     revision.ChangeSource,
		SourceRefType:    revision.SourceRefType,
		SourceRefID:      revision.SourceRefID,
		CreatedBy:        createdBy,
		CreatedAt:        revision.CreatedAt,
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	return createRevisionEntries(tx, revision.ID, fromVersionEntries(entries))
}

func (s versionStore) UpdateHead(ctx context.Context, tx *gorm.DB, skillID string, previousRevisionID string, revisionID string, now time.Time) error {
	result := tx.WithContext(ctx).Model(&skillRow{}).Where("id = ? AND head_revision_id = ?", skillID, previousRevisionID).Updates(map[string]any{
		"head_revision_id": revisionID,
		"version":          gorm.Expr("version + 1"),
		"updated_at":       now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return versionfs.ErrHeadRevisionConflict
	}
	return nil
}

func (s versionStore) ResetDraftAfterCommit(ctx context.Context, tx *gorm.DB, skillID string, revisionID string, draft versionfs.DraftState, userID string, now time.Time) error {
	if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&skillDraftEntryRow{}).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Model(&skillDraftRow{}).Where("skill_id = ?", skillID).Updates(map[string]any{
		"base_revision_id": revisionID,
		"task_id":          "",
		"conversation_id":  nil,
		"version":          draft.Version,
		"updated_at":       now,
		"draft_updated_at": nil,
	}).Error
}

func (s versionStore) ResetDraftAfterRollback(ctx context.Context, tx *gorm.DB, skillID string, revisionID string, targetEntries map[string]versionfs.Entry, draft versionfs.DraftState, userID string, now time.Time) error {
	return tx.WithContext(ctx).Model(&skillDraftRow{}).Where("skill_id = ?", skillID).Updates(map[string]any{
		"base_revision_id": revisionID,
		"task_id":          "",
		"conversation_id":  nil,
		"updated_by":       nullableString(userID),
		"version":          draft.Version,
		"updated_at":       now,
		"draft_updated_at": nil,
	}).Error
}

func (s versionStore) MarkActiveReviews(ctx context.Context, tx *gorm.DB, skillID string, status string, userID string, now time.Time) error {
	return skillreview.MarkSkillReviews(ctx, tx, skillID, status, userID, now)
}

func (s versionStore) EnforceRevisionLimit(ctx context.Context, tx *gorm.DB, skillID string, protected map[string]bool) error {
	return s.service.enforceRevisionLimit(ctx, tx, skillID, protected)
}

func (s versionStore) AfterCommit(ctx context.Context, tx *gorm.DB, revision versionfs.RevisionRecord, entries map[string]versionfs.Entry) error {
	return skillsearch.RebuildSkillTx(ctx, tx, revision.ResourceID, revision.CreatedAt)
}

func (s versionStore) AfterRollback(ctx context.Context, tx *gorm.DB, skillID string, revisionID string, entries map[string]versionfs.Entry, now time.Time) error {
	return skillsearch.RebuildSkillTx(ctx, tx, skillID, now)
}

func (s versionStore) ListBlobHashes(ctx context.Context, tx *gorm.DB) ([]string, error) {
	var blobs []skillBlobRow
	if err := tx.WithContext(ctx).Find(&blobs).Error; err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(blobs))
	for _, blob := range blobs {
		hashes = append(hashes, blob.Hash)
	}
	return hashes, nil
}

func (s versionStore) BlobReferenced(ctx context.Context, tx *gorm.DB, hash string) (bool, error) {
	return blobReferenced(tx, hash)
}

func (s versionStore) DeleteBlob(ctx context.Context, tx *gorm.DB, hash string) error {
	return s.service.blobStore.DeleteBlob(ctx, tx, hash)
}

func toVersionEntries(entries map[string]mergedEntry) map[string]versionfs.Entry {
	out := make(map[string]versionfs.Entry, len(entries))
	for path, entry := range entries {
		out[path] = toVersionEntry(entry)
	}
	return out
}

func toVersionEntry(entry mergedEntry) versionfs.Entry {
	hash := ""
	if entry.BlobHash != nil {
		hash = *entry.BlobHash
	}
	return versionfs.Entry{
		Path:      entry.Path,
		EntryType: entry.EntryType,
		BlobHash:  hash,
		Size:      entry.Size,
		Mime:      entry.Mime,
		FileType:  entry.FileType,
		Binary:    entry.Binary,
		Mode:      entry.Mode,
	}
}

func fromVersionEntries(entries map[string]versionfs.Entry) map[string]mergedEntry {
	out := make(map[string]mergedEntry, len(entries))
	for path, entry := range entries {
		out[path] = fromVersionEntry(entry)
	}
	return out
}

func fromVersionEntrySlice(entries []versionfs.Entry) []mergedEntry {
	out := make([]mergedEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, fromVersionEntry(entry))
	}
	return out
}

func toVersionOverlays(overlays []skillDraftEntryRow) []versionfs.Overlay {
	out := make([]versionfs.Overlay, 0, len(overlays))
	for _, overlay := range overlays {
		hash := ""
		if overlay.BlobHash != nil {
			hash = *overlay.BlobHash
		}
		out = append(out, versionfs.Overlay{
			Path:      overlay.Path,
			Op:        overlay.Op,
			EntryType: overlay.EntryType,
			BlobHash:  hash,
			Size:      overlay.Size,
			Mime:      overlay.Mime,
			FileType:  overlay.FileType,
			Binary:    overlay.Binary,
			Mode:      overlay.Mode,
			UpdatedAt: overlay.UpdatedAt,
		})
	}
	return out
}

func fromVersionEntry(entry versionfs.Entry) mergedEntry {
	var hash *string
	if entry.BlobHash != "" {
		hash = &entry.BlobHash
	}
	return mergedEntry{
		Path:      entry.Path,
		EntryType: entry.EntryType,
		BlobHash:  hash,
		Size:      entry.Size,
		Mime:      entry.Mime,
		FileType:  entry.FileType,
		Binary:    entry.Binary,
		Mode:      entry.Mode,
	}
}
