package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func TestHTTPAuthConnectionClientTokenMapping(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/api/authservice/v1/cloud/connections/auth-1/token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("user_id") != "user-1" || r.URL.Query().Get("tenant_id") != "" {
			t.Fatalf("token request did not include owner scope: %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-LazyMind-Internal-Token") != "internal-token" {
			t.Fatalf("missing internal service token")
		}
		writeFeishuJSON(t, w, http.StatusOK, map[string]any{"connection_id": "auth-1", "access_token": "user-token"})
	}))
	defer server.Close()

	client := newHTTPAuthTestClient(t, server.URL)
	token, err := client.GetToken(context.Background(), TokenRequest{AuthConnectionID: "auth-1", UserID: "user-1"})
	if err != nil || token.AccessToken != "user-token" {
		t.Fatalf("unexpected token=%+v err=%v", token, err)
	}
}

func TestHTTPAuthConnectionClientVerifyUsesOwnerScope(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/api/authservice/v1/cloud/connections/auth-1/verify" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("user_id") != "user-1" || r.URL.Query().Get("tenant_id") != "tenant-1" {
			t.Fatalf("verify request did not include owner scope: %s", r.URL.RawQuery)
		}
		writeFeishuJSON(t, w, http.StatusOK, map[string]any{"connection_id": "auth-1", "valid": true})
	}))
	defer server.Close()

	client := newHTTPAuthTestClient(t, server.URL)
	if err := client.Verify(context.Background(), "auth-1", "user-1", "tenant-1"); err != nil {
		t.Fatalf("verify auth connection: %v", err)
	}
}

func TestHTTPAuthConnectionClientMapsExpiredToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFeishuJSON(t, w, http.StatusUnauthorized, map[string]string{"code": "token_expired", "message": "refresh failed"})
	}))
	defer server.Close()

	client := newHTTPAuthTestClient(t, server.URL)
	_, err := client.GetToken(context.Background(), TokenRequest{AuthConnectionID: "auth-1"})
	assertFeishuErrorCode(t, err, ErrorCodeAuthInvalid)
}

