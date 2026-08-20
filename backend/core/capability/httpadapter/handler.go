package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"lazymind/core/capability"
	"lazymind/core/common"
)

const maxRequestBytes = 1 << 20

type Handler struct {
	service *capability.Service
}

func New(service *capability.Service) (*Handler, error) {
	if service == nil {
		return nil, capability.NewError(
			capability.Internal,
			"capability.http.new",
			"service is required",
			false,
			nil,
		)
	}
	return &Handler{service: service}, nil
}

func (h *Handler) ListCloudDocuments(w http.ResponseWriter, r *http.Request) {
	handle(w, r, h.service.ListCloudDocuments)
}

func (h *Handler) GetCloudDocument(w http.ResponseWriter, r *http.Request) {
	handle(w, r, h.service.GetCloudDocument)
}

func (h *Handler) SearchCloudDocuments(w http.ResponseWriter, r *http.Request) {
	handle(w, r, h.service.SearchCloudDocuments)
}

func handle[Input any, Output any](
	w http.ResponseWriter,
	r *http.Request,
	invoke func(
		context.Context,
		capability.InvocationContext,
		Input,
	) (Output, error),
) {
	var input Input
	decoder := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, maxRequestBytes),
	)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		common.ReplyErr(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	result, err := invoke(r.Context(), invocation(r), input)
	if err != nil {
		writeError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func invocation(r *http.Request) capability.InvocationContext {
	return capability.InvocationContext{Principal: capability.Principal{
		UserID:   strings.TrimSpace(r.Header.Get("X-User-Id")),
		TenantID: strings.TrimSpace(r.Header.Get("X-Tenant-Id")),
		Permissions: capability.NewPermissionSet(
			capability.RequiredPermission,
		),
	}}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	var capabilityErr *capability.Error
	if errors.As(err, &capabilityErr) {
		switch capabilityErr.Code {
		case capability.InvalidArgument:
			status = http.StatusBadRequest
		case capability.Unauthenticated:
			status = http.StatusUnauthorized
		case capability.PermissionDenied:
			status = http.StatusForbidden
		case capability.NotFound:
			status = http.StatusNotFound
		case capability.DeadlineExceeded:
			status = http.StatusGatewayTimeout
		case capability.Unavailable:
			status = http.StatusServiceUnavailable
		case capability.ResultTooLarge:
			status = http.StatusRequestEntityTooLarge
		}
	}
	common.ReplyErr(w, err.Error(), status)
}
