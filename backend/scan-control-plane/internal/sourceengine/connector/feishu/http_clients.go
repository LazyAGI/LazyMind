package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

type HTTPAuthConnectionClient struct {
	baseURL       *url.URL
	internalToken string
	httpClient    *http.Client
}

func NewHTTPAuthConnectionClient(baseURL, internalToken string, client *http.Client) (*HTTPAuthConnectionClient, error) {
	parsed, httpClient, err := newHTTPBoundary(baseURL, client)
	if err != nil {
		return nil, err
	}
	return &HTTPAuthConnectionClient{
		baseURL:       parsed,
		internalToken: strings.TrimSpace(internalToken),
		httpClient:    httpClient,
	}, nil
}

func (c *HTTPAuthConnectionClient) GetToken(ctx context.Context, req TokenRequest) (Token, error) {
	connectionID := strings.TrimSpace(req.AuthConnectionID)
	if connectionID == "" {
		return Token{}, connector.NewError(ErrorCodeAuthInvalid, "auth_connection_id is required")
	}
	var out Token
	path := "/api/authservice/v1/cloud/connections/" + url.PathEscape(connectionID) + "/token"
	if err := c.doAuthServiceToken(ctx, endpoint(c.baseURL, path, authQuery(req.UserID, "")), &out); err != nil {
		return Token{}, err
	}
	return out, nil
}

func (c *HTTPAuthConnectionClient) Verify(ctx context.Context, authConnectionID, userID, tenantID string) error {
	connectionID := strings.TrimSpace(authConnectionID)
	if connectionID == "" {
		return connector.NewError(ErrorCodeAuthInvalid, "auth_connection_id is required")
	}
	path := "/api/authservice/v1/cloud/connections/" + url.PathEscape(connectionID) + "/verify"
	return c.doAuthServiceRequest(ctx, endpoint(c.baseURL, path, authQuery(userID, tenantID)), http.MethodGet, nil, nil)
}

func (c *HTTPAuthConnectionClient) BatchStatus(ctx context.Context, req ConnectionStatusRequest) (map[string]ConnectionStatus, error) {
	connectionIDs := uniqueNonEmptyStrings(req.ConnectionIDs)
	if len(connectionIDs) == 0 {
		return map[string]ConnectionStatus{}, nil
	}
	body, err := json.Marshal(map[string]any{"connection_ids": connectionIDs})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Items []struct {
			ConnectionID      string `json:"connection_id"`
			TenantID          string `json:"tenant_id"`
			OwnerUserID       string `json:"owner_user_id"`
			Provider          string `json:"provider"`
			AuthMode          string `json:"auth_mode"`
			ProviderAccountID string `json:"provider_account_id"`
			DisplayName       string `json:"display_name"`
			ProviderTenantKey string `json:"provider_tenant_key"`
			Status            string `json:"status"`
			LastError         string `json:"last_error"`
			LastUsedAt        string `json:"last_used_at"`
			UpdatedAt         string `json:"updated_at"`
		} `json:"items"`
	}
	path := "/api/authservice/v1/cloud/connections/status:batch"
	if err := c.doAuthServiceRequest(ctx, endpoint(c.baseURL, path, authQuery(req.UserID, req.TenantID)), http.MethodPost, bytes.NewReader(body), &payload); err != nil {
		return nil, err
	}
	statuses := make(map[string]ConnectionStatus, len(payload.Items))
	for _, item := range payload.Items {
		connectionID := strings.TrimSpace(item.ConnectionID)
		if connectionID == "" {
			continue
		}
		statuses[connectionID] = ConnectionStatus{
			ConnectionID:      connectionID,
			TenantID:          item.TenantID,
			OwnerUserID:       item.OwnerUserID,
			Provider:          item.Provider,
			AuthMode:          item.AuthMode,
			ProviderAccountID: item.ProviderAccountID,
			DisplayName:       item.DisplayName,
			ProviderTenantKey: item.ProviderTenantKey,
			Status:            strings.ToUpper(strings.TrimSpace(item.Status)),
			LastError:         item.LastError,
			LastUsedAt:        item.LastUsedAt,
			UpdatedAt:         item.UpdatedAt,
		}
	}
	return statuses, nil
}