func TestDefaultFeishuAPIClientDriveAndWikiMapping(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("missing bearer token on %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/open-apis/drive/explorer/v2/root_folder/meta":
			writeFeishuOpenAPIData(t, w, map[string]any{"token": "root-token", "name": "Drive Root"})
		case "/open-apis/drive/explorer/v2/folder/folder-1/meta":
			seen["drive_folder_meta"] = "yes"
			writeFeishuOpenAPIData(t, w, map[string]any{"token": "folder-1", "name": "Folder Meta", "parent_token": "root-token", "edit_time": "1710000000"})
		case "/open-apis/drive/v1/files":
			seen["drive_cursor"] = r.URL.Query().Get("cursor")
			seen["drive_page_token"] = r.URL.Query().Get("page_token")
			seen["drive_page_size"] = r.URL.Query().Get("page_size")
			seen["drive_folder_token"] = r.URL.Query().Get("folder_token")
			writeFeishuOpenAPIData(t, w, map[string]any{
				"files": []map[string]any{
					{"type": "folder", "token": "folder-1", "name": "Folder", "has_child": true, "edit_time": "1710000000"},
					{"type": "file", "token": "file-1", "name": "a.pdf", "mime_type": "application/pdf", "file_extension": ".pdf", "size": 7, "revision": "rev-1"},
				},
				"next_page_token": "next",
			})
		case "/open-apis/drive/v1/files/file-1/download":
			seen["drive_download"] = "yes"
			_, _ = w.Write([]byte("drive-bytes"))
		case "/open-apis/wiki/v2/spaces":
			seen["wiki_page_token"] = r.URL.Query().Get("page_token")
			writeFeishuOpenAPIData(t, w, map[string]any{"items": []map[string]any{{"space_id": "space-1", "name": "Wiki"}}})
		case "/open-apis/wiki/v2/spaces/get_node":
			if r.URL.Query().Get("token") != "node-1" {
				t.Fatalf("unexpected wiki node token query: %s", r.URL.RawQuery)
			}
			writeFeishuOpenAPIData(t, w, map[string]any{"node": map[string]any{"space_id": "space-1", "node_token": "node-1", "title": "Node", "obj_type": "docx", "obj_token": "docx-1", "has_child": true}})
		case "/open-apis/wiki/v2/spaces/space-1/nodes":
			if r.URL.Query().Get("parent_node_token") != "node-1" {
				t.Fatalf("unexpected wiki parent query: %s", r.URL.RawQuery)
			}
			writeFeishuOpenAPIData(t, w, map[string]any{"items": []map[string]any{{"node_token": "node-child", "title": "Child", "obj_type": "docx", "obj_token": "docx-child"}}})
		case "/open-apis/docx/v1/documents/docx-1/raw_content":
			seen["wiki_export"] = "yes"
			writeFeishuOpenAPIData(t, w, map[string]string{"content": "wiki markdown"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newHTTPFeishuAPITestClient(t, server.URL)
	if _, err := client.GetDriveRoot(context.Background(), "user-token"); err != nil {
		t.Fatalf("get drive root: %v", err)
	}
	folder, err := client.GetDriveFolder(context.Background(), "user-token", "folder-1")
	if err != nil {
		t.Fatalf("get drive folder: %v", err)
	}
	if folder.Name != "Folder Meta" || folder.Token != "folder-1" || folder.ParentToken != "root-token" {
		t.Fatalf("drive folder metadata was not mapped: %+v", folder)
	}
	if _, err := client.ListDriveChildren(context.Background(), "user-token", "folder-1", "cursor-1", 50); err != nil {
		t.Fatalf("list drive children: %v", err)
	}
	if _, err := client.DownloadDriveFile(context.Background(), "user-token", "file-1", "rev-1"); err != nil {
		t.Fatalf("download drive file: %v", err)
	}
	if _, err := client.ListWikiSpaces(context.Background(), "user-token", "cursor-2", 25); err != nil {
		t.Fatalf("list wiki spaces: %v", err)
	}
	if _, err := client.GetWikiNode(context.Background(), "user-token", "space-1", "node-1"); err != nil {
		t.Fatalf("get wiki node: %v", err)
	}
	if _, err := client.ListWikiChildren(context.Background(), "user-token", "space-1", "node-1", "", 10); err != nil {
		t.Fatalf("list wiki children: %v", err)
	}
	if _, err := client.ExportWikiNodeMarkdown(context.Background(), "user-token", "space-1", "node-1", "wiki-rev"); err != nil {
		t.Fatalf("export wiki: %v", err)
	}
	if seen["drive_cursor"] != "" || seen["drive_page_token"] != "cursor-1" || seen["drive_page_size"] != "50" || seen["drive_folder_token"] != "folder-1" || seen["drive_folder_meta"] != "yes" || seen["wiki_page_token"] != "cursor-2" {
		t.Fatalf("pagination query was not mapped: %+v", seen)
	}
	if seen["drive_download"] != "yes" || seen["wiki_export"] != "yes" {
		t.Fatalf("export APIs were not called: %+v", seen)
	}
}

func TestDefaultFeishuAPIClientMapsWikiNodeNameFallbacks(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/wiki/v2/spaces/get_node":
			writeFeishuOpenAPIData(t, w, map[string]any{"node": map[string]any{"space_id": "space-1", "node_token": "node-1", "node_title": "Node Title", "obj_type": "docx", "obj_token": "docx-1"}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newHTTPFeishuAPITestClient(t, server.URL)
	node, err := client.GetWikiNode(context.Background(), "user-token", "space-1", "node-1")
	if err != nil {
		t.Fatalf("get wiki node: %v", err)
	}
	if node.Name != "Node Title" {
		t.Fatalf("wiki node title fallback was not mapped: %+v", node)
	}
}

func TestDefaultFeishuAPIClientOpenFeishuBaseURLUsesOpenAPISPrefix(t *testing.T) {
	t.Parallel()

	var gotPath string
	client := newHTTPFeishuAPIClientWithTransport(t, "https://open.feishu.cn", roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		return jsonResponse(http.StatusOK, map[string]any{"code": 0, "msg": "ok", "data": map[string]string{"token": "root-token"}}), nil
	}))

	root, err := client.GetDriveRoot(context.Background(), "user-token")
	if err != nil {
		t.Fatalf("get drive root: %v", err)
	}
	if gotPath != "/open-apis/drive/explorer/v2/root_folder/meta" {
		t.Fatalf("expected OpenAPI root path, got %s", gotPath)
	}
	if root.Token != "root-token" || root.Kind != ObjectKindDriveFolder {
		t.Fatalf("unexpected root mapping: %+v", root)
	}
}

func TestDefaultFeishuAPIClientMapsHTMLResponseToTransient(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not an api response</html>"))
	}))
	defer server.Close()

	client := newHTTPFeishuAPITestClient(t, server.URL)
	_, err := client.GetDriveRoot(context.Background(), "user-token")
	assertFeishuErrorCode(t, err, connector.ErrorCodeTransient)
	if err != nil && err.Error() == "invalid character '<' looking for beginning of value" {
		t.Fatalf("html decode error leaked as raw internal error")
	}
}

