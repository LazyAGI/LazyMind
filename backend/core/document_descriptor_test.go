package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

const descriptorIRSchema = "application/vnd.lazymind.writer+json"
const descriptorMarkdown = `{"is_document":true,"representation":"markdown","schema":"text/markdown","features":{"headings":true,"numbering":true,"cross_references":true,"provider_binding":false}}`
const descriptorIR = `{"is_document":true,"representation":"ir","schema":"application/vnd.lazymind.writer+json","features":{"headings":true,"numbering":true,"cross_references":true,"provider_binding":true}}`
const descriptorNonDocument = `{"is_document":false,"representation":null,"schema":null,"features":{"headings":false,"numbering":false,"cross_references":false,"provider_binding":false}}`

// Tests exercise real Core GET routes and storage. Only the Algorithm HTTP boundary is a fixture.
type descriptorFixture struct {
	db     *orm.DB
	router *mux.Router
}

func newDescriptorFixture(t *testing.T) descriptorFixture {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{},
		&orm.WorkflowSlotRevision{}, &orm.WorkflowHumanArtifact{}, &orm.SubAgentTask{},
		&orm.SubAgentArtifact{}, &orm.WorkflowSlotOrder{}, &orm.WorkflowStepIntent{},
		&orm.WorkflowEvent{}, &orm.WorkflowAttemptInputBinding{}, &orm.WorkflowRouteDecision{}, &orm.WorkflowResource{}, &orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{})
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowSession{ID: "descriptor-session", ConversationID: "descriptor-conversation",
		WorkflowID: "unseen-workflow", CreateUserID: "descriptor-owner", Status: "active", StateVersion: 9,
		CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	registerCoreRoutes(router)
	return descriptorFixture{db: db, router: router}
}

