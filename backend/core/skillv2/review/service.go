package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	skilldiff "lazymind/core/skillv2/diff"
	skillsearch "lazymind/core/skillv2/search"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/versionfs"
)

const (
	statusActive      = "active"
	statusInvalidated = "invalidated"
	statusCommitted   = "committed"
	statusDiscarded   = "discarded"

	decisionPending  = "pending"
	decisionAccepted = "accepted"
	decisionRejected = "rejected"

	defaultUndoLimit = 20
)

type ServiceDeps struct {
	DB        *gorm.DB
	BlobStore *skillservice.BlobStore
	UndoLimit int
}

type Service struct {
	db        *gorm.DB
	blobStore *skillservice.BlobStore
	undoLimit int
	clock     clock
}

type PrepareFileRequest struct {
	SkillID string
	UserID  string
	File    skilldiff.DiffFile
}

type ActionRequest struct {
	SkillID               string
	UserID                string
	ReviewID              string
	ExpectedReviewVersion int64
	Items                 []ActionItem
}

type ActionItem struct {
	Path     string
	HunkID   string
	Decision string
}

type ActionResponse struct {
	ReviewVersion int64
	BatchID       string
	CanUndo       bool
}

type UndoRequest struct {
	SkillID               string
	UserID                string
	ReviewID              string
	ExpectedReviewVersion int64
}

type UndoResponse struct {
	ReviewVersion int64
	UndoneBatchID string
	Items         []ActionItem
	CanUndo       bool
}

type CommitRequest struct {
	SkillID               string
	UserID                string
	ReviewID              string
	ExpectedReviewVersion int64
}

type CommitResponse struct {
	RevisionID string
	RevisionNo int64
}

type ReviewInfo struct {
	ReviewID          string
	ReviewVersion     int64
	DraftVersion      int64
	BaseRevisionID    string
	DraftSnapshotHash string
	CanUndo           bool
}

func NewService(deps ServiceDeps) *Service {
	undoLimit := deps.UndoLimit
	if undoLimit <= 0 {
		undoLimit = defaultUndoLimit
	}
	return &Service{db: deps.DB, blobStore: deps.BlobStore, undoLimit: undoLimit, clock: systemClock{}}
}

func (s *Service) PrepareFile(ctx context.Context, req PrepareFileRequest) (skilldiff.DiffFile, error) {
	session, err := s.ensureSession(ctx, s.db, req.SkillID, req.UserID)
	if err != nil {
		return skilldiff.DiffFile{}, err
	}
	decisions, err := s.currentDecisions(ctx, s.db, session.ID)
	if err != nil {
		return skilldiff.DiffFile{}, err
	}
	canUndo, err := s.canUndo(ctx, s.db, session.ID)
	if err != nil {
		return skilldiff.DiffFile{}, err
	}
	file := req.File
	s.annotateFile(&file, session, decisions, canUndo)
	file.DiffEntryLines = reviewDisplayDiffEntryLines(file.DiffEntryLines)
	return file, nil
}

