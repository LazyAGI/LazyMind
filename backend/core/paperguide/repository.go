package paperguide

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"strings"
	"time"
)

const JobType = "document.paper_guide"

var (
	ErrModelTimeout  = errors.New("paper_guide_model_timeout")
	ErrModelRequest  = errors.New("paper_guide_model_request_failed")
	ErrRegenerate    = errors.New("paper_guide_regeneration_required")
	ErrGeneration    = errors.New("paper_guide_generation_failed")
	ErrConflict      = errors.New("paper_guide_conflict")
	ErrModelChanged  = errors.New("paper_guide_model_unavailable")
	ErrSourceChanged = errors.New("paper_guide_source_changed")
	ErrPreparation   = errors.New("paper_guide_preparation_required")
	ErrForbidden     = errors.New("paper_guide_permission_revoked")
	ErrDisabled      = errors.New("paper_guide_disabled")
	ErrContext       = errors.New("paper_guide_context_too_long")
	ErrMaterial      = errors.New("paper_guide_material_insufficient")
	ErrEvidence      = errors.New("paper_guide_invalid_evidence")
	ErrQuality       = errors.New("paper_guide_review_required")
)

type Scope struct {
	OwnerID    string `json:"-"`
	DatasetID  string `json:"dataset_id"`
	DocumentID string `json:"document_id"`
	Language   string `json:"language"`
}

