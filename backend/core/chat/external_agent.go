package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/externalagent"
	"lazymind/core/store"
)

type externalAgentExecutor struct {
	Provider         string
	ProviderThreadID string
	RequestID        string
}

type externalAgentChatContext struct {
	service *externalagent.Service
	seq     int
}

func parseExternalAgentExecutor(raw map[string]any) (*externalAgentExecutor, error) {
	value, ok := raw["executor"]
	if !ok || value == nil {
		return nil, nil
	}
	executor, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid request: executor must be an object")
	}
	kind := strings.TrimSpace(stringValue(executor["kind"]))
	if kind != "external_agent" {
		return nil, nil
	}
	parsed := &externalAgentExecutor{
		Provider:         strings.ToLower(strings.TrimSpace(stringValue(executor["provider"]))),
		ProviderThreadID: strings.TrimSpace(stringValue(executor["provider_thread_id"])),
		RequestID:        strings.TrimSpace(stringValue(executor["request_id"])),
	}
	if parsed.Provider != externalagent.ProviderCodex {
		return nil, externalagent.ErrUnsupportedProvider
	}
	if parsed.ProviderThreadID == "" {
		return nil, fmt.Errorf("invalid request: executor.provider_thread_id required")
	}
	if parsed.RequestID == "" {
		return nil, fmt.Errorf("invalid request: executor.request_id required")
	}
	return parsed, nil
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func ensureExternalAgentConversation(
	ctx context.Context,
	db *gorm.DB,
	service *externalagent.Service,
	conversationID, providerThreadID string,
) (*orm.Conversation, int, error) {
	binding, err := service.BindingByConversation(ctx, conversationID)
	if err != nil {
		return nil, 0, err
	}
	if binding.Provider != externalagent.ProviderCodex || binding.ProviderThreadID != providerThreadID {
		return nil, 0, externalagent.ErrBindingNotFound
	}
	var conversation orm.Conversation
	if err := db.WithContext(ctx).Where("id = ?", conversationID).First(&conversation).Error; err != nil {
		return nil, 0, err
	}
	var count int64
	if err := db.WithContext(ctx).Model(&orm.ChatHistory{}).
		Where("conversation_id = ?", conversationID).Count(&count).Error; err != nil {
		return nil, 0, err
	}
	return &conversation, int(count) + 1, nil
}

func prepareExternalAgentChatConversation(
	ctx context.Context,
	db *gorm.DB,
	executor *externalAgentExecutor,
	conversationID string,
) (*externalAgentChatContext, error) {
	service, err := externalagent.Default()
	if err != nil {
		return nil, err
	}
	_, seq, err := ensureExternalAgentConversation(ctx, db, service, conversationID, executor.ProviderThreadID)
	if err != nil {
		return nil, err
	}
	return &externalAgentChatContext{service: service, seq: seq}, nil
}

