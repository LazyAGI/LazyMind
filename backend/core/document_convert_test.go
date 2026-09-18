package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

const portableReference = "builtin:document.convert_document.v1"

type portableFixture struct {
	descriptorFixture
	representation string
	source         any
	workspace      string
	native         bool
}

func newPortableFixture(t *testing.T, representation string, withModel bool) portableFixture {
	t.Helper()
	f := portableFixture{representation: representation}
	if withModel {
		rewrite := newRewriteFixture(t, representation)
		f.descriptorFixture = rewrite.descriptorFixture
		f.source = rewrite.source
	} else {
		f.descriptorFixture = newDescriptorFixture(t)
		if err := f.db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
			t.Fatal(err)
		}
		f.source = "# Stored\n\nStored text."
		ct := "text/markdown"
		var stored any = f.source
		if representation == "ir" {
			f.source = map[string]any{"document_id": "portable-doc", "blocks": []any{map[string]any{"node_id": "p", "type": "paragraph", "content": "Stored text."}}}
			ct = "json"
			stored = map[string]any{"schema": descriptorIRSchema, "data": f.source}
		}
		f.seed(t, "descriptor-artifact", "unknown-slot", ct, mustDescriptorJSON(t, stored))
	}
	f.workspace = t.TempDir()
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", f.workspace)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", f.workspace)
	return f
}
func (f *portableFixture) carrier(t *testing.T, kind string) {
	t.Helper()
	now := time.Now().UTC()
	ct := "text/markdown"
	var stored any = f.source
	if f.representation == "ir" {
		ct = "json"
		stored = map[string]any{"schema": descriptorIRSchema, "data": f.source}
	}
	if kind == "file" {
		suffix := ".md"
		var body []byte
		if text, ok := f.source.(string); ok {
			body = []byte(text)
		}
		if f.representation == "ir" {
			suffix = ".lmd"
			body = []byte(mustDescriptorJSON(t, stored))
		}
		path := filepath.Join(f.workspace, "source"+suffix)
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"content_type": "file", "value": json.RawMessage(mustDescriptorJSON(t, map[string]any{"path": path}))}).Error; err != nil {
			t.Fatal(err)
		}
	} else if kind == "native" {
		for _, row := range []any{&orm.WorkflowSessionStep{ID: "portable-step", SessionID: "descriptor-session", StepID: "source", TaskID: "portable-task", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now}, &orm.SubAgentArtifact{ID: "portable-source", TaskID: "portable-task", Slot: "unknown-slot", Seq: 1, ContentType: ct, Value: json.RawMessage(mustDescriptorJSON(t, stored)), CreatedAt: now}} {
			if err := f.db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"human_artifact_id": nil, "artifact_seq": 1}).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Delete(&orm.WorkflowHumanArtifact{}, "id = ?", "descriptor-artifact-human").Error; err != nil {
			t.Fatal(err)
		}
		f.native = true
	} else if kind == "envelope" {
		if f.representation == "markdown" {
			stored = map[string]any{"schema_name": "text/markdown", "data": f.source}
		}
		if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"content_type": "json", "value": json.RawMessage(mustDescriptorJSON(t, stored))}).Error; err != nil {
			t.Fatal(err)
		}
	}
}
func (f portableFixture) body(format string) map[string]any {
	body := map[string]any{"action": "convert_document", "base_revision": 3, "input": map[string]any{"output_format": format}}
	if !f.native {
		body["base_draft_version"] = 7
	}
	return body
}
func (f portableFixture) post(ctx context.Context, phase, owner string, body map[string]any, scope string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/workflow-artifacts/descriptor-artifact/document-actions:"+phase, strings.NewReader(mustJSONRewrite(body))).WithContext(ctx)
	req.Header.Set("X-User-Id", owner)
	if scope != "" {
		req.Header.Set("X-LazyMind-Invocation-Conversation-Id", scope)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}
func portableState(t *testing.T, f portableFixture) string {
	t.Helper()
	base := rewriteSnapshot(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
	var native []orm.SubAgentArtifact
	if err := f.db.Order("id").Find(&native).Error; err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	if err := filepath.WalkDir(f.workspace, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(f.workspace, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[relative] = "directory"
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			files[relative] = target
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = fmt.Sprintf("%x", sha256.Sum256(content))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return base + mustDescriptorJSON(t, []any{native, files})
}

type portableGate struct {
	started, cancelled chan struct{}
	release            chan struct{}
}
type portableServer struct {
	url          string
	mu           sync.Mutex
	calls        []map[string]any
	inspectCalls int
	status       int
	rawResponse  string
	response     map[string]any
	gate         *portableGate
}

func newPortableServer(t *testing.T, f portableFixture, format string, snapshot any, hasSnapshot bool) *portableServer {
	t.Helper()
	spy := &portableServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "POST" || (r.URL.Path != "/api/document:inspect" && r.URL.Path != "/api/document/actions:invoke") {
			t.Errorf("unexpected Algorithm/Provider call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path == "/api/document:inspect" {
			spy.mu.Lock()
			spy.inspectCalls++
			spy.mu.Unlock()
			response := descriptorMarkdown
			if f.representation == "ir" {
				response = descriptorIR
			}
			_, _ = w.Write([]byte(response))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/document/actions:invoke" {
			t.Errorf("unexpected Algorithm/Provider call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		spy.mu.Lock()
		spy.calls = append(spy.calls, body)
		status, raw, override, gate := spy.status, spy.rawResponse, spy.response, spy.gate
		spy.mu.Unlock()
		for key := range body {
			switch key {
			case "reference", "phase", "artifact", "arguments":
			case "artifact_store":
				if body[key] != "" {
					t.Error("portable conversion selected a manifest store")
				}
			default:
				t.Errorf("conversion sent forbidden context %s", key)
			}
		}
		if body["reference"] != portableReference || body["phase"] != "preview" {
			t.Errorf("wrong conversion dispatch=%#v", body)
		}
		artifact, ok := body["artifact"].(map[string]any)
		if !ok || !reflect.DeepEqual(artifact, map[string]any{"data": f.source}) {
			t.Errorf("server source not logical data envelope=%#v", body["artifact"])
		}
		expectedArgs := map[string]any{"output_format": format}
		if hasSnapshot {
			expectedArgs["document"] = snapshot
		}
		if !reflect.DeepEqual(body["arguments"], expectedArgs) {
			t.Errorf("convert arguments=%#v want=%#v", body["arguments"], expectedArgs)
		}
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
		if raw != "" {
			_, _ = w.Write([]byte(raw))
			return
		}
		result := map[string]any{"provider": "", "format": format, "content": "converted " + format + "\n", "source_document": map[string]any{"document_id": "private-source-metadata"}, "media_references": map[string]any{"private": "/private/algorithm-file"}}
		if override != nil {
			result = override
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	spy.url = server.URL
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	return spy
}
func (s *portableServer) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.calls) }

func (s *portableServer) inspections() int { s.mu.Lock(); defer s.mu.Unlock(); return s.inspectCalls }

type portableCosts struct{ modelQueries, providerCalls, contentQueries atomic.Int32 }

func watchPortableCosts(t *testing.T, f portableFixture) *portableCosts {
	t.Helper()
	costs := &portableCosts{}
	if err := f.db.Callback().Query().After("gorm:query").Register("portable-costs", func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if strings.Contains(sql, "user_selected_models") || strings.Contains(sql, "user_model_provider_groups") || strings.Contains(sql, "user_model_provider_group_models") {
			costs.modelQueries.Add(1)
		}
		if strings.Contains(sql, "plugin_human_artifacts") || strings.Contains(sql, "sub_agent_artifacts") { // workflow-naming: persistence
			costs.contentQueries.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Callback().Query().Remove("portable-costs") })
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { costs.providerCalls.Add(1); w.WriteHeader(500) }))
	t.Cleanup(trap.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", trap.URL)
	return costs
}
func requirePortableCosts(t *testing.T, costs *portableCosts) {
	t.Helper()
	if costs.modelQueries.Load() != 0 || costs.providerCalls.Load() != 0 {
		t.Errorf("conversion performed model/credential I/O=%d/%d", costs.modelQueries.Load(), costs.providerCalls.Load())
	}
}
func requirePortableResult(t *testing.T, w *httptest.ResponseRecorder, format string) {
	t.Helper()
	data := rewriteData(t, w)
	want := map[string]any{"provider": "", "format": format, "content": "converted " + format + "\n"}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("public portable result=%#v want=%#v", data, want)
	}
}

func TestDocumentConvertSavedSources(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, carrier := range []string{"human", "native", "envelope", "file"} {
			for _, format := range []string{"markdown", "latex", "text"} {
				t.Run(representation+"/"+carrier+"/"+format, func(t *testing.T) {
					f := newPortableFixture(t, representation, false)
					f.carrier(t, carrier)
					spy := newPortableServer(t, f, format, nil, false)
					costs := watchPortableCosts(t, f)
					before := portableState(t, f)
					w := f.post(t.Context(), "preview", "descriptor-owner", f.body(format), "")
					requirePortableResult(t, w, format)
					if spy.count() != 1 {
						t.Errorf("conversion calls=%d", spy.count())
					}
					requirePortableCosts(t, costs)
					if portableState(t, f) != before {
						t.Error("conversion changed stored state or workspace files")
					}
				})
			}
		}
	}
}

func TestDocumentConvertEditorSnapshots(t *testing.T) {
	cases := []struct {
		name, representation string
		snapshot             any
	}{
		{"markdown", "markdown", "# Unsaved\n\nNew text"},
		{"empty markdown", "markdown", ""},
		{"path literal", "markdown", "/tmp/looks-like-a-document.md"},
		{"JSON literal", "markdown", `{"data":"literal text"}`},
		{"IR", "ir", map[string]any{"document_id": "portable-doc", "blocks": []any{map[string]any{"node_id": "p", "type": "paragraph", "content": "Unsaved text."}}}},
	}
	for _, tc := range cases {
		for _, format := range []string{"markdown", "latex", "text"} {
			t.Run(tc.name+format, func(t *testing.T) {
				f := newPortableFixture(t, tc.representation, false)
				spy := newPortableServer(t, f, format, tc.snapshot, true)
				costs := watchPortableCosts(t, f)
				body := f.body(format)
				body["input"].(map[string]any)["document"] = tc.snapshot
				before := portableState(t, f)
				requirePortableResult(t, f.post(t.Context(), "preview", "descriptor-owner", body, ""), format)
				if spy.count() != 1 {
					t.Errorf("snapshot conversions=%d", spy.count())
				}
				requirePortableCosts(t, costs)
				if portableState(t, f) != before {
					t.Error("snapshot persisted or changed workspace")
				}
			})
		}
	}
}

func TestDocumentConvertIsIndependentOfModelAndLiveConsumers(t *testing.T) {
	for _, withModel := range []bool{false, true} {
		t.Run(fmt.Sprint(withModel), func(t *testing.T) {
			f := newPortableFixture(t, "markdown", withModel)
			seedRewriteConsumer(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "running")
			spy := newPortableServer(t, f, "text", nil, false)
			costs := watchPortableCosts(t, f)
			before := portableState(t, f)
			requirePortableResult(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("text"), ""), "text")
			if spy.count() != 1 {
				t.Errorf("live document conversion calls=%d", spy.count())
			}
			requirePortableCosts(t, costs)
			if portableState(t, f) != before {
				t.Error("copy invalidated a live dependency")
			}
		})
	}
}

func TestDocumentConvertRejectsInvalidInputAndExecute(t *testing.T) {
	for _, kind := range []string{"missing format", "wrong format type", "native", "docx", "provider", "target_document", "template", "media_assets", "reference", "artifact_store", "llm_config", "tool_config", "snapshot null", "snapshot array", "snapshot number", "snapshot path", "snapshot url", "snapshot wrong representation", "execute"} {
		t.Run(kind, func(t *testing.T) {
			f := newPortableFixture(t, "markdown", false)
			spy := newPortableServer(t, f, "markdown", nil, false)
			costs := watchPortableCosts(t, f)
			body := f.body("markdown")
			input := body["input"].(map[string]any)
			var snapshotFetches atomic.Int32
			status := 400
			code := "DOCUMENT_ACTION_INVALID"
			phase := "preview"
			switch kind {
			case "missing format":
				delete(input, "output_format")
			case "wrong format type":
				input["output_format"] = 3
			case "native", "docx":
				input["output_format"] = kind
				status = 422
				code = "DOCUMENT_ACTION_UNSUPPORTED"
			case "provider", "target_document", "template", "media_assets":
				input[kind] = "injected"
			case "reference", "artifact_store", "llm_config", "tool_config":
				body[kind] = "injected"
			case "snapshot null":
				input["document"] = nil
			case "snapshot array":
				input["document"] = []any{"not document"}
			case "snapshot number":
				input["document"] = 3
			case "snapshot path":
				path := filepath.Join(f.workspace, "private.md")
				if err := os.WriteFile(path, []byte("private snapshot sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
				input["document"] = map[string]any{"path": path}
			case "snapshot url":
				remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					snapshotFetches.Add(1)
					_, _ = w.Write([]byte("private snapshot content"))
				}))
				t.Cleanup(remote.Close)
				input["document"] = map[string]any{"url": remote.URL + "/document.md"}
			case "snapshot wrong representation":
				input["document"] = map[string]any{"document_id": "not-markdown", "blocks": []any{}}
			case "execute":
				phase = "execute"
				status = 422
				code = "DOCUMENT_ACTION_UNSUPPORTED"
			}
			before := portableState(t, f)
			rewriteError(t, f.post(t.Context(), phase, "descriptor-owner", body, ""), status, code)
			if spy.count() != 0 {
				t.Error("invalid portable request reached conversion")
			}
			if snapshotFetches.Load() != 0 {
				t.Error("snapshot URL was fetched")
			}
			requirePortableCosts(t, costs)
			if portableState(t, f) != before {
				t.Error("invalid portable request changed state")
			}
		})
	}
}

func TestDocumentConvertAuthorizationAndBaselines(t *testing.T) {
	for _, withSnapshot := range []bool{false, true} {
		for _, kind := range []string{"missing owner", "wrong owner", "wrong scope", "missing revision", "stale revision", "missing draft", "stale draft", "historical", "stale", "deleted", "dismissed", "unknown session", "ordinary text"} {
			t.Run(fmt.Sprint(withSnapshot)+kind, func(t *testing.T) {
				f := newPortableFixture(t, "markdown", false)
				spy := newPortableServer(t, f, "text", "# Valid editor snapshot", withSnapshot)
				costs := watchPortableCosts(t, f)
				body := f.body("text")
				if withSnapshot {
					body["input"].(map[string]any)["document"] = "# Valid editor snapshot"
				}
				owner, scope := "descriptor-owner", ""
				status := 409
				code := "REVISION_CONFLICT"
				var err error
				switch kind {
				case "missing owner":
					owner = ""
					status = 400
					code = "IDENTITY_REQUIRED"
				case "wrong owner":
					owner = "other"
					status = 403
					code = "PERMISSION_DENIED"
				case "wrong scope":
					scope = "another-conversation"
					status = 403
					code = "PERMISSION_DENIED"
				case "missing revision":
					delete(body, "base_revision")
					status = 400
					code = "REVISION_REQUIRED"
				case "stale revision":
					body["base_revision"] = 2
				case "missing draft":
					delete(body, "base_draft_version")
					status = 400
					code = "DRAFT_VERSION_REQUIRED"
				case "stale draft":
					body["base_draft_version"] = 8
					code = "DRAFT_VERSION_CONFLICT"
				case "historical":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false).Error
				case "stale", "deleted":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", kind).Error
				case "dismissed":
					err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("dismissed", true).Error
					status = 404
					code = "ARTIFACT_NOT_FOUND"
				case "unknown session":
					err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("status", "unknown-state").Error
					code = "SESSION_NOT_EDITABLE"
				case "ordinary text":
					err = f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("content_type", "text").Error
					status = 422
					code = "DOCUMENT_ACTION_UNSUPPORTED"
				}
				if err != nil {
					t.Fatal(err)
				}
				before := portableState(t, f)
				costs.contentQueries.Store(0)
				rewriteError(t, f.post(t.Context(), "preview", owner, body, scope), status, code)
				if kind == "missing owner" || kind == "wrong owner" || kind == "wrong scope" {
					if costs.contentQueries.Load() != 0 {
						t.Error("unauthorized request loaded content")
					}
				}
				if kind != "ordinary text" && spy.inspections() != 0 {
					t.Errorf("rejected target reached Inspect %d times", spy.inspections())
				}
				if spy.count() != 0 {
					t.Error("rejected request converted content")
				}
				requirePortableCosts(t, costs)
				if portableState(t, f) != before {
					t.Error("rejected request persisted")
				}
			})
		}
	}
}

