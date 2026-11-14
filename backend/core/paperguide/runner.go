package paperguide

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"io"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"strings"
	"time"
)

const callMillis = int64(120000)

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
	remaining := time.Duration(MaxActiveMillis-rev.ActiveMillis) * time.Millisecond
	if remaining <= 0 {
		return asyncjob.Result{Permanent: true, ErrorCode: ErrBudget.Error()}, ErrBudget
	}
	runCtx, cancelRun := context.WithTimeout(ctx, remaining)
	defer cancelRun()
	x.started = time.Now()
	err := s.Repo.fenced(runCtx, f, rev.ID, rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		if r.ActiveMillis+callMillis > MaxActiveMillis {
			return ErrBudget
		}
		return tx.Model(r).Update("active_millis", gorm.Expr("active_millis + ?", callMillis)).Error
	})
	if err == nil {
		x.overheadReserved = true
		err = x.execute(runCtx)
	}
	if err != nil {
		_ = x.settleOverhead(runCtx)
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
	for _, known := range []error{ErrConflict, ErrModelChanged, ErrSourceChanged, ErrPreparation, ErrForbidden, ErrDisabled, ErrContext, ErrMaterial, ErrEvidence, ErrQuality, ErrBudget} {
		if errors.Is(e, known) {
			return known
		}
	}
	return ErrGeneration
}

func mustMarshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (x *execution) check(ctx context.Context) (map[string]any, error) {
	if !x.started.IsZero() && time.Since(x.started).Milliseconds()-x.callTime >= callMillis {
		return nil, ErrBudget
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
	m, cfg, e := x.s.ResolveModel(ctx, x.scope, x.model.Ref)
	if e != nil || m != x.model {
		return nil, ErrModelChanged
	}
	return cfg, nil
}
func (x *execution) execute(ctx context.Context) error {
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
		if !state.Snapshot.Coverage.Body || state.Snapshot.Coverage.AbstractOnly {
			return ErrMaterial
		}
		e = x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
			return tx.Model(r).Updates(map[string]any{"snapshot_path": state.Path, "ir_digest": state.Snapshot.IRDigest, "fingerprint": digest([]any{state.Hash, state.Snapshot.IRDigest, SchemaVersion, x.model})}).Error
		})
		if e != nil {
			return e
		}
		x.rev.SnapshotPath = state.Path
		x.rev.IRDigest = state.Snapshot.IRDigest
	}
	snap, e := x.s.Source.Read(ctx, x.rev.SnapshotPath)
	if e != nil {
		return e
	}
	if e = ValidateSnapshot(snap, SourceIdentity{x.rev.SourceSHA256, x.scope.DatasetID, x.scope.DocumentID, x.scope.OwnerID}, x.rev.IRDigest); e != nil {
		return ErrEvidence
	}
	x.snap = snap
	steps, e := x.s.Repo.Steps(ctx, x.rev.ID)
	if e != nil {
		return e
	}
	for _, step := range steps {
		x.steps[step.StepKey] = step
	}
	if e = x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "analyzing", ""); e != nil {
		return e
	}
	var plan GuidePlan
	if saved, ok := x.steps["plan"]; ok {
		if e = json.Unmarshal([]byte(saved.OutputJSON), &plan); e != nil {
			return e
		}
	} else {
		input, packet, e := x.planInput(ctx)
		if e != nil {
			return e
		}
		e = x.task(ctx, "plan", "paper.guide.plan", input, &plan, func() error { return ValidatePlan(packet, plan) })
		if e != nil {
			return e
		}
	}
	sections := []GuideSection{}
	for _, sp := range plan.Sections {
		var section GuideSection
		version := 1
		key := "section:" + string(sp.Role) + ":v1"
		for _, step := range x.steps {
			if step.Published && strings.HasPrefix(step.StepKey, "section:"+string(sp.Role)+":v") {
				var candidate GuideSection
				if json.Unmarshal([]byte(step.OutputJSON), &candidate) == nil && candidate.Version >= version {
					section = candidate
					version = candidate.Version
					key = step.StepKey
				}
			}
		}
		if section.PublicationStatus != "published" {
			var e error
			section, key, e = x.writeSection(ctx, plan, sp, sections, 1, nil, false)
			if e != nil {
				return e
			}
		}
		sections = append(sections, section)
		_ = key
	}
	if e = x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "reviewing", ""); e != nil {
		return e
	}
	// Whole review may produce at most one correction for each of at most two
	// locations. Every corrected section is checked again and appended as v2.
	for round := 1; round <= 2; round++ {
		packet, e := sectionPacket(x.snap, plan, sections)
		if e != nil {
			return e
		}
		input := x.input(packet)
		input["plan"] = plan
		input["sections"] = reviewSections(sections)
		var review GuideReview
		key := "whole_review:" + stringInt(int64(round))
		if saved, ok := x.steps[key]; ok && round == 1 {
			// This review is bound to its original immutable section versions. Resume its remaining
			// repairs before making a fresh whole review of the candidate v2 sections.
			if e = decodeStrict([]byte(saved.OutputJSON), &review); e != nil {
				return ErrEvidence
			}
		} else if e = x.task(ctx, key, "paper.guide.review", input, &review, func() error { return ValidateReview(plan, sections, review) }, wholeReviewBindings(sections)); e != nil {
			return e
		}
		if len(review.Findings) == 0 {
			for _, section := range sections {
				if section.PublicationStatus != "checking" {
					continue
				}
				section.PublicationStatus = "published"
				if _, e = x.check(ctx); e != nil {
					return e
				}
				if e = x.s.Repo.PutStep(ctx, x.f, x.rev.ID, x.rev.Epoch, StepWrite{Key: "section:" + string(section.Role) + ":v" + stringInt(int64(section.Version)), InputHash: digest(input), Output: mustMarshal(section), Review: mustMarshal(review), Published: true}); e != nil {
					return e
				}
			}
			if _, e = x.check(ctx); e != nil {
				return e
			}
			finalStatus := "complete"
			if len(x.snap.Coverage.PageWarnings) > 0 || len(x.snap.Coverage.Warnings) > 0 {
				finalStatus = "limited"
			}
			if e = x.settleOverhead(ctx); e != nil {
				return e
			}
			return x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "completed", "", finalStatus)
		}
		if round == 2 {
			return ErrQuality
		}
		for i, section := range sections {
			findings := []GuideFinding{}
			for _, finding := range review.Findings {
				if sectionHasTarget(section, plan, finding) {
					findings = append(findings, finding)
				}
			}
			if len(findings) == 0 {
				continue
			}
			if section.Version >= 2 {
				if _, reviewed := x.steps["whole_review:2"]; reviewed {
					continue
				}
				return ErrQuality
			}
			updated, _, e := x.writeSection(ctx, plan, plan.Sections[i], sections[:i], 2, findings, true)
			if e != nil {
				return e
			}
			sections[i] = updated
		}
	}
	return ErrQuality
}

