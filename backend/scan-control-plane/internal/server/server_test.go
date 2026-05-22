package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/lazymind/scan_control_plane/internal/cloudsync/authclient"
	cloudprovider "github.com/lazymind/scan_control_plane/internal/cloudsync/provider"
	"github.com/lazymind/scan_control_plane/internal/coreclient"
	"github.com/lazymind/scan_control_plane/internal/model"
	"github.com/lazymind/scan_control_plane/internal/sourcelayout"
	"github.com/lazymind/scan_control_plane/internal/store"
)

func newServerTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cp.db")
	st, err := store.New("sqlite", dbPath, 10*time.Second, zap.NewNop())
	if err != nil {
		t.Fatalf("new store failed: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func markNextDueParseSucceededForTest(t *testing.T, st *store.Store, ctx context.Context, now time.Time, coreDocumentID string) {
	t.Helper()
	dueAt := now.UTC().Add(48 * time.Hour)
	if _, err := st.ScheduleDueParses(ctx, dueAt); err != nil {
		t.Fatalf("schedule due parses failed: %v", err)
	}
	tasks, err := st.ClaimDueTasks(ctx, "test-worker", dueAt, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim due tasks failed: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatalf("expected at least one due parse task")
	}
	task := tasks[0]
	if err := st.MarkTaskSubmitted(ctx, task.TaskID, "dataset-test", coreDocumentID, "core-task-"+coreDocumentID, dueAt); err != nil {
		t.Fatalf("mark task submitted failed: %v", err)
	}
	if err := st.MarkTaskSucceeded(ctx, task.TaskID, task.DocumentID, task.TargetVersionID); err != nil {
		t.Fatalf("mark task succeeded failed: %v", err)
	}
}

func TestCreateSourceRejectsDuplicateLocalRootPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := newServerTestStore(t)
	h := &Handler{store: st, core: coreclient.NewNoop(), log: zap.NewNop()}

	body := `{"tenant_id":"tenant-1","name":"src-1","agent_id":"agent-1","root_path":"/tmp/watch","idle_window_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources", strings.NewReader(body))
	req.Header.Set("X-User-Id", "user-1")
	w := httptest.NewRecorder()
	h.createSource(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected first create status 200, got %d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/scan/sources", strings.NewReader(body))
	req.Header.Set("X-User-Id", "user-1")
	w = httptest.NewRecorder()
	h.createSource(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected duplicate create status 409, got %d body=%s", w.Code, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode duplicate response failed: %v", err)
	}
	if resp.Code != "SOURCE_ALREADY_EXISTS" {
		t.Fatalf("expected SOURCE_ALREADY_EXISTS, got %+v", resp)
	}

	var sources []model.Source
	sources, err := st.ListSources(ctx, "tenant-1", "user-1")
	if err != nil {
		t.Fatalf("list sources failed: %v", err)
	}
	if len(sources) != 1 || sources[0].Name != "src-1" {
		t.Fatalf("expected original source only, got %+v", sources)
	}
}

func TestUpdateSourceRejectsDuplicateLocalRootPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := newServerTestStore(t)
	h := &Handler{store: st, core: coreclient.NewNoop(), log: zap.NewNop()}
	first, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		CreateUserID:      "user-1",
		Name:              "src-1",
		AgentID:           "agent-1",
		RootPath:          "/tmp/watch-a",
		IdleWindowSeconds: 300,
	})
	if err != nil {
		t.Fatalf("create first source failed: %v", err)
	}
	second, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		CreateUserID:      "user-1",
		Name:              "src-2",
		AgentID:           "agent-1",
		RootPath:          "/tmp/watch-b",
		IdleWindowSeconds: 300,
	})
	if err != nil {
		t.Fatalf("create second source failed: %v", err)
	}

	body := fmt.Sprintf(`{"root_path":%q}`, first.RootPath)
	req := httptest.NewRequest(http.MethodPut, "/api/scan/sources/"+second.ID, strings.NewReader(body))
	req.SetPathValue("id", second.ID)
	w := httptest.NewRecorder()
	h.updateSource(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected duplicate update status 409, got %d body=%s", w.Code, w.Body.String())
	}
	var resp model.ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode duplicate response failed: %v", err)
	}
	if resp.Code != "SOURCE_ALREADY_EXISTS" {
		t.Fatalf("expected SOURCE_ALREADY_EXISTS, got %+v", resp)
	}
}

func TestFetchTreeFileStatsRunsInParallel(t *testing.T) {
	t.Parallel()

	var inFlight int64
	var maxInFlight int64
	var ts *httptest.Server
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("skip: httptest listener not available in current sandbox: %v", r)
			}
		}()
		ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/fs/stat" {
				http.NotFound(w, r)
				return
			}
			var req struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			current := atomic.AddInt64(&inFlight, 1)
			for {
				prev := atomic.LoadInt64(&maxInFlight)
				if current <= prev {
					break
				}
				if atomic.CompareAndSwapInt64(&maxInFlight, prev, current) {
					break
				}
			}
			defer atomic.AddInt64(&inFlight, -1)
			time.Sleep(50 * time.Millisecond)

			_ = json.NewEncoder(w).Encode(map[string]any{
				"path":     req.Path,
				"size":     123,
				"mod_time": time.Now().UTC(),
				"is_dir":   false,
				"checksum": "sha1",
			})
		}))
	}()
	if ts == nil {
		return
	}
	defer ts.Close()

	h := &Handler{
		client: &http.Client{Timeout: 2 * time.Second},
		log:    zap.NewNop(),
	}

	items := []model.TreeNode{
		{Key: "/tmp/watch/a.txt", IsDir: false},
		{Key: "/tmp/watch/b.txt", IsDir: false},
		{Key: "/tmp/watch/c.txt", IsDir: false},
		{Key: "/tmp/watch/d.txt", IsDir: false},
		{Key: "/tmp/watch/e.txt", IsDir: false},
		{Key: "/tmp/watch/f.txt", IsDir: false},
	}

	stats, err := h.fetchTreeFileStats(context.Background(), ts.URL, items)
	if err != nil {
		t.Fatalf("fetchTreeFileStats failed: %v", err)
	}
	if len(stats) != len(items) {
		t.Fatalf("expected %d stats, got %d", len(items), len(stats))
	}
	if atomic.LoadInt64(&maxInFlight) <= 1 {
		t.Fatalf("expected concurrent fs/stat calls, max in-flight=%d", atomic.LoadInt64(&maxInFlight))
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/decode", strings.NewReader(`{"agent_id":"a1","tenant_id":"t1","unknown":"x"}`))
	w := httptest.NewRecorder()
	var out model.PullCommandsRequest
	if ok := decodeJSON(w, req, &out); ok {
		t.Fatalf("expected decodeJSON to reject unknown fields")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 status, got %d", w.Code)
	}
}

func TestDecodeJSONRejectsMultipleJSONValues(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/decode", strings.NewReader(`{"agent_id":"a1","tenant_id":"t1"} {"x":1}`))
	w := httptest.NewRecorder()
	var out model.PullCommandsRequest
	if ok := decodeJSON(w, req, &out); ok {
		t.Fatalf("expected decodeJSON to reject multiple JSON payloads")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 status, got %d", w.Code)
	}
}

type applyingScanResultMerger struct {
	st     *store.Store
	events []model.FileEvent
	err    error
}

func (m *applyingScanResultMerger) Ingest(events []model.FileEvent) {
	m.events = append(m.events, events...)
	mutations, err := m.st.BuildMutationsFromEvents(context.Background(), events)
	if err != nil {
		m.err = err
		return
	}
	m.err = m.st.BatchApplyDocumentMutations(context.Background(), mutations)
}

func TestReportScanResultsPersistsMetadataWhenMergerEnabled(t *testing.T) {
	t.Parallel()

	st := newServerTestStore(t)
	ctx := context.Background()
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:          "tenant-1",
		Name:              "src-scan-result-metadata",
		RootPath:          "/tmp/watch",
		AgentID:           "agent-1",
		WatchEnabled:      true,
		IdleWindowSeconds: 10,
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}

	merger := &applyingScanResultMerger{st: st}
	h := &Handler{store: st, merger: merger, core: coreclient.NewNoop(), log: zap.NewNop()}
	path := "/tmp/watch/server.txt"
	modAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	payload, err := json.Marshal(model.ReportScanResultsRequest{
		SourceID: src.ID,
		Mode:     "full",
		Records: []model.ScanRecord{
			{Path: path, Size: 2048, ModTime: modAt},
		},
	})
	if err != nil {
		t.Fatalf("marshal scan result request failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/scan-results", strings.NewReader(string(payload)))
	w := httptest.NewRecorder()
	h.reportScanResults(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	if merger.err != nil {
		t.Fatalf("merger apply failed: %v", merger.err)
	}
	if len(merger.events) != 1 || merger.events[0].SourceID != src.ID {
		t.Fatalf("expected scan result event to use request source_id fallback, got %#v", merger.events)
	}
	markNextDueParseSucceededForTest(t, st, ctx, time.Now().UTC(), "core-doc-server")

	resp, err := st.ListSourceDocuments(ctx, src.ID, model.ListSourceDocumentsRequest{
		TenantID: src.TenantID,
		Page:     1,
		PageSize: 20,
	})
	if err != nil {
		t.Fatalf("list source documents failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 document, got %d", len(resp.Items))
	}
	if resp.Items[0].SizeBytes != 2048 {
		t.Fatalf("expected size_bytes=2048 from merger scan metadata, got %d", resp.Items[0].SizeBytes)
	}
	if resp.Summary.StorageBytes != 2048 {
		t.Fatalf("expected storage_bytes=2048 from merger scan metadata, got %d", resp.Summary.StorageBytes)
	}
	if resp.Items[0].SourceUpdatedAt == nil || !resp.Items[0].SourceUpdatedAt.Equal(modAt) {
		t.Fatalf("expected source_updated_at=%v, got %v", modAt, resp.Items[0].SourceUpdatedAt)
	}
}

func TestOpenAPISpecHidesAgentFSCompatAliases(t *testing.T) {
	t.Parallel()

	spec := buildOpenAPISpec()
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected OpenAPI paths map, got %#v", spec["paths"])
	}

	for _, path := range []string{
		"/api/scan/agents/fs/tree",
		"/api/scan/agents/fs/validate",
	} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("expected canonical path %s in OpenAPI spec", path)
		}
	}
	for _, path := range []string{
		"/api/v1/agents/fs/tree",
		"/api/v1/agents/fs/validate",
	} {
		if _, ok := paths[path]; ok {
			t.Fatalf("compat alias %s should not be exposed in OpenAPI spec", path)
		}
	}
}

type fakeKnowledgeBaseCore struct {
	createResult    coreclient.CreateKnowledgeBaseResult
	createErr       error
	foundKB         coreclient.KnowledgeBaseRef
	found           bool
	findErr         error
	deleteDatasetID string
	deleteUserID    string
	deleteUserName  string
	deleteErr       error
	searchCalls     []fakeSearchCall
	searchStates    map[string]coreclient.TaskState
}

type fakeSearchCall struct {
	datasetID string
	taskIDs   []string
	userID    string
	userName  string
}

func (f *fakeKnowledgeBaseCore) Enabled() bool { return true }

func (f *fakeKnowledgeBaseCore) SubmitParseTask(context.Context, store.PendingTask, string, string, int64) (coreclient.SubmitResult, error) {
	return coreclient.SubmitResult{}, nil
}

func (f *fakeKnowledgeBaseCore) CreateKnowledgeBase(context.Context, coreclient.CreateKnowledgeBaseRequest) (coreclient.CreateKnowledgeBaseResult, error) {
	return f.createResult, f.createErr
}

func (f *fakeKnowledgeBaseCore) FindKnowledgeBaseByName(context.Context, string, string, string) (coreclient.KnowledgeBaseRef, bool, error) {
	return f.foundKB, f.found, f.findErr
}

func (f *fakeKnowledgeBaseCore) DeleteDataset(_ context.Context, datasetID, userID, userName string) error {
	f.deleteDatasetID = datasetID
	f.deleteUserID = userID
	f.deleteUserName = userName
	return f.deleteErr
}

func (f *fakeKnowledgeBaseCore) SearchTasks(context.Context, []string) (map[string]coreclient.TaskState, error) {
	return map[string]coreclient.TaskState{}, nil
}

func (f *fakeKnowledgeBaseCore) SearchTasksByDataset(context.Context, string, []string) (map[string]coreclient.TaskState, error) {
	return map[string]coreclient.TaskState{}, nil
}

func (f *fakeKnowledgeBaseCore) SearchTasksByDatasetAs(_ context.Context, datasetID string, taskIDs []string, userID string, userName string) (map[string]coreclient.TaskState, error) {
	f.searchCalls = append(f.searchCalls, fakeSearchCall{
		datasetID: datasetID,
		taskIDs:   append([]string(nil), taskIDs...),
		userID:    userID,
		userName:  userName,
	})
	if f.searchStates != nil {
		return f.searchStates, nil
	}
	return map[string]coreclient.TaskState{}, nil
}

type fakeCloudProvider struct {
	validateErr error
	validateReq cloudprovider.ListRequest
	objects     []cloudprovider.RemoteObject
}

func (f *fakeCloudProvider) Name() string { return "feishu" }

func (f *fakeCloudProvider) ListObjects(context.Context, cloudprovider.ListRequest) ([]cloudprovider.RemoteObject, error) {
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	return append([]cloudprovider.RemoteObject(nil), f.objects...), nil
}

func (f *fakeCloudProvider) DownloadObject(context.Context, string, cloudprovider.RemoteObject) ([]byte, error) {
	return nil, nil
}

func (f *fakeCloudProvider) ValidateTarget(_ context.Context, req cloudprovider.ListRequest) error {
	f.validateReq = req
	return f.validateErr
}

type fakeCloudAuth struct {
	accessToken string
	err         error
}

func (f fakeCloudAuth) GetAccessToken(context.Context, string) (authclient.TokenResponse, error) {
	if f.err != nil {
		return authclient.TokenResponse{}, f.err
	}
	return authclient.TokenResponse{
		ConnectionID: "conn-1",
		Provider:     "feishu",
		AccessToken:  f.accessToken,
		Status:       "ACTIVE",
	}, nil
}

func boolPtr(v bool) *bool {
	return &v
}

func findTreeNodeByPath(items []model.TreeNode, path string) (model.TreeNode, bool) {
	for _, item := range items {
		if item.Key == path {
			return item, true
		}
		if len(item.Children) > 0 {
			if found, ok := findTreeNodeByPath(item.Children, path); ok {
				return found, true
			}
		}
	}
	return model.TreeNode{}, false
}

func TestValidateCloudTargetEndpointUsesProvider(t *testing.T) {
	provider := &fakeCloudProvider{}
	h := &Handler{
		cloudAuth:      fakeCloudAuth{accessToken: "access-token-1"},
		cloudProviders: map[string]cloudprovider.Provider{"feishu": provider},
		log:            zap.NewNop(),
	}

	body := `{"provider":"feishu","auth_connection_id":"conn-1","target_type":"wiki_space","target_ref":"space-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/cloud/target/validate", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.validateCloudTarget(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	if provider.validateReq.AccessToken != "access-token-1" || provider.validateReq.TargetRef != "space-1" {
		t.Fatalf("unexpected validation request: %#v", provider.validateReq)
	}
}

func TestBuildCloudTreeBySourceLiveFoldsWikiPageWithChildren(t *testing.T) {
	ctx := context.Background()
	st := newServerTestStore(t)
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		Name:                  "feishu wiki",
		RootPath:              "/tmp/live-feishu-source",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if _, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn-1",
		TargetType:       "wiki_space",
		TargetRef:        "space-1",
	}); err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	provider := &fakeCloudProvider{
		objects: []cloudprovider.RemoteObject{
			{
				ExternalObjectID: "node_test2",
				ExternalPath:     "test2",
				ExternalName:     "test2",
				ExternalKind:     "docx",
				ExternalVersion:  "rev-test2",
				ProviderMeta:     map[string]any{"has_child": true},
			},
			{
				ExternalObjectID: "node_test222",
				ExternalParentID: "node_test2",
				ExternalPath:     "test2/test222",
				ExternalName:     "test222",
				ExternalKind:     "docx",
				ExternalVersion:  "rev-test222",
			},
		},
	}
	h := &Handler{
		store:          st,
		cloudAuth:      fakeCloudAuth{accessToken: "access-token-1"},
		cloudProviders: map[string]cloudprovider.Provider{"feishu": provider},
		log:            zap.NewNop(),
	}
	mirrorRoot := sourcelayout.CloudMirrorRoot(src.RootPath)
	items, fileStats, err := h.buildCloudTreeBySourceLive(ctx, src, src.ID, mirrorRoot, 8, true)
	if err != nil {
		t.Fatalf("build live cloud tree failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one root wiki node, got %+v", items)
	}
	root := items[0]
	if root.IsDir || root.Key != filepath.Join(mirrorRoot, "test2") || root.Title != "test2" || root.ExternalFileID != "node_test2" {
		t.Fatalf("expected selectable parent wiki page node, got %+v", root)
	}
	if _, ok := findTreeNodeByPath(root.Children, filepath.Join(mirrorRoot, "test2", "test2.md")); ok {
		t.Fatalf("did not expect mirrored parent page file as visible child")
	}
	if _, ok := findTreeNodeByPath(root.Children, filepath.Join(mirrorRoot, "test2", "test222.md")); !ok {
		t.Fatalf("expected child wiki page under parent page, got %+v", root.Children)
	}
	if _, ok := fileStats[filepath.Join(mirrorRoot, "test2")]; !ok {
		t.Fatalf("expected file stat keyed by tree path, got %+v", fileStats)
	}
	if _, ok := fileStats[filepath.Join(mirrorRoot, "test2", "test2.md")]; ok {
		t.Fatalf("did not expect file stat keyed by mirrored parent page file")
	}
}

