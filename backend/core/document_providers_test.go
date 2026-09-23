package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

func providerItem(id string, capabilities ...string) map[string]any {
	caps := make([]any, len(capabilities))
	for i, value := range capabilities {
		caps[i] = value
	}
	return map[string]any{"id": id, "capabilities": caps}
}
func providerCatalog(items ...map[string]any) map[string]any {
	providers := make([]any, len(items))
	for i, item := range items {
		providers[i] = item
	}
	return map[string]any{"providers": providers}
}

type providerCatalogServer struct {
	mu                 sync.Mutex
	result             any
	raw                string
	rawSet             bool
	status             int
	gate               *portableGate
	calls, inspections int
	url                string
}

func newProviderCatalogServer(t *testing.T, result any) *providerCatalogServer {
	t.Helper()
	spy := &providerCatalogServer{result: result}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/document:inspect" && r.Method == "POST" {
			spy.mu.Lock()
			spy.inspections++
			spy.mu.Unlock()
			_, _ = w.Write([]byte(descriptorMarkdown))
			return
		}
		if r.Method != "GET" || r.URL.Path != "/api/document/providers" {
			t.Errorf("unexpected provider discovery I/O: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if len(raw) != 0 || r.URL.RawQuery != "" {
			t.Error("client input forwarded to Algorithm")
		}
		for _, key := range []string{"Authorization", "Cookie", "X-User-Id", "X-LazyMind-External-Ref", "X-LazyMind-External-Lease", "X-LazyMind-Invocation-Conversation-Id"} {
			if r.Header.Get(key) != "" {
				t.Errorf("private client context forwarded: %s", key)
			}
		}
		spy.mu.Lock()
		spy.calls++
		result, status, body, rawSet, gate := spy.result, spy.status, spy.raw, spy.rawSet, spy.gate
		spy.mu.Unlock()
		if gate != nil {
			gate.started <- struct{}{}
			select {
			case <-r.Context().Done():
				gate.cancelled <- struct{}{}
			case <-gate.release:
			}
			return
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		if rawSet {
			_, _ = w.Write([]byte(body))
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	spy.url = server.URL
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	return spy
}
func (s *providerCatalogServer) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }
func providerCatalogRequest(ctx context.Context, f portableFixture, owner, query, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/document-providers"+query, strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("X-User-Id", owner)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}
func requireProviderError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	rewriteError(t, w, status, code)
	for _, private := range []string{"provider-fixture-secret", "private-provider-path", "credential_required", "account_connected"} {
		if strings.Contains(w.Body.String(), private) {
			t.Errorf("private or inferred metadata leaked: %s", private)
		}
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if data, ok := body["data"].(map[string]any); ok {
		if _, exists := data["providers"]; exists {
			t.Error("error returned partial or stale providers")
		}
	}
}

func TestDocumentProvidersDynamicCatalog(t *testing.T) {
	cases := []map[string]any{
		providerCatalog(providerItem("feishu", "load", "create", "replace"), providerItem("github", "append", "revision_check"), providerItem("notion", "patch"), providerItem("obsidian", "create", "media"), providerItem("wechat", "create")),
		providerCatalog(providerItem("custom.writer-v2", "future_capability", "load"), providerItem("lark")),
		providerCatalog(),
	}
	for i, result := range cases {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			f := newPortableFixture(t, "markdown", false)
			spy := newProviderCatalogServer(t, result)
			costs := watchPortableCosts(t, f)
			before := portableState(t, f)
			var queries, writes atomic.Int32
			query := func(*gorm.DB) { queries.Add(1) }
			write := func(*gorm.DB) { writes.Add(1) }
			if err := f.db.Callback().Query().Before("gorm:query").Register("providers-query", query); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Callback().Row().Before("gorm:row").Register("providers-row", query); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Callback().Raw().Before("gorm:raw").Register("providers-raw", query); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Callback().Create().Before("gorm:create").Register("providers-create", write); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Callback().Update().Before("gorm:update").Register("providers-update", write); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Callback().Delete().Before("gorm:delete").Register("providers-delete", write); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				f.db.Callback().Query().Remove("providers-query")
				f.db.Callback().Row().Remove("providers-row")
				f.db.Callback().Raw().Remove("providers-raw")
				f.db.Callback().Create().Remove("providers-create")
				f.db.Callback().Update().Remove("providers-update")
				f.db.Callback().Delete().Remove("providers-delete")
			})
			data := rewriteData(t, providerCatalogRequest(t.Context(), f, "catalog-reader", "", "", map[string]string{"Authorization": "Bearer provider-fixture-secret", "Cookie": "session=provider-fixture-secret", "X-LazyMind-Invocation-Conversation-Id": "unrelated-conversation"}))
			if !reflect.DeepEqual(data, result) {
				t.Errorf("dynamic providers changed order, identity or capability: %#v want=%#v", data, result)
			}
			if queries.Load() != 0 || writes.Load() != 0 || spy.inspections != 0 {
				t.Errorf("catalog depended on DB/artifact: queries=%d writes=%d inspect=%d", queries.Load(), writes.Load(), spy.inspections)
			}
			requirePortableCosts(t, costs)
			if spy.count() != 1 || portableState(t, f) != before {
				t.Error("catalog had extra I/O or persistence")
			}
		})
	}
}

