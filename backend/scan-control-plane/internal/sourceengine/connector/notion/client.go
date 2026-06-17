package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	apiBase       = "https://api.notion.com/v1"
	notionVersion = "2022-06-28"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type ObjectPage struct {
	Items      []Object
	NextCursor string
	HasMore    bool
	Watermark  string
}

type pageListResponse struct {
	Results    []map[string]any `json:"results"`
	NextCursor string           `json:"next_cursor"`
	HasMore    bool             `json:"has_more"`
}

func NewClient(baseURL string, client *http.Client) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = apiBase
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: baseURL, httpClient: client}
}

func (c *Client) GetPage(ctx context.Context, token, pageID string) (Object, error) {
	var out map[string]any
	if err := c.getJSON(ctx, token, "/pages/"+url.PathEscape(normalizeNotionID(pageID)), nil, &out); err != nil {
		return Object{}, err
	}
	return pageObject(out, ""), nil
}

func (c *Client) GetDatabase(ctx context.Context, token, databaseID string) (Object, error) {
	var out map[string]any
	if err := c.getJSON(ctx, token, "/databases/"+url.PathEscape(normalizeNotionID(databaseID)), nil, &out); err != nil {
		return Object{}, err
	}
	return databaseObject(out, ""), nil
}

func (c *Client) ListBlockChildren(ctx context.Context, token, blockID, cursor string, size int) (ObjectPage, error) {
	var out pageListResponse
	params := map[string]string{"page_size": strconv.Itoa(pageSize(size, defaultPageSize))}
	if strings.TrimSpace(cursor) != "" {
		params["start_cursor"] = strings.TrimSpace(cursor)
	}
	if err := c.getJSON(ctx, token, "/blocks/"+url.PathEscape(normalizeNotionID(blockID))+"/children", params, &out); err != nil {
		return ObjectPage{}, err
	}
	items := make([]Object, 0, len(out.Results))
	for _, raw := range out.Results {
		switch strings.TrimSpace(valueAsString(raw["type"])) {
		case "child_page":
			content, _ := raw["child_page"].(map[string]any)
			id := normalizeNotionID(valueAsString(raw["id"]))
			items = append(items, Object{
				Kind:            ObjectKindPage,
				ID:              id,
				ParentKind:      ObjectKindPage,
				ParentID:        normalizeNotionID(blockID),
				Name:            firstNonEmpty(valueAsString(content["title"]), id),
				HasChildren:     boolOption(raw, "has_children"),
				LastEditedTime:  valueAsString(raw["last_edited_time"]),
				ModifiedUnixSec: unixFromNotionTime(valueAsString(raw["last_edited_time"])),
			})
		case "child_database":
			content, _ := raw["child_database"].(map[string]any)
			id := normalizeNotionID(valueAsString(raw["id"]))
			items = append(items, Object{
				Kind:            ObjectKindDatabase,
				ID:              id,
				ParentKind:      ObjectKindPage,
				ParentID:        normalizeNotionID(blockID),
				Name:            firstNonEmpty(valueAsString(content["title"]), id),
				HasChildren:     true,
				LastEditedTime:  valueAsString(raw["last_edited_time"]),
				ModifiedUnixSec: unixFromNotionTime(valueAsString(raw["last_edited_time"])),
			})
		}
	}
	return ObjectPage{Items: items, HasMore: out.HasMore, NextCursor: out.NextCursor}, nil
}

func (c *Client) QueryDatabase(ctx context.Context, token, databaseID, cursor string, size int) (ObjectPage, error) {
	payload := map[string]any{"page_size": pageSize(size, defaultPageSize)}
	if strings.TrimSpace(cursor) != "" {
		payload["start_cursor"] = strings.TrimSpace(cursor)
	}
	var out pageListResponse
	if err := c.postJSON(ctx, token, "/databases/"+url.PathEscape(normalizeNotionID(databaseID))+"/query", payload, &out); err != nil {
		return ObjectPage{}, err
	}
	items := make([]Object, 0, len(out.Results))
	for _, raw := range out.Results {
		item := pageObject(raw, normalizeNotionID(databaseID))
		item.ParentKind = ObjectKindDatabase
		items = append(items, item)
	}
	return ObjectPage{Items: items, HasMore: out.HasMore, NextCursor: out.NextCursor}, nil
}