func (c *HTTPAuthConnectionClient) ListTargetCacheConnections(ctx context.Context, req ConnectionListRequest) ([]ConnectionStatus, error) {
	query := url.Values{}
	if provider := strings.TrimSpace(req.Provider); provider != "" {
		query.Set("provider", provider)
	}
	if req.Limit > 0 {
		query.Set("limit", strconv.Itoa(req.Limit))
	}
	var payload struct {
		Items []struct {
			ConnectionID      string `json:"connection_id"`
			TenantID          string `json:"tenant_id"`
			OwnerUserID       string `json:"owner_user_id"`
			Provider          string `json:"provider"`
			AuthMode          string `json:"auth_mode"`
			ProviderAccountID string `json:"provider_account_id"`
			DisplayName       string `json:"display_name"`
			ProviderTenantKey string `json:"provider_tenant_key"`
			Status            string `json:"status"`
			LastError         string `json:"last_error"`
			LastUsedAt        string `json:"last_used_at"`
			UpdatedAt         string `json:"updated_at"`
		} `json:"items"`
	}
	path := "/api/authservice/v1/cloud/connections/internal/target-cache-candidates"
	if err := c.doAuthServiceRequest(ctx, endpoint(c.baseURL, path, query), http.MethodGet, nil, &payload); err != nil {
		return nil, err
	}
	items := make([]ConnectionStatus, 0, len(payload.Items))
	for _, item := range payload.Items {
		connectionID := strings.TrimSpace(item.ConnectionID)
		if connectionID == "" {
			continue
		}
		items = append(items, ConnectionStatus{
			ConnectionID:      connectionID,
			TenantID:          item.TenantID,
			OwnerUserID:       item.OwnerUserID,
			Provider:          item.Provider,
			AuthMode:          item.AuthMode,
			ProviderAccountID: item.ProviderAccountID,
			DisplayName:       item.DisplayName,
			ProviderTenantKey: item.ProviderTenantKey,
			Status:            strings.ToUpper(strings.TrimSpace(item.Status)),
			LastError:         item.LastError,
			LastUsedAt:        item.LastUsedAt,
			UpdatedAt:         item.UpdatedAt,
		})
	}
	return items, nil
}

