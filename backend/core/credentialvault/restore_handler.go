package credentialvault

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/mux"

	"lazymind/core/cloudclient"
	"lazymind/core/cloudsession"
	"lazymind/core/common"
	"lazymind/core/store"
)

type RestoreServiceFactory func(string) (*RestoreService, error)

type RestoreHandler struct {
	factory  RestoreServiceFactory
	mu       sync.Mutex
	services map[string]*RestoreService
}

func NewRestoreHandler(factory RestoreServiceFactory) (*RestoreHandler, error) {
	if factory == nil {
		return nil, ErrInvalidContract
	}
	return &RestoreHandler{factory: factory, services: make(map[string]*RestoreService)}, nil
}

var defaultRestoreHandlerState = struct {
	sync.RWMutex
	handler *RestoreHandler
}{}

func SetDefaultRestoreHandler(handler *RestoreHandler) {
	defaultRestoreHandlerState.Lock()
	defaultRestoreHandlerState.handler = handler
	defaultRestoreHandlerState.Unlock()
}

func DefaultRestoreHandler() *RestoreHandler {
	defaultRestoreHandlerState.RLock()
	defer defaultRestoreHandlerState.RUnlock()
	return defaultRestoreHandlerState.handler
}

func (handler *RestoreHandler) Discover(w http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.factory == nil {
		common.ReplyOK(w, unavailableRestoreDiscovery("cloud_session_required"))
		return
	}
	service, ok := handler.requestService(w, request)
	if !ok {
		return
	}
	discovery, err := service.Discover(request.Context())
	if err != nil {
		if errors.Is(err, cloudsession.ErrNoRefreshToken) {
			common.ReplyOK(w, unavailableRestoreDiscovery("cloud_session_required"))
			return
		}
		replyRestoreError(w, err)
		return
	}
	common.ReplyOK(w, discovery)
}

func unavailableRestoreDiscovery(reason string) RestoreDiscovery {
	return RestoreDiscovery{
		ReasonCode: reason, RequiresExplicitAction: true,
		Records: make([]RestoreRecordSummary, 0),
	}
}

func (handler *RestoreHandler) Start(w http.ResponseWriter, request *http.Request) {
	service, ok := handler.requestService(w, request)
	if !ok {
		return
	}
	request.Body = http.MaxBytesReader(w, request.Body, 64*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var command RestoreCommand
	if err := decoder.Decode(&command); err != nil {
		common.ReplyErr(w, "credential restore request is invalid", http.StatusBadRequest)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		common.ReplyErr(w, "credential restore request is invalid", http.StatusBadRequest)
		return
	}
	operation, err := service.Start(request.Context(), command)
	if err != nil {
		replyRestoreError(w, err)
		return
	}
	common.ReplyOK(w, operation)
}

func (handler *RestoreHandler) Get(w http.ResponseWriter, request *http.Request) {
	service, ok := handler.requestService(w, request)
	if !ok {
		return
	}
	operation, err := service.Advance(request.Context(), strings.TrimSpace(mux.Vars(request)["operation_id"]))
	if err != nil {
		replyRestoreError(w, err)
		return
	}
	common.ReplyOK(w, operation)
}

func (handler *RestoreHandler) Cancel(w http.ResponseWriter, request *http.Request) {
	service, ok := handler.requestService(w, request)
	if !ok {
		return
	}
	if err := service.Cancel(request.Context(), strings.TrimSpace(mux.Vars(request)["operation_id"])); err != nil {
		replyRestoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (handler *RestoreHandler) ClearTemporaryCredentials(w http.ResponseWriter, request *http.Request) {
	service, ok := handler.requestService(w, request)
	if !ok {
		return
	}
	if err := service.ClearTemporary(request.Context()); err != nil {
		replyRestoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// InternalClearTemporaryCredentials lets the Desktop owner clear existing
// temporary credentials after its renderer closes, without a user session.
func (handler *RestoreHandler) InternalClearTemporaryCredentials(w http.ResponseWriter, request *http.Request) {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	provided := strings.TrimSpace(request.Header.Get("X-LazyMind-Internal-Token"))
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		common.ReplyErr(w, "internal token required", http.StatusUnauthorized)
		return
	}
	if err := handler.ClearTemporary(request.Context()); err != nil {
		replyRestoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (handler *RestoreHandler) ClearTemporary(ctx context.Context) error {
	if handler == nil {
		return nil
	}
	handler.mu.Lock()
	services := make([]*RestoreService, 0, len(handler.services))
	for _, service := range handler.services {
		services = append(services, service)
	}
	handler.mu.Unlock()
	for _, service := range services {
		if err := service.ClearTemporary(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (handler *RestoreHandler) requestService(w http.ResponseWriter, request *http.Request) (*RestoreService, bool) {
	if handler == nil || handler.factory == nil {
		common.ReplyErr(w, "credential restore is unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	userID := strings.TrimSpace(store.UserID(request))
	if userID == "" {
		common.ReplyErr(w, "credential restore requires a local user", http.StatusUnauthorized)
		return nil, false
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if service := handler.services[userID]; service != nil {
		return service, true
	}
	service, err := handler.factory(userID)
	if err != nil {
		common.ReplyErr(w, "credential restore is unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	handler.services[userID] = service
	return service, true
}

func replyRestoreError(w http.ResponseWriter, err error) {
	var cloudErr *cloudclient.CloudError
	switch {
	case errors.As(err, &cloudErr):
		status := cloudErr.HTTPStatus
		if status != http.StatusUnauthorized && status != http.StatusForbidden && status != http.StatusNotFound &&
			status != http.StatusConflict && status != http.StatusGone && status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
			status = http.StatusBadGateway
		}
		data := map[string]any{"reason_code": strconv.Itoa(cloudErr.Code)}
		if cloudErr.RetryAfterSeconds != nil && *cloudErr.RetryAfterSeconds > 0 && *cloudErr.RetryAfterSeconds <= 3600 {
			data["retry_after_seconds"] = *cloudErr.RetryAfterSeconds
			w.Header().Set("Retry-After", strconv.Itoa(*cloudErr.RetryAfterSeconds))
		}
		common.ReplyErrWithData(w, "credential restore request was rejected", data, status)
	case errors.Is(err, ErrLocalConflict):
		common.ReplyErrWithData(w, "credential restore conflicts with local credentials", map[string]string{"reason_code": "local_conflict"}, http.StatusConflict)
	case errors.Is(err, ErrLocalSecureStoreUnavailable):
		common.ReplyErrWithData(w, "local secure storage is unavailable", map[string]string{"reason_code": "local_secure_store_unavailable"}, http.StatusServiceUnavailable)
	case errors.Is(err, ErrRestoreOperationNotFound), errors.Is(err, ErrLocalKeyNotFound):
		common.ReplyErrWithData(w, "credential restore operation was not found", map[string]string{"reason_code": "restore_operation_not_found"}, http.StatusNotFound)
	case errors.Is(err, ErrInvalidContract):
		common.ReplyErr(w, "credential restore request is invalid", http.StatusBadRequest)
	default:
		common.ReplyErr(w, "credential restore is temporarily unavailable", http.StatusServiceUnavailable)
	}
}
