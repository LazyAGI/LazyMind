package conversationgroup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestOrdinaryGroupPinningPersistsAndPreservesMembers(t *testing.T) {
	for _, task := range []bool{false, true} {
		t.Run(fmt.Sprintf("task=%t", task), func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.ExternalAgentBinding{}, &orm.ExternalAgentSession{}, &orm.Conversation{}, &orm.ConversationOpening{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{})
			store.Init(db.DB, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			now := time.Now().UTC().Truncate(time.Second)
			for _, row := range []orm.ConversationGroup{
				{ID: "a", Kind: KindGroup, UserID: "u", IsTaskConv: task, Collapsed: true, SortOrder: 1},
				{ID: "b", Kind: KindGroup, UserID: "u", IsTaskConv: task, SortOrder: 2},
				{ID: "project", Kind: KindProject, UserID: "u", IsTaskConv: task, Pinned: true, SortOrder: 9},
				{ID: "other", Kind: KindGroup, UserID: "other-user", IsTaskConv: task, Pinned: true, SortOrder: 17},
				{ID: "other-mode", Kind: KindGroup, UserID: "u", IsTaskConv: !task, Pinned: true, SortOrder: 19},
			} {
				row.Name, row.NormalizedName, row.Version, row.CreatedAt, row.UpdatedAt = row.ID, row.ID, 7, now, now
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&orm.Conversation{ID: "member", IsTaskConv: task, BaseModel: orm.BaseModel{CreateUserID: "u"}}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.ConversationGroupMember{ConversationID: "member", GroupID: "a", UserID: "u"}).Error; err != nil {
				t.Fatal(err)
			}
			invoke := func(handler http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, fmt.Sprintf("/?is_task_conv=%t", task), strings.NewReader(body))
				req.Header.Set("X-User-Id", "u")
				req = mux.SetURLVars(req, map[string]string{"group_id": id})
				rec := httptest.NewRecorder()
				handler(rec, req)
				return rec
			}
			for _, step := range []struct {
				id, body string
				order    string
				pinned   bool
			}{
				{"a", `{"pinned":true}`, "a,project,b", true},
				{"a", `{"pinned":true}`, "a,project,b", true},
				{"b", `{"pinned":true,"before_group_id":"project"}`, "a,b,project", true},
				{"a", `{"before_group_id":""}`, "b,project,a", true},
				{"b", `{"pinned":false}`, "project,a,b", false},
				{"a", `{"pinned":false}`, "project,a,b", false},
			} {
				rec := invoke(UpdateGroupPlacement, "PATCH", step.id, step.body)
				if rec.Code != 200 {
					t.Fatalf("%s %s: %d %s", step.id, step.body, rec.Code, rec.Body.String())
				}
				// Read back through the list and detail APIs, as a fresh client would.
				rec = invoke(ListGroups, "GET", "", "")
				var list struct {
					Groups []GroupDTO `json:"groups"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, group := range list.Groups {
					ids = append(ids, group.ID)
				}
				if rec.Code != 200 || strings.Join(ids, ",") != step.order {
					t.Fatalf("want %s, got %s", step.order, rec.Body.String())
				}
				rec = invoke(GetGroup, "GET", step.id, "")
				var detail struct {
					Group GroupDTO `json:"group"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
					t.Fatal(err)
				}
				if rec.Code != 200 || detail.Group.Pinned != step.pinned || detail.Group.Collapsed != (step.id == "a") || detail.Group.Version != 7 {
					t.Fatalf("incorrect saved state: %s", rec.Body.String())
				}
			}
			for _, step := range []struct{ id, body string }{
				{"other", `{"pinned":false}`},
				{"a", `{"pinned":true,"before_group_id":"other"}`},
				{"a", `{"pinned":true,"before_group_id":"other-mode"}`},
				{"a", `{"pinned":true,"before_group_id":"b"}`},
			} {
				if rec := invoke(UpdateGroupPlacement, "PATCH", step.id, step.body); rec.Code != 404 {
					t.Fatalf("invalid placement: %d %s", rec.Code, rec.Body.String())
				}
			}
			for id, pinned := range map[string]bool{"a": false, "b": false, "project": true, "other": true, "other-mode": true} {
				var group orm.ConversationGroup
				if err := db.Where("id=?", id).Take(&group).Error; err != nil {
					t.Fatal(err)
				}
				if group.Pinned != pinned || group.Version != 7 || !group.UpdatedAt.Equal(now) {
					t.Fatalf("unexpected group change: %+v", group)
				}
				if (id == "other" && group.SortOrder != 17) || (id == "other-mode" && group.SortOrder != 19) {
					t.Fatalf("changed isolated placement: %+v", group)
				}
			}
			var member orm.ConversationGroupMember
			if err := db.Where("conversation_id=?", "member").Take(&member).Error; err != nil || member.GroupID != "a" {
				t.Fatalf("pin changed group membership: %+v, %v", member, err)
			}
			var conversation orm.Conversation
			if err := db.Where("id=?", "member").Take(&conversation).Error; err != nil || conversation.PinnedAt != nil {
				t.Fatalf("pin changed member pin state: %+v, %v", conversation, err)
			}
		})
	}
}