func TestBuildCloudTreeBySourceLiveKeepsWikiTreeStableFromExistingIndex(t *testing.T) {
	ctx := context.Background()
	st := newServerTestStore(t)
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		Name:                  "feishu wiki",
		RootPath:              "/tmp/live-feishu-source-existing",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if _, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn-1",
		TargetType:       "wiki_space",
		TargetRef:        "space-1",
	}); err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	mirrorRoot := sourcelayout.CloudMirrorRoot(src.RootPath)
	now := time.Now().UTC()
	if err := st.UpsertCloudObjectIndexBatch(ctx, src.ID, "feishu", []store.CloudObjectIndexRecord{
		{
			ExternalObjectID: "node_test4",
			ExternalName:     "test4",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-test4",
			LocalRelPath:     "test4/test4.md",
			LocalAbsPath:     filepath.Join(mirrorRoot, "test4", "test4.md"),
			Checksum:         "checksum-test4",
			SizeBytes:        6,
			ProviderMeta:     map[string]any{"has_child": true},
		},
		{
			ExternalObjectID: "node_11111",
			ExternalParentID: "node_test4",
			ExternalName:     "11111",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-11111",
			LocalRelPath:     "test4/11111/11111.md",
			LocalAbsPath:     filepath.Join(mirrorRoot, "test4", "11111", "11111.md"),
			Checksum:         "checksum-11111",
			SizeBytes:        14,
			ProviderMeta:     map[string]any{"has_child": true},
		},
		{
			ExternalObjectID: "node_33333",
			ExternalParentID: "node_11111",
			ExternalName:     "33333",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-33333",
			LocalRelPath:     "test4/11111/33333.md",
			LocalAbsPath:     filepath.Join(mirrorRoot, "test4", "11111", "33333.md"),
			Checksum:         "checksum-33333",
			SizeBytes:        13,
		},
		{
			ExternalObjectID: "node_222222",
			ExternalParentID: "node_test4",
			ExternalName:     "222222",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-222222",
			LocalRelPath:     "test4/222222.md",
			LocalAbsPath:     filepath.Join(mirrorRoot, "test4", "222222.md"),
			Checksum:         "checksum-222222",
			SizeBytes:        15,
		},
	}, now); err != nil {
		t.Fatalf("seed cloud object index failed: %v", err)
	}

	provider := &fakeCloudProvider{
		objects: []cloudprovider.RemoteObject{
			{ExternalObjectID: "node_test4", ExternalPath: "test4", ExternalName: "test4", ExternalKind: "docx", ExternalVersion: "rev-test4"},
			{ExternalObjectID: "node_11111", ExternalParentID: "node_test4", ExternalPath: "test4/11111", ExternalName: "11111", ExternalKind: "docx", ExternalVersion: "rev-11111"},
			{ExternalObjectID: "node_33333", ExternalParentID: "node_11111", ExternalPath: "test4/11111/33333", ExternalName: "33333", ExternalKind: "docx", ExternalVersion: "rev-33333"},
			{ExternalObjectID: "node_222222", ExternalParentID: "node_test4", ExternalPath: "test4/222222", ExternalName: "222222", ExternalKind: "docx", ExternalVersion: "rev-222222"},
		},
	}
	h := &Handler{
		store:          st,
		cloudAuth:      fakeCloudAuth{accessToken: "access-token-1"},
		cloudProviders: map[string]cloudprovider.Provider{"feishu": provider},
		log:            zap.NewNop(),
	}

	items, fileStats, err := h.buildCloudTreeBySourceLive(ctx, src, src.ID, mirrorRoot, 8, true)
	if err != nil {
		t.Fatalf("build live cloud tree failed: %v", err)
	}
	root, ok := findTreeNodeByPath(items, filepath.Join(mirrorRoot, "test4"))
	if !ok || root.IsDir {
		t.Fatalf("expected selectable test4 wiki page, got %+v in %+v", root, items)
	}
	child, ok := findTreeNodeByPath(root.Children, filepath.Join(mirrorRoot, "test4", "11111"))
	if !ok || child.IsDir {
		t.Fatalf("expected selectable 11111 child page, got %+v under %+v", child, root.Children)
	}
	if _, ok := findTreeNodeByPath(root.Children, filepath.Join(mirrorRoot, "test4", "test4.md")); ok {
		t.Fatalf("did not expect parent page mirror file as visible child")
	}
	if _, ok := findTreeNodeByPath(child.Children, filepath.Join(mirrorRoot, "test4", "11111", "11111.md")); ok {
		t.Fatalf("did not expect nested parent page mirror file as visible child")
	}
	if _, ok := findTreeNodeByPath(child.Children, filepath.Join(mirrorRoot, "test4", "11111", "33333.md")); !ok {
		t.Fatalf("expected 33333 leaf page under 11111, got %+v", child.Children)
	}
	if _, ok := findTreeNodeByPath(root.Children, filepath.Join(mirrorRoot, "test4", "222222.md")); !ok {
		t.Fatalf("expected 222222 leaf page under test4, got %+v", root.Children)
	}
	if _, ok := fileStats[filepath.Join(mirrorRoot, "test4")]; !ok {
		t.Fatalf("expected test4 display path stat, got %+v", fileStats)
	}
	if _, ok := fileStats[filepath.Join(mirrorRoot, "test4", "11111")]; !ok {
		t.Fatalf("expected 11111 display path stat, got %+v", fileStats)
	}
}