func (f descriptorFixture) seed(t *testing.T, id, slot, contentType, value string) {
	t.Helper()
	humanID := id + "-human"
	now := time.Now().UTC()
	if err := f.db.Create(&orm.WorkflowHumanArtifact{ID: humanID, SessionID: "descriptor-session", Slot: slot,
		ContentType: contentType, Value: json.RawMessage(value), DraftVersion: 7, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&orm.WorkflowSlotRevision{ID: id, SessionID: "descriptor-session", SlotID: slot, Slot: slot,
		Revision: 3, Selected: true, Validity: "effective", HumanArtifactID: &humanID, StepID: "source", Attempt: 1,
		ChangeSource: "agent", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
}

func (f descriptorFixture) read(ctx context.Context, path, owner string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	req.Header.Set("X-User-Id", owner)
	req.Header.Set("Workflow-Contract-Version", "workflow.v1")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

var descriptorRoutes = []string{
	"/workflow-sessions/descriptor-session/slots",
	"/workflow-sessions/descriptor-session",
	"/conversations/descriptor-conversation/workflow-sessions:active",
	"/conversations/descriptor-conversation/workflow-sessions:latest",
	"/workflow-sessions/descriptor-session/artifacts",
	"/workflow-artifacts/descriptor-artifact",
	"/workflow-sessions/descriptor-session/slots/unknown-slot/items/idx/-1/versions",
}

// Locate records in the existing Panel and Facade response envelopes, without binding tests to Go DTO names.
func descriptorOptionalRecords(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var result []map[string]any
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if _, ok := v["revision"]; ok {
				result = append(result, v)
				return
			}
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(body)
	return result
}
func descriptorRecords(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	records := descriptorOptionalRecords(t, w)
	if len(records) == 0 {
		t.Fatalf("no artifact records: %s", w.Body.String())
	}
	return records
}

type descriptorInspectSpy struct {
	mu       sync.Mutex
	requests []map[string]json.RawMessage
}

func descriptorAlgorithm(t *testing.T, status int, response string) *descriptorInspectSpy {
	t.Helper()
	spy := &descriptorInspectSpy{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/document:inspect" {
			t.Errorf("unexpected Algorithm call %s %s", r.Method, r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		for key := range body {
			if key != "artifact" && key != "schema" {
				t.Errorf("Inspect must not carry %s", key)
			}
		}
		spy.mu.Lock()
		spy.requests = append(spy.requests, body)
		spy.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	return spy
}
func (s *descriptorInspectSpy) calls() []map[string]json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]json.RawMessage(nil), s.requests...)
}
func requireDescriptor(t *testing.T, record map[string]any, representation string, editable bool, copyEligibility ...bool) {
	t.Helper()
	doc, ok := record["document"].(map[string]any)
	if !ok {
		t.Fatalf("missing document: %#v", record)
	}
	schema := "text/markdown"
	if representation == "ir" {
		schema = descriptorIRSchema
	}
	if doc["representation"] != representation || doc["schema"] != schema || doc["editable"] != editable {
		t.Errorf("document=%#v", doc)
	}
	copyable := editable
	if len(copyEligibility) > 0 {
		copyable = copyEligibility[0]
	}
	want := []any{}
	if editable {
		want = []any{"save"}
	}
	if copyable {
		want = append(want, "convert_document", "numbering", "cross_reference")
	}
	if editable {
		want = append(want, "publish_document")
	}
	if !reflect.DeepEqual(doc["capabilities"], want) {
		t.Errorf("capabilities=%#v want=%#v", doc["capabilities"], want)
	}
	if record["document_error"] != nil {
		t.Errorf("successful document has error=%#v", record["document_error"])
	}
	if doc["provider_synced"] == true {
		t.Error("Inspect binding/features cannot establish provider sync")
	}
}
func descriptorSnapshot(t *testing.T, f descriptorFixture) string {
	t.Helper()
	var sessions []orm.WorkflowSession
	var revisions []orm.WorkflowSlotRevision
	var humans []orm.WorkflowHumanArtifact
	var events []orm.WorkflowEvent
	for _, model := range []any{&sessions, &revisions, &humans, &events} {
		if err := f.db.Order("id").Find(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal([]any{sessions, revisions, humans, events})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func requireDescriptorReadOnly(t *testing.T, f descriptorFixture) {
	t.Helper()
	before := descriptorSnapshot(t, f)
	t.Cleanup(func() {
		if after := descriptorSnapshot(t, f); after != before {
			t.Errorf("read changed persisted session/revision/value/event\nbefore=%s\nafter=%s", before, after)
		}
	})
}

func TestDocumentDescriptorAllReadEntrypoints(t *testing.T) {
	for _, path := range descriptorRoutes {
		t.Run(path, func(t *testing.T) {
			f := newDescriptorFixture(t)
			f.seed(t, "descriptor-artifact", "unknown-slot", "text", `{"schema":"text/markdown","data":"Short prose."}`)
			spy := descriptorAlgorithm(t, 200, descriptorMarkdown)
			requireDescriptorReadOnly(t, f)
			records := descriptorRecords(t, f.read(t.Context(), path, "descriptor-owner"))
			if len(records) != 1 {
				t.Fatalf("records=%#v", records)
			}
			if records[0]["artifact_id"] != "descriptor-artifact" || records[0]["draft_version"] != float64(7) || records[0]["revision"] != float64(3) {
				t.Errorf("identity/baseline=%#v", records[0])
			}
			requireDescriptor(t, records[0], "markdown", true)
			calls := spy.calls()
			if len(calls) != 1 {
				t.Fatalf("Inspect calls=%d want 1", len(calls))
			}
			var artifact any
			_ = json.Unmarshal(calls[0]["artifact"], &artifact)
			if artifact != "Short prose." || string(calls[0]["schema"]) != `"text/markdown"` {
				t.Errorf("Inspect body=%s", mustDescriptorJSON(t, calls[0]))
			}
		})
	}
}
func mustDescriptorJSON(t *testing.T, v any) string {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestDocumentDescriptorCarrierRecognition(t *testing.T) {
	cases := []struct{ name, ct, value, representation, response, artifact, schema, returned string }{
		{"mime root", "text/markdown", `"plain prose"`, "markdown", descriptorMarkdown, `"plain prose"`, "text/markdown", `{"text":"plain prose"}`},
		{"mime text", "text/markdown", `{"text":"plain prose"}`, "markdown", descriptorMarkdown, `"plain prose"`, "text/markdown", ""},
		{"schema_name envelope", "json", `{"schema_name":"text/markdown","data":"plain prose"}`, "markdown", descriptorMarkdown, `"plain prose"`, "text/markdown", ""},
		{"logical markdown", "markdown", `{"text":"plain prose"}`, "markdown", descriptorMarkdown, `"plain prose"`, "text/markdown", ""},
		{"writer ir", "json", `{"document_id":"doc-one","blocks":[],"provider_binding":{"provider":"future-provider"}}`, "ir", descriptorIR, `{"document_id":"doc-one","blocks":[],"provider_binding":{"provider":"future-provider"}}`, "", ""},
		{"writer envelope", "json", `{"schema_name":"lazyllm.tools.writer.data_models.writer_ir.WriterDocument","data":{"document_id":"doc-one","blocks":[]}}`, "ir", descriptorIR, `{"document_id":"doc-one","blocks":[]}`, descriptorIRSchema, ""},
		{"ordinary root", "text", `"# status heading is not a declaration"`, "", descriptorMarkdown, "", "", `{"text":"# status heading is not a declaration"}`},
		{"ordinary text", "text", `{"text":"# status heading"}`, "", descriptorMarkdown, "", "", ""},
		{"ordinary json", "json", `{"status":"done"}`, "", descriptorNonDocument, `{"status":"done"}`, "", ""},
		{"fake ir", "json", `{"document_id":"fake","blocks":"not-an-array"}`, "", descriptorNonDocument, `{"document_id":"fake","blocks":"not-an-array"}`, "", ""},
		{"unknown schema", "text/markdown", `{"schema":"application/x-unknown","data":"# heading"}`, "", descriptorMarkdown, "", "", ""},
		{"plain mime", "text/plain", `{"text":"# heading"}`, "", descriptorMarkdown, "", "", ""},
	}
	for _, tc := range cases {
		for _, endpoint := range []string{descriptorRoutes[0], descriptorRoutes[4], descriptorRoutes[5]} {
			t.Run(tc.name+endpoint, func(t *testing.T) {
				f := newDescriptorFixture(t)
				f.seed(t, "descriptor-artifact", "unknown-slot", tc.ct, tc.value)
				spy := descriptorAlgorithm(t, 200, tc.response)
				requireDescriptorReadOnly(t, f)
				record := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))[0]
				returned := tc.returned
				if returned == "" {
					returned = tc.value
				}
				requireDescriptorValue(t, record, returned)
				requireDescriptorInspectInput(t, spy, tc.artifact, tc.schema)
				if tc.representation != "" {
					requireDescriptor(t, record, tc.representation, true)
				} else if record["document"] != nil || record["document_error"] != nil {
					t.Errorf("non-document received projection=%#v", record)
				}
			})
		}
	}
}

func requireDescriptorValue(t *testing.T, record map[string]any, wantJSON string) {
	t.Helper()
	var want any
	if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
		t.Fatal(err)
	}
	var actual any
	for _, key := range []string{"value", "artifact_value", "content_snapshot"} {
		if value, ok := record[key]; ok {
			actual = value
			break
		}
	}
	if !reflect.DeepEqual(actual, want) {
		t.Errorf("artifact value=%#v want=%#v", actual, want)
	}
}

func requireDescriptorInspectInput(t *testing.T, spy *descriptorInspectSpy, artifact, schema string) {
	t.Helper()
	calls := spy.calls()
	if artifact == "" {
		if len(calls) != 0 {
			t.Errorf("unqualified content reached Inspect: %#v", calls)
		}
		return
	}
	if len(calls) != 1 {
		t.Errorf("Inspect calls=%d want1", len(calls))
		return
	}
	var actual, want any
	if err := json.Unmarshal(calls[0]["artifact"], &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(artifact), &want); err != nil {
		t.Fatal(err)
	}
	var actualSchema string
	if len(calls[0]["schema"]) > 0 {
		if err := json.Unmarshal(calls[0]["schema"], &actualSchema); err != nil {
			t.Fatal(err)
		}
	}
	// Inspect accepts the raw IR or a data/schema_name envelope; neither may alter its document bytes semantically.
	if obj, ok := actual.(map[string]any); ok && obj["data"] != nil && schema == descriptorIRSchema {
		actual = obj["data"]
		if actualSchema == "" {
			actualSchema, _ = obj["schema_name"].(string)
		}
	}
	if actualSchema == "lazyllm.tools.writer.data_models.writer_ir.WriterDocument" {
		actualSchema = descriptorIRSchema
	}
	if !reflect.DeepEqual(actual, want) || actualSchema != schema {
		t.Errorf("Inspect input=%#v schema=%q want=%#v schema=%q", actual, actualSchema, want, schema)
	}
}

func TestDocumentDescriptorTwoArbitrarySlots(t *testing.T) {
	f := newDescriptorFixture(t)
	descriptorAlgorithm(t, 200, descriptorMarkdown)
	for _, slot := range []string{"novel_intro", "research_notes"} {
		f.seed(t, slot, slot, "text/markdown", `{"text":"same document representation"}`)
	}
	for _, path := range []string{descriptorRoutes[0], descriptorRoutes[4]} {
		records := descriptorRecords(t, f.read(t.Context(), path, "descriptor-owner"))
		if len(records) != 2 {
			t.Fatalf("records=%#v", records)
		}
		ids := map[string]bool{}
		for _, record := range records {
			requireDescriptor(t, record, "markdown", true)
			ids[record["artifact_id"].(string)] = true
		}
		if !ids["novel_intro"] || !ids["research_notes"] {
			t.Fatalf("identity=%#v", ids)
		}
	}
}

func TestDocumentDescriptorInspectionFailuresAreExplicit(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body, code string
		retryable  bool
	}{
		{"invalid declared ir", 422, `{"detail":"private-upstream-detail"}`, "DOCUMENT_INVALID", false},
		{"unavailable", 503, `{"detail":"private-upstream-detail"}`, "DOCUMENT_INSPECTION_FAILED", true},
		{"invalid json", 200, `not-json-private-upstream-detail`, "DOCUMENT_INSPECTION_FAILED", true},
		{"missing fields", 200, `{}`, "DOCUMENT_INSPECTION_FAILED", true},
		{"contradictory", 200, `{"is_document":true,"representation":null,"schema":null}`, "DOCUMENT_INSPECTION_FAILED", true},
	}
	for _, tc := range cases {
		for _, path := range []string{descriptorRoutes[0], descriptorRoutes[4], descriptorRoutes[5]} {
			t.Run(tc.name+path, func(t *testing.T) {
				f := newDescriptorFixture(t)
				f.seed(t, "descriptor-artifact", "unknown-slot", "json", `{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"doc-one","blocks":[]}}`)
				spy := descriptorAlgorithm(t, tc.status, tc.body)
				requireDescriptorReadOnly(t, f)
				w := f.read(t.Context(), path, "descriptor-owner")
				record := descriptorRecords(t, w)[0]
				requireDescriptorError(t, record, tc.code, tc.retryable)
				if strings.Contains(w.Body.String(), "private-upstream-detail") {
					t.Error("upstream detail leaked")
				}
				if len(spy.calls()) != 1 {
					t.Errorf("calls=%d", len(spy.calls()))
				}
			})
		}
	}
}
func requireDescriptorError(t *testing.T, record map[string]any, code string, retryable bool) {
	t.Helper()
	if record["document"] != nil {
		t.Errorf("failure advertised document=%#v", record["document"])
	}
	e, ok := record["document_error"].(map[string]any)
	if !ok || e["code"] != code || e["retryable"] != retryable {
		t.Errorf("document_error=%#v want %s retryable=%v", record["document_error"], code, retryable)
	}
}

func TestDocumentDescriptorAuthorizationBeforeInspection(t *testing.T) {
	for _, path := range descriptorRoutes {
		for _, owner := range []string{"intruder", ""} {
			t.Run(path+"/"+owner, func(t *testing.T) {
				f := newDescriptorFixture(t)
				f.seed(t, "descriptor-artifact", "unknown-slot", "text/markdown", `{"text":"private document"}`)
				spy := descriptorAlgorithm(t, 200, descriptorMarkdown)
				requireDescriptorReadOnly(t, f)
				w := f.read(t.Context(), path, owner)
				if w.Code < 400 || w.Code >= 500 {
					t.Errorf("unauthorized status=%d body=%s", w.Code, w.Body.String())
				}
				if strings.Contains(w.Body.String(), "private document") {
					t.Error("unauthorized content leaked")
				}
				if len(spy.calls()) != 0 {
					t.Error("unauthorized request reached Algorithm")
				}
			})
		}
	}
}

func TestDocumentDescriptorStateRestrictsSave(t *testing.T) {
	for _, state := range []string{"historical", "stale", "deleted", "dismissed"} {
		for index, endpoint := range descriptorRoutes {
			t.Run(state+endpoint, func(t *testing.T) {
				f := newDescriptorFixture(t)
				f.seed(t, "descriptor-artifact", "unknown-slot", "text", `{"text":"readable historical document"}`)
				seedDescriptorPinnedHint(t, f, true)
				var err error
				switch state {
				case "historical":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false).Error
				case "stale", "deleted":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", state).Error
				case "dismissed":
					err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("dismissed", true).Error
				}
				if err != nil {
					t.Fatal(err)
				}
				spy := descriptorAlgorithm(t, 200, descriptorMarkdown)
				requireDescriptorReadOnly(t, f)
				w := f.read(t.Context(), endpoint, "descriptor-owner")
				if state == "dismissed" && index < 2 {
					if w.Code != 404 {
						t.Fatalf("dismissed Panel status=%d", w.Code)
					}
					if len(spy.calls()) != 0 {
						t.Error("filtered record inspected")
					}
					return
				}
				if (state == "dismissed" && (index == 2 || index == 3)) || (state == "historical" && index == 4) {
					if records := descriptorOptionalRecords(t, w); len(records) != 0 {
						t.Errorf("filtered records=%#v", records)
					}
					if len(spy.calls()) != 0 {
						t.Error("filtered record inspected")
					}
					return
				}
				record := descriptorRecords(t, w)[0]
				requireDescriptorValue(t, record, `{"text":"readable historical document"}`)
				requireDescriptorInspectInput(t, spy, `"readable historical document"`, "text/markdown")
				requireDescriptor(t, record, "markdown", false)
			})
		}
	}
}

func TestDocumentDescriptorFileCarriers(t *testing.T) {
	for _, kind := range []string{"md", "markdown", "lmd", "outside", "symlink", "missing", "url"} {
		t.Run(kind, func(t *testing.T) {
			f := newDescriptorFixture(t)
			root := t.TempDir()
			t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
			t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
			response := descriptorMarkdown
			content := "Plain prose without headings."
			ext := kind
			if kind == "lmd" {
				response = descriptorIR
				content = `{"schema_name":"lazyllm.tools.writer.data_models.writer_ir.WriterDocument","data":{"document_id":"file-doc","blocks":[]}}`
			}
			if kind == "outside" || kind == "symlink" || kind == "missing" || kind == "url" {
				ext = "md"
			}
			path := filepath.Join(root, "document."+ext)
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			var remoteCalls atomic.Int32
			bad := false
			switch kind {
			case "outside":
				path = filepath.Join(t.TempDir(), "secret.md")
				if err := os.WriteFile(path, []byte("private-file-secret"), 0600); err != nil {
					t.Fatal(err)
				}
				bad = true
			case "symlink":
				outside := filepath.Join(t.TempDir(), "secret.md")
				if err := os.WriteFile(outside, []byte("private-file-secret"), 0600); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(root, "link.md")
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
				bad = true
			case "missing":
				path = filepath.Join(root, "missing.md")
				bad = true
			case "url":
				remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					remoteCalls.Add(1)
					_, _ = w.Write([]byte("private-file-secret"))
				}))
				t.Cleanup(remote.Close)
				path = remote.URL + "/private.md"
				bad = true
			}
			f.seed(t, "descriptor-artifact", "unknown-slot", "file", mustDescriptorJSON(t, map[string]any{"path": path}))
			spy := descriptorAlgorithm(t, 200, response)
			requireDescriptorReadOnly(t, f)
			for _, endpoint := range []string{descriptorRoutes[0], descriptorRoutes[4], descriptorRoutes[5]} {
				record := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))[0]
				if endpoint != descriptorRoutes[0] {
					requireDescriptorValue(t, record, mustDescriptorJSON(t, map[string]any{"path": path}))
				}
				if bad {
					requireDescriptorError(t, record, "DOCUMENT_READ_FAILED", false)
				} else {
					representation := "markdown"
					if kind == "lmd" {
						representation = "ir"
					}
					requireDescriptor(t, record, representation, true)
				}
			}
			if remoteCalls.Load() != 0 {
				t.Errorf("forbidden URL fetched %d times", remoteCalls.Load())
			}
			calls := spy.calls()
			if bad && len(calls) != 0 {
				t.Errorf("unsafe/unreadable file reached Inspect: %#v", calls)
			}
			if !bad {
				if len(calls) != 3 {
					t.Fatalf("calls=%d", len(calls))
				}
				for _, call := range calls {
					one := &descriptorInspectSpy{requests: []map[string]json.RawMessage{call}}
					if kind == "lmd" {
						requireDescriptorInspectInput(t, one, `{"document_id":"file-doc","blocks":[]}`, descriptorIRSchema)
					} else {
						requireDescriptorInspectInput(t, one, mustDescriptorJSON(t, content), "text/markdown")
					}
				}
			}
		})
	}
}

