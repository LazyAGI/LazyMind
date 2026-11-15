package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
	"strings"
	"time"
)

type SourceState struct {
	PrecisionUnavailable bool
	DependencyFailed     bool
	CanPrepare           bool
	Hash                 string
	Snapshot             *GuideSourceSnapshot
	Path, DependencyID   string
}
type SourceLoader interface {
	Load(context.Context, Scope, bool) (SourceState, error)
	Read(context.Context, string) (GuideSourceSnapshot, error)
}
type Service struct {
	FeatureEnabled func() bool
	Repo           Repository
	Source         SourceLoader
	Authorize      func(context.Context, Scope, bool) error
	ResolveModel   func(context.Context, Scope, string) (ModelIdentity, map[string]any, error)
	Call           func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error)
}
type GenerateRequest struct {
	Language           string `json:"language"`
	ExpectedSourceHash string `json:"expected_source_hash"`
	IdempotencyKey     string `json:"idempotency_key"`
	Regenerate         bool   `json:"regenerate"`
	ModelRef           string `json:"model_ref,omitempty"`
}
type RunView struct {
	ID                string `json:"id"`
	Stage             string `json:"stage"`
	JobID             string `json:"job_id"`
	ErrorCode         string `json:"error_code,omitempty"`
	ResumeBlockReason string `json:"resume_block_reason,omitempty"`
	CallsUsed         int    `json:"calls_used"`
	CallLimit         int    `json:"call_limit"`
	ActiveMillis      int64  `json:"active_millis"`
	CostKnown         bool   `json:"cost_known"`
}
type RevisionView struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	SourceSHA256 string        `json:"source_sha256"`
	IRDigest     string        `json:"ir_digest"`
	Version      int64         `json:"version"`
	Model        ModelIdentity `json:"model"`
	Run          RunView       `json:"run"`
	CreatedAt    time.Time     `json:"created_at"`
}
type StatusResponse struct {
	Enabled           bool             `json:"enabled"`
	CanRead           bool             `json:"can_read"`
	CanGenerate       bool             `json:"can_generate"`
	UnavailableReason string           `json:"unavailable_reason,omitempty"`
	SourceSHA256      string           `json:"source_sha256"`
	Version           int64            `json:"version"`
	PublishedRevision *RevisionView    `json:"published_revision"`
	CandidateRevision *RevisionView    `json:"candidate_revision"`
	Progress          *ProgressRequest `json:"progress,omitempty"`
	// CurrentIRDigest also invalidates status when revision currentness changes
	// without any change to the public status representation.
	CurrentIRDigest string `json:"-"`
}
type PublishedSection struct {
	Key           string `json:"key"`
	ChangeVersion int64  `json:"change_version"`
	GuideSection
}
type RevisionResponse struct {
	RevisionView
	Coverage      GuideCoverage      `json:"coverage"`
	Plan          *GuidePlan         `json:"plan,omitempty"`
	Sections      []PublishedSection `json:"sections"`
	SourceCurrent bool               `json:"source_current"`
	ReviewSummary *GuideReview       `json:"review_summary,omitempty"`
}
type ProgressRequest struct {
	RevisionID string `json:"revision_id"`
	SectionKey string `json:"section_key"`
	BlockID    string `json:"block_id"`
	Offset     int    `json:"offset"`
	Mode       string `json:"mode"`
}
type SelectionRequest struct {
	SectionKey string `json:"section_key"`
	BlockID    string `json:"block_id"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
}
type SelectionContext struct {
	RevisionID    string          `json:"revision_id"`
	SectionKey    string          `json:"section_key"`
	BlockID       string          `json:"block_id"`
	Text          string          `json:"text"`
	Evidence      []GuideEvidence `json:"evidence"`
	Claims        []GuideClaim    `json:"claims"`
	SourceSHA256  string          `json:"source_sha256"`
	IRDigest      string          `json:"ir_digest"`
	SourceCurrent bool            `json:"source_current"`
}

func view(r orm.PaperGuideRevision) RevisionView {
	var m ModelIdentity
	_ = json.Unmarshal([]byte(r.ModelJSON), &m)
	return RevisionView{
		ID: r.ID, Status: r.Status, SourceSHA256: r.SourceSHA256, IRDigest: r.IRDigest, Version: r.ChangeVersion,
		Model: m, CreatedAt: r.CreatedAt,
		Run: RunView{
			ID: r.ID, Stage: r.Stage, JobID: r.JobID, ErrorCode: r.ErrorCode,
			ResumeBlockReason: resumeBlockReason(r),
			CallsUsed:         r.CallsUsed, CallLimit: MaxCalls, ActiveMillis: r.ActiveMillis,
		},
	}
}
func (s Service) Status(ctx context.Context, scope Scope) (StatusResponse, error) {
	out := StatusResponse{}
	if e := s.Authorize(ctx, scope, false); e != nil {
		return out, e
	}
	out.CanRead = true
	e := s.Authorize(ctx, scope, true)
	out.Enabled = !errors.Is(e, ErrDisabled)
	if s.FeatureEnabled != nil {
		out.Enabled = s.FeatureEnabled()
	}
	out.CanGenerate = e == nil
	if e != nil {
		out.UnavailableReason = e.Error()
	}
	source, e := s.Source.Load(ctx, scope, false)
	if e != nil && !errors.Is(e, ErrSourceChanged) {
		return out, e
	}
	out.SourceSHA256 = source.Hash
	if source.Snapshot != nil {
		out.CurrentIRDigest = source.Snapshot.IRDigest
	}
	if source.Snapshot == nil && !source.CanPrepare {
		out.CanGenerate = false
		out.UnavailableReason = ErrPreparation.Error()
		if source.PrecisionUnavailable {
			out.UnavailableReason = ErrPrecision.Error()
		}
	}
	if errors.Is(e, ErrSourceChanged) {
		out.CanGenerate = false
		out.UnavailableReason = ErrSourceChanged.Error()
	}
	g, e := s.Repo.Guide(ctx, scope)
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	out.Version = g.ChangeVersion
	for i, id := range []string{g.PublishedRevisionID, g.CandidateRevisionID} {
		if id == "" {
			continue
		}
		r, e := s.Repo.Revision(ctx, scope, id)
		if e != nil {
			return out, e
		}
		v := s.observedView(ctx, r)
		if i == 0 {
			out.PublishedRevision = &v
		} else {
			out.CandidateRevision = &v
		}
	}
	var p orm.PaperGuideProgress
	if e = s.Repo.DB.WithContext(ctx).Where("owner_id = ? AND revision_id IN ?", scope.OwnerID, []string{g.PublishedRevisionID, g.CandidateRevisionID}).Order("updated_at DESC").Take(&p).Error; e == nil {
		out.Progress = &ProgressRequest{p.RevisionID, p.SectionKey, p.BlockID, p.Offset, p.Mode}
	}
	return out, nil
}
func (s Service) Generate(ctx context.Context, scope Scope, in GenerateRequest) (RevisionView, bool, error) {
	if e := s.Authorize(ctx, scope, false); e != nil {
		return RevisionView{}, false, e
	}
	state, e := s.Source.Load(ctx, scope, false)
	if e != nil {
		return RevisionView{}, false, e
	}
	if in.ExpectedSourceHash == "" || in.ExpectedSourceHash != state.Hash {
		return RevisionView{}, false, ErrSourceChanged
	}
	if g, e := s.Repo.Guide(ctx, scope); e == nil {
		if in.IdempotencyKey != "" {
			var r orm.PaperGuideRevision
			if s.Repo.DB.WithContext(ctx).Where("guide_id = ? AND idempotency_key = ?", g.ID, in.IdempotencyKey).Take(&r).Error == nil {
				return view(r), true, nil
			}
		}
		if !in.Regenerate {
			for _, id := range []string{g.CandidateRevisionID, g.PublishedRevisionID} {
				if id != "" {
					r, e := s.Repo.Revision(ctx, scope, id)
					if e == nil && r.SourceSHA256 == state.Hash && (state.Snapshot == nil || r.IRDigest == state.Snapshot.IRDigest) {
						return view(r), true, nil
					}
				}
			}
		}
	}
	if e = s.Authorize(ctx, scope, true); e != nil {
		return RevisionView{}, false, e
	}
	m, _, e := s.ResolveModel(ctx, scope, in.ModelRef)
	if e != nil {
		return RevisionView{}, false, ErrModelChanged
	}
	// Persist the immutable personal snapshot only for an authorized generation
	// request. Read-only status/source inspection never writes artifacts.
	state, e = s.Source.Load(ctx, scope, true)
	if e != nil {
		return RevisionView{}, false, e
	}
	if state.Hash != in.ExpectedSourceHash {
		return RevisionView{}, false, ErrSourceChanged
	}
	req := CreateRevision{Scope: scope, SourceSHA256: state.Hash, SnapshotPath: state.Path, DependencyID: state.DependencyID, Model: m, IdempotencyKey: in.IdempotencyKey, Regenerate: in.Regenerate}
	if state.Snapshot != nil {
		req.IRDigest = state.Snapshot.IRDigest
		if !state.Snapshot.Coverage.Body || state.Snapshot.Coverage.AbstractOnly {
			return RevisionView{}, false, ErrMaterial
		}
	}
	r, e := s.Repo.Create(ctx, req)
	return view(r), false, e
}
func (s Service) Revision(ctx context.Context, scope Scope, id string, after int64) (RevisionResponse, error) {
	out := RevisionResponse{Sections: []PublishedSection{}}
	if e := s.Authorize(ctx, scope, false); e != nil {
		return out, e
	}
	r, e := s.Repo.Revision(ctx, scope, id)
	if e != nil {
		return out, e
	}
	out.RevisionView = s.observedView(ctx, r)
	current, e := s.Source.Load(ctx, scope, false)
	if e != nil && !errors.Is(e, ErrSourceChanged) {
		return out, e
	}
	out.SourceCurrent = current.Hash == r.SourceSHA256 && current.Snapshot != nil && current.Snapshot.IRDigest == r.IRDigest
	if r.SnapshotPath != "" {
		snap, e := s.readSnapshot(ctx, scope, r)
		if e != nil {
			return out, e
		}
		out.Coverage = snap.Coverage
	}
	steps, e := s.Repo.Steps(ctx, id)
	if e != nil {
		return out, e
	}
	latest := map[Role]PublishedSection{}
	for _, step := range steps {
		if step.StepKey == "plan" {
			var scope planningScope
			if json.Unmarshal([]byte(step.ReviewJSON), &scope) == nil && scope.Extracted {
				out.Coverage.Warnings = append(append([]string{}, out.Coverage.Warnings...), extractedReviewWarning)
			}
			var p GuidePlan
			if json.Unmarshal([]byte(step.OutputJSON), &p) == nil {
				out.Plan = &p
			}
		}
		if step.Published {
			var section GuideSection
			if json.Unmarshal([]byte(step.OutputJSON), &section) != nil {
				return out, ErrEvidence
			}
			latest[section.Role] = PublishedSection{step.StepKey, step.Version, section}
		}
		if strings.HasPrefix(step.StepKey, "whole_review:") {
			var review GuideReview
			if json.Unmarshal([]byte(step.OutputJSON), &review) == nil {
				out.ReviewSummary = &review
			}
		}
	}
	// A late whole-paper finding does not mutate any published artifact,
	// but its display status must disclose the pending correction immediately.
	for _, step := range steps {
		if !strings.HasPrefix(step.StepKey, "whole_review:") {
			continue
		}
		var review GuideReview
		if json.Unmarshal([]byte(step.OutputJSON), &review) != nil {
			continue
		}
		var bindings []reviewedSection
		if json.Unmarshal([]byte(step.ReviewJSON), &bindings) != nil {
			continue
		}
		for role, section := range latest {
			reviewed := false
			for _, binding := range bindings {
				if binding.Key == section.Key && binding.Version == section.Version {
					reviewed = true
					break
				}
			}
			if !reviewed {
				continue
			}
			for _, finding := range review.Findings {
				if sectionHasTarget(section.GuideSection, GuidePlan{}, finding) {
					section.PublicationStatus = "correction_required"
					if step.Version > section.ChangeVersion {
						section.ChangeVersion = step.Version
					}
					latest[role] = section
					break
				}
			}
		}
	}
	for _, role := range Roles() {
		if section, ok := latest[role]; ok && section.ChangeVersion > after {
			out.Sections = append(out.Sections, section)
		}
	}
	return out, nil
}
func (s Service) Evidence(ctx context.Context, scope Scope, id, evidenceID string) (GuideEvidence, bool, error) {
	if e := s.Authorize(ctx, scope, false); e != nil {
		return GuideEvidence{}, false, e
	}
	rev, e := s.Repo.Revision(ctx, scope, id)
	if e != nil {
		return GuideEvidence{}, false, e
	}
	snap, e := s.readSnapshot(ctx, scope, rev)
	if e != nil {
		return GuideEvidence{}, false, e
	}
	current, e := s.Source.Load(ctx, scope, false)
	if e != nil && !errors.Is(e, ErrSourceChanged) {
		return GuideEvidence{}, false, e
	}
	same := current.Hash == rev.SourceSHA256 && current.Snapshot != nil && current.Snapshot.IRDigest == rev.IRDigest
	for _, ev := range snap.Evidence {
		if ev.ID == evidenceID {
			if !same {
				ev.Locator = GuideLocator{Kind: "page", Page: ev.Locator.Page}
			}
			return ev, same, nil
		}
	}
	return GuideEvidence{}, same, ErrEvidence
}
func (s Service) Selection(ctx context.Context, scope Scope, id string, in SelectionRequest) (SelectionContext, error) {
	out := SelectionContext{RevisionID: id, SectionKey: in.SectionKey, BlockID: in.BlockID, Evidence: []GuideEvidence{}, Claims: []GuideClaim{}}
	if e := s.Authorize(ctx, scope, false); e != nil {
		return out, e
	}
	rev, e := s.Repo.Revision(ctx, scope, id)
	if e != nil {
		return out, e
	}
	var step orm.PaperGuideStep
	if e = s.Repo.DB.WithContext(ctx).Where("revision_id = ? AND step_key = ? AND published = ?", id, in.SectionKey, true).Take(&step).Error; e != nil {
		return out, ErrEvidence
	}
	var section GuideSection
	if json.Unmarshal([]byte(step.OutputJSON), &section) != nil {
		return out, ErrEvidence
	}
	var block *GuideBlock
	for i := range section.Blocks {
		if section.Blocks[i].ID == in.BlockID {
			block = &section.Blocks[i]
			break
		}
	}
	if block == nil || in.Start < 0 || in.End <= in.Start || in.End > len([]rune(block.Text)) || in.End-in.Start > 8000 {
		return out, ErrEvidence
	}
	out.Text = string([]rune(block.Text)[in.Start:in.End])
	out.SourceSHA256 = rev.SourceSHA256
	out.IRDigest = rev.IRDigest
	var planStep orm.PaperGuideStep
	var plan GuidePlan
	if s.Repo.DB.WithContext(ctx).Where("revision_id = ? AND step_key = ?", id, "plan").Take(&planStep).Error != nil || json.Unmarshal([]byte(planStep.OutputJSON), &plan) != nil {
		return out, ErrEvidence
	}
	ids := append([]string{}, block.EvidenceIDs...)
	for _, c := range append(plan.Claims, section.Claims...) {
		if contains(block.ClaimIDs, c.ID) {
			out.Claims = append(out.Claims, c)
			ids = append(ids, c.EvidenceIDs...)
		}
	}
	snapshot, e := s.readSnapshot(ctx, scope, rev)
	if e != nil {
		return out, e
	}
	current, e := s.Source.Load(ctx, scope, false)
	if e != nil && !errors.Is(e, ErrSourceChanged) {
		return out, e
	}
	out.SourceCurrent = current.Hash == rev.SourceSHA256 && current.Snapshot != nil && current.Snapshot.IRDigest == rev.IRDigest
	for _, evID := range unique(ids) {
		found := false
		for _, ev := range snapshot.Evidence {
			if ev.ID != evID {
				continue
			}
			found = true
			if !out.SourceCurrent {
				ev.Locator = GuideLocator{Kind: "page", Page: ev.Locator.Page}
			}
			out.Evidence = append(out.Evidence, ev)
			break
		}
		if !found {
			return out, ErrEvidence
		}
	}

	return out, nil
}
func (s Service) SaveProgress(ctx context.Context, scope Scope, in ProgressRequest) error {
	if in.Mode != "article" && in.Mode != "source" {
		return ErrEvidence
	}
	if in.Offset < 0 {
		return ErrEvidence
	}
	end := in.Offset
	if end == 0 {
		end = 1
	}
	_, e := s.Selection(ctx, scope, in.RevisionID, SelectionRequest{in.SectionKey, in.BlockID, end - 1, end})
	if e != nil {
		return e
	}
	row := orm.PaperGuideProgress{OwnerID: scope.OwnerID, RevisionID: in.RevisionID, SectionKey: in.SectionKey, BlockID: in.BlockID, Offset: in.Offset, Mode: in.Mode, UpdatedAt: time.Now().UTC()}
	return s.Repo.DB.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}, {Name: "revision_id"}}, DoUpdates: clause.AssignmentColumns([]string{"section_key", "block_id", "offset", "mode", "updated_at"})}).Create(&row).Error
}
func contains(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

// CallError exposes only a stable class from the structured-task transport.
// The provider's raw error and runtime credentials must never be persisted.
type CallError struct{ Class string }

func (e *CallError) Error() string { return "paper_guide_generation_failed" }

func (s Service) readSnapshot(ctx context.Context, scope Scope, revision orm.PaperGuideRevision) (GuideSourceSnapshot, error) {
	snapshot, err := s.Source.Read(ctx, revision.SnapshotPath)
	if err != nil {
		return snapshot, err
	}
	identity := SourceIdentity{SourceSHA256: revision.SourceSHA256, OwnerID: scope.OwnerID, DatasetID: scope.DatasetID, DocumentID: scope.DocumentID}
	if err = ValidateSnapshot(snapshot, identity, revision.IRDigest); err != nil {
		return GuideSourceSnapshot{}, ErrEvidence
	}
	return snapshot, nil
}

func (s Service) observedView(ctx context.Context, r orm.PaperGuideRevision) RevisionView {
	out := view(r)
	if r.Stage != "completed" && errors.Is(s.Repo.CheckContract(ctx, r), ErrRegenerate) {
		out.Run.ErrorCode = ErrRegenerate.Error()
		if r.Stage != "canceled" && r.Stage != "source_changed" {
			out.Run.Stage = "paused"
		}
		return out
	}
	if r.Stage == "completed" || r.Stage == "canceled" || r.Stage == "source_changed" || r.Stage == "paused" || r.Stage == "failed" {
		return out
	}
	var job orm.AsyncJob
	if s.Repo.DB.WithContext(ctx).Where("id = ?", r.JobID).Take(&job).Error == nil {
		if job.Status == "failed" {
			out.Run.Stage = "paused"
			out.Run.ErrorCode = ErrGeneration.Error()
		}
		if job.Status == "canceled" {
			out.Run.Stage = "canceled"
		}
	}
	return out
}
