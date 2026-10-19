package taskcenter

import (
	"encoding/json"
	"time"
)

const notificationHistoryCompletionKey = "_notification_completion"

type notificationHistoryCompletion struct {
	CompletedAt time.Time `json:"completed_at"`
	RunID       string    `json:"run_id,omitempty"`
}

// NotificationHistoryCompletedAt reads the server-owned terminal timestamp.
// Legacy histories retain their original fallback until a terminal writer or
// an edit preserves that value in Ext; later edits cannot move completion.
func NotificationHistoryCompletedAt(createdAt, updatedAt time.Time, ext json.RawMessage) time.Time {
	var fields struct {
		Completion notificationHistoryCompletion `json:"_notification_completion"`
	}
	if json.Unmarshal(ext, &fields) == nil && !fields.Completion.CompletedAt.IsZero() {
		return fields.Completion.CompletedAt
	}
	if createdAt.After(updatedAt) {
		return createdAt
	}
	return updatedAt
}

// WithNotificationCompletionTime persists a completion without a run identity,
// for legacy callers. Run-aware terminal writers use the companion helper.
func WithNotificationCompletionTime(ext json.RawMessage, completedAt time.Time) json.RawMessage {
	return WithNotificationCompletionTimeForRun(ext, completedAt, "")
}

// WithNotificationCompletionTimeForRun replaces only the server-owned marker.
// The owner guard at persistence binds this timestamp to the terminal run.
func WithNotificationCompletionTimeForRun(ext json.RawMessage, completedAt time.Time, runID string) json.RawMessage {
	if completedAt.IsZero() {
		return ext
	}
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(ext, &fields)
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	marker, err := json.Marshal(notificationHistoryCompletion{CompletedAt: completedAt.UTC(), RunID: runID})
	if err != nil {
		return ext
	}
	fields[notificationHistoryCompletionKey] = marker
	encoded, err := json.Marshal(fields)
	if err != nil {
		return ext
	}
	return encoded
}
