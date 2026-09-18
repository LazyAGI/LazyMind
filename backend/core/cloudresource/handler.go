package cloudresource

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"lazymind/core/cloudclient"
	"lazymind/core/common"
)

type Handler struct {
	Service      *Service
	ResourceType string
	Adapter      LocalAdapter
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	owner := common.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	pageSize := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("page_size")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			common.ReplyErr(w, "page_size must be between 1 and 100", http.StatusBadRequest)
			return
		}
		pageSize = value
	}
	if h.Service == nil {
		common.ReplyErr(w, "LazyMind Cloud is unavailable", http.StatusServiceUnavailable)
		return
	}
	page, err := h.Service.List(r.Context(), ListRequest{
		OwnerUserID: owner, ResourceType: h.ResourceType, Cursor: r.URL.Query().Get("cursor"),
		PageSize: pageSize, Adapter: h.Adapter,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, page)
}

func (h Handler) Download(w http.ResponseWriter, r *http.Request) {
	owner := common.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	resourceID := strings.TrimSpace(common.PathVar(r, "resource_id"))
	if resourceID == "" {
		common.ReplyErr(w, "resource_id is required", http.StatusBadRequest)
		return
	}
	if h.Service == nil {
		common.ReplyErr(w, "LazyMind Cloud is unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := h.Service.Download(r.Context(), DownloadRequest{
		OwnerUserID: owner, ResourceType: h.ResourceType, ResourceID: resourceID, Adapter: h.Adapter,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func (h Handler) Upload(w http.ResponseWriter, r *http.Request) {
	owner := common.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	localResourceID := strings.TrimSpace(common.PathVar(r, "skill_id"))
	if localResourceID == "" {
		localResourceID = strings.TrimSpace(common.PathVar(r, "workflow_ref"))
	}
	if localResourceID == "" {
		common.ReplyErr(w, "local resource id is required", http.StatusBadRequest)
		return
	}
	uploadAdapter, ok := h.Adapter.(UploadAdapter)
	if h.Service == nil || !ok {
		common.ReplyErr(w, "LazyMind Cloud upload is unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := h.Service.Upload(r.Context(), UploadRequest{
		OwnerUserID: owner, ResourceType: h.ResourceType, LocalResourceID: localResourceID, Adapter: uploadAdapter,
	})
	if err != nil {
		replyError(w, err)
		return
	}
	common.ReplyOK(w, result)
}

func replyError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, ErrSessionRequired):
		common.ReplyErr(w, "LazyMind Cloud login is required", http.StatusUnauthorized)
		return
	case errors.Is(err, ErrPresenceConflict):
		status = http.StatusConflict
	default:
		var cloudErr *cloudclient.CloudError
		if errors.As(err, &cloudErr) {
			if cloudErr.HTTPStatus == http.StatusUnauthorized {
				common.ReplyErr(w, "LazyMind Cloud login is required", http.StatusUnauthorized)
				return
			}
			if cloudErr.RetryAfterSeconds != nil {
				w.Header().Set("Retry-After", strconv.Itoa(*cloudErr.RetryAfterSeconds))
			}
			switch cloudErr.HTTPStatus {
			case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
				http.StatusNotFound, http.StatusConflict, http.StatusTooManyRequests,
				http.StatusPreconditionFailed, http.StatusPreconditionRequired, http.StatusUnprocessableEntity, http.StatusServiceUnavailable:
				status = cloudErr.HTTPStatus
			}
		}
	}
	code := common.ErrorCodeFromHTTPStatus(status)
	if status == http.StatusPreconditionFailed {
		code = common.ErrCodeConflict
	}
	if status == http.StatusPreconditionRequired {
		code = common.ErrCodeInvalidParams
	}
	common.ReplyAppErr(w, common.NewAppError(status, code, "Cloud resource request failed"))
}