func TestDocumentProvidersNeedsNoDatabase(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	result := providerCatalog(providerItem("future-provider", "future_capability"))
	spy := newProviderCatalogServer(t, result)
	corestore.Init(nil, nil, nil)
	defer corestore.Init(f.db.DB, nil, nil)
	data := rewriteData(t, providerCatalogRequest(t.Context(), f, "catalog-reader", "", "", nil))
	if !reflect.DeepEqual(data, result) || spy.count() != 1 {
		t.Error("global catalog requires local Artifact/account state")
	}
}

func TestDocumentProvidersRefreshAndFailureDoesNotUseCache(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	spy := newProviderCatalogServer(t, providerCatalog(providerItem("old", "load")))
	before := portableState(t, f)
	for _, result := range []map[string]any{providerCatalog(providerItem("old", "load")), providerCatalog(providerItem("new-plugin", "future_capability")), providerCatalog()} {
		spy.mu.Lock()
		spy.result = result
		spy.mu.Unlock()
		data := rewriteData(t, providerCatalogRequest(t.Context(), f, "descriptor-owner", "", "", nil))
		if !reflect.DeepEqual(data, result) {
			t.Errorf("stale catalog=%#v want=%#v", data, result)
		}
	}
	spy.mu.Lock()
	spy.status = 503
	spy.result = map[string]any{"detail": "provider-fixture-secret private-provider-path"}
	spy.mu.Unlock()
	requireProviderError(t, providerCatalogRequest(t.Context(), f, "descriptor-owner", "", "", nil), 502, "DOCUMENT_PROVIDERS_UNAVAILABLE")
	if spy.count() != 4 || portableState(t, f) != before {
		t.Error("cached catalog or state change")
	}
}

func TestDocumentProvidersProjectsOnlyPublicFields(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	item := providerItem("custom", "load")
	item["credential_required"] = false
	item["account_connected"] = true
	item["token"] = "provider-fixture-secret"
	result := providerCatalog(item)
	result["internal_config"] = map[string]any{"path": "private-provider-path"}
	spy := newProviderCatalogServer(t, result)
	w := providerCatalogRequest(t.Context(), f, "descriptor-owner", "", "", nil)
	data := rewriteData(t, w)
	if !reflect.DeepEqual(data, providerCatalog(providerItem("custom", "load"))) || spy.count() != 1 {
		t.Errorf("public projection=%#v", data)
	}
	for _, private := range []string{"provider-fixture-secret", "private-provider-path", "credential_required", "account_connected"} {
		if strings.Contains(w.Body.String(), private) {
			t.Errorf("undocumented metadata exposed: %s", private)
		}
	}
}

func TestDocumentProvidersRejectsInputBeforeIO(t *testing.T) {
	for _, kind := range []string{"identity", "blank identity", "query", "body"} {
		t.Run(kind, func(t *testing.T) {
			f := newPortableFixture(t, "markdown", false)
			spy := newProviderCatalogServer(t, providerCatalog())
			costs := watchPortableCosts(t, f)
			owner, query, body := "descriptor-owner", "", ""
			code := "DOCUMENT_PROVIDERS_INVALID"
			switch kind {
			case "identity":
				owner = ""
				code = "IDENTITY_REQUIRED"
			case "blank identity":
				owner = "   "
				code = "IDENTITY_REQUIRED"
			case "query":
				query = "?provider=feishu&url=https://example.invalid/private"
			case "body":
				body = `{"tool_config":{"token":"provider-fixture-secret"}}`
			}
			before := portableState(t, f)
			requireProviderError(t, providerCatalogRequest(t.Context(), f, owner, query, body, nil), 400, code)
			if spy.count() != 0 || portableState(t, f) != before {
				t.Error("invalid discovery input reached Algorithm or persisted")
			}
			requirePortableCosts(t, costs)
		})
	}
}