func (s Scope) Key() string {
	return digest([]string{s.OwnerID, s.DatasetID, s.DocumentID, s.Language})
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type ModelIdentity struct {
	Ref                 string `json:"ref"`
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Protocol            string `json:"protocol"`
	ParametersHash      string `json:"parameters_hash"`
	ContextTokens       int    `json:"context_tokens"`
	OutputReserveTokens int    `json:"output_reserve_tokens,omitempty"`
}
type CreateRevision struct {
	Scope                                                              Scope
	SourceSHA256, IRDigest, SnapshotPath, IdempotencyKey, DependencyID string
	Model                                                              ModelIdentity
	Regenerate                                                         bool
}
type Repository struct {
	DB                *gorm.DB
	PrepareCheckpoint func(context.Context, orm.PaperGuide) (context.Context, error)
	CheckpointGuard   func(context.Context, *gorm.DB, orm.PaperGuide) error
}
type JobPayload struct {
	RevisionID string `json:"revision_id"`
	Epoch      int64  `json:"epoch"`
}

// Contract identity is independent of PDF/IR identity. A software upgrade must
// never make an old paid checkpoint appear to have used the new task contract.
func contractFingerprint(sourceHash, irDigest string, model ModelIdentity) string {
	return digest([]any{sourceHash, irDigest, SourceCatalogVersion, TaskVersion, model})
}
func (r Repository) CheckContract(ctx context.Context, rev orm.PaperGuideRevision) error {
	var model ModelIdentity
	if json.Unmarshal([]byte(rev.ModelJSON), &model) != nil {
		return ErrRegenerate
	}
	if rev.Fingerprint == contractFingerprint(rev.SourceSHA256, rev.IRDigest, model) {
		return nil
	}
	// A source dependency without any model attempt has no old generated contract
	// to preserve. It adopts the current versions when its first snapshot freezes.
	if rev.SnapshotPath == "" && rev.IRDigest == "" && rev.CallsUsed == 0 {
		var count int64
		if err := r.DB.WithContext(ctx).Model(&orm.PaperGuideStep{}).Where("revision_id = ?", rev.ID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
	}
	return ErrRegenerate
}

func lock(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() == "postgres" {
		return db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	return db
}
func (r Repository) Guide(ctx context.Context, s Scope) (orm.PaperGuide, error) {
	var g orm.PaperGuide
	e := r.DB.WithContext(ctx).Where("scope_key = ? AND deleted = ?", s.Key(), false).Take(&g).Error
	return g, e
}
func (r Repository) Revision(ctx context.Context, s Scope, id string) (orm.PaperGuideRevision, error) {
	var rev orm.PaperGuideRevision
	g, e := r.Guide(ctx, s)
	if e != nil {
		return rev, e
	}
	e = r.DB.WithContext(ctx).Where("id = ? AND guide_id = ?", id, g.ID).Take(&rev).Error
	return rev, e
}
func (r Repository) Create(ctx context.Context, in CreateRevision) (out orm.PaperGuideRevision, err error) {
	err = domainTransaction(ctx, r.DB, func(tx *gorm.DB) error {
		heldJob, e := lockActiveExecution(tx, in.Scope.Key())
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		g := orm.PaperGuide{ID: uuid.NewString(), ScopeKey: in.Scope.Key(), OwnerID: in.Scope.OwnerID, DatasetID: in.Scope.DatasetID, DocumentID: in.Scope.DocumentID, Language: in.Scope.Language, CreatedAt: now, UpdatedAt: now}
		if e := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope_key"}}, DoNothing: true}).Create(&g).Error; e != nil {
			return e
		}
		g = orm.PaperGuide{}
		if e := lock(tx).Where("scope_key = ?", in.Scope.Key()).Take(&g).Error; e != nil {
			return e
		}
		if g.Deleted {
			return ErrForbidden
		}
		if in.IdempotencyKey != "" {
			var existing orm.PaperGuideRevision
			e := tx.Where("guide_id = ? AND idempotency_key = ?", g.ID, in.IdempotencyKey).Take(&existing).Error
			if e == nil {
				out = existing
				return nil
			}
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
		}
		if !in.Regenerate {
			for _, id := range []string{g.CandidateRevisionID, g.PublishedRevisionID} {
				if id == "" {
					continue
				}
				var existing orm.PaperGuideRevision
				if e := tx.Where("id = ?", id).Take(&existing).Error; e != nil {
					return e
				}
				if existing.SourceSHA256 == in.SourceSHA256 && (in.IRDigest == "" || existing.IRDigest == in.IRDigest) {
					out = existing
					return nil
				}
			}
		}
		// Cancel the old lease before changing its epoch, in the same transaction.
		if g.CandidateRevisionID != "" {
			var candidate orm.PaperGuideRevision
			if e := tx.Where("id = ?", g.CandidateRevisionID).Take(&candidate).Error; e != nil {
				return e
			}
			if candidate.JobID != heldJob {
				return ErrConflict
			}
			if e := cancelRevision(tx, g.CandidateRevisionID, "canceled"); e != nil {
				return e
			}
		}
		g.Epoch++
		g.ChangeVersion++
		model, _ := json.Marshal(in.Model)
		stage := "queued"
		if in.SnapshotPath == "" {
			stage = "waiting_source"
		}
		out = orm.PaperGuideRevision{ID: uuid.NewString(), GuideID: g.ID, Epoch: g.Epoch, IdempotencyKey: in.IdempotencyKey, SourceSHA256: in.SourceSHA256, IRDigest: in.IRDigest, SnapshotPath: in.SnapshotPath, ModelJSON: string(model), Fingerprint: contractFingerprint(in.SourceSHA256, in.IRDigest, in.Model), Status: "draft", Stage: stage, DependencyID: in.DependencyID, CreatedAt: now, UpdatedAt: now}
		if e := tx.Create(&out).Error; e != nil {
			return e
		}
		if e := enqueue(ctx, tx, g, &out); e != nil {
			return e
		}
		return tx.Model(&g).Updates(map[string]any{"candidate_revision_id": out.ID, "epoch": g.Epoch, "change_version": g.ChangeVersion}).Error
	})
	return
}
func enqueue(ctx context.Context, tx *gorm.DB, g orm.PaperGuide, rev *orm.PaperGuideRevision) error {
	job, e := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: JobType, ResourceType: "paper_guide_user", ResourceID: g.OwnerID, IdempotencyKey: rev.ID + ":" + stringInt(rev.Epoch), Payload: JobPayload{rev.ID, rev.Epoch}, MaxAttempts: 4, CreateUserID: g.OwnerID})
	if e != nil {
		return e
	}
	rev.JobID = job.ID
	return tx.Model(rev).Update("job_id", job.ID).Error
}
func cancelRevision(tx *gorm.DB, id, stage string) error {
	var rev orm.PaperGuideRevision
	if e := tx.Where("id = ?", id).Take(&rev).Error; e != nil {
		return e
	}
	if e := tx.Model(&orm.AsyncJob{}).Where("id = ? AND status IN ?", rev.JobID, []string{"pending", "running"}).Updates(map[string]any{"status": "canceled", "locked_by": "", "lock_until": nil, "updated_at": time.Now().UTC()}).Error; e != nil {
		return e
	}
	return tx.Model(&rev).Updates(map[string]any{"epoch": gorm.Expr("epoch + 1"), "stage": stage, "change_version": gorm.Expr("change_version + 1")}).Error
}
func (r Repository) Cancel(ctx context.Context, s Scope, id string) error {
	return domainTransaction(ctx, r.DB, func(tx *gorm.DB) error {
		heldJob, err := lockActiveExecution(tx, s.Key())
		if err != nil {
			return err
		}
		rr := Repository{DB: tx, CheckpointGuard: r.CheckpointGuard}
		rev, e := rr.Revision(ctx, s, id)
		if e != nil {
			return e
		}
		if rev.JobID != heldJob {
			return ErrConflict
		}
		if rev.Stage == "completed" {
			return ErrConflict
		}
		if e = cancelRevision(tx, id, "canceled"); e != nil {
			return e
		}
		return tx.Model(&orm.PaperGuide{}).Where("id = ?", rev.GuideID).Updates(map[string]any{"epoch": gorm.Expr("epoch + 1"), "change_version": gorm.Expr("change_version + 1")}).Error
	})
}
func (r Repository) Resume(ctx context.Context, s Scope, id string, m ModelIdentity) (out orm.PaperGuideRevision, err error) {
	err = domainTransaction(ctx, r.DB, func(tx *gorm.DB) error {
		heldJob, err := lockActiveExecution(tx, s.Key())
		if err != nil {
			return err
		}
		rr := Repository{DB: tx, CheckpointGuard: r.CheckpointGuard}
		rev, e := rr.Revision(ctx, s, id)
		if e != nil {
			return e
		}
		if rev.JobID != heldJob {
			return ErrConflict
		}
		if e = rr.CheckContract(ctx, rev); e != nil {
			return e
		}
		var old ModelIdentity
		if json.Unmarshal([]byte(rev.ModelJSON), &old) != nil || old != m {
			return ErrModelChanged
		}
		var g orm.PaperGuide
		if e = lock(tx).Where("id = ?", rev.GuideID).Take(&g).Error; e != nil {
			return e
		}
		if g.CandidateRevisionID != id {
			return ErrConflict
		}
		if rev.Stage != "paused" && rev.Stage != "failed" && rev.Stage != "canceled" {
			var job orm.AsyncJob
			if tx.Where("id = ?", rev.JobID).Take(&job).Error != nil || job.Status != "failed" {
				return ErrConflict
			}
		}
		if rev.CallsUsed >= MaxCalls || rev.ActiveMillis >= MaxActiveMillis {
			return ErrBudget
		}
		g.Epoch++
		rev.Epoch = g.Epoch
		rev.Stage = "queued"
		rev.ErrorCode = ""
		if e = tx.Save(&rev).Error; e != nil {
			return e
		}
		if e = enqueue(ctx, tx, g, &rev); e != nil {
			return e
		}
		out = rev
		return tx.Model(&g).Updates(map[string]any{"epoch": g.Epoch, "change_version": gorm.Expr("change_version + 1")}).Error
	})
	return
}

