package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lazymind/scan_control_plane/internal/access"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector/localfs"
	sourceengine "github.com/lazymind/scan_control_plane/internal/sourceengine/source"
	taskengine "github.com/lazymind/scan_control_plane/internal/sourceengine/task"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/tree"
)

func TestCreateSourceHandlerRequiresBindingsArray(t *testing.T) {
	t.Parallel()

	engine := &serverSourceEngineStub{}
	handler := NewHandler(WithSourceEngine(engine), WithAccessChecker(allowAccess{}))
	body := `{"request_id":"req-1","name":"Docs","bindings":[{"connector_type":"local_fs","target_type":"local_path","target_ref":"/workspace/docs","sync_mode":"manual"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources", strings.NewReader(body))
	setAPIContractActor(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	if engine.createCalls != 1 {
		t.Fatalf("expected create source call, got %d", engine.createCalls)
	}
	if engine.lastCreate.CallerID != "user-1" || engine.lastCreate.TenantID != "tenant-1" || len(engine.lastCreate.Bindings) != 1 {
		t.Fatalf("create request did not use caller and bindings[]: %+v", engine.lastCreate)
	}
	if engine.lastCreate.Bindings[0].TargetRef != "/workspace/docs" {
		t.Fatalf("binding target was not decoded from bindings[]: %+v", engine.lastCreate.Bindings[0])
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/scan/sources", strings.NewReader(`{"request_id":"req-2","name":"Docs","target_ref":"/workspace/docs"}`))
	setAPIContractActor(badReq)
	badResp := httptest.NewRecorder()
	handler.ServeHTTP(badResp, badReq)

	if badResp.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown root-level target_ref to be rejected, got %d body=%s", badResp.Code, badResp.Body.String())
	}
	var errResp ErrorResponse
	if err := json.NewDecoder(badResp.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Code != string(sourceengine.ErrCodeInvalidRequest) {
		t.Fatalf("expected invalid request error, got %+v", errResp)
	}
}

func TestCreateSourceHandlerRejectsLocalSourceForNonAdmin(t *testing.T) {
	t.Parallel()

	engine := &serverSourceEngineStub{}
	handler := NewHandler(WithSourceEngine(engine), WithAccessChecker(allowAccess{}))
	body := `{"request_id":"req-1","name":"Docs","bindings":[{"connector_type":"local_fs","target_type":"local_path","target_ref":"/workspace/docs","sync_mode":"manual"}],"source_options":{"source_type":"local"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources", strings.NewReader(body))
	setAPIContractActorRole(req, "user")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected non-admin local source creation to be forbidden, got %d body=%s", w.Code, w.Body.String())
	}
	if engine.createCalls != 0 {
		t.Fatalf("expected denied request not to call source engine, got %d calls", engine.createCalls)
	}
}

func TestCreateSourceBindingRejectsLocalSourceForNonAdmin(t *testing.T) {
	t.Parallel()

	engine := &serverSourceEngineStub{}
	handler := NewHandler(WithSourceEngine(engine), WithAccessChecker(allowAccess{}))
	body := `{"connector_type":"local_fs","target_type":"local_path","target_ref":"/workspace/docs","sync_mode":"manual"}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/bindings", strings.NewReader(body))
	setAPIContractActorRole(req, "user")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected non-admin local binding creation to be forbidden, got %d body=%s", w.Code, w.Body.String())
	}
	if engine.addBindingCalls != 0 {
		t.Fatalf("expected denied request not to add binding, got %d calls", engine.addBindingCalls)
	}
}

func TestCreateSourceHandlerAcceptsStructuredProviderOptions(t *testing.T) {
	t.Parallel()

	engine := &serverSourceEngineStub{}
	handler := NewHandler(WithSourceEngine(engine), WithAccessChecker(allowAccess{}))
	body := `{
		"request_id":"feishu-source-d677919a-f32e-4b22-ab35-6d0f5587d82d",
		"name":"阿斯顿发",
		"bindings":[{
			"connector_type":"feishu",
				"target_type":"wiki_node",
				"sync_mode":"scheduled",
				"schedule_policy":{
					"timezone":"Asia/Shanghai",
					"calendar":"weekly",
					"rules":[{"days":["everyday"],"time":"02:00:00"}]
				},
				"auth_connection_id":"conn_6c27d5a8f42b4d24ae1e69c9313a1e32",
			"provider_options":{
				"include_patterns":["**/*.md","**/*.doc","**/*.docx","**/*.pdf","**/*.txt"],
				"exclude_patterns":["**/~$*"],
				"max_object_size_bytes":209715200,
				"reconcile_after_sync":true,
				"reconcile_delay_minutes":10
			},
			"target_ref":"wiki:7637791838560078802:ZpsGwoZYmiJRKzkYBAccv16MnJh"
		}],
		"source_options":{"source_type":"feishu","auth_connection_id":"conn_6c27d5a8f42b4d24ae1e69c9313a1e32"}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources", strings.NewReader(body))
	setAPIContractActor(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", w.Code, w.Body.String())
	}
	options := engine.lastCreate.Bindings[0].ProviderOptions
	assertAnySlice(t, options["include_patterns"], []string{"**/*.md", "**/*.doc", "**/*.docx", "**/*.pdf", "**/*.txt"})
	assertAnySlice(t, options["exclude_patterns"], []string{"**/~$*"})
	assertJSONNumber(t, options["max_object_size_bytes"], "209715200")
	assertJSONNumber(t, options["reconcile_delay_minutes"], "10")
	if options["reconcile_after_sync"] != true {
		t.Fatalf("expected boolean provider option to be preserved, got %#v", options["reconcile_after_sync"])
	}
	policy := engine.lastCreate.Bindings[0].SchedulePolicy
	if policy["timezone"] != "Asia/Shanghai" || policy["calendar"] != "weekly" {
		t.Fatalf("expected schedule policy to be preserved, got %#v", policy)
	}
}

func TestNewHandlerWithoutAccessCheckerDeniesProtectedRoutes(t *testing.T) {
	t.Parallel()

	engine := &serverSourceEngineStub{}
	handler := NewHandler(WithSourceEngine(engine))
	body := `{"request_id":"req-1","name":"Docs","bindings":[{"connector_type":"local_fs","target_type":"local_path","target_ref":"/workspace/docs","sync_mode":"manual"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources", strings.NewReader(body))
	setAPIContractActor(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected default access checker to deny protected route, got %d body=%s", w.Code, w.Body.String())
	}
	if engine.createCalls != 0 {
		t.Fatalf("expected denied request not to call source engine, got %d calls", engine.createCalls)
	}
}

func TestDeleteSourceByDatasetInternalRouteSkipsCoreDatasetDelete(t *testing.T) {
	t.Parallel()

	engine := &serverSourceEngineStub{}
	handler := NewHandler(WithSourceEngine(engine))
	req := httptest.NewRequest(http.MethodDelete, "/api/scan/internal/sources/by-dataset/dataset-1", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if engine.deleteByDatasetCalls != 1 {
		t.Fatalf("expected one delete by dataset call, got %d", engine.deleteByDatasetCalls)
	}
	if engine.lastDeleteDatasetID != "dataset-1" {
		t.Fatalf("expected dataset id to be routed, got %q", engine.lastDeleteDatasetID)
	}
	if !engine.lastDeleteOptions.SkipCoreDatasetDelete {
		t.Fatalf("internal dataset delete route must skip core dataset delete")
	}
}

func TestHandlersExposeConnectorsTargetTreeAndSourceTree(t *testing.T) {
	t.Parallel()

	registry, err := connector.NewDefaultConnectorRegistry(localfs.NewLocalFSConnector(nil))
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	targetTree := &serverTargetTreeStub{}
	sourceTree := &serverSourceTreeStub{}
	documents := &serverDocumentQueryStub{}
	handler := NewHandler(
		WithConnectorRegistry(registry),
		WithTargetTreeEngine(targetTree),
		WithSourceTreeQueryEngine(sourceTree),
		WithSourceDocumentQuery(documents),
		WithAccessChecker(allowAccess{}),
	)

	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/scan/connectors", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"connector_type":"local_fs"`) {
		t.Fatalf("unexpected connectors response: code=%d body=%s", get.Code, get.Body.String())
	}

	targetReq := httptest.NewRequest(http.MethodPost, "/api/scan/binding-targets/tree/children", strings.NewReader(`{"connector_type":"local_fs","target_type":"local_path","target_ref":"/workspace/docs","page_size":10}`))
	setAPIContractActor(targetReq)
	targetResp := httptest.NewRecorder()
	handler.ServeHTTP(targetResp, targetReq)
	if targetResp.Code != http.StatusOK || targetTree.childrenCalls != 1 || targetTree.lastChildren.ConnectorType != localfs.ConnectorType {
		t.Fatalf("target tree handler did not call engine: code=%d calls=%d req=%+v body=%s", targetResp.Code, targetTree.childrenCalls, targetTree.lastChildren, targetResp.Body.String())
	}
	if targetTree.lastChildren.ProviderOptions["user_id"] != "user-1" || targetTree.lastChildren.ProviderOptions["tenant_id"] != "tenant-1" {
		t.Fatalf("target tree handler did not pass actor to connector provider options: %+v", targetTree.lastChildren.ProviderOptions)
	}

	sourceReq := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/tree/children", strings.NewReader(`{"binding_id":"binding-1","use_cache":true}`))
	setAPIContractActor(sourceReq)
	sourceResp := httptest.NewRecorder()
	handler.ServeHTTP(sourceResp, sourceReq)
	if sourceResp.Code != http.StatusOK || sourceTree.childrenCalls != 1 || sourceTree.lastChildren.SourceID != "source-1" || sourceTree.lastChildren.UseCache == nil || !*sourceTree.lastChildren.UseCache {
		t.Fatalf("source tree handler did not set source_id: code=%d calls=%d req=%+v body=%s", sourceResp.Code, sourceTree.childrenCalls, sourceTree.lastChildren, sourceResp.Body.String())
	}
	if sourceTree.lastChildren.ProviderOptions["user_id"] != "user-1" || sourceTree.lastChildren.ProviderOptions["tenant_id"] != "tenant-1" {
		t.Fatalf("source tree handler did not pass actor to connector provider options: %+v", sourceTree.lastChildren.ProviderOptions)
	}

	docReq := httptest.NewRequest(http.MethodGet, "/api/scan/sources/source-1/documents?binding_id=binding-1", nil)
	setAPIContractActor(docReq)
	docResp := httptest.NewRecorder()
	handler.ServeHTTP(docResp, docReq)
	if docResp.Code != http.StatusOK || documents.calls != 1 || documents.lastReq.SourceID != "source-1" || documents.lastReq.BindingID != "binding-1" {
		t.Fatalf("document handler did not call query: code=%d calls=%d req=%+v body=%s", docResp.Code, documents.calls, documents.lastReq, docResp.Body.String())
	}
}

func TestDocumentHandlerPassesFiltersAndPagination(t *testing.T) {
	t.Parallel()

	documents := &serverDocumentQueryStub{}
	handler := NewHandler(WithSourceDocumentQuery(documents), WithAccessChecker(allowAccess{}))
	req := httptest.NewRequest(http.MethodGet, "/api/scan/sources/source-1/documents?binding_id=binding-1&state_filter=NEW&state_filter=MODIFIED&parse_status=PENDING&page=2&page_size=30", nil)
	setAPIContractActor(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d body=%s", w.Code, w.Body.String())
	}
	if got := documents.lastReq; got.Page != 2 || got.PageSize != 30 || len(got.StateFilter) != 2 || got.StateFilter[1] != "MODIFIED" || len(got.ParseStatuses) != 1 || got.ParseStatuses[0] != "PENDING" {
		t.Fatalf("document query filters were not propagated: %+v", got)
	}
}

func TestReadHandlersRefreshSourceStateByDefaultWithoutSyncingData(t *testing.T) {
	t.Parallel()

	sourceTree := &serverSourceTreeStub{}
	documents := &serverDocumentQueryStub{}
	refresher := &serverReadRefresherStub{}
	handler := NewHandler(
		WithSourceTreeQueryEngine(sourceTree),
		WithSourceDocumentQuery(documents),
		WithSourceReadRefresher(refresher),
		WithAccessChecker(allowAccess{}),
	)

	treeReq := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/tree/children", strings.NewReader(`{"binding_id":"binding-1"}`))
	setAPIContractActor(treeReq)
	treeResp := httptest.NewRecorder()
	handler.ServeHTTP(treeResp, treeReq)
	if treeResp.Code != http.StatusOK {
		t.Fatalf("tree read failed: code=%d body=%s", treeResp.Code, treeResp.Body.String())
	}
	if refresher.calls != 1 || refresher.lastReq.SourceID != "source-1" || refresher.lastReq.BindingID != "binding-1" {
		t.Fatalf("default tree read did not refresh source state: calls=%d req=%+v", refresher.calls, refresher.lastReq)
	}

	cachedTreeReq := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/tree/children", strings.NewReader(`{"binding_id":"binding-1","use_cache":true}`))
	setAPIContractActor(cachedTreeReq)
	cachedTreeResp := httptest.NewRecorder()
	handler.ServeHTTP(cachedTreeResp, cachedTreeReq)
	if cachedTreeResp.Code != http.StatusOK {
		t.Fatalf("cached tree read failed: code=%d body=%s", cachedTreeResp.Code, cachedTreeResp.Body.String())
	}
	if refresher.calls != 2 || refresher.lastReq.SourceID != "source-1" || refresher.lastReq.BindingID != "binding-1" {
		t.Fatalf("cached tree read should still refresh source state: calls=%d req=%+v", refresher.calls, refresher.lastReq)
	}

	cachedOnlyTreeReq := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/tree/children", strings.NewReader(`{"binding_id":"binding-1","use_cache":true,"refresh_state":false}`))
	setAPIContractActor(cachedOnlyTreeReq)
	cachedOnlyTreeResp := httptest.NewRecorder()
	handler.ServeHTTP(cachedOnlyTreeResp, cachedOnlyTreeReq)
	if cachedOnlyTreeResp.Code != http.StatusOK {
		t.Fatalf("cached-only tree read failed: code=%d body=%s", cachedOnlyTreeResp.Code, cachedOnlyTreeResp.Body.String())
	}
	if refresher.calls != 2 {
		t.Fatalf("explicit refresh_state=false tree read should not refresh source state, calls=%d", refresher.calls)
	}

	searchReq := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/tree/search", strings.NewReader(`{"binding_id":"binding-1","keyword":"doc"}`))
	setAPIContractActor(searchReq)
	searchResp := httptest.NewRecorder()
	handler.ServeHTTP(searchResp, searchReq)
	if searchResp.Code != http.StatusOK {
		t.Fatalf("tree search failed: code=%d body=%s", searchResp.Code, searchResp.Body.String())
	}
	if refresher.calls != 3 || refresher.lastReq.SourceID != "source-1" || refresher.lastReq.BindingID != "binding-1" {
		t.Fatalf("tree search did not refresh source state: calls=%d req=%+v", refresher.calls, refresher.lastReq)
	}

	cachedSearchReq := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/tree/search", strings.NewReader(`{"binding_id":"binding-1","keyword":"doc","refresh_state":false}`))
	setAPIContractActor(cachedSearchReq)
	cachedSearchResp := httptest.NewRecorder()
	handler.ServeHTTP(cachedSearchResp, cachedSearchReq)
	if cachedSearchResp.Code != http.StatusOK {
		t.Fatalf("cached tree search failed: code=%d body=%s", cachedSearchResp.Code, cachedSearchResp.Body.String())
	}
	if refresher.calls != 3 {
		t.Fatalf("explicit refresh_state=false tree search should not refresh source state, calls=%d", refresher.calls)
	}

	docReq := httptest.NewRequest(http.MethodGet, "/api/scan/sources/source-1/documents?binding_id=binding-1", nil)
	setAPIContractActor(docReq)
	docResp := httptest.NewRecorder()
	handler.ServeHTTP(docResp, docReq)
	if docResp.Code != http.StatusOK {
		t.Fatalf("document read failed: code=%d body=%s", docResp.Code, docResp.Body.String())
	}
	if refresher.calls != 4 || refresher.lastReq.SourceID != "source-1" || refresher.lastReq.BindingID != "binding-1" {
		t.Fatalf("default document read did not refresh source state: calls=%d req=%+v", refresher.calls, refresher.lastReq)
	}

	cachedDocReq := httptest.NewRequest(http.MethodGet, "/api/scan/sources/source-1/documents?binding_id=binding-1&refresh_state=false", nil)
	setAPIContractActor(cachedDocReq)
	cachedDocResp := httptest.NewRecorder()
	handler.ServeHTTP(cachedDocResp, cachedDocReq)
	if cachedDocResp.Code != http.StatusOK {
		t.Fatalf("cached document read failed: code=%d body=%s", cachedDocResp.Code, cachedDocResp.Body.String())
	}
	if refresher.calls != 4 {
		t.Fatalf("explicit cached document read should not refresh source state, calls=%d", refresher.calls)
	}
}

func TestOpenAPIDocsRoutesMatchLegacyScanMode(t *testing.T) {
	t.Parallel()

	handler := NewHandler()
	for _, tc := range []struct {
		path    string
		specURL string
	}{
		{path: "/docs", specURL: "/openapi.json"},
		{path: "/api/scan/docs", specURL: "/api/scan/openapi.json"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected OK for %s, got %d body=%s", tc.path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("expected html content type for %s, got %q", tc.path, w.Header().Get("Content-Type"))
		}
		if !strings.Contains(w.Body.String(), "SwaggerUIBundle") || !strings.Contains(w.Body.String(), tc.specURL) {
			t.Fatalf("unexpected docs html body for %s: %s", tc.path, w.Body.String())
		}
	}

	for _, path := range []string{"/openapi.yaml", "/api/scan/openapi.yaml"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected OK for %s, got %d body=%s", path, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Type"), "application/x-yaml") {
			t.Fatalf("expected yaml content type for %s, got %q", path, w.Header().Get("Content-Type"))
		}
		if !strings.Contains(w.Body.String(), "openapi:") || !strings.Contains(w.Body.String(), "/api/scan/connectors") {
			t.Fatalf("unexpected OpenAPI yaml body for %s: %s", path, w.Body.String())
		}
	}
}