// A whole review and its exact input versions are persisted in one checkpoint.
// Bindings include unpublished correction drafts, so a finding on a draft can
// never accidentally mark a different published version with the same block ID.
type reviewedSection struct {
	Key     string `json:"key"`
	Version int    `json:"version"`
}

func wholeReviewBindings(sections []GuideSection) json.RawMessage {
	bindings := make([]reviewedSection, 0, len(sections))
	for _, section := range sections {
		bindings = append(bindings, reviewedSection{Key: "section:" + string(section.Role) + ":v" + stringInt(int64(section.Version)), Version: section.Version})
	}
	return mustMarshal(bindings)
}

func sectionHasTarget(s GuideSection, p GuidePlan, f GuideFinding) bool {
	if f.TargetType == "block" {
		for _, b := range s.Blocks {
			if b.ID == f.TargetID {
				return true
			}
		}
	}
	return contains(s.ClaimIDs, f.TargetID)
}
func (x *execution) input(packet GuideSourceSnapshot) map[string]any {
	return map[string]any{"language": x.scope.Language, "evidence": packet.Evidence, "coverage": x.snap.Coverage}
}
func (x *execution) inputFits(input map[string]any) bool {
	if evidence, ok := input["evidence"].([]GuideEvidence); ok && len(evidence) > 2048 {
		return false
	}
	b, e := json.Marshal(input)
	limit := x.model.ContextTokens
	if limit <= 0 {
		limit = 32768
	}
	return e == nil && len(b) <= 1<<20 && estimatedTokens(b) <= limit*7/10
}

