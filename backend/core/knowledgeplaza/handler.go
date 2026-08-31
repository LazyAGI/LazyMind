package knowledgeplaza

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"lazymind/core/cloudclient"
	"lazymind/core/common"
)

const defaultPageSize = 50
const maxPageSize = 100

type Source interface {
	List(context.Context, cloudclient.KnowledgeQuery) (cloudclient.KnowledgePage, error)
}

type Handler struct {
	Source Source
}

func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.Source == nil {
		common.ReplyErr(w, "LazyMind Cloud knowledge is unavailable", http.StatusServiceUnavailable)
		return
	}
	page, ok := parseBoundedPositiveInt(r.URL.Query().Get("page"), 1, 1<<30)
	if !ok {
		common.ReplyErr(w, "page must be a positive integer", http.StatusBadRequest)
		return
	}
	pageSize, ok := parseBoundedPositiveInt(r.URL.Query().Get("page_size"), defaultPageSize, maxPageSize)
	if !ok {
		common.ReplyErr(w, "page_size must be between 1 and 100", http.StatusBadRequest)
		return
	}

	result, err := h.Source.List(r.Context(), cloudclient.KnowledgeQuery{
		Keyword: strings.TrimSpace(r.URL.Query().Get("q")),
		Type:    strings.TrimSpace(r.URL.Query().Get("type")),
		Domain:  strings.TrimSpace(r.URL.Query().Get("domain")),
		Page:    page, PageSize: pageSize,
	})
	if err != nil {
		common.ReplyErr(w, "LazyMind Cloud knowledge request failed", responseStatus(err))
		return
	}
	if len(result.Items) > pageSize {
		common.ReplyErr(w, "LazyMind Cloud knowledge response is invalid", http.StatusBadGateway)
		return
	}
	for i := range result.Items {
		item := &result.Items[i]
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" || (item.Type != "industry" && item.Type != "evaluation") {
			common.ReplyErr(w, "LazyMind Cloud knowledge response is invalid", http.StatusBadGateway)
			return
		}
		if !strings.HasPrefix(item.ID, "cloud:") {
			item.ID = "cloud:" + item.ID
		}
		if item.Tags == nil {
			item.Tags = []string{}
		}
		if item.Questions == nil {
			item.Questions = []string{}
		}
	}
	common.ReplyOK(w, result)
}

func parseBoundedPositiveInt(raw string, fallback, maximum int) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value > 0 && value <= maximum
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