func TestDocumentProvidersMalformedResponse(t *testing.T) {
	invalid := map[string]string{
		"empty body": "", "invalid JSON": `{"providers":`, "missing providers": `{}`, "null providers": `{"providers":null}`, "wrong providers": `{"providers":{}}`,
		"null item": `{"providers":[null]}`, "missing id": `{"providers":[{"capabilities":[]}]}`, "null id": `{"providers":[{"id":null,"capabilities":[]}]}`, "empty id": `{"providers":[{"id":"","capabilities":[]}]}`, "number id": `{"providers":[{"id":1,"capabilities":[]}]}`,
		"missing capabilities": `{"providers":[{"id":"valid"}]}`, "null capabilities": `{"providers":[{"id":"valid","capabilities":null}]}`, "wrong capabilities": `{"providers":[{"id":"valid","capabilities":"load"}]}`,
		"null capability": `{"providers":[{"id":"valid","capabilities":[null]}]}`, "empty capability": `{"providers":[{"id":"valid","capabilities":[""]}]}`, "number capability": `{"providers":[{"id":"valid","capabilities":[7]}]}`,
		"blank id":         `{"providers":[{"id":"  ","capabilities":[]}]}`,
		"blank capability": `{"providers":[{"id":"valid","capabilities":["  "]}]}`,
		"duplicate id":     `{"providers":[{"id":"same","capabilities":[]},{"id":"same","capabilities":[]}]}`, "duplicate capability": `{"providers":[{"id":"valid","capabilities":["load","load"]}]}`,
		"mixed valid invalid": `{"providers":[{"id":"good","capabilities":["load"]},{"id":"bad","capabilities":null}]}`,
	}
	for kind, body := range invalid {
		t.Run(kind, func(t *testing.T) {
			f := newPortableFixture(t, "markdown", false)
			spy := newProviderCatalogServer(t, nil)
			spy.raw = body
			spy.rawSet = true
			before := portableState(t, f)
			requireProviderError(t, providerCatalogRequest(t.Context(), f, "descriptor-owner", "", "", nil), 502, "DOCUMENT_PROVIDERS_RESULT_INVALID")
			if spy.count() != 1 || portableState(t, f) != before {
				t.Error("malformed response boundary or persistence")
			}
		})
	}
}

func TestDocumentProvidersUpstreamFailures(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := newPortableFixture(t, "markdown", false)
			spy := newProviderCatalogServer(t, map[string]any{"detail": "private-upstream-detail provider-fixture-secret private-provider-path"})
			spy.status = status
			before := portableState(t, f)
			requireProviderError(t, providerCatalogRequest(t.Context(), f, "descriptor-owner", "", "", nil), 502, "DOCUMENT_PROVIDERS_UNAVAILABLE")
			if spy.count() != 1 || portableState(t, f) != before {
				t.Error("upstream failure retried or persisted")
			}
		})
	}
	t.Run("connection failure", func(t *testing.T) {
		f := newPortableFixture(t, "markdown", false)
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()
		t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
		before := portableState(t, f)
		requireProviderError(t, providerCatalogRequest(t.Context(), f, "descriptor-owner", "", "", nil), 502, "DOCUMENT_PROVIDERS_UNAVAILABLE")
		if portableState(t, f) != before {
			t.Error("connection failure persisted")
		}
	})
}

func TestDocumentProvidersCancellation(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	spy := newProviderCatalogServer(t, providerCatalog())
	gate := &portableGate{started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1), release: make(chan struct{})}
	spy.gate = gate
	t.Cleanup(func() { close(gate.release) })
	before := portableState(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- providerCatalogRequest(ctx, f, "descriptor-owner", "", "", nil) }()
	select {
	case <-gate.started:
	case w := <-done:
		t.Fatalf("catalog never reached Algorithm: %d", w.Code)
	case <-time.After(2 * time.Second):
		t.Fatal("catalog did not start")
	}
	cancel()
	select {
	case <-gate.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("catalog cancellation did not propagate")
	}
	select {
	case w := <-done:
		requireProviderError(t, w, 502, "DOCUMENT_PROVIDERS_UNAVAILABLE")
	case <-time.After(2 * time.Second):
		t.Fatal("catalog cancellation did not finish")
	}
	if portableState(t, f) != before {
		t.Error("cancel changed local state")
	}
}

