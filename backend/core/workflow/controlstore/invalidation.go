package controlstore

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
	"time"
)

func InvalidateAttempts(tx *gorm.DB, session *orm.WorkflowSession, queue []orm.WorkflowSessionStep) error {
	seen := map[string]bool{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current.ID] {
			continue
		}
		seen[current.ID] = true
		// Revoke only executions in the dependency invalidation closure. The session
		// lock makes this atomic with publication and the replacement execution.
		updates := map[string]any{"validity": "stale"}
		switch current.Status {
		case "pending", "queued", "claimed", "running":
			now := time.Now().UTC()
			updates["status"] = "cancelled"
			updates["lease_token"] = ""
			updates["lease_expires_at"] = nil
			updates["fencing_generation"] = gorm.Expr("fencing_generation + 1")
			updates["terminal_code"] = "WORKFLOW_REGENERATED"
			updates["updated_at"] = now
			if err := tx.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ? AND status IN ?", current.ID, []string{"pending", "claimed"}).
				Updates(map[string]any{"status": "cancelled", "updated_at": now}).Error; err != nil {
				return err
			}
			if current.TaskID != "" {
				if err := tx.Model(&orm.SubAgentTask{}).Where("id = ? AND status IN ?", current.TaskID, []string{"pending", "running"}).
					Updates(map[string]any{"status": "interrupted", "updated_at": now}).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Model(&orm.WorkflowSessionStep{}).Where("id = ?", current.ID).Updates(updates).Error; err != nil {
			return err
		}
		var outputs []orm.WorkflowSlotRevision
		if err := tx.Where("session_id = ? AND ((producer_attempt_id = ? AND producer_attempt_id != '') OR (step_id = ? AND attempt = ?))", session.ID, current.ID, current.StepID, current.Attempt).Find(&outputs).Error; err != nil {
			return err
		}
		for _, output := range outputs {
			if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", output.ID).Updates(map[string]any{"validity": "stale", "selected": false}).Error; err != nil {
				return err
			}
			var bindings []orm.WorkflowAttemptInputBinding
			if err := tx.Where("material_revision_id = ?", output.ID).Find(&bindings).Error; err != nil {
				return err
			}
			for _, binding := range bindings {
				var consumer orm.WorkflowSessionStep
				if tx.Where("id = ? AND validity = ?", binding.AttemptID, "effective").First(&consumer).Error == nil {
					queue = append(queue, consumer)
				}
			}
			var decisions []orm.WorkflowRouteDecision
			if err := tx.Where("session_id = ? AND validity = ?", session.ID, "effective").Find(&decisions).Error; err != nil {
				return err
			}
			for _, decision := range decisions {
				var witnesses []graphengine.Witness
				_ = json.Unmarshal(decision.WitnessJSON, &witnesses)
				usesRevision := false
				for _, witness := range witnesses {
					if witness.RevisionID == output.ID {
						usesRevision = true
						break
					}
				}
				if usesRevision {
					if err := EnqueueExclusiveRouteAttempts(tx, session.ID, decision, &queue); err != nil {
						return err
					}
					if err := tx.Model(&orm.WorkflowRouteDecision{}).Where("id = ?", decision.ID).Update("validity", "stale").Error; err != nil {
						return err
					}
				}
			}
		}
		var sourceDecisions []orm.WorkflowRouteDecision
		if err := tx.Where("session_id = ? AND source_attempt_id IN ? AND validity = ?", session.ID, []string{current.ID, current.TaskID}, "effective").Find(&sourceDecisions).Error; err != nil {
			return err
		}
		for _, decision := range sourceDecisions {
			if err := EnqueueExclusiveRouteAttempts(tx, session.ID, decision, &queue); err != nil {
				return err
			}
			if err := tx.Model(&orm.WorkflowRouteDecision{}).Where("id = ?", decision.ID).Update("validity", "stale").Error; err != nil {
				return err
			}
		}
	}
	if Controlled(*session) {
		return PruneListOrders(tx, session.ID)
	}
	return nil
}

func EnqueueExclusiveRouteAttempts(tx *gorm.DB, sessionID string, decision orm.WorkflowRouteDecision, queue *[]orm.WorkflowSessionStep) error {
	var targets []string
	_ = json.Unmarshal(decision.ActivatedJSON, &targets)
	for _, target := range targets {
		if target == "__end__" {
			continue
		}
		var other []orm.WorkflowRouteDecision
		if err := tx.Where("session_id = ? AND validity = ? AND id != ?", sessionID, "effective", decision.ID).Find(&other).Error; err != nil {
			return err
		}
		stillActivated := false
		for _, candidate := range other {
			var activated []string
			_ = json.Unmarshal(candidate.ActivatedJSON, &activated)
			for _, value := range activated {
				if value == target {
					stillActivated = true
					break
				}
			}
			if stillActivated {
				break
			}
		}
		if stillActivated {
			continue
		}
		var attempt orm.WorkflowSessionStep
		query := tx.Where("session_id = ? AND step_id = ? AND validity = ?", sessionID, target, "effective").Order("attempt DESC").First(&attempt)
		if query.Error == nil {
			*queue = append(*queue, attempt)
		} else if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return query.Error
		}
	}
	return nil
}

// Recovery replaces a collection; historical item identities are retained for
// lineage, but must no longer participate in current display/export ordering.
func PruneListOrders(tx *gorm.DB, sessionID string) error {
	var orders []orm.WorkflowSlotOrder
	if err := tx.Where("session_id = ?", sessionID).Find(&orders).Error; err != nil {
		return err
	}
	for _, order := range orders {
		var indices []int
		if err := json.Unmarshal(order.OrderList, &indices); err != nil {
			return err
		}
		var current []int
		if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ? AND slot_id = ? AND selected = ? AND validity = ? AND list_index IS NOT NULL", sessionID, order.SlotID, true, "effective").Pluck("list_index", &current).Error; err != nil {
			return err
		}
		valid := map[int]bool{}
		for _, index := range current {
			valid[index] = true
		}
		kept := []int{}
		for _, index := range indices {
			if valid[index] {
				kept = append(kept, index)
			}
		}
		if len(kept) == len(indices) {
			continue
		}
		raw, _ := json.Marshal(kept)
		if err := tx.Model(&orm.WorkflowSlotOrder{}).Where("session_id = ? AND slot_id = ?", sessionID, order.SlotID).Updates(map[string]any{"order_list": raw, "order_version": gorm.Expr("order_version + 1")}).Error; err != nil {
			return err
		}
	}
	return nil
}
