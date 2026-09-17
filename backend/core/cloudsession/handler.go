package cloudsession

import (
	"context"
	"errors"
	"net/http"

	"lazymind/core/common"
)

type TemporaryCredentialCleaner interface {
	ClearTemporary(context.Context) error
}

type Handler struct {
	Service              *Service
	Login                *LoginCoordinator
	TemporaryCredentials TemporaryCredentialCleaner
	RegistrationURL      string
}

func (h Handler) BeginLogin(w http.ResponseWriter, r *http.Request) {
	if h.Login == nil {
		common.ReplyErr(w, "LazyMind Cloud browser login is unavailable", http.StatusServiceUnavailable)
		return
	}
	start, err := h.Login.Start(r.Context())
	if err != nil {
		common.ReplyErr(w, "LazyMind Cloud browser login could not start", http.StatusBadGateway)
		return
	}
	common.ReplyOK(w, start)
}

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.ReplyOK(w, Status{State: StateSignedOut, Reachability: ReachabilityUnknown})
		return
	}
	status := h.Service.Status(r.Context())
	status.RegistrationURL = h.RegistrationURL
	common.ReplyOK(w, status)
}

func (h Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if h.Login != nil {
		h.Login.Cancel()
	}
	var cleanupErr error
	if h.TemporaryCredentials != nil {
		cleanupErr = h.TemporaryCredentials.ClearTemporary(r.Context())
	}
	if h.Service == nil {
		if cleanupErr != nil {
			common.ReplyErr(w, "temporary credentials could not be cleared", http.StatusServiceUnavailable)
			return
		}
		common.ReplyOK(w, Status{State: StateSignedOut, Reachability: ReachabilityUnknown})
		return
	}
	logoutErr := h.Service.Logout(r.Context())
	if cleanupErr != nil {
		common.ReplyErr(w, "temporary credentials could not be cleared", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(logoutErr, ErrLocalLogoutFailed) {
		common.ReplyErr(w, "cloud session could not be cleared from local storage", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, h.Service.Status(r.Context()))
}
