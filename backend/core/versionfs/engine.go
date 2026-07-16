package versionfs

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Clock interface {
	Now() time.Time
}

type Engine struct {
	db    *gorm.DB
	store Store
	clock Clock
}

type Store interface {
	LoadHead(ctx context.Context, tx *gorm.DB, resourceID string) (HeadState, error)
	LoadDraft(ctx context.Context, tx *gorm.DB, resourceID string) (DraftState, error)
	HasDraftChanges(ctx context.Context, tx *gorm.DB, resourceID string, draft DraftState) (bool, error)
	ClaimDraft(ctx context.Context, tx *gorm.DB, resourceID string, draft DraftState, userID string, now time.Time) (DraftState, error)
	DraftEntries(ctx context.Context, tx *gorm.DB, resourceID string, baseRevisionID string) (map[string]Entry, error)
	RevisionEntries(ctx context.Context, tx *gorm.DB, resourceID string, revisionID string) (map[string]Entry, error)
	EnsureBlobs(ctx context.Context, tx *gorm.DB, entries map[string]Entry) error
	NextRevisionNo(ctx context.Context, tx *gorm.DB, resourceID string) (int64, error)
	CreateRevision(ctx context.Context, tx *gorm.DB, revision RevisionRecord, entries map[string]Entry) error
	UpdateHead(ctx context.Context, tx *gorm.DB, resourceID string, previousRevisionID string, revisionID string, now time.Time) error
	ResetDraftAfterCommit(ctx context.Context, tx *gorm.DB, resourceID string, revisionID string, draft DraftState, userID string, now time.Time) error
	ResetDraftAfterRollback(ctx context.Context, tx *gorm.DB, resourceID string, revisionID string, targetEntries map[string]Entry, draft DraftState, userID string, now time.Time) error
	MarkActiveReviews(ctx context.Context, tx *gorm.DB, resourceID string, status string, userID string, now time.Time) error
	EnforceRevisionLimit(ctx context.Context, tx *gorm.DB, resourceID string, protected map[string]bool) error
	AfterCommit(ctx context.Context, tx *gorm.DB, revision RevisionRecord, entries map[string]Entry) error
	AfterRollback(ctx context.Context, tx *gorm.DB, resourceID string, revisionID string, entries map[string]Entry, now time.Time) error
	ListBlobHashes(ctx context.Context, tx *gorm.DB) ([]string, error)
	BlobReferenced(ctx context.Context, tx *gorm.DB, hash string) (bool, error)
	DeleteBlob(ctx context.Context, tx *gorm.DB, hash string) error
}

type initialCommitStore interface {
	AllowsInitialCommit() bool
}

type HeadState struct {
	RevisionID string
}

type DraftState struct {
	BaseRevisionID string
	Version        int64
	BlobHash       string
	Status         string
}

type RevisionRecord struct {
	ID               string
	ResourceID       string
	ParentRevisionID string
	RevisionNo       int64
	TreeHash         string
	Message          string
	ChangeSource     string
	SourceRefType    string
	SourceRefID      string
	CreatedBy        string
	CreatedAt        time.Time
}

type CommitDraftRequest struct {
	ResourceID             string
	UserID                 string
	ExpectedHeadRevisionID string
	ExpectedDraftVersion   int64
	Message                string
	ChangeSource           string
	SourceRefType          string
	SourceRefID            string
}

type CommitDraftResponse struct {
	RevisionID string
	RevisionNo int64
}

type CommitEntriesRequest struct {
	ResourceID             string
	UserID                 string
	ParentRevisionID       string
	ExpectedHeadRevisionID string
	ExpectedDraftVersion   int64
	Message                string
	ChangeSource           string
	SourceRefType          string
	SourceRefID            string
	Entries                map[string]Entry
}

type CommitEntriesResponse struct {
	RevisionID string
	RevisionNo int64
}

type RollbackRequest struct {
	ResourceID             string
	UserID                 string
	TargetRevisionID       string
	ExpectedHeadRevisionID string
	Message                string
	RequireNoDraft         bool
}

type RollbackResponse struct {
	RevisionID string
}

type EngineDeps struct {
	DB    *gorm.DB
	Store Store
	Clock Clock
}

func NewEngine(deps EngineDeps) *Engine {
	clock := deps.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &Engine{db: deps.DB, store: deps.Store, clock: clock}
}