func TestDocumentProvidersReadLeaseRemainsRestricted(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	spy := newProviderCatalogServer(t, providerCatalog())
	if err := f.db.AutoMigrate(&orm.ExternalChatRun{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiry := now.Add(time.Minute)
	run := orm.ExternalChatRun{ID: "providers-run", RequestID: "providers-request", ConversationID: "descriptor-conversation", HistoryID: "providers-history", Provider: "codex", ActorUserID: "descriptor-owner", Status: "running", HostID: "providers-host", LeaseToken: "providers-test-lease", LeaseExpiresAt: &expiry, CreatedAt: now, UpdatedAt: now}
	if err := f.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	before := portableState(t, f)
	headers := map[string]string{"X-LazyMind-External-Ref": run.ID, "X-LazyMind-External-Lease": run.LeaseToken, "X-LazyMind-External-Host": run.HostID, "X-LazyMind-Conversation-Id": run.ConversationID}
	w := providerCatalogRequest(t.Context(), f, "descriptor-owner", "", "", headers)
	if w.Code != 409 {
		t.Errorf("read lease status=%d", w.Code)
	}
	if spy.count() != 0 || portableState(t, f) != before {
		t.Error("lease lookup reached Algorithm or persisted")
	}
}

func TestDocumentProvidersDoesNotEnablePublish(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	spy := newProviderCatalogServer(t, providerCatalog(providerItem("obsidian", "create", "replace", "revision_check")))
	before := descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/descriptor-artifact", "descriptor-owner"))[0]["document"]
	rewriteData(t, providerCatalogRequest(t.Context(), f, "descriptor-owner", "", "", nil))
	after := descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/descriptor-artifact", "descriptor-owner"))[0]["document"]
	if !reflect.DeepEqual(before, after) || containsPortable(schemaStringList(after.(map[string]any)["capabilities"]), "publish") {
		t.Error("directory visibility changed publication eligibility")
	}
	if spy.count() != 1 {
		t.Error("descriptor invoked provider discovery implicitly")
	}
}

func TestDocumentProvidersFixtureControl(t *testing.T) {
	result := providerCatalog(providerItem("future-provider", "future_capability"))
	spy := newProviderCatalogServer(t, result)
	response, err := http.Get(spy.url + "/api/document/providers")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !reflect.DeepEqual(decoded, result) || spy.count() != 1 {
		t.Error("catalog fixture is not usable")
	}
}

func TestDocumentProvidersOpenAPI(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	raw, err := buildOpenAPISpecFromRouter(f.router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	op := openAPIOperationForTest(t, spec, "get", "/api/core/document-providers")
	if op["requestBody"] != nil {
		t.Error("provider catalog unexpectedly takes a body")
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	resolve := func(value any) map[string]any {
		obj, _ := value.(map[string]any)
		if ref, ok := obj["$ref"].(string); ok {
			obj, _ = schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		}
		return obj
	}
	responses := op["responses"].(map[string]any)
	response := resolve(responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
	data := resolve(response["properties"].(map[string]any)["data"])
	providers := resolve(data["properties"].(map[string]any)["providers"])
	if providers["type"] != "array" || !containsPortable(schemaStringList(data["required"]), "providers") {
		t.Fatalf("providers schema=%#v", data)
	}
	item := resolve(providers["items"])
	props := item["properties"].(map[string]any)
	id := resolve(props["id"])
	caps := resolve(props["capabilities"])
	if len(props) != 2 || id["type"] != "string" || id["enum"] != nil || caps["type"] != "array" || resolve(caps["items"])["type"] != "string" || resolve(caps["items"])["enum"] != nil {
		t.Errorf("dynamic provider schema=%#v", item)
	}
	for _, field := range []string{"id", "capabilities"} {
		if !containsPortable(schemaStringList(item["required"]), field) {
			t.Errorf("provider %s not required", field)
		}
	}
	for status, expected := range map[string][]string{"400": {"IDENTITY_REQUIRED", "DOCUMENT_PROVIDERS_INVALID"}, "502": {"DOCUMENT_PROVIDERS_UNAVAILABLE", "DOCUMENT_PROVIDERS_RESULT_INVALID"}} {
		if responses[status] == nil {
			t.Fatalf("missing error response %s", status)
		}
		response := resolve(responses[status].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
		data := resolve(response["properties"].(map[string]any)["data"])
		code := resolve(data["properties"].(map[string]any)["code"])
		if code["type"] != "string" || !containsPortable(schemaStringList(data["required"]), "code") {
			t.Error("error code is not a required string")
		}
		for _, value := range expected {
			if !containsPortable(schemaStringList(code["enum"]), value) {
				t.Errorf("missing stable error code %s", value)
			}
		}
	}
}
