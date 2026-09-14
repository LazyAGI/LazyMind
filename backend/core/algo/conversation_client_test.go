package algo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConversationTitleClientUsesDedicatedContracts(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		for _, key := range []string{"mode", "task_type", "skills", "tools"} {
			if _, ok := request[key]; ok {
				t.Errorf("generic task field %s", key)
			}
		}
		if !strings.Contains(string(request["llm_config"]), "chosen-model") {
			t.Error("model config missing")
		}
		switch r.URL.Path {
		case conversationTitlePath:
			if string(request["input"]) != `{"text":"hello"}` {
				t.Errorf("input: %s", request["input"])
			}
			fmt.Fprint(w, `{"status":"succeeded","output":{"title":"hello","initial_intent_summary":"intent","intent_status":"ready","missing_context":[]}}`)
		case conversationTitlesPath:
			var items []ConversationTitleBatchInput
			if err := json.Unmarshal(request["items"], &items); err != nil {
				t.Error(err)
			}
			if len(items) != 1 || items[0].ID != "c1" {
				t.Errorf("items: %+v", items)
			}
			fmt.Fprint(w, `{"status":"succeeded","output":{"items":[{"id":"c1","title":"hello"}]}}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	config := map[string]any{"llm": map[string]any{"source": "openai", "model": "chosen-model"}}
	input := json.RawMessage(`{"text":"hello"}`)
	single, err := GenerateConversationTitle(t.Context(), input, config, 60)
	if err != nil || single.Output.Title != "hello" {
		t.Fatalf("%+v %v", single, err)
	}
	batch, err := GenerateConversationTitles(t.Context(), []ConversationTitleBatchInput{{ID: "c1", Input: input}}, config, 60)
	if err != nil || len(batch.Output.Items) != 1 {
		t.Fatalf("%+v %v", batch, err)
	}
	if len(paths) != 2 {
		t.Fatalf("calls: %v", paths)
	}
}

func TestConversationGroupingClientStreamsProgressAndCancels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case conversationGroupingExecutionsPath + "execution:cancel":
			fmt.Fprint(w, `{"settled":true}`)
		case conversationGroupingExecutionsPath + "execution:stream", conversationGroupingPath:
			var request map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if _, ok := request["task_type"]; ok {
				t.Error("generic task field present")
			}
			if string(request["input"]) != `{"phase":"batch"}` {
				t.Errorf("input: %s", request["input"])
			}
			if r.URL.Path == conversationGroupingPath {
				fmt.Fprint(w, `{"status":"succeeded","output":{"processed":1}}`)
				return
			}
			fmt.Fprintln(w, `{"type":"progress","state":"generating","received_chars":12}`)
			fmt.Fprintln(w, `{"type":"result","result":{"status":"succeeded","output":{"processed":1}}}`)
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	request := ConversationGroupingRequest{Input: map[string]any{"phase": "batch"}}
	var progress []ConversationGroupingProgress
	result, err := StreamConversationGrouping(t.Context(), "execution", request, func(p ConversationGroupingProgress) error {
		progress = append(progress, p)
		return nil
	})
	if err != nil || result.Status != "succeeded" || len(progress) != 1 || progress[0].ReceivedChars != 12 {
		t.Fatalf("result=%+v progress=%+v err=%v", result, progress, err)
	}
	settled, err := CancelConversationGrouping(t.Context(), "execution")
	if err != nil || !settled {
		t.Fatalf("settled=%v err=%v", settled, err)
	}
	sync, err := RunConversationGrouping(t.Context(), request)
	if err != nil || sync.Status != "succeeded" {
		t.Fatalf("%+v %v", sync, err)
	}
	checkpointErr := errors.New("checkpoint lease lost")
	_, err = StreamConversationGrouping(t.Context(), "execution", request,
		func(ConversationGroupingProgress) error { return checkpointErr })
	if !errors.Is(err, checkpointErr) {
		t.Fatalf("lost callback error: %v", err)
	}
}

func TestConversationGroupingClientRejectsIncompleteTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "http-failure") {
			w.WriteHeader(503)
			return
		}
		fmt.Fprintln(w, `{"type":"progress","state":"waiting"}`)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	_, err := StreamConversationGrouping(t.Context(), "eof", ConversationGroupingRequest{}, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected terminal frame: %v", err)
	}
	_, err = StreamConversationGrouping(t.Context(), "http-failure", ConversationGroupingRequest{}, nil)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("HTTP failure: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = StreamConversationGrouping(ctx, "cancelled", ConversationGroupingRequest{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
