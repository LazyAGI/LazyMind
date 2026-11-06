package workflowtool

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/academic"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	corestore "lazymind/core/store"
	"lazymind/core/workflow/graphengine"
)

func academicJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }

func academicToolFixture(t *testing.T) *Service {
	t.Helper()
	service, _ := workflowToolFixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("audited academic adapter escaped into Python/model tool service")
		return nil, nil
	})
	if err := service.DB.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		t.Fatal(err)
	}
	if err := service.DB.AutoMigrate(&orm.WorkflowInputResource{}); err != nil {
		t.Fatal(err)
	}
	oldDB, oldLazy, oldState := corestore.DB(), corestore.LazyLLMDB(), corestore.State()
	shared, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	corestore.Init(service.DB, nil, shared)
	t.Cleanup(func() { corestore.Init(oldDB, oldLazy, oldState); _ = shared.Close() })
	oldTransport := http.DefaultTransport
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/scan/internal/source-access/by-dataset:batch":
			return toolResponse(`{"items":[]}`), nil
		case "/api/scan/internal/sources/by-datasets":
			return toolResponse(`{"source_map":{}}`), nil
		default:
			t.Fatalf("unexpected data/model service request: %s", r.URL)
			return nil, nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	var revision orm.WorkflowRevision
	if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["write"]
	node.ExternalExecution.Tools = nil
	for _, name := range []string{"academic_context", "academic_expansion", "academic_search_papers", "academic_read_paper", "academic_resolve_papers"} {
		node.ExternalExecution.Tools = append(node.ExternalExecution.Tools, graphengine.ExternalTool{Name: name, Source: "package"})
	}
	graph.Nodes["write"] = node
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(academicBinding)))
	for _, row := range []any{
		&orm.WorkflowBlob{Hash: hash, Content: []byte(academicBinding), Size: int64(len(academicBinding))},
		&orm.WorkflowRevisionEntry{RevisionID: "pinned-revision", Path: academicBindingPath, EntryType: "file", BlobHash: &hash},
		&orm.Dataset{ID: "papers", KbID: "papers", DisplayName: "Papers", Scene: "academic_papers", Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.Conversation{ID: "chat", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.AcademicExpansion{ID: "expansion", UserID: "owner", DatasetID: "papers", ConversationID: "chat", RequestOwner: "owner", RequestKey: "expansion", Status: "running", DiscoveryMode: "external", PreparationRevision: 1, ResultJSON: json.RawMessage(`{"papers":[]}`), DiscoveryJSON: json.RawMessage(`[{"papers":[{"paper_id":"paper","title":"Registered paper","source_provider":"arxiv","source_version":"v1"}]}]`)},
		&orm.AcademicWork{ID: "paper", CanonicalTitle: "Registered paper", NormalizedTitle: "registered paper", AuthorsJSON: json.RawMessage(`[]`), ExternalIDsJSON: json.RawMessage(`{}`), ProvenanceJSON: json.RawMessage(`{}`)},
		&orm.AcademicPaperContent{ID: "paper-content", UserID: "owner", PaperID: "paper", ParserVersion: orm.AcademicPaperParserVersion, SourceKey: "registered-fixture", SourceProvider: "arxiv", SourceKind: "external", SourceVersion: "v1", Status: "ready", ContentHash: "real-content-hash", PagesJSON: json.RawMessage(`[{"page":1,"text":"Abstract"},{"page":2,"text":"Methods"},{"page":3,"text":"Results"}]`), QualityJSON: json.RawMessage(`{"eligible":true,"identity":"matched","material":"main","text":"usable"}`)},
	} {
		if err := service.DB.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	setAcademicBindingInput(t, service, "expansion")
	return service
}

func setAcademicBindingInput(t *testing.T, service *Service, id string) {
	t.Helper()
	payload := map[string]any{"operation": "execute", "metadata": map[string]string{"workflow_control_inputs_frozen": "true"}}
	if id != "" {
		payload["inputs"] = map[string]any{"expansion_id": map[string]any{"value": map[string]any{"text": id}}}
	}
	if err := service.DB.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", academicJSON(payload)).Error; err != nil {
		t.Fatal(err)
	}
}

func academicCall(t *testing.T, service *Service, command, name string, args map[string]any) Result {
	t.Helper()
	result, err := service.Call(t.Context(), "owner", "run", "attempt", CallInput{ExecutionHandle: "lease", ToolName: name, CommandID: command, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireAcademicSuccess(t *testing.T, result Result) map[string]any {
	t.Helper()
	if result.Error != nil {
		t.Fatalf("academic call: %+v", result.Error)
	}
	value, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("academic result is not object: %+v", result)
	}
	return value
}

func TestAcademicToolsReuseOwnedRegistryAndNativeACL(t *testing.T) {
	service := academicToolFixture(t)
	context := requireAcademicSuccess(t, academicCall(t, service, "context", "academic_context", nil))
	if context["expansion_id"] != "expansion" {
		t.Fatalf("wrong current conversation: %+v", context)
	}
	detail := requireAcademicSuccess(t, academicCall(t, service, "expansion", "academic_expansion", nil))
	if detail["record"].(map[string]any)["id"] != "expansion" {
		t.Fatal("wrong expansion")
	}
	resolved := requireAcademicSuccess(t, academicCall(t, service, "resolve", "academic_resolve_papers", map[string]any{"paper_ids": []string{"paper"}}))
	if len(resolved["papers"].([]any)) != 1 || resolved["contents"].([]any)[0].(map[string]any)["content_hash"] != "real-content-hash" {
		t.Fatal("lost registered identity or version")
	}
	unknown := academicCall(t, service, "unknown", "academic_resolve_papers", map[string]any{"paper_ids": []string{"invented"}})
	if unknown.Error == nil || unknown.Error.Code != "UNREGISTERED_EXPANSION_PAPER" {
		t.Fatalf("accepted invented registry ID: %+v", unknown)
	}
	// The unmodified native endpoint returns the same frozen identity data.
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"paper_ids":["paper"]}`))
	r.Header.Set("X-User-Id", "owner")
	w := httptest.NewRecorder()
	academic.ResolveExpansionPapers(w, mux.SetURLVars(r, map[string]string{"expansion_id": "expansion"}))
	var native any
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &native) != nil || string(academicJSON(native)) != string(academicJSON(resolved)) {
		t.Fatalf("native identity result changed: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	academic.AcademicChatContext(w, httptest.NewRequest(http.MethodGet, "/academic/chat-context?conversation_id=chat", nil))
	if w.Code == http.StatusOK {
		t.Fatal("native missing-user admission changed")
	}
	// Access to the workflow does not bypass permission on the selected dataset.
	if err := service.DB.Model(&orm.Dataset{}).Where("id = ?", "papers").Update("create_user_id", "other").Error; err != nil {
		t.Fatal(err)
	}
	denied := academicCall(t, service, "denied", "academic_resolve_papers", map[string]any{"paper_ids": []string{"paper"}})
	if denied.Error == nil || denied.Error.Code != "FORBIDDEN" {
		t.Fatalf("dataset ACL bypassed: %+v", denied)
	}
	var tasks, documents int64
	service.DB.Model(&orm.SubAgentTask{}).Count(&tasks)
	service.DB.Model(&orm.Document{}).Count(&documents)
	if tasks != 0 || documents != 0 {
		t.Fatalf("read adapter generated/imported content: tasks=%d docs=%d", tasks, documents)
	}
}

func TestAcademicReadPreservesCursorAndOnlyDeliveredReceipts(t *testing.T) {
	service := academicToolFixture(t)
	first := requireAcademicSuccess(t, academicCall(t, service, "read-first", "academic_read_paper", map[string]any{"paper_id": "paper", "max_results": 2}))
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" || len(first["pages"].([]any)) != 2 {
		t.Fatalf("missing genuine continuation: %+v", first)
	}
	assertCoverage := func(want int) {
		var record orm.AcademicExpansion
		if err := service.DB.First(&record, "id = ?", "expansion").Error; err != nil {
			t.Fatal(err)
		}
		var ledger map[string]struct {
			Positions []int  `json:"positions"`
			Hash      string `json:"content_hash"`
		}
		if err := json.Unmarshal(record.ReadingJSON, &ledger); err != nil {
			t.Fatal(err)
		}
		if len(ledger["paper"].Positions) != want || ledger["paper"].Hash != "real-content-hash" {
			t.Fatalf("false read coverage: %+v", ledger)
		}
	}
	assertCoverage(2)
	// Even another owned task's cursor cannot redirect the bound tool.
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatal(err)
	}
	var foreign map[string]any
	if err := json.Unmarshal(raw, &foreign); err != nil {
		t.Fatal(err)
	}
	foreign["expansion_id"] = "other-owned-expansion"
	denied := academicCall(t, service, "foreign-cursor", "academic_read_paper", map[string]any{"cursor": base64.RawURLEncoding.EncodeToString(academicJSON(foreign))})
	if denied.Error == nil || denied.Error.Code != "INVALID_CURSOR" {
		t.Fatalf("foreign cursor accepted: %+v", denied)
	}
	assertCoverage(2)
	last := requireAcademicSuccess(t, academicCall(t, service, "read-last", "academic_read_paper", map[string]any{"cursor": cursor}))
	if len(last["pages"].([]any)) != 1 || last["next_cursor"] != "" {
		t.Fatalf("lost server continuation: %+v", last)
	}
	assertCoverage(3)
	var native struct {
		Pages []any `json:"pages"`
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"expansion_id":"expansion","paper_id":"paper","max_results":2}`))
	r.Header.Set("X-User-Id", "owner")
	w := httptest.NewRecorder()
	academic.ReadPaperContent(w, r)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &native) != nil || len(native.Pages) != 2 {
		t.Fatalf("native read changed: %d %s", w.Code, w.Body.String())
	}
}

