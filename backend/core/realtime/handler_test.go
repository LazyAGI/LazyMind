package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func connect(t *testing.T, handler Handler) *websocket.Conn {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	dialer := websocket.Dialer{Subprotocols: []string{"lazymind.realtime.v1"}}
	ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

func readFrame(t *testing.T, ws *websocket.Conn) frame {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var value frame
	if err := ws.ReadJSON(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestMultiplexedStreamsAndCancellation(t *testing.T) {
	var started, cancelled atomic.Int32
	ws := connect(t, Handler{
		Authorize: func(r *http.Request) int {
			if r.Header.Get("Authorization") != "Bearer test" {
				return 401
			}
			r.Header.Set("X-User-Id", "owner")
			return 200
		},
		Routes: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-User-Id") != "owner" || r.Header.Get("Last-Event-ID") != "7" {
				t.Error("identity or cursor lost")
			}
			started.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id: 8\ndata: 中文\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			cancelled.Add(1)
		}),
	})
	for i := 0; i < 12; i++ {
		if err := ws.WriteJSON(command{Type: "open", ID: fmt.Sprint(i), Method: "GET", Path: "/conversations/c1/events?view=ordinary", Authorization: "Bearer test", LastEventID: "7"}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for len(seen) < 12 {
		msg := readFrame(t, ws)
		if msg.Type == "data" {
			if string(msg.Data) != "id: 8\ndata: 中文\n\n" {
				t.Fatal("SSE bytes changed")
			}
			seen[msg.ID] = true
		}
	}
	if err := ws.WriteJSON(command{Type: "cancel", ID: "0"}); err != nil {
		t.Fatal(err)
	}
	for {
		msg := readFrame(t, ws)
		if msg.Type == "end" && msg.ID == "0" {
			break
		}
	}
	if started.Load() != 12 || cancelled.Load() != 1 {
		t.Fatalf("started=%d cancelled=%d", started.Load(), cancelled.Load())
	}
	ws.Close()
	deadline := time.Now().Add(time.Second)
	for cancelled.Load() != 12 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if cancelled.Load() != 12 {
		t.Fatal("disconnect leaked handlers")
	}
}

func TestUnauthorizedAndNonStreamOperationsNeverReachHandler(t *testing.T) {
	var calls atomic.Int32
	ws := connect(t, Handler{Authorize: func(*http.Request) int { return 403 }, Routes: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) })})
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/conversations/c1/events", 403},
		{"POST", "/conversations:chat", 403},
		{"DELETE", "/conversations/c1", 400},
		{"GET", "http://external.invalid/conversations/c1/events", 400},
		{"GET", "/conversations/../events", 400},
		{"GET", "/internal/subagent/tasks/t1/events", 400},
		{"GET", "/conversations/a%2Fb/events", 400},
	} {
		if err := ws.WriteJSON(command{Type: "open", ID: tc.path, Method: tc.method, Path: tc.path}); err != nil {
			t.Fatal(err)
		}
		msg := readFrame(t, ws)
		if msg.Type != "error" || msg.Status != tc.status {
			t.Fatalf("%s: %+v", tc.path, msg)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized execution")
	}
}

func TestPostExecutesOnceAndDisconnectCancels(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{})
	ws := connect(t, Handler{Authorize: func(*http.Request) int { return 200 }, Routes: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["conversation_id"] != "c1" || r.Method != "POST" {
			t.Error("payload changed")
		}
		w.Write([]byte("data: started\n\n"))
		<-r.Context().Done()
		close(done)
	})})
	input := command{Type: "open", ID: "post", Method: "POST", Path: "/conversations:chat", Payload: `{"conversation_id":"c1"}`}
	if err := ws.WriteJSON(input); err != nil {
		t.Fatal(err)
	}
	for readFrame(t, ws).Type != "data" {
	}
	// A duplicate live ID closes the connection; it cannot start a second chat.
	if err := ws.WriteJSON(input); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler not cancelled")
	}
	if calls.Load() != 1 {
		t.Fatal("POST replayed")
	}
}

func TestStreamLimitRejectsWithoutStartingMoreWork(t *testing.T) {
	ws := connect(t, Handler{Authorize: func(*http.Request) int { return 200 }, Routes: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.(http.Flusher).Flush(); <-r.Context().Done() })})
	for i := 0; i < maxStreams; i++ {
		if err := ws.WriteJSON(command{Type: "open", ID: fmt.Sprint(i), Method: "GET", Path: "/tasks/t1:stream"}); err != nil {
			t.Fatal(err)
		}
		if readFrame(t, ws).Type != "headers" {
			t.Fatal("missing headers")
		}
	}
	if err := ws.WriteJSON(command{Type: "open", ID: "overflow", Method: "GET", Path: "/tasks/t1:stream"}); err != nil {
		t.Fatal(err)
	}
	if msg := readFrame(t, ws); msg.Type != "error" || msg.Status != 429 {
		t.Fatalf("%+v", msg)
	}
}

func TestAuthorizeUsesOriginalOperationAndTrustedClaims(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/authorize" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("incorrect authorization request")
		}
		var operation map[string]string
		json.NewDecoder(r.Body).Decode(&operation)
		if operation["method"] != "POST" || operation["path"] != "/api/core/conversations:chat" {
			t.Error(operation)
		}
		fmt.Fprint(w, `{"data":{"user_id":"verified","tenant_id":"tenant"}}`)
	}))
	defer server.Close()
	r := httptest.NewRequest("POST", "/conversations:chat", nil)
	r.Header.Set("Authorization", "Bearer token")
	r.Header.Set("X-User-Id", "forged")
	if status := Authorize(server.URL)(r); status != 200 || r.Header.Get("X-User-Id") != "verified" || r.Header.Get("X-Tenant-Id") != "tenant" {
		t.Fatal("trusted identity not propagated")
	}
}

func TestStreamWriterStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &streamWriter{ctx: ctx, id: "1", header: make(http.Header), send: func(frame) error { return nil }}
	if _, err := w.Write([]byte("event")); err != context.Canceled {
		t.Fatalf("%v", err)
	}
}