func (c *Client) Search(ctx context.Context, token, keyword, cursor string, size int) (ObjectPage, error) {
	payload := map[string]any{"query": strings.TrimSpace(keyword), "page_size": pageSize(size, defaultPageSize)}
	if strings.TrimSpace(cursor) != "" {
		payload["start_cursor"] = strings.TrimSpace(cursor)
	}
	var out pageListResponse
	if err := c.postJSON(ctx, token, "/search", payload, &out); err != nil {
		return ObjectPage{}, err
	}
	items := make([]Object, 0, len(out.Results))
	for _, raw := range out.Results {
		switch strings.TrimSpace(valueAsString(raw["object"])) {
		case "database":
			items = append(items, databaseObject(raw, ""))
		case "page":
			items = append(items, pageObject(raw, ""))
		}
	}
	return ObjectPage{Items: items, HasMore: out.HasMore, NextCursor: out.NextCursor}, nil
}

func (c *Client) PageToMarkdown(ctx context.Context, token, pageID string) (string, error) {
	return c.pageToMarkdown(ctx, token, pageID, 1, map[string]struct{}{})
}

func (c *Client) DatabaseToMarkdown(ctx context.Context, token, databaseID string) (string, error) {
	return c.databaseToMarkdown(ctx, token, databaseID, 1, map[string]struct{}{})
}

func (c *Client) pageToMarkdown(ctx context.Context, token, pageID string, headingLevel int, visited map[string]struct{}) (string, error) {
	pageID = normalizeNotionID(pageID)
	if _, ok := visited["page-md:"+pageID]; ok {
		return "", nil
	}
	visited["page-md:"+pageID] = struct{}{}
	var pageObj map[string]any
	if err := c.getJSON(ctx, token, "/pages/"+url.PathEscape(pageID), nil, &pageObj); err != nil {
		return "", err
	}
	title := firstNonEmpty(pageTitle(pageObj), pageID)
	children, err := c.paginateBlockChildren(ctx, token, pageID)
	if err != nil {
		return "", err
	}
	lines := []string{fmt.Sprintf("%s %s", markdownHeading(headingLevel), title)}
	lines = append(lines, c.blocksToMarkdown(ctx, token, children, headingLevel+1, visited)...)
	return joinMarkdown(lines), nil
}

func (c *Client) databaseToMarkdown(ctx context.Context, token, databaseID string, headingLevel int, visited map[string]struct{}) (string, error) {
	databaseID = normalizeNotionID(databaseID)
	var dbObj map[string]any
	if err := c.getJSON(ctx, token, "/databases/"+url.PathEscape(databaseID), nil, &dbObj); err != nil {
		return "", err
	}
	title := firstNonEmpty(databaseTitle(dbObj), databaseID)
	lines := []string{fmt.Sprintf("%s %s", markdownHeading(headingLevel), title)}
	cursor := ""
	for {
		page, err := c.QueryDatabase(ctx, token, databaseID, cursor, defaultPageSize)
		if err != nil {
			return "", err
		}
		for _, item := range page.Items {
			body, err := c.pageToMarkdown(ctx, token, item.ID, headingLevel+1, visited)
			if err == nil && strings.TrimSpace(body) != "" {
				lines = append(lines, body)
			}
		}
		if !page.HasMore || strings.TrimSpace(page.NextCursor) == "" {
			break
		}
		cursor = page.NextCursor
	}
	return joinMarkdown(lines), nil
}

func (c *Client) blocksToMarkdown(ctx context.Context, token string, blocks []map[string]any, headingLevel int, visited map[string]struct{}) []string {
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		lines = append(lines, c.blockToMarkdown(ctx, token, block, headingLevel, visited)...)
	}
	return lines
}

