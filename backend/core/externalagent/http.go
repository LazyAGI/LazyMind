package externalagent

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

const maxRunQueryBytes = 64 << 10

func RunHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRunQueryBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body struct {
		Query string `json:"query"`
	}
	if err := decoder.Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	body.Query = strings.TrimSpace(body.Query)
	if body.Query == "" {
		common.ReplyErr(w, "invalid request: query required", http.StatusBadRequest)
		return
	}
	requestID := operationID(r)
	if requestID == "" {
		common.ReplyErr(w, "invalid request: X-Request-Id required", http.StatusBadRequest)
		return
	}
	conversationID := strings.TrimSpace(mux.Vars(r)["conversation_id"])
	actor := actorUserID(r)
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	binding, err := service.BindingByConversation(r.Context(), conversationID, actor)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, ErrBindingNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	var conversation orm.Conversation
	if err := db.WithContext(r.Context()).Where("id = ?", conversationID).First(&conversation).Error; err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	var historyCount int64
	if err := db.WithContext(r.Context()).Model(&orm.ChatHistory{}).
		Where("conversation_id = ?", conversationID).Count(&historyCount).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	execution, err := service.StartOrSteer(r.Context(), ChatInput{
		Provider: ProviderCodex, ProviderThreadID: binding.ProviderThreadID,
		ConversationID: conversationID, HistoryID: uuid.NewString(), RequestID: requestID,
		Query: body.Query, ActorUserID: actor, Seq: int(historyCount) + 1,
	})
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, ErrBindingNotFound), errors.Is(err, gorm.ErrRecordNotFound):
			status = http.StatusNotFound
		case errors.Is(err, ErrThreadBusy), errors.Is(err, ErrUnmanagedActive):
			status = http.StatusConflict
		case errors.Is(err, ErrOperationMismatch):
			status = http.StatusConflict
		case errors.Is(err, ErrUnsupportedProvider):
			status = http.StatusBadRequest
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	if execution.Cancel != nil {
		defer execution.Cancel()
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		common.ReplyErr(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	snapshot := RunSnapshot{
		ConversationID: conversationID,
		RunID:          execution.RunID,
		Status:         runStatusRunning,
	}
	writeRunUpdate(w, flusher, RunUpdate{
		ConversationID: conversationID, HistoryID: execution.HistoryID, Seq: execution.Seq,
		Event:    Event{Type: "run_attached", Provider: binding.Provider, ThreadID: binding.ProviderThreadID, RunID: execution.RunID},
		Snapshot: snapshot,
	})
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-execution.Events:
			if !open {
				return
			}
			snapshot = applyRunEventSnapshot(snapshot, event)
			writeRunUpdate(w, flusher, RunUpdate{
				ConversationID: conversationID, HistoryID: execution.HistoryID, Seq: execution.Seq, Event: event, Snapshot: snapshot,
			})
			if event.Terminal {
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
		}
	}
}

func applyRunEventSnapshot(snapshot RunSnapshot, event Event) RunSnapshot {
	snapshot.RunID = event.RunID
	if event.Status != "" {
		snapshot.Status = event.Status
	}
	switch event.Type {
	case "run_attached", "turn_started", "progress", "agent_message_delta", "thread_forked":
		snapshot.Status = runStatusRunning
		snapshot.PendingRequest = nil
	case "request_required":
		snapshot.Status = runStatusWaiting
		snapshot.PendingRequest = event.Request
	case "turn_completed":
		snapshot.Status = runStatusCompleted
		snapshot.PendingRequest = nil
	case "turn_failed":
		snapshot.Status = runStatusFailed
		snapshot.PendingRequest = nil
	case "turn_interrupted":
		snapshot.Status = runStatusInterrupted
		snapshot.PendingRequest = nil
	}
	if event.Delta != "" {
		snapshot.Answer = truncateRunText(snapshot.Answer+event.Delta, 16000)
	}
	if event.Message != "" {
		snapshot.Answer = truncateRunText(event.Message, 16000)
	}
	if event.ControlRelease != "" {
		snapshot.ControlRelease = event.ControlRelease
	}
	return snapshot
}

func truncateRunText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[len(runes)-limit:])
}

func writeRunUpdate(w http.ResponseWriter, flusher http.Flusher, update RunUpdate) {
	payload, _ := json.Marshal(update)
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(payload)
	_, _ = w.Write([]byte("\n\n"))
	flusher.Flush()
}

