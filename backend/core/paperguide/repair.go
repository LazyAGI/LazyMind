package paperguide

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

const (
	maxRepairReservations   = 2
	repairReservationPrefix = "repair-reservation:"
	repairTargetPrefix      = "correction:"
)

type sectionRepair struct {
	base     GuideSection
	findings []GuideFinding
	whole    bool
}

type repairCheckpoint struct {
	Key        string `json:"key"`
	ID         string `json:"id"`
	OutputHash string `json:"output_hash"`
}

type repairReservation struct {
	Contract    string           `json:"contract"`
	RepairKey   string           `json:"repair_key"`
	Mode        string           `json:"mode"`
	Review      repairCheckpoint `json:"review"`
	Base        repairCheckpoint `json:"base"`
	BaseVersion int              `json:"base_version"`
	Findings    []GuideFinding   `json:"findings"`
}

func sectionFindings(section GuideSection, findings []GuideFinding) []GuideFinding {
	var assigned []GuideFinding
	for _, finding := range findings {
		if finding.TargetType == "block" && slices.ContainsFunc(section.Blocks, func(block GuideBlock) bool { return block.ID == finding.TargetID }) ||
			finding.TargetType == "claim" && contains(section.ClaimIDs, finding.TargetID) {
			assigned = append(assigned, finding)
		}
	}
	return assigned
}

func repairTargetKeys(findings []GuideFinding) []string {
	keys := make([]string, 0, len(findings))
	for _, finding := range findings {
		keys = append(keys, repairTargetPrefix+finding.TargetType+":"+finding.TargetID)
	}
	return unique(keys)
}

func checkpointIdentity(step orm.PaperGuideStep) repairCheckpoint {
	return repairCheckpoint{Key: step.StepKey, ID: step.ID, OutputHash: digest(step.OutputJSON)}
}

// The reservation and all target owners are committed together before any
// request marker. Replays must match the same original checkpoints and batch.
func (x *execution) reserveRepair(ctx context.Context, p GuidePlan, role Role, version int, repair sectionRepair, replayOnly bool) error {
	if version < 2 || version > 3 || repair.base.Role != role || len(repair.findings) == 0 {
		return ErrQuality
	}
	key := sectionKey(role, version)
	base := sectionKey(role, version-1)
	baseKey, reviewKey, mode := base+":draft", base+":review", "local"
	if repair.whole {
		if version != 2 || repair.base.Version != 1 || repair.base.PublicationStatus != "published" {
			return ErrQuality
		}
		baseKey, reviewKey, mode = base, "whole_review:1", "whole"
	}
	return x.s.Repo.fenced(ctx, x.f, x.rev.ID, x.rev.Epoch, func(tx *gorm.DB, r *orm.PaperGuideRevision, _ *orm.PaperGuide) error {
		if err := (Repository{DB: tx}).CheckContract(ctx, *r); err != nil {
			return err
		}
		var checkpoints []orm.PaperGuideStep
		if err := tx.Where("revision_id = ? AND step_key IN ?", r.ID, []string{baseKey, reviewKey}).Find(&checkpoints).Error; err != nil {
			return err
		}
		if len(checkpoints) != 2 {
			return ErrEvidence
		}
		baseStep, reviewStep := checkpoints[0], checkpoints[1]
		if baseStep.StepKey != baseKey {
			baseStep, reviewStep = reviewStep, baseStep
		}
		var storedBase GuideSection
		var review GuideReview
		if baseStep.Published != repair.whole || reviewStep.Published ||
			decodeStrict([]byte(baseStep.OutputJSON), &storedBase) != nil || digest(storedBase) != digest(repair.base) ||
			decodeStrict([]byte(reviewStep.OutputJSON), &review) != nil {
			return ErrEvidence
		}
		if repair.whole {
			// execute has validated the review input and all ordered
			// section bindings. Refuse a changed checkpoint within this fence.
			saved := x.steps[reviewKey]
			if reviewStep.InputHash != saved.InputHash || reviewStep.ReviewJSON != saved.ReviewJSON || reviewStep.OutputJSON != saved.OutputJSON {
				return ErrConflict
			}
		} else if ValidateReview(p, []GuideSection{storedBase}, review) != nil || len(review.Findings) != len(repair.findings) {
			return ErrEvidence
		}
		if !slices.Equal(sectionFindings(storedBase, review.Findings), repair.findings) {
			return ErrQuality
		}

		reservation := repairReservation{
			Contract: TaskVersion, RepairKey: key, Mode: mode, Review: checkpointIdentity(reviewStep),
			Base: checkpointIdentity(baseStep), BaseVersion: version - 1, Findings: repair.findings,
		}
		return persistRepairReservation(tx, r, reservation, replayOnly)
	})
}

