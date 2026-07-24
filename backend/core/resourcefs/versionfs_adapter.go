package resourcefs

import (
	"context"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/versionfs"
)

type versionStore struct{}

func (versionStore) LoadHead(ctx context.Context, tx *gorm.DB, resourceID string) (versionfs.HeadState, error) {
	var resource orm.PersonalResource
	if err := tx.WithContext(ctx).Where("id = ?", resourceID).Take(&resource).Error; err != nil {
		return versionfs.HeadState{}, err
	}
	return versionfs.HeadState{RevisionID: valueOrEmpty(resource.HeadRevisionID)}, nil
}

func (versionStore) LoadDraft(ctx context.Context, tx *gorm.DB, resourceID string) (versionfs.DraftState, error) {
	var draft orm.PersonalResourceDraft
	if err := tx.WithContext(ctx).Where("resource_id = ?", resourceID).Take(&draft).Error; err != nil {
		return versionfs.DraftState{}, err
	}
	return versionfs.DraftState{
		BaseRevisionID: valueOrEmpty(draft.BaseRevisionID),
		Version:        draft.Version,
		BlobHash:       draft.BlobHash,
		Status:         draft.DraftStatus,
	}, nil
}

func (versionStore) HasDraftChanges(ctx context.Context, tx *gorm.DB, resourceID string, draft versionfs.DraftState) (bool, error) {
	return draft.Status == "pending_confirm", nil
}

func (versionStore) ClaimDraft(ctx context.Context, tx *gorm.DB, resourceID string, draft versionfs.DraftState, userID string, now time.Time) (versionfs.DraftState, error) {
	updates := map[string]any{
		"version":    gorm.Expr("version + 1"),
		"updated_at": now,
	}
	if userID != "" {
		updates["updated_by"] = nullableString(userID)
	}
	result := tx.WithContext(ctx).Model(&orm.PersonalResourceDraft{}).
		Where("resource_id = ? AND version = ?", resourceID, draft.Version).
		Updates(updates)
	if result.Error != nil {
		return versionfs.DraftState{}, result.Error
	}
	if result.RowsAffected != 1 {
		return versionfs.DraftState{}, versionfs.ErrStaleDraftVersion
	}
	draft.Version++
	return draft, nil
}

func (versionStore) DraftEntries(ctx context.Context, tx *gorm.DB, resourceID string, baseRevisionID string) (map[string]versionfs.Entry, error) {
	var draft orm.PersonalResourceDraft
	if err := tx.WithContext(ctx).Where("resource_id = ?", resourceID).Take(&draft).Error; err != nil {
		return nil, err
	}
	return map[string]versionfs.Entry{draft.Path: entryFromDraft(draft)}, nil
}

func (versionStore) RevisionEntries(ctx context.Context, tx *gorm.DB, resourceID string, revisionID string) (map[string]versionfs.Entry, error) {
	revision, err := findRevisionByID(ctx, tx, resourceID, revisionID)
	if err != nil {
		return nil, err
	}
	return map[string]versionfs.Entry{revision.Path: entryFromRevision(revision)}, nil
}

func (versionStore) EnsureBlobs(ctx context.Context, tx *gorm.DB, entries map[string]versionfs.Entry) error {
	for _, entry := range entries {
		if entry.EntryType != versionfs.EntryTypeFile || entry.BlobHash == "" {
			continue
		}
		if _, err := findBlob(ctx, tx, entry.BlobHash); err != nil {
			return err
		}
	}
	return nil
}

func (versionStore) NextRevisionNo(ctx context.Context, tx *gorm.DB, resourceID string) (int64, error) {
	return nextRevisionNo(ctx, tx, resourceID)
}

func (versionStore) CreateRevision(ctx context.Context, tx *gorm.DB, revision versionfs.RevisionRecord, entries map[string]versionfs.Entry) error {
	entry, ok := singleFileEntry(entries)
	if !ok {
		return ErrInvalidPath
	}
	parent := nullableString(revision.ParentRevisionID)
	row := orm.PersonalResourceRevision{
		ID:               revision.ID,
		ResourceID:       revision.ResourceID,
		ParentRevisionID: parent,
		RevisionNo:       revision.RevisionNo,
		Path:             entry.Path,
		BlobHash:         entry.BlobHash,
		ContentHash:      entry.BlobHash,
		Size:             entry.Size,
		Mime:             entry.Mime,
		FileType:         entry.FileType,
		Binary:           entry.Binary,
		Message:          revision.Message,
		ChangeSource:     revision.ChangeSource,
		SourceRefType:    revision.SourceRefType,
		SourceRefID:      revision.SourceRefID,
		CreatedBy:        nullableString(revision.CreatedBy),
		CreatedAt:        revision.CreatedAt,
	}
	return tx.WithContext(ctx).Create(&row).Error
}

