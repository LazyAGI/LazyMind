package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
)

func TestForkChatRequestSearchConfigPrecedence(t *testing.T) {
	inherited := &DatasetFilters{DatasetIDs: []string{"kb-a"}, Creators: []string{"u1"}, Tags: []string{"inherited"}}
	for _, test := range []struct {
		name            string
		fields          string
		want            *DatasetFilters
		wantForbidden   bool
		revokeInherited bool
	}{
		{name: "explicit replacement", fields: `,"conversation":{"search_config":{"dataset_list":[{"id":"kb-b"}],"creators":[],"tags":[]}}`, want: &DatasetFilters{DatasetIDs: []string{"kb-b"}}},
		// The composer sends an empty dataset_list when an uploaded file takes priority.
		{name: "explicit empty dataset list", fields: `,"conversation":{"search_config":{"dataset_list":[],"creators":[],"tags":[]}}`},
		{name: "explicit empty search config", fields: `,"conversation":{"search_config":{}}`},
		{name: "omitted nested fields do not inherit", fields: `,"conversation":{"search_config":{"tags":["requested"]}}`, want: &DatasetFilters{Tags: []string{"requested"}}},
		{name: "top level filters win", fields: `,"filters":{"kb_id":["kb-b"]},"conversation":{"search_config":{"dataset_list":[{"id":"kb-a"}]}}`, want: &DatasetFilters{DatasetIDs: []string{"kb-b"}}},
		{name: "empty top level filters win", fields: `,"filters":{},"conversation":{"search_config":{"dataset_list":[{"id":"kb-b"}]}}`},
		{name: "omitted conversation inherits", want: inherited},
		{name: "omitted search config inherits", fields: `,"conversation":{}`, want: inherited},
		{name: "null search config inherits", fields: `,"conversation":{"search_config":null}`, want: inherited},
		{name: "unreadable requested dataset rejected", fields: `,"conversation":{"search_config":{"dataset_list":[{"id":"kb-foreign"}]}}`, wantForbidden: true},
		{name: "revoked inherited dataset rejected", fields: `,"conversation":{"search_config":{"dataset_list":[{"id":"kb-b"}]}}`, revokeInherited: true, wantForbidden: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, source, histories, _ := forkFixture(t, 1)
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			seedConversationSearchConfigDataset(t, db, "kb-a", "u1")
			seedConversationSearchConfigDataset(t, db, "kb-b", "u1")
			seedConversationSearchConfigDataset(t, db, "kb-foreign", "another-user")
			config := forkConfigFromHistory(histories[0])
			config.Filters = map[string]any{"kb_id": []string{"kb-a"}, "creator": []string{"u1"}, "tags": []string{"inherited"}}
			histories[0].Ext = marshalChatHistoryExt(map[string]any{"conversation_config_snapshot": config})
			if err := db.Model(&histories[0]).Update("ext", histories[0].Ext).Error; err != nil {
				t.Fatal(err)
			}
			revision, err := forkPrefixRevision(histories)
			if err != nil {
				t.Fatal(err)
			}
			result, err := createConversationFork(t.Context(), db, doc.DatasetCatalogCaller{UserID: "u1"}, source.ID, "search-config", forkCreateRequest{SourceHistoryID: histories[0].ID, ExpectedPrefixRevision: revision})
			if err != nil {
				t.Fatal(err)
			}
			conversationID := result.Conversation["conversation_id"].(string)
			if test.revokeInherited {
				if err := db.Model(&orm.Dataset{}).Where("id = ?", "kb-a").Update("create_user_id", "another-user").Error; err != nil {
					t.Fatal(err)
				}
			}
			captured := make(chan LazyChatRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/scan/sources" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"items":[],"total":0}`))
					return
				}
				var request LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				captured <- request
				encoder := json.NewEncoder(w)
				_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"text": "answer"}})
				_ = encoder.Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, RunTerminal{Status: "completed", Reason: "normal"})}})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", server.URL)
			recorder := httptest.NewRecorder()
			Chat(recorder, sidechatRequest(http.MethodPost, "/api/core/chat", "u1", fmt.Sprintf(`{"conversation_id":%q,"query":"continue branch","stream":false%s}`, conversationID, test.fields), nil))
			if test.wantForbidden {
				if recorder.Code != http.StatusForbidden {
					t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
				}
				if len(captured) != 0 {
					t.Fatal("unauthorized request reached model")
				}
				return
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("chat status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			select {
			case request := <-captured:
				if !reflect.DeepEqual(request.Retrieval.Filters, test.want) {
					t.Fatalf("retrieval filters=%#v, want %#v", request.Retrieval.Filters, test.want)
				}
			default:
				t.Fatal("no upstream request")
			}
		})
	}
}
