package stream

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	workflowstore "lazymind/core/workflow/store"
)

type SnapshotFunc func(*http.Request, string, string) (any, error)

type Handler struct {
	Store     *workflowstore.Repository
	Snapshot  SnapshotFunc
	Heartbeat time.Duration
}

type streamError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, id int64, eventType string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if id > 0 {
		_, _ = fmt.Fprintf(w, "id: %d\n", id)
	}
	if eventType != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", eventType)
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
	return err
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	owner := strings.TrimSpace(r.Header.Get("X-User-Id"))
	if owner == "" {
		http.Error(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(mux.Vars(r)["session_id"])
	}
	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}
	after := int64(0)
	if raw := strings.TrimSpace(r.Header.Get("Last-Event-ID")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			http.Error(w, "invalid Last-Event-ID", http.StatusBadRequest)
			return
		}
		after = parsed
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	updates, cancel := h.Store.Subscribe(sessionID)
	defer cancel()
	if after == 0 && h.Snapshot != nil {
		cursor, err := h.Store.LatestEventID(r.Context(), sessionID, owner)
		if err != nil {
			_ = writeEvent(w, flusher, 0, "error", streamError{Code: "STREAM_CURSOR_FAILED", Message: err.Error(), Retryable: true})
			return
		}
		snapshot, err := h.Snapshot(r, sessionID, owner)
		if err != nil {
			code := "PERMISSION_DENIED"
			if errors.Is(err, workflowstore.ErrNotFound) {
				code = "WORKFLOW_SESSION_NOT_FOUND"
			}
			_ = writeEvent(w, flusher, 0, "error", streamError{Code: code, Message: err.Error()})
			return
		}
		_ = writeEvent(w, flusher, cursor, "snapshot", snapshot)
		after = cursor
	}
	for {
		events, err := h.Store.Replay(r.Context(), sessionID, owner, after, 1000)
		if err != nil {
			_ = writeEvent(w, flusher, 0, "error", streamError{Code: "STREAM_REPLAY_FAILED", Message: err.Error(), Retryable: true})
			return
		}
		for _, event := range events {
			if err := writeEvent(w, flusher, event.ID, event.EventType, event); err != nil {
				return
			}
			after = event.ID
		}
		if len(events) < 1000 {
			break
		}
	}
	heartbeat := h.Heartbeat
	if heartbeat <= 0 {
		heartbeat = 20 * time.Second
	}
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-updates:
			if event.ID <= after || event.OwnerUserID != owner {
				continue
			}
			if err := writeEvent(w, flusher, event.ID, event.EventType, event); err != nil {
				return
			}
			after = event.ID
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
