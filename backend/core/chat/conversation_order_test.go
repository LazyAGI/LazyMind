package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func callConversationOrder(t *testing.T, id, target, position string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"target_conversation_id": target, "position": position})
	req := httptest.NewRequest(http.MethodPost, "/api/core/conversations/"+id+":reorder", strings.NewReader(string(body)))
	req = mux.SetURLVars(req, map[string]string{"conversation_id": id})
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	ReorderConversation(rec, req)
	return rec
}

func orderedConversationIDs(t *testing.T, query string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/core/conversations?"+query, nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	ListConversations(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var result struct {
		Conversations []struct {
			ID string `json:"conversation_id"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(result.Conversations))
	for _, item := range result.Conversations {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestConversationManualOrder(t *testing.T) {
	for _, task := range []bool{false, true} {
		t.Run(fmt.Sprint(task), func(t *testing.T) {
			db := newPromptTestDB(t).DB
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
			for i, id := range []string{"older", "middle", "newer"} {
				if err := db.Create(&orm.Conversation{ID: id, DisplayName: id, IsTaskConv: task, BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: base, UpdatedAt: base.Add(time.Duration(i) * time.Hour)}}).Error; err != nil {
					t.Fatal(err)
				}
			}
			query := fmt.Sprintf("is_task_conv=%t", task)
			for i := 0; i < 2; i++ {
				rec := callConversationOrder(t, "older", "newer", "before")
				if rec.Code != http.StatusOK {
					t.Fatalf("reorder status=%d body=%s", rec.Code, rec.Body.String())
				}
			}
			if err := db.Model(&orm.Conversation{}).Where("id = ?", "middle").UpdateColumn("updated_at", base.Add(24*time.Hour)).Error; err != nil {
				t.Fatal(err)
			}
			if got := orderedConversationIDs(t, query); !reflect.DeepEqual(got, []string{"older", "newer", "middle"}) {
				t.Fatalf("activity changed manual order: %v", got)
			}
			if got := orderedConversationIDs(t, query+"&page_size=1&page_token=1"); !reflect.DeepEqual(got, []string{"newer"}) {
				t.Fatalf("pagination=%v", got)
			}
			var older orm.Conversation
			if err := db.First(&older, "id = ?", "older").Error; err != nil {
				t.Fatal(err)
			}
			if !older.UpdatedAt.Equal(base) {
				t.Fatal("reorder changed activity timestamp")
			}
			pin := func(id string, pinned bool) {
				req := httptest.NewRequest(http.MethodPost, "/", nil)
				req = mux.SetURLVars(req, map[string]string{"conversation_id": id})
				req.Header.Set("X-User-Id", "u1")
				rec := httptest.NewRecorder()
				setConversationPinned(rec, req, pinned)
				if rec.Code != http.StatusOK {
					t.Fatalf("pin status=%d body=%s", rec.Code, rec.Body.String())
				}
			}
			pin("middle", true)
			pin("older", true)
			if rec := callConversationOrder(t, "middle", "older", "before"); rec.Code != http.StatusOK {
				t.Fatal(rec.Body.String())
			}
			pin("older", true) // Repeated pin must retain the user's manual placement.
			if got := orderedConversationIDs(t, query); !reflect.DeepEqual(got, []string{"middle", "older", "newer"}) {
				t.Fatalf("pinned order=%v", got)
			}
			pin("older", false)
			if got := orderedConversationIDs(t, query); !reflect.DeepEqual(got, []string{"middle", "newer", "older"}) {
				t.Fatalf("unpin did not use activity time: %v", got)
			}
		})
	}
}

func TestConversationOrderRejectsUnavailableTargets(t *testing.T) {
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	parent := "mine"
	for _, item := range []orm.Conversation{
		{ID: "mine", BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: now, CreatedAt: now}},
		{ID: "other", BaseModel: orm.BaseModel{CreateUserID: "u2", UpdatedAt: now, CreatedAt: now}},
		{ID: "archived", ArchivedAt: &now, BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: now, CreatedAt: now}},
		{ID: "ephemeral", IsEphemeral: true, BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: now, CreatedAt: now}},
		{ID: "child", ParentConversationID: &parent, RelationType: "sidechat", BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: now, CreatedAt: now}},
		{ID: "pinned", PinnedAt: &now, BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: now, CreatedAt: now}},
	} {
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"missing", "other", "archived", "ephemeral", "child"} {
		if rec := callConversationOrder(t, "mine", target, "before"); rec.Code != http.StatusNotFound {
			t.Fatalf("target %s status=%d", target, rec.Code)
		}
	}
	if rec := callConversationOrder(t, "other", "mine", "before"); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign source status=%d", rec.Code)
	}
	if rec := callConversationOrder(t, "mine", "pinned", "before"); rec.Code != http.StatusConflict {
		t.Fatalf("pin partition mismatch status=%d", rec.Code)
	}
	for _, position := range []string{"", "sideways"} {
		if rec := callConversationOrder(t, "mine", "pinned", position); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid position status=%d", rec.Code)
		}
	}
	var changed int64
	if err := db.Model(&orm.Conversation{}).Where("history_order IS NOT NULL").Count(&changed).Error; err != nil {
		t.Fatal(err)
	}
	if changed != 0 {
		t.Fatal("rejected requests changed order")
	}
}

func TestConversationOrderPaginatesChildrenAfterTheirParent(t *testing.T) {
	for _, task := range []bool{false, true} {
		for _, pinned := range []bool{false, true} {
			t.Run(fmt.Sprintf("task=%t/pinned=%t", task, pinned), func(t *testing.T) {
				db := newPromptTestDB(t).DB
				store.Init(db, nil, nil)
				t.Cleanup(func() { store.Init(nil, nil, nil) })
				base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
				for i, id := range []string{"b", "a", "c"} {
					order := int64(i + 1)
					row := orm.Conversation{ID: id, DisplayName: id, HistoryOrder: &order, IsTaskConv: task,
						BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: base.Add(time.Duration(i) * time.Hour)}}
					if pinned {
						row.PinnedAt = &row.UpdatedAt
					}
					if err := db.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
				}
				parent := "a"
				for _, id := range []string{"a-child", "foreign-child", "archived-child", "ephemeral-child"} {
					row := orm.Conversation{ID: id, DisplayName: id, ParentConversationID: &parent, RelationType: "sidechat", IsTaskConv: task,
						BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: base.Add(24 * time.Hour)}}
					switch id {
					case "foreign-child":
						row.CreateUserID = "u2"
					case "archived-child":
						row.ArchivedAt = &base
					case "ephemeral-child":
						row.IsEphemeral = true
					}
					if err := db.Create(&row).Error; err != nil {
						t.Fatal(err)
					}
				}
				query := fmt.Sprintf("is_task_conv=%t", task)
				for offset, want := range []string{"b", "a", "a-child", "c"} {
					if got := orderedConversationIDs(t, fmt.Sprintf("%s&page_size=1&page_token=%d", query, offset)); !reflect.DeepEqual(got, []string{want}) {
						t.Fatalf("page %d=%v, want %s", offset, got, want)
					}
				}
				if got := orderedConversationIDs(t, query+"&keyword=a-child"); !reflect.DeepEqual(got, []string{"a-child"}) {
					t.Fatalf("child-only search=%v", got)
				}
				var child orm.Conversation
				if err := db.First(&child, "id = ?", "a-child").Error; err != nil {
					t.Fatal(err)
				}
				if child.HistoryOrder != nil || child.PinnedAt != nil || child.ParentConversationID == nil || *child.ParentConversationID != parent {
					t.Fatalf("listing modified the child: %#v", child)
				}
			})
		}
	}
}

func TestConversationOrderUnpinPreservesOtherManualPositions(t *testing.T) {
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	for i, id := range []string{"oldest", "returning", "newer", "newest"} {
		row := orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: base.Add(time.Duration(i) * time.Hour)}}
		if id == "returning" {
			row.PinnedAt = &base
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if rec := callConversationOrder(t, "oldest", "newest", "before"); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, err := updateConversationPin(context.Background(), db, "u1", "returning", false); err != nil {
		t.Fatal(err)
	}
	if got := orderedConversationIDs(t, ""); !reflect.DeepEqual(got, []string{"oldest", "newest", "returning", "newer"}) {
		t.Fatalf("unpin should use its chronological index and retain other manual positions: %v", got)
	}
}

func TestConversationOrderRetainsFilteredAndUnloadedRows(t *testing.T) {
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	base := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	for i, id := range []string{"unloaded", "moved", "filtered", "target"} {
		if err := db.Create(&orm.Conversation{ID: id, IsTaskConv: id == "filtered", BaseModel: orm.BaseModel{CreateUserID: "u1", UpdatedAt: base.Add(time.Duration(i) * time.Hour)}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if rec := callConversationOrder(t, "moved", "target", "before"); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if got := orderedConversationIDs(t, "is_task_conv=false&page_size=2"); !reflect.DeepEqual(got, []string{"moved", "target"}) {
		t.Fatalf("visible order=%v", got)
	}
	if got := orderedConversationIDs(t, "is_task_conv=false&page_size=2&page_token=2"); !reflect.DeepEqual(got, []string{"unloaded"}) {
		t.Fatalf("next page=%v", got)
	}
	if got := orderedConversationIDs(t, "is_task_conv=true"); !reflect.DeepEqual(got, []string{"filtered"}) {
		t.Fatalf("filtered order=%v", got)
	}
}

func TestConversationOrderConcurrentMoves(t *testing.T) {
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := db.Create(&orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u1"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, id := range []string{"b", "c", "d"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if rec := callConversationOrder(t, id, "a", "before"); rec.Code != http.StatusOK {
				t.Errorf("move %s: %d %s", id, rec.Code, rec.Body.String())
			}
		}(id)
	}
	wg.Wait()
	var rows []orm.Conversation
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for _, row := range rows {
		if row.HistoryOrder == nil || seen[*row.HistoryOrder] {
			t.Fatalf("missing or duplicate order: %#v", row.HistoryOrder)
		}
		seen[*row.HistoryOrder] = true
	}
	if got := orderedConversationIDs(t, ""); len(got) != 4 || got[3] != "a" {
		t.Fatalf("concurrent order=%v", got)
	}
}
