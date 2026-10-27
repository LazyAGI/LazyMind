package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestNonStreamHTTPAndHistoryShareFailureTerminal(t *testing.T) {
	invoked := true
	for _, tc := range []struct {
		name                             string
		terminal                         *RunTerminal
		text, think                      string
		truncated                        bool
		cancelRequest                    bool
		wantStatus, wantReason, wantCode string
	}{
		{name: "authentication", terminal: &RunTerminal{Status: "failed", Reason: "model_failure", Code: "authentication_failed", ModelInvoked: &invoked}, wantStatus: "failed", wantReason: "model_failure", wantCode: "authentication_failed"},
		{name: "provider missing", terminal: &RunTerminal{Status: "failed", Reason: "model_failure", Code: "not_found"}, wantStatus: "failed", wantReason: "model_failure", wantCode: "not_found"},
		{name: "runtime protocol", terminal: &RunTerminal{Status: "failed", Reason: "runtime_failure", Code: "protocol_error"}, wantStatus: "failed", wantReason: "runtime_failure", wantCode: "protocol_error"},
		{name: "incomplete", terminal: &RunTerminal{Status: "interrupted", Reason: "model_incomplete", Code: "length"}, wantStatus: "interrupted", wantReason: "model_incomplete", wantCode: "length"},
		{name: "cancelled", terminal: &RunTerminal{Status: "cancelled", Reason: "user_cancelled"}, wantStatus: "cancelled", wantReason: "user_cancelled"},
		{name: "request cancelled", cancelRequest: true, wantStatus: "cancelled", wantReason: "user_cancelled"},
		{name: "reasoning preserved", think: "partial reasoning", terminal: &RunTerminal{Status: "failed", Reason: "model_failure", Code: "rate_limited", PartialOutput: true}, wantStatus: "failed", wantReason: "model_failure", wantCode: "rate_limited"},
		{name: "transport partial", text: "partial answer", truncated: true, wantStatus: "failed", wantReason: "runtime_failure", wantCode: "transport_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newChatExecutionTestDB(t).DB
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			conversation := orm.Conversation{ID: "contract-chat", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
			if err := db.Create(&conversation).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				frames := []string{}
				if tc.text != "" || tc.think != "" {
					frames = append(frames, algorithmFrame(t, map[string]any{"text": tc.text, "think": tc.think}))
				}
				if tc.terminal != nil {
					frames = append(frames, algorithmFrame(t, map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, *tc.terminal)}))
				}
				body := strings.Join(frames, "\n") + "\n"
				if tc.truncated {
					w.Header().Set("Content-Length", strconv.Itoa(len(body)+16))
				}
				_, _ = fmt.Fprint(w, body)
				if tc.cancelRequest {
					w.(http.Flusher).Flush()
					cancel()
				}
			}))
			defer upstream.Close()
			response := httptest.NewRecorder()
			handleNonStreamChat(response, ctx, db, nil, upstream.URL, map[string]any{"user_id": "user-1"}, conversation.ID, "test question", chatPersistTarget{Seq: 1, HistoryID: "contract-history"}, nil)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("HTTP=%d body=%s", response.Code, response.Body.String())
			}
			var payload struct {
				Data struct {
					RunTerminal *RunTerminal `json:"run_terminal"`
					Detail      struct {
						HistoryID string `json:"history_id"`
					} `json:"detail"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", "contract-history").Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			terminal, err := parseRunTerminal(history.RunTerminal)
			if err != nil {
				t.Fatalf("invalid persisted terminal: %s %v", history.RunTerminal, err)
			}
			if terminal.Status != tc.wantStatus || terminal.Reason != tc.wantReason || terminal.Code != tc.wantCode {
				t.Fatalf("terminal=%+v", terminal)
			}
			if !reflect.DeepEqual(payload.Data.RunTerminal, terminal) || payload.Data.Detail.HistoryID != history.ID {
				t.Fatalf("HTTP/history mismatch: HTTP=%+v stored=%+v", payload.Data, terminal)
			}
			if tc.text != "" && !strings.Contains(history.Result, tc.text) || tc.think != "" && !strings.Contains(history.Result, tc.think) {
				t.Fatalf("partial output lost: %q", history.Result)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/core/conversations/contract-chat:history", nil)
			request.Header.Set("X-User-Id", "user-1")
			request = mux.SetURLVars(request, map[string]string{"name": "contract-chat:history"})
			refreshed := httptest.NewRecorder()
			GetConversationHistory(refreshed, request)
			var restored struct {
				History []struct {
					RunTerminal *RunTerminal `json:"run_terminal"`
				} `json:"history"`
			}
			if err := json.Unmarshal(refreshed.Body.Bytes(), &restored); err != nil {
				t.Fatal(err)
			}
			if refreshed.Code != http.StatusOK || len(restored.History) != 1 || !reflect.DeepEqual(restored.History[0].RunTerminal, payload.Data.RunTerminal) {
				t.Fatalf("refresh changed terminal: %s", refreshed.Body.String())
			}
		})
	}
}

func TestNonStreamTransientRequestFailureWritePreservesAcceptedTerminal(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%t", cancelRequest), func(t *testing.T) {
			db := newChatExecutionTestDB(t).DB
			stateStore := newRunDecisionTestStore(t)
			store.Init(db, nil, stateStore)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			conversation := orm.Conversation{ID: "request-write-contract", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
			if err := db.Create(&conversation).Error; err != nil {
				t.Fatal(err)
			}
			var writes int
			callback := "contract-transient-request-failure-write"
			if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok || tx.Statement.Table != "chat_histories" || values["run_terminal"] == nil {
					return
				}
				writes++
				if writes == 1 {
					tx.AddError(errors.New("injected transient request failure write"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Callback().Update().Remove(callback) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if cancelRequest {
					cancel()
				}
				http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
			}))
			defer upstream.Close()
			response := httptest.NewRecorder()
			handleNonStreamChat(response, ctx, db, stateStore, upstream.URL, map[string]any{"user_id": "user-1"}, conversation.ID, "test question", chatPersistTarget{Seq: 1, HistoryID: "request-write-history"}, nil)
			if response.Code != http.StatusBadGateway {
				t.Fatalf("HTTP=%d body=%s", response.Code, response.Body.String())
			}
			var payload struct {
				Data struct {
					RunTerminal *RunTerminal `json:"run_terminal"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", "request-write-history").Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			stored, err := parseRunTerminal(history.RunTerminal)
			if err != nil || stored == nil || !reflect.DeepEqual(stored, payload.Data.RunTerminal) || history.RunStatus != stored.Status {
				t.Fatalf("HTTP/DB mismatch: HTTP=%+v DB=%s err=%v", payload.Data.RunTerminal, history.RunTerminal, err)
			}
			cached, err := getChatStatus(context.Background(), stateStore, conversation.ID, history.ID)
			if err != nil || !reflect.DeepEqual(cached.RunTerminal, stored) || cached.Status != stored.Status {
				t.Fatalf("cache/DB mismatch: cache=%+v DB=%+v err=%v", cached, stored, err)
			}
			if cancelRequest && (stored.Status != "cancelled" || stored.Reason != "user_cancelled" || stored.Code != "") {
				t.Fatalf("invalid cancellation: %+v", stored)
			}
			if !cancelRequest && (stored.Status != "failed" || stored.Code != "upstream_request_failed" || stored.DiagnosticID == "") {
				t.Fatalf("invalid request failure: %+v", stored)
			}
			if writes != 2 || calls.Load() != 1 {
				t.Fatalf("writes=%d upstream calls=%d", writes, calls.Load())
			}
		})
	}
}

