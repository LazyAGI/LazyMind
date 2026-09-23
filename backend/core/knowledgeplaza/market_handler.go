package knowledgeplaza

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"lazymind/core/cloudclient"
	"lazymind/core/common"
)

type MarketClient interface {
	ListKnowledgeMarket(context.Context, string, url.Values) (cloudclient.KnowledgeMarketPage, error)
	GetKnowledgeMarketItem(context.Context, string, string) (cloudclient.KnowledgeMarketDetail, error)
}

// MarketHandler consumes the published dynamic catalog, independently of the
// older link-plaza contract and the YAML-managed local knowledge catalog.
type MarketHandler struct {
	Tokens AccessTokenSource
	Client MarketClient
}

func (h MarketHandler) access(w http.ResponseWriter, r *http.Request) (string, bool) {
	if common.UserID(r) == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	if h.Tokens == nil || h.Client == nil {
		common.ReplyErr(w, "LazyMind Cloud is unavailable", http.StatusServiceUnavailable)
		return "", false
	}
	token, err := h.Tokens.AccessToken(r.Context(), 30*time.Second)
	if err != nil {
		common.ReplyErr(w, "LazyMind Cloud login is required", http.StatusUnauthorized)
		return "", false
	}
	return token, true
}

func (h MarketHandler) List(w http.ResponseWriter, r *http.Request) {
	token, ok := h.access(w, r)
	if !ok {
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		common.ReplyErr(w, "invalid request", http.StatusBadRequest)
		return
	}
	limits := map[string]int{"cursor": 2048, "page_size": 3, "category": 10, "domain": 64, "q": 200}
	for key, values := range query {
		limit, allowed := limits[key]
		if !allowed || len(values) != 1 || !utf8.ValidString(values[0]) || utf8.RuneCountInString(values[0]) > limit {
			common.ReplyErr(w, "invalid request", http.StatusBadRequest)
			return
		}
	}
	if category := query.Get("category"); category != "" && category != "industry" && category != "evaluation" {
		common.ReplyErr(w, "invalid request", http.StatusBadRequest)
		return
	}
	if query.Get("page_size") == "" {
		query.Set("page_size", "100")
	}
	size, err := strconv.Atoi(query.Get("page_size"))
	if err != nil || size < 1 || size > 100 {
		common.ReplyErr(w, "invalid request", http.StatusBadRequest)
		return
	}
	page, err := h.Client.ListKnowledgeMarket(r.Context(), token, query)
	if err != nil {
		replyMarketError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.ReplyOK(w, page)
}

func (h MarketHandler) Get(w http.ResponseWriter, r *http.Request) {
	token, ok := h.access(w, r)
	if !ok {
		return
	}
	key := common.PathVar(r, "catalog_key")
	if !cloudclient.ValidKnowledgeCatalogKey(key) {
		common.ReplyErr(w, "invalid request", http.StatusBadRequest)
		return
	}
	detail, err := h.Client.GetKnowledgeMarketItem(r.Context(), token, key)
	if err != nil {
		replyMarketError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.ReplyOK(w, detail)
}

func replyMarketError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	var cloudErr *cloudclient.CloudError
	if errors.As(err, &cloudErr) {
		if cloudErr.HTTPStatus == http.StatusUnauthorized {
			common.ReplyErr(w, "LazyMind Cloud login is required", http.StatusUnauthorized)
			return
		}
		switch cloudErr.HTTPStatus {
		case 400, 403, 404, 412, 422, 429, 503:
			status = cloudErr.HTTPStatus
		}
		if cloudErr.RetryAfterSeconds != nil {
			w.Header().Set("Retry-After", strconv.Itoa(*cloudErr.RetryAfterSeconds))
		}
	}
	common.ReplyAppErr(w, common.NewAppError(status, common.ErrorCodeFromHTTPStatus(status), "Cloud knowledge request failed"))
}
