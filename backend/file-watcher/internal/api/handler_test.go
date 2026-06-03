package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internal "github.com/lazymind/file_watcher/internal"
	"github.com/lazymind/file_watcher/internal/fs"
)

func TestLegacyFSHandlersAreDisabled(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "release-root")
	mustMkdir(t, root)
	handler := NewHandler(nil, fs.NewPathValidator([]string{root}), nil, fs.NewPathMapper("", nil), nil)

	cases := []struct {
		name   string
		path   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{name: "browse", path: "/api/v1/fs/browse", handle: handler.Browse},
		{name: "tree", path: "/api/v1/fs/tree", handle: handler.Tree},
		{name: "validate", path: "/api/v1/fs/validate", handle: handler.ValidatePath},
		{name: "stat", path: "/api/v1/fs/stat", handle: handler.StatFile},
		{name: "stage", path: "/api/v1/fs/stage", handle: handler.StageFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"path":`+quoteJSON(root)+`}`))
			w := httptest.NewRecorder()
			tc.handle(w, req)
			if w.Code != http.StatusGone {
				t.Fatalf("expected 410 status, got %d: %s", w.Code, w.Body.String())
			}
			var resp internal.ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response failed: %v", err)
			}
			if resp.Code != "LEGACY_DISABLED" {
				t.Fatalf("expected LEGACY_DISABLED, got %+v", resp)
			}
		})
	}
}

func TestAgentFSProtocolValidateListStatExport(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "watch")
	mustMkdir(t, root)
	mustMkdir(t, filepath.Join(root, "docs"))
	filePath := filepath.Join(root, "docs", "a.md")
	mustWriteFile(t, filePath, "hello")
	mustWriteFile(t, filepath.Join(root, "docs", ".hidden.swp"), "ignored")

	staging := &apiStagingStub{}
	handler := NewHandler(nil, fs.NewPathValidator([]string{root}), staging, fs.NewPathMapper("", nil), nil)

	validateReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fs/validate", strings.NewReader(`{"agent_id":"agent-1","path":`+quoteJSON(filepath.Join(root, "docs"))+`,"user_id":"user-1"}`))
	validateW := httptest.NewRecorder()
	handler.AgentValidatePath(validateW, validateReq)
	if validateW.Code != http.StatusOK {
		t.Fatalf("validate status = %d body = %s", validateW.Code, validateW.Body.String())
	}
	var validateResp agentFSPathInfo
	if err := json.NewDecoder(validateW.Body).Decode(&validateResp); err != nil {
		t.Fatalf("decode validate: %v", err)
	}
	if !validateResp.Exists || !validateResp.Readable || !validateResp.IsDir || validateResp.NormalizedPath == "" || validateResp.DisplayName != "docs" {
		t.Fatalf("unexpected validate response: %+v", validateResp)
	}

	listReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fs/list", strings.NewReader(`{"agent_id":"agent-1","path":`+quoteJSON(filepath.Join(root, "docs"))+`,"page_size":10,"include_files":true}`))
	listW := httptest.NewRecorder()
	handler.AgentListDir(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listW.Code, listW.Body.String())
	}
	var listResp agentFSListResponse
	if err := json.NewDecoder(listW.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].Name != "a.md" || listResp.Items[0].SizeBytes != 5 || listResp.Items[0].MTimeUnixNano == 0 {
		t.Fatalf("unexpected list response: %+v", listResp)
	}

	statReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fs/stat", strings.NewReader(`{"agent_id":"agent-1","path":`+quoteJSON(filePath)+`}`))
	statW := httptest.NewRecorder()
	handler.AgentStatPath(statW, statReq)
	if statW.Code != http.StatusOK {
		t.Fatalf("stat status = %d body = %s", statW.Code, statW.Body.String())
	}
	var statResp agentFSPathInfo
	if err := json.NewDecoder(statW.Body).Decode(&statResp); err != nil {
		t.Fatalf("decode stat: %v", err)
	}
	version := agentVersion(statResp.MTimeUnixNano, statResp.SizeBytes)

	exportReq := httptest.NewRequest(http.MethodPost, "/api/v1/agents/fs/export", strings.NewReader(`{"agent_id":"agent-1","path":`+quoteJSON(filePath)+`,"expected_version":`+quoteJSON(version)+`}`))
	exportW := httptest.NewRecorder()
	handler.AgentExportFile(exportW, exportReq)
	if exportW.Code != http.StatusOK {
		t.Fatalf("export status = %d body = %s", exportW.Code, exportW.Body.String())
	}
	var exportResp agentFSExportResponse
	if err := json.NewDecoder(exportW.Body).Decode(&exportResp); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if exportResp.ContentURI != "scan-temp://a.md" || exportResp.CleanupToken != "scan-temp://a.md" || exportResp.SizeBytes != 5 || exportResp.MTimeUnixNano != statResp.MTimeUnixNano {
		t.Fatalf("unexpected export response: %+v", exportResp)
	}
	if staging.sourceID == "agent-1" || staging.srcPath != filePath {
		t.Fatalf("export should isolate staging source id and pass runtime path, staging=%+v", staging)
	}
}

func TestAgentFSProtocolRequiresAgentID(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	handler := NewHandler(nil, fs.NewPathValidator([]string{root}), &apiStagingStub{}, fs.NewPathMapper("", nil), nil)

	cases := []struct {
		name   string
		path   string
		body   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{name: "validate", path: "/api/v1/agents/fs/validate", body: `{"path":` + quoteJSON(root) + `}`, handle: handler.AgentValidatePath},
		{name: "list", path: "/api/v1/agents/fs/list", body: `{"path":` + quoteJSON(root) + `}`, handle: handler.AgentListDir},
		{name: "stat", path: "/api/v1/agents/fs/stat", body: `{"path":` + quoteJSON(root) + `}`, handle: handler.AgentStatPath},
		{name: "export", path: "/api/v1/agents/fs/export", body: `{"path":` + quoteJSON(filepath.Join(root, "a.md")) + `}`, handle: handler.AgentExportFile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			tc.handle(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			var resp internal.ErrorResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response failed: %v", err)
			}
			if resp.Code != agentErrInvalidArgument {
				t.Fatalf("expected INVALID_ARGUMENT, got %+v", resp)
			}
		})
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s failed: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s failed: %v", path, err)
	}
}

func quoteJSON(s string) string {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

type apiStagingStub struct {
	sourceID string
	srcPath  string
}

func (s *apiStagingStub) StageFile(_ context.Context, sourceID, _, _, srcPath string) (internal.StageResult, error) {
	s.sourceID = sourceID
	s.srcPath = srcPath
	return internal.StageResult{
		URI:  "scan-temp://" + filepath.Base(srcPath),
		Size: 5,
	}, nil
}