func TestValidateBindingTargetAcceptsSnakeCasePayload(t *testing.T) {
	t.Parallel()

	connectorStub := &apiContractConnectorStub{}
	registry, err := connector.NewDefaultConnectorRegistry(connectorStub)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	handler := NewHandler(WithConnectorRegistry(registry), WithAccessChecker(allowAccess{}))
	req := httptest.NewRequest(http.MethodPost, "/api/scan/binding-targets/validate", strings.NewReader(`{"connector_type":"local_fs","target_type":"local_path","target_ref":"/workspace/docs","agent_id":"agent-1","provider_options":{"display_name":"Docs"}}`))
	setAPIContractActor(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d body=%s", w.Code, w.Body.String())
	}
	if connectorStub.lastValidate.ConnectorType != localfs.ConnectorType || connectorStub.lastValidate.TargetType != localfs.TargetTypeLocalPath || connectorStub.lastValidate.TargetRef != "/workspace/docs" || connectorStub.lastValidate.AgentID != "agent-1" {
		t.Fatalf("validate request did not decode snake_case fields: %+v", connectorStub.lastValidate)
	}
	if connectorStub.lastValidate.UserID != "user-1" || connectorStub.lastValidate.ProviderOptions["display_name"] != "Docs" {
		t.Fatalf("validate request did not preserve caller/provider options: %+v", connectorStub.lastValidate)
	}
	if !strings.Contains(w.Body.String(), `"target_fingerprint":"fp-1"`) || strings.Contains(w.Body.String(), `"TargetFingerprint"`) {
		t.Fatalf("validate response was not encoded as snake_case: %s", w.Body.String())
	}
}

