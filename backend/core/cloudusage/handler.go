package cloudusage

import (
	"context"
	"errors"
	"net/http"

	"lazymind/core/cloudclient"
	"lazymind/core/common"
)

type Source interface {
	Get(context.Context) (cloudclient.AccountTokenPlan, error)
}

type Handler struct {
	Source Source
}

type response struct {
	Status      string                             `json:"status"`
	ModelQuotas []cloudclient.TokenPlanModelQuota  `json:"model_quotas,omitempty"`
	Usage       []cloudclient.TokenPlanPeriodUsage `json:"usage,omitempty"`
}

func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	if h.Source == nil {
		common.ReplyErr(w, "LazyMind Cloud usage is unavailable", http.StatusServiceUnavailable)
		return
	}
	plan, err := h.Source.Get(r.Context())
	if err != nil {
		if errors.Is(err, ErrCloudSessionUnavailable) {
			common.ReplyErr(w, "LazyMind Cloud login is required", http.StatusUnauthorized)
			return
		}
		common.ReplyErr(w, "LazyMind Cloud usage request failed", responseStatus(err))
		return
	}
	common.ReplyOK(w, response{
		Status: plan.Status, ModelQuotas: plan.ModelQuotas, Usage: plan.Usage,
	})
}

func responseStatus(err error) int {
	if errors.Is(err, ErrCloudSessionUnavailable) {
		return http.StatusUnauthorized
	}
	var cloudErr *cloudclient.CloudError
	if !errors.As(err, &cloudErr) {
		return http.StatusBadGateway
	}
	switch cloudErr.HTTPStatus {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return cloudErr.HTTPStatus
	default:
		return http.StatusBadGateway
	}
}