func TestPathTreeByAgentKeywordKeepsWikiDisplayPathAndDiffState(t *testing.T) {
	ctx := context.Background()
	st := newServerTestStore(t)
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		Name:                  "handpull-feishu-wiki",
		RootPath:              "/tmp/keyword-feishu-wiki-source",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if _, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn-1",
		TargetType:       "wiki_space",
		TargetRef:        "space-1",
	}); err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	mirrorRoot := filepath.Clean(sourcelayout.CloudMirrorRoot(src.RootPath))
	rootDisplayPath := filepath.Join(mirrorRoot, "handpull-feishu-wiki")
	rootObjectPath := filepath.Join(rootDisplayPath, "handpull-feishu-wiki.md")
	childDisplayPath := filepath.Join(rootDisplayPath, "11111")
	childObjectPath := filepath.Join(childDisplayPath, "11111.md")
	now := time.Now().UTC().Add(-time.Minute)
	if err := st.UpsertCloudObjectIndexBatch(ctx, src.ID, "feishu", []store.CloudObjectIndexRecord{
		{
			ExternalObjectID: "node_root",
			ExternalName:     "handpull-feishu-wiki",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-root",
			LocalRelPath:     "handpull-feishu-wiki/handpull-feishu-wiki.md",
			LocalAbsPath:     rootObjectPath,
			Checksum:         "rev-root",
			SizeBytes:        10,
			ProviderMeta:     map[string]any{"has_child": true},
		},
		{
			ExternalObjectID: "node_11111",
			ExternalParentID: "node_root",
			ExternalName:     "11111",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-11111",
			LocalRelPath:     "handpull-feishu-wiki/11111/11111.md",
			LocalAbsPath:     childObjectPath,
			Checksum:         "rev-11111",
			SizeBytes:        14,
			ProviderMeta:     map[string]any{"has_child": true},
		},
	}, now); err != nil {
		t.Fatalf("seed cloud object index failed: %v", err)
	}
	if err := st.PersistScanResultSnapshotMetadata(ctx, model.ReportScanResultsRequest{
		AgentID:  "agent-1",
		SourceID: src.ID,
		Mode:     "reconcile",
		Records: []model.ScanRecord{
			{SourceID: src.ID, Path: rootObjectPath, Size: 10, ModTime: now, Checksum: "rev-root"},
			{SourceID: src.ID, Path: childObjectPath, Size: 14, ModTime: now, Checksum: "rev-11111"},
		},
	}); err != nil {
		t.Fatalf("seed committed snapshot failed: %v", err)
	}

	provider := &fakeCloudProvider{
		objects: []cloudprovider.RemoteObject{
			{ExternalObjectID: "node_root", ExternalPath: "handpull-feishu-wiki", ExternalName: "handpull-feishu-wiki", ExternalKind: "docx", ExternalVersion: "rev-root"},
			{ExternalObjectID: "node_11111", ExternalParentID: "node_root", ExternalPath: "handpull-feishu-wiki/11111", ExternalName: "11111", ExternalKind: "docx", ExternalVersion: "rev-11111"},
		},
	}
	h := &Handler{
		store:          st,
		cloudAuth:      fakeCloudAuth{accessToken: "access-token-1"},
		cloudProviders: map[string]cloudprovider.Provider{"feishu": provider},
		log:            zap.NewNop(),
	}
	body := fmt.Sprintf(`{"source_id":%q,"path":%q,"keyword":"11111","include_files":true,"max_depth":8}`, src.ID, sourcelayout.CloudPublicRoot(src.ID))
	req := httptest.NewRequest(http.MethodPost, "/api/scan/agents/fs/tree", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.pathTreeByAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 status, got %d: %s", w.Code, w.Body.String())
	}
	var resp model.AgentPathTreeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode tree response failed: %v", err)
	}
	root, ok := findTreeNodeByPath(resp.Items, rootDisplayPath)
	if !ok {
		t.Fatalf("expected keyword response to keep wiki root %s, got %+v", rootDisplayPath, resp.Items)
	}
	child, ok := findTreeNodeByPath(root.Children, childDisplayPath)
	if !ok {
		t.Fatalf("expected keyword response to keep wiki child display path %s, got %+v", childDisplayPath, root.Children)
	}
	if child.Title != "11111" || child.Key != childDisplayPath || child.IsDir {
		t.Fatalf("expected folded selectable wiki child, got %+v", child)
	}
	if _, ok := findTreeNodeByPath(root.Children, childObjectPath); ok {
		t.Fatalf("did not expect keyword response to expose mirror object path %s", childObjectPath)
	}
	if child.UpdateType != "UNCHANGED" || child.HasUpdate == nil || *child.HasUpdate {
		t.Fatalf("expected keyword response child to stay unchanged, got %+v", child)
	}
}