// Checkpoints lock the lease first, then domain rows. Creation/cancel use no
// network calls inside their transactions; a conflicting operation is retried.
func (r Repository) fenced(ctx context.Context, f asyncjob.FencedReporter, id string, epoch int64, fn func(*gorm.DB, *orm.PaperGuideRevision, *orm.PaperGuide) error) error {
	// Membership/identity resolution must finish before acquiring the job lease.
	// The guard below rechecks current local rows under the transaction locks.
	if r.PrepareCheckpoint != nil {
		var guide orm.PaperGuide
		err := r.DB.WithContext(ctx).Where("id IN (?)", r.DB.Model(&orm.PaperGuideRevision{}).Select("guide_id").Where("id = ?", id)).Take(&guide).Error
		if err != nil {
			return err
		}
		ctx, err = r.PrepareCheckpoint(ctx, guide)
		if err != nil {
			return err
		}
	}
	return f.WithLease(ctx, func(tx *gorm.DB) error {
		var rev orm.PaperGuideRevision
		if e := tx.Where("id = ?", id).Take(&rev).Error; e != nil {
			return e
		}
		var g orm.PaperGuide
		if e := lock(tx).Where("id = ?", rev.GuideID).Take(&g).Error; e != nil {
			return e
		}
		if e := lock(tx).Where("id = ?", id).Take(&rev).Error; e != nil {
			return e
		}
		if g.Deleted || g.CandidateRevisionID != id || g.Epoch != epoch || rev.Epoch != epoch || rev.Stage == "canceled" || rev.Stage == "source_changed" || rev.Stage == "completed" {
			return ErrConflict
		}
		if r.CheckpointGuard != nil {
			if e := r.CheckpointGuard(ctx, tx, g); e != nil {
				return e
			}
		}
		return fn(tx, &rev, &g)
	})
}

type StepWrite struct {
	Key, InputHash string
	Output, Review json.RawMessage
	Published      bool
	ActiveMillis   int64
}

