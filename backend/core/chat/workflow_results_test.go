package chat

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestChatListsExternalWorkflowResultsWithoutModelsOrHistory(t *testing.T) {
	db := newConversationTestDB(t).DB
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowSlotRevision{}); err != nil {
		t.Fatal(err)
	}
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	for _, owner := range []string{"owner", "other"} {
		if err := db.Create(&orm.WorkflowSession{ID: owner + "-run", WorkflowID: "ppt", CreateUserID: owner,
			OriginHost: "external-agent", ControllerHost: "external-agent", Status: "completed",
			CreatedAt: now, UpdatedAt: now, IntentContext: `{"text":"论文四页总结"}`}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSlotRevision{ID: owner + "-artifact", SessionID: owner + "-run", SlotID: "pptx",
			Slot: "pptx", Selected: true, Revision: 1, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/core/conversations", nil)
	request.Header.Set("X-User-Id", "owner")
	recorder := httptest.NewRecorder()
	ListConversations(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var listed struct {
		Conversations []map[string]any `json:"conversations"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil || len(listed.Conversations) != 1 {
		t.Fatalf("list=%s err=%v", recorder.Body.String(), err)
	}
	item := listed.Conversations[0]
	conversationID, _ := item["conversation_id"].(string)
	if item["external_workflow_session_id"] != "owner-run" || item["execution_owner"] != "external_agent" || item["display_name"] != "论文四页总结" {
		t.Fatalf("missing result contract: %+v", item)
	}
	for _, endpoint := range []struct {
		name string
		read http.HandlerFunc
	}{{"get", GetConversation}, {"detail", GetConversationDetail}} {
		request := httptest.NewRequest(http.MethodGet, "/api/core/conversations/"+conversationID, nil)
		request = mux.SetURLVars(request, map[string]string{"name": "conversations/" + conversationID})
		request.Header.Set("X-User-Id", "owner")
		recorder := httptest.NewRecorder()
		endpoint.read(recorder, request)
		var response map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || recorder.Code != http.StatusOK {
			t.Fatalf("%s: status=%d err=%v body=%s", endpoint.name, recorder.Code, err, recorder.Body.String())
		}
		if endpoint.name == "detail" {
			response, _ = response["conversation"].(map[string]any)
		}
		if response["external_workflow_session_id"] != "owner-run" || response["execution_owner"] != "external_agent" {
			t.Fatalf("%s lost result metadata: %+v", endpoint.name, response)
		}
		request.Header.Set("X-User-Id", "other")
		recorder = httptest.NewRecorder()
		endpoint.read(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s exposed foreign conversation: %d", endpoint.name, recorder.Code)
		}
	}
	if _, _, err := ensureConversation(t.Context(), db, conversationID, "", nil, nil, "owner", "", false, "", nil, nil); !errors.Is(err, errExternalWorkflowConversation) {
		t.Fatalf("results conversation admitted a local chat/model run: %v", err)
	}
	var count int64
	if err := db.Model(&orm.ChatHistory{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("reading result created chat history: count=%d error=%v", count, err)
	}
}