func TestBuildCloudTreeBySourceLiveMarksMissingIndexDeleted(t *testing.T) {
	ctx := context.Background()
	st := newServerTestStore(t)
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		Name:                  "feishu drive",
		RootPath:              "/tmp/live-feishu-delete-index",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if _, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          boolPtr(true),
		AuthConnectionID: "conn-1",
		TargetType:       "drive_folder",
		TargetRef:        "folder-1",
	}); err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	mirrorRoot := sourcelayout.CloudMirrorRoot(src.RootPath)
	now := time.Now().UTC()
	if err := st.UpsertCloudObjectIndexBatch(ctx, src.ID, "feishu", []store.CloudObjectIndexRecord{
		{
			ExternalObjectID: "obj_existing",
			ExternalName:     "existing",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-1",
			LocalRelPath:     "existing.md",
			LocalAbsPath:     filepath.Join(mirrorRoot, "existing.md"),
			Checksum:         "rev-1",
			SizeBytes:        10,
		},
		{
			ExternalObjectID: "obj_deleted",
			ExternalName:     "deleted",
			ExternalKind:     "docx",
			ExternalVersion:  "rev-old",
			LocalRelPath:     "deleted.md",
			LocalAbsPath:     filepath.Join(mirrorRoot, "deleted.md"),
			Checksum:         "rev-old",
			SizeBytes:        20,
		},
	}, now); err != nil {
		t.Fatalf("seed cloud object index failed: %v", err)
	}
	provider := &fakeCloudProvider{
		objects: []cloudprovider.RemoteObject{
			{ExternalObjectID: "obj_existing", ExternalPath: "existing", ExternalName: "existing", ExternalKind: "docx", ExternalVersion: "rev-1"},
		},
	}
	h := &Handler{
		store:          st,
		cloudAuth:      fakeCloudAuth{accessToken: "access-token-1"},
		cloudProviders: map[string]cloudprovider.Provider{"feishu": provider},
		log:            zap.NewNop(),
	}

	if _, _, err := h.buildCloudTreeBySourceLive(ctx, src, src.ID, mirrorRoot, 8, true); err != nil {
		t.Fatalf("build live cloud tree failed: %v", err)
	}
	rows, err := st.ListCloudObjectIndex(ctx, src.ID)
	if err != nil {
		t.Fatalf("list cloud object index failed: %v", err)
	}
	deletedByID := map[string]bool{}
	for _, row := range rows {
		deletedByID[row.ExternalObjectID] = row.IsDeleted
	}
	if deletedByID["obj_existing"] {
		t.Fatalf("expected existing object to remain active")
	}
	if !deletedByID["obj_deleted"] {
		t.Fatalf("expected missing object to be marked deleted, got %+v", rows)
	}
}