// A UTF-8 byte bound also covers dense punctuation/formula tokenization.
// Metadata-free providers get a conservative 32k context.
func estimatedTokens(raw []byte) int { return len(raw) }
func (x *execution) planInput(ctx context.Context) (map[string]any, GuideSourceSnapshot, error) {
	input := x.input(x.snap)
	input["source_sections"] = x.snap.Sections
	if x.inputFits(input) {
		return input, x.snap, nil
	}
	// Keep original reading order and cover every evidence item. Section
	// boundaries are preferred; an oversized section is split into page/block
	// packets without truncating a single original block.
	packets := []GuideSourceSnapshot{}
	ids := []string{}
	sectionStarts := map[string][]string{}
	for _, section := range x.snap.Sections {
		if len(section.EvidenceIDs) > 0 {
			sectionStarts[section.EvidenceIDs[0]] = section.EvidenceIDs
		}
	}
	for _, ev := range x.snap.Evidence {
		if group, ok := sectionStarts[ev.ID]; ok && len(ids) > 0 {
			whole, err := RestrictEvidence(x.snap, group)
			if err != nil {
				return nil, GuideSourceSnapshot{}, err
			}
			combined, err := RestrictEvidence(x.snap, append(append([]string{}, ids...), group...))
			if err != nil {
				return nil, GuideSourceSnapshot{}, err
			}
			if x.inputFits(x.input(whole)) && !x.inputFits(x.input(combined)) {
				prior, _ := RestrictEvidence(x.snap, ids)
				packets = append(packets, prior)
				ids = []string{}
			}
		}

		candidate := append(append([]string{}, ids...), ev.ID)
		packet, e := RestrictEvidence(x.snap, candidate)
		if e != nil {
			return nil, GuideSourceSnapshot{}, e
		}
		if !x.inputFits(x.input(packet)) {
			if len(ids) == 0 {
				return nil, GuideSourceSnapshot{}, ErrContext
			}
			prior, _ := RestrictEvidence(x.snap, ids)
			packets = append(packets, prior)
			ids = []string{ev.ID}
			single, _ := RestrictEvidence(x.snap, ids)
			if !x.inputFits(x.input(single)) {
				return nil, GuideSourceSnapshot{}, ErrContext
			}
		} else {
			ids = candidate
		}
	}
	if len(ids) > 0 {
		packet, _ := RestrictEvidence(x.snap, ids)
		packets = append(packets, packet)
	}
	if len(packets) > MaxExtractCalls {
		return nil, GuideSourceSnapshot{}, ErrContext
	}
	extracts := []GuideExtract{}
	keep := map[string]bool{}
	for i, packet := range packets {
		var out GuideExtract
		e := x.task(ctx, "extract:"+stringInt(int64(i+1)), "paper.guide.extract", x.input(packet), &out, func() error { return ValidateExtract(packet, out) })
		if e != nil {
			return nil, GuideSourceSnapshot{}, e
		}
		extracts = append(extracts, out)
		for _, c := range out.Claims {
			for _, id := range c.EvidenceIDs {
				keep[id] = true
			}
		}
	}
	ids = []string{}
	for _, ev := range x.snap.Evidence {
		if keep[ev.ID] {
			ids = append(ids, ev.ID)
		}
	}
	if len(ids) == 0 {
		return nil, GuideSourceSnapshot{}, ErrMaterial
	}
	packet, e := RestrictEvidence(x.snap, ids)
	if e != nil {
		return nil, GuideSourceSnapshot{}, e
	}
	input = x.input(packet)
	input["source_sections"] = x.snap.Sections
	input["extracts"] = extracts
	if !x.inputFits(input) {
		return nil, GuideSourceSnapshot{}, ErrContext
	}
	return input, packet, nil
}
func sectionPacket(snap GuideSourceSnapshot, plan GuidePlan, sections []GuideSection) (GuideSourceSnapshot, error) {
	ids := []string{}
	for _, s := range sections {
		for _, c := range append(append([]GuideClaim{}, plan.Claims...), s.Claims...) {
			if contains(s.ClaimIDs, c.ID) {
				ids = append(ids, c.EvidenceIDs...)
			}
		}
		for _, b := range s.Blocks {
			ids = append(ids, b.EvidenceIDs...)
			if b.AssetID != "" {
				ids = append(ids, b.AssetID)
			}
		}
	}
	return RestrictEvidence(snap, unique(ids))
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
func (x *execution) writeSection(ctx context.Context, p GuidePlan, sp GuideSectionPlan, prior []GuideSection, version int, findings []GuideFinding, deferReview bool) (GuideSection, string, error) {
	if len(findings) > 0 {
		if e := x.reserveCorrections(ctx, "section:"+string(sp.Role)+":v"+stringInt(int64(version)), findings); e != nil {
			return GuideSection{}, "", e
		}
	}
	if e := x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "writing", ""); e != nil {
		return GuideSection{}, "", e
	}
	ids := append(append([]string{}, sp.EvidenceIDs...), sp.AssetIDs...)
	for _, c := range p.Claims {
		if contains(sp.ClaimIDs, c.ID) {
			ids = append(ids, c.EvidenceIDs...)
		}
	}
	packet, e := RestrictEvidence(x.snap, unique(ids))
	if e != nil {
		return GuideSection{}, "", e
	}
	input := x.input(packet)
	input["plan"] = p
	input["role"] = sp.Role
	if len(prior) > 0 {
		summary := []rune(prior[len(prior)-1].Summary)
		if len(summary) > 4096 {
			summary = summary[:4096]
		}
		input["prior_summary"] = string(summary)
	}
	if len(findings) > 0 {
		input["findings"] = findings
	}
	key := "section:" + string(sp.Role) + ":v" + stringInt(int64(version))
	var section GuideSection
	e = x.task(ctx, key+":draft", "paper.guide.section", input, &section, func() error {
		if section.Version != 0 || section.PublicationStatus != "" {
			return ErrEvidence
		}
		return ValidateSection(packet, p, sp.Role, section)
	})
	if e != nil {
		return section, key, e
	}
	if deferReview {
		section.Version = version
		section.PublicationStatus = "checking"
		return section, key, nil
	}
	reviewInput := x.input(packet)
	reviewInput["plan"] = p
	reviewInput["sections"] = []GuideSection{section}
	var review GuideReview
	if e = x.task(ctx, key+":review", "paper.guide.review", reviewInput, &review, func() error { return ValidateReview(p, []GuideSection{section}, review) }); e != nil {
		return section, key, e
	}
	if len(review.Findings) > 0 {
		if version >= 2 {
			return section, key, ErrQuality
		}
		return x.writeSection(ctx, p, sp, prior, 2, review.Findings, false)
	}
	// Cross-section IDs must be unique before publication, not just at final review.
	if e = ValidateReview(p, append(append([]GuideSection{}, prior...), section), GuideReview{}); e != nil {
		return section, key, ErrEvidence
	}
	section.Version = version
	section.PublicationStatus = "published"
	if _, e = x.check(ctx); e != nil {
		return section, key, e
	}
	raw := mustMarshal(section)
	e = x.s.Repo.PutStep(ctx, x.f, x.rev.ID, x.rev.Epoch, StepWrite{Key: key, InputHash: digest(reviewInput), Output: raw, Review: mustMarshal(review), Published: true})
	if e == nil {
		x.steps[key] = orm.PaperGuideStep{StepKey: key, OutputJSON: string(raw), Published: true}
	}
	return section, key, e
}
func (x *execution) reserveCorrections(ctx context.Context, repairKey string, findings []GuideFinding) error {
	return x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
		seen := map[string]bool{}
		for _, f := range findings {
			key := "correction:" + f.TargetType + ":" + f.TargetID
			if seen[key] {
				continue
			}
			seen[key] = true
			var existing orm.PaperGuideStep
			e := tx.Where("revision_id = ? AND step_key = ?", r.ID, key).Take(&existing).Error
			if e == nil {
				if existing.InputHash != digest([]any{repairKey, f}) {
					return ErrQuality
				}
				continue
			}
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			if r.CorrectionCount >= 2 {
				return ErrQuality
			}
			r.CorrectionCount++
			if e := tx.Create(&orm.PaperGuideStep{ID: uuid.NewString(), RevisionID: r.ID, StepKey: key, InputHash: digest([]any{repairKey, f}), OutputJSON: string(mustMarshal(f)), Epoch: r.Epoch, CreatedAt: time.Now().UTC()}).Error; e != nil {
				return e
			}
		}
		return tx.Model(r).Update("correction_count", r.CorrectionCount).Error
	})
}
func (x *execution) task(ctx context.Context, key, task string, input map[string]any, out any, validate func() error, metadata ...json.RawMessage) error {
	if !x.inputFits(input) {
		return ErrContext
	}
	hash := digest(input)
	if prior, ok := x.steps[key]; ok {
		if prior.InputHash != hash {
			return ErrConflict
		}
		if e := decodeStrict([]byte(prior.OutputJSON), out); e != nil {
			return ErrEvidence
		}
		return validate()
	}
	for retry := 0; retry < 2; retry++ {
		cfg, e := x.check(ctx)
		if e != nil {
			return e
		}
		started := time.Now()
		ticket := "request:" + key + ":" + uuid.NewString()
		e = x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
			var attempts int64
			if e := tx.Model(&orm.PaperGuideStep{}).Where("revision_id = ? AND input_hash = ? AND step_key LIKE ?", r.ID, hash, "request:"+key+":%").Count(&attempts).Error; e != nil {
				return e
			}
			recovery := attempts > 0
			extract := task == "paper.guide.extract"
			if r.CallsUsed >= 24 || r.ActiveMillis+callMillis > MaxActiveMillis || recovery && r.RecoveryCalls >= 2 || !recovery && !extract && r.PlannedCalls >= 14 || extract && !recovery && r.ExtractCalls >= 8 {
				return ErrBudget
			}
			updates := map[string]any{"calls_used": gorm.Expr("calls_used + 1"), "active_millis": gorm.Expr("active_millis + ?", callMillis)}
			if !recovery && !extract {
				updates["planned_calls"] = gorm.Expr("planned_calls + 1")
			}
			if recovery {
				updates["recovery_calls"] = gorm.Expr("recovery_calls + 1")
			}
			if extract && !recovery {
				updates["extract_calls"] = gorm.Expr("extract_calls + 1")
			}
			if e := tx.Model(r).Updates(updates).Error; e != nil {
				return e
			}
			return tx.Create(&orm.PaperGuideStep{ID: uuid.NewString(), RevisionID: r.ID, StepKey: ticket, InputHash: hash, Epoch: r.Epoch, CreatedAt: started.UTC()}).Error
		})
		if e != nil {
			return e
		}
		callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		raw, callErr := x.s.Call(callCtx, task, input, cfg)
		cancel()
		elapsed := time.Since(started).Milliseconds()
		x.callTime += elapsed
		if elapsed > callMillis {
			elapsed = callMillis
		}
		diagnostic := "success"
		if callErr != nil {
			diagnostic = "transport"
			var classified *CallError
			if errors.As(callErr, &classified) {
				diagnostic = classified.Class
			}
			if errors.Is(callErr, context.DeadlineExceeded) {
				diagnostic = "timeout"
			}
		} else {
			callErr = decodeStrict(raw, out)
			if callErr != nil {
				diagnostic = "schema"
			} else {
				callErr = validate()
				if callErr != nil {
					diagnostic = "contract"
				}
			}
		}
		// Settle only this invocation, once. Unknown crashed requests retain
		// the 120-second reservation; diagnostic metadata never includes raw
		// provider errors, model config or private source text.
		e = x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, g *orm.PaperGuide) error {
			marker := tx.Model(&orm.PaperGuideStep{}).Where("revision_id = ? AND step_key = ? AND output_json = ?", r.ID, ticket, "").Update("output_json", string(mustMarshal(map[string]any{"task": task, "error_class": diagnostic, "elapsed_ms": elapsed})))
			if marker.Error != nil {
				return marker.Error
			}
			if marker.RowsAffected != 1 {
				return ErrConflict
			}
			return tx.Model(r).Update("active_millis", gorm.Expr("active_millis - ?", callMillis-elapsed)).Error
		})
		if e != nil {
			return e
		}
		if _, e = x.check(ctx); e != nil {
			return e
		}

		if callErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if retry == 0 {
				continue
			}
			return fmt.Errorf("%w: model output or request failed", ErrEvidence)
		}
		var reviewMetadata json.RawMessage
		if len(metadata) == 1 {
			reviewMetadata = metadata[0]
		}
		e = x.s.Repo.PutStep(ctx, x.f, x.rev.ID, x.rev.Epoch, StepWrite{Key: key, InputHash: hash, Output: raw, Review: reviewMetadata})
		if e != nil {
			return e
		}
		x.steps[key] = orm.PaperGuideStep{StepKey: key, InputHash: hash, OutputJSON: string(raw), ReviewJSON: string(reviewMetadata)}
		return nil
	}
	return ErrEvidence
}
func decodeStrict(raw []byte, out any) error {
	if len(raw) > 256<<10 {
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

func reviewSections(sections []GuideSection) []GuideSection {
	out := append([]GuideSection{}, sections...)
	for i := range out {
		out[i].Version = 0
		out[i].PublicationStatus = ""
	}
	return out
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
		return tx.Model(r).Update("active_millis", gorm.Expr("active_millis - ?", callMillis-elapsed)).Error
	})
	if err == nil {
		x.overheadReserved = false
	}
	return err
}
