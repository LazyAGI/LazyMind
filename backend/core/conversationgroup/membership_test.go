package conversationgroup

import (
	"errors"
	"testing"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

func TestMembershipTransitionPreservesFreeFenceAndRollsBackTogether(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{})
	// A legacy member with no state must still report its original group.
	if err := db.Create(&orm.ConversationGroupMember{ConversationID: "c", UserID: "u", GroupID: "old", Revision: 7, Source: CreatedByUser}).Error; err != nil {
		t.Fatal(err)
	}
	apply := func(target *string, run string) membershipChange {
		t.Helper()
		var change membershipChange
		err := db.Transaction(func(tx *gorm.DB) error {
			var err error
			change, err = moveMembershipTx(tx, "u", "c", target, CreatedByUser, run)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return change
	}
	first := apply(nil, "run")
	if first.BeforeGroupID == nil || *first.BeforeGroupID != "old" || first.Revision != 1 {
		t.Fatalf("lost legacy provenance: %+v", first)
	}
	group := "new"
	second := apply(&group, "run")
	if second.BeforeGroupID != nil || second.Revision != 2 {
		t.Fatalf("lost free revision: %+v", second)
	}
	// Explicitly moving to the same group revokes the previous run's undo authority.
	third := apply(&group, "")
	if third.Revision != 3 {
		t.Fatalf("same-group action did not fence: %+v", third)
	}
	rollback := errors.New("rollback fixture")
	err := db.Transaction(func(tx *gorm.DB) error {
		if _, err := moveMembershipTx(tx, "u", "c", nil, CreatedByUser, ""); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var member orm.ConversationGroupMember
	var state orm.ConversationGroupState
	if err := db.Where("conversation_id=?", "c").Take(&member).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("conversation_id=?", "c").Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if member.GroupID != group || member.Revision != 2 || state.GroupID == nil || *state.GroupID != group || state.Revision != 3 || state.SourceRunID != "" {
		t.Fatalf("membership and fence diverged: member=%+v state=%+v", member, state)
	}
}
