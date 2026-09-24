package taskdisplay

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	appLog "lazymind/core/log"
)

func TestPublicDisplayObservabilityHasOnlySafeFixedFields(t *testing.T) {
	var output bytes.Buffer
	previous := appLog.Logger
	appLog.Logger = zerolog.New(&output)
	t.Cleanup(func() { appLog.Logger = previous })
	req := httptest.NewRequest("GET", "https://private.internal/task?token=SECRET_URL", strings.NewReader("SECRET_BODY /private/SECRET_PATH"))
	req.Header.Set("X-Request-ID", "SECRET_REQUEST_ID")
	req.Header.Set("Authorization", "SECRET_AUTH")
	response := httptest.NewRecorder()
	id := PrepareRequest(response, req)
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("request ID not server-generated UUID: %q", id)
	}
	if PrepareRequest(response, req) != id || RequestID(req.Context()) != id || response.Header().Get("X-Request-ID") != id {
		t.Fatal("request ID was not stable")
	}
	Observe(req.Context(), EventSnapshot, 12*time.Millisecond, 2)
	Observe(req.Context(), EventMissingProcess, 0, 1)
	Observe(req.Context(), "SECRET_EVENT /private/SECRET_PATH", 0, 1)
	ReplyError(response, req, "task not found", http.StatusNotFound)
	if response.Code != 404 {
		t.Fatalf("status=%d", response.Code)
	}
	var body struct {
		Data struct {
			RequestID string `json:"request_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Data.RequestID != id {
		t.Fatalf("error request ID missing: %s %v", response.Body.String(), err)
	}
	allowed := map[string]bool{"level": true, "component": true, "event": true, "request_id": true, "duration_ms": true, "count": true, "message": true, "http_status": true, "error_code": true}
	rows := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(rows) != 3 {
		t.Fatalf("unexpected log records: %s", output.String())
	}
	for _, line := range rows {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatal(err)
		}
		if fields["request_id"] != id {
			t.Fatalf("log request ID differs: %s", line)
		}
		for key := range fields {
			if !allowed[key] {
				t.Fatalf("unapproved log field %q", key)
			}
		}
	}
	if strings.Contains(output.String(), "SECRET") || strings.Contains(response.Body.String(), "SECRET") {
		t.Fatalf("sensitive request fields escaped into public observability: %s", output.String())
	}
}