func (c *HTTPAuthConnectionClient) doAuthServiceRequest(ctx context.Context, url, method string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.internalToken != "" {
		req.Header.Set("X-LazyMind-Internal-Token", c.internalToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeFeishuHTTPError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := decodeAuthServiceJSON(resp.Body, out); err != nil {
		return err
	}
	return nil
}

func decodeAuthServiceJSON(r io.Reader, out any) error {
	var raw json.RawMessage
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		return err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		return json.Unmarshal(envelope.Data, out)
	}
	return json.Unmarshal(raw, out)
}

func (c *HTTPAuthConnectionClient) doAuthServiceToken(ctx context.Context, url string, out *Token) error {
	var payload struct {
		AccessToken string `json:"access_token"`
		Data        struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := c.doAuthServiceRequest(ctx, url, http.MethodGet, nil, &payload); err != nil {
		return err
	}
	out.AccessToken = strings.TrimSpace(firstNonEmpty(payload.AccessToken, payload.Data.AccessToken))
	return nil
}

func authQuery(userID, tenantID string) url.Values {
	query := url.Values{}
	if userID = strings.TrimSpace(userID); userID != "" {
		query.Set("user_id", userID)
	}
	if tenantID = strings.TrimSpace(tenantID); tenantID != "" {
		query.Set("tenant_id", tenantID)
	}
	return query
}

type DefaultFeishuAPIClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewDefaultFeishuAPIClient(baseURL string, client *http.Client) (*DefaultFeishuAPIClient, error) {
	parsed, httpClient, err := newHTTPBoundary(baseURL, client)
	if err != nil {
		return nil, err
	}
	parsed = openAPIBaseURL(parsed)
	return &DefaultFeishuAPIClient{baseURL: parsed, httpClient: httpClient}, nil
}

func (c *DefaultFeishuAPIClient) GetDriveRoot(ctx context.Context, token string) (Object, error) {
	var out map[string]any
	if err := doFeishuOpenAPIJSON(ctx, c.httpClient, endpoint(c.baseURL, "/drive/explorer/v2/root_folder/meta", nil), http.MethodGet, token, nil, &out); err != nil {
		return Object{}, err
	}
	return driveRootObject(out), nil
}

func (c *DefaultFeishuAPIClient) GetDriveFolder(ctx context.Context, token, folderToken string) (Object, error) {
	folderToken = strings.TrimSpace(folderToken)
	if folderToken == "" || folderToken == "root" {
		return c.GetDriveRoot(ctx, token)
	}
	var out map[string]any
	path := "/drive/explorer/v2/folder/" + url.PathEscape(folderToken) + "/meta"
	if err := doFeishuOpenAPIJSON(ctx, c.httpClient, endpoint(c.baseURL, path, nil), http.MethodGet, token, nil, &out); err != nil {
		return Object{}, err
	}
	return driveFolderObject(openAPIMapValue(out["folder"], out), folderToken), nil
}

func (c *DefaultFeishuAPIClient) ListDriveChildren(ctx context.Context, token, folderToken, cursor string, pageSize int) (ObjectPage, error) {
	var out openAPIDriveFiles
	if err := doFeishuOpenAPIJSON(ctx, c.httpClient, endpoint(c.baseURL, "/drive/v1/files", driveFilesQuery(folderToken, cursor, pageSize)), http.MethodGet, token, nil, &out); err != nil {
		return ObjectPage{}, err
	}
	return driveObjectPage(out, folderToken), nil
}

func (c *DefaultFeishuAPIClient) DownloadDriveFile(ctx context.Context, token, fileToken, expectedVersion string) (ExportedContent, error) {
	body, err := doFeishuDownload(ctx, c.httpClient, endpoint(c.baseURL, "/drive/v1/files/"+url.PathEscape(fileToken)+"/download", nil), token)
	if err != nil {
		return ExportedContent{}, err
	}
	return ExportedContent{Content: body, SizeBytes: int64(len(body)), ExportedVersion: expectedVersion}, nil
}

func (c *DefaultFeishuAPIClient) ExportDriveDocumentMarkdown(ctx context.Context, token, docToken, expectedVersion string) (ExportedContent, error) {
	content, err := c.rawContent(ctx, token, "docx", docToken)
	if err != nil {
		if isFeishuNotFound(err) {
			content, err = c.rawContent(ctx, token, "doc", docToken)
		}
		if err != nil {
			return ExportedContent{}, err
		}
	}
	return ExportedContent{
		Content:         []byte(content),
		MimeType:        "text/markdown",
		FileExtension:   ".md",
		SizeBytes:       int64(len(content)),
		ExportedVersion: expectedVersion,
	}, nil
}

func (c *DefaultFeishuAPIClient) ListWikiSpaces(ctx context.Context, token, cursor string, pageSize int) (ObjectPage, error) {
	var out openAPIWikiSpaces
	if err := doFeishuOpenAPIJSON(ctx, c.httpClient, endpoint(c.baseURL, "/wiki/v2/spaces", openAPIPageQuery(cursor, pageSize)), http.MethodGet, token, nil, &out); err != nil {
		return ObjectPage{}, err
	}
	return wikiSpacesPage(out), nil
}

func (c *DefaultFeishuAPIClient) GetWikiNode(ctx context.Context, token, spaceID, nodeToken string) (Object, error) {
	var out map[string]any
	query := url.Values{}
	query.Set("token", nodeToken)
	if err := doFeishuOpenAPIJSON(ctx, c.httpClient, endpoint(c.baseURL, "/wiki/v2/spaces/get_node", query), http.MethodGet, token, nil, &out); err != nil {
		return Object{}, err
	}
	return wikiNodeObject(openAPIMapValue(out["node"], out), spaceID, nodeToken), nil
}

func (c *DefaultFeishuAPIClient) ListWikiChildren(ctx context.Context, token, spaceID, nodeToken, cursor string, pageSize int) (ObjectPage, error) {
	var out openAPIWikiNodes
	query := openAPIPageQuery(cursor, pageSize)
	if strings.TrimSpace(nodeToken) != "" {
		query.Set("parent_node_token", strings.TrimSpace(nodeToken))
	}
	path := "/wiki/v2/spaces/" + url.PathEscape(spaceID) + "/nodes"
	if err := doFeishuOpenAPIJSON(ctx, c.httpClient, endpoint(c.baseURL, path, query), http.MethodGet, token, nil, &out); err != nil {
		return ObjectPage{}, err
	}
	return wikiNodesPage(out, spaceID, strings.TrimSpace(nodeToken)), nil
}

func (c *DefaultFeishuAPIClient) ExportWikiNodeMarkdown(ctx context.Context, token, spaceID, nodeToken, expectedVersion string) (ExportedContent, error) {
	node, err := c.GetWikiNode(ctx, token, spaceID, nodeToken)
	if err != nil {
		return ExportedContent{}, err
	}
	objType := normalizedFeishuObjectType(firstNonEmpty(node.DriveType, node.FileExtension))
	objToken := firstNonEmpty(node.StableID, node.Token)
	if !isFeishuDocType(objType) && objType != "" && objType != "md" {
		exported, err := c.DownloadDriveFile(ctx, token, objToken, expectedVersion)
		if err != nil {
			return ExportedContent{}, err
		}
		if exported.MimeType == "" {
			exported.MimeType = node.MimeType
		}
		if exported.FileExtension == "" {
			exported.FileExtension = node.FileExtension
		}
		if exported.SizeBytes == 0 {
			exported.SizeBytes = node.SizeBytes
		}
		return exported, nil
	}
	if objType == "" || objType == "md" {
		objType = "docx"
	}
	content, err := c.rawContent(ctx, token, objType, objToken)
	if err != nil {
		return ExportedContent{}, err
	}
	return ExportedContent{Content: []byte(content), MimeType: "text/markdown", FileExtension: ".md", SizeBytes: int64(len(content)), ExportedVersion: expectedVersion}, nil
}

func (c *DefaultFeishuAPIClient) rawContent(ctx context.Context, token, objType, objToken string) (string, error) {
	path := "/docx/v1/documents/" + url.PathEscape(objToken) + "/raw_content"
	if objType == "doc" {
		path = "/doc/v2/" + url.PathEscape(objToken) + "/raw_content"
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := doFeishuOpenAPIJSON(ctx, c.httpClient, endpoint(c.baseURL, path, nil), http.MethodGet, token, nil, &out); err != nil {
		return "", err
	}
	return out.Content, nil
}

func newHTTPBoundary(baseURL string, client *http.Client) (*url.URL, *http.Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, nil, fmt.Errorf("base url must include scheme and host")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return parsed, client, nil
}

func openAPIBaseURL(base *url.URL) *url.URL {
	u := *base
	clean := strings.TrimRight(u.Path, "/")
	if clean == "" {
		u.Path = "/open-apis"
		return &u
	}
	if clean == "/open-apis" || strings.HasSuffix(clean, "/open-apis") {
		u.Path = clean
		return &u
	}
	u.Path = path.Join(clean, "open-apis")
	return &u
}

type openAPIDriveFiles struct {
	Files         []map[string]any `json:"files"`
	NextPageToken string           `json:"next_page_token"`
	PageToken     string           `json:"page_token"`
	HasMore       *bool            `json:"has_more"`
}

type openAPIWikiSpaces struct {
	Items         []map[string]any `json:"items"`
	Spaces        []map[string]any `json:"spaces"`
	NextPageToken string           `json:"next_page_token"`
	PageToken     string           `json:"page_token"`
	HasMore       *bool            `json:"has_more"`
}

type openAPIWikiNodes struct {
	Items         []map[string]any `json:"items"`
	Nodes         []map[string]any `json:"nodes"`
	NextPageToken string           `json:"next_page_token"`
	PageToken     string           `json:"page_token"`
	HasMore       *bool            `json:"has_more"`
}

func doFeishuOpenAPIJSON(ctx context.Context, client *http.Client, url, method, token string, in any, out any) error {
	body, err := encodeFeishuBody(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeFeishuHTTPErrorBody(resp.StatusCode, resp.Status, respBody)
	}
	if isHTMLResponse(resp.Header.Get("Content-Type"), respBody) {
		return connector.NewError(connector.ErrorCodeTransient, "feishu api returned non-json response")
	}
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return connector.NewError(connector.ErrorCodeTransient, "decode feishu api response failed")
	}
	if envelope.Code != 0 {
		return mapFeishuOpenAPIError(strconv.Itoa(envelope.Code), envelope.Msg, resp.StatusCode)
	}
	if out == nil || len(envelope.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return connector.NewError(connector.ErrorCodeTransient, "decode feishu api data failed")
	}
	return nil
}

func doFeishuDownload(ctx context.Context, client *http.Client, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "*/*")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeFeishuHTTPErrorBody(resp.StatusCode, resp.Status, body)
	}
	if isHTMLResponse(resp.Header.Get("Content-Type"), body) {
		return nil, connector.NewError(connector.ErrorCodeTransient, "feishu api returned non-json response")
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		var envelope struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if err := json.Unmarshal(body, &envelope); err == nil && envelope.Code != 0 {
			return nil, mapFeishuOpenAPIError(strconv.Itoa(envelope.Code), envelope.Msg, resp.StatusCode)
		}
	}
	return body, nil
}

