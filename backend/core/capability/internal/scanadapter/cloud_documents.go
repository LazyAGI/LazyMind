// Package scanadapter exposes LazyMind's authorized cloud accounts through the
// existing online connector tree. It never creates a Scan source or starts a
// scan/sync job; Scan only hosts the already-established provider adapters.
package scanadapter

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"lazymind/core/capability"
)

type CloudDocumentReader struct {
	scanBase      *url.URL
	authBase      *url.URL
	runtimeBase   *url.URL
	internalToken string
	timeout       time.Duration
}

func NewCloudDocumentReader(scanBaseURL, authBaseURL, internalToken string, timeout time.Duration) (*CloudDocumentReader, error) {
	scanBase, err := parseBaseURL(scanBaseURL)
	if err != nil {
		return nil, capability.NewError(capability.InvalidArgument, "cloud_document.adapter.new", "scan base_url is invalid", false, err)
	}
	authBase, err := parseBaseURL(authBaseURL)
	if err != nil {
		return nil, capability.NewError(capability.InvalidArgument, "cloud_document.adapter.new", "auth base_url is invalid", false, err)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &CloudDocumentReader{
		scanBase: scanBase, authBase: authBase, internalToken: strings.TrimSpace(internalToken),
		timeout: timeout,
	}, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("expected an HTTP service URL")
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
	if account.Provider == "googledrive" {
		return a.browseGoogleDrive(ctx, call, account, in)
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
	if err := a.request(ctx, call, "cloud_document.get", a.scanBase, "/api/scan/binding-targets/tree/children", body, false, &page); err != nil {
		return capability.GetCloudDocumentResult{}, withAccessGuidance(err, account)
	}
	result.Documents = make([]capability.CloudDocumentMetadata, 0, len(page.Items))
	for _, item := range page.Items {
		result.Documents = append(result.Documents, documentMetadata(account, item))
	}
	result.DocumentsPage = &capability.CursorPageInfo{ProviderCursor: nextCursor(page)}
	return result, nil
}

func (a *CloudDocumentReader) SearchCloudDocuments(ctx context.Context, call capability.InvocationContext, in capability.SearchCloudDocumentsInput) (capability.SearchCloudDocumentsResult, error) {
	account, err := a.account(ctx, call, "cloud_document.search", in.SourceID)
	if err != nil {
		return capability.SearchCloudDocumentsResult{}, err
	}
	if account.Provider == "googledrive" {
		return a.searchGoogleDrive(ctx, call, account, in)
	}
	if in.QueryMode != "" && in.QueryMode != "name" {
		return capability.SearchCloudDocumentsResult{}, capability.NewError(capability.Unsupported, "cloud_document.search", "this provider supports title search only", false, nil)
	}
	if account.Provider == "notion" && (in.NodeRef != "" || in.TargetRef != "" || in.TargetType != "") {
		return capability.SearchCloudDocumentsResult{}, capability.NewError(capability.Unsupported, "cloud_document.search", "Notion search matches titles across the connected workspace; browse a page to limit by parent", false, nil)
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
	if err := a.request(ctx, call, "cloud_document.search", a.scanBase, "/api/scan/binding-targets/tree/search", body, false, &page); err != nil {
		return capability.SearchCloudDocumentsResult{}, withAccessGuidance(err, account)
	}
	hits := make([]capability.CloudDocumentSearchHit, 0, len(page.Items))
	includeAll := !in.IncludeDocuments && !in.IncludeContainers
	for _, item := range page.Items {
		if !includeAll && item.IsDocument && !in.IncludeDocuments || !includeAll && item.IsContainer && !item.IsDocument && !in.IncludeContainers {
			continue
		}
		hits = append(hits, searchHit(account, item))
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
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\?#") || strings.TrimSpace(call.Principal.UserID) == "" {
		return cloudAccount{}, capability.NewError(capability.InvalidArgument, op, "invalid connection identity", false, nil)
	}
	var response struct {
		Data *cloudAccount `json:"data"`
	}
	path := "/v1/cloud/connections/internal/" + url.PathEscape(id) + "?user_id=" + url.QueryEscape(call.Principal.UserID)
	if err := a.request(ctx, call, op, a.authBase, path, nil, true, &response); err != nil {
		if code, _ := capability.CodeOf(err); code == capability.NotFound || code == capability.PermissionDenied {
			return cloudAccount{}, connectionError(capability.NotFound, op, "CONNECTION_UNAVAILABLE", "connection is unavailable to this user", "", "connect", "")
		}
		return cloudAccount{}, err
	}
	if response.Data == nil || response.Data.ConnectionID != id || !ownedAccount(*response.Data, call) {
		return cloudAccount{}, capability.NewError(capability.PermissionDenied, op, "connection identity mismatch", false, nil)
	}
	account := *response.Data
	if !supportedProvider(account.Provider) {
		return cloudAccount{}, capability.NewError(capability.Unsupported, op, "cloud provider is not supported", false, nil)
	}
	if account.Status == "REVOKED" || account.Status == "PENDING" {
		return cloudAccount{}, connectionError(capability.PermissionDenied, op, "AUTH_REQUIRED", "connect or reauthorize this cloud account", account.Provider, "reauthorize", account.ConnectionID)
	}
	if account.Status != "ACTIVE" && account.Status != "EXPIRED" && account.Status != "ERROR" {
		return cloudAccount{}, capability.NewError(capability.PermissionDenied, op, "invalid connection status", false, nil)
	}
	if !account.ProviderOptions.ChatEnabled {
		return cloudAccount{}, connectionError(capability.PermissionDenied, op, "CONNECTION_DISABLED", "enable this account for LazyMind before using it", account.Provider, "enable", account.ConnectionID)
	}
	return account, nil
}

func (a *CloudDocumentReader) accounts(ctx context.Context, call capability.InvocationContext, op string) ([]cloudAccount, error) {
	query := url.Values{}
	if strings.TrimSpace(call.Principal.UserID) == "" {
		return nil, capability.NewError(capability.Unauthenticated, op, "authenticated user is required", false, nil)
	}
	query.Set("owner_user_id", call.Principal.UserID)
	var envelope cloudAccountEnvelope
	path := "/v1/cloud/connections/internal/chat-enabled?" + query.Encode()
	if err := a.request(ctx, call, op, a.authBase, path, nil, true, &envelope); err != nil {
		return nil, err
	}
	if envelope.Data == nil || envelope.Data.Items == nil {
		return nil, capability.NewError(capability.Unavailable, op, "invalid connection list response", true, nil)
	}
	accounts := make([]cloudAccount, 0, len(envelope.Data.Items))
	for _, account := range envelope.Data.Items {
		if !ownedAccount(account, call) || account.ConnectionID == "" || account.Status != "ACTIVE" || !account.ProviderOptions.ChatEnabled {
			return nil, capability.NewError(capability.PermissionDenied, op, "invalid connection list identity or status", false, nil)
		}
		if supportedProvider(account.Provider) {
			accounts = append(accounts, account)
		}
	}
	return accounts, nil
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
	Data *struct {
		Items []cloudAccount `json:"items"`
	} `json:"data"`
}

type cloudAccount struct {
	OwnerUserID     string `json:"owner_user_id"`
	TenantID        string `json:"tenant_id"`
	ProviderOptions struct {
		ChatEnabled bool `json:"chat_enabled"`
	} `json:"provider_options"`
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

func documentMetadata(account cloudAccount, item treeNode) capability.CloudDocumentMetadata {
	fileType, _ := item.ProviderMeta["file_type"].(string)
	locator, sourceURL := documentLocation(account.Provider, item)
	return capability.CloudDocumentMetadata{
		ID: item.Key, SourceID: account.ConnectionID, NodeRef: item.NodeRef,
		Provider: account.Provider, ReadLocator: locator, SourceURL: sourceURL,
		TargetType: item.TargetType, TargetRef: item.TargetRef,
		ObjectKey: item.ObjectKey, ParentKey: item.ParentKey,
		DisplayName: item.DisplayName, FileType: fileType,
		IsDocument: item.IsDocument, IsContainer: item.IsContainer,
		HasChildren: item.HasChildren, Selectable: item.Selectable,
	}
}

func searchHit(account cloudAccount, item treeNode) capability.CloudDocumentSearchHit {
	metadata := documentMetadata(account, item)
	return capability.CloudDocumentSearchHit{
		Provider: metadata.Provider, ReadLocator: metadata.ReadLocator, SourceURL: metadata.SourceURL, FileType: metadata.FileType,
		Key: item.Key, DisplayName: item.DisplayName, SearchName: item.SearchName,
		SourceID: account.ConnectionID, NodeRef: item.NodeRef, TargetType: item.TargetType,
		TargetRef: item.TargetRef, ObjectKey: item.ObjectKey, ParentKey: item.ParentKey,
		IsDocument: item.IsDocument, IsContainer: item.IsContainer,
		HasChildren: item.HasChildren, Selectable: item.Selectable,
	}
}