func TestDocumentConvertFailureAndCancellation(t *testing.T) {
	for _, kind := range []string{"upstream", "invalid JSON", "missing content", "non-string content", "wrong format", "nonempty provider", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			f := newPortableFixture(t, "markdown", false)
			spy := newPortableServer(t, f, "latex", nil, false)
			costs := watchPortableCosts(t, f)
			spy.mu.Lock()
			switch kind {
			case "upstream":
				spy.status = 502
				spy.rawResponse = `{"detail":{"code":"WORKFLOW_ACTION_FAILED","message":"private-upstream-detail"}}`
			case "invalid JSON":
				spy.rawResponse = `not-json`
			case "missing content":
				spy.response = map[string]any{"provider": "", "format": "latex"}
			case "non-string content":
				spy.response = map[string]any{"provider": "", "format": "latex", "content": map[string]any{"path": "/private/internal"}}
			case "wrong format":
				spy.response = map[string]any{"provider": "", "format": "markdown", "content": "wrong"}
			case "nonempty provider":
				spy.response = map[string]any{"provider": "notion", "format": "latex", "content": "wrong"}
			case "cancel":
				spy.gate = &portableGate{started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1), release: make(chan struct{})}
			}
			gate := spy.gate
			spy.mu.Unlock()
			before := portableState(t, f)
			if kind == "cancel" {
				t.Cleanup(func() { close(gate.release) })
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- f.post(ctx, "preview", "descriptor-owner", f.body("latex"), "") }()
				select {
				case <-gate.started:
				case w := <-done:
					t.Fatalf("conversion did not start: %d %s", w.Code, w.Body.String())
				case <-time.After(2 * time.Second):
					t.Fatal("conversion not started")
				}
				cancel()
				select {
				case <-gate.cancelled:
				case <-time.After(2 * time.Second):
					t.Fatal("cancel not propagated")
				}
				select {
				case w := <-done:
					if w.Code < 400 {
						t.Error("cancel returned successful content")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("cancel did not finish")
				}
			} else {
				code := "DOCUMENT_ACTION_RESULT_INVALID"
				if kind == "upstream" {
					code = "DOCUMENT_ACTION_FAILED"
				}
				rewriteError(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("latex"), ""), 502, code)
			}
			requirePortableCosts(t, costs)
			if portableState(t, f) != before {
				t.Error("failed conversion changed state")
			}
		})
	}
}

