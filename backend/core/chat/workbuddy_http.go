package chat

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/externallease"
	"lazymind/core/store"
)

type workBuddyExecutorStatus struct {
	Installed         bool   `json:"installed"`
	Ready             bool   `json:"ready"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

func WorkBuddyExecutorStatus(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	status := workBuddyExecutorStatus{Installed: true}
	gateway, err := newWorkBuddyGateway(r.Context(), owner)
	if err != nil {
		if errors.Is(err, errWorkBuddyAuthorizationRequired) {
			status.UnavailableReason = errWorkBuddyAuthorizationRequired.Error()
		} else {
			status.UnavailableReason = "WorkBuddy status unavailable: " + err.Error()
		}
		common.ReplyOK(w, status)
		return
	}
	online, err := gateway.online(r.Context())
	if err != nil {
		status.UnavailableReason = "WorkBuddy status unavailable: " + err.Error()
	} else if !online {
		status.UnavailableReason = "WorkBuddy desktop is offline"
	} else {
		status.Ready = true
	}
	common.ReplyOK(w, status)
}

func ExecuteWorkBuddyRun(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	var input struct {
		RunID          string `json:"run_id"`
		ConversationID string `json:"conversation_id"`
		HostID         string `json:"host_id"`
		LeaseToken     string `json:"lease_token"`
	}
	if owner == "" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&input) != nil {
		common.ReplyErr(w, "invalid WorkBuddy execution request", http.StatusBadRequest)
		return
	}
	input.RunID = strings.TrimSpace(input.RunID)
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.HostID = strings.TrimSpace(input.HostID)
	input.LeaseToken = strings.TrimSpace(input.LeaseToken)
	lease := externallease.Request{
		Owner: owner, RunID: input.RunID, ConversationID: input.ConversationID,
		HostID: input.HostID, LeaseToken: input.LeaseToken,
		Operation: externallease.OperationExternalAgentInvoke,
	}
	if err := externallease.ValidateRequest(r.Context(), store.DB(), lease, time.Now().UTC()); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusConflict)
		return
	}
	var run orm.ExternalChatRun
	if err := store.DB().WithContext(r.Context()).
		Where("id = ? AND actor_user_id = ? AND provider = ?", input.RunID, owner, ChatExecutorWorkBuddy).
		Take(&run).Error; err != nil {
		common.ReplyErr(w, "WorkBuddy run not found", http.StatusNotFound)
		return
	}
	gateway, err := newWorkBuddyGateway(r.Context(), owner)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	result, err := gateway.execute(r.Context(), run.Prompt)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, result)
}
