package resourcefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/filediff"
	"lazymind/core/preferencefile"
	"lazymind/core/versionfs"
)

type ServiceDeps struct {
	DB *gorm.DB
}

type Service struct {
	db    *gorm.DB
	clock clock
}

const (
	reviewStatusActive      = "active"
	reviewStatusInvalidated = "invalidated"
	reviewStatusCommitted   = "committed"
	reviewStatusDiscarded   = "discarded"

	decisionPending  = "pending"
	decisionAccepted = "accepted"
	decisionRejected = "rejected"

	draftStatusPendingConfirm = "pending_confirm"
	draftStatusPending        = "pending"
	draftStatusAutoPending    = "auto_pending"
)

func NewService(deps ServiceDeps) *Service {
	return &Service{db: deps.DB, clock: systemClock{}}
}

func (s *Service) EnsureResource(ctx context.Context, ref ResourceRef, initialContent string) (ResourceState, error) {
	if err := validateRef(ref); err != nil {
		return ResourceState{}, err
	}
	if s.db == nil {
		return ResourceState{}, fmt.Errorf("db is not configured")
	}
	if state, err := s.loadState(ctx, s.db, ref); err == nil {
		return state, nil
	} else if !errors.Is(err, ErrResourceNotFound) {
		return ResourceState{}, err
	}

	var out ResourceState
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if state, err := s.loadState(ctx, tx, ref); err == nil {
			out = state
			return nil
		} else if !errors.Is(err, ErrResourceNotFound) {
			return err
		}
		now := s.clock.Now()
		path, err := FixedPath(ref.ResourceType)
		if err != nil {
			return err
		}
		blob, err := putBlob(ctx, tx, []byte(initialContent), now)
		if err != nil {
			return err
		}
		resourceID := uuid.NewString()
		revisionID := uuid.NewString()
		head := revisionID
		resource := orm.PersonalResource{
			ID:                 resourceID,
			UserID:             ref.UserID,
			ResourceType:       string(ref.ResourceType),
			HeadRevisionID:     &head,
			Version:            1,
			AutoEvo:            true,
			AutoEvoApplyStatus: "idle",
			UpdatedBy:          ref.UserID,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := tx.Create(&resource).Error; err != nil {
			return err
		}
		revision := orm.PersonalResourceRevision{
			ID:           revisionID,
			ResourceID:   resourceID,
			RevisionNo:   1,
			Path:         path,
			BlobHash:     blob.Hash,
			ContentHash:  blob.Hash,
			Size:         blob.Size,
			Mime:         blob.Mime,
			FileType:     blob.FileType,
			Binary:       blob.Binary,
			Message:      "initial import",
			ChangeSource: "initial_import",
			CreatedAt:    now,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		draft := orm.PersonalResourceDraft{
			ResourceID:     resourceID,
			BaseRevisionID: &head,
			Path:           path,
			BlobHash:       blob.Hash,
			ContentHash:    blob.Hash,
			Size:           blob.Size,
			Mime:           blob.Mime,
			FileType:       blob.FileType,
			Binary:         blob.Binary,
			DraftStatus:    "",
			Version:        1,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&draft).Error; err != nil {
			return err
		}
		out = ResourceState{
			ID:             resourceID,
			UserID:         ref.UserID,
			ResourceType:   ref.ResourceType,
			Path:           path,
			HeadRevisionID: revisionID,
			Version:        resource.Version,
			DraftVersion:   draft.Version,
			DraftStatus:    draft.DraftStatus,
		}
		return nil
	})
	if err != nil {
		if state, loadErr := s.loadState(ctx, s.db, ref); loadErr == nil {
			return state, nil
		}
		return ResourceState{}, err
	}
	return out, nil
}

func (s *Service) ReadFile(ctx context.Context, req ReadFileRequest) (FileResponse, error) {
	if err := validateRef(req.Ref); err != nil {
		return FileResponse{}, err
	}
	resource, err := findResource(ctx, s.db, req.Ref)
	if err != nil {
		return FileResponse{}, err
	}
	refType := req.RefType
	if refType == "" {
		refType = FileRefHead
	}
	switch refType {
	case FileRefHead:
		if resource.HeadRevisionID == nil {
			return FileResponse{}, ErrRevisionNotFound
		}
		revision, err := findRevisionByID(ctx, s.db, resource.ID, *resource.HeadRevisionID)
		if err != nil {
			return FileResponse{}, err
		}
		return s.fileResponseForRevision(ctx, req.Ref.ResourceType, revision)
	case FileRefRevision:
		if strings.TrimSpace(req.RevisionID) == "" {
			return FileResponse{}, ErrRevisionNotFound
		}
		revision, err := findRevisionByID(ctx, s.db, resource.ID, req.RevisionID)
		if err != nil {
			return FileResponse{}, err
		}
		return s.fileResponseForRevision(ctx, req.Ref.ResourceType, revision)
	case FileRefDraft:
		var draft orm.PersonalResourceDraft
		if err := s.db.WithContext(ctx).Where("resource_id = ?", resource.ID).Take(&draft).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return FileResponse{}, ErrDraftNotFound
			}
			return FileResponse{}, err
		}
		content, err := readBlobContent(ctx, s.db, draft.BlobHash)
		if err != nil {
			return FileResponse{}, err
		}
		return FileResponse{
			ResourceType: req.Ref.ResourceType,
			Path:         draft.Path,
			Content:      string(content),
			BlobHash:     draft.BlobHash,
			ContentHash:  draft.ContentHash,
			Size:         draft.Size,
			Mime:         draft.Mime,
			FileType:     draft.FileType,
			Binary:       draft.Binary,
			DraftVersion: draft.Version,
			DraftStatus:  draft.DraftStatus,
		}, nil
	default:
		return FileResponse{}, ErrInvalidResourceType
	}
}