func (s *Service) Action(ctx context.Context, req ActionRequest) (ActionResponse, error) {
	var out ActionResponse
	if req.ExpectedReviewVersion <= 0 {
		return out, fmt.Errorf("expected_review_version required")
	}
	if len(req.Items) == 0 {
		return out, fmt.Errorf("items required")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, err := s.loadActiveSession(ctx, tx, req.SkillID, req.ReviewID)
		if err != nil {
			return err
		}
		if session.Version != req.ExpectedReviewVersion {
			return fmt.Errorf("stale review version")
		}
		if err := s.validateSnapshot(ctx, tx, session); err != nil {
			return err
		}
		known, err := s.hunksByPath(ctx, tx, session, uniquePaths(req.Items))
		if err != nil {
			return err
		}
		decisions, err := s.currentDecisions(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		seq, err := nextBatchSequence(tx, session.ID)
		if err != nil {
			return err
		}
		batchID := uuid.NewString()
		createdBy := nullableString(req.UserID)
		if err := tx.Create(&reviewActionBatchRow{
			ID:              batchID,
			ReviewSessionID: session.ID,
			Sequence:        seq,
			CreatedBy:       createdBy,
			CreatedAt:       now,
		}).Error; err != nil {
			return err
		}
		rows := make([]reviewActionItemRow, 0, len(req.Items))
		for _, item := range req.Items {
			path := strings.TrimSpace(item.Path)
			hunkID := strings.TrimSpace(item.HunkID)
			decision := strings.TrimSpace(item.Decision)
			if decision != decisionAccepted && decision != decisionRejected {
				return fmt.Errorf("invalid decision")
			}
			if !known[path][hunkID] {
				return fmt.Errorf("unknown hunk_id")
			}
			before := decisions[versionfs.DecisionKey(path, hunkID)]
			if before == "" {
				before = decisionPending
			}
			rows = append(rows, reviewActionItemRow{
				ID:              uuid.NewString(),
				BatchID:         batchID,
				ReviewSessionID: session.ID,
				Path:            path,
				HunkID:          hunkID,
				BeforeDecision:  before,
				AfterDecision:   decision,
				CreatedAt:       now,
			})
			decisions[versionfs.DecisionKey(path, hunkID)] = decision
		}
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
		nextVersion := session.Version + 1
		if err := tx.Model(&reviewSessionRow{}).Where("id = ?", session.ID).Updates(map[string]any{
			"version":    nextVersion,
			"updated_by": nullableString(req.UserID),
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := s.lockOldUndoBatches(tx, session.ID, seq); err != nil {
			return err
		}
		canUndo, err := s.canUndo(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		out = ActionResponse{ReviewVersion: nextVersion, BatchID: batchID, CanUndo: canUndo}
		return nil
	})
	return out, err
}

func (s *Service) Undo(ctx context.Context, req UndoRequest) (UndoResponse, error) {
	var out UndoResponse
	if req.ExpectedReviewVersion <= 0 {
		return out, fmt.Errorf("expected_review_version required")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, err := s.loadActiveSession(ctx, tx, req.SkillID, req.ReviewID)
		if err != nil {
			return err
		}
		if session.Version != req.ExpectedReviewVersion {
			return fmt.Errorf("stale review version")
		}
		if err := s.validateSnapshot(ctx, tx, session); err != nil {
			return err
		}
		var batch reviewActionBatchRow
		if err := tx.Where("review_session_id = ? AND undone_at IS NULL AND undo_locked = ?", session.ID, false).
			Order("sequence DESC").Take(&batch).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("undo not available")
			}
			return err
		}
		var items []reviewActionItemRow
		if err := tx.Where("batch_id = ?", batch.ID).Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
			return err
		}
		now := s.clock.Now()
		if err := tx.Model(&reviewActionBatchRow{}).Where("id = ?", batch.ID).Updates(map[string]any{
			"undone_at": now,
			"undone_by": nullableString(req.UserID),
		}).Error; err != nil {
			return err
		}
		nextVersion := session.Version + 1
		if err := tx.Model(&reviewSessionRow{}).Where("id = ?", session.ID).Updates(map[string]any{
			"version":    nextVersion,
			"updated_by": nullableString(req.UserID),
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
		outItems := make([]ActionItem, 0, len(items))
		for _, item := range items {
			outItems = append(outItems, ActionItem{Path: item.Path, HunkID: item.HunkID, Decision: item.BeforeDecision})
		}
		canUndo, err := s.canUndo(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		out = UndoResponse{ReviewVersion: nextVersion, UndoneBatchID: batch.ID, Items: outItems, CanUndo: canUndo}
		return nil
	})
	return out, err
}

func (s *Service) Commit(ctx context.Context, req CommitRequest) (CommitResponse, error) {
	var out CommitResponse
	if req.ExpectedReviewVersion <= 0 {
		return out, fmt.Errorf("expected_review_version required")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, err := s.loadActiveSession(ctx, tx, req.SkillID, req.ReviewID)
		if err != nil {
			return err
		}
		if session.Version != req.ExpectedReviewVersion {
			return fmt.Errorf("stale review version")
		}
		if err := s.validateSnapshot(ctx, tx, session); err != nil {
			return err
		}
		decisions, err := s.currentDecisions(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		baseEntries, err := entriesForRevision(ctx, tx, req.SkillID, session.BaseRevisionID)
		if err != nil {
			return err
		}
		draftEntries, err := mergedEntriesForDraft(ctx, tx, req.SkillID, session.BaseRevisionID)
		if err != nil {
			return err
		}
		finalEntries, err := s.reviewedEntries(ctx, tx, session, baseEntries, draftEntries, decisions)
		if err != nil {
			return err
		}
		commit, err := versionfs.NewEngine(versionfs.EngineDeps{DB: s.db, Store: reviewVersionStore{service: s}, Clock: s.clock}).CommitEntriesTx(ctx, tx, versionfs.CommitEntriesRequest{
			ResourceID:             req.SkillID,
			UserID:                 req.UserID,
			ParentRevisionID:       session.BaseRevisionID,
			ExpectedHeadRevisionID: session.BaseRevisionID,
			ExpectedDraftVersion:   session.DraftVersionAtStart,
			ChangeSource:           "draft_review",
			Entries:                toVersionEntries(finalEntries),
		})
		if err != nil {
			return err
		}
		if err := deleteReviewSession(ctx, tx, session.ID); err != nil {
			return err
		}
		out = CommitResponse{RevisionID: commit.RevisionID, RevisionNo: commit.RevisionNo}
		return nil
	})
	return out, err
}

func MarkSkillReviews(ctx context.Context, tx *gorm.DB, skillID, status, userID string, now time.Time) error {
	return markSkillReviews(ctx, tx, skillID, status, userID, now)
}

type reviewVersionStore struct {
	service *Service
}

func (s reviewVersionStore) LoadHead(ctx context.Context, tx *gorm.DB, skillID string) (versionfs.HeadState, error) {
	var skill skillRow
	if err := tx.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return versionfs.HeadState{}, err
	}
	return versionfs.HeadState{RevisionID: valueOrEmpty(skill.HeadRevisionID)}, nil
}

func (s reviewVersionStore) LoadDraft(ctx context.Context, tx *gorm.DB, skillID string) (versionfs.DraftState, error) {
	var draft skillDraftRow
	if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Take(&draft).Error; err != nil {
		return versionfs.DraftState{}, err
	}
	return versionfs.DraftState{BaseRevisionID: valueOrEmpty(draft.BaseRevisionID), Version: draft.Version}, nil
}

func (s reviewVersionStore) HasDraftChanges(ctx context.Context, tx *gorm.DB, skillID string, draft versionfs.DraftState) (bool, error) {
	var count int64
	if err := tx.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", skillID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s reviewVersionStore) ClaimDraft(ctx context.Context, tx *gorm.DB, skillID string, draft versionfs.DraftState, userID string, now time.Time) (versionfs.DraftState, error) {
	updates := map[string]any{
		"version":          gorm.Expr("version + 1"),
		"updated_at":       now,
		"draft_updated_at": now,
	}
	if userID != "" {
		updates["updated_by"] = nullableString(userID)
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

func (s reviewVersionStore) DraftEntries(ctx context.Context, tx *gorm.DB, skillID string, baseRevisionID string) (map[string]versionfs.Entry, error) {
	entries, err := mergedEntriesForDraft(ctx, tx, skillID, baseRevisionID)
	if err != nil {
		return nil, err
	}
	return toVersionEntries(entries), nil
}

func (s reviewVersionStore) RevisionEntries(ctx context.Context, tx *gorm.DB, skillID string, revisionID string) (map[string]versionfs.Entry, error) {
	entries, err := entriesForRevision(ctx, tx, skillID, revisionID)
	if err != nil {
		return nil, err
	}
	return toVersionEntries(entries), nil
}

func (s reviewVersionStore) EnsureBlobs(ctx context.Context, tx *gorm.DB, entries map[string]versionfs.Entry) error {
	return nil
}

func (s reviewVersionStore) NextRevisionNo(ctx context.Context, tx *gorm.DB, skillID string) (int64, error) {
	return nextRevisionNo(tx, skillID)
}

func (s reviewVersionStore) CreateRevision(ctx context.Context, tx *gorm.DB, revision versionfs.RevisionRecord, entries map[string]versionfs.Entry) error {
	parent := revision.ParentRevisionID
	row := skillRevisionRow{
		ID:               revision.ID,
		SkillID:          revision.ResourceID,
		ParentRevisionID: &parent,
		RevisionNo:       revision.RevisionNo,
		TreeHash:         revision.TreeHash,
		ChangeSource:     revision.ChangeSource,
		CreatedBy:        nullableString(revision.CreatedBy),
		CreatedAt:        revision.CreatedAt,
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return err
	}
	return createRevisionEntries(tx, revision.ID, fromVersionEntries(entries))
}

func (s reviewVersionStore) UpdateHead(ctx context.Context, tx *gorm.DB, skillID string, previousRevisionID string, revisionID string, now time.Time) error {
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

func (s reviewVersionStore) ResetDraftAfterCommit(ctx context.Context, tx *gorm.DB, skillID string, revisionID string, draft versionfs.DraftState, userID string, now time.Time) error {
	if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&skillDraftEntryRow{}).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Model(&skillDraftRow{}).Where("skill_id = ?", skillID).Updates(map[string]any{
		"base_revision_id": revisionID,
		"task_id":          "",
		"conversation_id":  nil,
		"updated_by":       nullableString(userID),
		"version":          draft.Version,
		"draft_updated_at": nil,
		"updated_at":       now,
	}).Error
}

func (s reviewVersionStore) ResetDraftAfterRollback(ctx context.Context, tx *gorm.DB, skillID string, revisionID string, targetEntries map[string]versionfs.Entry, draft versionfs.DraftState, userID string, now time.Time) error {
	return tx.WithContext(ctx).Model(&skillDraftRow{}).Where("skill_id = ?", skillID).Updates(map[string]any{
		"base_revision_id": revisionID,
		"updated_by":       nullableString(userID),
		"version":          draft.Version,
		"updated_at":       now,
	}).Error
}

func (s reviewVersionStore) MarkActiveReviews(ctx context.Context, tx *gorm.DB, skillID string, status string, userID string, now time.Time) error {
	return markSkillReviews(ctx, tx, skillID, status, userID, now)
}

func (s reviewVersionStore) EnforceRevisionLimit(ctx context.Context, tx *gorm.DB, skillID string, protected map[string]bool) error {
	return nil
}

func (s reviewVersionStore) AfterCommit(ctx context.Context, tx *gorm.DB, revision versionfs.RevisionRecord, entries map[string]versionfs.Entry) error {
	return skillsearch.RebuildSkillTx(ctx, tx, revision.ResourceID, revision.CreatedAt)
}

func (s reviewVersionStore) AfterRollback(ctx context.Context, tx *gorm.DB, revision versionfs.RevisionRecord, entries map[string]versionfs.Entry) error {
	return skillsearch.RebuildSkillTx(ctx, tx, revision.ResourceID, revision.CreatedAt)
}

func (s reviewVersionStore) ListBlobHashes(ctx context.Context, tx *gorm.DB) ([]string, error) {
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

func (s reviewVersionStore) BlobReferenced(ctx context.Context, tx *gorm.DB, hash string) (bool, error) {
	return blobReferenced(tx, hash)
}

func (s reviewVersionStore) DeleteBlob(ctx context.Context, tx *gorm.DB, hash string) error {
	if s.service == nil || s.service.blobStore == nil {
		return nil
	}
	return s.service.blobStore.DeleteBlob(ctx, tx, hash)
}

func (s *Service) ensureSession(ctx context.Context, db *gorm.DB, skillID, userID string) (reviewSessionRow, error) {
	baseRevisionID, draftVersion, snapshot, err := draftReviewSnapshot(ctx, db, skillID)
	if err != nil {
		return reviewSessionRow{}, err
	}
	var session reviewSessionRow
	err = db.WithContext(ctx).Where("skill_id = ? AND status = ?", skillID, statusActive).Order("updated_at DESC").Take(&session).Error
	now := s.clock.Now()
	if err == nil {
		if session.BaseRevisionID == baseRevisionID && session.DraftVersionAtStart == draftVersion && session.DraftSnapshotHash == snapshot {
			return session, nil
		}
		if err := db.WithContext(ctx).Model(&reviewSessionRow{}).Where("id = ?", session.ID).Updates(map[string]any{
			"status":     statusInvalidated,
			"updated_by": nullableString(userID),
			"updated_at": now,
		}).Error; err != nil {
			return reviewSessionRow{}, err
		}
	} else if err != gorm.ErrRecordNotFound {
		return reviewSessionRow{}, err
	}
	session = reviewSessionRow{
		ID:                  uuid.NewString(),
		SkillID:             skillID,
		BaseRevisionID:      baseRevisionID,
		DraftVersionAtStart: draftVersion,
		DraftSnapshotHash:   snapshot,
		Status:              statusActive,
		Version:             1,
		UndoLimit:           s.undoLimit,
		CreatedBy:           nullableString(userID),
		UpdatedBy:           nullableString(userID),
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := db.WithContext(ctx).Create(&session).Error; err != nil {
		return reviewSessionRow{}, err
	}
	return session, nil
}

func (s *Service) loadActiveSession(ctx context.Context, tx *gorm.DB, skillID, reviewID string) (reviewSessionRow, error) {
	var session reviewSessionRow
	if err := tx.WithContext(ctx).Where("id = ? AND skill_id = ? AND status = ?", reviewID, skillID, statusActive).Take(&session).Error; err != nil {
		return reviewSessionRow{}, err
	}
	return session, nil
}

func (s *Service) validateSnapshot(ctx context.Context, tx *gorm.DB, session reviewSessionRow) error {
	baseRevisionID, draftVersion, snapshot, err := draftReviewSnapshot(ctx, tx, session.SkillID)
	if err != nil {
		return err
	}
	if baseRevisionID != session.BaseRevisionID || draftVersion != session.DraftVersionAtStart || snapshot != session.DraftSnapshotHash {
		return fmt.Errorf("draft snapshot changed")
	}
	return nil
}

func (s *Service) reviewedEntries(ctx context.Context, tx *gorm.DB, session reviewSessionRow, baseEntries, draftEntries map[string]mergedEntry, decisions map[string]string) (map[string]mergedEntry, error) {
	finalEntries := cloneEntries(baseEntries)
	paths := unionEntryPaths(baseEntries, draftEntries)
	for _, path := range paths {
		baseEntry, baseOK := baseEntries[path]
		draftEntry, draftOK := draftEntries[path]
		if baseOK && draftOK && entrySignature(baseEntry) == entrySignature(draftEntry) {
			finalEntries[path] = draftEntry
			continue
		}
		if (baseOK && baseEntry.EntryType == "dir") || (draftOK && draftEntry.EntryType == "dir") {
			if draftOK {
				finalEntries[path] = draftEntry
			} else {
				delete(finalEntries, path)
			}
			continue
		}
		file, err := s.diffFileForPath(ctx, tx, session, path)
		if err != nil {
			return nil, err
		}
		hunks := versionfs.HunkLines(reviewFileFromSkill(file))
		if len(hunks) == 0 {
			continue
		}
		fileDecision := ""
		for _, hunk := range hunks {
			decision := decisions[versionfs.DecisionKey(path, hunk.HunkID)]
			if decision == "" || decision == decisionPending {
				return nil, fmt.Errorf("pending hunks exist")
			}
			if fileDecision == "" {
				fileDecision = decision
			} else if fileDecision != decision {
				merged, err := s.mergeTextFile(ctx, tx, path, file, decisions)
				if err != nil {
					return nil, err
				}
				finalEntries[path] = merged
				fileDecision = ""
				break
			}
		}
		switch fileDecision {
		case decisionAccepted:
			if draftOK {
				finalEntries[path] = draftEntry
			} else {
				delete(finalEntries, path)
			}
		case decisionRejected:
			if baseOK {
				finalEntries[path] = baseEntry
			} else {
				delete(finalEntries, path)
			}
		case "":
		default:
			return nil, fmt.Errorf("invalid decision")
		}
	}
	return finalEntries, nil
}

func (s *Service) mergeTextFile(ctx context.Context, tx *gorm.DB, path string, file skilldiff.DiffFile, decisions map[string]string) (mergedEntry, error) {
	content, err := versionfs.MergeTextFile(reviewFileFromSkill(file), decisions)
	if err != nil {
		return mergedEntry{}, err
	}
	blob, err := s.blobStore.Put(ctx, tx, path, []byte(content), s.clock)
	if err != nil {
		return mergedEntry{}, err
	}
	hash := blob.Hash
	return mergedEntry{Path: path, EntryType: "file", BlobHash: &hash, Size: blob.Size, Mime: blob.Mime, FileType: blob.FileType, Binary: blob.Binary, Mode: 0o644}, nil
}

func (s *Service) hunksByPath(ctx context.Context, tx *gorm.DB, session reviewSessionRow, paths []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	for _, path := range paths {
		file, err := s.diffFileForPath(ctx, tx, session, path)
		if err != nil {
			return nil, err
		}
		out[path] = map[string]bool{}
		for _, hunk := range versionfs.HunkLines(reviewFileFromSkill(file)) {
			out[path][hunk.HunkID] = true
		}
	}
	return out, nil
}

func (s *Service) diffFileForPath(ctx context.Context, tx *gorm.DB, session reviewSessionRow, path string) (skilldiff.DiffFile, error) {
	baseEntries, err := entriesForRevision(ctx, tx, session.SkillID, session.BaseRevisionID)
	if err != nil {
		return skilldiff.DiffFile{}, err
	}
	draftEntries, err := mergedEntriesForDraft(ctx, tx, session.SkillID, session.BaseRevisionID)
	if err != nil {
		return skilldiff.DiffFile{}, err
	}
	oldFS := dbReadOnlyFS{db: tx, entries: baseEntries}
	newFS := dbReadOnlyFS{db: tx, entries: draftEntries}
	file, err := skilldiff.NewService(skilldiff.ServiceDeps{}).CompareFile(ctx, oldFS, newFS, skilldiff.DiffOptions{Path: path})
	if err != nil {
		return skilldiff.DiffFile{}, err
	}
	s.annotateFile(&file, session, map[string]string{}, false)
	return file, nil
}

func (s *Service) annotateFile(file *skilldiff.DiffFile, session reviewSessionRow, decisions map[string]string, canUndo bool) {
	reviewFile := reviewFileFromSkill(*file)
	versionfs.AnnotateReviewFile(&reviewFile, versionfs.ReviewSessionMeta{
		ID:                session.ID,
		Path:              file.Path,
		Status:            file.Status,
		Version:           session.Version,
		DraftVersion:      session.DraftVersionAtStart,
		BaseRevisionID:    session.BaseRevisionID,
		DraftSnapshotHash: session.DraftSnapshotHash,
	}, decisions, canUndo)
	applyReviewFileToSkill(reviewFile, file)
}

func reviewDisplayDiffEntryLines(lines []skilldiff.DiffEntryLine) []skilldiff.DiffEntryLine {
	out := make([]skilldiff.DiffEntryLine, 0, len(lines))
	for i := 0; i < len(lines); {
		line := lines[i]
		if line.Type != "HUNK" {
			out = append(out, line)
			i++
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if lines[j].Type == "HUNK" {
				end = j
				break
			}
		}
		switch strings.TrimSpace(line.Decision) {
		case decisionAccepted:
			out = append(out, line)
			out = append(out, resolvedHunkLines(lines[i+1:end], decisionAccepted)...)
		case decisionRejected:
			out = append(out, line)
			out = append(out, resolvedHunkLines(lines[i+1:end], decisionRejected)...)
		default:
			out = append(out, lines[i:end]...)
		}
		i = end
	}
	return out
}

func resolvedHunkLines(lines []skilldiff.DiffEntryLine, decision string) []skilldiff.DiffEntryLine {
	out := make([]skilldiff.DiffEntryLine, 0, len(lines))
	for _, line := range lines {
		switch line.Type {
		case "CONTEXT":
			out = append(out, line)
		case "ADDITION":
			if decision == decisionAccepted {
				out = append(out, resolvedContextLine(line, line.NewLine))
			}
		case "DELETION":
			if decision == decisionRejected {
				out = append(out, resolvedContextLine(line, line.OldLine))
			}
		default:
			out = append(out, line)
		}
	}
	return out
}

func resolvedContextLine(line skilldiff.DiffEntryLine, lineNo int) skilldiff.DiffEntryLine {
	return skilldiff.DiffEntryLine{
		Type:                    "CONTEXT",
		Text:                    line.Text,
		HTML:                    html.EscapeString(line.Text),
		OldLine:                 lineNo,
		NewLine:                 lineNo,
		DisplayNoNewLineWarning: line.DisplayNoNewLineWarning,
	}
}

func draftReviewSnapshot(ctx context.Context, db *gorm.DB, skillID string) (string, int64, string, error) {
	var draft skillDraftRow
	if err := db.WithContext(ctx).Where("skill_id = ?", skillID).Take(&draft).Error; err != nil {
		return "", 0, "", err
	}
	var overlays []skillDraftEntryRow
	if err := db.WithContext(ctx).Where("skill_id = ?", skillID).Order("path ASC").Find(&overlays).Error; err != nil {
		return "", 0, "", err
	}
	if len(overlays) == 0 {
		return "", 0, "", fmt.Errorf("draft overlay is empty")
	}
	baseRevisionID := ""
	if draft.BaseRevisionID != nil {
		baseRevisionID = *draft.BaseRevisionID
	}
	if baseRevisionID == "" {
		var skill skillRow
		if err := db.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
			return "", 0, "", err
		}
		if skill.HeadRevisionID == nil || *skill.HeadRevisionID == "" {
			return "", 0, "", fmt.Errorf("skill has no base revision")
		}
		baseRevisionID = *skill.HeadRevisionID
	}
	lines := make([]string, 0, len(overlays))
	for _, overlay := range overlays {
		hash := ""
		if overlay.BlobHash != nil {
			hash = *overlay.BlobHash
		}
		lines = append(lines, strings.Join([]string{
			overlay.Path,
			overlay.Op,
			overlay.EntryType,
			hash,
			strconv.FormatInt(overlay.Size, 10),
			overlay.FileType,
			strconv.FormatBool(overlay.Binary),
		}, "\x00"))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return baseRevisionID, draft.Version, hex.EncodeToString(sum[:]), nil
}

func (s *Service) currentDecisions(ctx context.Context, db *gorm.DB, reviewID string) (map[string]string, error) {
	var batches []reviewActionBatchRow
	if err := db.WithContext(ctx).Where("review_session_id = ? AND undone_at IS NULL", reviewID).Order("sequence ASC").Find(&batches).Error; err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, batch := range batches {
		var items []reviewActionItemRow
		if err := db.WithContext(ctx).Where("batch_id = ?", batch.ID).Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
			return nil, err
		}
		for _, item := range items {
			out[versionfs.DecisionKey(item.Path, item.HunkID)] = item.AfterDecision
		}
	}
	return out, nil
}

func (s *Service) canUndo(ctx context.Context, db *gorm.DB, reviewID string) (bool, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&reviewActionBatchRow{}).Where("review_session_id = ? AND undone_at IS NULL AND undo_locked = ?", reviewID, false).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *Service) lockOldUndoBatches(tx *gorm.DB, reviewID string, latestSeq int64) error {
	limit := s.undoLimit
	if limit <= 0 {
		return nil
	}
	cutoff := latestSeq - int64(limit)
	if cutoff <= 0 {
		return nil
	}
	return tx.Model(&reviewActionBatchRow{}).Where("review_session_id = ? AND sequence <= ?", reviewID, cutoff).Update("undo_locked", true).Error
}

type dbReadOnlyFS struct {
	db      *gorm.DB
	entries map[string]mergedEntry
}

func (fs dbReadOnlyFS) ListAll(ctx context.Context) ([]skilldiff.EntryInfo, error) {
	paths := make([]string, 0, len(fs.entries))
	for path := range fs.entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]skilldiff.EntryInfo, 0, len(paths))
	for _, path := range paths {
		entry := fs.entries[path]
		hash := ""
		if entry.BlobHash != nil {
			hash = *entry.BlobHash
		}
		out = append(out, skilldiff.EntryInfo{Path: path, Type: entry.EntryType, BlobHash: hash, Binary: entry.Binary, FileType: entry.FileType, Size: entry.Size})
	}
	return out, nil
}

func (fs dbReadOnlyFS) ReadFile(ctx context.Context, path string) ([]byte, error) {
	entry, ok := fs.entries[path]
	if !ok || entry.EntryType != "file" || entry.BlobHash == nil {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	var blob skillBlobRow
	if err := fs.db.WithContext(ctx).Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		return nil, err
	}
	if blob.Binary {
		return nil, fmt.Errorf("binary content is not available: %s", path)
	}
	return blob.Content, nil
}

func entriesForRevision(ctx context.Context, db *gorm.DB, skillID, revisionID string) (map[string]mergedEntry, error) {
	var rev skillRevisionRow
	if err := db.WithContext(ctx).Where("id = ? AND skill_id = ?", revisionID, skillID).Take(&rev).Error; err != nil {
		return nil, err
	}
	var rows []skillRevisionEntryRow
	if err := db.WithContext(ctx).Where("revision_id = ?", revisionID).Order("path ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]mergedEntry, len(rows))
	for _, row := range rows {
		hash := row.BlobHash
		out[row.Path] = mergedEntry{Path: row.Path, EntryType: row.EntryType, BlobHash: hash, Size: row.Size, Mime: row.Mime, FileType: row.FileType, Binary: row.Binary, Mode: row.Mode}
	}
	return out, nil
}

func mergedEntriesForDraft(ctx context.Context, db *gorm.DB, skillID, baseRevisionID string) (map[string]mergedEntry, error) {
	entries, err := entriesForRevision(ctx, db, skillID, baseRevisionID)
	if err != nil {
		return nil, err
	}
	var overlays []skillDraftEntryRow
	if err := db.WithContext(ctx).Where("skill_id = ?", skillID).Order("path ASC").Find(&overlays).Error; err != nil {
		return nil, err
	}
	return fromVersionEntries(versionfs.MergeEntries(toVersionEntries(entries), toVersionOverlays(overlays))), nil
}

func createRevisionEntries(tx *gorm.DB, revisionID string, entries map[string]mergedEntry) error {
	rows := make([]skillRevisionEntryRow, 0, len(entries))
	for _, entry := range sortedEntries(entries) {
		hash := entry.BlobHash
		rows = append(rows, skillRevisionEntryRow{
			RevisionID: revisionID,
			Path:       entry.Path,
			EntryType:  entry.EntryType,
			BlobHash:   hash,
			Size:       entry.Size,
			Mime:       entry.Mime,
			FileType:   entry.FileType,
			Binary:     entry.Binary,
			Mode:       entry.Mode,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

func blobReferenced(tx *gorm.DB, hash string) (bool, error) {
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

func markSkillReviews(ctx context.Context, tx *gorm.DB, skillID, status, userID string, now time.Time) error {
	updates := map[string]any{
		"status":     status,
		"updated_by": nullableString(userID),
		"updated_at": now,
	}
	return tx.WithContext(ctx).Model(&reviewSessionRow{}).Where("skill_id = ? AND status = ?", skillID, statusActive).Updates(updates).Error
}

func deleteReviewSession(ctx context.Context, tx *gorm.DB, reviewID string) error {
	if err := tx.WithContext(ctx).Where("review_session_id = ?", reviewID).Delete(&reviewActionItemRow{}).Error; err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Where("review_session_id = ?", reviewID).Delete(&reviewActionBatchRow{}).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Where("id = ?", reviewID).Delete(&reviewSessionRow{}).Error
}

func nextBatchSequence(tx *gorm.DB, reviewID string) (int64, error) {
	var maxSeq int64
	if err := tx.Model(&reviewActionBatchRow{}).Where("review_session_id = ?", reviewID).Select("COALESCE(MAX(sequence), 0)").Scan(&maxSeq).Error; err != nil {
		return 0, err
	}
	return maxSeq + 1, nil
}

func nextRevisionNo(tx *gorm.DB, skillID string) (int64, error) {
	var maxNo int64
	if err := tx.Model(&skillRevisionRow{}).Where("skill_id = ?", skillID).Select("COALESCE(MAX(revision_no), 0)").Scan(&maxNo).Error; err != nil {
		return 0, err
	}
	return maxNo + 1, nil
}

func sortedEntries(entries map[string]mergedEntry) []mergedEntry {
	return fromVersionEntrySlice(versionfs.SortedEntries(toVersionEntries(entries)))
}

func cloneEntries(entries map[string]mergedEntry) map[string]mergedEntry {
	return fromVersionEntries(versionfs.CloneEntries(toVersionEntries(entries)))
}

func unionEntryPaths(a, b map[string]mergedEntry) []string {
	return versionfs.UnionEntryPaths(toVersionEntries(a), toVersionEntries(b))
}

func uniquePaths(items []ActionItem) []string {
	seen := map[string]bool{}
	for _, item := range items {
		path := strings.TrimSpace(item.Path)
		if path != "" {
			seen[path] = true
		}
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func entrySignature(entry mergedEntry) string {
	return versionfs.EntrySignature(toVersionEntry(entry))
}

func nullableString(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func reviewFileFromSkill(file skilldiff.DiffFile) versionfs.ReviewFile {
	lines := make([]versionfs.DiffLine, 0, len(file.DiffEntryLines))
	for _, line := range file.DiffEntryLines {
		lines = append(lines, versionfs.DiffLine{
			Type:                    line.Type,
			Text:                    line.Text,
			HTML:                    line.HTML,
			OldLine:                 line.OldLine,
			NewLine:                 line.NewLine,
			DisplayNoNewLineWarning: line.DisplayNoNewLineWarning,
			HunkID:                  line.HunkID,
			Decision:                line.Decision,
			OldStart:                line.OldStart,
			OldLines:                line.OldLines,
			NewStart:                line.NewStart,
			NewLines:                line.NewLines,
		})
	}
	return versionfs.ReviewFile{
		Path:              file.Path,
		Type:              file.Type,
		Status:            file.Status,
		Binary:            file.Binary,
		TooLarge:          file.TooLarge,
		ReviewID:          file.ReviewID,
		ReviewVersion:     file.ReviewVersion,
		DraftVersion:      file.DraftVersion,
		BaseRevisionID:    file.BaseRevisionID,
		DraftSnapshotHash: file.DraftSnapshotHash,
		CanUndo:           file.CanUndo,
		HunkCount:         file.HunkCount,
		PendingCount:      file.PendingCount,
		AcceptedCount:     file.AcceptedCount,
		RejectedCount:     file.RejectedCount,
		DiffLines:         lines,
	}
}

func applyReviewFileToSkill(src versionfs.ReviewFile, dst *skilldiff.DiffFile) {
	lines := make([]skilldiff.DiffEntryLine, 0, len(src.DiffLines))
	for _, line := range src.DiffLines {
		lines = append(lines, skilldiff.DiffEntryLine{
			Type:                    line.Type,
			Text:                    line.Text,
			HTML:                    line.HTML,
			OldLine:                 line.OldLine,
			NewLine:                 line.NewLine,
			DisplayNoNewLineWarning: line.DisplayNoNewLineWarning,
			HunkID:                  line.HunkID,
			Decision:                line.Decision,
			OldStart:                line.OldStart,
			OldLines:                line.OldLines,
			NewStart:                line.NewStart,
			NewLines:                line.NewLines,
		})
	}
	dst.ReviewID = src.ReviewID
	dst.ReviewVersion = src.ReviewVersion
	dst.DraftVersion = src.DraftVersion
	dst.BaseRevisionID = src.BaseRevisionID
	dst.DraftSnapshotHash = src.DraftSnapshotHash
	dst.CanUndo = src.CanUndo
	dst.HunkCount = src.HunkCount
	dst.PendingCount = src.PendingCount
	dst.AcceptedCount = src.AcceptedCount
	dst.RejectedCount = src.RejectedCount
	dst.DiffEntryLines = lines
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

type clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type reviewSessionRow struct {
	ID                  string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID             string    `gorm:"column:skill_id;type:varchar(36);not null"`
	BaseRevisionID      string    `gorm:"column:base_revision_id;type:varchar(36);not null"`
	DraftVersionAtStart int64     `gorm:"column:draft_version_at_start;not null"`
	DraftSnapshotHash   string    `gorm:"column:draft_snapshot_hash;type:text;not null"`
	Status              string    `gorm:"column:status;type:text;not null;default:'active'"`
	Version             int64     `gorm:"column:version;not null;default:1"`
	UndoLimit           int       `gorm:"column:undo_limit;not null;default:20"`
	CreatedBy           *string   `gorm:"column:created_by;type:text"`
	UpdatedBy           *string   `gorm:"column:updated_by;type:text"`
	CreatedAt           time.Time `gorm:"column:created_at;not null"`
	UpdatedAt           time.Time `gorm:"column:updated_at;not null"`
}

func (reviewSessionRow) TableName() string { return "skill_draft_review_sessions" }

type reviewActionBatchRow struct {
	ID              string     `gorm:"column:id;type:varchar(36);primaryKey"`
	ReviewSessionID string     `gorm:"column:review_session_id;type:varchar(36);not null"`
	Sequence        int64      `gorm:"column:sequence;not null"`
	UndoLocked      bool       `gorm:"column:undo_locked;not null;default:false"`
	UndoneAt        *time.Time `gorm:"column:undone_at"`
	UndoneBy        *string    `gorm:"column:undone_by;type:text"`
	CreatedBy       *string    `gorm:"column:created_by;type:text"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null"`
}

func (reviewActionBatchRow) TableName() string {
	return "skill_draft_review_action_batches"
}

type reviewActionItemRow struct {
	ID              string    `gorm:"column:id;type:varchar(36);primaryKey"`
	BatchID         string    `gorm:"column:batch_id;type:varchar(36);not null"`
	ReviewSessionID string    `gorm:"column:review_session_id;type:varchar(36);not null"`
	Path            string    `gorm:"column:path;type:text;not null"`
	HunkID          string    `gorm:"column:hunk_id;type:text;not null"`
	BeforeDecision  string    `gorm:"column:before_decision;type:text;not null;default:'pending'"`
	AfterDecision   string    `gorm:"column:after_decision;type:text;not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null"`
}

func (reviewActionItemRow) TableName() string {
	return "skill_draft_review_action_items"
}

type skillRow struct {
	ID             string    `gorm:"column:id;type:varchar(36);primaryKey"`
	HeadRevisionID *string   `gorm:"column:head_revision_id;type:varchar(36)"`
	Version        int64     `gorm:"column:version"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (skillRow) TableName() string { return "skills" }

type skillBlobRow struct {
	Hash    string `gorm:"column:hash;type:text;primaryKey"`
	Binary  bool   `gorm:"column:binary"`
	Content []byte `gorm:"column:content;type:blob"`
}

func (skillBlobRow) TableName() string { return "skill_blobs" }

type skillRevisionRow struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID          string    `gorm:"column:skill_id;type:varchar(36);not null"`
	ParentRevisionID *string   `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo       int64     `gorm:"column:revision_no;not null"`
	TreeHash         string    `gorm:"column:tree_hash;type:text;not null"`
	ChangeSource     string    `gorm:"column:change_source;type:text;not null;default:'draft_commit'"`
	CreatedBy        *string   `gorm:"column:created_by;type:text"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (skillRevisionRow) TableName() string { return "skill_revisions" }

type skillRevisionEntryRow struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:text;primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:text;not null"`
	BlobHash   *string `gorm:"column:blob_hash;type:text"`
	Size       int64   `gorm:"column:size"`
	Mime       string  `gorm:"column:mime;type:text"`
	FileType   string  `gorm:"column:file_type;type:text"`
	Binary     bool    `gorm:"column:binary"`
	Mode       int     `gorm:"column:mode"`
}

func (skillRevisionEntryRow) TableName() string { return "skill_revision_entries" }

type skillDraftRow struct {
	SkillID        string     `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	TaskID         string     `gorm:"column:task_id;type:text"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(36)"`
	UpdatedBy      *string    `gorm:"column:updated_by;type:text"`
	Version        int64      `gorm:"column:version"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
}

func (skillDraftRow) TableName() string { return "skill_drafts" }

type skillDraftEntryRow struct {
	SkillID   string  `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	Path      string  `gorm:"column:path;type:text;primaryKey"`
	Op        string  `gorm:"column:op;type:text;not null"`
	EntryType string  `gorm:"column:entry_type;type:text"`
	BlobHash  *string `gorm:"column:blob_hash;type:text"`
	Size      int64   `gorm:"column:size"`
	Mime      string  `gorm:"column:mime;type:text"`
	FileType  string  `gorm:"column:file_type;type:text"`
	Binary    bool    `gorm:"column:binary"`
	Mode      int     `gorm:"column:mode"`
}

func (skillDraftEntryRow) TableName() string { return "skill_draft_entries" }

type mergedEntry struct {
	Path      string
	EntryType string
	BlobHash  *string
	Size      int64
	Mime      string
	FileType  string
	Binary    bool
	Mode      int
}