func TestUpsertCloudBindingValidationFailureCleansNewCloudSourceAndDataset(t *testing.T) {
	ctx := context.Background()
	st := newServerTestStore(t)
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		CreateUserID:          "user-1",
		Name:                  "bad feishu source",
		AgentID:               "agent-1",
		DatasetID:             "ds-bad-target",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	core := &fakeKnowledgeBaseCore{}
	provider := &fakeCloudProvider{validateErr: errors.New("space not found")}
	h := &Handler{
		store:          st,
		core:           core,
		cloudAuth:      fakeCloudAuth{accessToken: "access-token-1"},
		cloudProviders: map[string]cloudprovider.Provider{"feishu": provider},
		log:            zap.NewNop(),
	}

	body := `{"provider":"feishu","enabled":true,"auth_connection_id":"conn-1","target_type":"wiki_space","target_ref":"bad-space"}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources/"+src.ID+"/cloud/binding", strings.NewReader(body))
	req.SetPathValue("id", src.ID)
	req.Header.Set("X-User-Id", "user-1")
	w := httptest.NewRecorder()
	h.upsertCloudBinding(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := st.GetSource(ctx, src.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected newly-created source to be cleaned up, got err=%v", err)
	}
	if core.deleteDatasetID != "ds-bad-target" || core.deleteUserID != "user-1" {
		t.Fatalf("expected dataset cleanup as user-1, got dataset=%q user=%q", core.deleteDatasetID, core.deleteUserID)
	}
	if provider.validateReq.TargetRef != "bad-space" || provider.validateReq.AccessToken != "access-token-1" {
		t.Fatalf("unexpected validation request: %#v", provider.validateReq)
	}
}

func TestUpsertCloudBindingValidationFailureKeepsExistingBinding(t *testing.T) {
	ctx := context.Background()
	st := newServerTestStore(t)
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		CreateUserID:          "user-1",
		Name:                  "existing feishu source",
		AgentID:               "agent-1",
		DatasetID:             "ds-existing",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	enabled := true
	if _, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          &enabled,
		AuthConnectionID: "conn-1",
		TargetType:       "wiki_space",
		TargetRef:        "good-space",
	}); err != nil {
		t.Fatalf("seed cloud binding failed: %v", err)
	}
	core := &fakeKnowledgeBaseCore{}
	h := &Handler{
		store:          st,
		core:           core,
		cloudAuth:      fakeCloudAuth{accessToken: "access-token-1"},
		cloudProviders: map[string]cloudprovider.Provider{"feishu": &fakeCloudProvider{validateErr: errors.New("space not found")}},
		log:            zap.NewNop(),
	}

	body := `{"provider":"feishu","enabled":true,"auth_connection_id":"conn-1","target_type":"wiki_space","target_ref":"bad-space"}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources/"+src.ID+"/cloud/binding", strings.NewReader(body))
	req.SetPathValue("id", src.ID)
	req.Header.Set("X-User-Id", "user-1")
	w := httptest.NewRecorder()
	h.upsertCloudBinding(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := st.GetSource(ctx, src.ID); err != nil {
		t.Fatalf("existing source should not be deleted, got err=%v", err)
	}
	binding, err := st.GetCloudSourceBinding(ctx, src.ID)
	if err != nil {
		t.Fatalf("existing binding should remain: %v", err)
	}
	if binding.TargetRef != "good-space" {
		t.Fatalf("expected original binding target to remain, got %q", binding.TargetRef)
	}
	if core.deleteDatasetID != "" {
		t.Fatalf("existing dataset should not be deleted, got %q", core.deleteDatasetID)
	}
}

func TestCreateKnowledgeBaseReusesUnboundScanManagedDataset(t *testing.T) {
	t.Parallel()

	st := newServerTestStore(t)
	core := &fakeKnowledgeBaseCore{
		createErr: &coreclient.HTTPError{StatusCode: http.StatusConflict, Body: "dataset name already exists"},
		foundKB: coreclient.KnowledgeBaseRef{
			DatasetID:   "ds_scan_half_created",
			Name:        "local kb",
			ScanManaged: true,
		},
		found: true,
	}
	h := &Handler{
		store: st,
		core:  core,
		log:   zap.NewNop(),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/scan/knowledge-bases", strings.NewReader(`{"name":"local kb","algo":{"algo_id":"algo-1"}}`))
	req.Header.Set("X-User-Id", "user-1")
	w := httptest.NewRecorder()

	h.createKnowledgeBase(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp model.CreateKnowledgeBaseResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.DatasetID != "ds_scan_half_created" || resp.Name != "local kb" {
		t.Fatalf("expected reused dataset, got %#v", resp)
	}
}

func TestCreateKnowledgeBaseDoesNotReuseBoundScanManagedDataset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := newServerTestStore(t)
	if err := st.RegisterAgent(ctx, model.RegisterAgentRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
		Hostname: "test",
		Version:  "v1",
	}); err != nil {
		t.Fatalf("register agent failed: %v", err)
	}
	if _, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:     "tenant-1",
		CreateUserID: "user-1",
		Name:         "bound source",
		AgentID:      "agent-1",
		RootPath:     "/tmp/bound-source",
		DatasetID:    "ds_scan_bound",
	}); err != nil {
		t.Fatalf("create source failed: %v", err)
	}

	h := &Handler{
		store: st,
		core: &fakeKnowledgeBaseCore{
			createErr: &coreclient.HTTPError{StatusCode: http.StatusConflict, Body: "dataset name already exists"},
			foundKB: coreclient.KnowledgeBaseRef{
				DatasetID:   "ds_scan_bound",
				Name:        "local kb",
				ScanManaged: true,
			},
			found: true,
		},
		log: zap.NewNop(),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/scan/knowledge-bases", strings.NewReader(`{"name":"local kb","algo":{"algo_id":"algo-1"}}`))
	req.Header.Set("X-User-Id", "user-1")
	w := httptest.NewRecorder()

	h.createKnowledgeBase(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteSourceDeletesBoundCoreDataset(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := newServerTestStore(t)
	if err := st.RegisterAgent(ctx, model.RegisterAgentRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
		Hostname: "test",
		Version:  "v1",
	}); err != nil {
		t.Fatalf("register agent failed: %v", err)
	}
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:     "tenant-1",
		CreateUserID: "owner-1",
		Name:         "source with kb",
		AgentID:      "agent-1",
		RootPath:     "/tmp/source-with-kb",
		DatasetID:    "ds-bound",
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	core := &fakeKnowledgeBaseCore{}
	h := &Handler{store: st, core: core, log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodDelete, "/api/scan/sources/"+src.ID, nil)
	req.SetPathValue("id", src.ID)
	req.Header.Set("X-User-Id", "operator-1")
	req.Header.Set("X-User-Name", "Operator One")
	w := httptest.NewRecorder()

	h.deleteSource(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}
	if core.deleteDatasetID != "ds-bound" {
		t.Fatalf("expected bound dataset to be deleted, got %q", core.deleteDatasetID)
	}
	if core.deleteUserID != "owner-1" || core.deleteUserName != "Operator One" {
		t.Fatalf("unexpected delete user headers: user_id=%q user_name=%q", core.deleteUserID, core.deleteUserName)
	}
	if _, err := st.GetSource(ctx, src.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected source to be deleted, got err=%v", err)
	}
}

func TestDeleteSourceKeepsSourceWhenCoreDatasetDeleteFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := newServerTestStore(t)
	if err := st.RegisterAgent(ctx, model.RegisterAgentRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
		Hostname: "test",
		Version:  "v1",
	}); err != nil {
		t.Fatalf("register agent failed: %v", err)
	}
	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:     "tenant-1",
		CreateUserID: "owner-1",
		Name:         "source with failing kb delete",
		AgentID:      "agent-1",
		RootPath:     "/tmp/source-with-failing-kb-delete",
		DatasetID:    "ds-bound",
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	core := &fakeKnowledgeBaseCore{deleteErr: fmt.Errorf("core unavailable")}
	h := &Handler{store: st, core: core, log: zap.NewNop()}

	req := httptest.NewRequest(http.MethodDelete, "/api/scan/sources/"+src.ID, nil)
	req.SetPathValue("id", src.ID)
	w := httptest.NewRecorder()

	h.deleteSource(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d body=%s", w.Code, w.Body.String())
	}
	if core.deleteDatasetID != "ds-bound" || core.deleteUserID != "owner-1" {
		t.Fatalf("unexpected core delete call: dataset=%q user=%q", core.deleteDatasetID, core.deleteUserID)
	}
	if _, err := st.GetSource(ctx, src.ID); err != nil {
		t.Fatalf("expected source to remain when core delete fails, got %v", err)
	}
}

