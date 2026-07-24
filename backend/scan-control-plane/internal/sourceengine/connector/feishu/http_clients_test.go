package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestHTTPAuthConnectionClientBatchStatus(t *testing.T) {
	t.Parallel()

	var requestBody struct {
		ConnectionIDs []string `json:"connection_ids"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/api/authservice/v1/cloud/connections/status:batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("user_id") != "user-1" || r.URL.Query().Get("tenant_id") != "tenant-1" {
			t.Fatalf("batch status request did not include owner scope: %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-LazyMind-Internal-Token") != "internal-token" {
			t.Fatalf("missing internal service token")
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		writeFeishuJSON(t, w, http.StatusOK, map[string]any{
			"items": []map[string]any{
				{"connection_id": "auth-1", "provider": "feishu", "status": "ACTIVE"},
				{"connection_id": "auth-2", "provider": "feishu", "status": "REVOKED", "last_error": "deleted"},
			},
		})
	}))
	defer server.Close()

	client := newHTTPAuthTestClient(t, server.URL)
	statuses, err := client.BatchStatus(context.Background(), ConnectionStatusRequest{
		ConnectionIDs: []string{"auth-1", "auth-2", "auth-1", ""},
		UserID:        "user-1",
		TenantID:      "tenant-1",
	})
	if err != nil {
		t.Fatalf("batch status: %v", err)
	}
	if !reflect.DeepEqual(requestBody.ConnectionIDs, []string{"auth-1", "auth-2"}) {
		t.Fatalf("connection ids were not deduped: %#v", requestBody.ConnectionIDs)
	}
	if statuses["auth-1"].Status != "ACTIVE" || statuses["auth-2"].Status != "REVOKED" || statuses["auth-2"].LastError != "deleted" {
		t.Fatalf("unexpected statuses: %+v", statuses)
	}
}

func TestHTTPAuthConnectionClientListTargetCacheConnections(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if r.URL.Path != "/api/authservice/v1/cloud/connections/internal/target-cache-candidates" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("provider") != "feishu" || r.URL.Query().Get("limit") != "50" {
			t.Fatalf("unexpected target cache query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("X-LazyMind-Internal-Token") != "internal-token" {
			t.Fatalf("missing internal service token")
		}
		body := `{"code":200,"message":"success","data":{"items":[{"connection_id":"auth-1","owner_user_id":"user-1","provider":"feishu","provider_tenant_key":"tenant-demo","status":"active"}]}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	client, err := NewHTTPAuthConnectionClient("http://auth.test", "internal-token", httpClient)
	if err != nil {
		t.Fatalf("create auth client: %v", err)
	}
	items, err := client.ListTargetCacheConnections(context.Background(), ConnectionListRequest{Provider: "feishu", Limit: 50})
	if err != nil {
		t.Fatalf("list target cache connections: %v", err)
	}
	if len(items) != 1 || items[0].ConnectionID != "auth-1" || items[0].ProviderTenantKey != "tenant-demo" || items[0].Status != "ACTIVE" {
		t.Fatalf("unexpected target cache connections: %+v", items)
	}
}

func TestOpenAPIPagesRespectExplicitHasMoreFalse(t *testing.T) {
	t.Parallel()

	hasMore := false
	drive := driveObjectPage(openAPIDriveFiles{
		Files: []map[string]any{
			{"type": "file", "token": "file-1", "name": "File"},
		},
		PageToken: "echoed-token",
		HasMore:   &hasMore,
	}, "root")
	if drive.HasMore {
		t.Fatalf("drive page should respect explicit has_more=false: %+v", drive)
	}

	spaces := wikiSpacesPage(openAPIWikiSpaces{
		Items: []map[string]any{
			{"space_id": "space-1", "name": "Space"},
		},
		PageToken: "echoed-token",
		HasMore:   &hasMore,
	})
	if spaces.HasMore {
		t.Fatalf("wiki spaces page should respect explicit has_more=false: %+v", spaces)
	}

	nodes := wikiNodesPage(openAPIWikiNodes{
		Items: []map[string]any{
			{"node_token": "node-1", "title": "Node"},
		},
		PageToken: "echoed-token",
		HasMore:   &hasMore,
	}, "space-1", "")
	if nodes.HasMore {
		t.Fatalf("wiki nodes page should respect explicit has_more=false: %+v", nodes)
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
	downloaded, err := client.DownloadDriveFile(context.Background(), "user-token", "file-1", "rev-1")
	if err != nil {
		t.Fatalf("download drive file: %v", err)
	}
	if downloaded.SizeBytes != int64(len("drive-bytes")) {
		t.Fatalf("download should expose content size, got %+v", downloaded)
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

func TestDefaultFeishuAPIClientExportsWikiFileNodeWithDriveDownload(t *testing.T) {
	t.Parallel()

	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/open-apis/wiki/v2/spaces/get_node":
			writeFeishuOpenAPIData(t, w, map[string]any{"node": map[string]any{
				"space_id":   "space-1",
				"node_token": "node-1",
				"title":      "ALCOHOLDINGS.pdf",
				"obj_type":   "file",
				"obj_token":  "file-1",
			}})
		case "/open-apis/drive/v1/files/file-1/download":
			_, _ = w.Write([]byte("%PDF-1.7"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newHTTPFeishuAPITestClient(t, server.URL)
	exported, err := client.ExportWikiNodeMarkdown(context.Background(), "user-token", "space-1", "node-1", "wiki-rev")
	if err != nil {
		t.Fatalf("export wiki file node: %v", err)
	}
	if string(exported.Content) != "%PDF-1.7" || exported.SizeBytes != int64(len("%PDF-1.7")) || exported.FileExtension != ".pdf" || exported.MimeType != "application/pdf" || exported.ExportedVersion != "wiki-rev" {
		t.Fatalf("unexpected wiki file export: %+v", exported)
	}
	if len(paths) != 2 || paths[0] != "/open-apis/wiki/v2/spaces/get_node" || paths[1] != "/open-apis/drive/v1/files/file-1/download" {
		t.Fatalf("unexpected request sequence: %v", paths)
	}
}

func TestWikiFileNodeObjectIsDocumentAndContainer(t *testing.T) {
	t.Parallel()

	obj := wikiNodeObject(map[string]any{
		"space_id":   "space-1",
		"node_token": "node-1",
		"title":      "aa.doc",
		"obj_type":   "file",
		"obj_token":  "file-1",
		"has_child":  false,
	}, "space-1", "")

	if !obj.IsDocument || !obj.IsContainer || obj.HasChildren {
		t.Fatalf("wiki uploaded file should be document and container without implying children: %+v", obj)
	}
	if obj.Name != "aa.doc" || obj.DriveType != "file" || obj.FileExtension != ".doc" {
		t.Fatalf("wiki uploaded file metadata changed unexpectedly: %+v", obj)
	}
}

func TestDriveObjectMapsShortcutTargetMetadata(t *testing.T) {
	t.Parallel()

	obj := driveObject(map[string]any{
		"type":     "shortcut",
		"token":    "shortcut-1",
		"name":     "alias.pdf",
		"revision": "rev-1",
		"shortcut_info": map[string]any{
			"target_type":  "file",
			"target_token": "file-target",
		},
	}, "folder-1")

	if obj.DriveType != "shortcut" || obj.ShortcutTargetType != "file" || obj.ShortcutTargetToken != "file-target" {
		t.Fatalf("shortcut target metadata was not mapped: %+v", obj)
	}
	if obj.FileExtension != ".pdf" {
		t.Fatalf("shortcut upload file should infer extension from display name: %+v", obj)
	}
}

func TestDriveObjectTreatsNativeShortcutAsMarkdown(t *testing.T) {
	t.Parallel()

	obj := driveObject(map[string]any{
		"type":     "shortcut",
		"token":    "shortcut-1",
		"name":     "native-doc",
		"revision": "rev-1",
		"shortcut_info": map[string]any{
			"target_type":  "docx",
			"target_token": "docx-target",
		},
	}, "folder-1")

	if obj.FileExtension != ".md" {
		t.Fatalf("native shortcut should be exposed as markdown: %+v", obj)
	}
}

func TestWikiNodeObjectUsesNameExtensionWhenTypeMissing(t *testing.T) {
	t.Parallel()

	obj := wikiNodeObject(map[string]any{
		"node_token": "node-1",
		"title":      "script.py",
	}, "space-1", "")

	if obj.FileExtension != ".py" {
		t.Fatalf("wiki upload with suffix should use display name extension: %+v", obj)
	}
}

func TestDriveObjectUsesUpdatedTimeFallbackForVersion(t *testing.T) {
	t.Parallel()

	obj := driveObject(map[string]any{
		"type":         "file",
		"token":        "file-1",
		"name":         "Guide.md",
		"updated_time": "1710002222",
	}, "folder-1")

	if obj.Revision != "1710002222" || versionFor(obj) != "1710002222" {
		t.Fatalf("drive version should use updated_time fallback, got revision=%q version=%q", obj.Revision, versionFor(obj))
	}
}

func TestWikiNodeObjectUsesModifiedTimeFallbacksForVersion(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"modified_time", "node_update_time", "obj_update_time"} {
		field := field
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			obj := wikiNodeObject(map[string]any{
				"node_token": "node-1",
				"obj_type":   "docx",
				"obj_token":  "docx-1",
				field:        "1710003333",
			}, "space-1", "")

			if obj.Revision != "1710003333" || versionFor(obj) != "1710003333" {
				t.Fatalf("wiki version should use %s fallback, got revision=%q version=%q", field, obj.Revision, versionFor(obj))
			}
		})
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

func TestFeishuOpenAPIMapsFrequencyLimitAsRateLimited(t *testing.T) {
	t.Parallel()

	err := mapFeishuOpenAPIError("99991400", "request trigger frequency limit", http.StatusOK)
	assertFeishuErrorCode(t, err, connector.ErrorCodeRateLimited)
}

func TestFeishuOpenAPIMapsPermissionTextAsPermissionDenied(t *testing.T) {
	t.Parallel()

	err := mapFeishuOpenAPIError("123456", "permission denied: no download permission", http.StatusOK)
	assertFeishuErrorCode(t, err, connector.ErrorCodePermissionDenied)
}

func TestFeishuOpenAPIMapsAuthTextAsAuthInvalid(t *testing.T) {
	t.Parallel()

	err := mapFeishuOpenAPIError("123456", "access token invalid", http.StatusOK)
	assertFeishuErrorCode(t, err, ErrorCodeAuthInvalid)
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