func ListThreadsHTTP(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	if err := validateProvider(provider); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	page, err := service.ListThreads(
		r.Context(),
		r.URL.Query().Get("cursor"),
		r.URL.Query().Get("cwd"),
		limit,
		actorUserID(r),
	)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrInvalidCursor) {
			status = http.StatusBadRequest
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	common.ReplyOK(w, page)
}

func ListProjectsHTTP(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	if err := validateProvider(provider); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
		return
	}
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	projects, err := service.ListProjects(
		r.Context(),
		r.URL.Query().Get("cursor"),
		limit,
		actorUserID(r),
	)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrInvalidCursor) {
			status = http.StatusBadRequest
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	common.ReplyOK(w, projects)
}

func ReadThreadHTTP(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	if err := validateProvider(provider); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusNotFound)
		return
	}
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if r.URL.Query().Has("offset") || r.URL.Query().Has("limit") {
		page, err := service.ReadThreadPage(
			r.Context(),
			strings.TrimSpace(mux.Vars(r)["thread_id"]),
			offset,
			limit,
			r.URL.Query().Get("tail") == "true",
			actorUserID(r),
		)
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, ErrThreadNotFound) {
				status = http.StatusNotFound
			}
			common.ReplyErr(w, err.Error(), status)
			return
		}
		common.ReplyOK(w, page)
		return
	}
	thread, err := service.ReadThread(
		r.Context(),
		strings.TrimSpace(mux.Vars(r)["thread_id"]),
		actorUserID(r),
	)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrThreadNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	common.ReplyOK(w, thread)
}

func InterruptHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ExpectedRunID string `json:"expected_run_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ExpectedRunID) == "" {
		common.ReplyErr(w, "invalid request: expected_run_id required", http.StatusBadRequest)
		return
	}
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	actor := actorUserID(r)
	if !claimMutation(w, r, service, actor, "interrupt", map[string]any{
		"conversation_id": mux.Vars(r)["conversation_id"],
		"expected_run_id": strings.TrimSpace(body.ExpectedRunID),
	}) {
		return
	}
	if err := service.Interrupt(r.Context(), mux.Vars(r)["conversation_id"], actor, strings.TrimSpace(body.ExpectedRunID)); err != nil {
		status := http.StatusConflict
		if errors.Is(err, ErrBindingNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	if err := service.CompleteOperation(
		r.Context(), actor, operationID(r), "interrupt", map[string]any{},
	); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{})
}

func ReleaseHTTP(w http.ResponseWriter, r *http.Request) {
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	actor := actorUserID(r)
	if !claimMutation(w, r, service, actor, "release", map[string]any{
		"conversation_id": mux.Vars(r)["conversation_id"],
	}) {
		return
	}
	if err := service.Release(
		r.Context(),
		mux.Vars(r)["conversation_id"],
		actor,
	); err != nil {
		status := http.StatusConflict
		if errors.Is(err, ErrBindingNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	if err := service.CompleteOperation(
		r.Context(), actor, operationID(r), "release", map[string]any{},
	); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{})
}

func RespondRequestHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body struct {
		ActionID string                           `json:"action_id"`
		Answers  map[string]ExternalRequestAnswer `json:"answers,omitempty"`
	}
	if err := decoder.Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	actor := actorUserID(r)
	if !claimMutation(w, r, service, actor, "respond", map[string]any{
		"request_id": mux.Vars(r)["request_id"],
		"action_id":  strings.TrimSpace(body.ActionID),
		"answers":    body.Answers,
	}) {
		return
	}
	err = service.RespondRequest(RequestResponse{
		RequestID:   mux.Vars(r)["request_id"],
		ActionID:    strings.TrimSpace(body.ActionID),
		Answers:     body.Answers,
		ActorUserID: actor,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrRequestNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	if err := service.CompleteOperation(
		r.Context(), actor, operationID(r), "respond", map[string]any{},
	); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{})
}

func operationID(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Request-Id"))
}

func claimMutation(
	w http.ResponseWriter,
	r *http.Request,
	service *Service,
	actor, kind string,
	request any,
) bool {
	_, claimed, err := service.ClaimOperation(
		r.Context(), actor, operationID(r), kind, request,
	)
	if err == nil && !claimed {
		common.ReplyOK(w, map[string]any{})
		return false
	}
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrInvalidOperationIdentity) {
			status = http.StatusBadRequest
		} else if errors.Is(err, ErrOperationPending) || errors.Is(err, ErrOperationMismatch) {
			status = http.StatusConflict
		}
		common.ReplyErr(w, err.Error(), status)
		return false
	}
	return true
}

func actorUserID(r *http.Request) string {
	userID := store.UserID(r)
	if userID == "" {
		return "0"
	}
	return userID
}