func TestLocalFSBindingTargetValidateAndChildrenAcceptMissingAgentID(t *testing.T) {
	t.Parallel()

	agent := &apiContractLocalAgentStub{}
	registry, err := connector.NewDefaultConnectorRegistry(localfs.NewLocalFSConnector(agent, localfs.WithDefaultAgentID("agent-default")))
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	targetTree := tree.NewDefaultTargetTreeEngine(registry)
	handler := NewHandler(
		WithConnectorRegistry(registry),
		WithTargetTreeEngine(targetTree),
		WithAccessChecker(allowAccess{}),
	)

	validateReq := httptest.NewRequest(http.MethodPost, "/api/scan/binding-targets/validate", strings.NewReader(`{"connector_type":"local_fs","target_type":"local_path","target_ref":"/workspace/docs"}`))
	setAPIContractActor(validateReq)
	validateResp := httptest.NewRecorder()
	handler.ServeHTTP(validateResp, validateReq)

	if validateResp.Code != http.StatusOK {
		t.Fatalf("validate expected OK, got %d body=%s", validateResp.Code, validateResp.Body.String())
	}
	if strings.Contains(validateResp.Body.String(), "agent_id is required") || !strings.Contains(validateResp.Body.String(), `"agent_id":"agent-default"`) {
		t.Fatalf("validate did not use default local_fs agent: %s", validateResp.Body.String())
	}

	childrenReq := httptest.NewRequest(http.MethodPost, "/api/scan/binding-targets/tree/children", strings.NewReader(`{"connector_type":"local_fs","target_type":"local_path","target_ref":"/workspace/docs","include_files":false,"list_mode":"page","page_size":50}`))
	setAPIContractActor(childrenReq)
	childrenResp := httptest.NewRecorder()
	handler.ServeHTTP(childrenResp, childrenReq)

	if childrenResp.Code != http.StatusOK {
		t.Fatalf("children expected OK, got %d body=%s", childrenResp.Code, childrenResp.Body.String())
	}
	if strings.Contains(childrenResp.Body.String(), "agent_id is required") || !strings.Contains(childrenResp.Body.String(), `"key":"local_fs:agent-default:path:/workspace/docs/guides"`) {
		t.Fatalf("children did not use default local_fs agent: %s", childrenResp.Body.String())
	}
	if agent.lastValidate.AgentID != "agent-default" || agent.lastList.AgentID != "agent-default" {
		t.Fatalf("default agent was not sent to local agent: validate=%+v list=%+v", agent.lastValidate, agent.lastList)
	}
}

