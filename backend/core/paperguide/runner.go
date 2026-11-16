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
	"maps"
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
	planningPacket   GuideSourceSnapshot
	extractedScope   bool
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
		err = x.execute(runCtx)
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
		if !state.Snapshot.Coverage.Body || state.Snapshot.Coverage.AbstractOnly {
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
	_, hasPlan := x.steps["plan"]
	whole1, hasWhole1 := x.steps["whole_review:1"]
	_, hasWhole2 := x.steps["whole_review:2"]
	if hasWhole2 && !hasWhole1 || hasWhole1 && !hasPlan {
		return ErrEvidence
	}
	if e = x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "analyzing", ""); e != nil {
		return e
	}
	// Retain the independent packet before Plan selects claims. On resume this
	// reconstructs only from immutable, validated extraction checkpoints.
	input, packet, e := x.planInput(ctx)
	if e != nil {
		return e
	}
	x.planningPacket = packet
	_, x.extractedScope = input["extracts"]
	var plan GuidePlan
	metadata := mustMarshal(planningScope{Extracted: x.extractedScope, OriginalAssets: x.extractedScope})
	if e = x.task(ctx, "plan", "paper.guide.plan", input, &plan, func() error { return ValidatePlan(packet, plan) }, metadata); e != nil {
		return e
	}
	sections := make([]GuideSection, 0, len(plan.Sections))
	if hasWhole1 {
		sections, e = x.originalWholeSections(plan, whole1)
		if e != nil {
			return e
		}
	} else {
		for _, sp := range plan.Sections {
			var section GuideSection
			for _, step := range x.steps {
				if step.Published && strings.HasPrefix(step.StepKey, "section:"+string(sp.Role)+":v") {
					var candidate GuideSection
					if json.Unmarshal([]byte(step.OutputJSON), &candidate) == nil && candidate.Version > section.Version {
						section = candidate
					}
				}
			}
			if section.PublicationStatus != "published" {
				section, _, e = x.writeSection(ctx, plan, sp, sections, 1, nil)
				if e != nil {
					return e
				}
			}
			if e = ValidateSection(packet, plan, sp.Role, section); e != nil {
				return ErrEvidence
			}
			sections = append(sections, section)
		}
	}
	if e = x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "reviewing", ""); e != nil {
		return e
	}
	// Whole review may correct a v1 section, recheck it, and append v2.
	// Sections already repaired by their single-section review stay at their
	// published versions; findings on those versions pause the run.
	for round := 1; round <= 2; round++ {
		input := x.independentInput()
		input["plan"] = plan
		input["sections"] = reviewSections(sections)
		var review GuideReview
		key := "whole_review:" + stringInt(int64(round))
		bindings := wholeReviewBindings(sections)
		if saved, ok := x.steps[key]; ok && saved.ReviewJSON != string(bindings) {
			return ErrEvidence
		}
		if e = x.task(ctx, key, "paper.guide.review", input, &review, func() error { return ValidateReview(plan, sections, review) }, bindings); e != nil {
			return e
		}
		if len(review.Findings) == 0 {
			if round == 1 && hasWhole2 {
				return ErrEvidence
			}
			for _, section := range sections {
				if section.PublicationStatus != "checking" {
					continue
				}
				section.PublicationStatus = "published"
				write := StepWrite{Key: sectionKey(section.Role, section.Version), InputHash: digest(input), Output: mustMarshal(section), Review: mustMarshal(review), Published: true}
				if saved, ok := x.steps[write.Key]; ok {
					if !saved.Published || saved.InputHash != write.InputHash || saved.OutputJSON != string(write.Output) || saved.ReviewJSON != string(write.Review) {
						return ErrConflict
					}
					continue
				}
				if _, e = x.check(ctx); e != nil {
					return e
				}
				if e = x.s.Repo.PutStep(ctx, x.f, x.rev.ID, x.rev.Epoch, write); e != nil {
					return e
				}
			}
			if _, e = x.check(ctx); e != nil {
				return e
			}
			finalStatus := "complete"
			if x.extractedScope || len(x.snap.Coverage.PageWarnings) > 0 || len(x.snap.Coverage.Warnings) > 0 {
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
			findings := sectionFindings(section, review.Findings)
			if len(findings) == 0 {
				continue
			}
			if section.Version != 1 {
				return ErrQuality
			}
			if _, promoted := x.steps[sectionKey(section.Role, 2)]; promoted && !hasWhole2 {
				return ErrEvidence
			}
			updated, _, e := x.writeSection(ctx, plan, plan.Sections[i], sections[:i], 2, &sectionRepair{base: section, findings: findings, whole: true})
			if e != nil {
				return e
			}
			sections[i] = updated
		}
	}
	return ErrQuality
}