func TestFilterTreeByKeywordKeepsMatchingAncestors(t *testing.T) {
	t.Parallel()

	items := []model.TreeNode{
		{Title: "root", Key: "/root", IsDir: true, Children: []model.TreeNode{
			{Title: "docs", Key: "/root/docs", IsDir: true, Children: []model.TreeNode{
				{Title: "ReleaseNotes.md", Key: "/root/docs/ReleaseNotes.md", IsDir: false},
				{Title: "guide.txt", Key: "/root/release-path-only/guide.txt", IsDir: false},
			}},
			{Title: "assets", Key: "/root/assets", IsDir: true, Children: []model.TreeNode{
				{Title: "logo.png", Key: "/root/assets/logo.png", IsDir: false},
			}},
		}},
	}

	got := filterTreeByKeyword(items, "release")
	if len(got) != 1 {
		t.Fatalf("expected root to be kept, got %d nodes", len(got))
	}
	if len(got[0].Children) != 1 || got[0].Children[0].Title != "docs" {
		t.Fatalf("expected only docs ancestor, got %#v", got[0].Children)
	}
	docs := got[0].Children[0]
	if len(docs.Children) != 1 || docs.Children[0].Title != "ReleaseNotes.md" {
		t.Fatalf("expected only matching release file, got %#v", docs.Children)
	}
}

