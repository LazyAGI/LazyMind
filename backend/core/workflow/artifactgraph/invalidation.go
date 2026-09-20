package artifactgraph

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

var ErrArtifactInUse error = artifactGraphError("ARTIFACT_IN_USE")

type artifactGraphError string

func (e artifactGraphError) Error() string { return string(e) }

// LockSession serializes dependency binding and Artifact mutation decisions.
func LockSession(tx *gorm.DB, sessionID string) (*orm.WorkflowSession, error) {
	var session orm.WorkflowSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", sessionID).First(&session).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

// InvalidateConsumers marks terminal consumers of the exact revisions stale.
// Any reachable effective non-terminal Attempt rejects the enclosing mutation.
func InvalidateConsumers(
	ctx context.Context,
	tx *gorm.DB,
	sessionID string,
	revisionIDs ...string,
) error {
	return walkConsumers(ctx, tx, sessionID, false, nil, revisionIDs...)
}

// InvalidateConsumersPreservingProducer updates publication target metadata
// without invalidating the completed attempt that produced the published draft.
// Other consumers and the live-attempt guard retain normal invalidation rules.
func InvalidateConsumersPreservingProducer(ctx context.Context, tx *gorm.DB, sessionID string, producer orm.WorkflowSlotRevision, revisionIDs ...string) error {
	return walkConsumers(ctx, tx, sessionID, false, &producer, revisionIDs...)
}

// CheckConsumers reports the same live-consumer guard without locks or writes.
// It is an advisory read; mutations still recheck under the Session lock.
func CheckConsumers(ctx context.Context, db *gorm.DB, sessionID string, revisionIDs ...string) error {
	return walkConsumers(ctx, db, sessionID, true, nil, revisionIDs...)
}

func walkConsumers(ctx context.Context, tx *gorm.DB, sessionID string, readOnly bool, preservedProducer *orm.WorkflowSlotRevision, revisionIDs ...string) error {
	engine := invalidationEngine{
		ctx: ctx, tx: tx.WithContext(ctx), sessionID: sessionID, readOnly: readOnly,
		seenAttempts: map[string]bool{}, seenDecisions: map[string]bool{},
		preservedProducer: preservedProducer,
	}
	for _, revisionID := range revisionIDs {
		if strings.TrimSpace(revisionID) == "" {
			continue
		}
		if err := engine.enqueueRevisionConsumers(revisionID); err != nil {
			return err
		}
	}
	return engine.drain()
}

type invalidationEngine struct {
	preservedProducer *orm.WorkflowSlotRevision
	readOnly          bool
	ctx               context.Context
	tx                *gorm.DB
	sessionID         string
	queue             []orm.WorkflowSessionStep
	seenAttempts      map[string]bool
	seenDecisions     map[string]bool
}

func (engine *invalidationEngine) attemptQuery() *gorm.DB {
	if engine.readOnly {
		return engine.tx
	}
	return engine.tx.Clauses(clause.Locking{Strength: "UPDATE"})
}

func terminalAttempt(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "failed", "interrupted", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func (engine *invalidationEngine) enqueueAttempt(attemptID string) error {
	if attemptID == "" || engine.seenAttempts[attemptID] {
		return nil
	}
	var attempt orm.WorkflowSessionStep
	err := engine.attemptQuery().
		Where("id = ? AND session_id = ? AND validity = ?", attemptID, engine.sessionID, "effective").
		First(&attempt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	engine.queue = append(engine.queue, attempt)
	return nil
}

func (engine *invalidationEngine) enqueueRevisionConsumers(revisionID string) error {
	var bindings []orm.WorkflowAttemptInputBinding
	if err := engine.tx.Where("session_id = ? AND material_revision_id = ?", engine.sessionID, revisionID).
		Find(&bindings).Error; err != nil {
		return err
	}
	for _, binding := range bindings {
		if err := engine.enqueueAttempt(binding.AttemptID); err != nil {
			return err
		}
	}
	var decisions []orm.WorkflowRouteDecision
	if err := engine.tx.Where("session_id = ? AND validity = ?", engine.sessionID, "effective").
		Find(&decisions).Error; err != nil {
		return err
	}
	for _, decision := range decisions {
		var witnesses []graphengine.Witness
		_ = json.Unmarshal(decision.WitnessJSON, &witnesses)
		for _, witness := range witnesses {
			if witness.RevisionID == revisionID {
				if err := engine.invalidateDecision(decision); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func (engine *invalidationEngine) drain() error {
	for len(engine.queue) > 0 {
		current := engine.queue[0]
		engine.queue = engine.queue[1:]
		if engine.seenAttempts[current.ID] {
			continue
		}
		engine.seenAttempts[current.ID] = true
		if !terminalAttempt(current.Status) {
			return ErrArtifactInUse
		}
		if producer := engine.preservedProducer; producer != nil && producer.SessionID == engine.sessionID {
			if producer.ProducerAttemptID != "" && (producer.ProducerAttemptID == current.ID || producer.ProducerAttemptID == current.TaskID) ||
				producer.ProducerAttemptID == "" && producer.StepID == current.StepID && producer.Attempt == current.Attempt {
				continue
			}
		}
		if !engine.readOnly {
			updated := engine.tx.Model(&orm.WorkflowSessionStep{}).
				Where("id = ? AND validity = ?", current.ID, "effective").
				Update("validity", "stale")
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected == 0 {
				continue
			}
		}
		var outputs []orm.WorkflowSlotRevision
		if err := engine.tx.Where(
			"session_id = ? AND validity = ? AND ((producer_attempt_id IN ? AND producer_attempt_id != '') OR (producer_attempt_id = '' AND step_id = ? AND attempt = ?))",
			engine.sessionID, "effective", []string{current.ID, current.TaskID}, current.StepID, current.Attempt,
		).Find(&outputs).Error; err != nil {
			return err
		}
		for _, output := range outputs {
			if !engine.readOnly {
				if err := engine.tx.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", output.ID).
					Updates(map[string]any{"validity": "stale", "selected": false}).Error; err != nil {
					return err
				}
			}
			if err := engine.enqueueRevisionConsumers(output.ID); err != nil {
				return err
			}
		}
		var sourceDecisions []orm.WorkflowRouteDecision
		if err := engine.tx.Where(
			"session_id = ? AND source_attempt_id IN ? AND validity = ?",
			engine.sessionID, []string{current.ID, current.TaskID}, "effective",
		).Find(&sourceDecisions).Error; err != nil {
			return err
		}
		for _, decision := range sourceDecisions {
			if err := engine.invalidateDecision(decision); err != nil {
				return err
			}
		}
	}
	return nil
}

func (engine *invalidationEngine) invalidateDecision(decision orm.WorkflowRouteDecision) error {
	if decision.ID == "" || engine.seenDecisions[decision.ID] {
		return nil
	}
	engine.seenDecisions[decision.ID] = true
	if !engine.readOnly {
		updated := engine.tx.Model(&orm.WorkflowRouteDecision{}).
			Where("id = ? AND validity = ?", decision.ID, "effective").
			Update("validity", "stale")
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return nil
		}
	}
	var targets []string
	_ = json.Unmarshal(decision.ActivatedJSON, &targets)
	for _, target := range targets {
		if target == "" || target == "__end__" {
			continue
		}
		shared, err := engine.targetStillActivated(target)
		if err != nil {
			return err
		}
		if shared {
			continue
		}
		var attempt orm.WorkflowSessionStep
		err = engine.attemptQuery().
			Where("session_id = ? AND step_id = ? AND validity = ?", engine.sessionID, target, "effective").
			Order("attempt DESC").First(&attempt).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		engine.queue = append(engine.queue, attempt)
	}
	return nil
}

func (engine *invalidationEngine) targetStillActivated(target string) (bool, error) {
	var decisions []orm.WorkflowRouteDecision
	if err := engine.tx.Where("session_id = ? AND validity = ?", engine.sessionID, "effective").
		Find(&decisions).Error; err != nil {
		return false, err
	}
	for _, decision := range decisions {
		if engine.seenDecisions[decision.ID] {
			continue
		}
		var activated []string
		_ = json.Unmarshal(decision.ActivatedJSON, &activated)
		for _, value := range activated {
			if value == target {
				return true, nil
			}
		}
	}
	return false, nil
}
