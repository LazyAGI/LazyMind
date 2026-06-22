package localfs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func TestHTTPAgentClientMapsLocalFSRequests(t *testing.T) {
	var validateReq ValidatePathRequest
	var rootsReq ListRootsRequest
	var listReq ListDirRequest
	var statReq StatPathRequest
	var exportReq ExportFileRequest
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer agent-token" {
			t.Fatalf("expected bearer token, got %q", auth)
		}
		var payload any
		switch r.URL.Path {
		case "/api/v1/agents/fs/validate":
			decodeAgentJSON(t, r, &validateReq)
			payload = PathInfo{Path: validateReq.Path, NormalizedPath: "/workspace/docs", DisplayName: "docs", Exists: true, Readable: true, IsDir: true}
		case "/api/v1/agents/fs/roots":
			decodeAgentJSON(t, r, &rootsReq)
			payload = struct {
				Items []PathInfo `json:"items"`
			}{Items: []PathInfo{{Path: "/workspace", NormalizedPath: "/workspace", DisplayName: "workspace", Exists: true, Readable: true, IsDir: true}}}
		case "/api/v1/agents/fs/list":
			decodeAgentJSON(t, r, &listReq)
			payload = ListDirPage{Items: []PathInfo{{Path: "/workspace/docs/a.md", NormalizedPath: "/workspace/docs/a.md", DisplayName: "a.md", SizeBytes: 5, MTimeUnixNano: 10, MimeType: "text/markdown", FileExtension: ".md"}}, HasMore: true, NextCursor: "2"}
		case "/api/v1/agents/fs/stat":
			decodeAgentJSON(t, r, &statReq)
			payload = PathInfo{Path: statReq.Path, NormalizedPath: statReq.Path, Exists: true, Readable: true, SizeBytes: 5, MTimeUnixNano: 10}
		case "/api/v1/agents/fs/export":
			decodeAgentJSON(t, r, &exportReq)
			payload = ExportedFile{ContentURI: "scan-temp://token", SizeBytes: 5, MTimeUnixNano: 10, MimeType: "text/markdown", FileExtension: ".md", CleanupToken: "token"}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return agentJSONResponse(t, http.StatusOK, payload), nil
	})

	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_AGENT_TOKEN", "agent-token")
	client := newHTTPAgentTestClient(t, "http://agent.test", transport)
	info, err := client.ValidatePath(context.Background(), ValidatePathRequest{AgentID: "agent-1", Path: "/workspace/docs", UserID: "user-1"})
	if err != nil || info.NormalizedPath != "/workspace/docs" {
		t.Fatalf("validate path info=%+v err=%v", info, err)
	}
	roots, err := client.ListRoots(context.Background(), ListRootsRequest{AgentID: "agent-1", UserID: "user-1"})
	if err != nil || len(roots) != 1 || roots[0].Path != "/workspace" {
		t.Fatalf("list roots=%+v err=%v", roots, err)
	}
	page, err := client.ListDir(context.Background(), ListDirRequest{AgentID: "agent-1", Path: "/workspace/docs", Cursor: "1", PageSize: 10, IncludeFiles: true})
	if err != nil || !page.HasMore || len(page.Items) != 1 {
		t.Fatalf("list dir page=%+v err=%v", page, err)
	}
	stat, err := client.StatPath(context.Background(), StatPathRequest{AgentID: "agent-1", Path: "/workspace/docs/a.md"})
	if err != nil || stat.SizeBytes != 5 {
		t.Fatalf("stat path info=%+v err=%v", stat, err)
	}
	exported, err := client.ExportFile(context.Background(), ExportFileRequest{AgentID: "agent-1", Path: "/workspace/docs/a.md", ExpectedVersion: "10:5"})
	if err != nil || exported.ContentURI != "scan-temp://token" {
		t.Fatalf("export file=%+v err=%v", exported, err)
	}
	if validateReq.AgentID != "agent-1" || validateReq.UserID != "user-1" {
		t.Fatalf("validate request was not mapped: %+v", validateReq)
	}
	if rootsReq.AgentID != "agent-1" || rootsReq.UserID != "user-1" {
		t.Fatalf("roots request was not mapped: %+v", rootsReq)
	}
	if listReq.Cursor != "1" || !listReq.IncludeFiles {
		t.Fatalf("list request was not mapped: %+v", listReq)
	}
	if exportReq.ExpectedVersion != "10:5" {
		t.Fatalf("export request was not mapped: %+v", exportReq)
	}
}

func TestHTTPAgentClientMapsPathDenied(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return agentJSONResponse(t, http.StatusForbidden, map[string]string{"code": "path_denied", "message": "denied"}), nil
	})

	client := newHTTPAgentTestClient(t, "http://agent.test", transport)
	_, err := client.ValidatePath(context.Background(), ValidatePathRequest{AgentID: "agent-1", Path: "/private", UserID: "user-1"})
	assertLocalErrorCode(t, err, connector.ErrorCodePermissionDenied)
}

func newHTTPAgentTestClient(t *testing.T, baseURL string, transport http.RoundTripper) *HTTPAgentClient {
	t.Helper()
	client, err := NewHTTPAgentClient(baseURL, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("new http agent client: %v", err)
	}
	return client
}

func decodeAgentJSON(t *testing.T, r *http.Request, out any) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Fatalf("expected POST, got %s", r.Method)
	}
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func agentJSONResponse(t *testing.T, status int, payload any) *http.Response {
	t.Helper()
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(&body),
	}
}