func TestNonStreamTransientFinalWritePreservesAcceptedTerminal(t *testing.T) {
	for _, terminal := range []RunTerminal{
		{Status: "completed", Reason: "normal", PartialOutput: true},
		{Status: "failed", Reason: "model_failure", Code: "authentication_failed", PartialOutput: true},
	} {
		t.Run(terminal.Reason, func(t *testing.T) {
			db := newChatExecutionTestDB(t).DB
			stateStore := newRunDecisionTestStore(t)
			store.Init(db, nil, stateStore)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			conversation := orm.Conversation{ID: "write-contract", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
			if err := db.Create(&conversation).Error; err != nil {
				t.Fatal(err)
			}
			var writes int
			callback := "contract-transient-final-write"
			if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok || tx.Statement.Table != "chat_histories" || values["run_status"] != terminal.Status {
					return
				}
				writes++
				if writes == 1 {
					tx.AddError(errors.New("injected transient final write failure"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Callback().Update().Remove(callback) })
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				_, _ = fmt.Fprintln(w, algorithmFrame(t, map[string]any{"text": "preserved answer"}))
				_, _ = fmt.Fprintln(w, algorithmFrame(t, map[string]any{"runtime_event": runFinishedEvent(request.Conversation.RunID, terminal)}))
			}))
			defer upstream.Close()
			response := httptest.NewRecorder()
			handleNonStreamChat(response, context.Background(), db, stateStore, upstream.URL, map[string]any{"user_id": "user-1"}, conversation.ID, "test question", chatPersistTarget{Seq: 1, HistoryID: "write-history"}, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("transient write did not recover: HTTP=%d %s", response.Code, response.Body.String())
			}
			var payload struct {
				Data struct {
					RuntimeEvent *ChatRuntimeEvent `json:"runtime_event"`
				} `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Data.RuntimeEvent == nil {
				t.Fatal("missing HTTP terminal event")
			}
			httpTerminal, err := payload.Data.RuntimeEvent.Terminal()
			if err != nil {
				t.Fatal(err)
			}
			var history orm.ChatHistory
			if err := db.Where("id = ?", "write-history").Take(&history).Error; err != nil {
				t.Fatal(err)
			}
			stored, err := parseRunTerminal(history.RunTerminal)
			if err != nil || !reflect.DeepEqual(stored, httpTerminal) || history.RunStatus != terminal.Status || history.Result != "preserved answer" {
				t.Fatalf("HTTP/DB mismatch: HTTP=%+v DB=%+v err=%v", httpTerminal, history, err)
			}
			cached, err := getChatStatus(context.Background(), stateStore, conversation.ID, history.ID)
			if err != nil || !reflect.DeepEqual(cached.RunTerminal, stored) || cached.Status != stored.Status {
				t.Fatalf("cache/DB mismatch: cache=%+v DB=%+v err=%v", cached, stored, err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/core/conversations/write-contract:history", nil)
			request.Header.Set("X-User-Id", "user-1")
			request = mux.SetURLVars(request, map[string]string{"name": "write-contract:history"})
			refreshed := httptest.NewRecorder()
			GetConversationHistory(refreshed, request)
			var restored struct {
				History []struct {
					RunTerminal *RunTerminal `json:"run_terminal"`
				} `json:"history"`
			}
			if err := json.Unmarshal(refreshed.Body.Bytes(), &restored); err != nil {
				t.Fatal(err)
			}
			if refreshed.Code != http.StatusOK || len(restored.History) != 1 || !reflect.DeepEqual(restored.History[0].RunTerminal, stored) {
				t.Fatalf("refresh changed terminal: %s", refreshed.Body.String())
			}
			if writes != 2 || calls.Load() != 1 {
				t.Fatalf("writes=%d model calls=%d", writes, calls.Load())
			}
		})
	}
}