func (e *Engine) CommitDraft(ctx context.Context, req CommitDraftRequest) (CommitDraftResponse, error) {
	var out CommitDraftResponse
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		head, err := e.store.LoadHead(ctx, tx, req.ResourceID)
		if err != nil {
			return err
		}
		if req.ExpectedHeadRevisionID != "" && head.RevisionID != req.ExpectedHeadRevisionID {
			return ErrHeadRevisionConflict
		}
		draft, err := e.store.LoadDraft(ctx, tx, req.ResourceID)
		if err != nil {
			return err
		}
		if req.ExpectedDraftVersion > 0 && draft.Version != req.ExpectedDraftVersion {
			return ErrStaleDraftVersion
		}
		changed, err := e.store.HasDraftChanges(ctx, tx, req.ResourceID, draft)
		if err != nil {
			return err
		}
		if !changed {
			return ErrDraftEmpty
		}
		now := e.clock.Now()
		draft, err = e.store.ClaimDraft(ctx, tx, req.ResourceID, draft, req.UserID, now)
		if err != nil {
			return err
		}
		baseRevisionID := draft.BaseRevisionID
		if baseRevisionID == "" {
			baseRevisionID = head.RevisionID
		}
		if baseRevisionID == "" {
			store, ok := e.store.(initialCommitStore)
			if !ok || !store.AllowsInitialCommit() {
				return fmt.Errorf("resource has no base revision")
			}
		}
		if baseRevisionID != head.RevisionID {
			return ErrDraftBaseConflict
		}
		if baseRevisionID != head.RevisionID {
			return ErrDraftBaseConflict
		}
		entries, err := e.store.DraftEntries(ctx, tx, req.ResourceID, baseRevisionID)
		if err != nil {
			return err
		}
		if err := e.store.EnsureBlobs(ctx, tx, entries); err != nil {
			return err
		}
		nextNo, err := e.store.NextRevisionNo(ctx, tx, req.ResourceID)
		if err != nil {
			return err
		}
		revisionID := uuid.NewString()
		changeSource := req.ChangeSource
		if changeSource == "" {
			changeSource = "draft_commit"
		}
		revision := RevisionRecord{
			ID:               revisionID,
			ResourceID:       req.ResourceID,
			ParentRevisionID: baseRevisionID,
			RevisionNo:       nextNo,
			TreeHash:         HashTree(entries),
			Message:          req.Message,
			ChangeSource:     changeSource,
			SourceRefType:    req.SourceRefType,
			SourceRefID:      req.SourceRefID,
			CreatedBy:        req.UserID,
			CreatedAt:        now,
		}
		if err := e.store.CreateRevision(ctx, tx, revision, entries); err != nil {
			return err
		}
		if err := e.store.UpdateHead(ctx, tx, req.ResourceID, head.RevisionID, revisionID, now); err != nil {
			return err
		}
		if err := e.store.ResetDraftAfterCommit(ctx, tx, req.ResourceID, revisionID, draft, req.UserID, now); err != nil {
			return err
		}
		if err := e.store.MarkActiveReviews(ctx, tx, req.ResourceID, "committed", req.UserID, now); err != nil {
			return err
		}
		if err := e.store.EnforceRevisionLimit(ctx, tx, req.ResourceID, protectedIDs(revisionID)); err != nil {
			return err
		}
		if err := e.store.AfterCommit(ctx, tx, revision, entries); err != nil {
			return err
		}
		if err := e.CleanupUnreferencedBlobsTx(ctx, tx); err != nil {
			return err
		}
		out = CommitDraftResponse{RevisionID: revisionID, RevisionNo: nextNo}
		return nil
	})
	return out, err
}

func (e *Engine) CommitEntries(ctx context.Context, req CommitEntriesRequest) (CommitEntriesResponse, error) {
	var out CommitEntriesResponse
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resp, err := e.CommitEntriesTx(ctx, tx, req)
		if err != nil {
			return err
		}
		out = resp
		return nil
	})
	return out, err
}

