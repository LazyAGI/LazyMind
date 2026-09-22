package cloudclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
)

type KnowledgeMarketItem struct {
	CatalogKey      string   `json:"catalog_key"`
	Version         int64    `json:"version"`
	Category        string   `json:"category"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Icon            string   `json:"icon"`
	Domain          string   `json:"domain"`
	Tags            []string `json:"tags"`
	OnlineAccessURL string   `json:"online_access_url"`
	DataSource      string   `json:"data_source"`
	PublishedAt     string   `json:"published_at"`
	UpdatedAt       string   `json:"updated_at"`
}

type KnowledgeMarketDetail struct {
	KnowledgeMarketItem
	PackageURL      string          `json:"package_url"`
	PackageRevision string          `json:"package_revision"`
	SourceAdapter   string          `json:"source_adapter"`
	AdapterOptions  json.RawMessage `json:"adapter_options"`
	SampleQuestions []string        `json:"sample_questions"`
}

type KnowledgeMarketPage struct {
	Items           []KnowledgeMarketItem `json:"items"`
	CatalogRevision int64                 `json:"catalog_revision"`
	NextCursor      string                `json:"next_cursor,omitempty"`
}

var knowledgeCatalogKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func ValidKnowledgeCatalogKey(key string) bool { return knowledgeCatalogKey.MatchString(key) }

func validKnowledgeMarketItem(item KnowledgeMarketItem) bool {
	return ValidKnowledgeCatalogKey(item.CatalogKey) && item.Version > 0 && item.Name != "" && (item.Category == "industry" || item.Category == "evaluation")
}

func (c *Client) ListKnowledgeMarket(ctx context.Context, token string, query url.Values) (KnowledgeMarketPage, error) {
	var page KnowledgeMarketPage
	_, err := c.readJSON(ctx, token, "/v1/knowledge-market/items", query, "", ResourceTreeLimit, &page)
	if err != nil {
		return page, err
	}
	limit, _ := strconv.Atoi(query.Get("page_size"))
	if limit == 0 {
		limit = 100
	}
	if page.Items == nil || len(page.Items) > limit || page.CatalogRevision < 0 || len(page.NextCursor) > 2048 {
		return KnowledgeMarketPage{}, errors.New("invalid Cloud knowledge page")
	}
	for _, item := range page.Items {
		if !validKnowledgeMarketItem(item) {
			return KnowledgeMarketPage{}, errors.New("invalid Cloud knowledge item")
		}
	}
	return page, nil
}

func (c *Client) GetKnowledgeMarketItem(ctx context.Context, token, key string) (KnowledgeMarketDetail, error) {
	if !ValidKnowledgeCatalogKey(key) {
		return KnowledgeMarketDetail{}, errors.New("invalid knowledge catalog key")
	}
	var detail KnowledgeMarketDetail
	_, err := c.readJSON(ctx, token, "/v1/knowledge-market/items/"+key, nil, "", ResourceTreeLimit, &detail)
	if err != nil {
		return detail, err
	}
	if detail.CatalogKey != key || !validKnowledgeMarketItem(detail.KnowledgeMarketItem) || len(detail.SampleQuestions) > 10 {
		return KnowledgeMarketDetail{}, errors.New("invalid Cloud knowledge detail")
	}
	return detail, nil
}