func endpoint(base *url.URL, endpointPath string, query url.Values) string {
	u := *base
	u.Path = path.Join(base.Path, endpointPath)
	u.RawQuery = query.Encode()
	return u.String()
}

func openAPIPageQuery(cursor string, pageSize int) url.Values {
	query := url.Values{}
	if cursor != "" {
		query.Set("page_token", cursor)
	}
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	return query
}

func driveFilesQuery(folderToken, cursor string, pageSize int) url.Values {
	query := openAPIPageQuery(cursor, pageSize)
	if folderToken = strings.TrimSpace(folderToken); folderToken != "" && folderToken != "root" {
		query.Set("folder_token", folderToken)
	}
	return query
}

func driveRootObject(data map[string]any) Object {
	token := firstNonEmpty(openAPIString(data["token"]), openAPIString(data["folder_token"]), "root")
	return Object{
		Kind:        ObjectKindDriveFolder,
		Token:       token,
		Name:        firstNonEmpty(openAPIString(data["name"]), "Drive Root"),
		IsContainer: true,
		HasChildren: true,
		Revision:    token,
		StableID:    token,
	}
}

func driveFolderObject(data map[string]any, fallbackToken string) Object {
	token := firstNonEmpty(openAPIString(data["token"]), openAPIString(data["folder_token"]), fallbackToken)
	revision := firstNonEmpty(
		openAPIString(data["revision"]),
		openAPIString(data["modified_time"]),
		openAPIString(data["edit_time"]),
		openAPIString(data["updated_time"]),
		openAPIString(data["update_time"]),
		token,
	)
	return Object{
		Kind:        ObjectKindDriveFolder,
		Token:       token,
		ParentToken: firstNonEmpty(openAPIString(data["parent_token"]), openAPIString(data["parent_folder_token"])),
		Name:        firstNonEmpty(openAPIString(data["name"]), openAPIString(data["title"]), token),
		IsContainer: true,
		HasChildren: true,
		Revision:    revision,
		StableID:    token,
	}
}