func (s *Service) WriteDraft(ctx context.Context, req WriteDraftRequest) (DraftResponse, error) {
	if err := validateRef(req.Ref); err != nil {
		return DraftResponse{}, err
	}
	var out DraftResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resource, err := findResource(ctx, tx, req.Ref)
		if err != nil {
			return err
		}
		if resource.HeadRevisionID == nil {
			return ErrRevisionNotFound
		}
		var draft orm.PersonalResourceDraft
		if err := tx.Where("resource_id = ?", resource.ID).Take(&draft).Error; err != nil {
			return err
		}
		if req.ExpectedDraftVersion > 0 && draft.Version != req.ExpectedDraftVersion {
			return ErrConflict
		}
		now := s.clock.Now()
		blob, err := putBlob(ctx, tx, []byte(req.Content), now)
		if err != nil {
			return err
		}
		nextVersion := draft.Version + 1
		updatedBy := nullableString(req.UpdatedBy)
		conversationID := nullableString(req.ConversationID)
		update := map[string]any{
			"base_revision_id": resource.HeadRevisionID,
			"path":             mustPath(req.Ref.ResourceType),
			"blob_hash":        blob.Hash,
			"content_hash":     blob.Hash,
			"size":             blob.Size,
			"mime":             blob.Mime,
			"file_type":        blob.FileType,
			"binary":           blob.Binary,
			"draft_status":     "pending_confirm",
			"draft_updated_at": now,
			"task_id":          strings.TrimSpace(req.TaskID),
			"conversation_id":  conversationID,
			"updated_by":       updatedBy,
			"version":          nextVersion,
			"updated_at":       now,
		}
		result := tx.Model(&orm.PersonalResourceDraft{}).Where("resource_id = ? AND version = ?", resource.ID, draft.Version).Updates(update)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrConflict
		}
		out = DraftResponse{
			Ref:            req.Ref,
			Path:           mustPath(req.Ref.ResourceType),
			DraftVersion:   nextVersion,
			DraftStatus:    "pending_confirm",
			BaseRevisionID: valueOrEmpty(resource.HeadRevisionID),
			BlobHash:       blob.Hash,
			ContentHash:    blob.Hash,
			DraftUpdatedAt: &now,
		}
		return nil
	})
	return out, normalizeGormErr(err)
}

func (s *Service) UpdateMetadata(ctx context.Context, req UpdateMetadataRequest) (MetadataResponse, error) {
	if err := validateRef(req.Ref); err != nil {
		return MetadataResponse{}, err
	}
	hasPreferencePatch := req.AgentPersona != nil || req.PreferredName != nil || req.ResponseStyle != nil
	if req.AutoEvo == nil && !hasPreferencePatch {
		return MetadataResponse{}, ErrInvalidResourceType
	}
	if hasPreferencePatch && req.Ref.ResourceType != ResourceTypeUserPreference {
		return MetadataResponse{}, ErrInvalidResourceType
	}
	var out MetadataResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resource, err := findResource(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), req.Ref)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		enabledFromOff := false
		var patchedPreference *preferencefile.PreferenceFile
		if hasPreferencePatch {
			if resource.HeadRevisionID == nil {
				return ErrRevisionNotFound
			}
			preferencePatch := preferencefile.PreferencePatch{
				AgentPersona:  req.AgentPersona,
				PreferredName: req.PreferredName,
				ResponseStyle: req.ResponseStyle,
			}
			patchedBlobs := map[string]orm.PersonalResourceBlob{}
			patchBlob := func(hash string) (orm.PersonalResourceBlob, preferencefile.PreferenceFile, error) {
				if blob, ok := patchedBlobs[hash]; ok {
					parsed, err := preferencefile.ParseFileContent(string(blob.Content))
					return blob, parsed, err
				}
				content, err := readBlobContent(ctx, tx, hash)
				if err != nil {
					return orm.PersonalResourceBlob{}, preferencefile.PreferenceFile{}, err
				}
				nextContent, parsed, err := preferencefile.PatchFileContent(string(content), preferencePatch)
				if err != nil {
					return orm.PersonalResourceBlob{}, preferencefile.PreferenceFile{}, err
				}
				blob, err := putBlob(ctx, tx, []byte(nextContent), now)
				if err != nil {
					return orm.PersonalResourceBlob{}, preferencefile.PreferenceFile{}, err
				}
				patchedBlobs[hash] = blob
				return blob, parsed, nil
			}
			head, err := findRevisionByID(ctx, tx, resource.ID, *resource.HeadRevisionID)
			if err != nil {
				return err
			}
			headBlob, parsed, err := patchBlob(head.BlobHash)
			if err != nil {
				return err
			}
			if err := tx.WithContext(ctx).Model(&orm.PersonalResourceRevision{}).
				Where("id = ? AND resource_id = ?", head.ID, resource.ID).
				Updates(map[string]any{
					"blob_hash":    headBlob.Hash,
					"content_hash": headBlob.Hash,
					"size":         headBlob.Size,
					"mime":         headBlob.Mime,
					"file_type":    headBlob.FileType,
					"binary":       headBlob.Binary,
				}).Error; err != nil {
				return err
			}

			var draft orm.PersonalResourceDraft
			if err := tx.WithContext(ctx).Where("resource_id = ?", resource.ID).Take(&draft).Error; err != nil {
				return err
			}
			draftBlob, _, err := patchBlob(draft.BlobHash)
			if err != nil {
				return err
			}
			result := tx.WithContext(ctx).Model(&orm.PersonalResourceDraft{}).
				Where("resource_id = ? AND version = ?", resource.ID, draft.Version).
				Updates(map[string]any{
					"blob_hash":        draftBlob.Hash,
					"content_hash":     draftBlob.Hash,
					"size":             draftBlob.Size,
					"mime":             draftBlob.Mime,
					"file_type":        draftBlob.FileType,
					"binary":           draftBlob.Binary,
					"draft_updated_at": now,
					"updated_by":       nullableString(req.UpdatedBy),
					"version":          draft.Version + 1,
					"updated_at":       now,
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return ErrConflict
			}

			var sessions []orm.PersonalResourceReviewSession
			if err := tx.WithContext(ctx).
				Where("resource_id = ? AND status = ?", resource.ID, reviewStatusActive).
				Find(&sessions).Error; err != nil {
				return err
			}
			for _, session := range sessions {
				var batches []orm.PersonalResourceReviewActionBatch
				if err := tx.WithContext(ctx).Where("session_id = ?", session.ID).Find(&batches).Error; err != nil {
					return err
				}
				for _, batch := range batches {
					beforeBlob, _, err := patchBlob(batch.BeforeDraftBlobHash)
					if err != nil {
						return err
					}
					afterBlob, _, err := patchBlob(batch.AfterDraftBlobHash)
					if err != nil {
						return err
					}
					if err := tx.WithContext(ctx).Model(&orm.PersonalResourceReviewActionBatch{}).
						Where("id = ? AND session_id = ?", batch.ID, session.ID).
						Updates(map[string]any{
							"before_draft_blob_hash": beforeBlob.Hash,
							"after_draft_blob_hash":  afterBlob.Hash,
						}).Error; err != nil {
						return err
					}
				}
				if err := tx.WithContext(ctx).Model(&orm.PersonalResourceReviewSession{}).
					Where("id = ? AND status = ?", session.ID, reviewStatusActive).
					Updates(map[string]any{
						"draft_version":   draft.Version + 1,
						"draft_blob_hash": draftBlob.Hash,
						"updated_at":      now,
					}).Error; err != nil {
					return err
				}
			}
			patchedPreference = &parsed
		}

		updates := map[string]any{
			"updated_by":      strings.TrimSpace(req.UpdatedBy),
			"updated_by_name": strings.TrimSpace(req.UpdatedByName),
			"updated_at":      now,
		}
		if req.AutoEvo != nil {
			enabledFromOff = !resource.AutoEvo && *req.AutoEvo
			updates["auto_evo"] = *req.AutoEvo
			updates["auto_evo_generation"] = gorm.Expr("auto_evo_generation + 1")
			updates["auto_evo_apply_status"] = "idle"
			updates["auto_evo_error"] = ""
			if *req.AutoEvo {
				updates["auto_evo_finished_at"] = nil
			} else {
				updates["auto_evo_started_at"] = nil
				updates["auto_evo_finished_at"] = now
			}
		}
		if enabledFromOff {
			if err := markPendingDraftAuto(ctx, tx, resource.ID, now); err != nil {
				return err
			}
		}
		if err := tx.Model(&orm.PersonalResource{}).Where("id = ?", resource.ID).Updates(updates).Error; err != nil {
			return err
		}
		var updated orm.PersonalResource
		if err := tx.Where("id = ?", resource.ID).Take(&updated).Error; err != nil {
			return err
		}
		out = MetadataResponse{
			Ref:                req.Ref,
			ResourceID:         updated.ID,
			AutoEvo:            updated.AutoEvo,
			AutoEvoApplyStatus: updated.AutoEvoApplyStatus,
			AutoEvoGeneration:  updated.AutoEvoGeneration,
			AutoEvoError:       updated.AutoEvoError,
			UpdatedBy:          updated.UpdatedBy,
			UpdatedByName:      updated.UpdatedByName,
			UpdatedAt:          updated.UpdatedAt,
			EnabledFromOff:     enabledFromOff,
		}
		if patchedPreference != nil {
			out.AgentPersona = &patchedPreference.AgentPersona
			out.PreferredName = &patchedPreference.PreferredName
			out.ResponseStyle = &patchedPreference.ResponseStyle
		}
		return nil
	})
	return out, normalizeGormErr(err)
}