func TestDocumentDescriptorPinnedUIHint(t *testing.T) {
	for _, pinnedMarkdown := range []bool{true, false} {
		t.Run(fmt.Sprint(pinnedMarkdown), func(t *testing.T) {
			f := newDescriptorFixture(t)
			f.seed(t, "descriptor-artifact", "unknown-slot", "text", `{"text":"unformatted prose"}`)
			seedDescriptorPinnedHint(t, f, pinnedMarkdown)
			spy := descriptorAlgorithm(t, 200, descriptorMarkdown)
			requireDescriptorReadOnly(t, f)
			for _, endpoint := range []string{descriptorRoutes[0], descriptorRoutes[4], descriptorRoutes[5]} {
				record := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))[0]
				if pinnedMarkdown {
					requireDescriptor(t, record, "markdown", true)
				} else if record["document"] != nil {
					t.Error("HEAD hint changed pinned identity")
				}
			}
			if pinnedMarkdown && len(spy.calls()) != 3 {
				t.Errorf("calls=%d", len(spy.calls()))
			}
			if !pinnedMarkdown && len(spy.calls()) != 0 {
				t.Error("HEAD hint reached Inspect")
			}
		})
	}
}

func TestDocumentDescriptorNativeAndLegacySources(t *testing.T) {
	for _, source := range []string{"native", "snapshot"} {
		t.Run(source, func(t *testing.T) {
			f := newDescriptorFixture(t)
			now := time.Now().UTC()
			value := json.RawMessage(`{"schema":"text/markdown","data":"source bytes"}`)
			revision := orm.WorkflowSlotRevision{ID: "descriptor-artifact", SessionID: "descriptor-session", SlotID: "unknown-slot", Slot: "unknown-slot", Revision: 3, Selected: true, Validity: "effective", StepID: "source", Attempt: 1, CreatedAt: now}
			if source == "snapshot" {
				revision.ContentSnapshot = value
			} else {
				seq := 1
				revision.ArtifactSeq = &seq
				for _, row := range []any{&orm.WorkflowSessionStep{ID: "native-attempt", SessionID: "descriptor-session", StepID: "source", Attempt: 1, TaskID: "native-task", Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now}, &orm.SubAgentArtifact{TaskID: "native-task", Slot: "unknown-slot", Seq: 1, ContentType: "text", Value: value, CreatedAt: now}} {
					if err := f.db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := f.db.Create(&revision).Error; err != nil {
				t.Fatal(err)
			}
			spy := descriptorAlgorithm(t, 200, descriptorMarkdown)
			requireDescriptorReadOnly(t, f)
			for _, endpoint := range []string{descriptorRoutes[0], descriptorRoutes[4], descriptorRoutes[5]} {
				record := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))[0]
				if record["artifact_id"] != "descriptor-artifact" {
					t.Errorf("artifact identity=%#v", record)
				}
				if value, exists := record["draft_version"]; exists && value != float64(0) {
					t.Errorf("non-human draft_version=%#v", value)
				}
				requireDescriptorValue(t, record, string(value))
				requireDescriptorInspectInput(t, spy, `"source bytes"`, "text/markdown")
				spy.mu.Lock()
				spy.requests = nil
				spy.mu.Unlock()
				requireDescriptor(t, record, "markdown", true)
			}
		})
	}
}

