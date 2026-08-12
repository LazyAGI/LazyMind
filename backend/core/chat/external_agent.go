package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/externalagent"
	"lazymind/core/store"
)

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
	operationID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if body.NewSession {
		completed, claimed, claimErr := service.ClaimOperation(
			r.Context(), userID, operationID, "create_thread", map[string]any{
				"conversation_id": strings.TrimSpace(body.ConversationID),
				"cwd":             strings.TrimSpace(body.Cwd),
				"display_name":    strings.TrimSpace(body.DisplayName),
			},
		)
		if claimErr != nil {
			status := http.StatusInternalServerError
			if errors.Is(claimErr, externalagent.ErrInvalidOperationIdentity) {
				status = http.StatusBadRequest
			} else if errors.Is(claimErr, externalagent.ErrOperationPending) ||
				errors.Is(claimErr, externalagent.ErrOperationMismatch) {
				status = http.StatusConflict
			}
			common.ReplyErr(w, claimErr.Error(), status)
			return
		}
		if !claimed {
			var response map[string]any
			if json.Unmarshal(completed, &response) != nil {
				common.ReplyErr(w, "invalid operation receipt", http.StatusInternalServerError)
				return
			}
			common.ReplyOK(w, response)
			return
		}
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
		if existing, lookupErr := service.BindingByThread(r.Context(), provider, body.ProviderThreadID, userID); lookupErr == nil {
			common.ReplyOK(w, bindingResponse(existing, externalagent.Thread{ID: body.ProviderThreadID}))
			return
		} else if !errors.Is(lookupErr, externalagent.ErrBindingNotFound) {
			common.ReplyErr(w, lookupErr.Error(), http.StatusConflict)
			return
		}
		thread, err = service.ReadThread(r.Context(), body.ProviderThreadID, userID)
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	mutationContext := r.Context()
	if body.NewSession {
		var cancel context.CancelFunc
		mutationContext, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
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
	if _, _, err := ensureConversation(mutationContext, db, conversationID, displayName, nil, nil, userID, userName, nil); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	binding, err := service.Bind(mutationContext, externalagent.BindInput{
		Provider: provider, ProviderThreadID: thread.ID, ConversationID: conversationID,
		CreatedByUserID: userID, CreatedByLazyMind: managed,
	})
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	response := bindingResponse(binding, thread)
	if body.NewSession {
		if err := service.CompleteOperation(
			mutationContext, userID, operationID, "create_thread", response,
		); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
			return
		}
		nameContext, cancelName := context.WithTimeout(context.Background(), 2*time.Second)
		if nameErr := service.NameThread(nameContext, thread.ID, displayName); nameErr != nil {
			log.Printf("external agent thread name update failed: %v", nameErr)
		}
		cancelName()
	}
	common.ReplyOK(w, response)
}

func DeleteExternalAgentConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(mux.Vars(r)["conversation_id"])
	if conversationID == "" {
		common.ReplyErr(w, "invalid conversation name", http.StatusBadRequest)
		return
	}
	service, err := externalagent.Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	if err := service.ArchiveBoundThread(r.Context(), conversationID, userID); err != nil {
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, externalagent.ErrBindingNotFound):
			status = http.StatusNotFound
		case errors.Is(err, externalagent.ErrThreadBusy):
			status = http.StatusConflict
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	if err := archiveConversation(r.Context(), db, conversationID, userID, true); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gorm.ErrRecordNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	common.ReplyOK(w, map[string]any{})
}

func bindingResponse(binding orm.ExternalAgentBinding, thread externalagent.Thread) map[string]any {
	return map[string]any{
		"binding": map[string]any{
			"conversation_id":     binding.ConversationID,
			"provider":            binding.Provider,
			"provider_thread_id":  binding.ProviderThreadID,
			"created_by_lazymind": binding.ManagedByLazyMind,
		},
		"thread": thread,
	}
}