func (s *Service) DraftPreview(ctx context.Context, req DraftPreviewRequest) (DraftPreviewResponse, error) {
	head, err := s.ReadFile(ctx, ReadFileRequest{Ref: req.Ref, RefType: FileRefHead})
	if err != nil {
		return DraftPreviewResponse{}, err
	}
	draft, err := s.ReadFile(ctx, ReadFileRequest{Ref: req.Ref, RefType: FileRefDraft})
	if err != nil {
		return DraftPreviewResponse{}, err
	}
	diff, err := filediff.CompareContent(
		filediff.Content{Path: head.Path, Data: []byte(head.Content), Binary: head.Binary, EditableText: true, Size: head.Size},
		filediff.Content{Path: draft.Path, Data: []byte(draft.Content), Binary: draft.Binary, EditableText: true, Size: draft.Size},
		filediff.Options{},
	)
	if err != nil {
		return DraftPreviewResponse{}, err
	}
	diff, review, err := s.prepareReviewDiff(ctx, req.Ref, diff, draft.DraftStatus)
	if err != nil {
		return DraftPreviewResponse{}, err
	}
	return DraftPreviewResponse{
		Ref:            req.Ref,
		ResourceType:   req.Ref.ResourceType,
		Path:           head.Path,
		BaseRevisionID: head.RevisionID,
		DraftVersion:   draft.DraftVersion,
		DraftStatus:    draft.DraftStatus,
		HeadContent:    head.Content,
		DraftContent:   draft.Content,
		Diff:           diff,
		ReviewID:       review.ReviewID,
		ReviewVersion:  review.ReviewVersion,
		CanUndo:        review.CanUndo,
		PendingCount:   review.PendingCount,
		AcceptedCount:  review.AcceptedCount,
		RejectedCount:  review.RejectedCount,
	}, nil
}

func (s *Service) CommitDraft(ctx context.Context, req CommitDraftRequest) (CommitResponse, error) {
	if err := validateRef(req.Ref); err != nil {
		return CommitResponse{}, err
	}
	resource, err := findResource(ctx, s.db, req.Ref)
	if err != nil {
		return CommitResponse{}, err
	}
	resp, err := versionfs.NewEngine(versionfs.EngineDeps{DB: s.db, Store: versionStore{}, Clock: s.clock}).CommitDraft(ctx, versionfs.CommitDraftRequest{
		ResourceID:             resource.ID,
		UserID:                 req.CreatedBy,
		ExpectedHeadRevisionID: strings.TrimSpace(req.ExpectedHeadRevisionID),
		ExpectedDraftVersion:   req.ExpectedDraftVersion,
		Message:                strings.TrimSpace(req.Message),
		ChangeSource:           "draft_commit",
		SourceRefType:          strings.TrimSpace(req.SourceRefType),
		SourceRefID:            strings.TrimSpace(req.SourceRefID),
	})
	if err != nil {
		return CommitResponse{}, normalizeVersionFSErr(err)
	}
	if strings.TrimSpace(req.CreatedBy) != "" {
		if err := s.db.WithContext(ctx).Model(&orm.PersonalResource{}).Where("id = ?", resource.ID).Updates(map[string]any{
			"updated_by":      strings.TrimSpace(req.CreatedBy),
			"updated_by_name": strings.TrimSpace(req.CreatedByName),
			"updated_at":      s.clock.Now(),
		}).Error; err != nil {
			return CommitResponse{}, normalizeGormErr(err)
		}
	}
	revision, err := findRevisionByID(ctx, s.db, resource.ID, resp.RevisionID)
	if err != nil {
		return CommitResponse{}, normalizeGormErr(err)
	}
	content, err := readBlobContent(ctx, s.db, revision.BlobHash)
	if err != nil {
		return CommitResponse{}, normalizeGormErr(err)
	}
	return CommitResponse{Ref: req.Ref, Path: revision.Path, RevisionID: resp.RevisionID, RevisionNo: resp.RevisionNo, Content: string(content)}, nil
}