func TestDocumentDescriptorCancellationReachesInspect(t *testing.T) {
	f := newDescriptorFixture(t)
	f.seed(t, "descriptor-artifact", "unknown-slot", "text/markdown", `{"text":"cancel me"}`)
	url, started, upstreamCancelled := descriptorCancellationServer(t)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", url)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.read(ctx, descriptorRoutes[5], "descriptor-owner") }()
	select {
	case <-started:
	case w := <-done:
		t.Fatalf("read completed before Inspect: status=%d body=%s", w.Code, w.Body.String())
	case <-time.After(2 * time.Second):
		t.Fatal("Inspect was not started")
	}
	cancel()
	select {
	case <-upstreamCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("request cancellation did not reach Inspect")
	}
	select {
	case w := <-done:
		if w.Code == 200 {
			record := descriptorRecords(t, w)[0]
			requireDescriptorError(t, record, "DOCUMENT_INSPECTION_FAILED", true)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Core read did not stop on cancellation")
	}
}

func TestDocumentDescriptorMixedListPreservesOriginalArtifacts(t *testing.T) {
	f := newDescriptorFixture(t)
	f.seed(t, "bad-document", "bad-slot", "text/markdown", `{"text":"bad"}`)
	f.seed(t, "plain-artifact", "plain-slot", "text", `{"text":"ordinary text"}`)
	descriptorAlgorithm(t, 503, `{"detail":"private-upstream-detail"}`)
	requireDescriptorReadOnly(t, f)
	for _, endpoint := range []string{descriptorRoutes[0], descriptorRoutes[4]} {
		records := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))
		if len(records) != 2 {
			t.Fatalf("records=%#v", records)
		}
		for _, record := range records {
			switch record["slot_id"] {
			case "bad-slot":
				requireDescriptorError(t, record, "DOCUMENT_INSPECTION_FAILED", true)
			case "plain-slot":
				if record["document"] != nil || record["document_error"] != nil {
					t.Errorf("non-document=%#v", record)
				}
			default:
				t.Errorf("unexpected record=%#v", record)
			}
			raw := record["value"]
			if raw == nil {
				raw = record["artifact_value"]
			}
			obj, ok := raw.(map[string]any)
			wantText := "ordinary text"
			if record["slot_id"] == "bad-slot" {
				wantText = "bad"
			}
			if !ok || !reflect.DeepEqual(obj, map[string]any{"text": wantText}) {
				t.Errorf("original artifact lost=%#v", record)
			}
		}
	}
}