func persistRepairReservation(tx *gorm.DB, rev *orm.PaperGuideRevision, reservation repairReservation, replayOnly bool) error {
	var steps []orm.PaperGuideStep
	if err := tx.Where("revision_id = ? AND (step_key LIKE ? OR step_key LIKE ?)", rev.ID, repairReservationPrefix+"%", repairTargetPrefix+"%").Find(&steps).Error; err != nil {
		return err
	}

	// Check the entire bounded ledger before reuse or insertion. Old target
	// markers and partially missing batches are not a basis for new spending.
	reservations := map[string]orm.PaperGuideStep{}
	markers := map[string]orm.PaperGuideStep{}
	owners := map[string]string{}
	for _, step := range steps {
		if step.Published || step.ReviewJSON != "" {
			return ErrQuality
		}
		if strings.HasPrefix(step.StepKey, repairTargetPrefix) {
			markers[step.StepKey] = step
			continue
		}
		var saved repairReservation
		if decodeStrict([]byte(step.OutputJSON), &saved) != nil || saved.Contract != TaskVersion ||
			step.StepKey != repairReservationPrefix+saved.RepairKey || step.InputHash != digest(saved) ||
			len(saved.Findings) == 0 || len(saved.Findings) > 48 {
			return ErrQuality
		}
		reservations[step.StepKey] = step
		for _, target := range repairTargetKeys(saved.Findings) {
			if _, exists := owners[target]; exists {
				return ErrQuality
			}
			owners[target] = step.StepKey
		}
	}
	if len(reservations) != rev.CorrectionCount || len(reservations) > maxRepairReservations || len(markers) != len(owners) {
		return ErrQuality
	}
	for target, owner := range owners {
		if marker, ok := markers[target]; !ok || marker.InputHash != reservations[owner].InputHash || marker.OutputJSON != string(mustMarshal(owner)) {
			return ErrQuality
		}
	}

	key := repairReservationPrefix + reservation.RepairKey
	hash, raw := digest(reservation), string(mustMarshal(reservation))
	if saved, exists := reservations[key]; exists {
		if saved.InputHash != hash || saved.OutputJSON != raw {
			return ErrQuality
		}
		return nil
	}
	if replayOnly || rev.CorrectionCount >= maxRepairReservations {
		return ErrQuality
	}
	targets := repairTargetKeys(reservation.Findings)
	for _, target := range targets {
		if _, exists := owners[target]; exists {
			return ErrQuality
		}
	}
	attempts, err := requestHashes(tx, rev.ID, reservation.RepairKey+":draft")
	if err != nil {
		return err
	}
	var draft orm.PaperGuideStep
	err = tx.Where("revision_id = ? AND step_key = ?", rev.ID, reservation.RepairKey+":draft").Take(&draft).Error
	if len(attempts) > 0 || err == nil {
		return ErrQuality
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	now := time.Now().UTC()
	writes := make([]orm.PaperGuideStep, 0, len(targets)+1)
	writes = append(writes, orm.PaperGuideStep{ID: uuid.NewString(), RevisionID: rev.ID, StepKey: key, InputHash: hash, OutputJSON: raw, Epoch: rev.Epoch, CreatedAt: now})
	for _, target := range targets {
		writes = append(writes, orm.PaperGuideStep{ID: uuid.NewString(), RevisionID: rev.ID, StepKey: target, InputHash: hash, OutputJSON: string(mustMarshal(key)), Epoch: rev.Epoch, CreatedAt: now})
	}
	if err := tx.Create(&writes).Error; err != nil {
		return err
	}
	return tx.Model(rev).Update("correction_count", rev.CorrectionCount+1).Error
}
