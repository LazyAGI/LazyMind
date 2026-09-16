package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestExtensionWebSocketAuthenticatesAndRoutesCommand(t *testing.T) {
	hub, err := NewHub()
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := hub.CreatePairing("user-1")
	if err != nil {
		t.Fatal(err)
	}
	paired, err := hub.PairExtension(PairExtensionInput{Code: pairing.Code, DeviceName: "Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(hub)
	mux := http.NewServeMux()
	mux.HandleFunc("/connect", handler.ConnectExtension)
	server := httptest.NewServer(mux)
	defer server.Close()

	dialer := websocket.Dialer{Subprotocols: []string{"lazymind.browser.v1"}}
	header := http.Header{"Origin": []string{"chrome-extension://abcdefghijklmnop"}}
	ws, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/connect", header)
	if err != nil {
		if response != nil {
			t.Fatalf("dial status=%d err=%v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer ws.Close()
	if err := ws.WriteJSON(helloEnvelope{
		Type: "hello", ProtocolVersion: ProtocolVersion,
		DeviceID: paired.DeviceID, DeviceToken: paired.DeviceToken,
	}); err != nil {
		t.Fatal(err)
	}
	var hello map[string]any
	if err := ws.ReadJSON(&hello); err != nil {
		t.Fatal(err)
	}
	if hello["type"] != "hello_ack" {
		t.Fatalf("hello = %#v", hello)
	}

	type outcome struct {
		raw json.RawMessage
		err error
	}
	done := make(chan outcome, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		raw, callErr := hub.Call(ctx, "user-1", paired.DeviceID, "snapshot", SessionInput{SessionID: "bs_test"})
		done <- outcome{raw: raw, err: callErr}
	}()
	var command commandEnvelope
	if err := ws.ReadJSON(&command); err != nil {
		t.Fatal(err)
	}
	if command.Action != "snapshot" || command.ID == "" {
		t.Fatalf("command = %#v", command)
	}
	if err := ws.WriteJSON(resultEnvelope{
		Type: "result", ID: command.ID, OK: true, Result: json.RawMessage(`{"revision":3}`),
	}); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || string(result.raw) != `{"revision":3}` {
		t.Fatalf("result=%s err=%v", result.raw, result.err)
	}
}

func TestExtensionWebSocketRejectsWebOrigin(t *testing.T) {
	hub, _ := NewHub()
	handler := NewHTTPHandler(hub)
	server := httptest.NewServer(http.HandlerFunc(handler.ConnectExtension))
	defer server.Close()
	dialer := websocket.Dialer{Subprotocols: []string{"lazymind.browser.v1"}}
	responseHeader := http.Header{"Origin": []string{"https://attacker.example"}}
	connection, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), responseHeader)
	if connection != nil {
		_ = connection.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("connection=%v status=%v err=%v", connection, response, err)
	}
}

func TestExtensionOriginsIncludeChromeAndEdge(t *testing.T) {
	for _, origin := range []string{
		"chrome-extension://abcdefghijklmnop",
		"edge-extension://abcdefghijklmnop",
	} {
		req := httptest.NewRequest(http.MethodGet, "/connect", nil)
		req.Header.Set("Origin", origin)
		if !validExtensionOrigin(req) {
			t.Fatalf("extension origin rejected: %s", origin)
		}
	}
}