func TestPathTreeByAgentFiltersKeywordWhenAgentReturnsFullTree(t *testing.T) {
	t.Parallel()

	var receivedKeyword string
	var ts *httptest.Server
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("skip: httptest listener not available in current sandbox: %v", r)
			}
		}()
		ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/fs/tree" {
				http.NotFound(w, r)
				return
			}
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			receivedKeyword, _ = req["keyword"].(string)
			_ = json.NewEncoder(w).Encode(model.AgentPathTreeResponse{
				Items: []model.TreeNode{
					{Title: "root", Key: "/root", IsDir: true, Children: []model.TreeNode{
						{Title: "ReleaseNotes.md", Key: "/root/ReleaseNotes.md", IsDir: false},
						{Title: "guide.txt", Key: "/root/release-path-only/guide.txt", IsDir: false},
					}},
				},
			})
		}))
	}()
	if ts == nil {
		return
	}
	defer ts.Close()

	ctx := context.Background()
	st := newServerTestStore(t)
	if err := st.RegisterAgent(ctx, model.RegisterAgentRequest{
		AgentID:    "agent-keyword",
		TenantID:   "tenant-1",
		Hostname:   "test",
		Version:    "v1",
		ListenAddr: ts.URL,
	}); err != nil {
		t.Fatalf("register agent failed: %v", err)
	}
	h := &Handler{
		store:  st,
		client: &http.Client{Timeout: 2 * time.Second},
		log:    zap.NewNop(),
	}

	body := `{"agent_id":"agent-keyword","path":"/root","keyword":"release","include_files":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/agents/fs/tree", strings.NewReader(body))
	w := httptest.NewRecorder()

	h.pathTreeByAgent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 status, got %d: %s", w.Code, w.Body.String())
	}
	if receivedKeyword != "release" {
		t.Fatalf("expected keyword to be forwarded, got %q", receivedKeyword)
	}
	var resp model.AgentPathTreeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if len(resp.Items) != 1 || len(resp.Items[0].Children) != 1 {
		t.Fatalf("expected filtered tree, got %#v", resp.Items)
	}
	if resp.Items[0].Children[0].Title != "ReleaseNotes.md" {
		t.Fatalf("expected matching release file, got %#v", resp.Items[0].Children)
	}
}

func TestApplyCoreTaskStateUsesCoreParseStateWithoutChangingSnapshotUpdate(t *testing.T) {
	t.Parallel()

	hasUpdate := true
	item := model.SourceDocumentItem{
		DocumentID:             1,
		HasUpdate:              &hasUpdate,
		UpdateType:             "NEW",
		UpdateDesc:             "新文件待解析",
		ParseState:             "QUEUED",
		DesiredVersionID:       "v2",
		CurrentVersionID:       "",
		ParseTaskID:            10,
		ParseTaskAction:        "CREATE",
		ParseTaskTargetVersion: "v2",
	}

	applyCoreTaskStateToSourceDocumentItem(&item, "SUCCEEDED")

	if item.UpdateType != "NEW" {
		t.Fatalf("expected snapshot update_type NEW to be preserved, got %s", item.UpdateType)
	}
	if item.HasUpdate == nil || !*item.HasUpdate {
		t.Fatalf("expected snapshot has_update=true to be preserved, got %+v", item.HasUpdate)
	}
	if item.ParseState != "SUCCEEDED" {
		t.Fatalf("expected parse_state SUCCEEDED, got %s", item.ParseState)
	}
	if !shouldMarkSourceDocumentSucceededFromCore(item) {
		t.Fatalf("expected core success to be persisted")
	}
}

func TestApplyCoreTaskStateNormalizesSubmittedToRunning(t *testing.T) {
	t.Parallel()

	hasUpdate := true
	item := model.SourceDocumentItem{
		DocumentID:             1,
		HasUpdate:              &hasUpdate,
		UpdateType:             "MODIFIED",
		UpdateDesc:             "内容变化待重解析",
		ParseState:             "SUBMITTED",
		DesiredVersionID:       "v2",
		CurrentVersionID:       "v1",
		ParseTaskID:            10,
		ParseTaskAction:        "REPARSE",
		ParseTaskTargetVersion: "v2",
	}

	applyCoreTaskStateToSourceDocumentItem(&item, "TASK_STATE_SUBMITTED")

	if item.ParseState != "RUNNING" {
		t.Fatalf("expected submitted core state to be normalized to RUNNING, got %s", item.ParseState)
	}
	if item.CoreTaskState != "RUNNING" {
		t.Fatalf("expected core_task_state RUNNING, got %s", item.CoreTaskState)
	}
	if item.UpdateType != "MODIFIED" {
		t.Fatalf("expected snapshot update_type MODIFIED to be preserved, got %s", item.UpdateType)
	}
}

func TestApplyCoreTaskStateKeepsUpdateForStaleTaskVersion(t *testing.T) {
	t.Parallel()

	hasUpdate := true
	item := model.SourceDocumentItem{
		DocumentID:             1,
		HasUpdate:              &hasUpdate,
		UpdateType:             "MODIFIED",
		UpdateDesc:             "内容变化待重解析",
		ParseState:             "QUEUED",
		DesiredVersionID:       "v2",
		CurrentVersionID:       "v1",
		ParseTaskID:            10,
		ParseTaskAction:        "REPARSE",
		ParseTaskTargetVersion: "v1",
	}

	applyCoreTaskStateToSourceDocumentItem(&item, "SUCCEEDED")

	if item.UpdateType != "MODIFIED" {
		t.Fatalf("expected stale task to keep update_type MODIFIED, got %s", item.UpdateType)
	}
	if item.ParseState != "QUEUED" {
		t.Fatalf("expected stale task to keep parse_state QUEUED, got %s", item.ParseState)
	}
	if item.HasUpdate == nil || !*item.HasUpdate {
		t.Fatalf("expected has_update=true for stale task, got %+v", item.HasUpdate)
	}
	if shouldMarkSourceDocumentSucceededFromCore(item) {
		t.Fatalf("did not expect stale core success to be persisted")
	}
}

func TestApplyCoreTaskStateIgnoresStaleFailure(t *testing.T) {
	t.Parallel()

	hasUpdate := true
	item := model.SourceDocumentItem{
		DocumentID:             1,
		HasUpdate:              &hasUpdate,
		UpdateType:             "MODIFIED",
		UpdateDesc:             "内容变化待重解析",
		ParseState:             "PENDING",
		DesiredVersionID:       "v2",
		CurrentVersionID:       "v1",
		ParseTaskID:            10,
		ParseTaskAction:        "REPARSE",
		ParseTaskTargetVersion: "v1",
		CoreTaskID:             "core-task-old",
	}

	applyCoreTaskStateToSourceDocumentItem(&item, "FAILED")

	if item.ParseState != "PENDING" {
		t.Fatalf("expected stale failed task to keep parse_state PENDING, got %s", item.ParseState)
	}
	if item.CoreTaskState != "" {
		t.Fatalf("expected stale failed task not to set core_task_state, got %s", item.CoreTaskState)
	}
	if item.UpdateType != "MODIFIED" {
		t.Fatalf("expected stale failed task to keep update_type MODIFIED, got %s", item.UpdateType)
	}
}

func TestPublicParseStateCollapsesInternalAndCoreStates(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: "PENDING", want: "PROCESSING"},
		{in: "QUEUED", want: "PROCESSING"},
		{in: "RUNNING", want: "PROCESSING"},
		{in: "STAGING", want: "PROCESSING"},
		{in: "SUBMITTED", want: "PROCESSING"},
		{in: "RETRY_WAITING", want: "PROCESSING"},
		{in: "CREATING", want: "PROCESSING"},
		{in: "UPLOADING", want: "PROCESSING"},
		{in: "UPLOADED", want: "PROCESSING"},
		{in: "TASK_STATE_SUBMITTED", want: "PROCESSING"},
		{in: "SUCCEEDED", want: "SUCCESS"},
		{in: "SUCCESS", want: "SUCCESS"},
		{in: "TASK_STATE_SUCCEEDED", want: "SUCCESS"},
		{in: "DELETED", want: "SUCCESS"},
		{in: "FAILED", want: "FAILED"},
		{in: "SUBMIT_FAILED", want: "FAILED"},
		{in: "CANCELED", want: "FAILED"},
		{in: "SUSPENDED", want: "FAILED"},
	}

	for _, tc := range cases {
		if got := publicParseState(tc.in); got != tc.want {
			t.Fatalf("publicParseState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeSourceDocumentParseStatesForResponse(t *testing.T) {
	t.Parallel()

	items := []model.SourceDocumentItem{
		{
			ParseState:              "STAGING",
			CoreTaskState:           "TASK_STATE_SUBMITTED",
			ScanOrchestrationStatus: "SUCCEEDED",
		},
	}

	normalizeSourceDocumentParseStatesForResponse(items)
	if items[0].ParseState != "PROCESSING" {
		t.Fatalf("expected parse_state PROCESSING, got %s", items[0].ParseState)
	}
	if items[0].CoreTaskState != "PROCESSING" {
		t.Fatalf("expected core_task_state PROCESSING, got %s", items[0].CoreTaskState)
	}
	if items[0].ScanOrchestrationStatus != "SUCCESS" {
		t.Fatalf("expected scan_orchestration_status SUCCESS, got %s", items[0].ScanOrchestrationStatus)
	}
}

func TestNormalizeTreeParseQueueStatesForResponse(t *testing.T) {
	t.Parallel()

	items := []model.TreeNode{
		{Key: "/root/a.md", ParseQueueState: "STAGING", CoreTaskState: "TASK_STATE_SUBMITTED"},
		{Key: "/root/dir", IsDir: true, Children: []model.TreeNode{
			{Key: "/root/dir/b.md", ParseQueueState: "SUCCEEDED"},
			{Key: "/root/dir/c.md", ParseQueueState: "SUBMIT_FAILED"},
		}},
	}

	got := normalizeTreeParseQueueStatesForResponse(items)
	if got[0].ParseQueueState != "PROCESSING" {
		t.Fatalf("expected first node PROCESSING, got %s", got[0].ParseQueueState)
	}
	if got[0].CoreTaskState != "PROCESSING" {
		t.Fatalf("expected first node core_task_state PROCESSING, got %s", got[0].CoreTaskState)
	}
	if got[1].Children[0].ParseQueueState != "SUCCESS" {
		t.Fatalf("expected child success state, got %s", got[1].Children[0].ParseQueueState)
	}
	if got[1].Children[1].ParseQueueState != "FAILED" {
		t.Fatalf("expected child failed state, got %s", got[1].Children[1].ParseQueueState)
	}
}

func TestListSourcesIncludesCurrentUserBatchOverview(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := newServerTestStore(t)
	if err := st.RegisterAgent(ctx, model.RegisterAgentRequest{
		AgentID:  "agent-1",
		TenantID: "tenant-1",
		Hostname: "test",
		Version:  "v1",
	}); err != nil {
		t.Fatalf("register agent failed: %v", err)
	}

	src, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		CreateUserID:          "user-1",
		Name:                  "cloud source",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
		DefaultTriggerPolicy:  string(model.TriggerPolicyImmediate),
	})
	if err != nil {
		t.Fatalf("create source failed: %v", err)
	}
	if _, err := st.CreateSource(ctx, model.CreateSourceRequest{
		TenantID:              "tenant-1",
		CreateUserID:          "user-2",
		Name:                  "other source",
		AgentID:               "agent-1",
		DefaultOriginType:     string(model.OriginTypeCloudSync),
		DefaultOriginPlatform: "FEISHU",
	}); err != nil {
		t.Fatalf("create other source failed: %v", err)
	}

	enabled := true
	if _, err := st.UpsertCloudSourceBinding(ctx, src.ID, model.UpsertCloudSourceBindingRequest{
		Provider:         "feishu",
		Enabled:          &enabled,
		AuthConnectionID: "conn-1",
		TargetType:       "wiki_space",
		TargetRef:        "space-1",
	}); err != nil {
		t.Fatalf("upsert cloud binding failed: %v", err)
	}

	mutations, err := st.BuildMutationsFromEvents(ctx, []model.FileEvent{
		{
			SourceID:       src.ID,
			EventType:      "modified",
			Path:           "/tmp/watch/a.txt",
			OccurredAt:     time.Now().UTC(),
			OriginType:     string(model.OriginTypeCloudSync),
			OriginPlatform: "FEISHU",
			TriggerPolicy:  string(model.TriggerPolicyImmediate),
		},
	})
	if err != nil {
		t.Fatalf("build mutations failed: %v", err)
	}
	if err := st.BatchApplyDocumentMutations(ctx, mutations); err != nil {
		t.Fatalf("apply mutations failed: %v", err)
	}
	markNextDueParseSucceededForTest(t, st, ctx, time.Now().UTC(), "core-doc-a")

	h := &Handler{store: st, core: coreclient.NewNoop(), log: zap.NewNop()}
	req := httptest.NewRequest(http.MethodGet, "/api/scan/sources?tenant_id=tenant-1", nil)
	req.Header.Set("X-User-Id", "user-1")
	w := httptest.NewRecorder()
	h.listSources(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []model.Source `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected current user's source only, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.ID != src.ID {
		t.Fatalf("expected source %s, got %s", src.ID, item.ID)
	}
	if item.CloudBinding == nil || item.CloudBinding.Status != "ACTIVE" {
		t.Fatalf("expected active cloud binding, got %#v", item.CloudBinding)
	}
	if item.Documents == nil {
		t.Fatalf("expected documents overview")
	}
	if item.Documents.Total != 1 || item.Documents.Summary.TotalDocumentCount != 1 {
		t.Fatalf("expected one document, got total=%d summary=%d", item.Documents.Total, item.Documents.Summary.TotalDocumentCount)
	}
	if len(item.Documents.Items) != 1 || item.Documents.Items[0].Name != "a.txt" {
		t.Fatalf("expected first document a.txt, got %#v", item.Documents.Items)
	}
}

