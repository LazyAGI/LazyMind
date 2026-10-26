package taskcenter

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNotificationHistoryCompletedAtKeepsMetadataAndLegacyFallback(t *testing.T) {
	completed := time.Date(2026, 10, 16, 1, 2, 3, 123456789, time.UTC)
	changed := completed.Add(time.Hour)
	for _, test := range []struct {
		name string
		ext  json.RawMessage
		want time.Time
	}{
		{"empty", nil, changed},
		{"invalid", json.RawMessage(`{`), changed},
		{"invalid_timestamp", json.RawMessage(`{"_notification_completion":{"completed_at":"bad"}}`), changed},
		{"zero_timestamp", json.RawMessage(`{"_notification_completion":{"completed_at":"0001-01-01T00:00:00Z"}}`), changed},
		{"persisted", WithNotificationCompletionTime(json.RawMessage(`{"keep":{"integer":9007199254740993}}`), completed), completed},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NotificationHistoryCompletedAt(completed, changed, test.ext); !got.Equal(test.want) {
				t.Fatalf("completed=%s want=%s", got, test.want)
			}
		})
	}
	ext := WithNotificationCompletionTimeForRun(json.RawMessage(`{"keep":{"integer":9007199254740993}}`), completed, "run-new")
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(ext, &stored); err != nil {
		t.Fatal(err)
	}
	if string(stored["keep"]) != `{"integer":9007199254740993}` {
		t.Fatalf("unrelated metadata changed: %s", ext)
	}
	var completion struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(stored["_notification_completion"], &completion); err != nil {
		t.Fatal(err)
	}
	if completion.RunID != "run-new" {
		t.Fatalf("completion not bound to its run: %s", ext)
	}
	// A fresh terminal run can replace an old run's timestamp.
	next := WithNotificationCompletionTimeForRun(ext, changed, "another-run")
	if got := NotificationHistoryCompletedAt(completed, changed.Add(time.Hour), next); !got.Equal(changed) {
		t.Fatalf("new run retained old time: %s", next)
	}
	if got := NotificationHistoryCompletedAt(changed, completed, nil); !got.Equal(changed) {
		t.Fatalf("legacy fallback discarded later create time: %s", got)
	}
}