func (s *Service) DiscardDraft(ctx context.Context, ref ResourceRef) (DraftResponse, error) {
	if err := validateRef(ref); err != nil {
		return DraftResponse{}, err
	}
	var out DraftResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resource, err := findResource(ctx, tx, ref)
		if err != nil {
			return err
		}
		if resource.HeadRevisionID == nil {
			return ErrRevisionNotFound
		}
		head, err := findRevisionByID(ctx, tx, resource.ID, *resource.HeadRevisionID)
		if err != nil {
			return err
		}
		var draft orm.PersonalResourceDraft
		if err := tx.Where("resource_id = ?", resource.ID).Take(&draft).Error; err != nil {
			return err
		}
		now := s.clock.Now()
		nextVersion := draft.Version + 1
		if err := tx.Model(&orm.PersonalResourceDraft{}).Where("resource_id = ?", resource.ID).Updates(map[string]any{
			"base_revision_id": resource.HeadRevisionID,
			"path":             head.Path,
			"blob_hash":        head.BlobHash,
			"content_hash":     head.ContentHash,
			"size":             head.Size,
			"mime":             head.Mime,
			"file_type":        head.FileType,
			"binary":           head.Binary,
			"draft_status":     "",
			"draft_updated_at": nil,
			"task_id":          "",
			"conversation_id":  nil,
			"updated_by":       nil,
			"version":          nextVersion,
			"updated_at":       now,
		}).Error; err != nil {
			return err
		}
		if err := markActiveReviewSessions(ctx, tx, resource.ID, reviewStatusDiscarded, ref.UserID, now); err != nil {
			return err
		}
		out = DraftResponse{
			Ref:            ref,
			Path:           head.Path,
			DraftVersion:   nextVersion,
			DraftStatus:    "",
			BaseRevisionID: head.ID,
			BlobHash:       head.BlobHash,
			ContentHash:    head.ContentHash,
		}
		return nil
	})
	return out, normalizeGormErr(err)
}

func (s *Service) ListRevisions(ctx context.Context, req ListRevisionsRequest) (RevisionListResponse, error) {
	resource, err := findResource(ctx, s.db, req.Ref)
	if err != nil {
		return RevisionListResponse{}, err
	}
	var rows []orm.PersonalResourceRevision
	if err := s.db.WithContext(ctx).Where("resource_id = ?", resource.ID).Order("revision_no DESC").Find(&rows).Error; err != nil {
		return RevisionListResponse{}, err
	}
	items := make([]RevisionSummary, 0, len(rows))
	for _, row := range rows {
		item := revisionSummary(row)
		item.IsHead = resource.HeadRevisionID != nil && row.ID == *resource.HeadRevisionID
		items = append(items, item)
	}
	return RevisionListResponse{Items: items}, nil
}

func (s *Service) GetRevision(ctx context.Context, ref ResourceRef, revisionID string) (RevisionDetailResponse, error) {
	resource, err := findResource(ctx, s.db, ref)
	if err != nil {
		return RevisionDetailResponse{}, err
	}
	row, err := findRevisionByID(ctx, s.db, resource.ID, revisionID)
	if err != nil {
		return RevisionDetailResponse{}, err
	}
	content, err := readBlobContent(ctx, s.db, row.BlobHash)
	if err != nil {
		return RevisionDetailResponse{}, err
	}
	detail := revisionDetail(row, string(content))
	detail.IsHead = resource.HeadRevisionID != nil && row.ID == *resource.HeadRevisionID
	return detail, nil
}

func (s *Service) Rollback(ctx context.Context, req RollbackRequest) (RollbackResponse, error) {
	if err := validateRef(req.Ref); err != nil {
		return RollbackResponse{}, err
	}
	resource, err := findResource(ctx, s.db, req.Ref)
	if err != nil {
		return RollbackResponse{}, err
	}
	resp, err := versionfs.NewEngine(versionfs.EngineDeps{DB: s.db, Store: versionStore{}, Clock: s.clock}).Rollback(ctx, versionfs.RollbackRequest{
		ResourceID:             resource.ID,
		UserID:                 req.CreatedBy,
		TargetRevisionID:       strings.TrimSpace(req.RevisionID),
		ExpectedHeadRevisionID: strings.TrimSpace(req.ExpectedHeadRevisionID),
		Message:                strings.TrimSpace(req.Message),
		RequireNoDraft:         true,
	})
	if err != nil {
		return RollbackResponse{}, normalizeVersionFSErr(err)
	}
	if strings.TrimSpace(req.CreatedBy) != "" {
		if err := s.db.WithContext(ctx).Model(&orm.PersonalResource{}).Where("id = ?", resource.ID).Updates(map[string]any{
			"updated_by":      strings.TrimSpace(req.CreatedBy),
			"updated_by_name": strings.TrimSpace(req.CreatedByName),
			"updated_at":      s.clock.Now(),
		}).Error; err != nil {
			return RollbackResponse{}, normalizeGormErr(err)
		}
	}
	revision, err := findRevisionByID(ctx, s.db, resource.ID, resp.RevisionID)
	if err != nil {
		return RollbackResponse{}, normalizeGormErr(err)
	}
	content, err := readBlobContent(ctx, s.db, revision.BlobHash)
	if err != nil {
		return RollbackResponse{}, normalizeGormErr(err)
	}
	return RollbackResponse{Ref: req.Ref, Path: revision.Path, RevisionID: resp.RevisionID, RevisionNo: revision.RevisionNo, Content: string(content)}, nil
}