func driveObjectPage(data openAPIDriveFiles, parentToken string) ObjectPage {
	items := make([]Object, 0, len(data.Files))
	for _, item := range data.Files {
		items = append(items, driveObject(item, parentToken))
	}
	next := firstNonEmpty(data.NextPageToken, data.PageToken)
	return ObjectPage{
		Items:      items,
		NextCursor: next,
		HasMore:    openAPIHasMore(data.HasMore, next),
	}
}

func driveObject(item map[string]any, parentToken string) Object {
	token := firstNonEmpty(openAPIString(item["token"]), openAPIString(item["file_token"]))
	name := firstNonEmpty(openAPIString(item["name"]), token)
	rawType := strings.ToLower(firstNonEmpty(openAPIString(item["type"]), openAPIString(item["file_type"])))
	isFolder := rawType == "folder"
	shortcutInfo, _ := item["shortcut_info"].(map[string]any)
	modified := openAPIInt64(firstNonEmpty(
		openAPIString(item["modified_time"]),
		openAPIString(item["edit_time"]),
		openAPIString(item["updated_time"]),
		openAPIString(item["update_time"]),
		openAPIString(item["file_modified_time"]),
		openAPIString(item["file_edit_time"]),
	))
	revision := firstNonEmpty(
		openAPIString(item["revision"]),
		openAPIString(item["modified_time"]),
		openAPIString(item["edit_time"]),
		openAPIString(item["updated_time"]),
		openAPIString(item["update_time"]),
		openAPIString(item["file_modified_time"]),
		openAPIString(item["file_edit_time"]),
		token,
	)
	object := Object{
		Kind:                ObjectKindDriveFile,
		Token:               token,
		ParentToken:         firstNonEmpty(strings.TrimSpace(parentToken), openAPIString(item["parent_token"]), openAPIString(item["parent_folder_token"])),
		Name:                name,
		IsDocument:          true,
		Revision:            revision,
		ModifiedUnixSec:     modified,
		SizeBytes:           openAPIInt64(item["size"]),
		MimeType:            openAPIString(item["mime_type"]),
		FileExtension:       driveFileExtension(item, name, rawType, shortcutInfo),
		DriveType:           rawType,
		ShortcutTargetType:  strings.ToLower(openAPIString(shortcutInfo["target_type"])),
		ShortcutTargetToken: openAPIString(shortcutInfo["target_token"]),
		StableID:            token,
	}
	if isFolder {
		object.Kind = ObjectKindDriveFolder
		object.IsDocument = false
		object.IsContainer = true
		object.HasChildren = true
		object.MimeType = ""
		object.FileExtension = ""
	}
	return object
}