func (c *Client) blockToMarkdown(ctx context.Context, token string, block map[string]any, headingLevel int, visited map[string]struct{}) []string {
	blockType := strings.TrimSpace(valueAsString(block["type"]))
	content, _ := block[blockType].(map[string]any)
	text := richText(content["rich_text"])
	var lines []string
	switch blockType {
	case "paragraph":
		if text != "" {
			lines = append(lines, text)
		}
	case "heading_1", "heading_2", "heading_3":
		level := map[string]int{"heading_1": 1, "heading_2": 2, "heading_3": 3}[blockType]
		lines = append(lines, fmt.Sprintf("%s %s", markdownHeading(level), text))
	case "bulleted_list_item", "toggle":
		lines = append(lines, "- "+text)
	case "numbered_list_item":
		lines = append(lines, "1. "+text)
	case "to_do":
		mark := " "
		if boolOption(content, "checked") {
			mark = "x"
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s", mark, text))
	case "quote", "callout":
		lines = append(lines, "> "+text)
	case "code":
		lines = append(lines, "```"+strings.TrimSpace(valueAsString(content["language"])), text, "```")
	case "divider":
		lines = append(lines, "---")
	case "child_page", "child_database":
		if title := strings.TrimSpace(valueAsString(content["title"])); title != "" {
			lines = append(lines, fmt.Sprintf("%s %s", markdownHeading(headingLevel), title))
		}
	case "image", "file", "pdf", "video", "audio":
		if fileURL := fileBlockURL(content); fileURL != "" {
			label := firstNonEmpty(richText(content["caption"]), blockType)
			lines = append(lines, fmt.Sprintf("[%s](%s)", label, fileURL))
		}
	default:
		if text != "" {
			lines = append(lines, text)
		}
	}
	if boolOption(block, "has_children") {
		blockID := normalizeNotionID(valueAsString(block["id"]))
		if blockID != "" {
			children, err := c.paginateBlockChildren(ctx, token, blockID)
			if err == nil {
				lines = append(lines, c.blocksToMarkdown(ctx, token, children, headingLevel+1, visited)...)
			}
		}
	}
	return lines
}

func (c *Client) paginateBlockChildren(ctx context.Context, token, blockID string) ([]map[string]any, error) {
	var all []map[string]any
	cursor := ""
	for {
		params := map[string]string{"page_size": strconv.Itoa(defaultPageSize)}
		if cursor != "" {
			params["start_cursor"] = cursor
		}
		var out pageListResponse
		if err := c.getJSON(ctx, token, "/blocks/"+url.PathEscape(normalizeNotionID(blockID))+"/children", params, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Results...)
		if !out.HasMore || strings.TrimSpace(out.NextCursor) == "" {
			return all, nil
		}
		cursor = strings.TrimSpace(out.NextCursor)
	}
}

func (c *Client) getJSON(ctx context.Context, token, apiPath string, params map[string]string, out any) error {
	u, err := url.Parse(c.baseURL + apiPath)
	if err != nil {
		return err
	}
	q := u.Query()
	for key, value := range params {
		if strings.TrimSpace(value) != "" {
			q.Set(key, strings.TrimSpace(value))
		}
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, token, out)
}

func (c *Client) postJSON(ctx context.Context, token, apiPath string, payload map[string]any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+apiPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	return c.doJSON(req, token, out)
}

func (c *Client) doJSON(req *http.Request, token string, out any) error {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", notionVersion)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("notion api %s returned %d: %s", req.URL.Path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode notion api %s response failed: %w", req.URL.Path, err)
	}
	return nil
}

func pageObject(raw map[string]any, parentID string) Object {
	id := normalizeNotionID(valueAsString(raw["id"]))
	if parentID == "" {
		parentID = parentIDFromRaw(raw)
	}
	lastEdited := valueAsString(raw["last_edited_time"])
	return Object{
		Kind:            ObjectKindPage,
		ID:              id,
		ParentID:        parentID,
		Name:            firstNonEmpty(pageTitle(raw), id),
		URL:             firstNonEmpty(valueAsString(raw["url"]), valueAsString(raw["public_url"])),
		HasChildren:     boolOption(raw, "has_children"),
		LastEditedTime:  lastEdited,
		ModifiedUnixSec: unixFromNotionTime(lastEdited),
	}
}

func databaseObject(raw map[string]any, parentID string) Object {
	id := normalizeNotionID(valueAsString(raw["id"]))
	if parentID == "" {
		parentID = parentIDFromRaw(raw)
	}
	lastEdited := valueAsString(raw["last_edited_time"])
	return Object{
		Kind:            ObjectKindDatabase,
		ID:              id,
		ParentID:        parentID,
		Name:            firstNonEmpty(databaseTitle(raw), id),
		URL:             valueAsString(raw["url"]),
		HasChildren:     true,
		LastEditedTime:  lastEdited,
		ModifiedUnixSec: unixFromNotionTime(lastEdited),
	}
}

func parentIDFromRaw(raw map[string]any) string {
	parent, _ := raw["parent"].(map[string]any)
	for _, key := range []string{"page_id", "database_id", "block_id", "workspace"} {
		if value := normalizeNotionID(valueAsString(parent[key])); value != "" {
			return value
		}
	}
	return ""
}

func unixFromNotionTime(raw string) int64 {
	if t := parseNotionTime(raw); t != nil {
		return t.Unix()
	}
	return 0
}