func TestDocumentDescriptorOpenAPIReadContracts(t *testing.T) {
	router := mux.NewRouter()
	registerAllRoutes(router)
	raw, err := buildOpenAPISpecFromRouter(router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	resolve := func(v any) map[string]any {
		obj, _ := v.(map[string]any)
		if ref, ok := obj["$ref"].(string); ok {
			obj, _ = schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		}
		return obj
	}
	paths := []string{"/workflow-sessions/{session_id}/slots", "/workflow-sessions/{session_id}", "/conversations/{conversation_id}/workflow-sessions:active", "/conversations/{conversation_id}/workflow-sessions:latest", "/workflow-sessions/{session_id}/artifacts", "/workflow-artifacts/{artifact_id}", "/workflow-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/versions"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			op := openAPIOperationForTest(t, spec, "get", "/api/core"+path)
			resp := op["responses"].(map[string]any)["200"].(map[string]any)
			content, _ := resp["content"].(map[string]any)
			media, _ := content["application/json"].(map[string]any)
			var record, recordSchema map[string]any
			seen := map[string]bool{}
			var visit func(any)
			visit = func(v any) {
				obj, _ := v.(map[string]any)
				if ref, ok := obj["$ref"].(string); ok {
					if seen[ref] {
						return
					}
					seen[ref] = true
				}
				obj = resolve(v)
				props, _ := obj["properties"].(map[string]any)
				if props["revision"] != nil && props["artifact_id"] != nil {
					record = props
					recordSchema = obj
					return
				}
				for _, child := range props {
					visit(child)
				}
				if items := obj["items"]; items != nil {
					visit(items)
				}
				for _, key := range []string{"allOf", "oneOf", "anyOf"} {
					if branches, ok := obj[key].([]any); ok {
						for _, child := range branches {
							visit(child)
						}
					}
				}
			}
			visit(media["schema"])
			if record == nil {
				t.Fatal("200 response does not describe artifact records")
			}
			checkType := func(value any, want string) {
				t.Helper()
				if got := resolve(value)["type"]; got != want {
					t.Errorf("schema type=%v want %s: %#v", got, want, value)
				}
			}
			checkRequired := func(schema map[string]any, field string, want bool) {
				t.Helper()
				found := false
				for _, value := range schemaStringList(schema["required"]) {
					found = found || value == field
				}
				if found != want {
					t.Errorf("required %s=%v want%v", field, found, want)
				}
			}
			checkType(record["artifact_id"], "string")
			checkType(record["revision"], "integer")
			checkType(record["draft_version"], "integer")
			for _, field := range []string{"artifact_id", "revision"} {
				checkRequired(recordSchema, field, true)
			}
			for _, field := range []string{"draft_version", "document", "document_error"} {
				checkRequired(recordSchema, field, false)
			}
			doc := resolve(record["document"])
			checkType(record["document"], "object")
			props, _ := doc["properties"].(map[string]any)
			for _, field := range []string{"representation", "schema"} {
				checkType(props[field], "string")
			}
			checkType(props["editable"], "boolean")
			checkType(props["capabilities"], "array")
			checkType(resolve(props["capabilities"])["items"], "string")
			for _, field := range []string{"representation", "schema", "editable", "capabilities"} {
				checkRequired(doc, field, true)
			}
			errSchema := resolve(record["document_error"])
			checkType(record["document_error"], "object")
			errorProps, _ := errSchema["properties"].(map[string]any)
			checkType(errorProps["code"], "string")
			checkType(errorProps["retryable"], "boolean")
			checkRequired(errSchema, "code", true)
			checkRequired(errSchema, "retryable", true)
		})
	}
}

