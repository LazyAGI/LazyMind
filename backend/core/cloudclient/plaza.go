package cloudclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const knowledgePlazaPath = "/v1/plaza/items"

type KnowledgeQuery struct {
	Keyword  string
	Type     string
	Domain   string
	Page     int
	PageSize int
}

type KnowledgeItem struct {
	ID              string   `json:"id"`
	Type            string   `json:"type"`
	Domain          string   `json:"domain"`
	Icon            string   `json:"icon"`
	Name            string   `json:"name"`
	Description     string   `json:"desc"`
	Tags            []string `json:"tags"`
	DocumentCount   int      `json:"docs"`
	DisplaySize     string   `json:"size"`
	Version         string   `json:"version"`
	Updated         string   `json:"updated"`
	Installed       bool     `json:"installed"`
	UpdateAvailable bool     `json:"update_available,omitempty"`
	Coverage        string   `json:"coverage"`
	Source          string   `json:"source"`
	Questions       []string `json:"questions"`
}

type KnowledgePage struct {
	Items      []KnowledgeItem `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func (c *Client) ListKnowledge(ctx context.Context, accessToken string, query KnowledgeQuery) (KnowledgePage, error) {
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return KnowledgePage{}, errors.New("LazyMind Cloud access token is required")
	}
	endpoint, err := url.Parse(c.resolve(knowledgePlazaPath))
	if err != nil {
		return KnowledgePage{}, err
	}
	values := endpoint.Query()
	if keyword := strings.TrimSpace(query.Keyword); keyword != "" {
		values.Set("q", keyword)
	}
	if itemType := strings.TrimSpace(query.Type); itemType != "" {
		values.Set("type", itemType)
	}
	if domain := strings.TrimSpace(query.Domain); domain != "" {
		values.Set("domain", domain)
	}
	if query.Page > 0 {
		values.Set("page", strconv.Itoa(query.Page))
	}
	if query.PageSize > 0 {
		values.Set("page_size", strconv.Itoa(query.PageSize))
	}
	endpoint.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return KnowledgePage{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return KnowledgePage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var cloudErr CloudError
		_ = json.NewDecoder(resp.Body).Decode(&cloudErr)
		cloudErr.HTTPStatus = resp.StatusCode
		return KnowledgePage{}, &cloudErr
	}
	var page KnowledgePage
	decoder := json.NewDecoder(resp.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&page); err != nil {
		return KnowledgePage{}, fmt.Errorf("decode LazyMind Cloud knowledge response: %w", err)
	}
	if page.Items == nil {
		page.Items = []KnowledgeItem{}
	}
	return page, nil
}