func TestAcademicToolsRejectContextInjectionAndUnauditedBinding(t *testing.T) {
	service := academicToolFixture(t)
	for i, key := range []string{"expansion_id", "handoff_id", "conversation_id", "dataset_id", "authorization", "unknown"} {
		result := academicCall(t, service, fmt.Sprintf("inject-%d", i), "academic_read_paper", map[string]any{"paper_id": "paper", key: "other"})
		if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("accepted %s: %+v", key, result)
		}
	}
	setAcademicBindingInput(t, service, "other-owned-expansion")
	result := academicCall(t, service, "different-frozen-input", "academic_expansion", nil)
	if result.Error == nil || result.Error.Code != "ACADEMIC_CONTEXT_UNAVAILABLE" {
		t.Fatal("fixed input escaped current conversation")
	}
	setAcademicBindingInput(t, service, "")
	for _, name := range []string{"academic_expansion", "academic_search_papers", "academic_resolve_papers", "academic_read_paper"} {
		result := academicCall(t, service, "unbound-"+name, name, map[string]any{})
		if result.Error == nil || (result.Error.Code != "ACADEMIC_CONTEXT_UNAVAILABLE" && result.Error.Code != "INVALID_ARGUMENT") {
			t.Fatalf("unbound workflow guessed an academic task: %+v", result)
		}
	}
	setAcademicBindingInput(t, service, "expansion")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(academicBinding)))
	if err := service.DB.Model(&orm.WorkflowBlob{}).Where("hash = ?", hash).Update("content", []byte(academicBinding+"# changed\n")).Error; err != nil {
		t.Fatal(err)
	}
	result = academicCall(t, service, "corrupt-adapter", "academic_read_paper", map[string]any{"paper_id": "paper"})
	if result.Error == nil || result.Error.Code != "INVALID_PACKAGE" {
		t.Fatal("unaudited binding accepted")
	}
	list, err := service.List(t.Context(), "owner", "run", "attempt", "lease")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range list {
		if tool.Available {
			t.Fatal("invalid binding advertised ready")
		}
	}
	var record orm.AcademicExpansion
	if err := service.DB.First(&record, "id = ?", "expansion").Error; err != nil {
		t.Fatal(err)
	}
	if string(record.ReadingJSON) != "{}" {
		t.Fatalf("rejected calls wrote read receipts: %s", record.ReadingJSON)
	}
}

