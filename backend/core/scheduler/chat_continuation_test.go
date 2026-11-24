package scheduler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScheduledContinuationMatchesOneLoopbackUserAndPayload(t *testing.T) {
	body := []byte(`{"conversation_id":"scheduled-run","query":"report"}`)
	other := []byte(`{"conversation_id":"different-run","query":"report"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(continuationHeader) == "" {
			t.Error("local dispatch omitted its continuation")
		}
		for _, scenario := range []string{"remote", "host", "origin", "route", "query", "method", "user", "payload", "valid", "replay"} {
			probe := r.Clone(r.Context())
			probe.Body = io.NopCloser(bytes.NewReader(body))
			switch scenario {
			case "remote":
				probe.RemoteAddr = "192.0.2.1:1000"
			case "host":
				probe.Host = "other.invalid"
			case "origin":
				probe.Header.Set("Origin", "https://example.invalid")
			case "route":
				probe.URL.Path = "/new-work"
			case "query":
				probe.URL.RawQuery = "other=true"
			case "method":
				probe.Method = http.MethodPut
			case "user":
				probe.Header.Set("X-User-Id", "other")
			case "payload":
				probe.Body = io.NopCloser(bytes.NewReader(other))
				probe.ContentLength = int64(len(other))
			}
			if matched := ConsumeScheduledChatContinuation(probe); matched != (scenario == "valid") {
				t.Errorf("%s matched=%v", scenario, matched)
			}
			if probe.Header.Get(continuationHeader) != "" {
				t.Errorf("%s left its credential on the business request", scenario)
			}
			if scenario == "valid" {
				got, err := io.ReadAll(probe.Body)
				if err != nil || !bytes.Equal(got, body) {
					t.Errorf("admission changed the scheduled request: %s %v", got, err)
				}
			}
			probe.Body.Close()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/conversations:chat", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-User-Id", "owner")
	resp, err := dispatchScheduledChat(request, "task", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