func seedDescriptorPinnedHint(t *testing.T, f descriptorFixture, pinnedMarkdown bool) {
	t.Helper()
	now := time.Now().UTC()
	resource := orm.WorkflowResource{ID: "descriptor-resource", WorkflowID: "unseen-workflow", WorkflowRef: "user:descriptor-owner/unseen-workflow", OwnerUserID: "descriptor-owner", OwnerScope: "user", RelativeRoot: "unseen-workflow", HeadRevisionID: "head", CreatedAt: now, UpdatedAt: now}
	if err := f.db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"pinned", "head"} {
		markdown := pinnedMarkdown
		if id == "head" {
			markdown = !markdown
		}
		widget := "text"
		if markdown {
			widget = "text-markdown"
		}
		body := []byte("id: unseen-workflow\nui:\n  slots:\n    unknown-slot:\n      widgetType: " + widget + "\n")
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:])
		for _, row := range []any{&orm.WorkflowRevision{ID: id, WorkflowResourceID: resource.ID, RevisionNo: int64(index + 1), TreeHash: hash, CreatedAt: now}, &orm.WorkflowBlob{Hash: hash, Size: int64(len(body)), Content: body, CreatedAt: now}, &orm.WorkflowRevisionEntry{RevisionID: id, Path: "workflow.yaml", BlobHash: &hash, Size: int64(len(body))}} {
			if err := f.db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Updates(map[string]any{"plugin_revision_id": "pinned", "plugin_ref": resource.WorkflowRef}).Error; err != nil {
		t.Fatal(err)
	}
}