func (versionStore) UpdateHead(ctx context.Context, tx *gorm.DB, resourceID string, previousRevisionID string, revisionID string, now time.Time) error {
	result := tx.WithContext(ctx).Model(&orm.PersonalResource{}).
		Where("id = ? AND head_revision_id = ?", resourceID, previousRevisionID).
		Updates(map[string]any{
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

func (versionStore) ResetDraftAfterCommit(ctx context.Context, tx *gorm.DB, resourceID string, revisionID string, draft versionfs.DraftState, userID string, now time.Time) error {
	revision, err := findRevisionByID(ctx, tx, resourceID, revisionID)
	if err != nil {
		return err
	}
	return resetDraftToRevision(ctx, tx, resourceID, revision, draft.Version, userID, now)
}

func (versionStore) ResetDraftAfterRollback(ctx context.Context, tx *gorm.DB, resourceID string, revisionID string, targetEntries map[string]versionfs.Entry, draft versionfs.DraftState, userID string, now time.Time) error {
	revision, err := findRevisionByID(ctx, tx, resourceID, revisionID)
	if err != nil {
		return err
	}
	return resetDraftToRevision(ctx, tx, resourceID, revision, draft.Version, userID, now)
}

func (versionStore) MarkActiveReviews(ctx context.Context, tx *gorm.DB, resourceID string, status string, userID string, now time.Time) error {
	return markActiveReviewSessions(ctx, tx, resourceID, status, userID, now)
}

func (versionStore) EnforceRevisionLimit(ctx context.Context, tx *gorm.DB, resourceID string, protected map[string]bool) error {
	return nil
}

func (versionStore) AfterCommit(ctx context.Context, tx *gorm.DB, revision versionfs.RevisionRecord, entries map[string]versionfs.Entry) error {
	return nil
}

func (versionStore) AfterRollback(ctx context.Context, tx *gorm.DB, resourceID string, revisionID string, entries map[string]versionfs.Entry, now time.Time) error {
	return nil
}

func (versionStore) ListBlobHashes(ctx context.Context, tx *gorm.DB) ([]string, error) {
	var rows []orm.PersonalResourceBlob
	if err := tx.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(rows))
	for _, row := range rows {
		hashes = append(hashes, row.Hash)
	}
	return hashes, nil
}

func (versionStore) BlobReferenced(ctx context.Context, tx *gorm.DB, hash string) (bool, error) {
	var revisionRefs int64
	if err := tx.WithContext(ctx).Model(&orm.PersonalResourceRevision{}).Where("blob_hash = ?", hash).Count(&revisionRefs).Error; err != nil {
		return false, err
	}
	if revisionRefs > 0 {
		return true, nil
	}
	var draftRefs int64
	if err := tx.WithContext(ctx).Model(&orm.PersonalResourceDraft{}).Where("blob_hash = ?", hash).Count(&draftRefs).Error; err != nil {
		return false, err
	}
	return draftRefs > 0, nil
}

func (versionStore) DeleteBlob(ctx context.Context, tx *gorm.DB, hash string) error {
	return tx.WithContext(ctx).Where("hash = ?", hash).Delete(&orm.PersonalResourceBlob{}).Error
}

func entryFromDraft(draft orm.PersonalResourceDraft) versionfs.Entry {
	return versionfs.Entry{
		Path:      draft.Path,
		EntryType: versionfs.EntryTypeFile,
		BlobHash:  draft.BlobHash,
		Size:      draft.Size,
		Mime:      draft.Mime,
		FileType:  draft.FileType,
		Binary:    draft.Binary,
		Mode:      0o644,
		FromDraft: true,
	}
}

func entryFromRevision(revision orm.PersonalResourceRevision) versionfs.Entry {
	return versionfs.Entry{
		Path:      revision.Path,
		EntryType: versionfs.EntryTypeFile,
		BlobHash:  revision.BlobHash,
		Size:      revision.Size,
		Mime:      revision.Mime,
		FileType:  revision.FileType,
		Binary:    revision.Binary,
		Mode:      0o644,
		FromHead:  true,
	}
}

func singleFileEntry(entries map[string]versionfs.Entry) (versionfs.Entry, bool) {
	if len(entries) != 1 {
		return versionfs.Entry{}, false
	}
	for _, entry := range entries {
		if entry.EntryType != versionfs.EntryTypeFile || entry.BlobHash == "" {
			return versionfs.Entry{}, false
		}
		return entry, true
	}
	return versionfs.Entry{}, false
}

func resetDraftToRevision(ctx context.Context, tx *gorm.DB, resourceID string, revision orm.PersonalResourceRevision, version int64, userID string, now time.Time) error {
	return tx.WithContext(ctx).Model(&orm.PersonalResourceDraft{}).Where("resource_id = ?", resourceID).Updates(map[string]any{
		"base_revision_id": revision.ID,
		"path":             revision.Path,
		"blob_hash":        revision.BlobHash,
		"content_hash":     revision.ContentHash,
		"size":             revision.Size,
		"mime":             revision.Mime,
		"file_type":        revision.FileType,
		"binary":           revision.Binary,
		"draft_status":     "",
		"draft_updated_at": nil,
		"task_id":          "",
		"conversation_id":  nil,
		"updated_by":       nullableString(userID),
		"version":          version,
		"updated_at":       now,
	}).Error
}
