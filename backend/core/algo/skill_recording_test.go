package algo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestRecordingProgressArrivesBeforeResult(t *testing.T) {
	received := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat/recording_skill_stream" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		fmt.Fprintln(w, `{"progress":45}`)
		w.(http.Flusher).Flush()
		select {
		case <-received:
		case <-time.After(3 * time.Second):
			t.Error("progress was buffered until completion")
			return
		}
		fmt.Fprintln(w, `{"progress":90}`)
		fmt.Fprintln(w, `{"result":{"name":"Export","content":"1. Export"}}`)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	var updates []int
	ctx := WithRecordingProgress(context.Background(), func(p int) {
		updates = append(updates, p)
		if p == 45 {
			close(received)
		}
	})
	result, err := GenerateRecordingSkill(ctx, nil, "", RecordingEvidence{}, nil)
	if err != nil || result.Name != "Export" || !reflect.DeepEqual(updates, []int{45, 90}) {
		t.Fatalf("result=%+v err=%v progress=%v", result, err, updates)
	}
}

func TestRecordingStreamWithoutResultFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"progress":90}`)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	_, err := GenerateRecordingSkill(WithRecordingProgress(context.Background(), func(int) {}), nil, "", RecordingEvidence{}, nil)
	if err == nil {
		t.Fatal("missing terminal result accepted")
	}
}
