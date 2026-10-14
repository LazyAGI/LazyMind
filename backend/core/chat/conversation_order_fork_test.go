package chat

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func newForkOrderTestDB(t *testing.T) (*gorm.DB, map[string]orm.Conversation) {
	t.Helper()
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	base := time.Date(2026, 10, 14, 8, 0, 0, 0, time.UTC)
	parent := "parent-a"
	rows := map[string]orm.Conversation{}
	for i, id := range []string{"parent-a", "parent-b", "root-a", "root-b", "sidechat", "fork-a", "fork-b", "other-fork", "foreign-fork", "archived-fork"} {
		order := int64((i + 1) * 10)
		row := orm.Conversation{ID: id, DisplayName: id, HistoryOrder: &order,
			BaseModel: orm.BaseModel{CreateUserID: "u1", CreatedAt: base, UpdatedAt: base}}
		if id == "sidechat" {
			row.ParentConversationID, row.RelationType = &parent, "sidechat"
		}
		if id == "foreign-fork" {
			row.CreateUserID = "u2"
		}
		if id == "archived-fork" {
			row.ArchivedAt = &base
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		rows[id] = row
	}
	for _, id := range []string{"fork-a", "fork-b", "other-fork", "foreign-fork", "archived-fork"} {
		source := parent
		if id == "other-fork" {
			source = "parent-b"
		}
		origin := orm.ConversationForkOrigin{ConversationID: id, SourceConversationID: source,
			SourceHistoryID: "source-history", SourceSeq: 1, SourceHistoryRevision: "test-revision",
			SourcePrefixRevision: "test-prefix", SourceTitleSnapshot: source, ForkedAt: base}
		if err := db.Create(&origin).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, rows
}

func TestConversationOrderForkAndSidechatShareParentScope(t *testing.T) {
	for _, scenario := range []struct {
		name, moved, target string
		want                []string
	}{
		{"fork-before-sidechat", "fork-a", "sidechat", []string{"fork-a", "sidechat", "fork-b"}},
		{"sidechat-before-fork", "sidechat", "fork-b", []string{"fork-a", "sidechat", "fork-b"}},
		{"fork-before-fork", "fork-b", "fork-a", []string{"sidechat", "fork-b", "fork-a"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, before := newForkOrderTestDB(t)
			var origins []orm.ConversationForkOrigin
			if err := db.Order("conversation_id").Find(&origins).Error; err != nil {
				t.Fatal(err)
			}
			for repeat := 0; repeat < 2; repeat++ {
				response := callConversationOrder(t, scenario.moved, scenario.target, "before")
				if response.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				var result conversationOrderResult
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, update := range result.OrderUpdates {
					ids = append(ids, update.ConversationID)
				}
				if !reflect.DeepEqual(ids, scenario.want) {
					t.Fatalf("updated scopes=%v, want %v", ids, scenario.want)
				}
			}
			var after []orm.Conversation
			if err := db.Find(&after).Error; err != nil {
				t.Fatal(err)
			}
			for _, row := range after {
				original := before[row.ID]
				for i, id := range scenario.want {
					if row.ID == id {
						order := int64(i + 1)
						original.HistoryOrder = &order
					}
				}
				if !reflect.DeepEqual(row, original) {
					t.Fatalf("unexpected conversation change: got %+v, want %+v", row, original)
				}
			}
			var afterOrigins []orm.ConversationForkOrigin
			if err := db.Order("conversation_id").Find(&afterOrigins).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(origins, afterOrigins) {
				t.Fatal("reorder changed fork provenance")
			}
		})
	}
}

func TestConversationOrderRejectsForksOutsideParentScope(t *testing.T) {
	for _, scenario := range []struct {
		moved, target string
		status        int
	}{
		{"fork-a", "other-fork", http.StatusConflict},
		{"sidechat", "other-fork", http.StatusConflict},
		{"fork-a", "root-a", http.StatusNotFound},
		{"root-a", "fork-a", http.StatusNotFound},
		{"fork-a", "foreign-fork", http.StatusNotFound},
		{"fork-a", "archived-fork", http.StatusNotFound},
	} {
		t.Run(scenario.moved+"/"+scenario.target, func(t *testing.T) {
			db, before := newForkOrderTestDB(t)
			response := callConversationOrder(t, scenario.moved, scenario.target, "before")
			if response.Code != scenario.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var rows []orm.Conversation
			if err := db.Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if !reflect.DeepEqual(row, before[row.ID]) {
					t.Fatalf("rejected reorder changed %s", row.ID)
				}
			}
		})
	}
}

func TestConversationOrderRootsExcludeForks(t *testing.T) {
	db, before := newForkOrderTestDB(t)
	response := callConversationOrder(t, "root-b", "root-a", "before")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result conversationOrderResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, update := range result.OrderUpdates {
		ids = append(ids, update.ConversationID)
	}
	if want := []string{"parent-a", "parent-b", "root-b", "root-a"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("updated root scope=%v, want %v", ids, want)
	}
	for _, id := range []string{"sidechat", "fork-a", "fork-b", "other-fork", "foreign-fork", "archived-fork"} {
		var row orm.Conversation
		if err := db.Where("id=?", id).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(row, before[id]) {
			t.Fatalf("root reorder changed child %s", id)
		}
	}
}
