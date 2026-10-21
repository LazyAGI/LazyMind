package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
)

// Read every page so the same assertions cover both membership and pagination.
func keywordConversationIDs(t *testing.T, handler http.HandlerFunc, groupID string, query url.Values) []string {
	t.Helper()
	values := query.Encode()
	ids := []string{}
	seen := map[string]bool{}
	token := ""
	for page := 0; ; page++ {
		if page > 20 {
			t.Fatal("keyword pagination did not finish")
		}
		r := httptest.NewRequest(http.MethodGet, "/?"+values+"&page_size=1&page_token="+url.QueryEscape(token), nil)
		r.Header.Set("X-User-Id", "u1")
		r = mux.SetURLVars(r, map[string]string{"group_id": groupID})
		w := httptest.NewRecorder()
		handler(w, r)
		var result struct {
			Conversations []struct {
				ID string `json:"conversation_id"`
			} `json:"conversations"`
			Next string `json:"next_page_token"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("keyword list: %d %s", w.Code, w.Body.String())
		}
		for _, item := range result.Conversations {
			if seen[item.ID] {
				t.Fatalf("duplicate keyword result %s", item.ID)
			}
			seen[item.ID] = true
			ids = append(ids, item.ID)
		}
		if result.Next == "" {
			break
		}
		if result.Next == token {
			t.Fatal("keyword pagination repeated a token")
		}
		token = result.Next
	}
	sort.Strings(ids)
	return ids
}

func keywordGroupIDs(t *testing.T, query url.Values) []string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil)
	r.Header.Set("X-User-Id", "u1")
	w := httptest.NewRecorder()
	conversationgroup.ListGroups(w, r)
	var result struct {
		Groups []conversationgroup.GroupDTO `json:"groups"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("keyword groups: %d %s", w.Code, w.Body.String())
	}
	ids := []string{}
	for _, group := range result.Groups {
		ids = append(ids, group.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestConversationKeywordSurvivesGroupingMovingAndPinning(t *testing.T) {
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	conversation := orm.Conversation{ID: "matching", DisplayName: "TitleNeedle Änderung", SourceDisplayName: "SourceNeedle ÖQuelle", SourceDocumentID: "DocumentNeedle ΩDocument", BaseModel: orm.BaseModel{CreateUserID: "u1"}}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	metadata := orm.ConversationOpening{ConversationID: conversation.ID, UserID: "u1", Summary: "SummaryNeedle ÜRésumé", InputJSON: json.RawMessage(`{}`), SourceHistoryIDs: json.RawMessage(`[]`)}
	if err := db.Create(&metadata).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if err := db.Create(&orm.ConversationGroup{ID: id, UserID: "u1", Name: id, NormalizedName: id, Collapsed: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, stage := range []string{"recent", "first", "second", "pinned"} {
		switch stage {
		case "first":
			if err := db.Create(&orm.ConversationGroupMember{ConversationID: conversation.ID, GroupID: "first", UserID: "u1"}).Error; err != nil {
				t.Fatal(err)
			}
		case "second":
			if err := db.Model(&orm.ConversationGroupMember{}).Where("conversation_id = ?", conversation.ID).Update("group_id", "second").Error; err != nil {
				t.Fatal(err)
			}
		case "pinned":
			if err := db.Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Update("pinned_at", time.Now().UTC()).Error; err != nil {
				t.Fatal(err)
			}
		}
		t.Run(stage, func(t *testing.T) {
			// SQLite folds ASCII in LOWER/LIKE. Exact non-ASCII uppercase text
			// must survive normalization while ASCII case remains insensitive.
			for _, keyword := range []string{"titleneedle", "SUMMARYNEEDLE", " sourceNEEDLE ", "documentneedle", "Änderung", "ÜRésumé", "ÖQuelle", "ΩDocument"} {
				query := url.Values{"keyword": {keyword}, "assistants": {"lazymind"}, "is_task_conv": {"false"}}
				if got := keywordConversationIDs(t, ListConversations, "", query); !reflect.DeepEqual(got, []string{conversation.ID}) {
					t.Fatalf("history keyword=%q: %v", keyword, got)
				}
				if stage == "recent" {
					continue
				}
				groupID := stage
				if stage == "pinned" {
					groupID = "second"
				}
				if got := keywordGroupIDs(t, query); !reflect.DeepEqual(got, []string{groupID}) {
					t.Fatalf("groups keyword=%q: %v", keyword, got)
				}
				if got := keywordConversationIDs(t, conversationgroup.GetGroup, groupID, query); !reflect.DeepEqual(got, []string{conversation.ID}) {
					t.Fatalf("members keyword=%q: %v", keyword, got)
				}
				query.Set("assistants", "codex")
				if got := keywordGroupIDs(t, query); len(got) != 0 {
					t.Fatalf("member search ignored source filter: %v", got)
				}
			}
		})
	}
}

func TestConversationGroupKeywordIncludesPinnedMembersWithinVisibilityScope(t *testing.T) {
	db := newPromptTestDB(t).DB
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	projectPath := "/workspace/ProjectNeedle/ÑPath"
	for _, groupID := range []string{"normal", "task", "foreign"} {
		owner := "u1"
		if groupID == "foreign" {
			owner = "u2"
		}
		group := orm.ConversationGroup{ID: groupID, UserID: owner, Name: "GroupNeedle Équipe", NormalizedName: "groupneedle équipe", Kind: "project", ProjectPath: &projectPath, IsTaskConv: groupID == "task", Collapsed: true}
		if err := db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"pinned", "ordinary", "lazymind", "hidden", "archived", "deleted"} {
			id := groupID + "-" + kind
			conversation := orm.Conversation{ID: id, DisplayName: "Unrelated title", IsTaskConv: group.IsTaskConv, BaseModel: orm.BaseModel{CreateUserID: owner}}
			if kind == "pinned" {
				conversation.PinnedAt = &now
			}
			if kind == "archived" {
				conversation.ArchivedAt = &now
			}
			if kind == "deleted" {
				conversation.DeletedAt = &now
			}
			if err := db.Create(&conversation).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.ConversationGroupMember{ConversationID: id, GroupID: groupID, UserID: owner}).Error; err != nil {
				t.Fatal(err)
			}
			if kind != "lazymind" {
				binding := orm.ExternalAgentBinding{ID: id, ConversationID: id, Provider: "codex", HostID: "host", ProviderThreadID: id, CreatedByUserID: owner, ManagedByLazyMind: kind != "hidden"}
				if err := db.Create(&binding).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, keyword := range []string{" groupNEEDLE ", "PROJECTNEEDLE", "Équipe", "ÑPath"} {
		for _, mode := range []string{"false", "true"} {
			groupID := "normal"
			if mode == "true" {
				groupID = "task"
			}
			query := url.Values{"keyword": {keyword}, "assistants": {"codex"}, "is_task_conv": {mode}}
			want := []string{groupID + "-ordinary", groupID + "-pinned"}
			if got := keywordConversationIDs(t, ListConversations, "", query); !reflect.DeepEqual(got, want) {
				t.Fatalf("history keyword=%q mode=%s: %v", keyword, mode, got)
			}
			if got := keywordGroupIDs(t, query); !reflect.DeepEqual(got, []string{groupID}) {
				t.Fatalf("groups keyword=%q mode=%s: %v", keyword, mode, got)
			}
			if got := keywordConversationIDs(t, conversationgroup.GetGroup, groupID, query); !reflect.DeepEqual(got, want) {
				t.Fatalf("members keyword=%q mode=%s: %v", keyword, mode, got)
			}
		}
	}
	if err := db.Model(&orm.ConversationGroup{}).Where("user_id = ?", "u1").Update("deleted_at", now).Error; err != nil {
		t.Fatal(err)
	}
	query := url.Values{"keyword": {strings.ToUpper("GroupNeedle")}, "assistants": {"codex"}}
	if got := keywordConversationIDs(t, ListConversations, "", query); len(got) != 0 {
		t.Fatalf("deleted or foreign group label matched conversations: %v", got)
	}
}