func (s *Service) Action(ctx context.Context, req ReviewActionRequest) (ReviewActionResponse, error) {
	if err := validateRef(req.Ref); err != nil {
		return ReviewActionResponse{}, err
	}
	if strings.TrimSpace(req.ReviewID) == "" || req.ExpectedReviewVersion <= 0 || len(req.Items) == 0 {
		return ReviewActionResponse{}, ErrInvalidReview
	}
	var out ReviewActionResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resource, session, draft, err := s.loadActiveReview(ctx, tx, req.Ref, req.ReviewID)
		if err != nil {
			return err
		}
		if session.ReviewVersion != req.ExpectedReviewVersion {
			return ErrConflict
		}
		if err := validateReviewSnapshot(resource, draft, session); err != nil {
			return err
		}
		if resource.HeadRevisionID == nil {
			return ErrRevisionNotFound
		}
		diff, headContent, draftContent, err := diffForReviewSession(ctx, tx, req.Ref.ResourceType, resource.ID, session)
		if err != nil {
			return err
		}
		decisions, err := currentReviewDecisions(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		reviewFile := reviewFileFromFileDiff(diff)
		versionfs.AnnotateReviewFile(&reviewFile, reviewSessionMeta(session), decisions, false)
		known := versionfs.KnownHunks(reviewFile)
		if len(known) == 0 {
			return ErrInvalidReview
		}

		actionDecisions := map[string]string{}
		nextDecisions := copyReviewDecisions(decisions)
		outItems := make([]ReviewActionItem, 0, len(req.Items))
		rows := make([]orm.PersonalResourceReviewActionItem, 0, len(req.Items))
		for _, item := range req.Items {
			hunkID := strings.TrimSpace(item.HunkID)
			line, ok := known[hunkID]
			if !ok {
				return ErrInvalidReview
			}
			path := NormalizePath(item.Path)
			if path != "" && path != session.Path {
				return ErrInvalidPath
			}
			if _, exists := actionDecisions[hunkID]; exists {
				return ErrInvalidReview
			}
			decision, err := normalizeReviewDecision(item.Decision)
			if err != nil {
				return err
			}
			actionDecisions[hunkID] = decision
			nextDecisions[hunkID] = decision
			outItems = append(outItems, ReviewActionItem{Path: session.Path, HunkID: hunkID, Decision: decision})
			rows = append(rows, orm.PersonalResourceReviewActionItem{
				ID:       uuid.NewString(),
				HunkID:   hunkID,
				Decision: decision,
				OldStart: line.OldStart,
				OldLines: line.OldLines,
				NewStart: line.NewStart,
				NewLines: line.NewLines,
			})
		}

		nextContent, err := versionfs.ApplyTextReview(headContent, draftContent, reviewFile, nextDecisions)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		blob, err := putBlob(ctx, tx, []byte(nextContent), now)
		if err != nil {
			return err
		}
		nextDraftVersion := draft.Version + 1
		result := tx.Model(&orm.PersonalResourceDraft{}).
			Where("resource_id = ? AND version = ? AND blob_hash = ?", resource.ID, draft.Version, draft.BlobHash).
			Updates(map[string]any{
				"blob_hash":        blob.Hash,
				"content_hash":     blob.Hash,
				"size":             blob.Size,
				"mime":             blob.Mime,
				"file_type":        blob.FileType,
				"binary":           blob.Binary,
				"draft_status":     "pending_confirm",
				"draft_updated_at": now,
				"updated_by":       nullableString(req.UpdatedBy),
				"version":          nextDraftVersion,
				"updated_at":       now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrConflict
		}

		nextReviewVersion := session.ReviewVersion + 1
		batchID := uuid.NewString()
		batch := orm.PersonalResourceReviewActionBatch{
			ID:                  batchID,
			SessionID:           session.ID,
			ResourceID:          resource.ID,
			BeforeDraftBlobHash: draft.BlobHash,
			AfterDraftBlobHash:  blob.Hash,
			BeforeDraftVersion:  draft.Version,
			AfterDraftVersion:   nextDraftVersion,
			ReviewVersion:       nextReviewVersion,
			CreatedBy:           nullableString(req.UpdatedBy),
			CreatedAt:           now,
		}
		if err := tx.Create(&batch).Error; err != nil {
			return err
		}
		for i := range rows {
			rows[i].BatchID = batchID
			rows[i].CreatedAt = now
		}
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
		result = tx.Model(&orm.PersonalResourceReviewSession{}).
			Where("id = ? AND review_version = ?", session.ID, session.ReviewVersion).
			Updates(map[string]any{
				"draft_version":   nextDraftVersion,
				"draft_blob_hash": blob.Hash,
				"review_version":  nextReviewVersion,
				"updated_at":      now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrConflict
		}
		canUndo, err := canUndoReview(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		out = ReviewActionResponse{
			Ref:           req.Ref,
			ReviewID:      session.ID,
			ReviewVersion: nextReviewVersion,
			BatchID:       batchID,
			DraftVersion:  nextDraftVersion,
			CanUndo:       canUndo,
			DraftContent:  nextContent,
			Items:         outItems,
		}
		return nil
	})
	return out, normalizeGormErr(err)
}

func (s *Service) Undo(ctx context.Context, req ReviewUndoRequest) (ReviewUndoResponse, error) {
	if err := validateRef(req.Ref); err != nil {
		return ReviewUndoResponse{}, err
	}
	if strings.TrimSpace(req.ReviewID) == "" || req.ExpectedReviewVersion <= 0 {
		return ReviewUndoResponse{}, ErrInvalidReview
	}
	var out ReviewUndoResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resource, session, draft, err := s.loadActiveReview(ctx, tx, req.Ref, req.ReviewID)
		if err != nil {
			return err
		}
		if session.ReviewVersion != req.ExpectedReviewVersion {
			return ErrConflict
		}
		if err := validateReviewSnapshot(resource, draft, session); err != nil {
			return err
		}
		var batch orm.PersonalResourceReviewActionBatch
		if err := tx.WithContext(ctx).
			Where("session_id = ?", session.ID).
			Order("created_at DESC, id DESC").
			Take(&batch).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrConflict
			}
			return err
		}
		var items []orm.PersonalResourceReviewActionItem
		if err := tx.WithContext(ctx).Where("batch_id = ?", batch.ID).Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
			return err
		}
		beforeBlob, err := findBlob(ctx, tx, batch.BeforeDraftBlobHash)
		if err != nil {
			return err
		}
		content, err := readBlobContent(ctx, tx, beforeBlob.Hash)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		nextDraftVersion := draft.Version + 1
		result := tx.Model(&orm.PersonalResourceDraft{}).
			Where("resource_id = ? AND version = ? AND blob_hash = ?", resource.ID, draft.Version, draft.BlobHash).
			Updates(map[string]any{
				"blob_hash":        beforeBlob.Hash,
				"content_hash":     beforeBlob.Hash,
				"size":             beforeBlob.Size,
				"mime":             beforeBlob.Mime,
				"file_type":        beforeBlob.FileType,
				"binary":           beforeBlob.Binary,
				"draft_status":     "pending_confirm",
				"draft_updated_at": now,
				"updated_by":       nullableString(req.UpdatedBy),
				"version":          nextDraftVersion,
				"updated_at":       now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrConflict
		}
		if err := tx.WithContext(ctx).Where("batch_id = ?", batch.ID).Delete(&orm.PersonalResourceReviewActionItem{}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Where("id = ?", batch.ID).Delete(&orm.PersonalResourceReviewActionBatch{}).Error; err != nil {
			return err
		}
		nextReviewVersion := session.ReviewVersion + 1
		result = tx.Model(&orm.PersonalResourceReviewSession{}).
			Where("id = ? AND review_version = ?", session.ID, session.ReviewVersion).
			Updates(map[string]any{
				"draft_version":   nextDraftVersion,
				"draft_blob_hash": beforeBlob.Hash,
				"review_version":  nextReviewVersion,
				"updated_at":      now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrConflict
		}
		restoredDecisions, err := currentReviewDecisions(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		restored := make([]ReviewActionItem, 0, len(items))
		for _, item := range items {
			decision := restoredDecisions[item.HunkID]
			if decision == "" {
				decision = decisionPending
			}
			restored = append(restored, ReviewActionItem{Path: session.Path, HunkID: item.HunkID, Decision: decision})
		}
		canUndo, err := canUndoReview(ctx, tx, session.ID)
		if err != nil {
			return err
		}
		out = ReviewUndoResponse{
			Ref:             req.Ref,
			ReviewID:        session.ID,
			ReviewVersion:   nextReviewVersion,
			UndoneBatchID:   batch.ID,
			DraftVersion:    nextDraftVersion,
			CanUndo:         canUndo,
			DraftContent:    string(content),
			RestoredActions: restored,
		}
		return nil
	})
	return out, normalizeGormErr(err)
}

type reviewPreviewMeta struct {
	ReviewID      string
	ReviewVersion int64
	CanUndo       bool
	PendingCount  int
	AcceptedCount int
	RejectedCount int
}

func (s *Service) prepareReviewDiff(ctx context.Context, ref ResourceRef, diff filediff.FileDiff, draftStatus string) (filediff.FileDiff, reviewPreviewMeta, error) {
	if strings.TrimSpace(draftStatus) != "pending_confirm" {
		return diff, reviewPreviewMeta{}, nil
	}
	session, err := s.ensureReviewSession(ctx, ref)
	if err != nil {
		return filediff.FileDiff{}, reviewPreviewMeta{}, err
	}
	decisions, err := currentReviewDecisions(ctx, s.db, session.ID)
	if err != nil {
		return filediff.FileDiff{}, reviewPreviewMeta{}, err
	}
	canUndo, err := canUndoReview(ctx, s.db, session.ID)
	if err != nil {
		return filediff.FileDiff{}, reviewPreviewMeta{}, err
	}
	displayDiff, _, _, err := diffForReviewSession(ctx, s.db, ref.ResourceType, session.ResourceID, session)
	if err != nil {
		return filediff.FileDiff{}, reviewPreviewMeta{}, err
	}
	reviewFile := reviewFileFromFileDiff(displayDiff)
	versionfs.AnnotateReviewFile(&reviewFile, reviewSessionMeta(session), decisions, canUndo)
	out := fileDiffFromReviewFile(reviewFile, displayDiff)
	out.DiffEntryLines = reviewDisplayDiffEntryLines(out.DiffEntryLines)
	return out, reviewMetaFromFile(reviewFile), nil
}

func (s *Service) ensureReviewSession(ctx context.Context, ref ResourceRef) (orm.PersonalResourceReviewSession, error) {
	var out orm.PersonalResourceReviewSession
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resource, err := findResource(ctx, tx, ref)
		if err != nil {
			return err
		}
		if resource.HeadRevisionID == nil {
			return ErrRevisionNotFound
		}
		var draft orm.PersonalResourceDraft
		if err := tx.WithContext(ctx).Where("resource_id = ?", resource.ID).Take(&draft).Error; err != nil {
			return err
		}
		baseRevisionID := valueOrEmpty(draft.BaseRevisionID)
		if baseRevisionID == "" {
			baseRevisionID = *resource.HeadRevisionID
		}
		now := s.clock.Now()
		var existing orm.PersonalResourceReviewSession
		err = tx.WithContext(ctx).
			Where("resource_id = ? AND status = ?", resource.ID, reviewStatusActive).
			Order("updated_at DESC").
			Take(&existing).Error
		if err == nil {
			if existing.BaseRevisionID == baseRevisionID &&
				existing.HeadRevisionID == *resource.HeadRevisionID &&
				existing.DraftVersion == draft.Version &&
				existing.DraftBlobHash == draft.BlobHash {
				out = existing
				return nil
			}
			if err := tx.Model(&orm.PersonalResourceReviewSession{}).Where("id = ?", existing.ID).Updates(map[string]any{
				"status":     reviewStatusInvalidated,
				"updated_at": now,
			}).Error; err != nil {
				return err
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		out = orm.PersonalResourceReviewSession{
			ID:             uuid.NewString(),
			ResourceID:     resource.ID,
			Path:           draft.Path,
			BaseRevisionID: baseRevisionID,
			HeadRevisionID: *resource.HeadRevisionID,
			DraftVersion:   draft.Version,
			DraftBlobHash:  draft.BlobHash,
			ReviewVersion:  1,
			Status:         reviewStatusActive,
			CreatedBy:      nullableString(ref.UserID),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		return tx.WithContext(ctx).Create(&out).Error
	})
	return out, normalizeGormErr(err)
}

func (s *Service) loadActiveReview(ctx context.Context, tx *gorm.DB, ref ResourceRef, reviewID string) (orm.PersonalResource, orm.PersonalResourceReviewSession, orm.PersonalResourceDraft, error) {
	resource, err := findResource(ctx, tx, ref)
	if err != nil {
		return orm.PersonalResource{}, orm.PersonalResourceReviewSession{}, orm.PersonalResourceDraft{}, err
	}
	var session orm.PersonalResourceReviewSession
	if err := tx.WithContext(ctx).
		Where("id = ? AND resource_id = ? AND status = ?", strings.TrimSpace(reviewID), resource.ID, reviewStatusActive).
		Take(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return orm.PersonalResource{}, orm.PersonalResourceReviewSession{}, orm.PersonalResourceDraft{}, ErrReviewNotFound
		}
		return orm.PersonalResource{}, orm.PersonalResourceReviewSession{}, orm.PersonalResourceDraft{}, err
	}
	var draft orm.PersonalResourceDraft
	if err := tx.WithContext(ctx).Where("resource_id = ?", resource.ID).Take(&draft).Error; err != nil {
		return orm.PersonalResource{}, orm.PersonalResourceReviewSession{}, orm.PersonalResourceDraft{}, err
	}
	return resource, session, draft, nil
}

func validateReviewSnapshot(resource orm.PersonalResource, draft orm.PersonalResourceDraft, session orm.PersonalResourceReviewSession) error {
	if resource.HeadRevisionID == nil || *resource.HeadRevisionID != session.HeadRevisionID {
		return ErrConflict
	}
	baseRevisionID := valueOrEmpty(draft.BaseRevisionID)
	if baseRevisionID == "" {
		baseRevisionID = *resource.HeadRevisionID
	}
	if baseRevisionID != session.BaseRevisionID || draft.Version != session.DraftVersion || draft.BlobHash != session.DraftBlobHash {
		return ErrConflict
	}
	return nil
}

func diffForReviewSession(ctx context.Context, tx *gorm.DB, resourceType ResourceType, resourceID string, session orm.PersonalResourceReviewSession) (filediff.FileDiff, string, string, error) {
	head, err := findRevisionByID(ctx, tx, resourceID, session.HeadRevisionID)
	if err != nil {
		return filediff.FileDiff{}, "", "", err
	}
	basisBlobHash, err := reviewBasisDraftBlobHash(ctx, tx, session)
	if err != nil {
		return filediff.FileDiff{}, "", "", err
	}
	basisBlob, err := findBlob(ctx, tx, basisBlobHash)
	if err != nil {
		return filediff.FileDiff{}, "", "", err
	}
	headContent, err := readBlobContent(ctx, tx, head.BlobHash)
	if err != nil {
		return filediff.FileDiff{}, "", "", err
	}
	basisContent, err := readBlobContent(ctx, tx, basisBlob.Hash)
	if err != nil {
		return filediff.FileDiff{}, "", "", err
	}
	diff, err := filediff.CompareContent(
		filediff.Content{Path: mustPath(resourceType), Data: headContent, Binary: head.Binary, EditableText: true, Size: head.Size},
		filediff.Content{Path: mustPath(resourceType), Data: basisContent, Binary: basisBlob.Binary, EditableText: true, Size: basisBlob.Size},
		filediff.Options{},
	)
	if err != nil {
		return filediff.FileDiff{}, "", "", err
	}
	if !diff.Supported {
		return filediff.FileDiff{}, "", "", ErrUnsupported
	}
	return diff, string(headContent), string(basisContent), nil
}

func reviewBasisDraftBlobHash(ctx context.Context, tx *gorm.DB, session orm.PersonalResourceReviewSession) (string, error) {
	var batch orm.PersonalResourceReviewActionBatch
	err := tx.WithContext(ctx).
		Where("session_id = ?", session.ID).
		Order("created_at ASC, id ASC").
		Take(&batch).Error
	if err == nil {
		return batch.BeforeDraftBlobHash, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return session.DraftBlobHash, nil
	}
	return "", err
}

func normalizeReviewDecision(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accept", decisionAccepted:
		return decisionAccepted, nil
	case "reject", decisionRejected:
		return decisionRejected, nil
	default:
		return "", ErrInvalidReview
	}
}

