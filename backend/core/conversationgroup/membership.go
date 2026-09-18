package conversationgroup

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

type membershipChange struct {
	BeforeGroupID *string
	AfterGroupID  *string
	Revision      int64
}

// moveMembershipTx is the sole writer of membership and its durable undo fence.
// The caller owns the transaction, conversation lock, authorization, target-group
// validation, and any organizer conflict checks. Even a same-group move advances
// the fence: an explicit user action must prevent an older organizer undo.
func moveMembershipTx(tx *gorm.DB, uid, cid string, target *string, source, runID string) (membershipChange, error) {
	var count int64
	q := tx.Table("conversation_groups g").Where("g.user_id=? AND g.kind=?", uid, KindProject)
	if target != nil {
		q = q.Where("g.id=? OR g.id IN (SELECT group_id FROM conversation_group_members WHERE conversation_id=? AND user_id=?)", *target, cid, uid)
	} else {
		q = q.Where("g.id IN (SELECT group_id FROM conversation_group_members WHERE conversation_id=? AND user_id=?)", cid, uid)
	}
	if err := q.Count(&count).Error; err != nil {
		return membershipChange{}, err
	}
	if count != 0 {
		return membershipChange{}, projectError("membership_locked", 409)
	}
	return writeMembershipTx(tx, uid, cid, target, source, runID)
}

// Only new project conversations may bypass the immutable-membership check.
func writeMembershipTx(tx *gorm.DB, uid, cid string, target *string, source, runID string) (membershipChange, error) {
	var state orm.ConversationGroupState
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=? AND user_id=?", cid, uid).Take(&state).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return membershipChange{}, err
	}
	exists := err == nil
	// Membership is authoritative for the previous group; state only carries the undo fence.
	var before *string
	var current orm.ConversationGroupMember
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_id=? AND user_id=?", cid, uid).Take(&current).Error
	if err == nil {
		before = groupIDOrNil(current.GroupID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return membershipChange{}, err
	}
	now := time.Now().UTC()
	change := membershipChange{BeforeGroupID: before, AfterGroupID: target, Revision: state.Revision + 1}
	if target == nil {
		err = tx.Where("conversation_id=? AND user_id=?", cid, uid).Delete(&orm.ConversationGroupMember{}).Error
	} else {
		member := orm.ConversationGroupMember{ConversationID: cid, UserID: uid, GroupID: *target, Revision: 1, Source: source, SourceRunID: runID, CreatedAt: now, UpdatedAt: now}
		err = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "conversation_id"}}, DoUpdates: clause.Assignments(map[string]any{
			"group_id": *target, "revision": gorm.Expr("conversation_group_members.revision + 1"), "source": source, "source_run_id": runID, "updated_at": now,
		})}).Create(&member).Error
	}
	if err != nil {
		return membershipChange{}, err
	}
	if !exists {
		state = orm.ConversationGroupState{ConversationID: cid, UserID: uid, GroupID: target, Revision: change.Revision, SourceRunID: runID, UpdatedAt: now}
		err = tx.Create(&state).Error
	} else {
		err = tx.Model(&state).Updates(map[string]any{"group_id": target, "revision": change.Revision, "source_run_id": runID, "updated_at": now}).Error
	}
	return change, err
}