func driveFileExtension(item map[string]any, name, rawType string, shortcutInfo map[string]any) string {
	if extension := openAPIString(item["file_extension"]); extension != "" {
		return extension
	}
	if isFeishuDocType(rawType) || rawType == "shortcut" && isFeishuDocType(openAPIString(shortcutInfo["target_type"])) {
		return ".md"
	}
	return path.Ext(strings.TrimSpace(name))
}

func wikiSpacesPage(data openAPIWikiSpaces) ObjectPage {
	spaces := data.Items
	if len(spaces) == 0 {
		spaces = data.Spaces
	}
	items := make([]Object, 0, len(spaces))
	for _, space := range spaces {
		spaceID := firstNonEmpty(openAPIString(space["space_id"]), openAPIString(space["space_token"]), openAPIString(space["token"]))
		items = append(items, Object{
			Kind:        ObjectKindWikiSpace,
			Token:       "feishu:wiki:space:" + spaceID,
			SpaceID:     spaceID,
			Name:        firstNonEmpty(openAPIString(space["name"]), openAPIString(space["space_name"]), spaceID),
			IsContainer: true,
			HasChildren: true,
			Revision:    firstNonEmpty(openAPIString(space["edit_time"]), openAPIString(space["update_time"]), spaceID),
		})
	}
	next := firstNonEmpty(data.NextPageToken, data.PageToken)
	return ObjectPage{Items: items, NextCursor: next, HasMore: openAPIHasMore(data.HasMore, next)}
}