func (r Repository) PutStep(ctx context.Context, f asyncjob.FencedReporter, id string, epoch int64, w StepWrite) error {
	return r.fenced(ctx, f, id, epoch, func(tx *gorm.DB, rev *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		var old orm.PaperGuideStep
		e := tx.Where("revision_id = ? AND step_key = ?", id, w.Key).Take(&old).Error
		if e == nil {
			if old.InputHash == w.InputHash && old.OutputJSON == string(w.Output) && old.ReviewJSON == string(w.Review) && old.Published == w.Published {
				return nil
			}
			return ErrConflict
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		rev.ChangeVersion++
		step := orm.PaperGuideStep{ID: uuid.NewString(), RevisionID: id, StepKey: w.Key, InputHash: w.InputHash, OutputJSON: string(w.Output), ReviewJSON: string(w.Review), Epoch: epoch, Version: rev.ChangeVersion, Published: w.Published, CreatedAt: time.Now().UTC()}
		if e = tx.Create(&step).Error; e != nil {
			return e
		}
		updates := map[string]any{"change_version": rev.ChangeVersion, "active_millis": gorm.Expr("active_millis + ?", w.ActiveMillis)}
		if w.Published {
			updates["status"] = "partial"
		}
		if e = tx.Model(rev).Updates(updates).Error; e != nil {
			return e
		}
		return tx.Model(g).Update("change_version", gorm.Expr("change_version + 1")).Error
	})
}
func (r Repository) Steps(ctx context.Context, id string) ([]orm.PaperGuideStep, error) {
	out := []orm.PaperGuideStep{}
	e := r.DB.WithContext(ctx).Where("revision_id = ?", id).Order("version ASC").Find(&out).Error
	return out, e
}
func (r Repository) Transition(ctx context.Context, f asyncjob.FencedReporter, id string, epoch int64, stage, code string, finalStatus ...string) error {
	return r.fenced(ctx, f, id, epoch, func(tx *gorm.DB, rev *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		updates := map[string]any{"stage": stage, "error_code": code, "change_version": gorm.Expr("change_version + 1")}
		if stage == "source_changed" {
			updates["status"] = "source_changed"
		}
		if stage == "completed" {
			updates["status"] = "complete"
			if len(finalStatus) == 1 && finalStatus[0] == "limited" {
				updates["status"] = "limited"
			}
			if g.PublishedRevisionID != "" {
				if e := tx.Model(&orm.PaperGuideRevision{}).Where("id = ?", g.PublishedRevisionID).Update("status", "superseded").Error; e != nil {
					return e
				}
			}
			g.PublishedRevisionID = id
			g.CandidateRevisionID = ""
		}
		if e := tx.Model(rev).Updates(updates).Error; e != nil {
			return e
		}
		return tx.Model(g).Updates(map[string]any{"published_revision_id": g.PublishedRevisionID, "candidate_revision_id": g.CandidateRevisionID, "change_version": gorm.Expr("change_version + 1")}).Error
	})
}

// InvalidateDocumentTx is called from the document deletion transaction. It
// cancels only this feature's jobs and tombstones all personal guide scopes.
// Published history is retained for the existing artifact retention policy.
func InvalidateDocumentTx(ctx context.Context, tx *gorm.DB, dataset, document string) error {
	if !tx.Migrator().HasTable(&orm.PaperGuide{}) {
		return nil
	}
	var guides []orm.PaperGuide
	if e := tx.WithContext(ctx).Where("dataset_id = ? AND document_id = ? AND deleted = ?", dataset, document, false).Find(&guides).Error; e != nil {
		return e
	}
	for _, g := range guides {
		if g.CandidateRevisionID != "" {
			if e := cancelRevision(tx, g.CandidateRevisionID, "canceled"); e != nil {
				return e
			}
		}
		if e := tx.Model(&g).Updates(map[string]any{"deleted": true, "epoch": gorm.Expr("epoch + 1"), "change_version": gorm.Expr("change_version + 1")}).Error; e != nil {
			return e
		}
	}
	return nil
}

// Match the reporter's order: execution lease first, domain pointers second.
// A concurrent replacement can change the pointer between the initial read
// and lock; retry that transaction instead of acquiring locks in reverse order.
func lockActiveExecution(tx *gorm.DB, scopeKey string) (string, error) {
	var guide orm.PaperGuide
	err := tx.Where("scope_key = ?", scopeKey).Take(&guide).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if guide.CandidateRevisionID == "" {
		return "", nil
	}
	var revision orm.PaperGuideRevision
	if err = tx.Where("id = ?", guide.CandidateRevisionID).Take(&revision).Error; err != nil {
		return "", err
	}
	var job orm.AsyncJob
	if err = lock(tx).Where("id = ?", revision.JobID).Take(&job).Error; err != nil {
		return "", err
	}
	return job.ID, nil
}
func domainTransaction(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		err = common.TransactionWithSQLiteBusyRetry(ctx, db, fn)
		if err == nil {
			return nil
		}
		message := strings.ToLower(err.Error())
		if !errors.Is(err, ErrConflict) && !strings.Contains(message, "40p01") && !strings.Contains(message, "40001") {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return err
}
