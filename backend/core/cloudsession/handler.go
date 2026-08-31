package cloudsession

import (
	"context"
	"net/http"
	"time"

	"lazymind/core/cloudclient"
	"lazymind/core/common"
)

type AccountClient interface {
	GetCurrentAccount(context.Context, string) (cloudclient.Account, error)
}

type TemporaryCredentialCleaner interface {
	ClearTemporary(context.Context) error
}

type Handler struct {
	Service              *Service
	Accounts             AccountClient
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
		common.ReplyOK(w, Status{State: StateSignedOut})
		return
	}
	status := h.Service.Status(r.Context())
	status.RegistrationURL = h.RegistrationURL
	if status.State == StateSignedIn && h.Accounts != nil {
		if token, err := h.Service.AccessToken(r.Context(), 30*time.Second); err == nil {
			if account, err := h.Accounts.GetCurrentAccount(r.Context(), token); err == nil {
				status.AccountID = account.ID
				status.Username = account.Username
				status.EmailMasked = account.EmailMasked
			}
		}
	}
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
		common.ReplyOK(w, Status{State: StateSignedOut})
		return
	}
	_ = h.Service.Logout(r.Context())
	if cleanupErr != nil {
		common.ReplyErr(w, "temporary credentials could not be cleared", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, h.Service.Status(r.Context()))
}
