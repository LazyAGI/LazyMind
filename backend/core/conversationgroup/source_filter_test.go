package conversationgroup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestGroupSourceFilter(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationOpening{}, &orm.ExternalAgentBinding{}, &orm.ExternalAgentSession{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, task := range []bool{false, true} {
		groupID := "normal"
		kind := KindGroup
		if task {
			groupID = "task"
			kind = KindProject
		}
		if err := db.Create(&orm.ConversationGroup{ID: groupID, UserID: "u", Name: groupID, NormalizedName: groupID, IsTaskConv: task, Kind: kind}).Error; err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{"lazymind", "codex", "workbuddy", "hidden"} {
			id := groupID + source
			if err := db.Create(&orm.Conversation{ID: id, BaseModel: orm.BaseModel{CreateUserID: "u"}, DisplayName: source, IsTaskConv: task}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.ConversationGroupMember{ConversationID: id, GroupID: groupID, UserID: "u", Revision: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if source != "lazymind" {
				provider := source
				if source == "hidden" {
					provider = "codex"
				}
				if err := db.Create(&orm.ExternalAgentBinding{ID: id, ConversationID: id, Provider: provider, HostID: "host", ProviderThreadID: id, CreatedByUserID: "u", ManagedByLazyMind: source != "hidden"}).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	invoke := func(handler http.HandlerFunc, path, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-User-Id", "u")
		req = mux.SetURLVars(req, map[string]string{"group_id": id})
		w := httptest.NewRecorder()
		handler(w, req)
		return w
	}
	for _, query := range []string{"", "&assistants=", "&assistants=%20%20"} {
		t.Run("all-visible/"+query, func(t *testing.T) {
			for _, groupID := range []string{"normal", "task"} {
				task := "false"
				if groupID == "task" {
					task = "true"
				}
				w := invoke(ListGroups, "/?is_task_conv="+task+query, "")
				var list struct {
					Groups []GroupDTO `json:"groups"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Groups) != 1 {
					t.Fatalf("list: %s", w.Body.String())
				}
				if list.Groups[0].MemberCount != 3 || list.Groups[0].TotalMemberCount != 4 {
					t.Errorf("visible/total counts: %+v", list.Groups[0])
				}
				seen := map[string]bool{}
				token := ""
				for {
					w = invoke(GetGroup, "/?page_size=1&page_token="+token+query, groupID)
					var detail struct {
						Conversations []struct {
							ID string `json:"conversation_id"`
						} `json:"conversations"`
						Total int64  `json:"total_size"`
						Next  string `json:"next_page_token"`
					}
					if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil {
						t.Fatalf("detail: %s", w.Body.String())
					}
					if detail.Total != 3 {
						t.Errorf("visible total=%d, want 3", detail.Total)
					}
					for _, c := range detail.Conversations {
						if seen[c.ID] || c.ID == groupID+"hidden" {
							t.Errorf("unexpected member %s", c.ID)
						}
						seen[c.ID] = true
					}
					token = detail.Next
					if token == "" {
						break
					}
				}
				if len(seen) != 3 {
					t.Errorf("visible paginated members: %v", seen)
				}
				w = invoke(ListGroups, "/?keyword=hidden"+query, "")
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Groups) != 0 {
					t.Errorf("search leaked hidden session: %s", w.Body.String())
				}
				w = invoke(GetGroup, "/?keyword=hidden"+query, groupID)
				var search struct {
					Conversations []json.RawMessage `json:"conversations"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &search) != nil || len(search.Conversations) != 0 {
					t.Errorf("detail search leaked hidden session: %s", w.Body.String())
				}
			}
		})
	}
	for _, tc := range []struct {
		sources string
		count   int64
	}{{"codex", 1}, {"lazymind", 1}, {"codex,workbuddy", 2}, {" Codex , WORKBUDDY ", 2}, {"cursor", 0}} {
		for _, groupID := range []string{"normal", "task"} {
			task := "false"
			if groupID == "task" {
				task = "true"
			}
			w := invoke(ListGroups, "/?is_task_conv="+task+"&assistants="+url.QueryEscape(tc.sources), "")
			var list struct {
				Groups []GroupDTO `json:"groups"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Groups) != 1 || list.Groups[0].MemberCount != tc.count {
				t.Fatalf("list %s: %s", tc.sources, w.Body.String())
			}
			if list.Groups[0].TotalMemberCount != 4 {
				t.Fatalf("whole-project deletion count changed: %+v", list.Groups[0])
			}
			token := ""
			seen := map[string]bool{}
			for {
				w = invoke(GetGroup, "/?page_size=1&assistants="+url.QueryEscape(tc.sources)+"&page_token="+token, groupID)
				var detail struct {
					Conversations []struct {
						ID string `json:"conversation_id"`
					} `json:"conversations"`
					Total int64  `json:"total_size"`
					Next  string `json:"next_page_token"`
				}
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Total != tc.count {
					t.Fatalf("detail: %s", w.Body.String())
				}
				for _, c := range detail.Conversations {
					if seen[c.ID] || !strings.Contains(","+strings.ReplaceAll(strings.ToLower(tc.sources), " ", "")+",", ","+strings.TrimPrefix(c.ID, groupID)+",") {
						t.Fatalf("unexpected member %s", c.ID)
					}
					seen[c.ID] = true
				}
				token = detail.Next
				if token == "" {
					break
				}
			}
			if int64(len(seen)) != tc.count {
				t.Fatalf("paginated members %v", seen)
			}
		}
	}
	w := invoke(ListGroups, "/?assistants=codex&keyword=workbuddy", "")
	var list struct {
		Groups []GroupDTO `json:"groups"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Groups) != 0 {
		t.Fatalf("search leaked other sources: %s", w.Body.String())
	}
	for _, handler := range []http.HandlerFunc{ListGroups, GetGroup} {
		if w := invoke(handler, "/?assistants=invalid", "normal"); w.Code != 400 {
			t.Fatalf("invalid source: %d", w.Code)
		}
	}
}