func reviewSessionMeta(session orm.PersonalResourceReviewSession) versionfs.ReviewSessionMeta {
	return versionfs.ReviewSessionMeta{
		ID:             session.ID,
		Path:           session.Path,
		Version:        session.ReviewVersion,
		DraftVersion:   session.DraftVersion,
		BaseRevisionID: session.BaseRevisionID,
	}
}

func reviewMetaFromFile(file versionfs.ReviewFile) reviewPreviewMeta {
	return reviewPreviewMeta{
		ReviewID:      file.ReviewID,
		ReviewVersion: file.ReviewVersion,
		CanUndo:       file.CanUndo,
		PendingCount:  file.PendingCount,
		AcceptedCount: file.AcceptedCount,
		RejectedCount: file.RejectedCount,
	}
}

func reviewFileFromFileDiff(file filediff.FileDiff) versionfs.ReviewFile {
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
		Path:      file.Path,
		Type:      versionfs.EntryTypeFile,
		Status:    file.Status,
		Binary:    file.Binary,
		TooLarge:  file.TooLarge,
		HunkCount: file.HunkCount,
		DiffLines: lines,
	}
}

func fileDiffFromReviewFile(review versionfs.ReviewFile, file filediff.FileDiff) filediff.FileDiff {
	lines := make([]filediff.DiffEntryLine, 0, len(review.DiffLines))
	for _, line := range review.DiffLines {
		lines = append(lines, filediff.DiffEntryLine{
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
	file.HunkCount = review.HunkCount
	file.DiffEntryLines = lines
	return file
}

func reviewDisplayDiffEntryLines(lines []filediff.DiffEntryLine) []filediff.DiffEntryLine {
	out := make([]filediff.DiffEntryLine, 0, len(lines))
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
			out = append(out, resolvedReviewHunkLines(lines[i+1:end], decisionAccepted)...)
		case decisionRejected:
			out = append(out, line)
			out = append(out, resolvedReviewHunkLines(lines[i+1:end], decisionRejected)...)
		default:
			out = append(out, lines[i:end]...)
		}
		i = end
	}
	return out
}

func resolvedReviewHunkLines(lines []filediff.DiffEntryLine, decision string) []filediff.DiffEntryLine {
	out := make([]filediff.DiffEntryLine, 0, len(lines))
	for _, line := range lines {
		switch line.Type {
		case "CONTEXT":
			out = append(out, line)
		case "ADDITION":
			if decision == decisionAccepted {
				out = append(out, resolvedReviewContextLine(line, line.NewLine))
			}
		case "DELETION":
			if decision == decisionRejected {
				out = append(out, resolvedReviewContextLine(line, line.OldLine))
			}
		default:
			out = append(out, line)
		}
	}
	return out
}

func resolvedReviewContextLine(line filediff.DiffEntryLine, lineNo int) filediff.DiffEntryLine {
	return filediff.DiffEntryLine{
		Type:                    "CONTEXT",
		Text:                    line.Text,
		HTML:                    html.EscapeString(line.Text),
		OldLine:                 lineNo,
		NewLine:                 lineNo,
		DisplayNoNewLineWarning: line.DisplayNoNewLineWarning,
	}
}

func copyReviewDecisions(decisions map[string]string) map[string]string {
	out := make(map[string]string, len(decisions))
	for key, value := range decisions {
		out[key] = value
	}
	return out
}

func currentReviewDecisions(ctx context.Context, db *gorm.DB, sessionID string) (map[string]string, error) {
	var batches []orm.PersonalResourceReviewActionBatch
	if err := db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at ASC, id ASC").Find(&batches).Error; err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, batch := range batches {
		var items []orm.PersonalResourceReviewActionItem
		if err := db.WithContext(ctx).Where("batch_id = ?", batch.ID).Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
			return nil, err
		}
		for _, item := range items {
			out[item.HunkID] = item.Decision
		}
	}
	return out, nil
}

