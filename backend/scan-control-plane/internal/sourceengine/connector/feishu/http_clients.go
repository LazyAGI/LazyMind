package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if err := c.doAuthServiceJSON(ctx, endpoint(c.baseURL, path, authQuery(req.UserID, "")), &out); err != nil {
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
	return c.doAuthServiceJSON(ctx, endpoint(c.baseURL, path, authQuery(userID, tenantID)), nil)
}

func (c *HTTPAuthConnectionClient) doAuthServiceJSON(ctx context.Context, url string, out *Token) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
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
	var payload struct {
		AccessToken string `json:"access_token"`
		Data        struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
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
	return ExportedContent{Content: body, ExportedVersion: expectedVersion}, nil
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
	return wikiNodesPage(out, spaceID), nil
}

func (c *DefaultFeishuAPIClient) ExportWikiNodeMarkdown(ctx context.Context, token, spaceID, nodeToken, expectedVersion string) (ExportedContent, error) {
	node, err := c.GetWikiNode(ctx, token, spaceID, nodeToken)
	if err != nil {
		return ExportedContent{}, err
	}
	objType := strings.ToLower(strings.TrimSpace(node.FileExtension))
	objToken := firstNonEmpty(node.StableID, node.Token)
	if objType == ".doc" {
		objType = "doc"
	} else if objType == ".docx" || objType == "" {
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
}

type openAPIWikiSpaces struct {
	Items         []map[string]any `json:"items"`
	Spaces        []map[string]any `json:"spaces"`
	NextPageToken string           `json:"next_page_token"`
	PageToken     string           `json:"page_token"`
	HasMore       bool             `json:"has_more"`
}

type openAPIWikiNodes struct {
	Items         []map[string]any `json:"items"`
	Nodes         []map[string]any `json:"nodes"`
	NextPageToken string           `json:"next_page_token"`
	PageToken     string           `json:"page_token"`
	HasMore       bool             `json:"has_more"`
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
	return ObjectPage{
		Items:      items,
		NextCursor: firstNonEmpty(data.NextPageToken, data.PageToken),
		HasMore:    firstNonEmpty(data.NextPageToken, data.PageToken) != "",
	}
}

func driveObject(item map[string]any, parentToken string) Object {
	token := openAPIString(item["token"])
	name := firstNonEmpty(openAPIString(item["name"]), token)
	rawType := strings.ToLower(firstNonEmpty(openAPIString(item["type"]), openAPIString(item["file_type"])))
	isFolder := rawType == "folder"
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
		Kind:            ObjectKindDriveFile,
		Token:           token,
		ParentToken:     strings.TrimSpace(parentToken),
		Name:            name,
		IsDocument:      true,
		Revision:        revision,
		ModifiedUnixSec: modified,
		SizeBytes:       openAPIInt64(item["size"]),
		MimeType:        openAPIString(item["mime_type"]),
		FileExtension:   openAPIString(item["file_extension"]),
		DriveType:       rawType,
		StableID:        token,
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
	return ObjectPage{Items: items, NextCursor: next, HasMore: data.HasMore || next != ""}
}

func wikiNodesPage(data openAPIWikiNodes, spaceID string) ObjectPage {
	nodes := data.Items
	if len(nodes) == 0 {
		nodes = data.Nodes
	}
	items := make([]Object, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, wikiNodeObject(node, spaceID, ""))
	}
	next := firstNonEmpty(data.NextPageToken, data.PageToken)
	return ObjectPage{Items: items, NextCursor: next, HasMore: data.HasMore || next != ""}
}

func wikiNodeObject(node map[string]any, spaceID, fallbackToken string) Object {
	nodeToken := firstNonEmpty(openAPIString(node["node_token"]), openAPIString(node["token"]), fallbackToken)
	resolvedSpaceID := firstNonEmpty(openAPIString(node["space_id"]), spaceID)
	objType := strings.ToLower(openAPIString(node["obj_type"]))
	objToken := openAPIString(node["obj_token"])
	hasChild := openAPIBool(node["has_child"])
	modified := openAPIInt64(firstNonEmpty(
		openAPIString(node["obj_edit_time"]),
		openAPIString(node["update_time"]),
		openAPIString(node["edit_time"]),
		openAPIString(node["modified_time"]),
		openAPIString(node["node_update_time"]),
		openAPIString(node["obj_update_time"]),
	))
	return Object{
		Kind:            ObjectKindWikiNode,
		Token:           nodeToken,
		ParentToken:     openAPIString(node["parent_node_token"]),
		SpaceID:         resolvedSpaceID,
		Name:            firstNonEmpty(openAPIString(node["title"]), openAPIString(node["node_title"]), openAPIString(node["name"]), openAPIString(node["obj_name"]), nodeToken),
		IsDocument:      true,
		IsContainer:     hasChild || objType == "folder" || objType == "wiki" || objType == "space",
		HasChildren:     hasChild,
		Revision:        firstNonEmpty(openAPIString(node["obj_edit_time"]), openAPIString(node["update_time"]), openAPIString(node["edit_time"]), nodeToken),
		ModifiedUnixSec: modified,
		FileExtension:   "." + firstNonEmpty(objType, "docx"),
		StableID:        objToken,
	}
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
	if strings.TrimSpace(message) == "" {
		message = "feishu api request failed"
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
	default:
		if statusCode == http.StatusUnauthorized {
			return connector.NewError(ErrorCodeAuthInvalid, message)
		}
		if statusCode == http.StatusNotFound {
			return connector.NewError(connector.ErrorCodeNotFound, message)
		}
		if statusCode == http.StatusForbidden {
			return connector.NewError(connector.ErrorCodePermissionDenied, message)
		}
		return connector.NewError(connector.ErrorCodeTransient, message)
	}
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