func TestDocumentConvertCapabilities(t *testing.T) {
	for _, withModel := range []bool{false, true} {
		for _, state := range []string{"editable", "live", "historical", "stale", "deleted", "dismissed", "unknown session"} {
			for index, endpoint := range descriptorRoutes {
				t.Run(fmt.Sprint(withModel)+state+endpoint, func(t *testing.T) {
					f := newPortableFixture(t, "markdown", withModel)
					spy := newPortableServer(t, f, "text", nil, false)
					var err error
					switch state {
					case "live":
						seedRewriteConsumer(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "running")
					case "historical":
						err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false).Error
					case "stale", "deleted":
						err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", state).Error
					case "dismissed":
						err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("dismissed", true).Error
					case "unknown session":
						err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("status", "unknown-state").Error
					}
					if err != nil {
						t.Fatal(err)
					}
					before := portableState(t, f)
					w := f.read(t.Context(), endpoint, "descriptor-owner")
					if state == "dismissed" && index < 2 {
						if w.Code != 404 {
							t.Fatalf("dismissed status=%d", w.Code)
						}
					} else {
						records := descriptorOptionalRecords(t, w)
						filtered := (state == "dismissed" && (index == 2 || index == 3)) || (state == "historical" && index == 4) || (state == "unknown session" && index == 2)
						if filtered {
							if len(records) != 0 {
								t.Errorf("filter changed=%#v", records)
							}
						} else {
							found := false
							for _, record := range records {
								if record["artifact_id"] != "descriptor-artifact" {
									continue
								}
								found = true
								doc, _ := record["document"].(map[string]any)
								want := []string{}
								if state == "editable" {
									want = []string{"convert_document", "cross_reference", "numbering", "publish_document", "save"}
									if withModel {
										want = []string{"convert_document", "cross_reference", "numbering", "publish_document", "rewrite_selection", "save"}
									}
								} else if state == "live" {
									want = []string{"convert_document", "cross_reference", "numbering"}
								}
								caps := schemaStringList(doc["capabilities"])
								sort.Strings(caps)
								if !reflect.DeepEqual(caps, want) || doc["editable"] != (state == "editable") {
									t.Errorf("capabilities/state=%#v want=%#v", doc, want)
								}
							}
							if !found {
								t.Error("source document missing")
							}
						}
					}
					if spy.count() != 0 {
						t.Error("descriptor executed conversion")
					}
					if portableState(t, f) != before {
						t.Error("descriptor changed state")
					}
				})
			}
		}
	}
}

