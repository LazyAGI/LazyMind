package conversationgroup

import (
	"bytes"
	"encoding/json"
	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGroupPlacementPersistsAndDoesNotChangeOrganizerVersion(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	for i, id := range []string{"a", "b", "other"} {
		uid := "u"
		if id == "other" {
			uid = "other-user"
		}
		row := orm.ConversationGroup{ID: id, UserID: uid, Name: id, NormalizedName: id, Version: 7, CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	invoke := func(id, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/", bytes.NewBufferString(body))
		req.Header.Set("X-User-Id", "u")
		req = mux.SetURLVars(req, map[string]string{"group_id": id})
		rec := httptest.NewRecorder()
		UpdateGroupPlacement(rec, req)
		return rec
	}
	for _, step := range []struct {
		id, body string
		status   int
	}{
		{"b", `{"before_group_id":"a"}`, 200},
		{"b", `{"pinned":true}`, 200},
		{"a", `{"pinned":true,"before_group_id":"b"}`, 200},
		{"a", `{"before_group_id":"other"}`, 404},
		{"other", `{"pinned":true}`, 404},
	} {
		if rec := invoke(step.id, step.body); rec.Code != step.status {
			t.Fatalf("%s: %d %s", step.id, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-User-Id", "u")
	rec := httptest.NewRecorder()
	ListGroups(rec, req)
	var response struct {
		Groups []GroupDTO `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Groups) != 2 || response.Groups[0].ID != "a" || response.Groups[1].ID != "b" {
		t.Fatalf("saved order: %s", rec.Body.String())
	}
	for _, g := range response.Groups {
		if !g.Pinned || g.Version != 7 {
			t.Fatalf("placement changed organizer metadata: %+v", g)
		}
	}
	var other orm.ConversationGroup
	db.Where("id=?", "other").Take(&other)
	if other.Pinned || other.SortOrder != 0 {
		t.Fatal("changed another user's placement")
	}
}

func TestGroupDetailUsesLastActivityAndSearchFindsMemberSummary(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationOpening{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	group := orm.ConversationGroup{ID: "g", UserID: "u", Name: "Group", NormalizedName: "group", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"old-pinned", "recent"} {
		conv := orm.Conversation{ID: id, DisplayName: id, BaseModel: orm.BaseModel{CreateUserID: "u", CreatedAt: now, UpdatedAt: now.Add(time.Duration(i) * time.Hour)}}
		if i == 0 {
			conv.PinnedAt = &now
		}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.ConversationGroupMember{ConversationID: id, GroupID: "g", UserID: "u", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.ConversationOpening{ConversationID: "recent", UserID: "u", Summary: "searchable topic", InputJSON: json.RawMessage(`{}`), SourceHistoryIDs: json.RawMessage(`[]`), UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-User-Id", "u")
	req = mux.SetURLVars(req, map[string]string{"group_id": "g"})
	rec := httptest.NewRecorder()
	GetGroup(rec, req)
	var detail struct {
		Conversations []struct {
			ID string `json:"conversation_id"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Conversations) != 2 || detail.Conversations[0].ID != "recent" {
		t.Fatalf("activity order: %s", rec.Body.String())
	}
	req = httptest.NewRequest("GET", "/?keyword=searchable", nil)
	req.Header.Set("X-User-Id", "u")
	rec = httptest.NewRecorder()
	ListGroups(rec, req)
	var list struct {
		Groups []GroupDTO `json:"groups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Groups) != 1 || list.Groups[0].ID != "g" || list.Groups[0].MemberCount != 2 {
		t.Fatalf("summary search: %s", rec.Body.String())
	}
}
