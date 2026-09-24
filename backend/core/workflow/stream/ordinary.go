package stream

import (
	"fmt"
	"net/http"
	"time"

	"lazymind/core/common/taskdisplay"
)

// Ordinary streams carry only the public projection. Persisted workflow events
// include prompts, graph definitions and executor payloads and must not be sent
// to this view, including during Last-Event-ID replay.
func (h Handler) serveOrdinary(w http.ResponseWriter, r *http.Request, flusher http.Flusher, sessionID, owner string, after int64) {
	requestID := taskdisplay.PrepareRequest(w, r)
	if err := h.Store.AuthorizeSession(r.Context(), sessionID, owner); err != nil {
		taskdisplay.ReplyError(w, r, "workflow session unavailable", http.StatusNotFound)
		return
	}
	if h.Snapshot == nil {
		taskdisplay.ReplyError(w, r, "Unable to load task details", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	updates, cancel := h.Store.Subscribe(sessionID)
	defer cancel()
	failed := func() {
		taskdisplay.Observe(r.Context(), taskdisplay.EventRejected, 0, 1)
		_ = writeEvent(w, flusher, 0, "error", streamError{Code: "PUBLIC_PROJECTION_UNAVAILABLE", Message: "Unable to reload task details", Retryable: true, RequestID: requestID})
	}
	// Capture the cursor before reading the projection. Changes committed during
	// the read are replayed afterwards; a duplicate snapshot is harmless.
	refresh := func() bool {
		if err := h.Store.AuthorizeSession(r.Context(), sessionID, owner); err != nil {
			failed()
			return false
		}
		cursor, err := h.Store.LatestEventID(r.Context(), sessionID, owner)
		if err != nil {
			failed()
			return false
		}
		if after > cursor {
			taskdisplay.Observe(r.Context(), taskdisplay.EventResync, 0, 1)
			_ = writeEvent(w, flusher, 0, "resync_required", map[string]any{"schema_version": 1, "reason": "cursor_expired"})
		}
		snapshot, err := h.Snapshot(r, sessionID, owner)
		if err != nil {
			failed()
			return false
		}
		if err := writeEvent(w, flusher, cursor, "snapshot", snapshot); err != nil {
			return false
		}
		after = cursor
		return true
	}
	if !refresh() {
		return
	}
	poll := h.PollInterval
	if poll <= 0 {
		poll = time.Second
	}
	poller := time.NewTicker(poll)
	defer poller.Stop()
	heartbeat := h.Heartbeat
	if heartbeat <= 0 {
		heartbeat = 20 * time.Second
	}
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	drain := func() bool {
		events, err := h.Store.Replay(r.Context(), sessionID, owner, after, 1)
		if err != nil {
			failed()
			return false
		}
		return len(events) == 0 || refresh()
	}
	if !drain() {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates:
			if !drain() {
				return
			}
		case <-poller.C:
			if !drain() {
				return
			}
		case <-ticker.C:
			if err := h.Store.AuthorizeSession(r.Context(), sessionID, owner); err != nil {
				failed()
				return
			}
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