// Select one action-specific request schema, preserving the assertions on the
// existing rewrite branch while the shared Preview route gains another action.
func documentActionRequestSchemaForTest(t *testing.T, root any, schemas map[string]any, action string) map[string]any {
	t.Helper()
	var find func(any) map[string]any
	find = func(value any) map[string]any {
		obj, _ := value.(map[string]any)
		if ref, ok := obj["$ref"].(string); ok {
			obj, _ = schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		}
		props, _ := obj["properties"].(map[string]any)
		actionSchema, _ := props["action"].(map[string]any)
		values := schemaStringList(actionSchema["enum"])
		if len(values) == 1 && values[0] == action {
			return obj
		}
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			if branches, ok := obj[key].([]any); ok {
				for _, branch := range branches {
					if found := find(branch); found != nil {
						return found
					}
				}
			}
		}
		return nil
	}
	result := find(root)
	if result == nil {
		t.Fatalf("request schema has no %s branch", action)
	}
	return result
}

func TestDocumentConvertOpenAPI(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	raw, err := buildOpenAPISpecFromRouter(f.router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	resolve := func(value any) map[string]any {
		obj, _ := value.(map[string]any)
		if ref, ok := obj["$ref"].(string); ok {
			obj, _ = schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		}
		return obj
	}
	op := openAPIOperationForTest(t, spec, "post", "/api/core/workflow-artifacts/{artifact_id}/document-actions:preview")
	body := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
	request := documentActionRequestSchemaForTest(t, body, schemas, "convert_document")
	props := request["properties"].(map[string]any)
	for field, want := range map[string]string{"action": "string", "base_revision": "integer", "base_draft_version": "integer", "input": "object"} {
		if resolve(props[field])["type"] != want {
			t.Errorf("request field %s=%#v", field, props[field])
		}
	}
	required := schemaStringList(request["required"])
	for _, field := range []string{"action", "base_revision", "input"} {
		if !containsPortable(required, field) {
			t.Errorf("missing required %s", field)
		}
	}
	if containsPortable(required, "base_draft_version") {
		t.Error("native draft version made globally required")
	}
	input := resolve(props["input"])
	ip := input["properties"].(map[string]any)
	formats := schemaStringList(resolve(ip["output_format"])["enum"])
	sort.Strings(formats)
	if resolve(ip["output_format"])["type"] != "string" || !reflect.DeepEqual(formats, []string{"latex", "markdown", "text"}) || !containsPortable(schemaStringList(input["required"]), "output_format") {
		t.Errorf("formats/required=%#v", input)
	}
	if _, ok := ip["document"]; !ok {
		t.Error("snapshot field absent")
	}
	snapshotSchema := resolve(ip["document"])
	snapshotBranches, _ := snapshotSchema["oneOf"].([]any)
	if len(snapshotBranches) == 0 {
		snapshotBranches, _ = snapshotSchema["anyOf"].([]any)
	}
	snapshotTypes := []string{}
	for _, branch := range snapshotBranches {
		kind, _ := resolve(branch)["type"].(string)
		snapshotTypes = append(snapshotTypes, kind)
	}
	sort.Strings(snapshotTypes)
	if !reflect.DeepEqual(snapshotTypes, []string{"object", "string"}) {
		t.Errorf("snapshot schema must distinguish inline string and IR object: %#v", snapshotSchema)
	}
	if containsPortable(schemaStringList(input["required"]), "document") {
		t.Error("snapshot must be optional")
	}
	responses := op["responses"].(map[string]any)
	response := responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
	var findResult func(any) map[string]any
	findResult = func(value any) map[string]any {
		obj := resolve(value)
		properties, _ := obj["properties"].(map[string]any)
		if properties["provider"] != nil && properties["format"] != nil && properties["content"] != nil {
			return obj
		}
		for _, child := range properties {
			if found := findResult(child); found != nil {
				return found
			}
		}
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			if branches, ok := obj[key].([]any); ok {
				for _, branch := range branches {
					if found := findResult(branch); found != nil {
						return found
					}
				}
			}
		}
		return nil
	}
	result := findResult(response)
	if result == nil {
		t.Fatal("portable response schema absent")
	}
	rp := result["properties"].(map[string]any)
	for _, field := range []string{"provider", "format", "content"} {
		if resolve(rp[field])["type"] != "string" || !containsPortable(schemaStringList(result["required"]), field) {
			t.Errorf("result field %s not required string", field)
		}
	}
}
func containsPortable(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func documentActionResultSchemaForTest(t *testing.T, root any, schemas map[string]any, action string) map[string]any {
	t.Helper()
	var find func(any) map[string]any
	find = func(value any) map[string]any {
		obj, _ := value.(map[string]any)
		if ref, ok := obj["$ref"].(string); ok {
			obj, _ = schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		}
		props, _ := obj["properties"].(map[string]any)
		if action == "numbering" && props["numbering"] != nil && props["representation"] != nil {
			return obj
		}
		if action == "rewrite_selection" && ((props["commit"] != nil && props["preview"] != nil) || (props["artifact_id"] != nil && props["numbering"] == nil)) {
			return obj
		}
		if action == "convert_document" && props["provider"] != nil && props["format"] != nil && props["content"] != nil {
			return obj
		}
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			if branches, ok := obj[key].([]any); ok {
				for _, branch := range branches {
					if result := find(branch); result != nil {
						return result
					}
				}
			}
		}
		return nil
	}
	result := find(root)
	if result == nil {
		t.Fatalf("result schema has no %s branch", action)
	}
	return result
}

