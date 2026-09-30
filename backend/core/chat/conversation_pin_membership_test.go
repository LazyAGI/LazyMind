package chat

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestConversationPinLeavesOrdinaryGroupWithoutChangingActivity(t *testing.T) {
	for _, kind := range []string{"group", "project"} {
		t.Run(kind, func(t *testing.T) {
			db := newPromptTestDB(t).DB
			before := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
			conversation := orm.Conversation{ID: "member", BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: before, UpdatedAt: before}}
			group := orm.ConversationGroup{ID: "group", UserID: "u1", Kind: kind, Name: "Group", NormalizedName: "group"}
			for _, row := range []any{&conversation, &group,
				&orm.ConversationGroupMember{ConversationID: conversation.ID, GroupID: group.ID, UserID: "u1", Revision: 4},
				&orm.ConversationGroupState{ConversationID: conversation.ID, GroupID: &group.ID, UserID: "u1", Revision: 4},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := updateConversationPin(t.Context(), db, "u1", conversation.ID, true); err != nil {
				t.Fatal(err)
			}
			var count int64
			db.Model(&orm.ConversationGroupMember{}).Where("conversation_id=?", conversation.ID).Count(&count)
			want := int64(0)
			if kind == "project" {
				want = 1
			}
			if count != want {
				t.Fatalf("membership count=%d, want %d", count, want)
			}
			var state orm.ConversationGroupState
			if err := db.Where("conversation_id=?", conversation.ID).Take(&state).Error; err != nil {
				t.Fatal(err)
			}
			if kind == "group" && (state.GroupID != nil || state.Revision != 5) {
				t.Fatalf("pin did not fence membership undo: %+v", state)
			}
			if _, err := updateConversationPin(t.Context(), db, "u1", conversation.ID, false); err != nil {
				t.Fatal(err)
			}
			db.Model(&orm.ConversationGroupMember{}).Where("conversation_id=?", conversation.ID).Count(&count)
			if count != want {
				t.Fatalf("unpin unexpectedly restored membership: %d", count)
			}
			if err := db.Where("id=?", conversation.ID).Take(&conversation).Error; err != nil {
				t.Fatal(err)
			}
			if !conversation.UpdatedAt.Equal(before) || !conversation.CreatedAt.Equal(before) {
				t.Fatal("pin changed activity time")
			}
		})
	}
}
