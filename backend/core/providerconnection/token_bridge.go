package providerconnection

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	applog "lazymind/core/log"
)

type TokenBridge struct {
	Service       *Service
	InternalToken string
}

func (bridge TokenBridge) Resolve(w http.ResponseWriter, request *http.Request) {
	provided := strings.TrimSpace(request.Header.Get("X-LazyMind-Internal-Token"))
	expected := strings.TrimSpace(bridge.InternalToken)
	if bridge.Service == nil || expected == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body ResolveRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || strings.TrimSpace(request.Header.Get("X-User-Id")) != strings.TrimSpace(body.UserID) ||
		strings.TrimSpace(request.Header.Get("X-Tenant-Id")) != strings.TrimSpace(body.TenantID) ||
		strings.TrimSpace(mux.Vars(request)["auth_connection_id"]) != strings.TrimSpace(body.AuthConnectionID) {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	resolved, err := bridge.Service.ResolveAccessToken(request.Context(), body)
	if err != nil {
		event := applog.Logger.Warn().Str("operation", "resolve_provider_access_token")
		if classified, ok := err.(interface{ Class() string }); ok {
			event = event.Str("error_class", classified.Class())
		} else {
			event = event.Str("error_class", "local_authorization_or_session")
		}
		event.Msg("Provider Connection request failed")
		http.Error(w, "Provider Connection is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resolved)
}

func (bridge TokenBridge) Report(w http.ResponseWriter, request *http.Request) {
	provided := strings.TrimSpace(request.Header.Get("X-LazyMind-Internal-Token"))
	expected := strings.TrimSpace(bridge.InternalToken)
	if bridge.Service == nil || expected == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body AccessTokenFailureReport
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || strings.TrimSpace(request.Header.Get("X-User-Id")) != strings.TrimSpace(body.UserID) ||
		strings.TrimSpace(request.Header.Get("X-Tenant-Id")) != strings.TrimSpace(body.TenantID) ||
		strings.TrimSpace(mux.Vars(request)["auth_connection_id"]) != strings.TrimSpace(body.AuthConnectionID) {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	if err := bridge.Service.ReportAccessTokenFailure(request.Context(), body); err != nil {
		applog.Logger.Warn().Str("operation", "report_provider_access_token_failure").Msg("Provider Connection request failed")
		http.Error(w, "Provider Connection is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (bridge TokenBridge) ExecuteFeishuCLI(w http.ResponseWriter, request *http.Request) {
	provided := strings.TrimSpace(request.Header.Get("X-LazyMind-Internal-Token"))
	expected := strings.TrimSpace(bridge.InternalToken)
	if bridge.Service == nil || expected == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Handle    string            `json:"handle"`
		Operation string            `json:"operation"`
		Params    map[string]string `json:"params"`
		Identity  string            `json:"identity"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || strings.TrimSpace(body.Handle) == "" || strings.TrimSpace(body.Operation) == "" || body.Identity != "user" || len(body.Params) > 16 {
		http.Error(w, "invalid request", http.StatusUnprocessableEntity)
		return
	}
	result, err := bridge.Service.ExecuteFeishuCLI(request.Context(), body.Handle, body.Operation, body.Params)
	if err != nil {
		applog.Logger.Warn().Str("operation", "execute_feishu_cli").Str("error_code", safeFeishuCLIErrorCode(err)).Msg("Feishu CLI request failed")
		http.Error(w, "Feishu connection is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}