func TestDocumentConvertKeepsExactListItem(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("list_index", 0).Error; err != nil {
		t.Fatal(err)
	}
	f.seed(t, "other-list-item", "other-slot", "text/markdown", `{"text":"other item must not be copied"}`)
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "other-list-item").Updates(map[string]any{"slot_id": "unknown-slot", "slot": "unknown-slot", "list_index": 1}).Error; err != nil {
		t.Fatal(err)
	}
	spy := newPortableServer(t, f, "text", nil, false)
	before := portableState(t, f)
	requirePortableResult(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("text"), ""), "text")
	if spy.count() != 1 {
		t.Error("wrong conversion count")
	}
	if portableState(t, f) != before {
		t.Error("list conversion mutated either item")
	}
}

func TestDocumentConvertInvalidIRSnapshot(t *testing.T) {
	for _, snapshot := range []any{map[string]any{"status": "not IR"}, map[string]any{"document_id": "portable-doc", "blocks": "not an array"}} {
		t.Run(mustJSONRewrite(snapshot), func(t *testing.T) {
			f := newPortableFixture(t, "ir", false)
			spy := newPortableServer(t, f, "text", snapshot, true)
			spy.mu.Lock()
			spy.status = 422
			spy.rawResponse = `{"detail":{"code":"WORKFLOW_ACTION_INVALID","message":"private-upstream-detail"}}`
			spy.mu.Unlock()
			costs := watchPortableCosts(t, f)
			body := f.body("text")
			body["input"].(map[string]any)["document"] = snapshot
			before := portableState(t, f)
			rewriteError(t, f.post(t.Context(), "preview", "descriptor-owner", body, ""), 400, "DOCUMENT_ACTION_INVALID")
			// Full IR validation may be delegated to the pure Algorithm converter.
			if spy.count() > 1 {
				t.Error("invalid snapshot was retried")
			}
			requirePortableCosts(t, costs)
			if portableState(t, f) != before {
				t.Error("invalid IR snapshot persisted")
			}
		})
	}
}