func handleExternalAgentChat(
	w http.ResponseWriter,
	r *http.Request,
	service *externalagent.Service,
	executor *externalAgentExecutor,
	conversationID, historyID, query, userID string,
	seq int,
	stream bool,
) {
	execution, err := service.StartOrSteer(r.Context(), externalagent.ChatInput{
		Provider: executor.Provider, ProviderThreadID: executor.ProviderThreadID,
		ConversationID: conversationID, HistoryID: historyID, RequestID: executor.RequestID,
		Query: query, ActorUserID: userID, Seq: seq,
	})
	if err != nil {
		status := http.StatusConflict
		switch {
		case errors.Is(err, externalagent.ErrBindingNotFound):
			status = http.StatusNotFound
		case errors.Is(err, externalagent.ErrUnsupportedProvider):
			status = http.StatusBadRequest
		case errors.Is(err, externalagent.ErrUnmanagedActive), errors.Is(err, externalagent.ErrThreadBusy):
			status = http.StatusConflict
		default:
			status = http.StatusBadGateway
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	responseSeq := execution.Seq
	if responseSeq <= 0 {
		responseSeq = seq
	}
	if !stream {
		var final externalagent.Event
		for event := range execution.Events {
			if event.Terminal {
				final = event
			}
		}
		common.ReplyOK(w, map[string]any{
			"conversation_id":      conversationID,
			"history_id":           execution.HistoryID,
			"message":              final.Message,
			"external_agent_event": final,
		})
		return
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
	writeSSEChunk(w, flusher, map[string]any{
		"conversation_id": conversationID,
		"seq":             responseSeq,
		"history_id":      execution.HistoryID,
		"delta":           "",
		"finish_reason":   "FINISH_REASON_UNSPECIFIED",
		"external_agent_event": externalagent.Event{
			Type: "run_attached", Provider: executor.Provider,
			ThreadID: executor.ProviderThreadID, RunID: execution.RunID,
		},
	})
	for {
		select {
		case <-r.Context().Done():
			// An HTTP disconnect is not a user cancellation. The managed Run
			// continues and persists its final result in the background.
			return
		case event, open := <-execution.Events:
			if !open {
				return
			}
			finishReason := "FINISH_REASON_UNSPECIFIED"
			if event.Terminal {
				if event.Type == "turn_failed" {
					finishReason = "FINISH_REASON_UNKNOWN"
				} else {
					finishReason = "FINISH_REASON_STOP"
				}
			}
			writeSSEChunk(w, flusher, map[string]any{
				"conversation_id":      conversationID,
				"seq":                  responseSeq,
				"history_id":           execution.HistoryID,
				"delta":                event.Delta,
				"message":              event.Message,
				"finish_reason":        finishReason,
				"external_agent_event": event,
			})
			if event.Terminal {
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
		}
	}
}

// BindExternalAgentConversation creates only LazyMind's lightweight
// Conversation/binding metadata. Native provider history remains in Codex.
func BindExternalAgentConversation(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(mux.Vars(r)["provider"]))
	if provider != externalagent.ProviderCodex {
		common.ReplyErr(w, externalagent.ErrUnsupportedProvider.Error(), http.StatusNotFound)
		return
	}
	var body struct {
		ConversationID   string `json:"conversation_id"`
		ProviderThreadID string `json:"provider_thread_id"`
		NewSession       bool   `json:"new_session"`
		Cwd              string `json:"cwd"`
		DisplayName      string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	service, err := externalagent.Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	userID, userName := store.UserID(r), store.UserName(r)
	if userID == "" {
		userID = "0"
	}
	var thread externalagent.Thread
	managed := body.NewSession
	if body.NewSession {
		thread, err = service.StartThread(r.Context(), externalagent.StartThreadInput{Cwd: body.Cwd})
	} else {
		body.ProviderThreadID = strings.TrimSpace(body.ProviderThreadID)
		if body.ProviderThreadID == "" {
			common.ReplyErr(w, "invalid request: provider_thread_id required", http.StatusBadRequest)
			return
		}
		if existing, lookupErr := service.BindingByThread(r.Context(), provider, body.ProviderThreadID); lookupErr == nil {
			common.ReplyOK(w, bindingResponse(existing, externalagent.Thread{ID: body.ProviderThreadID}))
			return
		}
		thread, err = service.ReadThread(r.Context(), body.ProviderThreadID)
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	thread.Turns = nil
	conversationID := strings.TrimSpace(body.ConversationID)
	if conversationID == "" {
		conversationID = newConversationID()
	}
	displayName := strings.TrimSpace(body.DisplayName)
	if displayName == "" && thread.Name != nil {
		displayName = strings.TrimSpace(*thread.Name)
	}
	if displayName == "" {
		displayName = strings.TrimSpace(thread.Preview)
	}
	if displayName == "" {
		displayName = "Codex 会话"
	}
	if len([]rune(displayName)) > maxConversationDisplayNameLength {
		displayName = string([]rune(displayName)[:maxConversationDisplayNameLength])
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	if _, _, err := ensureConversation(r.Context(), db, conversationID, displayName, nil, nil, userID, userName, nil); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	binding, err := service.Bind(r.Context(), externalagent.BindInput{
		Provider: provider, ProviderThreadID: thread.ID, ConversationID: conversationID,
		CreatedByUserID: userID, Managed: managed,
	})
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	common.ReplyOK(w, bindingResponse(binding, thread))
}

func bindingResponse(binding orm.ExternalAgentBinding, thread externalagent.Thread) map[string]any {
	return map[string]any{
		"binding": map[string]any{
			"conversation_id":     binding.ConversationID,
			"provider":            binding.Provider,
			"provider_thread_id":  binding.ProviderThreadID,
			"managed_by_lazymind": binding.ManagedByLazyMind,
		},
		"thread": thread,
	}
}