func wikiNodesPage(data openAPIWikiNodes, spaceID, parentNodeToken string) ObjectPage {
	nodes := data.Items
	if len(nodes) == 0 {
		nodes = data.Nodes
	}
	items := make([]Object, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, wikiNodeObjectWithParent(node, spaceID, "", parentNodeToken))
	}
	next := firstNonEmpty(data.NextPageToken, data.PageToken)
	return ObjectPage{Items: items, NextCursor: next, HasMore: openAPIHasMore(data.HasMore, next)}
}

func openAPIHasMore(hasMore *bool, nextCursor string) bool {
	if hasMore != nil {
		return *hasMore
	}
	return strings.TrimSpace(nextCursor) != ""
}

func wikiNodeObject(node map[string]any, spaceID, fallbackToken string) Object {
	return wikiNodeObjectWithParent(node, spaceID, fallbackToken, "")
}

func wikiNodeObjectWithParent(node map[string]any, spaceID, fallbackToken, fallbackParentToken string) Object {
	nodeToken := firstNonEmpty(openAPIString(node["node_token"]), openAPIString(node["token"]), fallbackToken)
	resolvedSpaceID := firstNonEmpty(openAPIString(node["space_id"]), spaceID)
	objType := strings.ToLower(openAPIString(node["obj_type"]))
	objToken := openAPIString(node["obj_token"])
	hasChild := openAPIBool(node["has_child"])
	name := firstNonEmpty(openAPIString(node["title"]), openAPIString(node["node_title"]), openAPIString(node["name"]), openAPIString(node["obj_name"]), nodeToken)
	fileExtension := wikiNodeFileExtension(name, objType, node)
	mimeType := ""
	if !isFeishuDocType(objType) {
		mimeType = wikiNodeMimeType(fileExtension, node)
	}
	modified := openAPIInt64(firstNonEmpty(
		openAPIString(node["obj_edit_time"]),
		openAPIString(node["update_time"]),
		openAPIString(node["edit_time"]),
		openAPIString(node["modified_time"]),
		openAPIString(node["node_update_time"]),
		openAPIString(node["obj_update_time"]),
	))
	return Object{
		Kind:        ObjectKindWikiNode,
		Token:       nodeToken,
		ParentToken: firstNonEmpty(openAPIString(node["parent_node_token"]), strings.TrimSpace(fallbackParentToken)),
		SpaceID:     resolvedSpaceID,
		Name:        name,
		IsDocument:  true,
		IsContainer: true,
		HasChildren: hasChild,
		Revision: firstNonEmpty(
			openAPIString(node["obj_edit_time"]),
			openAPIString(node["update_time"]),
			openAPIString(node["edit_time"]),
			openAPIString(node["modified_time"]),
			openAPIString(node["node_update_time"]),
			openAPIString(node["obj_update_time"]),
			nodeToken,
		),
		ModifiedUnixSec: modified,
		SizeBytes:       openAPIInt64(firstNonEmpty(openAPIString(node["size"]), openAPIString(node["obj_size"]), openAPIString(node["file_size"]))),
		MimeType:        mimeType,
		FileExtension:   fileExtension,
		DriveType:       objType,
		StableID:        objToken,
	}
}

func wikiNodeFileExtension(name, objType string, node map[string]any) string {
	if extension := openAPIString(node["file_extension"]); extension != "" {
		if strings.HasPrefix(extension, ".") {
			return extension
		}
		return "." + extension
	}
	if objType == "file" {
		return path.Ext(name)
	}
	if objType == "" {
		if extension := path.Ext(strings.TrimSpace(name)); extension != "" {
			return extension
		}
		return ".docx"
	}
	return "." + objType
}

func wikiNodeMimeType(fileExtension string, node map[string]any) string {
	if mimeType := openAPIString(node["mime_type"]); mimeType != "" {
		return mimeType
	}
	return mime.TypeByExtension(fileExtension)
}

func openAPIMapValue(v any, fallback map[string]any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return fallback
}

func openAPIString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
}

func openAPIInt64(v any) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	default:
		n, _ := strconv.ParseInt(strings.TrimSpace(fmt.Sprintf("%v", v)), 10, 64)
		return n
	}
}

func openAPIBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		x = strings.TrimSpace(strings.ToLower(x))
		return x == "true" || x == "1" || x == "yes"
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	default:
		return false
	}
}

func encodeFeishuBody(in any) (io.Reader, error) {
	if in == nil {
		return nil, nil
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(in); err != nil {
		return nil, err
	}
	return &body, nil
}

func decodeFeishuHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return decodeFeishuHTTPErrorBody(resp.StatusCode, resp.Status, body)
}

func decodeFeishuHTTPErrorBody(statusCode int, status string, body []byte) error {
	var payload struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Msg     string `json:"msg"`
	}
	_ = json.Unmarshal(body, &payload)
	code := openAPIString(payload.Code)
	message := firstNonEmpty(payload.Message, payload.Msg, strings.TrimSpace(string(body)), status)
	if isHTMLResponse("", body) {
		message = "feishu api returned non-json response"
	}
	return mapFeishuOpenAPIError(code, message, statusCode)
}

func mapFeishuOpenAPIError(code, message string, statusCode int) error {
	code = strings.ToLower(strings.TrimSpace(code))
	if strings.TrimSpace(message) == "" {
		message = "feishu api request failed"
	}
	if statusCode == http.StatusTooManyRequests || isFeishuRateLimitMessage(message) {
		return connector.NewError(connector.ErrorCodeRateLimited, message)
	}

	switch code {
	case "connection_not_found", "token_expired", "refresh_failed", "auth_invalid":
		return connector.NewError(ErrorCodeAuthInvalid, message)
	case "scope_missing", "permission_denied":
		return connector.NewError(connector.ErrorCodePermissionDenied, message)
	case "rate_limited":
		return connector.NewError(connector.ErrorCodeRateLimited, message)
	case "unsupported_export":
		return connector.NewError(ErrorCodeExportDenied, message)
	}

	if isFeishuAuthInvalidMessage(message) {
		return connector.NewError(ErrorCodeAuthInvalid, message)
	}
	if isFeishuPermissionDeniedMessage(message) {
		return connector.NewError(connector.ErrorCodePermissionDenied, message)
	}
	if isFeishuUnsupportedExportMessage(message) {
		return connector.NewError(ErrorCodeExportDenied, message)
	}

	switch statusCode {
	case http.StatusUnauthorized:
		return connector.NewError(ErrorCodeAuthInvalid, message)
	case http.StatusNotFound:
		return connector.NewError(connector.ErrorCodeNotFound, message)
	case http.StatusForbidden:
		return connector.NewError(connector.ErrorCodePermissionDenied, message)
	default:
		return connector.NewError(connector.ErrorCodeTransient, message)
	}
}

func isFeishuAuthInvalidMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	for _, keyword := range []string{
		"auth_connection_invalid",
		"connection_not_found",
		"token_expired",
		"refresh_failed",
		"auth invalid",
		"authentication failed",
		"access token invalid",
		"invalid access token",
		"invalid token",
		"token invalid",
		"token expired",
		"unauthorized",
	} {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func isFeishuPermissionDeniedMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	if message == "" {
		return false
	}
	for _, keyword := range []string{
		"permission_denied",
		"scope_missing",
		"scope missing",
		"missing scope",
		"permission denied",
		"permission missing",
		"permission required",
		"no permission",
		"access denied",
		"forbidden",
		"not allowed",
		"download permission",
		"export permission",
	} {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func isFeishuUnsupportedExportMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "unsupported_export") ||
		strings.Contains(message, "unsupported export") ||
		strings.Contains(message, "export not supported") ||
		strings.Contains(message, "not support export")
}

func isFeishuRateLimitMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "frequency limit") ||
		strings.Contains(message, "rate limit") ||
		strings.Contains(message, "too many requests")
}

func isHTMLResponse(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return true
	}
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(strings.ToLower(trimmed), "<!doctype html") || strings.HasPrefix(strings.ToLower(trimmed), "<html")
}

func isFeishuNotFound(err error) bool {
	code, ok := connector.ErrorCodeOf(err)
	return ok && code == connector.ErrorCodeNotFound
}
