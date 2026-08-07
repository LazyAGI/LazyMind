package externalagent

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/store"
)

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
	page, err := service.ListThreads(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, page)
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
		)
		if err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadGateway)
			return
		}
		common.ReplyOK(w, page)
		return
	}
	thread, err := service.ReadThread(r.Context(), strings.TrimSpace(mux.Vars(r)["thread_id"]))
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, thread)
}

func SnapshotConversationHTTP(w http.ResponseWriter, r *http.Request) {
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	snapshot, err := service.SnapshotConversation(
		r.Context(),
		mux.Vars(r)["conversation_id"],
	)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, ErrBindingNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	common.ReplyOK(w, snapshot)
}

func InterruptHTTP(w http.ResponseWriter, r *http.Request) {
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if err := service.Interrupt(r.Context(), mux.Vars(r)["conversation_id"], actorUserID(r)); err != nil {
		status := http.StatusConflict
		if errors.Is(err, ErrBindingNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
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
	if err := service.Release(
		r.Context(),
		mux.Vars(r)["conversation_id"],
		actorUserID(r),
	); err != nil {
		status := http.StatusConflict
		if errors.Is(err, ErrBindingNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	common.ReplyOK(w, map[string]any{})
}

func RespondRequestHTTP(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	service, err := Default()
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	err = service.RespondRequest(RequestResponse{
		RequestID:   mux.Vars(r)["request_id"],
		Payload:     body,
		ActorUserID: actorUserID(r),
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrRequestNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	common.ReplyOK(w, map[string]any{})
}

func actorUserID(r *http.Request) string {
	userID := store.UserID(r)
	if userID == "" {
		return "0"
	}
	return userID
}