func (e *Engine) CommitEntriesTx(ctx context.Context, tx *gorm.DB, req CommitEntriesRequest) (CommitEntriesResponse, error) {
	head, err := e.store.LoadHead(ctx, tx, req.ResourceID)
	if err != nil {
		return CommitEntriesResponse{}, err
	}
	if req.ExpectedHeadRevisionID != "" && head.RevisionID != req.ExpectedHeadRevisionID {
		return CommitEntriesResponse{}, ErrHeadRevisionConflict
	}
	if head.RevisionID == "" {
		return CommitEntriesResponse{}, fmt.Errorf("resource has no head revision")
	}
	draft, err := e.store.LoadDraft(ctx, tx, req.ResourceID)
	if err != nil {
		return CommitEntriesResponse{}, err
	}
	if req.ExpectedDraftVersion > 0 && draft.Version != req.ExpectedDraftVersion {
		return CommitEntriesResponse{}, ErrStaleDraftVersion
	}
	now := e.clock.Now()
	draft, err = e.store.ClaimDraft(ctx, tx, req.ResourceID, draft, req.UserID, now)
	if err != nil {
		return CommitEntriesResponse{}, err
	}
	if len(req.Entries) == 0 {
		return CommitEntriesResponse{}, ErrDraftEmpty
	}
	if err := e.store.EnsureBlobs(ctx, tx, req.Entries); err != nil {
		return CommitEntriesResponse{}, err
	}
	nextNo, err := e.store.NextRevisionNo(ctx, tx, req.ResourceID)
	if err != nil {
		return CommitEntriesResponse{}, err
	}
	revisionID := uuid.NewString()
	parentRevisionID := req.ParentRevisionID
	if parentRevisionID == "" {
		parentRevisionID = head.RevisionID
	}
	if parentRevisionID != head.RevisionID {
		return CommitEntriesResponse{}, ErrDraftBaseConflict
	}
	changeSource := req.ChangeSource
	if changeSource == "" {
		changeSource = "draft_commit"
	}
	revision := RevisionRecord{
		ID:               revisionID,
		ResourceID:       req.ResourceID,
		ParentRevisionID: parentRevisionID,
		RevisionNo:       nextNo,
		TreeHash:         HashTree(req.Entries),
		Message:          req.Message,
		ChangeSource:     changeSource,
		SourceRefType:    req.SourceRefType,
		SourceRefID:      req.SourceRefID,
		CreatedBy:        req.UserID,
		CreatedAt:        now,
	}
	if err := e.store.CreateRevision(ctx, tx, revision, req.Entries); err != nil {
		return CommitEntriesResponse{}, err
	}
	if err := e.store.UpdateHead(ctx, tx, req.ResourceID, head.RevisionID, revisionID, now); err != nil {
		return CommitEntriesResponse{}, err
	}
	if err := e.store.ResetDraftAfterCommit(ctx, tx, req.ResourceID, revisionID, draft, req.UserID, now); err != nil {
		return CommitEntriesResponse{}, err
	}
	if err := e.store.MarkActiveReviews(ctx, tx, req.ResourceID, "committed", req.UserID, now); err != nil {
		return CommitEntriesResponse{}, err
	}
	if err := e.store.EnforceRevisionLimit(ctx, tx, req.ResourceID, protectedIDs(revisionID)); err != nil {
		return CommitEntriesResponse{}, err
	}
	if err := e.store.AfterCommit(ctx, tx, revision, req.Entries); err != nil {
		return CommitEntriesResponse{}, err
	}
	if err := e.CleanupUnreferencedBlobsTx(ctx, tx); err != nil {
		return CommitEntriesResponse{}, err
	}
	return CommitEntriesResponse{RevisionID: revisionID, RevisionNo: nextNo}, nil
}

func (e *Engine) Rollback(ctx context.Context, req RollbackRequest) (RollbackResponse, error) {
	var out RollbackResponse
	err := e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		head, err := e.store.LoadHead(ctx, tx, req.ResourceID)
		if err != nil {
			return err
		}
		if req.ExpectedHeadRevisionID != "" && head.RevisionID != req.ExpectedHeadRevisionID {
			return ErrHeadRevisionConflict
		}
		if head.RevisionID == "" {
			return fmt.Errorf("resource has no head revision")
		}
		entries, err := e.store.RevisionEntries(ctx, tx, req.ResourceID, req.TargetRevisionID)
		if err != nil {
			return err
		}
		if head.RevisionID == req.TargetRevisionID {
			out = RollbackResponse{RevisionID: req.TargetRevisionID}
			return nil
		}
		draft, err := e.store.LoadDraft(ctx, tx, req.ResourceID)
		if err != nil {
			return err
		}
		if req.RequireNoDraft {
			changed, err := e.store.HasDraftChanges(ctx, tx, req.ResourceID, draft)
			if err != nil {
				return err
			}
			if changed {
				return ErrDraftConflict
			}
		}
		now := e.clock.Now()
		draft, err = e.store.ClaimDraft(ctx, tx, req.ResourceID, draft, req.UserID, now)
		if err != nil {
			return err
		}
		if err := e.store.EnsureBlobs(ctx, tx, entries); err != nil {
			return err
		}
		if err := e.store.UpdateHead(ctx, tx, req.ResourceID, head.RevisionID, req.TargetRevisionID, now); err != nil {
			return err
		}
		if err := e.store.ResetDraftAfterRollback(ctx, tx, req.ResourceID, req.TargetRevisionID, entries, draft, req.UserID, now); err != nil {
			return err
		}
		if err := e.store.MarkActiveReviews(ctx, tx, req.ResourceID, "invalidated", req.UserID, now); err != nil {
			return err
		}
		if err := e.store.AfterRollback(ctx, tx, req.ResourceID, req.TargetRevisionID, entries, now); err != nil {
			return err
		}
		out = RollbackResponse{RevisionID: req.TargetRevisionID}
		return nil
	})
	return out, err
}

func (e *Engine) CleanupUnreferencedBlobs(ctx context.Context) error {
	return e.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return e.CleanupUnreferencedBlobsTx(ctx, tx)
	})
}

func (e *Engine) CleanupUnreferencedBlobsTx(ctx context.Context, tx *gorm.DB) error {
	hashes, err := e.store.ListBlobHashes(ctx, tx)
	if err != nil {
		return err
	}
	for _, hash := range hashes {
		referenced, err := e.store.BlobReferenced(ctx, tx, hash)
		if err != nil {
			return err
		}
		if referenced {
			continue
		}
		if err := e.store.DeleteBlob(ctx, tx, hash); err != nil {
			return err
		}
	}
	return nil
}

func protectedIDs(values ...string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		if value != "" {
			out[value] = true
		}
	}
	return out
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