func TestTreeSearchHandlersAcceptPageListMode(t *testing.T) {
	t.Parallel()

	targetTree := &serverTargetTreeStub{}
	sourceTree := &serverSourceTreeStub{}
	handler := NewHandler(
		WithTargetTreeEngine(targetTree),
		WithSourceTreeQueryEngine(sourceTree),
		WithAccessChecker(allowAccess{}),
	)

	targetReq := httptest.NewRequest(http.MethodPost, "/api/scan/binding-targets/tree/search", strings.NewReader(`{"connector_type":"local_fs","target_type":"local_path","keyword":"/workspace","include_files":false,"list_mode":"page","page_size":50}`))
	setAPIContractActor(targetReq)
	targetResp := httptest.NewRecorder()
	handler.ServeHTTP(targetResp, targetReq)

	if targetResp.Code != http.StatusOK {
		t.Fatalf("target search expected OK, got %d body=%s", targetResp.Code, targetResp.Body.String())
	}
	if targetTree.searchCalls != 1 || targetTree.lastSearch.ListMode != tree.ListModePage || targetTree.lastSearch.PageSize != 50 {
		t.Fatalf("target search request was not decoded with list_mode/page_size: %+v", targetTree.lastSearch)
	}

	sourceReq := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/tree/search", strings.NewReader(`{"binding_id":"binding-1","tree_key":"tree-root","keyword":"hand","include_documents":true,"include_containers":true,"list_mode":"page","page_size":50}`))
	setAPIContractActor(sourceReq)
	sourceResp := httptest.NewRecorder()
	handler.ServeHTTP(sourceResp, sourceReq)

	if sourceResp.Code != http.StatusOK {
		t.Fatalf("source search expected OK, got %d body=%s", sourceResp.Code, sourceResp.Body.String())
	}
	if sourceTree.searchCalls != 1 || sourceTree.lastSearch.ListMode != tree.ListModePage || sourceTree.lastSearch.PageSize != 50 {
		t.Fatalf("source search request was not decoded with list_mode/page_size: %+v", sourceTree.lastSearch)
	}
}

