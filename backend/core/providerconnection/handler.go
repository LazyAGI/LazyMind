package providerconnection

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"lazymind/core/cloudclient"
	applog "lazymind/core/log"
)

type Handler struct{ Service *Service }

func (handler Handler) CreateSession(w http.ResponseWriter, request *http.Request) {
	var body struct {
		Provider string `json:"provider"`
	}
	if handler.Service == nil || json.NewDecoder(request.Body).Decode(&body) != nil {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	session, err := handler.Service.CreateSessionForUser(request.Context(), request.Header.Get("X-User-Id"), body.Provider)
	if err != nil {
		applog.Logger.Warn().
			Str("operation", "create_provider_connection_session").
			Str("error_type", fmt.Sprintf("%T", err)).
			Bool("cloud_reauth_required", errors.Is(err, ErrCloudReauthRequired)).
			Bool("cloud_unavailable", errors.Is(err, ErrCloudUnavailable)).
			Msg("Provider Connection request failed")
	}
	writeResult(w, session, http.StatusCreated, err)
}

func (handler Handler) GetSession(w http.ResponseWriter, request *http.Request) {
	if handler.Service == nil {
		http.Error(w, "Provider Connection service is unavailable", http.StatusServiceUnavailable)
		return
	}
	session, err := handler.Service.GetSession(request.Context(), request.Header.Get("X-User-Id"), mux.Vars(request)["session_id"])
	writeResult(w, session, http.StatusOK, err)
}

func (handler Handler) CancelSession(w http.ResponseWriter, request *http.Request) {
	if handler.Service == nil {
		http.Error(w, "Provider Connection service is unavailable", http.StatusServiceUnavailable)
		return
	}
	err := handler.Service.CancelSessionForUser(request.Context(), request.Header.Get("X-User-Id"), mux.Vars(request)["session_id"])
	writeResult(w, nil, http.StatusNoContent, err)
}

func (handler Handler) List(w http.ResponseWriter, request *http.Request) {
	if handler.Service == nil {
		writeResult(w, cloudclient.ProviderConnectionPage{Items: []cloudclient.ProviderConnection{}}, http.StatusOK, nil)
		return
	}
	result, err := handler.Service.ListConnections(request.Context(), request.Header.Get("X-User-Id"))
	if errors.Is(err, ErrCloudReauthRequired) || errors.Is(err, ErrCloudUnavailable) {
		writeResult(w, cloudclient.ProviderConnectionPage{Items: []cloudclient.ProviderConnection{}}, http.StatusOK, nil)
		return
	}
	writeResult(w, result, http.StatusOK, err)
}

func (handler Handler) Reauthorize(w http.ResponseWriter, request *http.Request) {
	if handler.Service == nil {
		http.Error(w, "Provider Connection service is unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := handler.Service.ReauthorizeForUser(request.Context(), request.Header.Get("X-User-Id"), mux.Vars(request)["auth_connection_id"])
	writeResult(w, result, http.StatusCreated, err)
}

func (handler Handler) Revoke(w http.ResponseWriter, request *http.Request) {
	if handler.Service == nil {
		http.Error(w, "Provider Connection service is unavailable", http.StatusServiceUnavailable)
		return
	}
	err := handler.Service.Revoke(request.Context(), mux.Vars(request)["auth_connection_id"])
	writeResult(w, nil, http.StatusNoContent, err)
}

func writeResult(w http.ResponseWriter, result any, status int, err error) {
	if err != nil {
		code := http.StatusServiceUnavailable
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error_code": publicProviderConnectionErrorCode(err),
			"message":    "Provider Connection request failed",
		})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func publicProviderConnectionErrorCode(err error) string {
	var cliError *FeishuCLICommandError
	if errors.As(err, &cliError) || errors.Is(err, ErrCLINotInstalled) || errors.Is(err, ErrCLIVersionUnsupported) ||
		errors.Is(err, ErrCLIIntegrityMismatch) || errors.Is(err, ErrCLIOutputInvalid) || errors.Is(err, ErrCLITimeout) ||
		errors.Is(err, ErrCLIUnavailable) || errors.Is(err, ErrCLIProfileNotFound) || errors.Is(err, ErrCLIProfileOwnerMismatch) ||
		errors.Is(err, ErrCLIProfileTenantMismatch) {
		return safeFeishuCLIErrorCode(err)
	}
	if strings.Contains(err.Error(), "not found") {
		return "PROVIDER_CONNECTION_NOT_FOUND"
	}
	return "PROVIDER_CONNECTION_UNAVAILABLE"
}