func TestBuildSourceDocumentsSummaryWithCoreKeepsSnapshotUpdateCounts(t *testing.T) {
	t.Parallel()

	refs := []store.SourceDocumentCoreRef{
		{
			DocumentID:       1,
			ParseStatus:      "QUEUED",
			DesiredVersionID: "v2",
			CurrentVersionID: "",
			TaskID:           10,
			TaskAction:       "CREATE",
			TargetVersionID:  "v2",
			CoreTaskID:       "core-task-1",
		},
	}
	states := map[string]coreclient.TaskState{
		"core-task-1": {TaskID: "core-task-1", TaskState: "SUCCEEDED"},
	}

	summary := buildSourceDocumentsSummaryWithCore(refs, states, 0)
	if summary.NewCount != 1 || summary.PendingPullCount != 1 {
		t.Fatalf("expected snapshot update counts to be preserved, got new=%d pending=%d", summary.NewCount, summary.PendingPullCount)
	}
	if summary.ParsedDocumentCount != 1 {
		t.Fatalf("expected parsed_document_count=1, got %d", summary.ParsedDocumentCount)
	}
}

func TestBuildSourceDocumentsSummaryWithCoreIgnoresStaleFailure(t *testing.T) {
	t.Parallel()

	refs := []store.SourceDocumentCoreRef{
		{
			DocumentID:       1,
			ParseStatus:      "PENDING",
			DesiredVersionID: "v2",
			CurrentVersionID: "v1",
			TaskID:           10,
			TaskAction:       "REPARSE",
			TargetVersionID:  "v1",
			CoreTaskID:       "core-task-old",
		},
	}
	states := map[string]coreclient.TaskState{
		"core-task-old": {TaskID: "core-task-old", TaskState: "FAILED"},
	}

	summary := buildSourceDocumentsSummaryWithCore(refs, states, 0)
	if summary.ModifiedCount != 1 || summary.PendingPullCount != 1 {
		t.Fatalf("expected modified document to remain pending, got modified=%d pending=%d", summary.ModifiedCount, summary.PendingPullCount)
	}
	if summary.ParsedDocumentCount != 1 {
		t.Fatalf("expected stale failure not to hide current parsed version, got parsed_document_count=%d", summary.ParsedDocumentCount)
	}
}

func TestSearchCoreTaskStatesUsesSourceCreatorContext(t *testing.T) {
	t.Parallel()

	core := &fakeKnowledgeBaseCore{
		searchStates: map[string]coreclient.TaskState{
			"task-1": {TaskID: "task-1", TaskState: "SUCCEEDED"},
			"task-2": {TaskID: "task-2", TaskState: "FAILED"},
		},
	}
	h := &Handler{core: core, log: zap.NewNop()}
	refs := []store.SourceDocumentCoreRef{
		{
			CoreDatasetID:      "ds-1",
			CoreTaskID:         "task-1",
			SourceCreateUserID: "owner-1",
		},
		{
			CoreDatasetID:      "ds-1",
			CoreTaskID:         "task-2",
			SourceCreateUserID: "owner-2",
		},
	}

	states, err := h.searchCoreTaskStates(context.Background(), refs)
	if err != nil {
		t.Fatalf("search core task states failed: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected two states, got %#v", states)
	}
	if len(core.searchCalls) != 2 {
		t.Fatalf("expected calls grouped by dataset and source creator, got %#v", core.searchCalls)
	}
	seen := map[string]bool{}
	for _, call := range core.searchCalls {
		if call.datasetID != "ds-1" {
			t.Fatalf("unexpected dataset id %q", call.datasetID)
		}
		if len(call.taskIDs) != 1 {
			t.Fatalf("expected one task per owner call, got %#v", call.taskIDs)
		}
		seen[call.userID] = true
	}
	if !seen["owner-1"] || !seen["owner-2"] {
		t.Fatalf("expected owner contexts owner-1 and owner-2, got %#v", core.searchCalls)
	}
}
