package chat

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestConversationOrderWithinExistingRelationships(t *testing.T) {
	for _, kind := range []string{"recent", "group", "project", "children"} {
		for _, task := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/task=%t", kind, task), func(t *testing.T) {
				db := newConversationTestDB(t).DB
				store.Init(db, nil, nil)
				t.Cleanup(func() { store.Init(nil, nil, nil) })
				before := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
				parent := "parent"
				group := orm.ConversationGroup{ID: "group", UserID: "u1", Kind: kind, Name: "Group", NormalizedName: "group", IsTaskConv: task}
				if kind == "group" || kind == "project" {
					if err := db.Create(&group).Error; err != nil {
						t.Fatal(err)
					}
				}
				for i, id := range []string{"target", "moved", "untouched", parent} {
					order := int64(i + 1)
					row := orm.Conversation{ID: id, DisplayName: id, IsTaskConv: task, HistoryOrder: &order,
						BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: before, UpdatedAt: before}}
					if id == "untouched" {
						row.IsTaskConv = !task
					}
					if kind == "children" && (id == "moved" || id == "target") {
						row.ParentConversationID = &parent
						row.RelationType = "sidechat"
					}
					if err := db.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
					if (kind == "group" || kind == "project") && (id == "moved" || id == "target") {
						for _, value := range []any{
							&orm.ConversationGroupMember{ConversationID: id, GroupID: group.ID, UserID: "u1", Revision: 7},
							&orm.ConversationGroupState{ConversationID: id, GroupID: &group.ID, UserID: "u1", Revision: 7},
						} {
							if err := db.Create(value).Error; err != nil {
								t.Fatal(err)
							}
						}
					}
				}
				for repeat := 0; repeat < 2; repeat++ {
					if response := callConversationOrder(t, "moved", "target", "before"); response.Code != http.StatusOK {
						t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
					}
				}
				var rows []orm.Conversation
				if err := db.Order("id").Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					if !row.UpdatedAt.Equal(before) || !row.CreatedAt.Equal(before) {
						t.Fatalf("activity changed: %s", row.ID)
					}
					if row.ID == "untouched" && (row.HistoryOrder == nil || *row.HistoryOrder != 3) {
						t.Fatalf("other scope changed: %+v", row)
					}
					if row.ID == "moved" && (row.HistoryOrder == nil || *row.HistoryOrder != 1) {
						t.Fatalf("move not saved: %+v", row)
					}
					if kind == "children" && (row.ID == "moved" || row.ID == "target") && (row.ParentConversationID == nil || *row.ParentConversationID != parent) {
						t.Fatalf("parent changed: %+v", row)
					}
				}
				if kind == "group" || kind == "project" {
					var members []orm.ConversationGroupMember
					if err := db.Find(&members).Error; err != nil {
						t.Fatal(err)
					}
					if len(members) != 2 {
						t.Fatalf("memberships changed: %+v", members)
					}
					for _, member := range members {
						if member.GroupID != group.ID || member.Revision != 7 {
							t.Fatalf("membership revised: %+v", member)
						}
					}
					var state orm.ConversationGroupState
					if err := db.Where("conversation_id=?", "moved").Take(&state).Error; err != nil {
						t.Fatal(err)
					}
					if state.Revision != 7 || state.GroupID == nil || *state.GroupID != group.ID {
						t.Fatalf("membership fence changed: %+v", state)
					}
				}
				if kind == "children" {
					if got := orderedConversationIDs(t, fmt.Sprintf("is_task_conv=%t", task)); !reflect.DeepEqual(got, []string{parent, "moved", "target"}) {
						t.Fatalf("parent/child listing=%v", got)
					}
				}
			})
		}
	}
}

func TestConversationOrderRejectsDifferentLists(t *testing.T) {
	for _, kind := range []string{"mode", "parent", "group", "project"} {
		t.Run(kind, func(t *testing.T) {
			db := newConversationTestDB(t).DB
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			for _, id := range []string{"moved", "target", "parent-a", "parent-b"} {
				row := orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u1"}}
				if kind == "mode" && id == "moved" {
					row.IsTaskConv = true
				}
				if kind == "parent" && (id == "moved" || id == "target") {
					parent := "parent-a"
					if id == "target" {
						parent = "parent-b"
					}
					row.ParentConversationID = &parent
				}
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if kind == "group" || kind == "project" {
				group := orm.ConversationGroup{ID: "g", UserID: "u1", Kind: kind, Name: "G", NormalizedName: "g"}
				if err := db.Create(&group).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&orm.ConversationGroupMember{ConversationID: "moved", GroupID: "g", UserID: "u1"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			response := callConversationOrder(t, "moved", "target", "before")
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var changed int64
			db.Model(&orm.Conversation{}).Where("history_order IS NOT NULL").Count(&changed)
			if changed != 0 {
				t.Fatal("rejected move changed order")
			}
		})
	}
}

func TestConversationOrderRejectsOrganizerLockedScope(t *testing.T) {
	for _, lockedID := range []string{"moved", "target", "unloaded"} {
		t.Run(lockedID, func(t *testing.T) {
			db := newConversationTestDB(t).DB
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			for _, id := range []string{"moved", "target", "unloaded"} {
				if err := db.Create(&orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u1"}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, value := range []any{
				&orm.ConversationOrganizerRun{ID: "run", UserID: "u1", Status: "applying", SnapshotJSON: []byte("{}"), SnapshotHash: "test-only", ModelConfigJSON: []byte("{}")},
				&orm.ConversationOrganizerSnapshotItem{RunID: "run", ConversationID: lockedID, UserID: "u1", Title: "Test"},
			} {
				if err := db.Create(value).Error; err != nil {
					t.Fatal(err)
				}
			}
			response := callConversationOrder(t, "moved", "target", "before")
			if response.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var changed int64
			db.Model(&orm.Conversation{}).Where("history_order IS NOT NULL").Count(&changed)
			if changed != 0 {
				t.Fatal("locked list was partially reordered")
			}
		})
	}
}
