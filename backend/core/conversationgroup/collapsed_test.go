package conversationgroup

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestGroupCollapsedStatePersistsWithoutChangingPlacementOrOrganizer(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ExternalAgentBinding{}, &orm.ExternalAgentSession{}, &orm.Conversation{}, &orm.ConversationOpening{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"normal", "task", "project", "other", "deleted"} {
		group := orm.ConversationGroup{ID: id, UserID: "u", Name: id, NormalizedName: id, Kind: KindGroup, SortOrder: int64(i + 1), Version: 7, CreatedAt: now, UpdatedAt: now}
		if id == "task" {
			group.IsTaskConv = true
		}
		if id == "project" {
			group.Kind, group.Pinned = KindProject, true
		}
		if id == "other" {
			group.UserID = "other-user"
		}
		if id == "deleted" {
			group.DeletedAt = &now
		}
		if err := db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(handler http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
		req.Header.Set("X-User-Id", "u")
		req = mux.SetURLVars(req, map[string]string{"group_id": id})
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	assertCollapsed := func(rec *httptest.ResponseRecorder, id string, want bool) {
		t.Helper()
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Group  map[string]any   `json:"group"`
			Groups []map[string]any `json:"groups"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		candidates := append(body.Groups, body.Group)
		for _, group := range candidates {
			if group["id"] == id {
				if got, ok := group["collapsed"].(bool); !ok || got != want {
					t.Fatalf("collapsed = %v, want %v: %s", group["collapsed"], want, rec.Body.String())
				}
				return
			}
		}
		t.Fatalf("missing group %s: %s", id, rec.Body.String())
	}
	for i, id := range []string{"normal", "task", "project"} {
		assertCollapsed(invoke(GetGroup, "GET", id, ""), id, false)
		for _, want := range []bool{true, true, false} {
			raw, _ := json.Marshal(map[string]bool{"collapsed": want})
			assertCollapsed(invoke(UpdateGroupPlacement, "PATCH", id, string(raw)), id, want)
			assertCollapsed(invoke(GetGroup, "GET", id, ""), id, want)
			assertCollapsed(invoke(ListGroups, "GET", "", ""), id, want)
			var group orm.ConversationGroup
			if err := db.Where("id=?", id).Take(&group).Error; err != nil {
				t.Fatal(err)
			}
			if group.SortOrder != int64(i+1) || group.Version != 7 || !group.UpdatedAt.Equal(now) || group.Pinned != (id == "project") {
				t.Fatalf("collapse changed unrelated state: %+v", group)
			}
		}
	}
	for _, step := range []struct {
		id, body string
		status   int
	}{
		{"other", `{"collapsed":true}`, 404},
		{"deleted", `{"collapsed":true}`, 404},
		{"missing", `{"collapsed":true}`, 404},
		{"normal", `{"collapsed":"true"}`, 400},
		{"normal", `{"collapsed":null}`, 400},
		{"normal", `{}`, 400},
		{"normal", `{"collapsed":true,"before_group_id":"other"}`, 404},
		{"normal", `{"collapsed":true,"pinned":"true"}`, 400},
	} {
		rec := invoke(UpdateGroupPlacement, "PATCH", step.id, step.body)
		if rec.Code != step.status {
			t.Fatalf("%s %s: %d %s", step.id, step.body, rec.Code, rec.Body.String())
		}
	}
	assertCollapsed(invoke(GetGroup, "GET", "normal", ""), "normal", false)
	assertCollapsed(invoke(UpdateGroupPlacement, "PATCH", "normal", `{"collapsed":true,"before_group_id":"normal"}`), "normal", true)
}
