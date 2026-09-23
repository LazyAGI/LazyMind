package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

type Service struct {
	DB            *gorm.DB
	inTransaction bool
}

func New(db *gorm.DB) *Service { return &Service{DB: db} }

// InTransaction participates in the caller's commit/rollback boundary. The
// caller owns retries; no nested SQLite writer gate or independent commit.
func InTransaction(tx *gorm.DB) *Service { return &Service{DB: tx, inTransaction: true} }

func (s *Service) CommitRevision(ctx context.Context, req CommitRequest) (*RevisionView, error) {
	if s == nil || s.DB == nil {
		return nil, ErrDisabled
	}
	if strings.TrimSpace(req.OwnerUserID) == "" {
		return nil, ErrAccessDenied
	}
	if req.TenantID == "" {
		req.TenantID = req.OwnerUserID
	}
	if req.Kind == "" {
		req.Kind = KindFile
	}
	if req.Channel == "" {
		req.Channel = ChannelPublished
	}
	if req.ProducerType == "" {
		req.ProducerType = ProducerMainChat
	}
	now := time.Now().UTC()
	var view *RevisionView
	var err error
	transaction := common.ImmediateTransactionWithSQLiteBusyRetry
	if s.inTransaction {
		transaction = func(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error { return fn(db.WithContext(ctx)) }
	}
	for attempt := 0; attempt < 4; attempt++ {
		view = nil
		err = transaction(ctx, s.DB, func(tx *gorm.DB) error {
			// A row lock cannot protect an artifact that does not exist yet.
			// Serialize all writers for this logical identity, including existing
			// artifacts, before either the idempotency or artifact lookup. The
			// lock belongs to the caller's transaction and releases on rollback.
			if tx.Dialector.Name() == "postgres" && req.LogicalKey != "" {
				key, _ := json.Marshal([]string{"artifact/logical", req.TenantID, req.OwnerUserID, req.LogicalKey})
				if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", string(key)).Error; err != nil {
					return err
				}
			}
			if req.IdempotencyKey != "" {
				existing, err := InTransaction(tx).lookupIdempotency(ctx, req)
				if err != nil {
					return err
				}
				if existing != nil {
					view = existing
					return nil
				}
			}
			artifactID := strings.TrimSpace(req.ArtifactID)
			var art orm.ArtifactV2
			if artifactID != "" {
				err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("id = ? AND tenant_id = ? AND owner_user_id = ? AND deleted_at IS NULL", artifactID, req.TenantID, req.OwnerUserID).
					Take(&art).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if errors.Is(err, gorm.ErrRecordNotFound) {
					art = orm.ArtifactV2{
						ID: artifactID, TenantID: req.TenantID, OwnerUserID: req.OwnerUserID,
						Kind: req.Kind, Title: firstNonEmpty(req.Title, req.LogicalKey, artifactID),
						LogicalKey: req.LogicalKey, Status: "active", Classification: "internal",
						CreatedAt: now, UpdatedAt: now,
					}
					if err := tx.Create(&art).Error; err != nil {
						return err
					}
				}
			} else if req.LogicalKey != "" {
				err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("tenant_id = ? AND owner_user_id = ? AND logical_key = ? AND deleted_at IS NULL",
						req.TenantID, req.OwnerUserID, req.LogicalKey).
					Order("created_at ASC").Take(&art).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if errors.Is(err, gorm.ErrRecordNotFound) {
					art = orm.ArtifactV2{
						ID: uuid.NewString(), TenantID: req.TenantID, OwnerUserID: req.OwnerUserID,
						Kind: req.Kind, Title: firstNonEmpty(req.Title, req.LogicalKey),
						LogicalKey: req.LogicalKey, Status: "active", Classification: "internal",
						CreatedAt: now, UpdatedAt: now,
					}
					if err := tx.Create(&art).Error; err != nil {
						return err
					}
				}
			} else {
				art = orm.ArtifactV2{
					ID: uuid.NewString(), TenantID: req.TenantID, OwnerUserID: req.OwnerUserID,
					Kind: req.Kind, Title: firstNonEmpty(req.Title, "artifact"),
					LogicalKey: req.LogicalKey, Status: "active", Classification: "internal",
					CreatedAt: now, UpdatedAt: now,
				}
				if err := tx.Create(&art).Error; err != nil {
					return err
				}
			}

			var head orm.ArtifactHead
			headErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("artifact_id = ? AND channel = ?", art.ID, req.Channel).
				Take(&head).Error
			if headErr != nil && !errors.Is(headErr, gorm.ErrRecordNotFound) {
				return headErr
			}
			hasHead := headErr == nil
			if hasHead {
				if req.ExpectedHeadVer > 0 && head.Version != req.ExpectedHeadVer {
					return ErrRevisionConflict
				}
				if req.BaseRevisionID != "" && head.RevisionID != req.BaseRevisionID {
					return ErrRevisionConflict
				}
				if req.BaseRevisionID == "" {
					req.BaseRevisionID = head.RevisionID
				}
			}

			var lastNo int64
			_ = tx.Model(&orm.ArtifactRevision{}).Where("artifact_id = ?", art.ID).
				Select("COALESCE(MAX(revision_no), 0)").Scan(&lastNo).Error
			nextNo := lastNo + 1

			blobID, hash, size, inline := "", "", int64(0), json.RawMessage(nil)
			mime := req.MIMEType
			if strings.TrimSpace(req.BlobID) != "" {
				var stored orm.ArtifactBlob
				if err := tx.Where("id = ? AND tenant_id = ?", req.BlobID, req.TenantID).Take(&stored).Error; err != nil {
					return err
				}
				blobID = stored.ID
				hash = "sha256:" + stored.SHA256
				size = stored.Size
				mime = firstNonEmpty(mime, stored.MIMEType)
			} else if req.Content != nil {
				ref, err := PutBlob(req.TenantID, mime, bytes.NewReader(req.Content), "", int64(len(req.Content)))
				if err != nil {
					return err
				}
				blobID, err = persistBlob(tx, ref, now)
				if err != nil {
					return err
				}
				hash = "sha256:" + ref.SHA256
				size = ref.Size
			} else {
				inline = req.InlineJSON
				if len(inline) == 0 {
					inline = json.RawMessage(`{}`)
				}
				hash = contentHash(inline)
				size = int64(len(inline))
			}
			meta := map[string]any{}
			if len(req.Metadata) > 0 {
				_ = json.Unmarshal(req.Metadata, &meta)
			}
			meta["change_summary"] = req.ChangeSummary
			meta["filename"] = req.Title
			metadata, _ := json.Marshal(meta)
			rev := orm.ArtifactRevision{
				ID: uuid.NewString(), ArtifactID: art.ID, RevisionNo: nextNo,
				ParentRevisionID: req.BaseRevisionID, BlobID: blobID, InlineJSON: inline,
				ContentType: firstNonEmpty(req.ContentType, "application/octet-stream"),
				ContentHash: hash, Size: size, Caption: req.Caption, Metadata: metadata,
				ProducerType: req.ProducerType, ProducerID: req.ProducerID,
				ProducerRunID: req.ProducerRunID, ProducerEventID: req.ProducerEventID,
				CreatedBy: req.OwnerUserID, CreatedAt: now,
			}
			if err := tx.Create(&rev).Error; err != nil {
				return err
			}
			nextVersion := int64(1)
			if hasHead {
				nextVersion = head.Version + 1
				res := tx.Model(&orm.ArtifactHead{}).
					Where("artifact_id = ? AND channel = ? AND version = ?", art.ID, req.Channel, head.Version).
					Updates(map[string]any{"revision_id": rev.ID, "version": nextVersion, "updated_at": now})
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected != 1 {
					return ErrRevisionConflict
				}
			} else {
				if err := tx.Create(&orm.ArtifactHead{
					ArtifactID: art.ID, Channel: req.Channel, RevisionID: rev.ID,
					Version: nextVersion, UpdatedAt: now,
				}).Error; err != nil {
					return err
				}
			}
			if req.Channel == ChannelPublished {
				if err := upsertHead(tx, art.ID, ChannelCurrent, rev.ID, now); err != nil {
					return err
				}
			}
			for _, spec := range req.Bindings {
				row := orm.ArtifactBinding{
					ID: uuid.NewString(), ArtifactID: art.ID,
					RevisionID: firstNonEmpty(spec.RevisionID, rev.ID),
					ScopeType:  spec.ScopeType, ScopeID: spec.ScopeID, Role: spec.Role,
					SlotKey: spec.SlotKey, Validity: ValidityEffective,
					FollowHead: spec.FollowHead, CreatedAt: now,
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			}
			payload, _ := json.Marshal(map[string]any{
				"artifact_id": art.ID, "revision_id": rev.ID, "revision_no": rev.RevisionNo,
			})
			if err := tx.Create(&orm.ArtifactEventOutbox{
				ID: uuid.NewString(), EventType: "revision_committed", Payload: payload,
				Status: "pending", NextAttempt: now, CreatedAt: now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&orm.ArtifactV2{}).Where("id = ?", art.ID).
				Updates(map[string]any{"updated_at": now, "title": firstNonEmpty(req.Title, art.Title)}).Error; err != nil {
				return err
			}
			view = &RevisionView{
				ArtifactID: art.ID, RevisionID: rev.ID, RevisionNo: rev.RevisionNo,
				LogicalKey: art.LogicalKey, Title: firstNonEmpty(req.Title, art.Title),
				ContentType: rev.ContentType, MIMEType: mime, ContentHash: hash, Size: size,
				Caption: req.Caption, ChangeSummary: req.ChangeSummary,
				ProducerType: req.ProducerType, Channel: req.Channel, HeadVersion: nextVersion,
				InlineJSON: inline, DownloadHint: "revision_id", CreatedBy: req.OwnerUserID, CreatedAt: now,
			}
			if req.IdempotencyKey != "" {
				body, _ := json.Marshal(view)
				if err := tx.Create(&orm.ArtifactIdempotency{
					TenantID: req.TenantID, IdempotencyKey: req.IdempotencyKey,
					Operation: "commit_revision", RequestHash: hashRequest(req),
					ResponseJSON: body, CreatedAt: now,
				}).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			return view, nil
		}
		if s.inTransaction || !isUniqueConstraint(err) {
			return nil, err
		}
		if req.IdempotencyKey != "" {
			existing, lookErr := s.lookupIdempotency(ctx, req)
			if lookErr != nil {
				return nil, lookErr
			}
			if existing != nil {
				return existing, nil
			}
		}
	}
	return nil, err
}

func upsertHead(tx *gorm.DB, artifactID, channel, revisionID string, now time.Time) error {
	var head orm.ArtifactHead
	err := tx.Where("artifact_id = ? AND channel = ?", artifactID, channel).Take(&head).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Create(&orm.ArtifactHead{
			ArtifactID: artifactID, Channel: channel, RevisionID: revisionID, Version: 1, UpdatedAt: now,
		}).Error
	}
	if err != nil {
		return err
	}
	res := tx.Model(&orm.ArtifactHead{}).
		Where("artifact_id = ? AND channel = ? AND version = ?", artifactID, channel, head.Version).
		Updates(map[string]any{"revision_id": revisionID, "version": head.Version + 1, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrRevisionConflict
	}
	return nil
}

func (s *Service) lookupIdempotency(ctx context.Context, req CommitRequest) (*RevisionView, error) {
	var row orm.ArtifactIdempotency
	err := s.DB.WithContext(ctx).Where(
		"tenant_id = ? AND idempotency_key = ? AND operation = ?",
		req.TenantID, req.IdempotencyKey, "commit_revision",
	).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.RequestHash != hashRequest(req) {
		return nil, ErrIdempotencyConflict
	}
	var view RevisionView
	if json.Unmarshal(row.ResponseJSON, &view) != nil {
		return nil, ErrIdempotencyConflict
	}
	return &view, nil
}

func hashRequest(req CommitRequest) string {
	payload := append(append(append(append([]byte(req.LogicalKey), req.Content...), req.InlineJSON...), req.Metadata...), []byte(req.BlobID)...)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func (s *Service) GetRevision(ctx context.Context, ownerUserID, revisionID string) (*orm.ArtifactRevision, *orm.ArtifactV2, error) {
	var rev orm.ArtifactRevision
	if err := s.DB.WithContext(ctx).Where("id = ?", revisionID).Take(&rev).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	var art orm.ArtifactV2
	if err := s.DB.WithContext(ctx).Where("id = ?", rev.ArtifactID).Take(&art).Error; err != nil {
		return nil, nil, ErrNotFound
	}
	if art.DeletedAt != nil {
		return nil, nil, ErrNotFound
	}
	if art.OwnerUserID != ownerUserID {
		return nil, nil, ErrAccessDenied
	}
	return &rev, &art, nil
}

func (s *Service) ListRevisions(ctx context.Context, ownerUserID, artifactID string) ([]orm.ArtifactRevision, *orm.ArtifactV2, error) {
	var art orm.ArtifactV2
	if err := s.DB.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", artifactID, ownerUserID).Take(&art).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}
	var rows []orm.ArtifactRevision
	if err := s.DB.WithContext(ctx).Where("artifact_id = ?", artifactID).
		Order("revision_no ASC").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	return rows, &art, nil
}

func (s *Service) MoveHead(ctx context.Context, ownerUserID, artifactID, channel, revisionID string, expectedVersion int64) (*orm.ArtifactHead, error) {
	now := time.Now().UTC()
	var head orm.ArtifactHead
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rev, err := lockArtifactRevision(tx, ownerUserID, artifactID, revisionID)
		if err != nil {
			return err
		}
		moved, err := moveHeadLocked(tx, artifactID, channel, rev.ID, expectedVersion, now)
		if err != nil {
			return err
		}
		head = moved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &head, nil
}

// RestorePublished moves published and current in one transaction.
// Legacy conversation_artifacts rows stay unchanged so flag-off rollback
// keeps historical source-of-truth bytes. V2 projection overlays the head.
func (s *Service) RestorePublished(ctx context.Context, ownerUserID, artifactID, revisionID string, expectedVersion int64) (*orm.ArtifactHead, error) {
	if expectedVersion <= 0 {
		return nil, ErrRevisionConflict
	}
	now := time.Now().UTC()
	var published orm.ArtifactHead
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rev, err := lockArtifactRevision(tx, ownerUserID, artifactID, revisionID)
		if err != nil {
			return err
		}
		moved, err := moveHeadLocked(tx, artifactID, ChannelPublished, rev.ID, expectedVersion, now)
		if err != nil {
			return err
		}
		if _, err := moveHeadLocked(tx, artifactID, ChannelCurrent, rev.ID, 0, now); err != nil {
			return err
		}
		published = moved
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &published, nil
}

func lockArtifactRevision(tx *gorm.DB, ownerUserID, artifactID, revisionID string) (orm.ArtifactRevision, error) {
	var art orm.ArtifactV2
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", artifactID, ownerUserID).Take(&art).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return orm.ArtifactRevision{}, ErrNotFound
		}
		return orm.ArtifactRevision{}, err
	}
	var rev orm.ArtifactRevision
	if err := tx.Where("id = ? AND artifact_id = ?", revisionID, artifactID).Take(&rev).Error; err != nil {
		return orm.ArtifactRevision{}, ErrNotFound
	}
	return rev, nil
}

func moveHeadLocked(tx *gorm.DB, artifactID, channel, revisionID string, expectedVersion int64, now time.Time) (orm.ArtifactHead, error) {
	var head orm.ArtifactHead
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("artifact_id = ? AND channel = ?", artifactID, channel).Take(&head).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		head = orm.ArtifactHead{ArtifactID: artifactID, Channel: channel, RevisionID: revisionID, Version: 1, UpdatedAt: now}
		return head, tx.Create(&head).Error
	}
	if err != nil {
		return orm.ArtifactHead{}, err
	}
	if expectedVersion > 0 && head.Version != expectedVersion {
		return orm.ArtifactHead{}, ErrRevisionConflict
	}
	res := tx.Model(&orm.ArtifactHead{}).
		Where("artifact_id = ? AND channel = ? AND version = ?", artifactID, channel, head.Version).
		Updates(map[string]any{"revision_id": revisionID, "version": head.Version + 1, "updated_at": now})
	if res.Error != nil {
		return orm.ArtifactHead{}, res.Error
	}
	if res.RowsAffected != 1 {
		return orm.ArtifactHead{}, ErrRevisionConflict
	}
	head.RevisionID = revisionID
	head.Version++
	head.UpdatedAt = now
	return head, nil
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
}

func (s *Service) BindRevision(ctx context.Context, ownerUserID string, spec BindingSpec, artifactID string) error {
	var art orm.ArtifactV2
	if err := s.DB.WithContext(ctx).Where("id = ? AND owner_user_id = ?", artifactID, ownerUserID).Take(&art).Error; err != nil {
		return ErrNotFound
	}
	return s.DB.WithContext(ctx).Create(&orm.ArtifactBinding{
		ID: uuid.NewString(), ArtifactID: artifactID, RevisionID: spec.RevisionID,
		ScopeType: spec.ScopeType, ScopeID: spec.ScopeID, Role: spec.Role,
		SlotKey: spec.SlotKey, Validity: ValidityEffective, FollowHead: spec.FollowHead,
		CreatedAt: time.Now().UTC(),
	}).Error
}

func (s *Service) FindByLegacyID(ctx context.Context, legacyID string) (*orm.ArtifactBinding, error) {
	return s.FindByLegacyBinding(ctx, ScopeLegacyRow, legacyID)
}

func (s *Service) DropLegacyBindings(ctx context.Context, scopeType, scopeID string) error {
	if s == nil || s.DB == nil || strings.TrimSpace(scopeID) == "" {
		return nil
	}
	return s.DB.WithContext(ctx).Where("scope_type = ? AND scope_id = ?", scopeType, scopeID).
		Delete(&orm.ArtifactBinding{}).Error
}

func (s *Service) FindByLegacyBinding(ctx context.Context, scopeType, scopeID string) (*orm.ArtifactBinding, error) {
	var row orm.ArtifactBinding
	err := s.DB.WithContext(ctx).Where("scope_type = ? AND scope_id = ?", scopeType, scopeID).
		Order("created_at ASC").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &row, err
}

func (s *Service) FindLatestLegacyBinding(ctx context.Context, scopeType, scopeID string) (*orm.ArtifactBinding, error) {
	var row orm.ArtifactBinding
	err := s.DB.WithContext(ctx).
		Table("artifact_bindings AS b").
		Select("b.*").
		Joins("LEFT JOIN artifact_revisions AS r ON r.id = b.revision_id").
		Where("b.scope_type = ? AND b.scope_id = ?", scopeType, scopeID).
		Order("COALESCE(r.revision_no, 0) DESC, b.created_at DESC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &row, err
}

// PurgeConversationOwned hides V2 artifacts bound to purged conversations so
// later owner-scoped downloads cannot resurrect deleted session files.
func PurgeConversationOwned(tx *gorm.DB, ownerUserID string, conversationIDs []string) error {
	if tx == nil || strings.TrimSpace(ownerUserID) == "" || len(conversationIDs) == 0 {
		return nil
	}
	if !tx.Migrator().HasTable(&orm.ArtifactV2{}) {
		return nil
	}
	now := time.Now().UTC()
	idSet := map[string]struct{}{}
	var boundIDs []string
	if err := tx.Model(&orm.ArtifactBinding{}).
		Where("scope_type = ? AND scope_id IN ?", ScopeConversation, conversationIDs).
		Distinct("artifact_id").
		Pluck("artifact_id", &boundIDs).Error; err != nil {
		return err
	}
	for _, id := range boundIDs {
		idSet[id] = struct{}{}
	}
	for _, conversationID := range conversationIDs {
		var keyedIDs []string
		if err := tx.Model(&orm.ArtifactV2{}).
			Where("owner_user_id = ? AND deleted_at IS NULL AND logical_key LIKE ?",
				ownerUserID, ConversationScopedLogicalKey(conversationID, "")+"%").
			Pluck("id", &keyedIDs).Error; err != nil {
			return err
		}
		for _, id := range keyedIDs {
			idSet[id] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return nil
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	return tx.Model(&orm.ArtifactV2{}).
		Where("id IN ? AND owner_user_id = ? AND deleted_at IS NULL", ids, ownerUserID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now, "status": "deleted"}).Error
}

func (s *Service) Head(ctx context.Context, artifactID, channel string) (*orm.ArtifactHead, error) {
	var head orm.ArtifactHead
	err := s.DB.WithContext(ctx).Where("artifact_id = ? AND channel = ?", artifactID, channel).Take(&head).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &head, err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