func TestSyncHandlerAllowsMissingRequestIDAndEmptyBody(t *testing.T) {
	t.Parallel()

	engine := &serverSourceEngineStub{}
	handler := NewHandler(WithSourceEngine(engine), WithAccessChecker(allowAccess{}))
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/sync", nil)
	setAPIContractActor(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d body=%s", w.Code, w.Body.String())
	}
	if engine.lastSync.SourceID != "source-1" || engine.lastSync.RequestID != "" {
		t.Fatalf("sync request was not decoded as optional request_id: %+v", engine.lastSync)
	}
}

func TestGenerateTasksHandlerAcceptsManualPullSelection(t *testing.T) {
	t.Parallel()

	tasks := &serverTaskPlannerStub{}
	handler := NewHandler(WithTaskPlanner(tasks), WithAccessChecker(allowAccess{}))
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources/source-1/tasks/generate", strings.NewReader(`{
		"mode":"partial",
		"paths":["binding-1:local_fs:agent-1:path:/workspace/docs/111.txt"],
		"selection_token":"token-1",
		"trigger_policy":"IMMEDIATE",
		"updated_only":false
	}`))
	setAPIContractActor(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected OK, got %d body=%s", w.Code, w.Body.String())
	}
	if tasks.lastGenerate.CallerID != "user-1" || tasks.lastGenerate.TenantID != "tenant-1" || tasks.lastGenerate.SourceID != "source-1" {
		t.Fatalf("generate request did not preserve actor/source: %+v", tasks.lastGenerate)
	}
	if tasks.lastGenerate.Mode != "partial" || len(tasks.lastGenerate.Paths) != 1 || tasks.lastGenerate.SelectionToken != "token-1" {
		t.Fatalf("manual pull selection was not decoded: %+v", tasks.lastGenerate)
	}
}

func TestErrorResponseAlwaysIncludesDetailsObject(t *testing.T) {
	t.Parallel()

	handler := NewHandler(WithAccessChecker(allowAccess{}))
	req := httptest.NewRequest(http.MethodPost, "/api/scan/sources", strings.NewReader(`{"request_id":`))
	setAPIContractActor(req)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	var errResp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Details == nil {
		t.Fatalf("details must be present as an object: %+v", errResp)
	}
}

func setAPIContractActor(req *http.Request) {
	setAPIContractActorRole(req, "system-admin")
}

func setAPIContractActorRole(req *http.Request, role string) {
	req.Header.Set("X-User-ID", "user-1")
	req.Header.Set("X-Tenant-ID", "tenant-1")
	req.Header.Set("X-User-Role", role)
}

type allowAccess struct{}

func (allowAccess) ListReadableSourceIDs(context.Context, access.Actor) ([]string, error) {
	return nil, nil
}

func (allowAccess) CanCreateSource(context.Context, access.Actor) error {
	return nil
}

func (allowAccess) CanReadSource(context.Context, access.Actor, string) error {
	return nil
}

func (allowAccess) CanWriteSource(context.Context, access.Actor, string) error {
	return nil
}

func (allowAccess) CanDeleteSource(context.Context, access.Actor, string) error {
	return nil
}

func (allowAccess) CanReadBinding(context.Context, access.Actor, string, string) error {
	return nil
}

func (allowAccess) CanWriteBinding(context.Context, access.Actor, string, string) error {
	return nil
}

func (allowAccess) CanDeleteBinding(context.Context, access.Actor, string, string) error {
	return nil
}

func (allowAccess) CanReadTask(context.Context, access.Actor, string) error {
	return nil
}

func (allowAccess) CanWriteTask(context.Context, access.Actor, string) error {
	return nil
}

func (allowAccess) CanAccessBindingTarget(context.Context, access.Actor, access.BindingTargetRequest) error {
	return nil
}

func (allowAccess) CanUseAgent(context.Context, access.Actor, string) error {
	return nil
}

func (allowAccess) CanUseAuthConnection(context.Context, access.Actor, string) error {
	return nil
}

func TestOpenAPIContractCoversScanAPIAndDoesNotExposeLegacyPaths(t *testing.T) {
	t.Parallel()

	spec := OpenAPISpec()
	paths := spec["paths"].(map[string]any)
	required := map[string]string{
		"GET /api/scan/connectors":                                                 "listConnectors",
		"POST /api/scan/binding-targets/tree/children":                             "listBindingTargetChildren",
		"POST /api/scan/binding-targets/tree/search":                               "searchBindingTargets",
		"POST /api/scan/binding-targets/validate":                                  "validateBindingTarget",
		"POST /api/scan/sources":                                                   "createSource",
		"GET /api/scan/sources":                                                    "listSources",
		"GET /api/scan/sources/{source_id}":                                        "getSource",
		"PUT /api/scan/sources/{source_id}":                                        "updateSource",
		"DELETE /api/scan/sources/{source_id}":                                     "deleteSource",
		"POST /api/scan/sources/{source_id}/bindings":                              "createSourceBinding",
		"PUT /api/scan/sources/{source_id}/bindings/{binding_id}":                  "updateSourceBinding",
		"DELETE /api/scan/sources/{source_id}/bindings/{binding_id}":               "deleteSourceBinding",
		"POST /api/scan/sources/{source_id}/sync":                                  "triggerSourceSync",
		"POST /api/scan/sources/{source_id}/tree/children":                         "listSourceTreeChildren",
		"POST /api/scan/sources/{source_id}/tree/search":                           "searchSourceTree",
		"GET /api/scan/sources/{source_id}/documents":                              "listSourceDocuments",
		"GET /api/scan/sources/{source_id}/summary":                                "getSourceSummary",
		"POST /api/scan/sources/{source_id}/tasks/generate":                        "generateParseTasks",
		"POST /api/scan/sources/{source_id}/tasks/expedite":                        "expediteParseTasks",
		"GET /api/scan/parse-tasks":                                                "listParseTasks",
		"GET /api/scan/parse-tasks/stats":                                          "getParseTaskStats",
		"GET /api/scan/parse-tasks/{task_id}":                                      "getParseTask",
		"POST /api/scan/parse-tasks/{task_id}/retry":                               "retryParseTask",
		"GET /api/scan/admin/deleting":                                             "listDeletingResources",
		"GET /api/scan/admin/compensations":                                        "listCompensations",
		"POST /api/scan/admin/compensations/{operation_id}/retry":                  "retryCompensation",
		"GET /api/scan/admin/dead-letters":                                         "listDeadLetters",
		"POST /api/scan/admin/dead-letters/{dead_letter_id}/retry":                 "retryDeadLetter",
		"POST /api/scan/admin/sources/{source_id}/bindings/{binding_id}/reconcile": "reconcileBinding",
	}
	for route, operationID := range required {
		method, path := splitRoute(route)
		pathSpec, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("missing OpenAPI path %s", path)
		}
		operation, ok := pathSpec[strings.ToLower(method)].(map[string]any)
		if !ok {
			t.Fatalf("missing OpenAPI operation %s", route)
		}
		if operation["operationId"] != operationID {
			t.Fatalf("expected operationId %s for %s, got %+v", operationID, route, operation["operationId"])
		}
		if _, ok := operation["responses"].(map[string]any)["default"]; !ok {
			t.Fatalf("operation %s lacks default error response", route)
		}
	}

	body := mustMarshalSpec(t, spec)
	for _, forbidden := range []string{
		"/api/scan/" + "cloud" + "/target/validate",
		"/api/scan/sources/{source_id}/" + "cloud" + "/binding",
		"root" + "_path",
		"Origin" + "Type",
		"cloud" + "sync",
		"cloud" + "_source_bindings",
		"cloud" + "_object_index",
		"cloud" + "_sync",
	} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("OpenAPI spec contains forbidden legacy token %q", forbidden)
		}
	}

	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	assertSchemaRequired(t, schemas, "CreateSourceRequest", "request_id", "name", "bindings")
	assertSchemaRequired(t, schemas, "ErrorResponse", "code", "message", "details")
	assertSchemaRequired(t, schemas, "SourceListItem", "source_id", "name", "dataset_id", "status", "binding_count", "updated_at")
	assertSchemaRequired(t, schemas, "SourceBindingResponse", "binding_id", "connector_type", "target_type", "tree_key", "binding_generation", "core_parent_document_id")
	assertSchemaRequired(t, schemas, "TreeNodePage", "items", "next_cursor", "has_more", "list_complete", "truncated")
	assertSchemaRequired(t, schemas, "DeletingResource", "resource_type", "source_id", "status", "updated_at")
	assertSchemaRequired(t, schemas, "DeadLetter", "dead_letter_id", "task_id", "source_id", "binding_id", "binding_generation", "object_key")
	assertSchemaRequired(t, schemas, "ReconcileBindingResponse", "run_id", "source_id", "binding_id", "binding_generation", "status")
	assertSchemaNotRequired(t, schemas, "TriggerSourceSyncRequest", "request_id")
	assertEnumValues(t, schemas, "ListMode", "page", "all_current_level")
}

func splitRoute(route string) (string, string) {
	parts := strings.SplitN(route, " ", 2)
	return parts[0], parts[1]
}

func mustMarshalSpec(t *testing.T, spec map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	return body
}

func assertSchemaRequired(t *testing.T, schemas map[string]any, name string, fields ...string) {
	t.Helper()
	schema, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("missing schema %s", name)
	}
	rawRequired, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("schema %s has no required fields: %+v", name, schema)
	}
	required := map[string]struct{}{}
	for _, field := range rawRequired {
		required[field] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := required[field]; !ok {
			t.Fatalf("schema %s missing required field %s in %v", name, field, rawRequired)
		}
	}
}

func assertSchemaNotRequired(t *testing.T, schemas map[string]any, name string, fields ...string) {
	t.Helper()
	schema, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("missing schema %s", name)
	}
	rawRequired, _ := schema["required"].([]string)
	required := map[string]struct{}{}
	for _, field := range rawRequired {
		required[field] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := required[field]; ok {
			t.Fatalf("schema %s should not require field %s in %v", name, field, rawRequired)
		}
	}
}

func assertEnumValues(t *testing.T, schemas map[string]any, name string, values ...string) {
	t.Helper()
	schema := schemas[name].(map[string]any)
	rawEnum := schema["enum"].([]string)
	got := map[string]struct{}{}
	for _, value := range rawEnum {
		got[value] = struct{}{}
	}
	for _, value := range values {
		if _, ok := got[value]; !ok {
			t.Fatalf("schema %s missing enum value %s in %v", name, value, rawEnum)
		}
	}
}

func assertAnySlice(t *testing.T, value any, want []string) {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("expected []any, got %#v", value)
	}
	got := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("expected string array item, got %#v", item)
		}
		got = append(got, text)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected array option: got=%v want=%v", got, want)
	}
}

func assertJSONNumber(t *testing.T, value any, want string) {
	t.Helper()
	number, ok := value.(json.Number)
	if !ok {
		t.Fatalf("expected json.Number %q, got %#v", want, value)
	}
	if number.String() != want {
		t.Fatalf("unexpected number option: got=%s want=%s", number.String(), want)
	}
}

type serverSourceEngineStub struct {
	createCalls          int
	addBindingCalls      int
	deleteByDatasetCalls int
	lastCreate           sourceengine.CreateSourceRequest
	lastSync             sourceengine.TriggerSourceSyncRequest
	lastDeleteDatasetID  string
	lastDeleteOptions    sourceengine.DeleteSourceOptions
}

func (s *serverSourceEngineStub) CreateSource(_ context.Context, req sourceengine.CreateSourceRequest) (sourceengine.CreateSourceResponse, error) {
	s.createCalls++
	s.lastCreate = req
	now := time.Date(2026, 5, 27, 8, 0, 0, 0, time.UTC)
	return sourceengine.CreateSourceResponse{
		Source: sourceengine.SourceResponse{
			SourceID:      "source-1",
			Name:          req.Name,
			DatasetID:     "dataset-1",
			Status:        sourceengine.SourceStatusActive,
			ConfigVersion: 1,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		Bindings: []sourceengine.SourceBindingResponse{{
			BindingID:            "binding-1",
			SourceID:             "source-1",
			ConnectorType:        string(req.Bindings[0].ConnectorType),
			TargetType:           string(req.Bindings[0].TargetType),
			TargetRef:            req.Bindings[0].TargetRef,
			TreeKey:              "local-root",
			BindingGeneration:    1,
			CoreParentDocumentID: "core-folder-1",
			SyncMode:             req.Bindings[0].SyncMode,
			Status:               sourceengine.BindingStatusActive,
			CreatedAt:            now,
			UpdatedAt:            now,
		}},
	}, nil
}

func (s *serverSourceEngineStub) ListSources(context.Context, sourceengine.ListSourcesRequest) (sourceengine.ListSourcesResponse, error) {
	return sourceengine.ListSourcesResponse{}, nil
}

func (s *serverSourceEngineStub) GetSource(context.Context, sourceengine.GetSourceRequest) (sourceengine.GetSourceResponse, error) {
	return sourceengine.GetSourceResponse{}, nil
}

func (s *serverSourceEngineStub) GetSourceSummary(context.Context, sourceengine.SourceSummaryRequest) (sourceengine.SourceSummaryResponse, error) {
	return sourceengine.SourceSummaryResponse{SourceID: "source-1"}, nil
}

func (s *serverSourceEngineStub) TriggerSourceSync(_ context.Context, req sourceengine.TriggerSourceSyncRequest) (sourceengine.TriggerSourceSyncResponse, error) {
	s.lastSync = req
	return sourceengine.TriggerSourceSyncResponse{}, nil
}

func (s *serverSourceEngineStub) UpdateSource(context.Context, string, string, sourceengine.UpdateSourceRequest) (sourceengine.UpdateSourceResponse, error) {
	return sourceengine.UpdateSourceResponse{}, nil
}

func (s *serverSourceEngineStub) DeleteSource(context.Context, string) (sourceengine.DeleteSourceResponse, error) {
	return sourceengine.DeleteSourceResponse{}, nil
}

func (s *serverSourceEngineStub) DeleteSourceByDatasetID(_ context.Context, datasetID string, opts sourceengine.DeleteSourceOptions) (sourceengine.DeleteSourceResponse, error) {
	s.deleteByDatasetCalls++
	s.lastDeleteDatasetID = datasetID
	s.lastDeleteOptions = opts
	return sourceengine.DeleteSourceResponse{Deleted: true, SourceID: "source-1", RemovedDatasetID: datasetID}, nil
}

func (s *serverSourceEngineStub) AddBinding(context.Context, string, string, sourceengine.BindingInput) (sourceengine.BindingMutationResponse, error) {
	s.addBindingCalls++
	return sourceengine.BindingMutationResponse{}, nil
}

func (s *serverSourceEngineStub) UpdateBinding(context.Context, string, string, string, sourceengine.BindingInput) (sourceengine.BindingMutationResponse, error) {
	return sourceengine.BindingMutationResponse{}, nil
}

func (s *serverSourceEngineStub) DeleteBinding(context.Context, string, string) (sourceengine.DeleteBindingResponse, error) {
	return sourceengine.DeleteBindingResponse{}, nil
}

type serverTargetTreeStub struct {
	childrenCalls int
	lastChildren  tree.TargetTreeChildrenRequest
	searchCalls   int
	lastSearch    tree.TargetTreeSearchRequest
}

type serverTaskPlannerStub struct {
	lastGenerate taskengine.GenerateRequest
}

func (s *serverTaskPlannerStub) GenerateTasks(_ context.Context, req taskengine.GenerateRequest) (taskengine.GenerateResult, error) {
	s.lastGenerate = req
	return taskengine.GenerateResult{RequestedCount: 1, AcceptedCount: 1, TaskIDs: []string{}}, nil
}

func (s *serverTaskPlannerStub) GeneratePendingTasks(context.Context, taskengine.GeneratePendingRequest) (taskengine.GenerateResult, error) {
	return taskengine.GenerateResult{}, nil
}

func (s *serverTaskPlannerStub) ExpediteTasks(context.Context, taskengine.ExpediteRequest) (taskengine.ExpediteResult, error) {
	return taskengine.ExpediteResult{}, nil
}

func (s *serverTaskPlannerStub) RetryTask(context.Context, taskengine.RetryRequest) (taskengine.ParseTaskDetailResponse, error) {
	return taskengine.ParseTaskDetailResponse{}, nil
}

func (s *serverTargetTreeStub) ListChildren(_ context.Context, req tree.TargetTreeChildrenRequest) (tree.TreeNodePage, error) {
	s.childrenCalls++
	s.lastChildren = req
	return tree.TreeNodePage{Items: []tree.TreeNode{{Key: "node-1", DisplayName: "Node", IsContainer: true, HasChildren: true}}}, nil
}

func (s *serverTargetTreeStub) Search(_ context.Context, req tree.TargetTreeSearchRequest) (tree.TreeNodePage, error) {
	s.searchCalls++
	s.lastSearch = req
	return tree.TreeNodePage{}, nil
}

type serverSourceTreeStub struct {
	childrenCalls int
	lastChildren  tree.SourceTreeChildrenRequest
	searchCalls   int
	lastSearch    tree.SourceTreeSearchRequest
}

func (s *serverSourceTreeStub) ListChildren(_ context.Context, req tree.SourceTreeChildrenRequest) (tree.TreeNodePage, error) {
	s.childrenCalls++
	s.lastChildren = req
	return tree.TreeNodePage{Items: []tree.TreeNode{{Key: "binding-1", DisplayName: "Binding", BindingID: req.BindingID, SourceID: req.SourceID, IsContainer: true}}}, nil
}

func (s *serverSourceTreeStub) Search(_ context.Context, req tree.SourceTreeSearchRequest) (tree.TreeNodePage, error) {
	s.searchCalls++
	s.lastSearch = req
	return tree.TreeNodePage{}, nil
}

type serverDocumentQueryStub struct {
	calls   int
	lastReq tree.SourceDocumentListRequest
}

func (s *serverDocumentQueryStub) ListDocuments(_ context.Context, req tree.SourceDocumentListRequest) (tree.SourceDocumentListResponse, error) {
	s.calls++
	s.lastReq = req
	return tree.SourceDocumentListResponse{Items: []tree.SourceDocumentItem{{SourceID: req.SourceID, BindingID: req.BindingID, ObjectKey: "doc-1", DisplayName: "Doc"}}}, nil
}

type serverReadRefresherStub struct {
	calls   int
	lastReq tree.SourceReadRefreshRequest
}

func (s *serverReadRefresherStub) RefreshSourceRead(_ context.Context, req tree.SourceReadRefreshRequest) error {
	s.calls++
	s.lastReq = req
	return nil
}

type apiContractConnectorStub struct {
	lastValidate connector.ValidateTargetRequest
}

func (c *apiContractConnectorStub) Spec() connector.ConnectorSpec {
	return connector.ConnectorSpec{
		ConnectorType: localfs.ConnectorType,
		TargetTypes:   []connector.TargetType{localfs.TargetTypeLocalPath},
		MaxPageSize:   100,
	}
}

func (c *apiContractConnectorStub) ValidateTarget(_ context.Context, req connector.ValidateTargetRequest) (connector.NormalizedTarget, error) {
	c.lastValidate = req
	return connector.NormalizedTarget{
		TargetType:        req.TargetType,
		TargetRef:         req.TargetRef,
		TargetFingerprint: "fp-1",
		DisplayName:       "Docs",
		RootObjectKey:     "root-1",
	}, nil
}

func (c *apiContractConnectorStub) ListChildren(context.Context, connector.ListChildrenRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, nil
}

func (c *apiContractConnectorStub) Search(context.Context, connector.SearchRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, nil
}

func (c *apiContractConnectorStub) FetchPage(context.Context, connector.FetchPageRequest) (connector.RawObjectPage, error) {
	return connector.RawObjectPage{}, nil
}

func (c *apiContractConnectorStub) ExportObject(context.Context, connector.ExportObjectRequest) (connector.ExportedObject, error) {
	return connector.ExportedObject{}, nil
}

func (c *apiContractConnectorStub) MapObject(context.Context, connector.RawObject) (connector.NormalizedSourceObject, error) {
	return connector.NormalizedSourceObject{}, nil
}

type apiContractLocalAgentStub struct {
	lastValidate localfs.ValidatePathRequest
	lastList     localfs.ListDirRequest
}

func (a *apiContractLocalAgentStub) ValidatePath(_ context.Context, req localfs.ValidatePathRequest) (localfs.PathInfo, error) {
	a.lastValidate = req
	return localfs.PathInfo{
		Path:           req.Path,
		NormalizedPath: req.Path,
		DisplayName:    "docs",
		Exists:         true,
		Readable:       true,
		IsDir:          true,
	}, nil
}

func (a *apiContractLocalAgentStub) ListDir(_ context.Context, req localfs.ListDirRequest) (localfs.ListDirPage, error) {
	a.lastList = req
	return localfs.ListDirPage{Items: []localfs.PathInfo{{
		Path:           req.Path + "/guides",
		NormalizedPath: req.Path + "/guides",
		DisplayName:    "guides",
		Exists:         true,
		Readable:       true,
		IsDir:          true,
	}}}, nil
}

func (a *apiContractLocalAgentStub) StatPath(context.Context, localfs.StatPathRequest) (localfs.PathInfo, error) {
	return localfs.PathInfo{}, nil
}

func (a *apiContractLocalAgentStub) ExportFile(context.Context, localfs.ExportFileRequest) (localfs.ExportedFile, error) {
	return localfs.ExportedFile{}, nil
}

var _ sourceengine.Engine = (*serverSourceEngineStub)(nil)
var _ tree.TargetTreeEngine = (*serverTargetTreeStub)(nil)
var _ tree.SourceTreeQueryEngine = (*serverSourceTreeStub)(nil)
var _ tree.SourceDocumentQuery = (*serverDocumentQueryStub)(nil)
var _ connector.SourceConnector = (*apiContractConnectorStub)(nil)
var _ localfs.AgentClient = (*apiContractLocalAgentStub)(nil)
