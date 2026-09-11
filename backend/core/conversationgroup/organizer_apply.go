package conversationgroup

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"sort"
	"strings"
	"time"
)

func applyProposal(ctx context.Context, db *gorm.DB, run orm.ConversationOrganizerRun, job asyncjob.Job, p organizerProposal, raw json.RawMessage) error {
	return UserTransaction(ctx, db, run.UserID, func(tx *gorm.DB) error {
		if err := validateProposal(run.SnapshotJSON, p); err != nil {
			return err
		}
		var owned int64
		if err := tx.Model(&orm.AsyncJob{}).Where("id=? AND status=? AND attempt_count=? AND lock_until>?", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Count(&owned).Error; err != nil {
			return err
		}
		if owned != 1 {
			return errLeaseLost
		}
		if res := tx.Model(&orm.ConversationOrganizerRun{}).Where("id=? AND status=? AND job_id=?", run.ID, "running", job.ID).Where("EXISTS (SELECT 1 FROM async_jobs WHERE id=? AND status=? AND attempt_count=? AND lock_until>?)", job.ID, asyncjob.StatusRunning, job.AttemptCount, time.Now().UTC()).Updates(map[string]any{"status": "applying", "stage": "applying", "proposal_json": raw, "updated_at": time.Now().UTC(), "version": gorm.Expr("version + 1")}); res.Error != nil || res.RowsAffected != 1 {
			if res.Error != nil {
				return res.Error
			}
			return errLeaseLost
		}
		allowed := map[string]bool{}
		var snapshot organizerSnapshot
		if err := json.Unmarshal(run.SnapshotJSON, &snapshot); err != nil {
			return err
		}
		snapshotIDs := snapshot.conversationIDs()
		for _, id := range snapshotIDs {
			allowed[id] = true
		}
		seen := map[string]bool{}
		skipReasons := map[string]string{}
		controlledGroupVersions := map[string]int64{}
		organized := 0
		sort.Strings(snapshotIDs)
		var lockedConversations []orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND create_user_id=?", snapshotIDs, run.UserID).Order("id").Find(&lockedConversations).Error; err != nil {
			return err
		}
		formalIDs := make([]string, 0, len(p.ExistingGroupAssignments))
		for _, assignment := range p.ExistingGroupAssignments {
			formalIDs = append(formalIDs, assignment.GroupID)
		}
		sort.Strings(formalIDs)
		if len(formalIDs) > 0 {
			var lockedGroups []orm.ConversationGroup
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND user_id=?", formalIDs, run.UserID).Order("id").Find(&lockedGroups).Error; err != nil {
				return err
			}
		}
		var lockedStates []orm.ConversationGroupState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id IN ? AND user_id=?", snapshotIDs, run.UserID).Order("conversation_id").Find(&lockedStates).Error; err != nil {
			return err
		}
		var lockedMembers []orm.ConversationGroupMember
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id IN ? AND user_id=?", snapshotIDs, run.UserID).Order("conversation_id").Find(&lockedMembers).Error; err != nil {
			return err
		}
		conversationByID := make(map[string]orm.Conversation, len(lockedConversations))
		for _, conv := range lockedConversations {
			conversationByID[conv.ID] = conv
		}
		memberByID := make(map[string]bool, len(lockedMembers))
		for _, member := range lockedMembers {
			memberByID[member.ConversationID] = true
		}
		validConversation := func(id string) bool {
			if !allowed[id] || seen[id] {
				return false
			}
			seen[id] = true
			conv, exists := conversationByID[id]
			state := struct{ ConversationExists, Deleted, Archived, Grouped bool }{
				exists, conv.DeletedAt != nil, conv.ArchivedAt != nil, memberByID[id],
			}
			if state.ConversationExists && !state.Deleted && !state.Archived && !state.Grouped {
				return true
			}
			switch {
			case !state.ConversationExists:
				skipReasons[id] = "conversation_missing"
			case state.Deleted:
				skipReasons[id] = "conversation_deleted"
			case state.Archived:
				skipReasons[id] = "conversation_archived"
			case state.Grouped:
				skipReasons[id] = "membership_changed"
			}
			return false
		}
		for _, candidate := range p.NewGroups {
			ids := candidate.ConversationIDs
			valid := make([]string, 0, len(ids))
			for _, id := range ids {
				if validConversation(id) {
					valid = append(valid, id)
				}
			}
			if len(valid) < 3 {
				for _, id := range valid {
					skipReasons[id] = "candidate_below_minimum"
				}
				continue
			}
			name, scope, err := validateGroupInput(groupInput{Name: &candidate.Name, Scope: &candidate.Scope}, true)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			group := orm.ConversationGroup{ID: uuid.NewString(), UserID: run.UserID, Name: name, NormalizedName: normalizeName(name), Scope: scope, Version: 1, CreatedBy: CreatedByOrganizer, CreatedRunID: run.ID, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&group).Error; err != nil {
				return err
			}
			controlledGroupVersions[group.ID] = group.Version
			for _, id := range valid {
				moved, err := moveMembershipTx(tx, run.UserID, id, &group.ID, CreatedByOrganizer, run.ID)
				if err != nil {
					return err
				}
				if err := recordChange(tx, run.ID, id, moved.BeforeGroupID, moved.AfterGroupID, moved.Revision, "organize"); err != nil {
					return err
				}
				organized++
			}
		}
		for _, assignment := range p.ExistingGroupAssignments {
			var group orm.ConversationGroup
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND version=? AND deleted_at IS NULL", assignment.GroupID, run.UserID, assignment.GroupVersion).Take(&group).Error; err != nil {
				for _, id := range assignment.ConversationIDs {
					seen[id] = true
					skipReasons[id] = "group_scope_changed"
				}
				continue
			}
			for _, id := range assignment.ConversationIDs {
				if !validConversation(id) {
					continue
				}
				moved, err := moveMembershipTx(tx, run.UserID, id, &group.ID, CreatedByOrganizer, run.ID)
				if err != nil {
					return err
				}
				if err := recordChange(tx, run.ID, id, moved.BeforeGroupID, moved.AfterGroupID, moved.Revision, "organize"); err != nil {
					return err
				}
				organized++
			}
		}
		for _, id := range p.FreeConversationIDs {
			if allowed[id] && !seen[id] {
				seen[id] = true
			}
		}
		now := time.Now().UTC()
		reasons := p.UnassignedReasons
		if reasons == nil {
			reasons = map[string]string{}
		}
		summaryErrors := map[string]string{}
		total := len(snapshotIDs)
		if len(run.PreparationJSON) > 0 {
			var prep organizerPreparation
			if err := json.Unmarshal(run.PreparationJSON, &prep); err != nil {
				return err
			}
			var rows []orm.ConversationOrganizerSnapshotItem
			if err := tx.Select("conversation_id,preparation_reason,preparation_error").Where("run_id=?", run.ID).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				prep.Items = append(prep.Items, preparationItem{Conversation: snapshotConversation{ID: row.ConversationID}, Reason: row.PreparationReason, ErrorCode: row.PreparationError})
			}
			total = len(prep.Items)
			for _, item := range prep.Items {
				if item.Reason != "" {
					reasons[item.Conversation.ID] = item.Reason
				}
				if item.ErrorCode != "" {
					summaryErrors[item.Conversation.ID] = item.ErrorCode
				}
			}
		}
		for id, reason := range skipReasons {
			if reason == "candidate_below_minimum" {
				reasons[id] = "below_min_group_size"
				delete(skipReasons, id)
			}
		}
		// Only application conflicts count as skipped; all ungrouped items count as free.
		skipped := len(skipReasons)
		result, _ := json.Marshal(organizerResult{OrganizedCount: organized, FreeCount: total - organized, SkippedCount: skipped, SkipReasons: skipReasons, UnassignedReasons: reasons, SummaryErrors: summaryErrors, ControlledGroupVersions: controlledGroupVersions})
		return tx.Model(&orm.ConversationOrganizerRun{}).Where("id=? AND status=?", run.ID, "applying").Updates(map[string]any{"status": "succeeded", "stage": "completed", "result_json": result, "progress_current": len(snapshotIDs), "finished_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
}

func validateProposal(snapshotRaw json.RawMessage, p organizerProposal) error {
	var snapshot organizerSnapshot
	if err := json.Unmarshal(snapshotRaw, &snapshot); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, item := range snapshot.Conversations {
		want[item.ID] = true
	}
	groups := map[string]int64{}
	for _, group := range snapshot.Groups {
		groups[group.ID] = group.Version
	}
	seen := map[string]bool{}
	accept := func(ids []string) error {
		for _, id := range ids {
			if !want[id] {
				return errors.New("proposal contains unknown conversation")
			}
			if seen[id] {
				return errors.New("proposal contains duplicate conversation")
			}
			seen[id] = true
		}
		return nil
	}
	candidates := map[string]bool{}
	for _, group := range p.NewGroups {
		if strings.TrimSpace(group.CandidateID) == "" || candidates[group.CandidateID] {
			return errors.New("proposal contains invalid candidate id")
		}
		candidates[group.CandidateID] = true
		if strings.TrimSpace(group.Scope) == "" {
			return errors.New("automatic conversation group scope required")
		}
		if _, _, err := validateGroupInput(groupInput{Name: &group.Name, Scope: &group.Scope}, true); err != nil {
			return err
		}
		if err := accept(group.ConversationIDs); err != nil {
			return err
		}
	}
	assignedGroups := map[string]bool{}
	for _, assignment := range p.ExistingGroupAssignments {
		version, ok := groups[assignment.GroupID]
		if !ok || version != assignment.GroupVersion {
			return errors.New("proposal references stale conversation group")
		}
		if assignedGroups[assignment.GroupID] {
			return errors.New("proposal repeats conversation group")
		}
		assignedGroups[assignment.GroupID] = true
		if err := accept(assignment.ConversationIDs); err != nil {
			return err
		}
	}
	if err := accept(p.FreeConversationIDs); err != nil {
		return err
	}
	free := map[string]bool{}
	for _, id := range p.FreeConversationIDs {
		free[id] = true
	}
	for id, reason := range p.UnassignedReasons {
		if !free[id] || (reason != "no_matching_group" && reason != "below_min_group_size") {
			return errors.New("proposal contains invalid unassigned reason")
		}
	}
	if len(seen) != len(want) {
		return errors.New("proposal does not partition the snapshot")
	}
	return nil
}

func recordChange(tx *gorm.DB, runID, cid string, before, after *string, revision int64, kind string) error {
	return tx.Create(&orm.ConversationOrganizerChange{ID: uuid.NewString(), RunID: runID, ConversationID: cid, BeforeGroupID: before, AfterGroupID: after, AfterMemberRevision: revision, Kind: kind, CreatedAt: time.Now().UTC()}).Error
}