func canUndoReview(ctx context.Context, db *gorm.DB, sessionID string) (bool, error) {
	var count int64
	if err := db.WithContext(ctx).Model(&orm.PersonalResourceReviewActionBatch{}).Where("session_id = ?", sessionID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func findBlob(ctx context.Context, db *gorm.DB, hash string) (orm.PersonalResourceBlob, error) {
	var blob orm.PersonalResourceBlob
	if err := db.WithContext(ctx).Where("hash = ?", hash).Take(&blob).Error; err != nil {
		return orm.PersonalResourceBlob{}, err
	}
	return blob, nil
}

func markActiveReviewSessions(ctx context.Context, tx *gorm.DB, resourceID, status, userID string, now time.Time) error {
	return tx.WithContext(ctx).Model(&orm.PersonalResourceReviewSession{}).
		Where("resource_id = ? AND status = ?", resourceID, reviewStatusActive).
		Updates(map[string]any{
			"status":     status,
			"updated_at": now,
		}).Error
}

func markPendingDraftAuto(ctx context.Context, tx *gorm.DB, resourceID string, now time.Time) error {
	var draft orm.PersonalResourceDraft
	if err := tx.WithContext(ctx).Where("resource_id = ?", resourceID).Take(&draft).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if !isPendingConfirmDraftStatus(draft.DraftStatus) {
		return nil
	}
	if err := tx.WithContext(ctx).Model(&orm.PersonalResourceDraft{}).
		Where("resource_id = ? AND version = ?", resourceID, draft.Version).
		Updates(map[string]any{
			"draft_status": draftStatusAutoPending,
			"updated_at":   now,
		}).Error; err != nil {
		return err
	}
	return markActiveReviewSessions(ctx, tx, resourceID, reviewStatusInvalidated, "", now)
}

func isPendingConfirmDraftStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case draftStatusPendingConfirm, draftStatusPending:
		return true
	default:
		return false
	}
}