func TestAcademicSearchRegistersProviderResultsAndBoundContinuation(t *testing.T) {
	service := academicToolFixture(t)
	old := http.DefaultTransport
	calls := 0
	http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.openalex.org" {
			return old.RoundTrip(r)
		}
		calls++
		id := "W101"
		if r.URL.Query().Get("page") == "2" {
			id = "W102"
		}
		return toolResponse(fmt.Sprintf(`{"results":[{"id":"https://openalex.org/%s","title":"Paper %s","doi":"https://doi.org/10.1000/%s"}],"meta":{"count":2}}`, id, id, id)), nil
	})
	t.Cleanup(func() { http.DefaultTransport = old })
	first := requireAcademicSuccess(t, academicCall(t, service, "search-first", "academic_search_papers", map[string]any{"query": "agents", "sources": []string{"openalex"}, "max_results": 1}))
	papers := first["papers"].([]any)
	if len(papers) != 1 {
		t.Fatal(first)
	}
	id := papers[0].(map[string]any)["paper_id"].(string)
	var registered orm.AcademicWork
	if err := service.DB.First(&registered, "id = ?", id).Error; err != nil {
		t.Fatal("provider result not registered", err)
	}
	cursor := first["sources"].([]any)[0].(map[string]any)["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("no genuine provider continuation")
	}
	second := requireAcademicSuccess(t, academicCall(t, service, "search-next", "academic_search_papers", map[string]any{"cursor": cursor}))
	if second["papers"].([]any)[0].(map[string]any)["paper_id"] == id || calls != 2 {
		t.Fatalf("continuation not followed: %+v calls=%d", second, calls)
	}
	resolved := requireAcademicSuccess(t, academicCall(t, service, "resolve-searched", "academic_resolve_papers", map[string]any{"paper_ids": []string{id}}))
	if len(resolved["papers"].([]any)) != 1 {
		t.Fatal("search result missing task registration")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(cursor)
	var foreign map[string]any
	_ = json.Unmarshal(raw, &foreign)
	foreign["expansion_id"] = "other-owned-expansion"
	denied := academicCall(t, service, "search-foreign", "academic_search_papers", map[string]any{"cursor": base64.RawURLEncoding.EncodeToString(academicJSON(foreign))})
	if denied.Error == nil || denied.Error.Code != "INVALID_CURSOR" || calls != 2 {
		t.Fatalf("foreign search cursor escaped scope: %+v", denied)
	}
}

