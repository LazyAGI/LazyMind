package paperguide

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"io"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	applog "lazymind/core/log"
	"strings"
	"time"
)

type execution struct {
	started          time.Time
	callTime         int64
	overheadReserved bool
	s                Service
	scope            Scope
	rev              orm.PaperGuideRevision
	model            ModelIdentity
	snap             GuideSourceSnapshot
	f                asyncjob.FencedReporter
	steps            map[string]orm.PaperGuideStep
	excerpts         []sourceExcerpt
}

func (s Service) Run(ctx context.Context, job asyncjob.Job, reporter asyncjob.Reporter) (asyncjob.Result, error) {
	var payload JobPayload
	if json.Unmarshal(job.PayloadJSON, &payload) != nil {
		return asyncjob.Result{Permanent: true}, ErrConflict
	}
	f, ok := reporter.(asyncjob.FencedReporter)
	if !ok {
		return asyncjob.Result{Permanent: true}, ErrConflict
	}
	var rev orm.PaperGuideRevision
	if e := s.Repo.DB.WithContext(ctx).Where("id = ?", payload.RevisionID).Take(&rev).Error; e != nil {
		return asyncjob.Result{Permanent: true}, e
	}
	if rev.Stage == "completed" {
		return asyncjob.Result{}, nil
	}
	if rev.JobID != job.ID || rev.Epoch != payload.Epoch {
		return asyncjob.Result{Permanent: true}, ErrConflict
	}
	var guide orm.PaperGuide
	if e := s.Repo.DB.WithContext(ctx).Where("id = ?", rev.GuideID).Take(&guide).Error; e != nil {
		return asyncjob.Result{Permanent: true}, e
	}
	scope := Scope{guide.OwnerID, guide.DatasetID, guide.DocumentID, guide.Language}
	x := execution{s: s, scope: scope, rev: rev, f: f, steps: map[string]orm.PaperGuideStep{}}
	if json.Unmarshal([]byte(rev.ModelJSON), &x.model) != nil {
		return asyncjob.Result{Permanent: true}, ErrModelChanged
	}
	if err := s.Repo.CheckContract(ctx, rev); err != nil {
		_ = s.Repo.Transition(ctx, f, rev.ID, rev.Epoch, "paused", errorCode(err))
		return asyncjob.Result{Permanent: true, ErrorCode: errorCode(err)}, safeRunError(err)
	}
	remaining := time.Duration(MaxActiveMillis-rev.ActiveMillis) * time.Millisecond
	if remaining <= 0 {
		return asyncjob.Result{Permanent: true, ErrorCode: ErrBudget.Error()}, ErrBudget
	}
	runCtx, cancelRun := context.WithTimeout(ctx, remaining)
	defer cancelRun()
	x.started = time.Now()
	err := s.Repo.fenced(runCtx, f, rev.ID, rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		if r.ActiveMillis+localProcessingReserveMillis > MaxActiveMillis {
			return ErrBudget
		}
		return tx.Model(r).Update("active_millis", gorm.Expr("active_millis + ?", localProcessingReserveMillis)).Error
	})
	if err == nil {
		x.overheadReserved = true
		err = x.executeFast(runCtx)
	}
	if err != nil {
		_ = x.settleOverhead(runCtx)
		// A checkpoint query can report an expired run as a permission error.
		// Classify from the run context before choosing a stage or retry.
		if errors.Is(contextErrorAt(runCtx, time.Now()), context.DeadlineExceeded) {
			err = ErrBudget
		}
	}

	var waiting *dependencyWaitError
	if errors.As(err, &waiting) {
		return asyncjob.Result{RetryAt: time.Now().Add(15 * time.Second), DeferAttempt: true, ErrorCode: ErrPreparation.Error()}, err
	}
	if err != nil {
		stage := "paused"
		if errors.Is(err, ErrSourceChanged) {
			stage = "source_changed"
		}
		if errors.Is(err, ErrForbidden) {
			stage = "failed"
		}
		code := errorCode(err)
		_ = s.Repo.Transition(ctx, f, rev.ID, rev.Epoch, stage, code)
		if err.Error() == code {
			return asyncjob.Result{Permanent: true, ErrorCode: code}, err
		}
		return asyncjob.Result{Permanent: true, ErrorCode: code}, safeRunError(err)
	}
	return asyncjob.Result{ResultJSON: mustMarshal(map[string]any{"revision_id": rev.ID})}, nil
}
func errorCode(e error) string { return safeRunError(e).Error() }
func safeRunError(e error) error {
	for _, known := range []error{ErrModelTimeout, ErrModelRequest, ErrRegenerate, ErrConflict, ErrModelChanged, ErrSourceChanged, ErrPreparation, ErrPrecision, ErrForbidden, ErrDisabled, ErrContext, ErrMaterial, ErrEvidence, ErrQuality, ErrBudget} {
		if errors.Is(e, known) {
			return known
		}
	}
	return ErrGeneration
}

func mustMarshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (x *execution) check(ctx context.Context) (map[string]any, error) {
	if !x.started.IsZero() && time.Since(x.started).Milliseconds()-x.callTime >= localProcessingReserveMillis {
		return nil, ErrBudget
	}
	if e := x.s.Repo.CheckContract(ctx, x.rev); e != nil {
		return nil, e
	}
	if e := x.s.Authorize(ctx, x.scope, true); e != nil {
		return nil, e
	}
	state, e := x.s.Source.Load(ctx, x.scope, false)
	if e != nil {
		return nil, e
	}
	if state.Hash != x.rev.SourceSHA256 {
		return nil, ErrSourceChanged
	}
	if x.rev.IRDigest != "" && (state.Snapshot == nil || state.Snapshot.IRDigest != x.rev.IRDigest) {
		return nil, ErrSourceChanged
	}
	if state.Snapshot != nil && state.Snapshot.CatalogVersion != SourceCatalogVersion {
		return nil, ErrRegenerate
	}
	m, cfg, e := x.s.ResolveModel(ctx, x.scope, x.model.Ref)
	if e != nil || m != x.model {
		return nil, ErrModelChanged
	}
	return cfg, nil
}
func (x *execution) prepare(ctx context.Context) error {
	if _, e := x.check(ctx); e != nil {
		return e
	}
	if x.rev.SnapshotPath == "" {
		state, e := x.s.Source.Load(ctx, x.scope, false)
		if e != nil {
			return e
		}
		if state.Snapshot == nil {
			if state.DependencyFailed {
				if state.PrecisionUnavailable {
					return ErrPrecision
				}
				return ErrPreparation
			}
			e = x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
				if r.WaitCount >= 120 {
					return ErrPreparation
				}
				return tx.Model(r).Updates(map[string]any{"wait_count": gorm.Expr("wait_count + 1"), "stage": "waiting_source"}).Error
			})
			if e != nil {
				return e
			}
			return &dependencyWaitError{}
		}
		// The dependency is ready. Freeze its immutable file outside the
		// checkpoint transaction; do not re-enqueue a missing parser here.
		state, e = x.s.Source.Load(ctx, x.scope, true)
		if e != nil {
			return e
		}
		if state.Snapshot == nil {
			return ErrPreparation
		}
		if state.Hash != x.rev.SourceSHA256 {
			return ErrSourceChanged
		}
		if !state.Snapshot.Coverage.hasGenerationMaterial() {
			return ErrMaterial
		}
		e = x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
			return tx.Model(r).Updates(map[string]any{"snapshot_path": state.Path, "ir_digest": state.Snapshot.IRDigest, "fingerprint": contractFingerprint(state.Hash, state.Snapshot.IRDigest, x.model)}).Error
		})
		if e != nil {
			return e
		}
		x.rev.SnapshotPath = state.Path
		x.rev.IRDigest = state.Snapshot.IRDigest
		x.rev.Fingerprint = contractFingerprint(state.Hash, state.Snapshot.IRDigest, x.model)
	}
	snap, e := x.s.Source.Read(ctx, x.rev.SnapshotPath)
	if e != nil {
		return e
	}
	if e = ValidateSnapshot(snap, SourceIdentity{x.rev.SourceSHA256, x.scope.DatasetID, x.scope.DocumentID, x.scope.OwnerID}, x.rev.IRDigest); e != nil {
		return ErrEvidence
	}
	if snap.CatalogVersion != SourceCatalogVersion {
		return ErrRegenerate
	}
	x.snap = snap
	steps, e := x.s.Repo.Steps(ctx, x.rev.ID)
	if e != nil {
		return e
	}
	for _, step := range steps {
		x.steps[step.StepKey] = step
	}
	return nil
}

func sectionKey(role Role, version int) string {
	return "section:" + string(role) + ":v" + stringInt(int64(version))
}

func (x *execution) inputFits(input map[string]any) bool {
	raw, err := json.MarshalIndent(input, "", "")
	if err != nil {
		return false
	}
	return len(raw) <= 1<<20 && len(raw)+6000 <= x.modelInputBudget()
}

func (x *execution) modelInputBudget() int {
	limit := x.model.ContextTokens
	if limit <= 0 {
		limit = 32768
	}
	budget := limit / 10 * 7
	if x.model.OutputReserveTokens > 0 {
		budget = min(budget, limit-x.model.OutputReserveTokens)
	}
	return budget
}