func (s *Service) loadState(ctx context.Context, db *gorm.DB, ref ResourceRef) (ResourceState, error) {
	resource, err := findResource(ctx, db, ref)
	if err != nil {
		return ResourceState{}, err
	}
	var draft orm.PersonalResourceDraft
	if err := db.WithContext(ctx).Where("resource_id = ?", resource.ID).Take(&draft).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return ResourceState{}, err
	}
	return ResourceState{
		ID:             resource.ID,
		UserID:         resource.UserID,
		ResourceType:   ref.ResourceType,
		Path:           mustPath(ref.ResourceType),
		HeadRevisionID: valueOrEmpty(resource.HeadRevisionID),
		Version:        resource.Version,
		DraftVersion:   draft.Version,
		DraftStatus:    draft.DraftStatus,
	}, nil
}

func (s *Service) fileResponseForRevision(ctx context.Context, resourceType ResourceType, revision orm.PersonalResourceRevision) (FileResponse, error) {
	content, err := readBlobContent(ctx, s.db, revision.BlobHash)
	if err != nil {
		return FileResponse{}, err
	}
	return FileResponse{
		ResourceType: resourceType,
		Path:         revision.Path,
		Content:      string(content),
		BlobHash:     revision.BlobHash,
		ContentHash:  revision.ContentHash,
		Size:         revision.Size,
		Mime:         revision.Mime,
		FileType:     revision.FileType,
		Binary:       revision.Binary,
		RevisionID:   revision.ID,
		RevisionNo:   revision.RevisionNo,
	}, nil
}

func validateRef(ref ResourceRef) error {
	if strings.TrimSpace(ref.UserID) == "" {
		return ErrResourceNotFound
	}
	_, err := FixedPath(ref.ResourceType)
	return err
}

func findResource(ctx context.Context, db *gorm.DB, ref ResourceRef) (orm.PersonalResource, error) {
	var resource orm.PersonalResource
	if err := db.WithContext(ctx).
		Where("user_id = ? AND resource_type = ?", strings.TrimSpace(ref.UserID), string(ref.ResourceType)).
		Take(&resource).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return orm.PersonalResource{}, ErrResourceNotFound
		}
		return orm.PersonalResource{}, err
	}
	return resource, nil
}

func findRevisionByID(ctx context.Context, db *gorm.DB, resourceID, revisionID string) (orm.PersonalResourceRevision, error) {
	var revision orm.PersonalResourceRevision
	if err := db.WithContext(ctx).Where("id = ? AND resource_id = ?", revisionID, resourceID).Take(&revision).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return orm.PersonalResourceRevision{}, ErrRevisionNotFound
		}
		return orm.PersonalResourceRevision{}, err
	}
	return revision, nil
}

func nextRevisionNo(ctx context.Context, db *gorm.DB, resourceID string) (int64, error) {
	var maxNo int64
	if err := db.WithContext(ctx).Model(&orm.PersonalResourceRevision{}).
		Where("resource_id = ?", resourceID).
		Select("COALESCE(MAX(revision_no), 0)").
		Scan(&maxNo).Error; err != nil {
		return 0, err
	}
	return maxNo + 1, nil
}

func putBlob(ctx context.Context, db *gorm.DB, data []byte, now time.Time) (orm.PersonalResourceBlob, error) {
	hash := hashBytes(data)
	blob := orm.PersonalResourceBlob{
		Hash:           hash,
		Size:           int64(len(data)),
		Mime:           "text/markdown; charset=utf-8",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        append([]byte(nil), data...),
		CreatedAt:      now,
	}
	if err := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&blob).Error; err != nil {
		return orm.PersonalResourceBlob{}, err
	}
	if err := db.WithContext(ctx).Where("hash = ?", hash).Take(&blob).Error; err != nil {
		return orm.PersonalResourceBlob{}, err
	}
	return blob, nil
}

func readBlobContent(ctx context.Context, db *gorm.DB, hash string) ([]byte, error) {
	var blob orm.PersonalResourceBlob
	if err := db.WithContext(ctx).Where("hash = ?", hash).Take(&blob).Error; err != nil {
		return nil, err
	}
	if blob.Binary {
		return nil, fmt.Errorf("binary content is not available")
	}
	return append([]byte(nil), blob.Content...), nil
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func revisionSummary(row orm.PersonalResourceRevision) RevisionSummary {
	parent := valueOrEmpty(row.ParentRevisionID)
	createdBy := valueOrEmpty(row.CreatedBy)
	return RevisionSummary{
		ID:               row.ID,
		RevisionID:       row.ID,
		RevisionNo:       row.RevisionNo,
		ParentRevisionID: parent,
		Path:             row.Path,
		ContentHash:      row.ContentHash,
		Size:             row.Size,
		Message:          row.Message,
		ChangeSource:     row.ChangeSource,
		SourceRefType:    row.SourceRefType,
		SourceRefID:      row.SourceRefID,
		CreatedBy:        createdBy,
		CreatedAt:        row.CreatedAt,
	}
}

func revisionDetail(row orm.PersonalResourceRevision, content string) RevisionDetailResponse {
	return RevisionDetailResponse{
		RevisionSummary: revisionSummary(row),
		Content:         content,
		BlobHash:        row.BlobHash,
		Mime:            row.Mime,
		FileType:        row.FileType,
		Binary:          row.Binary,
	}
}

func normalizeGormErr(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrResourceNotFound
	}
	return err
}

func normalizeVersionFSErr(err error) error {
	switch {
	case errors.Is(err, versionfs.ErrDraftEmpty):
		return ErrDraftNotFound
	case errors.Is(err, versionfs.ErrDraftConflict), errors.Is(err, versionfs.ErrStaleDraftVersion), errors.Is(err, versionfs.ErrDraftBaseConflict), errors.Is(err, versionfs.ErrHeadRevisionConflict):
		return ErrConflict
	default:
		return normalizeGormErr(err)
	}
}

func nullableString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mustPath(resourceType ResourceType) string {
	path, err := FixedPath(resourceType)
	if err != nil {
		return ""
	}
	return path
}

type clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