func TestAcademicReadUsesOnlyCurrentConversationHandoff(t *testing.T) {
	service := academicToolFixture(t)
	setAcademicBindingInput(t, service, "")
	if err := service.DB.Model(&orm.AcademicExpansion{}).Where("id = ?", "expansion").Update("conversation_id", "prior-chat").Error; err != nil {
		t.Fatal(err)
	}
	var content orm.AcademicPaperContent
	if err := service.DB.First(&content, "id = ?", "paper-content").Error; err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"papers":[{"paper_id":"paper","title":"Registered paper","source_provider":"arxiv","source_version":"v1"}]}`)
	snapshot := map[string]any{"sources": []any{map[string]any{"paper": map[string]any{"paper_id": "paper", "title": "Registered paper", "source_provider": "arxiv", "source_version": "v1"}, "dataset_id": "papers"}}, "contents": []orm.AcademicPaperContent{content}}
	if err := service.DB.Create(&orm.AcademicHandoff{ID: "current-handoff", DatasetID: "papers", UserID: "owner", ConversationID: "chat", Kind: "reflection", RequestKey: "handoff", InputJSON: input, ContentsJSON: academicJSON(snapshot)}).Error; err != nil {
		t.Fatal(err)
	}
	first := requireAcademicSuccess(t, academicCall(t, service, "handoff-read", "academic_read_paper", map[string]any{"paper_id": "paper", "max_results": 2}))
	cursor := first["next_cursor"].(string)
	raw, _ := base64.RawURLEncoding.DecodeString(cursor)
	var foreign map[string]any
	_ = json.Unmarshal(raw, &foreign)
	if foreign["handoff_id"] != "current-handoff" {
		t.Fatal("handoff was not service bound")
	}
	foreign["handoff_id"] = "another-owned-handoff"
	denied := academicCall(t, service, "handoff-foreign", "academic_read_paper", map[string]any{"cursor": base64.RawURLEncoding.EncodeToString(academicJSON(foreign))})
	if denied.Error == nil || denied.Error.Code != "INVALID_CURSOR" {
		t.Fatalf("cross-handoff cursor accepted: %+v", denied)
	}
	requireAcademicSuccess(t, academicCall(t, service, "handoff-next", "academic_read_paper", map[string]any{"cursor": cursor}))
	if err := service.DB.Create(&orm.AcademicHandoff{ID: "second-handoff", DatasetID: "papers", UserID: "owner", ConversationID: "chat", Kind: "reflection", RequestKey: "second-handoff", InputJSON: input, ContentsJSON: academicJSON(snapshot)}).Error; err != nil {
		t.Fatal(err)
	}
	ambiguous := academicCall(t, service, "ambiguous-handoff", "academic_read_paper", map[string]any{"paper_id": "paper"})
	if ambiguous.Error == nil || ambiguous.Error.Code != "ACADEMIC_CONTEXT_AMBIGUOUS" {
		t.Fatalf("arbitrary handoff chosen: %+v", ambiguous)
	}
	if err := service.DB.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", json.RawMessage(`{"inputs":{"handoff_id":{"value":{"text":"current-handoff"}}},"metadata":{"workflow_control_inputs_frozen":"true"}}`)).Error; err != nil {
		t.Fatal(err)
	}
	requireAcademicSuccess(t, academicCall(t, service, "fixed-handoff", "academic_read_paper", map[string]any{"paper_id": "paper"}))
}

func TestAcademicHTTPRetainsTenantPermissionAndRejectsInvalidTypes(t *testing.T) {
	service := academicToolFixture(t)
	if err := service.DB.Model(&orm.Dataset{}).Where("id = ?", "papers").Update("tenant_id", "tenant-a").Error; err != nil {
		t.Fatal(err)
	}
	input := CallInput{ExecutionHandle: "lease", ToolName: "academic_resolve_papers", CommandID: "tenant-call", Arguments: map[string]any{"paper_ids": []string{"paper"}}}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(academicJSON(input))))
	r.Header.Set("X-User-Id", "owner")
	r.Header.Set("X-Tenant-Id", "tenant-a")
	r = mux.SetURLVars(r, map[string]string{"session_id": "run", "attempt_id": "attempt"})
	w := httptest.NewRecorder()
	(Handler{Service: service}).Call(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("authenticated dataset caller lost: %d %s", w.Code, w.Body.String())
	}
	for i, args := range []map[string]any{{"paper_id": 123}, {"paper_id": "paper", "max_results": 1.5}, {"cursor": nil}, {"paper_id": "paper", "positions": []any{"one"}}} {
		result := academicCall(t, service, fmt.Sprintf("wrong-type-%d", i), "academic_read_paper", args)
		if result.Error == nil || result.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("invalid type accepted: %+v", result)
		}
	}
}

func TestAcademicMissingSourceConfigurationRemainsExplicit(t *testing.T) {
	service := academicToolFixture(t)
	result := requireAcademicSuccess(t, academicCall(t, service, "missing-web", "academic_search_papers", map[string]any{"query": "agents", "sources": []string{"web"}}))
	if result["sources"].([]any)[0].(map[string]any)["status"] != "missing_configuration" || !strings.Contains(result["retry_guidance"].(string), "LazyMind") {
		t.Fatalf("missing capability disguised as success: %+v", result)
	}
	if len(result["papers"].([]any)) != 0 {
		t.Fatal("unconfigured source fabricated papers")
	}
}

func TestAcademicTaskInputResourceChecksOwnerRevisionAndContent(t *testing.T) {
	service := academicToolFixture(t)
	content := []byte("expansion\n")
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	resource := orm.WorkflowInputResource{ID: "task-id-resource", OwnerUserID: "owner", Name: "task.txt", MimeType: "text/plain", Size: int64(len(content)), Revision: 3, ContentHash: hash, Content: content}
	if err := service.DB.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	setResource := func(revision string) {
		payload := map[string]any{"inputs": map[string]any{"expansion_id": map[string]any{"source_type": "input_resource", "source_id": resource.ID, "source_revision": revision, "content_hash": hash}}, "metadata": map[string]string{"workflow_control_inputs_frozen": "true"}}
		if err := service.DB.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", academicJSON(payload)).Error; err != nil {
			t.Fatal(err)
		}
	}
	setResource("3")
	result := requireAcademicSuccess(t, academicCall(t, service, "resource-ok", "academic_expansion", nil))
	if result["record"].(map[string]any)["id"] != "expansion" {
		t.Fatal("text resource not resolved")
	}
	setResource("2")
	changed := academicCall(t, service, "resource-revision", "academic_expansion", nil)
	if changed.Error == nil || changed.Error.Code != "INPUT_VERSION_CHANGED" {
		t.Fatal("stale input revision accepted")
	}
	setResource("3")
	if err := service.DB.Model(&resource).Update("content", []byte("other-owned-task")).Error; err != nil {
		t.Fatal(err)
	}
	changed = academicCall(t, service, "resource-content", "academic_expansion", nil)
	if changed.Error == nil || changed.Error.Code != "INPUT_VERSION_CHANGED" {
		t.Fatal("corrupt input content accepted")
	}
	if err := service.DB.Model(&resource).Updates(map[string]any{"content": content, "owner_user_id": "other"}).Error; err != nil {
		t.Fatal(err)
	}
	changed = academicCall(t, service, "resource-owner", "academic_expansion", nil)
	if changed.Error == nil || changed.Error.Code != "ACADEMIC_CONTEXT_UNAVAILABLE" {
		t.Fatal("foreign resource accepted")
	}
}