func schemaStringList(value any) []string {
	result := []string{}
	if list, ok := value.([]any); ok {
		for _, item := range list {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
	}
	return result
}

// Both the production-boundary test and an independent standard-client control use this exact server fixture.
func descriptorCancellationServer(t *testing.T) (string, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("consume cancellation request: %v", err)
			return
		}
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			cancelled <- struct{}{}
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); server.Close() })
	return server.URL, started, cancelled
}

func TestDocumentDescriptorCancellationFixtureControl(t *testing.T) {
	url, started, cancelled := descriptorCancellationServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{"artifact":"control"}`))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(req)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("control request not read")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture did not detect standard HTTP cancellation")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled client unexpectedly succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("control client blocked")
	}
}

func TestDocumentDescriptorConflictingDeclarations(t *testing.T) {
	cases := []struct {
		name, ct, value, code string
		inspect               bool
	}{
		{"conflicting schema fields", "json", `{"schema":"text/markdown","schema_name":"application/vnd.lazymind.writer+json","data":"prose"}`, "DOCUMENT_INVALID", false},
		{"declared fake IR", "text/markdown", `{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"fake","blocks":"bad"}}`, "DOCUMENT_INVALID", true},
		{"UI versus plain MIME", "text/plain", `{"text":"# markdown looking prose"}`, "", false},
		{"UI versus unknown schema", "text", `{"schema":"application/x-unknown","data":"prose"}`, "", false},
	}
	for _, tc := range cases {
		for _, endpoint := range []string{descriptorRoutes[0], descriptorRoutes[4], descriptorRoutes[5]} {
			t.Run(tc.name+endpoint, func(t *testing.T) {
				f := newDescriptorFixture(t)
				f.seed(t, "descriptor-artifact", "unknown-slot", tc.ct, tc.value)
				seedDescriptorPinnedHint(t, f, true)
				spy := descriptorAlgorithm(t, 422, `{"detail":"invalid document"}`)
				requireDescriptorReadOnly(t, f)
				record := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))[0]
				requireDescriptorValue(t, record, tc.value)
				if tc.inspect {
					requireDescriptorInspectInput(t, spy, `{"document_id":"fake","blocks":"bad"}`, descriptorIRSchema)
				} else {
					requireDescriptorInspectInput(t, spy, "", "")
				}
				if tc.code != "" {
					requireDescriptorError(t, record, tc.code, false)
				} else if record["document"] != nil || record["document_error"] != nil {
					t.Errorf("UI hint overrode explicit non-document: %#v", record)
				}
			})
		}
	}
}

func TestDocumentDescriptorUnauthorizedFileDoesNotLoadCarrier(t *testing.T) {
	for _, endpoint := range descriptorRoutes {
		t.Run(endpoint, func(t *testing.T) {
			f := newDescriptorFixture(t)
			root := t.TempDir()
			t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
			t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", root)
			path := filepath.Join(root, "private.md")
			if err := os.WriteFile(path, []byte("private-file-sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			f.seed(t, "descriptor-artifact", "unknown-slot", "file", mustDescriptorJSON(t, map[string]any{"path": path}))
			spy := descriptorAlgorithm(t, 200, descriptorMarkdown)
			requireDescriptorReadOnly(t, f)
			// The only source of this unpredictable path is the persisted human carrier.
			// Rejecting before querying that carrier establishes the file-read authorization order.
			var reads atomic.Int32
			name := "descriptor-private-carrier"
			if err := f.db.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
				if tx.Statement.Table == "plugin_human_artifacts" || strings.Contains(tx.Statement.SQL.String(), "plugin_human_artifacts") {
					reads.Add(1)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { f.db.Callback().Query().Remove(name) })
			w := f.read(t.Context(), endpoint, "intruder")
			if w.Code < 400 || w.Code >= 500 {
				t.Errorf("unauthorized status=%d body=%s", w.Code, w.Body.String())
			}
			if reads.Load() != 0 {
				t.Errorf("unauthorized request read private carrier %d times before rejection", reads.Load())
			}
			if len(spy.calls()) != 0 {
				t.Error("unauthorized file reached Inspect")
			}
			if strings.Contains(w.Body.String(), path) || strings.Contains(w.Body.String(), "private-file-sentinel") {
				t.Error("unauthorized file information leaked")
			}
		})
	}
}
