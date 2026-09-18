package conversationgroup

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestMemberDragPersistsOrderAndMovesAtomically(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationOpening{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []string{"a", "b", "foreign"} {
		uid := "u"
		if id == "foreign" {
			uid = "other"
		}
		if err := db.Create(&orm.ConversationGroup{ID: id, UserID: uid, Name: id, NormalizedName: id, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []string{"one", "two", "three", "task", "outside"} {
		uid, group := "u", "a"
		if id == "task" {
			group = "b"
		}
		if id == "outside" {
			uid, group = "other", "foreign"
		}
		conv := orm.Conversation{ID: id, DisplayName: id, IsTaskConv: id == "task", BaseModel: orm.BaseModel{CreateUserID: uid, CreatedAt: now, UpdatedAt: now.Add(time.Duration(i) * time.Hour)}}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
		if err := MoveConversation(t.Context(), db.DB, uid, id, group, CreatedByUser, ""); err != nil {
			t.Fatal(err)
		}
	}
	move := func(group, id, target, position string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"conversation_id": id, "target_conversation_id": target, "position": position})
		r := httptest.NewRequest("POST", "/", bytes.NewReader(body))
		r.Header.Set("X-User-Id", "u")
		r = mux.SetURLVars(r, map[string]string{"group_id": group})
		w := httptest.NewRecorder()
		AddMember(w, r)
		return w
	}
	order := func(group, query string) []string {
		r := httptest.NewRequest("GET", "/"+query, nil)
		r.Header.Set("X-User-Id", "u")
		r = mux.SetURLVars(r, map[string]string{"group_id": group})
		w := httptest.NewRecorder()
		GetGroup(w, r)
		if w.Code != 200 {
			t.Fatalf("detail: %d %s", w.Code, w.Body.String())
		}
		var response struct {
			Conversations []struct {
				ID     string `json:"conversation_id"`
				IsTask *bool  `json:"is_task_conv"`
			} `json:"conversations"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, c := range response.Conversations {
			if c.IsTask == nil || *c.IsTask != (c.ID == "task") {
				t.Fatalf("incorrect preview type for %s: %v", c.ID, c.IsTask)
			}
			ids = append(ids, c.ID)
		}
		return ids
	}
	assertOrder := func(group string, want []string) {
		t.Helper()
		if got := order(group, ""); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %v want %v", group, got, want)
		}
	}
	for _, step := range []struct {
		group, id, target, position string
		want                        []string
	}{
		{"a", "one", "three", "before", []string{"one", "three", "two"}},
		{"a", "one", "two", "after", []string{"three", "two", "one"}},
		{"a", "task", "two", "before", []string{"three", "task", "two", "one"}},
	} {
		if w := move(step.group, step.id, step.target, step.position); w.Code != 200 {
			t.Fatalf("move: %d %s", w.Code, w.Body.String())
		}
		assertOrder(step.group, step.want)
	}
	assertOrder("b", []string{})
	// Activity and pagination must not undo a saved manual order.
	if err := db.Model(&orm.Conversation{}).Where("id=?", "one").UpdateColumn("updated_at", now.Add(24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	assertOrder("a", []string{"three", "task", "two", "one"})
	if got := order("a", "?page_size=2&page_token=2"); !reflect.DeepEqual(got, []string{"two", "one"}) {
		t.Fatalf("page: %v", got)
	}
	// Invalid or foreign anchors cannot partially move a conversation.
	for _, step := range []struct {
		group, id, target, position string
		status                      int
	}{
		{"b", "one", "missing", "before", 404},
		{"b", "one", "two", "before", 404},
		{"foreign", "one", "outside", "before", 404},
		{"a", "outside", "two", "before", 404},
		{"a", "one", "two", "invalid", 400},
		{"a", "one", "one", "before", 400},
		{"a", "one", "", "after", 400},
	} {
		if w := move(step.group, step.id, step.target, step.position); w.Code != step.status {
			t.Fatalf("invalid move %+v: %d %s", step, w.Code, w.Body.String())
		}
		assertOrder("a", []string{"three", "task", "two", "one"})
		assertOrder("b", []string{})
	}
	var unchanged orm.Conversation
	if err := db.Where("id=?", "two").Take(&unchanged).Error; err != nil {
		t.Fatal(err)
	}
	if !unchanged.UpdatedAt.Equal(now.Add(time.Hour)) {
		t.Fatal("drag changed activity timestamp")
	}
	// A database write failure must roll back membership and its undo fence too.
	anchor := orm.Conversation{ID: "anchor", BaseModel: orm.BaseModel{CreateUserID: "u", CreatedAt: now, UpdatedAt: now}}
	if err := db.Create(&anchor).Error; err != nil {
		t.Fatal(err)
	}
	if err := MoveConversation(t.Context(), db.DB, "u", anchor.ID, "b", CreatedByUser, ""); err != nil {
		t.Fatal(err)
	}
	var before orm.ConversationGroupState
	if err := db.Where("conversation_id=?", "one").Take(&before).Error; err != nil {
		t.Fatal(err)
	}
	const callback = "test:reject-member-order"
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "conversations" {
			tx.AddError(errors.New("test order write failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Update().Remove(callback) })
	for _, target := range []string{"anchor", ""} {
		position := ""
		if target != "" {
			position = "before"
		}
		if w := move("b", "one", target, position); w.Code != 500 {
			t.Fatalf("failed write with target %q: %d %s", target, w.Code, w.Body.String())
		}
	}
	assertOrder("a", []string{"three", "task", "two", "one"})
	assertOrder("b", []string{"anchor"})
	var after orm.ConversationGroupState
	if err := db.Where("conversation_id=?", "one").Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.GroupID == nil || *after.GroupID != "a" {
		t.Fatal("failed order write changed membership fence")
	}
}

func TestGroupHeaderMoveDoesNotReusePreviousOrder(t *testing.T) {
	for _, scenario := range []string{"cross-group", "ungrouped", "empty-group", "pinned"} {
		t.Run(scenario, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationOpening{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{})
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			now := time.Now().UTC().Truncate(time.Second)
			for _, id := range []string{"source", "target"} {
				if err := db.Create(&orm.ConversationGroup{ID: id, UserID: "u", Name: id, NormalizedName: id, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for i, id := range []string{"source-first", "moved", "target-first", "target-last"} {
				if scenario == "empty-group" && i >= 2 {
					continue
				}
				conv := orm.Conversation{ID: id, DisplayName: id, BaseModel: orm.BaseModel{CreateUserID: "u", CreatedAt: now, UpdatedAt: now.Add(time.Duration(i) * time.Hour)}}
				if scenario == "pinned" && id == "moved" {
					conv.PinnedAt = &now
				}
				if err := db.Create(&conv).Error; err != nil {
					t.Fatal(err)
				}
				group := "source"
				if i >= 2 {
					group = "target"
				}
				if scenario != "ungrouped" || id != "moved" {
					if _, err := moveMembershipTx(db.DB, "u", id, &group, CreatedByUser, ""); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Model(&conv).UpdateColumn("history_order", i%2+1).Error; err != nil {
					t.Fatal(err)
				}
			}
			move := func(target, position string) {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"conversation_id": "moved", "target_conversation_id": target, "position": position})
				req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
				req.Header.Set("X-User-Id", "u")
				req = mux.SetURLVars(req, map[string]string{"group_id": "target"})
				response := httptest.NewRecorder()
				AddMember(response, req)
				if response.Code != 200 {
					t.Fatalf("move: %d %s", response.Code, response.Body.String())
				}
			}
			assertOrder := func(want []string) {
				t.Helper()
				req := httptest.NewRequest("GET", "/", nil)
				req.Header.Set("X-User-Id", "u")
				req = mux.SetURLVars(req, map[string]string{"group_id": "target"})
				response := httptest.NewRecorder()
				GetGroup(response, req)
				var result struct {
					Conversations []struct {
						ID string `json:"conversation_id"`
					} `json:"conversations"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 {
					t.Fatalf("get group: %d %s, err=%v", response.Code, response.Body.String(), err)
				}
				var got []string
				for _, conv := range result.Conversations {
					got = append(got, conv.ID)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("order = %v, want %v", got, want)
				}
			}
			move("", "")
			var moved orm.Conversation
			if err := db.Where("id=?", "moved").Take(&moved).Error; err != nil {
				t.Fatal(err)
			}
			if !moved.UpdatedAt.Equal(now.Add(time.Hour)) {
				t.Fatal("move changed activity timestamp")
			}
			if scenario == "pinned" {
				if moved.HistoryOrder != nil {
					t.Fatal("pinned member retained an order from its previous group")
				}
				return
			}
			if scenario == "empty-group" {
				assertOrder([]string{"moved"})
				return
			}
			assertOrder([]string{"moved", "target-first", "target-last"})
			if err := db.Model(&orm.Conversation{}).Where("id=?", "target-last").UpdateColumn("updated_at", now.Add(24*time.Hour)).Error; err != nil {
				t.Fatal(err)
			}
			assertOrder([]string{"moved", "target-first", "target-last"})
			move("target-last", "after")
			move("", "")
			assertOrder([]string{"target-first", "target-last", "moved"})
		})
	}
}
