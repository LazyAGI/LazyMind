package chat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestWorkBuddyGatewayExecutesLocalAssistantAndDownloadsImage(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfixture")
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/localassistant":
			_, _ = w.Write([]byte(`{"code":0,"data":{"online":true}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/localassistant/message":
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"msg-user-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/localassistant/message":
			if r.URL.Query().Get("message_id") != "msg-user-1" {
				t.Fatalf("cursor=%q", r.URL.Query().Get("message_id"))
			}
			if polls.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"code":0,"data":{"messages":[]}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"messages":[{"message_id":"msg-assistant-1","role":"assistant","content":["图片已生成"],"msg_type":"text","attachments":[{"name":"dog.png","mime_type":"image/png","url":"` + serverURL(r) + `/assets/dog.png"}]}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/assets/dog.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	gateway := workBuddyGateway{
		baseURL:      server.URL,
		accessToken:  "access-token",
		httpClient:   server.Client(),
		pollInterval: time.Millisecond,
	}
	online, err := gateway.online(context.Background())
	if err != nil || !online {
		t.Fatalf("online=%v err=%v", online, err)
	}
	result, err := gateway.execute(context.Background(), "给我一张小狗图片")
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID != "msg-user-1" || result.Text != "图片已生成" || len(result.Attachments) != 1 {
		t.Fatalf("result=%#v", result)
	}
	attachment := result.Attachments[0]
	if attachment.Filename != "dog.png" || attachment.MediaType != "image/png" ||
		attachment.ContentBase64 != base64.StdEncoding.EncodeToString(png) {
		t.Fatalf("attachment=%#v", attachment)
	}
}

func TestWorkBuddyGatewayReportsOffline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"data":{"online":false}}`))
	}))
	defer server.Close()
	gateway := workBuddyGateway{baseURL: server.URL, accessToken: "token", httpClient: server.Client()}
	online, err := gateway.online(context.Background())
	if err != nil || online {
		t.Fatalf("online=%v err=%v", online, err)
	}
}

func TestExecuteWorkBuddyRunRequiresLeaseAndUsesPersistedPrompt(t *testing.T) {
	app, db := newExternalChatTestApplication(t)
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := app.createRun(context.Background(), &orm.ExternalChatRun{
		ID: "run-workbuddy", RequestID: "request-workbuddy", ConversationID: "conversation-1",
		HistoryID: "history-workbuddy", Provider: ChatExecutorWorkBuddy, ActorUserID: "user-1",
		Action: "start", Status: "pending", Prompt: "persisted WorkBuddy prompt", Query: "visible question",
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.reportHost(context.Background(), "user-1", ChatExecutorWorkBuddy, "host-1", true, true, ""); err != nil {
		t.Fatal(err)
	}
	job, err := app.claim(context.Background(), "user-1", ChatExecutorWorkBuddy, "host-1")
	if err != nil || job == nil {
		t.Fatalf("job=%#v err=%v", job, err)
	}

	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/connections/internal/chat-enabled") {
			_, _ = w.Write([]byte(`{"data":{"items":[{"connection_id":"connection-1"}]}}`))
			return
		}
		if strings.Contains(r.URL.Path, "/connections/connection-1/token") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"workbuddy-token"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer auth.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)

	var sentPrompt string
	workbuddy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			sentPrompt = body["content"]
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"message-1"}}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"code":0,"data":{"messages":[{"message_id":"answer-1","role":"assistant","content":["done"],"attachments":[]}]}}`))
		}
	}))
	defer workbuddy.Close()
	t.Setenv("LAZYMIND_WORKBUDDY_OPENAPI_URL", workbuddy.URL)
	t.Setenv("LAZYMIND_WORKBUDDY_POLL_INTERVAL_MS", "1")

	body, _ := json.Marshal(map[string]string{
		"run_id": job.RunID, "conversation_id": job.ConversationID,
		"host_id": job.HostID, "lease_token": job.LeaseToken,
	})
	request := httptest.NewRequest(http.MethodPost, "/external-chat/providers/workbuddy:execute", strings.NewReader(string(body)))
	request.Header.Set("X-User-Id", "user-1")
	response := httptest.NewRecorder()
	ExecuteWorkBuddyRun(response, request)
	if response.Code != http.StatusOK || sentPrompt != "persisted WorkBuddy prompt" {
		t.Fatalf("status=%d prompt=%q body=%s", response.Code, sentPrompt, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/external-chat/providers/workbuddy:execute", strings.NewReader(`{"run_id":"run-workbuddy"}`))
	request.Header.Set("X-User-Id", "user-1")
	response = httptest.NewRecorder()
	ExecuteWorkBuddyRun(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("invalid lease status=%d body=%s", response.Code, response.Body.String())
	}
}

func serverURL(r *http.Request) string {
	return "http://" + strings.TrimSpace(r.Host)
}
