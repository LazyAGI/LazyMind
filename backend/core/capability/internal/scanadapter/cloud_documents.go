// Package scanadapter exposes LazyMind's authorized cloud accounts through the
// existing online connector tree. It never creates a Scan source or starts a
// scan/sync job; Scan only hosts the already-established provider adapters.
package scanadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lazymind/core/capability"
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type CloudDocumentReader struct {
	scanBase      *url.URL
	authBase      *url.URL
	internalToken string
	client        HTTPClient
	timeout       time.Duration
}

func NewCloudDocumentReader(scanBaseURL, authBaseURL, internalToken string, client HTTPClient, timeout time.Duration) (*CloudDocumentReader, error) {
	scanBase, err := parseBaseURL(scanBaseURL)
	if err != nil {
		return nil, capability.NewError(capability.InvalidArgument, "cloud_document.adapter.new", "scan base_url is invalid", false, err)
	}
	authBase, err := parseBaseURL(authBaseURL)
	if err != nil {
		return nil, capability.NewError(capability.InvalidArgument, "cloud_document.adapter.new", "auth base_url is invalid", false, err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &CloudDocumentReader{
		scanBase: scanBase, authBase: authBase, internalToken: strings.TrimSpace(internalToken),
		client: client, timeout: timeout,
	}, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, err
	}
	return u, nil
}

func (a *CloudDocumentReader) ListCloudDocuments(ctx context.Context, call capability.InvocationContext, q capability.CloudDocumentListQuery) (capability.CloudDocumentListPage, error) {
	accounts, err := a.accounts(ctx, call, "cloud_document.list")
	if err != nil {
		return capability.CloudDocumentListPage{}, err
	}
	keyword := strings.ToLower(strings.TrimSpace(q.Keyword))
	status := strings.ToUpper(strings.TrimSpace(q.Status))
	items := make([]capability.CloudDocumentSource, 0, len(accounts))
	for _, account := range accounts {
		if keyword != "" && !strings.Contains(strings.ToLower(account.DisplayName+" "+account.Provider), keyword) {
			continue
		}
		if status != "" && strings.ToUpper(account.Status) != status {
			continue
		}
		items = append(items, accountSource(account))
	}
	total := int64(len(items))
	start := min(q.Offset, len(items))
	end := min(start+q.Limit, len(items))
	return capability.CloudDocumentListPage{Items: items[start:end], Total: total}, nil
}

func (a *CloudDocumentReader) GetCloudDocument(ctx context.Context, call capability.InvocationContext, in capability.GetCloudDocumentInput) (capability.GetCloudDocumentResult, error) {
	account, err := a.account(ctx, call, "cloud_document.get", in.SourceID)
	if err != nil {
		return capability.GetCloudDocumentResult{}, err
	}
	result := capability.GetCloudDocumentResult{Source: accountSource(account)}
	if !in.IncludeDocuments {
		return result, nil
	}
	body := map[string]any{
		"connector_type":     account.Provider,
		"auth_connection_id": account.ConnectionID,
		"provider_options":   map[string]any{"user_id": call.Principal.UserID},
		"include_files":      true,
		"list_mode":          "page",
		"page_size":          in.DocumentsPage.PageSize,
		"cursor":             in.ProviderCursor,
		"target_type":        in.TargetType,
		"target_ref":         in.TargetRef,
		"node_ref":           in.NodeRef,
	}
	var page treePage
	if err := a.request(ctx, call, "cloud_document.get", a.scanBase, "/api/scan/binding-targets/tree/children", http.MethodPost, body, false, &page); err != nil {
		return capability.GetCloudDocumentResult{}, err
	}
	result.Documents = make([]capability.CloudDocumentMetadata, 0, len(page.Items))
	for _, item := range page.Items {
		result.Documents = append(result.Documents, documentMetadata(account.ConnectionID, item))
	}
	result.DocumentsPage = &capability.CursorPageInfo{ProviderCursor: nextCursor(page)}
	return result, nil
}

func (a *CloudDocumentReader) SearchCloudDocuments(ctx context.Context, call capability.InvocationContext, in capability.SearchCloudDocumentsInput) (capability.SearchCloudDocumentsResult, error) {
	account, err := a.account(ctx, call, "cloud_document.search", in.SourceID)
	if err != nil {
		return capability.SearchCloudDocumentsResult{}, err
	}
	body := map[string]any{
		"connector_type":     account.Provider,
		"auth_connection_id": account.ConnectionID,
		"provider_options":   map[string]any{"user_id": call.Principal.UserID},
		"keyword":            in.Query,
		"include_files":      true,
		"direct":             true,
		"list_mode":          "page",
		"page_size":          in.Page.PageSize,
		"cursor":             in.ProviderCursor,
		"target_type":        in.TargetType,
		"target_ref":         in.TargetRef,
		"node_ref":           in.NodeRef,
	}
	var page treePage
	if err := a.request(ctx, call, "cloud_document.search", a.scanBase, "/api/scan/binding-targets/tree/search", http.MethodPost, body, false, &page); err != nil {
		return capability.SearchCloudDocumentsResult{}, err
	}
	hits := make([]capability.CloudDocumentSearchHit, 0, len(page.Items))
	includeAll := !in.IncludeDocuments && !in.IncludeContainers
	for _, item := range page.Items {
		if !includeAll && item.IsDocument && !in.IncludeDocuments || !includeAll && item.IsContainer && !item.IsDocument && !in.IncludeContainers {
			continue
		}
		hits = append(hits, searchHit(account.ConnectionID, item))
	}
	return capability.SearchCloudDocumentsResult{
		Hits: hits,
		Page: capability.CursorPageInfo{ProviderCursor: nextCursor(page)},
	}, nil
}

func nextCursor(page treePage) string {
	if !page.HasMore {
		return ""
	}
	return strings.TrimSpace(page.NextCursor)
}

func (a *CloudDocumentReader) account(ctx context.Context, call capability.InvocationContext, op, id string) (cloudAccount, error) {
	accounts, err := a.accounts(ctx, call, op)
	if err != nil {
		return cloudAccount{}, err
	}
	for _, account := range accounts {
		if account.ConnectionID == id {
			return account, nil
		}
	}
	return cloudAccount{}, capability.NewError(capability.NotFound, op, "authorized cloud account not found", false, nil)
}

func (a *CloudDocumentReader) accounts(ctx context.Context, call capability.InvocationContext, op string) ([]cloudAccount, error) {
	query := url.Values{}
	query.Set("provider", "feishu")
	query.Set("owner_user_id", call.Principal.UserID)
	var envelope cloudAccountEnvelope
	path := "/v1/cloud/connections/internal/chat-enabled?" + query.Encode()
	if err := a.request(ctx, call, op, a.authBase, path, http.MethodGet, nil, true, &envelope); err != nil {
		return nil, err
	}
	return envelope.Data.Items, nil
}

func (a *CloudDocumentReader) request(ctx context.Context, call capability.InvocationContext, op string, base *url.URL, path, method string, body any, internal bool, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return capability.NewError(capability.Internal, op, "request encoding failed", false, err)
		}
		reader = bytes.NewReader(raw)
	}
	c, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	u := endpoint(base, path)
	req, err := http.NewRequestWithContext(c, method, u.String(), reader)
	if err != nil {
		return capability.NewError(capability.Internal, op, "request creation failed", false, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-User-ID", call.Principal.UserID)
	if call.Principal.TenantID != "" {
		req.Header.Set("X-Tenant-ID", call.Principal.TenantID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if internal && a.internalToken != "" {
		req.Header.Set("X-LazyMind-Internal-Token", a.internalToken)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return capability.NewError(capability.Unavailable, op, "cloud document backend unavailable", true, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := capability.Internal
		switch {
		case resp.StatusCode == http.StatusBadRequest:
			code = capability.InvalidArgument
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			code = capability.PermissionDenied
		case resp.StatusCode == http.StatusNotFound:
			code = capability.NotFound
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			code = capability.Unavailable
		}
		return capability.NewError(code, op, "cloud document request failed", code == capability.Unavailable, nil)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return capability.NewError(capability.Unavailable, op, "cloud document response is invalid", true, err)
	}
	return nil
}

func endpoint(base *url.URL, path string) *url.URL {
	u := *base
	parsed, err := url.Parse(path)
	if err == nil && parsed.Path != "" {
		u.Path = strings.TrimRight(u.Path, "/") + parsed.Path
		u.RawQuery = parsed.RawQuery
	} else {
		u.Path = strings.TrimRight(u.Path, "/") + path
		u.RawQuery = ""
	}
	return &u
}

type cloudAccountEnvelope struct {
	Data struct {
		Items []cloudAccount `json:"items"`
	} `json:"data"`
}

type cloudAccount struct {
	ConnectionID string     `json:"connection_id"`
	Provider     string     `json:"provider"`
	DisplayName  string     `json:"display_name"`
	Status       string     `json:"status"`
	CreatedAt    *time.Time `json:"created_at"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

type treeNode struct {
	Key          string         `json:"key"`
	NodeRef      string         `json:"node_ref"`
	DisplayName  string         `json:"display_name"`
	SearchName   string         `json:"search_name"`
	TargetType   string         `json:"target_type"`
	TargetRef    string         `json:"target_ref"`
	ObjectKey    string         `json:"object_key"`
	ParentKey    string         `json:"parent_key"`
	IsDocument   bool           `json:"is_document"`
	IsContainer  bool           `json:"is_container"`
	HasChildren  bool           `json:"has_children"`
	Selectable   bool           `json:"selectable"`
	ProviderMeta map[string]any `json:"provider_meta"`
}

type treePage struct {
	Items      []treeNode `json:"items"`
	NextCursor string     `json:"next_cursor"`
	HasMore    bool       `json:"has_more"`
}

func accountSource(account cloudAccount) capability.CloudDocumentSource {
	return capability.CloudDocumentSource{
		ID: account.ConnectionID, Name: account.DisplayName, Provider: account.Provider,
		Status: account.Status, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt,
	}
}

func documentMetadata(sourceID string, item treeNode) capability.CloudDocumentMetadata {
	fileType, _ := item.ProviderMeta["file_type"].(string)
	return capability.CloudDocumentMetadata{
		ID: item.Key, SourceID: sourceID, NodeRef: item.NodeRef,
		TargetType: item.TargetType, TargetRef: item.TargetRef,
		ObjectKey: item.ObjectKey, ParentKey: item.ParentKey,
		DisplayName: item.DisplayName, FileType: fileType,
		IsDocument: item.IsDocument, IsContainer: item.IsContainer,
		HasChildren: item.HasChildren, Selectable: item.Selectable,
	}
}

func searchHit(sourceID string, item treeNode) capability.CloudDocumentSearchHit {
	return capability.CloudDocumentSearchHit{
		Key: item.Key, DisplayName: item.DisplayName, SearchName: item.SearchName,
		SourceID: sourceID, NodeRef: item.NodeRef, TargetType: item.TargetType,
		TargetRef: item.TargetRef, ObjectKey: item.ObjectKey, ParentKey: item.ParentKey,
		IsDocument: item.IsDocument, IsContainer: item.IsContainer,
		HasChildren: item.HasChildren, Selectable: item.Selectable,
	}
}