// The first whole review fixes the original published versions. On restart,
// use those checkpoints even when some replacements are already published.
func (x *execution) originalWholeSections(plan GuidePlan, saved orm.PaperGuideStep) ([]GuideSection, error) {
	var bindings []reviewedSection
	if decodeStrict([]byte(saved.ReviewJSON), &bindings) != nil || len(bindings) != len(plan.Sections) {
		return nil, ErrEvidence
	}
	sections := make([]GuideSection, 0, len(bindings))
	for i, binding := range bindings {
		role := plan.Sections[i].Role
		step, ok := x.steps[binding.Key]
		var section GuideSection
		if !ok || !step.Published || binding.Version < 1 || binding.Version > 3 ||
			binding.Key != sectionKey(role, binding.Version) ||
			decodeStrict([]byte(step.OutputJSON), &section) != nil || section.Version != binding.Version ||
			section.PublicationStatus != "published" || ValidateSection(x.planningPacket, plan, role, section) != nil {
			return nil, ErrEvidence
		}
		sections = append(sections, section)
	}
	return sections, nil
}

func sectionKey(role Role, version int) string {
	return "section:" + string(role) + ":v" + stringInt(int64(version))
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
		bindings = append(bindings, reviewedSection{Key: sectionKey(section.Role, section.Version), Version: section.Version})
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
func (x *execution) inputFits(task string, input map[string]any) bool {
	raw, err := json.MarshalIndent(input, "", "")
	if err != nil {
		return false
	}
	bound, ok := guideInputBound(task, raw)
	if !ok {
		return false
	}
	limit := x.model.ContextTokens
	if limit <= 0 {
		limit = 32768
	}
	budget := limit / 10 * 7
	if x.model.OutputReserveTokens > 0 {
		budget = min(budget, limit-x.model.OutputReserveTokens)
	}
	return bound <= budget
}

func (x *execution) planningInput(packet GuideSourceSnapshot) map[string]any {
	input := x.input(packet)
	input["source_sections"] = x.snap.Sections
	if c := packet.originalCandidates; c != nil && !c.empty() {
		input["original_asset_candidates"] = c
	}
	return input
}

func (x *execution) planInput(ctx context.Context) (map[string]any, GuideSourceSnapshot, error) {
	if len(x.snap.Sections) > 512 {
		return nil, GuideSourceSnapshot{}, ErrContext
	}
	short := x.snap
	candidates := selectOriginalAssets(short, nil)
	short.originalCandidates = &candidates
	input := x.planningInput(short)
	if x.inputFits("paper.guide.plan", input) {
		return input, short, nil
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
			if x.inputFits("paper.guide.extract", x.input(whole)) && !x.inputFits("paper.guide.extract", x.input(combined)) {
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
		if !x.inputFits("paper.guide.extract", x.input(packet)) {
			if len(ids) == 0 {
				return nil, GuideSourceSnapshot{}, ErrContext
			}
			prior, _ := RestrictEvidence(x.snap, ids)
			packets = append(packets, prior)
			ids = []string{ev.ID}
			single, _ := RestrictEvidence(x.snap, ids)
			if !x.inputFits("paper.guide.extract", x.input(single)) {
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
		key := "extract:" + stringInt(int64(i+1))
		extractInput := x.input(packet)
		var e error
		if _, savedPlan := x.steps["plan"]; savedPlan {
			// A saved Plan fixes provenance: never spend to recreate a missing,
			// corrupt or differently partitioned extract checkpoint.
			saved, ok := x.steps[key]
			if !ok {
				return nil, GuideSourceSnapshot{}, ErrEvidence
			}
			if saved.InputHash != digest([]any{TaskVersion, extractInput}) {
				return nil, GuideSourceSnapshot{}, ErrConflict
			}
			if decodeStrict([]byte(saved.OutputJSON), &out) != nil || ValidateExtract(packet, out) != nil {
				return nil, GuideSourceSnapshot{}, ErrEvidence
			}
		} else {
			e = x.task(ctx, key, "paper.guide.extract", extractInput, &out, func() error { return ValidateExtract(packet, out) })
		}
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
	// Extraction may summarize claims, but cannot remove the physical table
	// group before its single parent asset decision is planned.
	for _, asset := range x.snap.Assets {
		if len(asset.FragmentEvidenceIDs) > 0 {
			keep[asset.ID] = true
		}
	}
	candidates = selectOriginalAssets(x.snap, keep)
	for {
		wanted := make(map[string]bool, len(keep)+len(candidates.Figure)+len(candidates.Formula))
		for id := range keep {
			wanted[id] = true
		}
		for _, c := range candidates.Figure {
			wanted[c.ID] = true
		}
		for _, c := range candidates.Formula {
			wanted[c.ID] = true
		}
		ids = ids[:0]
		for _, ev := range x.snap.Evidence {
			if wanted[ev.ID] {
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
		packet.originalCandidates = &candidates
		input = x.planningInput(packet)
		input["extracts"] = extracts
		if x.inputFits("paper.guide.plan", input) {
			return input, packet, nil
		}
		if !candidates.trim() {
			return nil, GuideSourceSnapshot{}, ErrContext
		}
	}
}

// This diagnostic describes evidence availability, not source unavailability or
// a quality waiver. It is copied into requests and the existing public coverage
// warnings; neither the frozen snapshot nor published artifacts are rewritten.
const extractedReviewWarning = "全篇审核覆盖独立抽取保留的原文证据及完整表格组，不是全部原文逐段审核；抽取未保留的内容可能未被检查。这不代表原文缺失，也不豁免已发现的事实或解释缺陷。"

const originalAssetsReviewWarning = "全篇审核覆盖抽取证据、完整表格和预选原件，并非逐段审核全部原文；其他原件和内容可能未被检查。范围限制不豁免已发现的缺陷。"

type planningScope struct {
	Extracted      bool `json:"independent_extracted_scope"`
	OriginalAssets bool `json:"original_assets_scope,omitempty"`
}

func (x *execution) independentInput() map[string]any {
	packet := x.planningPacket
	if x.extractedScope {
		packet.Coverage.Warnings = append(append([]string{}, packet.Coverage.Warnings...), originalAssetsReviewWarning)
	}
	input := x.planningInput(packet)
	input["coverage"] = packet.Coverage
	return input
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

// sectionInput measures the complete request for the current stage. Local stages
// may narrow to the role and actual draft's source dependencies. A deferred
// whole-guide correction still requires the independent packet.
func (x *execution) sectionInput(task string, p GuidePlan, sp GuideSectionPlan, fields map[string]any, draft *GuideSection, independentOnly bool) (map[string]any, GuideSourceSnapshot, error) {
	input := x.independentInput()
	input["plan"] = p
	maps.Copy(input, fields)
	if x.inputFits(task, input) {
		return input, x.planningPacket, nil
	}
	if independentOnly {
		return nil, GuideSourceSnapshot{}, ErrContext
	}

	ids := append(append([]string{}, sp.EvidenceIDs...), sp.AssetIDs...)
	for _, claim := range p.Claims {
		if contains(sp.ClaimIDs, claim.ID) || (draft != nil && contains(draft.ClaimIDs, claim.ID)) {
			ids = append(ids, claim.EvidenceIDs...)
		}
	}
	if draft != nil {
		for _, claim := range draft.Claims {
			ids = append(ids, claim.EvidenceIDs...)
		}
		for _, block := range draft.Blocks {
			ids = append(ids, block.EvidenceIDs...)
			if block.AssetID != "" {
				ids = append(ids, block.AssetID)
			}
		}
	}
	packet, err := RestrictEvidence(x.planningPacket, unique(ids))
	if err != nil {
		return nil, GuideSourceSnapshot{}, ErrEvidence
	}
	input = x.input(packet)
	input["plan"] = p
	maps.Copy(input, fields)
	if !x.inputFits(task, input) {
		return nil, GuideSourceSnapshot{}, ErrContext
	}
	return input, packet, nil
}

func (x *execution) writeSection(ctx context.Context, p GuidePlan, sp GuideSectionPlan, prior []GuideSection, version int, repair *sectionRepair) (GuideSection, string, error) {
	key := sectionKey(sp.Role, version)
	var findings []GuideFinding
	var previous *GuideSection
	deferred := repair != nil && repair.whole
	_, wholeReviewed := x.steps["whole_review:2"]
	replayOnly := deferred && wholeReviewed
	if repair != nil {
		if replayOnly {
			if _, ok := x.steps[key+":draft"]; !ok {
				return GuideSection{}, key, ErrEvidence
			}
		}
		if _, e := x.check(ctx); e != nil {
			return GuideSection{}, key, e
		}
		if e := x.reserveRepair(ctx, p, sp.Role, version, *repair, replayOnly); e != nil {
			return GuideSection{}, key, e
		}
		findings = repair.findings
		if !deferred {
			previous = &repair.base
		}
	}
	if !replayOnly {
		if e := x.s.Repo.Transition(ctx, x.f, x.rev.ID, x.rev.Epoch, "writing", ""); e != nil {
			return GuideSection{}, key, e
		}
	}
	fields := map[string]any{"role": sp.Role}
	if len(prior) > 0 {
		summary := []rune(prior[len(prior)-1].Summary)
		if len(summary) > 4096 {
			summary = summary[:4096]
		}
		fields["prior_summary"] = string(summary)
	}
	if len(findings) > 0 {
		fields["findings"] = findings
	}
	input, packet, e := x.sectionInput("paper.guide.section", p, sp, fields, previous, deferred)
	if e != nil {
		return GuideSection{}, "", e
	}
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
	if deferred {
		section.Version = version
		section.PublicationStatus = "checking"
		return section, key, nil
	}
	reviewed := []GuideSection{section}
	reviewInput, _, e := x.sectionInput("paper.guide.review", p, sp, map[string]any{"sections": reviewed}, &section, false)
	if e != nil {
		return section, key, e
	}
	var review GuideReview
	if e = x.task(ctx, key+":review", "paper.guide.review", reviewInput, &review, func() error { return ValidateReview(p, reviewed, review) }); e != nil {
		return section, key, e
	}
	if len(review.Findings) > 0 {
		if version >= 3 {
			return section, key, ErrQuality
		}
		return x.writeSection(ctx, p, sp, prior, version+1, &sectionRepair{base: section, findings: review.Findings})
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
func requestHashes(tx *gorm.DB, revisionID, key string) ([]string, error) {
	var hashes []string
	prefix := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace("request:" + key + ":")
	err := tx.Model(&orm.PaperGuideStep{}).Where("revision_id = ? AND step_key LIKE ? ESCAPE '!'", revisionID, prefix+"%").
		Distinct("input_hash").Pluck("input_hash", &hashes).Error
	return hashes, err
}

func (x *execution) task(ctx context.Context, key, task string, input map[string]any, out any, validate func() error, metadata ...json.RawMessage) error {
	if !x.inputFits(task, input) {
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
	attemptTimeout := ModelAttemptTimeout(task)
	attemptMillis := attemptTimeout.Milliseconds()
	for retry := 0; retry < 2; retry++ {
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
			extract := task == "paper.guide.extract"
			if r.CallsUsed >= 24 || r.ActiveMillis+attemptMillis > MaxActiveMillis || recovery && r.RecoveryCalls >= 2 || !recovery && !extract && r.PlannedCalls >= 14 || extract && !recovery && r.ExtractCalls >= 8 {
				return ErrBudget
			}
			updates := map[string]any{"calls_used": gorm.Expr("calls_used + 1"), "active_millis": gorm.Expr("active_millis + ?", attemptMillis)}
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
		if _, e = x.check(ctx); e != nil {
			return e
		}

		if callErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(callErr, ErrBudget) || errors.Is(callErr, ErrConflict) || errors.Is(callErr, asyncjob.ErrLeaseLost) || errors.Is(callErr, context.Canceled) {
				return callErr
			}
			if retry == 0 {
				continue
			}
			return callErr
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
		return tx.Model(r).Update("active_millis", gorm.Expr("active_millis - ?", localProcessingReserveMillis-elapsed)).Error
	})
	if err == nil {
		x.overheadReserved = false
	}
	return err
}