func TestDocumentConvertAcceptsEmptyContent(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	spy := newPortableServer(t, f, "text", nil, false)
	spy.mu.Lock()
	spy.response = map[string]any{"provider": "", "format": "text", "content": "", "source_document": map[string]any{"document_id": "empty", "blocks": []any{}}, "media_references": map[string]any{}}
	spy.mu.Unlock()
	before := portableState(t, f)
	data := rewriteData(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("text"), ""))
	if !reflect.DeepEqual(data, map[string]any{"provider": "", "format": "text", "content": ""}) {
		t.Errorf("empty result changed=%#v", data)
	}
	if portableState(t, f) != before {
		t.Error("empty conversion persisted")
	}
}

func TestDocumentConvertReadLeaseRemainsRestricted(t *testing.T) {
	for _, withSnapshot := range []bool{false, true} {
		t.Run(fmt.Sprint(withSnapshot), func(t *testing.T) {
			f := newPortableFixture(t, "markdown", false)
			spy := newPortableServer(t, f, "text", "# Valid editor snapshot", withSnapshot)
			if err := f.db.AutoMigrate(&orm.ExternalChatRun{}); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			expiry := now.Add(time.Minute)
			run := orm.ExternalChatRun{ID: "portable-run", RequestID: "portable-request", ConversationID: "descriptor-conversation", HistoryID: "portable-history", Provider: "codex", ActorUserID: "descriptor-owner", Status: "running", HostID: "portable-host", LeaseToken: "portable-test-lease", LeaseExpiresAt: &expiry, CreatedAt: now, UpdatedAt: now}
			if err := f.db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			call := func(method, path, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("X-User-Id", "descriptor-owner")
				req.Header.Set("X-LazyMind-External-Ref", run.ID)
				req.Header.Set("X-LazyMind-External-Lease", run.LeaseToken)
				req.Header.Set("X-LazyMind-External-Host", run.HostID)
				req.Header.Set("X-LazyMind-Conversation-Id", run.ConversationID)
				w := httptest.NewRecorder()
				f.router.ServeHTTP(w, req)
				return w
			}
			record := descriptorRecords(t, call(http.MethodGet, "/workflow-artifacts/descriptor-artifact", ""))[0]
			doc, _ := record["document"].(map[string]any)
			if containsPortable(schemaStringList(doc["capabilities"]), "convert_document") {
				t.Error("lease advertised inaccessible conversion")
			}
			costs := watchPortableCosts(t, f)
			before := portableState(t, f)
			inspectionsBefore := spy.inspections()
			body := f.body("text")
			if withSnapshot {
				body["input"].(map[string]any)["document"] = "# Valid editor snapshot"
			}
			costs.contentQueries.Store(0)
			for _, phase := range []string{"preview", "execute"} {
				w := call(http.MethodPost, "/workflow-artifacts/descriptor-artifact/document-actions:"+phase, mustJSONRewrite(body))
				if w.Code != 409 {
					t.Errorf("read lease conversion status=%d", w.Code)
				}
			}
			if spy.inspections() != inspectionsBefore {
				t.Error("read lease rejection reached Inspect")
			}
			if costs.contentQueries.Load() != 0 || spy.count() != 0 {
				t.Error("read lease reached content/conversion")
			}
			requirePortableCosts(t, costs)
			if portableState(t, f) != before {
				t.Error("read lease action persisted")
			}
		})
	}

}

func TestDocumentConvertServerFixtureControl(t *testing.T) {
	f := newPortableFixture(t, "markdown", false)
	snapshot := "# Unsaved control"
	spy := newPortableServer(t, f, "text", snapshot, true)
	body := map[string]any{"reference": portableReference, "phase": "preview", "artifact": map[string]any{"data": f.source}, "arguments": map[string]any{"output_format": "text", "document": snapshot}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, spy.url+"/api/document/actions:invoke", strings.NewReader(mustJSONRewrite(body)))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	data, _ := result["result"].(map[string]any)
	if response.StatusCode != 200 || data["provider"] != "" || data["format"] != "text" || data["content"] != "converted text\n" || spy.count() != 1 {
		t.Fatalf("fixture response=%#v", result)
	}
}