func TestDefaultFeishuAPIClientDriveRootChildrenParseOpenAPIList(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/open-apis/drive/explorer/v2/root_folder/meta":
			writeFeishuOpenAPIData(t, w, map[string]string{"token": "root-token", "name": "Root"})
		case "/open-apis/drive/v1/files":
			if r.URL.Query().Get("folder_token") != "root-token" {
				t.Fatalf("expected root folder token, got query %s", r.URL.RawQuery)
			}
			writeFeishuOpenAPIData(t, w, map[string]any{"files": []map[string]any{{"token": "child-file", "name": "Child.md", "type": "file", "update_time": "1710002222"}}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newHTTPFeishuAPITestClient(t, server.URL)
	root, err := client.GetDriveRoot(context.Background(), "user-token")
	if err != nil {
		t.Fatalf("get drive root: %v", err)
	}
	page, err := client.ListDriveChildren(context.Background(), "user-token", root.Token, "", 50)
	if err != nil {
		t.Fatalf("list root children: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Token != "child-file" || !page.Items[0].IsDocument {
		t.Fatalf("unexpected drive list mapping: %+v", page)
	}
	if len(paths) != 2 || paths[0] != "/open-apis/drive/explorer/v2/root_folder/meta" || paths[1] != "/open-apis/drive/v1/files" {
		t.Fatalf("unexpected request sequence: %v", paths)
	}
}

func TestDefaultFeishuAPIClientExportsDriveDocumentRawContent(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/open-apis/docx/v1/documents/docx-token/raw_content":
			writeFeishuOpenAPIData(t, w, map[string]string{"content": "docx markdown"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newHTTPFeishuAPITestClient(t, server.URL)
	exported, err := client.ExportDriveDocumentMarkdown(context.Background(), "user-token", "docx-token", "rev-1")
	if err != nil {
		t.Fatalf("export drive document raw content: %v", err)
	}
	if string(exported.Content) != "docx markdown" || exported.FileExtension != ".md" || exported.MimeType != "text/markdown" || exported.ExportedVersion != "rev-1" {
		t.Fatalf("unexpected exported content: %+v", exported)
	}
	if len(paths) != 1 || paths[0] != "/open-apis/docx/v1/documents/docx-token/raw_content" {
		t.Fatalf("unexpected request sequence: %v", paths)
	}
}

func TestDefaultFeishuAPIClientMapsScopeMissing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeFeishuJSON(t, w, http.StatusForbidden, map[string]string{"code": "scope_missing", "message": "scope missing"})
	}))
	defer server.Close()

	client := newHTTPFeishuAPITestClient(t, server.URL)
	_, err := client.GetDriveRoot(context.Background(), "user-token")
	assertFeishuErrorCode(t, err, connector.ErrorCodePermissionDenied)
}

func newHTTPAuthTestClient(t *testing.T, baseURL string) *HTTPAuthConnectionClient {
	t.Helper()
	client, err := NewHTTPAuthConnectionClient(baseURL, "internal-token", nil)
	if err != nil {
		t.Fatalf("new http auth client: %v", err)
	}
	return client
}

func newHTTPFeishuAPITestClient(t *testing.T, baseURL string) *DefaultFeishuAPIClient {
	t.Helper()
	client, err := NewDefaultFeishuAPIClient(baseURL, nil)
	if err != nil {
		t.Fatalf("new http feishu api client: %v", err)
	}
	return client
}

func newHTTPFeishuAPIClientWithTransport(t *testing.T, baseURL string, transport http.RoundTripper) *DefaultFeishuAPIClient {
	t.Helper()
	client, err := NewDefaultFeishuAPIClient(baseURL, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("new http feishu api client: %v", err)
	}
	return client
}

func writeFeishuJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("write response: %v", err)
	}
}

func writeFeishuOpenAPIData(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	writeFeishuJSON(t, w, http.StatusOK, map[string]any{"code": 0, "msg": "ok", "data": data})
}

func jsonResponse(status int, payload any) *http.Response {
	body, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