func unique(ids []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func requestHashes(tx *gorm.DB, revisionID, key string) ([]string, error) {
	var hashes []string
	prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace("request:" + key + ":")
	err := tx.Model(&orm.PaperGuideStep{}).Where("revision_id = ? AND step_key LIKE ? ESCAPE '!'", revisionID, prefix+"%").
		Distinct("input_hash").Pluck("input_hash", &hashes).Error
	return hashes, err
}

func (x *execution) task(ctx context.Context, key, task string, input map[string]any, out any, validate func() error) error {
	if !x.inputFits(input) {
		return ErrContext
	}
	hash := digest([]any{TaskVersion, input})
	if prior, ok := x.steps[key]; ok {
		if prior.InputHash != hash {
			return ErrConflict
		}
		if e := decodeStrict([]byte(prior.OutputJSON), out); e != nil {
			return ErrEvidence
		}
		return validate()
	}
	attemptTimeout := ModelAttemptTimeout
	attemptMillis := attemptTimeout.Milliseconds()
	cfg, e := x.check(ctx)
	if e != nil {
		return e
	}
	started := time.Now()
	ticket := "request:" + key + ":" + uuid.NewString()
	e = x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		hashes, e := requestHashes(tx, r.ID, key)
		if e != nil {
			return e
		}
		for _, seen := range hashes {
			if seen != hash {
				return ErrConflict
			}
		}
		recovery := len(hashes) > 0
		if r.CallsUsed >= MaxCalls || r.ActiveMillis+attemptMillis > MaxActiveMillis || recovery && r.RecoveryCalls >= 2 || !recovery && r.PlannedCalls >= 14 {
			return ErrBudget
		}
		updates := map[string]any{"calls_used": gorm.Expr("calls_used + 1"), "active_millis": gorm.Expr("active_millis + ?", attemptMillis)}
		if !recovery {
			updates["planned_calls"] = gorm.Expr("planned_calls + 1")
		}
		if recovery {
			updates["recovery_calls"] = gorm.Expr("recovery_calls + 1")
		}
		if e := tx.Model(r).Updates(updates).Error; e != nil {
			return e
		}
		return tx.Create(&orm.PaperGuideStep{ID: uuid.NewString(), RevisionID: r.ID, StepKey: ticket, InputHash: hash, Epoch: r.Epoch, CreatedAt: started.UTC()}).Error
	})
	if e != nil {
		return e
	}
	callCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
	raw, callErr := x.s.Call(callCtx, task, input, cfg)
	// Observe both contexts before our cleanup cancel. Parent stops take
	// precedence, and a returned result can race the timer's cancellation.
	finished := time.Now()
	parentErr, callContextErr := contextErrorAt(ctx, finished), contextErrorAt(callCtx, finished)
	diagnostic, callErr := classifyModelCall(parentErr, callContextErr, callErr)
	cancel()
	observedMillis := finished.Sub(started).Milliseconds()
	x.callTime += observedMillis
	if parentErr != nil {
		return callErr
	}
	elapsed := min(observedMillis, attemptMillis)
	if errors.Is(callContextErr, context.DeadlineExceeded) {
		elapsed = attemptMillis
	}
	if callErr == nil {
		if err := decodeStrict(raw, out); err != nil {
			diagnostic, callErr = "schema", ErrEvidence
		} else if err = validate(); err != nil {
			diagnostic, callErr = "contract", ErrEvidence
		}
	}
	// Settle only this invocation, once. Unknown crashed requests retain
	// the full attempt reservation; diagnostic metadata never includes raw
	// provider errors, model config or private source text.
	e = x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		marker := tx.Model(&orm.PaperGuideStep{}).Where("revision_id = ? AND step_key = ? AND output_json = ?", r.ID, ticket, "").Update("output_json", string(mustMarshal(map[string]any{"task": task, "error_class": diagnostic, "elapsed_ms": elapsed})))
		if marker.Error != nil {
			return marker.Error
		}
		if marker.RowsAffected != 1 {
			return ErrConflict
		}
		return tx.Model(r).Update("active_millis", gorm.Expr("active_millis - ?", attemptMillis-elapsed)).Error
	})
	if e != nil {
		return e
	}
	if callErr != nil {
		applog.Logger.Warn().Str("job_id", x.rev.JobID).Str("revision_id", x.rev.ID).
			Str("step", key).Str("attempt", ticket).Str("error_class", diagnostic).
			Int64("elapsed_ms", elapsed).Msg("Paper guide model step failed")
	}
	if _, e = x.check(ctx); e != nil {
		return e
	}

	if callErr != nil {
		return callErr
	}
	e = x.s.Repo.PutStep(ctx, x.f, x.rev.ID, x.rev.Epoch, StepWrite{Key: key, InputHash: hash, Output: raw})
	if e != nil {
		return e
	}
	x.steps[key] = orm.PaperGuideStep{StepKey: key, InputHash: hash, OutputJSON: string(raw)}
	return nil
}
func decodeStrict(raw []byte, out any) error {
	if len(raw) > 512<<10 {
		return ErrEvidence
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrEvidence
	}
	return nil
}

type dependencyWaitError struct{}

func (*dependencyWaitError) Error() string { return "paper_guide_preparation_required" }
func (*dependencyWaitError) Unwrap() error { return ErrPreparation }

// Non-model work (source loading, ACL/model resolution, validation and DB
// checkpoints) has its own conservative two-minute reservation per execution.
// It is settled against observed active wall time, excluding model time already
// charged above. Interrupted executions retain the reservation, never queue wait.
func (x *execution) settleOverhead(ctx context.Context) error {
	if !x.overheadReserved {
		return nil
	}
	elapsed := time.Since(x.started).Milliseconds() - x.callTime
	if elapsed < 0 {
		elapsed = 0
	}
	err := x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		return tx.Model(r).Update("active_millis", gorm.Expr("active_millis - ?", localProcessingReserveMillis-elapsed)).Error
	})
	if err == nil {
		x.overheadReserved = false
	}
	return err
}
