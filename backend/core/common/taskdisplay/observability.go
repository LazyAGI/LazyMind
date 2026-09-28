package taskdisplay

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"lazymind/core/common"
	appLog "lazymind/core/log"
)

const (
	EventSnapshot       = "snapshot"
	EventMissingProcess = "process_missing"
	EventRejected       = "event_rejected"
	EventDuplicate      = "event_duplicate"
	EventResync         = "resync"
)

type requestIDKey struct{}

// PrepareRequest deliberately ignores caller-supplied request IDs. Only a
// server-generated opaque identifier is reflected into responses and logs.
func PrepareRequest(w http.ResponseWriter, r *http.Request) string {
	id := RequestID(r.Context())
	if id == "" {
		id = uuid.NewString()
		*r = *r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id))
	}
	w.Header().Set("X-Request-ID", id)
	return id
}
func RequestID(ctx context.Context) string { id, _ := ctx.Value(requestIDKey{}).(string); return id }

// Observe intentionally has no arbitrary fields, error or message argument.
// This makes prompt, credentials, source URLs and filesystem paths impossible
// to accidentally add to the public-display metrics/log boundary.
func Observe(ctx context.Context, event string, elapsed time.Duration, count int) {
	switch event {
	case EventSnapshot, EventMissingProcess, EventRejected, EventDuplicate, EventResync:
	default:
		return
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if count < 0 {
		count = 0
	}
	appLog.Logger.Info().Str("component", "task_display").Str("event", event).
		Str("request_id", RequestID(ctx)).Int64("duration_ms", elapsed.Milliseconds()).Int("count", count).Msg("Task display observation")
}

func ReplyError(w http.ResponseWriter, r *http.Request, message string, status int) {
	id := PrepareRequest(w, r)
	resolved := common.ResolveAppError(message, status)
	appLog.Logger.Warn().Str("component", "task_display").Str("event", "request_failed").Str("request_id", id).
		Int("http_status", resolved.HTTPStatus).Int("error_code", resolved.Code).Msg("Task display request failed")
	common.ReplyErrWithData(w, message, map[string]any{"request_id": id}, status)
}
