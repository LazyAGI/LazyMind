package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/modelconfig"
	"lazymind/core/workflow"
)

const rewriteReference = "builtin:document.rewrite_selection.v1"
const rewriteTestKey = "rewrite-fixture-only-key"

type rewriteFixture struct {
	descriptorFixture
	representation    string
	source, candidate any
	selection         map[string]any
}

func newRewriteFixture(t *testing.T, representation string) rewriteFixture {
	t.Helper()
	f := rewriteFixture{descriptorFixture: newDescriptorFixture(t), representation: representation}
	if err := f.db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	base := orm.BaseModel{CreateUserID: "descriptor-owner", CreatedAt: now, UpdatedAt: now}
	group := orm.UserModelProviderGroup{ID: "rewrite-group", UserModelProviderID: "rewrite-provider", Name: "test", BaseURL: "https://models.invalid/v1", APIKey: rewriteTestKey, IsVerified: true, BaseModel: base}
	model := orm.UserModelProviderGroupModel{ID: "rewrite-model", UserModelProviderID: group.UserModelProviderID, UserModelProviderGroupID: group.ID, ProviderName: "OpenAI", Name: "rewrite-test-model", ModelType: "llm", BaseModel: base}
	for _, row := range []any{&group, &model, &orm.UserSelectedModel{UserID: "descriptor-owner", ModelKey: "llm", UserModelProviderGroupModelID: model.ID, CreatedAt: now, UpdatedAt: now}} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	config, err := modelconfig.LoadLLMConfig(t.Context(), f.db.DB, "descriptor-owner")
	if err != nil || config["llm"] == nil {
		t.Fatalf("model fixture not usable: config=%#v err=%v", config, err)
	}
	f.source = "Original."
	f.candidate = "Rewritten."
	f.selection = map[string]any{"type": "markdown", "selected_text": "Original."}
	ct := "text/markdown"
	if representation == "ir" {
		ct = "json"
		f.source = map[string]any{"document_id": "rewrite-doc", "blocks": []any{map[string]any{"node_id": "p", "type": "paragraph", "content": "Original."}}}
		f.candidate = map[string]any{"document_id": "rewrite-doc", "blocks": []any{map[string]any{"node_id": "p", "type": "paragraph", "content": "Rewritten."}}}
		f.selection = map[string]any{"type": "ir", "node_id": "p"}
	}
	storedSource := f.source
	if representation == "ir" {
		storedSource = map[string]any{"schema": descriptorIRSchema, "data": f.source}
	}
	f.seed(t, "descriptor-artifact", "unknown-slot", ct, mustDescriptorJSON(t, storedSource))
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	return f
}
func (f rewriteFixture) body(phase, token string) map[string]any {
	input := map[string]any{"instruction": "Make it clearer", "selection": f.selection}
	if phase == "execute" {
		input = map[string]any{"commit_token": token}
	}
	return map[string]any{"action": "rewrite_selection", "base_revision": 3, "base_draft_version": 7, "input": input}
}
func (f rewriteFixture) post(ctx context.Context, phase, id, owner string, body map[string]any) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/workflow-artifacts/"+id+"/document-actions:"+phase, strings.NewReader(mustJSONRewrite(body))).WithContext(ctx)
	req.Header.Set("X-User-Id", owner)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}
func mustJSONRewrite(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
func rewriteData(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data == nil {
		t.Fatalf("missing Core data envelope: %s", w.Body.String())
	}
	return body.Data
}
func rewriteError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != status || body.Data["code"] != code {
		t.Errorf("status=%d body=%s want %d/%s", w.Code, w.Body.String(), status, code)
	}
	if strings.Contains(w.Body.String(), rewriteTestKey) || strings.Contains(w.Body.String(), "private-upstream-detail") {
		t.Error("sensitive upstream detail leaked")
	}
}

type rewriteRequest struct {
	Reference, Phase string
	Artifact         json.RawMessage
	Arguments        map[string]any
	ArtifactStore    string         `json:"artifact_store"`
	LLMConfig        map[string]any `json:"llm_config"`
}
type rewriteManifest struct {
	source         any
	artifact       map[string]any
	representation string
}
type rewriteExecuteGate struct {
	started, cancelled chan struct{}
	release            chan struct{}
}
type rewriteServer struct {
	executeGate       *rewriteExecuteGate
	url               string
	mu                sync.Mutex
	requests          []rewriteRequest
	inspectCalls      int
	manifests         map[string]rewriteManifest
	beforeExecute     func()
	resultOverride    map[string]any
	argumentsOverride map[string]any
	failureStatus     int
	failureCode       string
}

func newRewriteServer(t *testing.T, f rewriteFixture) *rewriteServer {
	t.Helper()
	server := &rewriteServer{manifests: map[string]rewriteManifest{}}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/api/document:inspect" {
			server.mu.Lock()
			server.inspectCalls++
			server.mu.Unlock()
			response := descriptorMarkdown
			if f.representation == "ir" {
				response = descriptorIR
			}
			_, _ = w.Write([]byte(response))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/document/actions:invoke" {
			t.Errorf("wrong Algorithm route: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		for key := range raw {
			switch key {
			case "reference", "phase", "artifact", "arguments", "artifact_store", "llm_config":
			default:
				t.Errorf("unexpected Action context %s", key)
			}
		}
		var req rewriteRequest
		if err := json.Unmarshal([]byte(mustJSONRewrite(raw)), &req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if req.Reference != rewriteReference || req.ArtifactStore == "" {
			t.Errorf("reference/store=%#v", req)
		}
		var envelope map[string]any
		if err := json.Unmarshal(req.Artifact, &envelope); err != nil || len(envelope) != 1 || envelope["data"] == nil {
			t.Errorf("logical document must use only data envelope, got %s", req.Artifact)
			w.WriteHeader(422)
			return
		}
		source := envelope["data"]
		server.mu.Lock()
		server.requests = append(server.requests, req)
		hook := server.beforeExecute
		gate := server.executeGate
		failureStatus, failureCode := server.failureStatus, server.failureCode
		override := server.resultOverride
		server.mu.Unlock()
		if failureStatus != 0 {
			w.WriteHeader(failureStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{"code": failureCode, "message": "private-upstream-detail", "retryable": false, "errors": []any{map[string]any{"input": rewriteTestKey}}}})
			return
		}
		if req.Phase == "preview" {
			expectedArguments := rewriteCompatibilityArguments(f)
			if server.argumentsOverride != nil {
				expectedArguments = server.argumentsOverride
			}
			if !reflect.DeepEqual(source, f.source) || !reflect.DeepEqual(req.Arguments, expectedArguments) {
				t.Errorf("preview content/args=%#v / %#v", source, req.Arguments)
			}
			llm, ok := req.LLMConfig["llm"].(map[string]any)
			if !ok || llm["model"] != "rewrite-test-model" || llm["api_key"] != rewriteTestKey {
				t.Error("preview did not use the owner configured llm")
			}
			server.mu.Lock()
			token := fmt.Sprintf("%032x", len(server.manifests)+1)
			artifact := map[string]any{"content_type": map[bool]string{true: "text", false: "json"}[f.representation == "markdown"], "value": f.candidate}
			server.manifests[req.ArtifactStore+"/"+token] = rewriteManifest{source: source, artifact: artifact, representation: f.representation}
			server.mu.Unlock()
			result := map[string]any{"representation": f.representation, "results": []any{map[string]any{"target": map[string]any{"type": "block", "block_type": "paragraph", "node_id": "p"}, "preview": map[string]any{"old_text": rewriteSelectedText(f), "new_text": "Rewritten."}, "patch": map[string]any{"type": map[bool]string{true: "string_replace_set", false: "writer_ir_patch"}[f.representation == "markdown"], "payload": map[string]any{}}}}, "artifact": artifact, "commit": map[string]any{"token": token}}
			if override != nil {
				result = override
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
			return
		}
		if req.Phase != "execute" {
			t.Errorf("unknown phase %q", req.Phase)
			w.WriteHeader(422)
			return
		}
		if len(req.LLMConfig) != 0 {
			t.Error("execute must not load/send model config")
		}
		token, _ := req.Arguments["commit_token"].(string)
		if len(req.Arguments) != 1 {
			t.Errorf("execute accepts only commit token: %#v", req.Arguments)
		}
		server.mu.Lock()
		manifest, exists := server.manifests[req.ArtifactStore+"/"+token]
		server.mu.Unlock()
		if !exists || !reflect.DeepEqual(manifest.source, source) {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{"code": "SELECTION_STALE", "message": "stale preview", "retryable": false}})
			return
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
		if hook != nil {
			hook()
		}
		result := map[string]any{"representation": manifest.representation, "artifact": manifest.artifact}
		if override != nil {
			result = override
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	}))
	server.url = httpServer.URL
	t.Cleanup(httpServer.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", httpServer.URL)
	return server
}
func (s *rewriteServer) calls() []rewriteRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rewriteRequest(nil), s.requests...)
}
func rewriteToken(t *testing.T, f rewriteFixture) string {
	t.Helper()
	result := rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", f.body("preview", "")))
	return requireRewritePreview(t, f, result)
}
func requireRewritePreview(t *testing.T, f rewriteFixture, result map[string]any) string {
	t.Helper()
	expected := map[string]any{
		"representation": f.representation,
		"target":         map[string]any{"type": "block", "block_type": "paragraph", "node_id": "p"},
		"preview":        map[string]any{"old_text": rewriteSelectedText(f), "new_text": "Rewritten."},
		"patch":          map[string]any{"type": map[bool]string{true: "string_replace_set", false: "writer_ir_patch"}[f.representation == "markdown"], "payload": map[string]any{}},
		"artifact":       map[string]any{"content_type": map[bool]string{true: "text", false: "json"}[f.representation == "markdown"], "value": f.candidate},
	}
	for key, want := range expected {
		if !reflect.DeepEqual(result[key], want) {
			t.Errorf("public preview %s=%#v want=%#v", key, result[key], want)
		}
	}
	if strings.Contains(mustJSONRewrite(result), rewriteTestKey) {
		t.Error("preview leaked model credential")
	}

	commit, ok := result["commit"].(map[string]any)
	token, _ := commit["token"].(string)
	if !ok || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(token) {
		t.Fatalf("invalid preview commit=%#v", result)
	}
	return token
}
func rewriteSnapshot(t *testing.T, f rewriteFixture) string {
	t.Helper()
	var attempts []orm.WorkflowSessionStep
	var decisions []orm.WorkflowRouteDecision
	var bindings []orm.WorkflowAttemptInputBinding
	for _, model := range []any{&attempts, &decisions, &bindings} {
		if err := f.db.Order("id").Find(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	return descriptorSnapshot(t, f.descriptorFixture) + mustDescriptorJSON(t, []any{attempts, decisions, bindings})
}

func TestDocumentRewritePreviewExecute(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, list := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/list=%v", representation, list), func(t *testing.T) {
				f := newRewriteFixture(t, representation)
				seedRewriteTerminalGraph(t, f)
				server := newRewriteServer(t, f)
				if list {
					if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("list_index", 0).Error; err != nil {
						t.Fatal(err)
					}
					f.seed(t, "other-item", "other-slot", "text/markdown", `{"text":"other item"}`)
					if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "other-item").Updates(map[string]any{"slot_id": "unknown-slot", "slot": "unknown-slot", "list_index": 1}).Error; err != nil {
						t.Fatal(err)
					}
				}
				before := rewriteSnapshot(t, f)
				token := rewriteToken(t, f)
				if rewriteSnapshot(t, f) != before {
					t.Error("preview persisted Core state")
				}
				if err := f.db.Where("user_id = ?", "descriptor-owner").Delete(&orm.UserSelectedModel{}).Error; err != nil {
					t.Fatal(err)
				}
				var modelReads atomic.Int32
				f.db.Callback().Query().After("gorm:query").Register("rewrite-execute-no-model", func(tx *gorm.DB) {
					if strings.Contains(tx.Statement.SQL.String(), "user_selected_models") {
						modelReads.Add(1)
					}
				})
				t.Cleanup(func() { f.db.Callback().Query().Remove("rewrite-execute-no-model") })
				result := rewriteData(t, f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", token)))
				if modelReads.Load() != 0 {
					t.Error("execute reloaded model configuration")
				}
				calls := server.calls()
				if len(calls) != 2 || calls[0].Phase != "preview" || calls[1].Phase != "execute" || calls[0].ArtifactStore != calls[1].ArtifactStore {
					t.Fatalf("unexpected action sequence=%#v", calls)
				}
				var rows []orm.WorkflowSlotRevision
				if err := f.db.Where("slot_id = ? AND selected = ?", "unknown-slot", true).Order("list_index").Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				wantCount := 1
				if list {
					wantCount = 2
				}
				if len(rows) != wantCount || rows[0].Revision != 4 || rows[0].ID == "descriptor-artifact" || rows[0].HumanArtifactID == nil {
					t.Fatalf("new selection=%#v", rows)
				}
				row := rows[0]
				if list && (row.ListIndex == nil || *row.ListIndex != 0 || rows[1].ID != "other-item") {
					t.Errorf("list scope=%#v", rows)
				}
				if result["artifact_id"] != row.ID || result["revision"] != float64(4) || result["draft_version"] != float64(1) {
					t.Errorf("execute identity=%#v", result)
				}
				var human orm.WorkflowHumanArtifact
				if err := f.db.First(&human, "id = ?", *row.HumanArtifactID).Error; err != nil {
					t.Fatal(err)
				}
				var wantValue any = map[string]any{"schema": descriptorIRSchema, "data": f.candidate}
				wantType := "json"
				if representation == "markdown" {
					wantType = "text/markdown"
					wantValue = map[string]any{"text": f.candidate}
				}
				var value any
				if err := json.Unmarshal(human.Value, &value); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(value, wantValue) || human.ContentType != wantType || human.DraftVersion != 1 {
					t.Errorf("persisted candidate=%#v type=%s draft=%d", value, human.ContentType, human.DraftVersion)
				}
				var events []orm.WorkflowEvent
				f.db.Order("id").Find(&events)
				if len(events) != 1 || events[0].EventType != "artifact.upsert" || events[0].EntityID != row.ID || events[0].StateVersion != 10 || events[0].OwnerUserID != "descriptor-owner" || events[0].ContractVersion != "workflow.v1" {
					t.Errorf("events=%#v", events)
				}
				if len(events) == 1 {
					var payload map[string]any
					if err := json.Unmarshal(events[0].PayloadJSON, &payload); err != nil {
						t.Fatal(err)
					}
					if payload["artifact_id"] != row.ID || payload["revision"] != float64(4) || payload["draft_version"] != float64(1) || payload["state_version"] != float64(10) || payload["change_source"] != "human" {
						t.Errorf("event payload=%#v", payload)
					}
				}
				var session orm.WorkflowSession
				f.db.First(&session, "id = ?", "descriptor-session")
				if session.StateVersion != 10 {
					t.Errorf("state=%d", session.StateVersion)
				}
				requireRewriteGraphState(t, f, "stale", false)
				var originalHuman orm.WorkflowHumanArtifact
				if err := f.db.First(&originalHuman, "id = ?", "descriptor-artifact-human").Error; err != nil {
					t.Fatal(err)
				}
				var originalValue any
				if err := json.Unmarshal(originalHuman.Value, &originalValue); err != nil {
					t.Fatal(err)
				}
				wantOriginal := f.source
				if representation == "ir" {
					wantOriginal = map[string]any{"schema": descriptorIRSchema, "data": f.source}
				}
				if !reflect.DeepEqual(originalValue, wantOriginal) || originalHuman.DraftVersion != 7 {
					t.Error("rewrite overwrote original human source")
				}
				var old orm.WorkflowSlotRevision
				f.db.First(&old, "id = ?", "descriptor-artifact")
				if old.Selected || old.Revision != 3 || old.Validity != "effective" || old.HumanArtifactID == nil || *old.HumanArtifactID != "descriptor-artifact-human" {
					t.Errorf("old lineage=%#v", old)
				}
				record := descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/"+row.ID, "descriptor-owner"))[0]
				doc, _ := record["document"].(map[string]any)
				if doc["representation"] != representation {
					t.Errorf("new revision lost document identity=%#v", record)
				}
				after := rewriteSnapshot(t, f)
				rewriteError(t, f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", token)), 409, "REVISION_CONFLICT")
				if rewriteSnapshot(t, f) != after {
					t.Error("duplicate execute mutated state")
				}
			})
		}
	}
}

func TestDocumentRewritePreconditionsBeforeCost(t *testing.T) {
	for _, phase := range []string{"preview", "execute"} {
		for _, kind := range []string{"missing identity", "wrong owner", "missing revision", "stale revision", "missing draft", "stale draft", "historical", "stale", "deleted", "dismissed", "ordinary text", "live"} {
			t.Run(phase+kind, func(t *testing.T) {
				f := newRewriteFixture(t, "markdown")
				server := newRewriteServer(t, f)
				body := f.body(phase, strings.Repeat("a", 32))
				owner := "descriptor-owner"
				status := 409
				code := "REVISION_CONFLICT"
				switch kind {
				case "missing identity":
					owner = ""
					status = 400
					code = "IDENTITY_REQUIRED"
				case "wrong owner":
					owner = "other"
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
					body["base_draft_version"] = 6
					code = "DRAFT_VERSION_CONFLICT"
				case "historical":
					f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false)
				case "stale", "deleted":
					f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", kind)
				case "dismissed":
					f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("dismissed", true)
					status = 404
					code = "ARTIFACT_NOT_FOUND"
				case "ordinary text":
					f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("content_type", "text")
					status = 422
					code = "DOCUMENT_ACTION_UNSUPPORTED"
				case "live":
					seedRewriteConsumer(t, f, "running")
					code = "ARTIFACT_IN_USE"
				}
				var reads, contentReads atomic.Int32
				f.db.Callback().Query().After("gorm:query").Register("rewrite-preflight-model", func(tx *gorm.DB) {
					if strings.Contains(tx.Statement.SQL.String(), "plugin_human_artifacts") { // workflow-naming: persistence
						contentReads.Add(1)
					}
					if strings.Contains(tx.Statement.SQL.String(), "user_selected_models") {
						reads.Add(1)
					}
				})
				t.Cleanup(func() { f.db.Callback().Query().Remove("rewrite-preflight-model") })
				before := rewriteSnapshot(t, f)
				contentReads.Store(0)
				rewriteError(t, f.post(t.Context(), phase, "descriptor-artifact", owner, body), status, code)
				if reads.Load() != 0 || len(server.calls()) != 0 {
					t.Errorf("rejected request reached model/action: reads=%d calls=%d", reads.Load(), len(server.calls()))
				}
				if kind == "missing identity" || kind == "wrong owner" {
					server.mu.Lock()
					inspectCalls := server.inspectCalls
					server.mu.Unlock()
					if contentReads.Load() != 0 || inspectCalls != 0 {
						t.Errorf("unauthorized preflight read content=%d inspect=%d", contentReads.Load(), inspectCalls)
					}
				}
				if rewriteSnapshot(t, f) != before {
					t.Error("precondition failure wrote state")
				}
			})
		}
	}
}

func seedRewriteConsumer(t *testing.T, f rewriteFixture, status string) {
	t.Helper()
	now := time.Now().UTC()
	for _, row := range []any{&orm.WorkflowSessionStep{ID: "rewrite-consumer", SessionID: "descriptor-session", StepID: "consumer", TaskID: "rewrite-consumer-task", Attempt: 1, Status: status, Validity: "effective", CreatedAt: now, UpdatedAt: now}, &orm.WorkflowAttemptInputBinding{ID: "rewrite-binding", SessionID: "descriptor-session", AttemptID: "rewrite-consumer", MaterialID: "unknown-slot", MaterialRevisionID: "descriptor-artifact", SourceType: "artifact", CreatedAt: now}, &orm.WorkflowSlotRevision{ID: "rewrite-output", SessionID: "descriptor-session", SlotID: "consumer-output", Slot: "consumer-output", Revision: 1, Selected: true, Validity: "effective", ProducerAttemptID: "rewrite-consumer", StepID: "consumer", Attempt: 1, ContentSnapshot: json.RawMessage(`42`), CreatedAt: now}} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestDocumentRewriteRejectsInputInjection(t *testing.T) {
	for _, phase := range []string{"preview", "execute"} {
		for _, field := range []string{"unknown action", "reference", "artifact_store", "artifact", "phase", "llm_config", "tool_config", "candidate", "patch", "document", "selection mismatch", "malformed token"} {
			t.Run(phase+field, func(t *testing.T) {
				f := newRewriteFixture(t, "markdown")
				server := newRewriteServer(t, f)
				body := f.body(phase, strings.Repeat("a", 32))
				status := 400
				code := "DOCUMENT_ACTION_INVALID"
				switch field {
				case "unknown action":
					body["action"] = "unregistered_document_action"
					status = 422
					code = "DOCUMENT_ACTION_UNSUPPORTED"
				case "candidate", "patch", "document":
					body["input"].(map[string]any)[field] = "injected"
				case "selection mismatch":
					body["input"].(map[string]any)["selection"] = map[string]any{"type": "ir", "node_id": "p"}
				case "malformed token":
					body["input"].(map[string]any)["commit_token"] = "../invalid"
				default:
					body[field] = "injected"
				}
				before := rewriteSnapshot(t, f)
				rewriteError(t, f.post(t.Context(), phase, "descriptor-artifact", "descriptor-owner", body), status, code)
				if len(server.calls()) != 0 {
					t.Error("injection reached Algorithm")
				}
				if rewriteSnapshot(t, f) != before {
					t.Error("injection persisted")
				}
			})
		}
	}
}

func TestDocumentRewriteTokenScopeAndSource(t *testing.T) {
	for _, kind := range []string{"other artifact", "other owner", "draft advanced", "source changed", "missing token"} {
		t.Run(kind, func(t *testing.T) {
			f := newRewriteFixture(t, "markdown")
			server := newRewriteServer(t, f)
			token := rewriteToken(t, f)
			id := "descriptor-artifact"
			owner := "descriptor-owner"
			body := f.body("execute", token)
			switch kind {
			case "other artifact":
				f.seed(t, "other-artifact", "other-slot", "text/markdown", mustJSONRewrite(f.source))
				id = "other-artifact"
			case "other owner":
				owner = "new-owner"
				if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("create_user_id", owner).Error; err != nil {
					t.Fatal(err)
				}
			case "draft advanced":
				f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("draft_version", 8)
				body["base_draft_version"] = 8
			case "source changed":
				f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(`{"text":"changed outside preview"}`))
			case "missing token":
				body["input"] = map[string]any{"commit_token": strings.Repeat("f", 32)}
			}
			before := rewriteSnapshot(t, f)
			rewriteError(t, f.post(t.Context(), "execute", id, owner, body), 409, "SELECTION_STALE")
			if rewriteSnapshot(t, f) != before {
				t.Error("stale token persisted")
			}
			calls := server.calls()
			if (kind == "other artifact" || kind == "other owner" || kind == "draft advanced") && len(calls) > 1 && calls[0].ArtifactStore == calls[1].ArtifactStore {
				t.Error("preview namespace not bound to Artifact/baseline")
			}
		})
	}
}

func TestDocumentRewriteFinalCASAndLiveGuard(t *testing.T) {
	for _, kind := range []string{"writer", "live"} {
		t.Run(kind, func(t *testing.T) {
			f := newRewriteFixture(t, "markdown")
			server := newRewriteServer(t, f)
			token := rewriteToken(t, f)
			afterConcurrent := ""
			server.beforeExecute = func() {
				if kind == "live" {
					seedRewriteConsumer(t, f, "running")
				} else {
					base, draft := 3, int64(7)
					if _, err := workflow.WriteSlotRevisionWithHumanArtifact(t.Context(), f.db.DB, "descriptor-session", "unknown-slot", "unknown-slot", "source", 1, "single", nil, "text/markdown", json.RawMessage(`{"text":"concurrent writer"}`), nil, "human", &base, &draft); err != nil {
						t.Error(err)
					}
				}
				afterConcurrent = rewriteSnapshot(t, f)
			}
			code := "REVISION_CONFLICT"
			if kind == "live" {
				code = "ARTIFACT_IN_USE"
			}
			rewriteError(t, f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", token)), 409, code)
			if afterConcurrent == "" || rewriteSnapshot(t, f) != afterConcurrent {
				t.Error("action leaked mutation after concurrent change")
			}
		})
	}
}

func TestDocumentRewriteEventFailureRollsBack(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	seedRewriteTerminalGraph(t, f)
	newRewriteServer(t, f)
	token := rewriteToken(t, f)
	if err := f.db.Callback().Create().Before("gorm:create").Register("rewrite-fail-event", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "workflow_events" {
			tx.AddError(errors.New("forced document event failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Callback().Create().Remove("rewrite-fail-event") })
	before := rewriteSnapshot(t, f)
	w := f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", token))
	rewriteError(t, w, 500, "DOCUMENT_ACTION_SAVE_FAILED")
	if rewriteSnapshot(t, f) != before {
		t.Error("failed event leaked human/revision/selection/validity/state")
	}
}

func TestDocumentRewriteAlgorithmFailuresAndInvalidResults(t *testing.T) {
	for _, kind := range []string{"upstream", "missing artifact", "wrong representation", "file carrier"} {
		t.Run(kind, func(t *testing.T) {
			f := newRewriteFixture(t, "markdown")
			server := newRewriteServer(t, f)
			token := rewriteToken(t, f)
			switch kind {
			case "upstream":
				server.failureStatus = 502
				server.failureCode = "WORKFLOW_ACTION_FAILED"
			case "missing artifact":
				server.resultOverride = map[string]any{"representation": "markdown"}
			case "wrong representation":
				server.resultOverride = map[string]any{"representation": "ir", "artifact": map[string]any{"content_type": "json", "value": f.candidate}}
			case "file carrier":
				server.resultOverride = map[string]any{"representation": "markdown", "artifact": map[string]any{"content_type": "file", "value": map[string]any{"path": "/tmp/private-upstream-detail"}}}
			}
			before := rewriteSnapshot(t, f)
			code := "DOCUMENT_ACTION_RESULT_INVALID"
			if kind == "upstream" {
				code = "DOCUMENT_ACTION_FAILED"
			}
			rewriteError(t, f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", token)), 502, code)
			if rewriteSnapshot(t, f) != before {
				t.Error("invalid result persisted")
			}
		})
	}
}

func TestDocumentRewriteCapabilitiesAndOpenAPI(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	newRewriteServer(t, f)
	record := descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/descriptor-artifact", "descriptor-owner"))[0]
	doc, _ := record["document"].(map[string]any)
	caps, _ := doc["capabilities"].([]any)
	found := false
	for _, capability := range caps {
		found = found || capability == "rewrite_selection"
		if capability != "rewrite_selection" && capability != "save" && capability != "convert_document" && capability != "numbering" && capability != "cross_reference" && capability != "publish_document" {
			t.Errorf("unsupported capability=%v", capability)
		}
	}
	if !found {
		t.Error("configured editable document lacks rewrite_selection")
	}
	if err := f.db.Where("user_id = ?", "descriptor-owner").Delete(&orm.UserSelectedModel{}).Error; err != nil {
		t.Fatal(err)
	}
	record = descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/descriptor-artifact", "descriptor-owner"))[0]
	doc, _ = record["document"].(map[string]any)
	caps, _ = doc["capabilities"].([]any)
	for _, capability := range caps {
		if capability == "rewrite_selection" {
			t.Error("rewrite advertised without model")
		}
	}
	if !reflect.DeepEqual(caps, []any{"save", "convert_document", "numbering", "cross_reference", "publish_document"}) || doc["editable"] != true {
		t.Errorf("missing model removed ordinary save: %#v", doc)
	}

	raw, err := buildOpenAPISpecFromRouter(f.router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"preview", "execute"} {
		op := openAPIOperationForTest(t, spec, "post", "/api/core/workflow-artifacts/{artifact_id}/document-actions:"+phase)
		if op["requestBody"] == nil {
			t.Errorf("%s missing request schema", phase)
		}
		if op["responses"] == nil {
			t.Errorf("%s missing responses", phase)
		}
	}
}

func TestDocumentRewriteCancellation(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path == "/api/document:inspect" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(descriptorMarkdown))
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
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	before := rewriteSnapshot(t, f)
	go func() {
		done <- f.post(ctx, "preview", "descriptor-artifact", "descriptor-owner", f.body("preview", ""))
	}()
	select {
	case <-started:
	case w := <-done:
		t.Fatalf("preview did not invoke Algorithm: status=%d", w.Code)
	case <-time.After(2 * time.Second):
		t.Fatal("preview did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not reach Algorithm")
	}
	select {
	case w := <-done:
		if w.Code < 400 {
			t.Errorf("cancel returned success=%d", w.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not complete")
	}
	if rewriteSnapshot(t, f) != before {
		t.Error("cancelled preview persisted")
	}
}

func rewriteSelectedText(f rewriteFixture) string {
	if text, ok := f.selection["selected_text"].(string); ok {
		return text
	}
	return "Original."
}

func seedRewriteTerminalGraph(t *testing.T, f rewriteFixture) {
	t.Helper()
	seedRewriteConsumer(t, f, "succeeded")
	now := time.Now().UTC()
	for _, name := range []string{"downstream", "route"} {
		for _, row := range []any{&orm.WorkflowSessionStep{ID: "rewrite-" + name, SessionID: "descriptor-session", StepID: name, Attempt: 1, TaskID: "rewrite-" + name + "-task", Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now}, &orm.WorkflowSlotRevision{ID: "rewrite-" + name + "-output", SessionID: "descriptor-session", SlotID: name + "-output", Slot: name + "-output", Revision: 1, Selected: true, Validity: "effective", ProducerAttemptID: "rewrite-" + name, StepID: name, Attempt: 1, ContentSnapshot: json.RawMessage(`42`), CreatedAt: now}} {
			if err := f.db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, row := range []any{&orm.WorkflowAttemptInputBinding{ID: "rewrite-downstream-binding", SessionID: "descriptor-session", AttemptID: "rewrite-downstream", MaterialID: "consumer-output", MaterialRevisionID: "rewrite-output", SourceType: "artifact", CreatedAt: now}, &orm.WorkflowRouteDecision{ID: "rewrite-decision", SessionID: "descriptor-session", FromStepID: "source", ActivatedJSON: json.RawMessage(`["route"]`), PrunedJSON: json.RawMessage(`[]`), BypassedJSON: json.RawMessage(`[]`), WitnessJSON: json.RawMessage(`[{"material_id":"unknown-slot","revision_id":"descriptor-artifact"}]`), Validity: "effective", CreatedAt: now}} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}
func requireRewriteGraphState(t *testing.T, f rewriteFixture, validity string, selected bool) {
	t.Helper()
	for _, id := range []string{"rewrite-consumer", "rewrite-downstream", "rewrite-route"} {
		var row orm.WorkflowSessionStep
		if err := f.db.First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.Validity != validity {
			t.Errorf("attempt %s validity=%s", id, row.Validity)
		}
	}
	for _, id := range []string{"rewrite-output", "rewrite-downstream-output", "rewrite-route-output"} {
		var row orm.WorkflowSlotRevision
		if err := f.db.First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.Validity != validity || row.Selected != selected {
			t.Errorf("output %s state=%s/%v", id, row.Validity, row.Selected)
		}
	}
	var decision orm.WorkflowRouteDecision
	if err := f.db.First(&decision, "id = ?", "rewrite-decision").Error; err != nil {
		t.Fatal(err)
	}
	if decision.Validity != validity {
		t.Errorf("decision validity=%s", decision.Validity)
	}
}

func TestDocumentRewriteConcurrentExecuteAppliesOnce(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	newRewriteServer(t, f)
	token := rewriteToken(t, f)
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			<-start
			results <- f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", token))
		}()
	}
	close(start)
	success, conflicts := 0, 0
	for range 2 {
		select {
		case w := <-results:
			if w.Code == 200 {
				success++
			} else {
				rewriteError(t, w, 409, "REVISION_CONFLICT")
				conflicts++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent execute blocked")
		}
	}
	if success != 1 || conflicts != 1 {
		t.Errorf("success=%d conflicts=%d", success, conflicts)
	}
	var revisions []orm.WorkflowSlotRevision
	var humans []orm.WorkflowHumanArtifact
	var events []orm.WorkflowEvent
	var session orm.WorkflowSession
	for _, rows := range []any{&revisions, &humans, &events} {
		if err := f.db.Find(rows).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.First(&session, "id = ?", "descriptor-session").Error; err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || len(humans) != 2 || len(events) != 1 || session.StateVersion != 10 {
		t.Fatalf("duplicate effect: revisions=%d humans=%d events=%d state=%d", len(revisions), len(humans), len(events), session.StateVersion)
	}
	selected := 0
	for _, row := range revisions {
		if row.Selected {
			selected++
			if row.Revision != 4 || row.ID != events[0].EntityID {
				t.Errorf("selected/event mismatch=%#v", row)
			}
		}
	}
	if selected != 1 {
		t.Errorf("selected=%d", selected)
	}
}

func TestDocumentRewriteModelAvailabilityAndPreviewErrors(t *testing.T) {
	for _, kind := range []string{"no model", "upstream", "invalid preview"} {
		t.Run(kind, func(t *testing.T) {
			f := newRewriteFixture(t, "markdown")
			server := newRewriteServer(t, f)
			status := 502
			code := "DOCUMENT_ACTION_FAILED"
			if kind == "no model" {
				if err := f.db.Where("user_id = ?", "descriptor-owner").Delete(&orm.UserSelectedModel{}).Error; err != nil {
					t.Fatal(err)
				}
				status = 400
				code = "MODEL_CONFIG_REQUIRED"
			} else if kind == "upstream" {
				server.failureStatus = 502
				server.failureCode = "WORKFLOW_ACTION_FAILED"
			} else {
				server.resultOverride = map[string]any{"representation": "markdown", "commit": map[string]any{"token": "invalid"}}
				code = "DOCUMENT_ACTION_RESULT_INVALID"
			}
			before := rewriteSnapshot(t, f)
			rewriteError(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", f.body("preview", "")), status, code)
			if kind == "no model" && len(server.calls()) != 0 {
				t.Error("preview without model reached Action")
			}
			if rewriteSnapshot(t, f) != before {
				t.Error("preview failure mutated Core")
			}
		})
	}
}

func TestDocumentRewriteLiteralPathIsContent(t *testing.T) {
	for _, source := range []string{"/tmp/looks-like-a-file.md", `{"data":"this is literal JSON text"}`} {
		t.Run(source, func(t *testing.T) {
			f := newRewriteFixture(t, "markdown")
			f.source = source
			f.selection = map[string]any{"type": "markdown", "selected_text": source}
			if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(source))).Error; err != nil {
				t.Fatal(err)
			}
			newRewriteServer(t, f)
			before := rewriteSnapshot(t, f)
			rewriteToken(t, f)
			if rewriteSnapshot(t, f) != before {
				t.Error("literal-content preview mutated Core")
			}
		})
	}
}

func TestDocumentRewriteConversationScopePrecedesContentRead(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	server := newRewriteServer(t, f)
	var reads atomic.Int32
	f.db.Callback().Query().After("gorm:query").Register("rewrite-forbidden-content", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "plugin_human_artifacts") || strings.Contains(tx.Statement.SQL.String(), "user_selected_models") { // workflow-naming: persistence
			reads.Add(1)
		}
	})
	t.Cleanup(func() { f.db.Callback().Query().Remove("rewrite-forbidden-content") })
	for _, phase := range []string{"preview", "execute"} {
		req := httptest.NewRequest(http.MethodPost, "/workflow-artifacts/descriptor-artifact/document-actions:"+phase, strings.NewReader(mustJSONRewrite(f.body(phase, strings.Repeat("a", 32)))))
		req.Header.Set("X-User-Id", "descriptor-owner")
		req.Header.Set("X-LazyMind-Invocation-Conversation-Id", "different-conversation")
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, req)
		rewriteError(t, w, 403, "PERMISSION_DENIED")
	}
	if reads.Load() != 0 || len(server.calls()) != 0 {
		t.Errorf("scope violation read content/model=%d calls=%d", reads.Load(), len(server.calls()))
	}
}

func TestDocumentRewriteOpenAPITypes(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
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
		// Keep the legacy assertions intact inside the additive input union.
		if branches, ok := obj["oneOf"].([]any); ok {
			for _, raw := range branches {
				branch, _ := raw.(map[string]any)
				properties, _ := branch["properties"].(map[string]any)
				if properties["instruction"] != nil && properties["selection"] != nil {
					obj = branch
					break
				}
			}
		}
		return obj
	}
	requiredField := func(t *testing.T, schema map[string]any, field string, want bool) {
		t.Helper()
		found := false
		for _, name := range schemaStringList(schema["required"]) {
			found = found || name == field
		}
		if found != want {
			t.Errorf("required %s=%v want%v", field, found, want)
		}
	}

	for _, phase := range []string{"preview", "execute"} {
		t.Run(phase, func(t *testing.T) {
			op := openAPIOperationForTest(t, spec, "post", "/api/core/workflow-artifacts/{artifact_id}/document-actions:"+phase)
			body := op["requestBody"].(map[string]any)
			content := body["content"].(map[string]any)["application/json"].(map[string]any)
			request := documentActionRequestSchemaForTest(t, content["schema"], schemas, "rewrite_selection")
			props, _ := request["properties"].(map[string]any)
			for field, want := range map[string]string{"action": "string", "base_revision": "integer", "base_draft_version": "integer", "input": "object"} {
				if resolve(props[field])["type"] != want {
					t.Errorf("%s request field %s type=%#v", phase, field, props[field])
				}
			}
			required := schemaStringList(request["required"])
			for _, field := range []string{"action", "base_revision", "input"} {
				found := false
				for _, value := range required {
					found = found || value == field
				}
				if !found {
					t.Errorf("request %s must be required", field)
				}
			}
			requiredField(t, request, "base_draft_version", false)
			input := resolve(props["input"])
			inputProps, _ := input["properties"].(map[string]any)
			if phase == "preview" {
				requiredField(t, input, "instruction", true)
				requiredField(t, input, "selection", true)
				selection := resolve(inputProps["selection"])
				branches, _ := selection["oneOf"].([]any)
				if len(branches) == 0 {
					branches, _ = selection["anyOf"].([]any)
				}
				for representation, field := range map[string]string{"markdown": "selected_text", "ir": "node_id"} {
					found := false
					for _, branch := range branches {
						schema := resolve(branch)
						properties, _ := schema["properties"].(map[string]any)
						matches := false
						for _, value := range schemaStringList(resolve(properties["type"])["enum"]) {
							matches = matches || value == representation
						}
						if !matches {
							continue
						}
						found = true
						if resolve(properties["type"])["type"] != "string" || resolve(properties[field])["type"] != "string" {
							t.Errorf("selection %s types invalid", representation)
						}
						requiredField(t, schema, "type", true)
						requiredField(t, schema, field, true)
					}
					if !found {
						t.Errorf("selection contract lacks %s branch", representation)
					}
				}
				if resolve(inputProps["instruction"])["type"] != "string" || inputProps["selection"] == nil {
					t.Error("preview input not described")
				}
			} else if resolve(inputProps["commit_token"])["type"] != "string" {
				t.Error("execute token not described")
			}
			if phase == "execute" {
				requiredField(t, input, "commit_token", true)
			}
			responses := op["responses"].(map[string]any)
			response := responses["200"].(map[string]any)
			responseSchema := resolve(response["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
			responseProps, _ := responseSchema["properties"].(map[string]any)
			data := documentActionResultSchemaForTest(t, responseProps["data"], schemas, "rewrite_selection")
			dataProps, _ := data["properties"].(map[string]any)
			if phase == "execute" {
				for field, want := range map[string]string{"artifact_id": "string", "revision": "integer", "draft_version": "integer"} {
					if resolve(dataProps[field])["type"] != want {
						t.Errorf("execute result %s missing %s", field, want)
					}
				}
			} else {
				for field, wantType := range map[string]string{"representation": "string", "target": "object", "preview": "object", "patch": "object", "artifact": "object", "commit": "object"} {
					if resolve(dataProps[field])["type"] != wantType {
						t.Errorf("preview %s type=%#v", field, dataProps[field])
					}
					requiredField(t, data, field, true)
				}
				for field, fields := range map[string]map[string]string{
					"target":   {"type": "string", "block_type": "string"},
					"preview":  {"old_text": "string", "new_text": "string"},
					"patch":    {"type": "string", "payload": "object"},
					"artifact": {"content_type": "string"},
				} {
					schema := resolve(dataProps[field])
					properties, _ := schema["properties"].(map[string]any)
					for name, wantType := range fields {
						if resolve(properties[name])["type"] != wantType {
							t.Errorf("preview %s.%s type=%#v", field, name, properties[name])
						}
						requiredField(t, schema, name, true)
					}
				}
				artifactSchema := resolve(dataProps["artifact"])
				artifactProps, _ := artifactSchema["properties"].(map[string]any)
				if _, ok := artifactProps["value"]; !ok {
					t.Error("preview artifact.value missing")
				}
				requiredField(t, artifactSchema, "value", true)
				commit := resolve(dataProps["commit"])
				requiredField(t, commit, "token", true)
				commitProps, _ := commit["properties"].(map[string]any)
				if resolve(commitProps["token"])["type"] != "string" {
					t.Error("preview commit response not described")
				}
			}
		})
	}
}

func TestDocumentRewriteReadLeaseCannotExecute(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	server := newRewriteServer(t, f)
	if err := f.db.AutoMigrate(&orm.ExternalChatRun{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiry := now.Add(time.Minute)
	run := orm.ExternalChatRun{ID: "rewrite-read-run", RequestID: "rewrite-read-request", ConversationID: "descriptor-conversation", HistoryID: "rewrite-history", Provider: "codex", ActorUserID: "descriptor-owner", Status: "running", HostID: "rewrite-host", LeaseToken: "rewrite-test-lease", LeaseExpiresAt: &expiry, CreatedAt: now, UpdatedAt: now}
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
	if w := call(http.MethodGet, "/workflow-artifacts/descriptor-artifact", ""); w.Code != 200 {
		t.Fatalf("read lease control=%d %s", w.Code, w.Body.String())
	}
	before := rewriteSnapshot(t, f)
	for _, phase := range []string{"preview", "execute"} {
		w := call(http.MethodPost, "/workflow-artifacts/descriptor-artifact/document-actions:"+phase, mustJSONRewrite(f.body(phase, strings.Repeat("a", 32))))
		if w.Code != 409 {
			t.Errorf("read lease action status=%d want409", w.Code)
		}
	}
	if len(server.calls()) != 0 {
		t.Error("read lease reached Action")
	}
	if rewriteSnapshot(t, f) != before {
		t.Error("read lease action persisted")
	}
}

func TestDocumentRewriteServerFixtureControl(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	server := newRewriteServer(t, f)
	call := func(phase, namespace string, source any, args map[string]any) (int, map[string]any) {
		body := map[string]any{"reference": rewriteReference, "phase": phase, "artifact": map[string]any{"data": source}, "arguments": args, "artifact_store": namespace}
		if phase == "preview" {
			body["llm_config"] = map[string]any{"llm": map[string]any{"model": "rewrite-test-model", "api_key": rewriteTestKey}}
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.url+"/api/document/actions:invoke", strings.NewReader(mustJSONRewrite(body)))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var value map[string]any
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, value
	}
	status, result := call("preview", "/fixture/a", f.source, rewriteCompatibilityArguments(f))
	if status != 200 {
		t.Fatalf("fixture preview status=%d", status)
	}
	token := result["result"].(map[string]any)["commit"].(map[string]any)["token"].(string)
	args := map[string]any{"commit_token": token}
	if status, _ := call("execute", "/fixture/a", f.source, args); status != 200 {
		t.Fatalf("fixture execute=%d", status)
	}
	if status, _ := call("execute", "/fixture/b", f.source, args); status != 409 {
		t.Fatalf("fixture accepted cross-namespace token: %d", status)
	}
	if status, _ := call("execute", "/fixture/a", "changed", args); status != 409 {
		t.Fatalf("fixture accepted stale source: %d", status)
	}
}

func TestDocumentRewriteNativeSourceDoesNotRequireDraftVersion(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		t.Run(representation, func(t *testing.T) {
			f := newRewriteFixture(t, representation)
			now := time.Now().UTC()
			ct := "text/markdown"
			if representation == "ir" {
				ct = "json"
			}
			storedSource := f.source
			if representation == "ir" {
				storedSource = map[string]any{"schema": descriptorIRSchema, "data": f.source}
			}
			source := orm.SubAgentArtifact{ID: "native-rewrite-source", TaskID: "native-rewrite-task", Slot: "unknown-slot", ContentType: ct, Value: json.RawMessage(mustJSONRewrite(storedSource)), Seq: 1, CreatedAt: now}
			if err := f.db.Create(&orm.WorkflowSessionStep{ID: "native-rewrite-attempt", SessionID: "descriptor-session", StepID: "source", TaskID: source.TaskID, Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Create(&source).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"human_artifact_id": nil, "artifact_seq": 1, "producer_attempt_id": "native-rewrite-attempt"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Delete(&orm.WorkflowHumanArtifact{}, "id = ?", "descriptor-artifact-human").Error; err != nil {
				t.Fatal(err)
			}
			var beforeSource orm.SubAgentArtifact
			if err := f.db.First(&beforeSource, "id = ?", source.ID).Error; err != nil {
				t.Fatal(err)
			}
			newRewriteServer(t, f)
			before := rewriteSnapshot(t, f)
			preview := f.body("preview", "")
			delete(preview, "base_draft_version")
			token := requireRewritePreview(t, f, rewriteData(t, f.post(t.Context(), "preview", "descriptor-artifact", "descriptor-owner", preview)))
			if rewriteSnapshot(t, f) != before {
				t.Error("native preview mutated Core")
			}
			execute := f.body("execute", token)
			delete(execute, "base_draft_version")
			result := rewriteData(t, f.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", execute))
			var selected orm.WorkflowSlotRevision
			if err := f.db.Where("slot_id = ? AND selected = ?", "unknown-slot", true).First(&selected).Error; err != nil {
				t.Fatal(err)
			}
			if selected.Revision != 4 || selected.HumanArtifactID == nil || selected.ID == "descriptor-artifact" {
				t.Fatalf("native rewrite did not create human revision: %#v", selected)
			}
			var human orm.WorkflowHumanArtifact
			if err := f.db.First(&human, "id = ?", *selected.HumanArtifactID).Error; err != nil {
				t.Fatal(err)
			}
			var value any
			if err := json.Unmarshal(human.Value, &value); err != nil {
				t.Fatal(err)
			}
			var want any = map[string]any{"schema": descriptorIRSchema, "data": f.candidate}
			if representation == "markdown" {
				want = map[string]any{"text": f.candidate}
			}
			if !reflect.DeepEqual(value, want) || human.DraftVersion != 1 || human.ContentType != ct {
				t.Errorf("native result value/type/draft=%#v/%s/%d", value, human.ContentType, human.DraftVersion)
			}
			var afterSource orm.SubAgentArtifact
			if err := f.db.First(&afterSource, "id = ?", source.ID).Error; err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeSource, afterSource) {
				t.Error("original native Artifact changed")
			}
			var old orm.WorkflowSlotRevision
			if err := f.db.First(&old, "id = ?", "descriptor-artifact").Error; err != nil {
				t.Fatal(err)
			}
			if old.Selected || old.HumanArtifactID != nil || old.ArtifactSeq == nil || *old.ArtifactSeq != 1 || old.Revision != 3 || old.ProducerAttemptID != "native-rewrite-attempt" {
				t.Errorf("native lineage changed=%#v", old)
			}
			var events []orm.WorkflowEvent
			if err := f.db.Find(&events).Error; err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].EntityID != selected.ID || events[0].StateVersion != 10 || events[0].EventType != "artifact.upsert" || events[0].OwnerUserID != "descriptor-owner" || events[0].ContractVersion != "workflow.v1" {
				t.Fatalf("native event=%#v", events)
			}
			var payload map[string]any
			if err := json.Unmarshal(events[0].PayloadJSON, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["artifact_id"] != selected.ID || payload["revision"] != float64(4) || payload["draft_version"] != float64(1) || payload["state_version"] != float64(10) {
				t.Errorf("native event payload=%#v", payload)
			}
			var session orm.WorkflowSession
			if err := f.db.First(&session, "id = ?", "descriptor-session").Error; err != nil {
				t.Fatal(err)
			}
			if session.StateVersion != 10 || result["artifact_id"] != selected.ID || result["revision"] != float64(4) || result["draft_version"] != float64(1) {
				t.Errorf("native response/state=%#v/%d", result, session.StateVersion)
			}
		})
	}
}

func TestDocumentRewriteCapabilityRequiresMutableState(t *testing.T) {
	for _, state := range []string{"session not editable", "historical", "stale", "live"} {
		for index, endpoint := range descriptorRoutes {
			t.Run(state+endpoint, func(t *testing.T) {
				f := newRewriteFixture(t, "markdown")
				server := newRewriteServer(t, f)
				var err error
				switch state {
				case "session not editable":
					err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("status", "unknown-state").Error
				case "historical":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false).Error
				case "stale":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", "stale").Error
				case "live":
					seedRewriteConsumer(t, f, "running")
				}
				if err != nil {
					t.Fatal(err)
				}
				before := rewriteSnapshot(t, f)
				records := descriptorOptionalRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))
				filtered := (state == "session not editable" && index == 2) || (state == "historical" && index == 4)
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
						doc, ok := record["document"].(map[string]any)
						if !ok {
							t.Fatalf("restricted document missing=%#v", record)
						}
						want := []any{}
						if state == "live" {
							want = []any{"convert_document", "numbering", "cross_reference"}
						}
						if doc["editable"] != false || !reflect.DeepEqual(doc["capabilities"], want) {
							t.Errorf("model enabled blocked capability: %#v", doc)
						}
					}
					if !found {
						t.Error("restricted artifact missing")
					}
				}
				if len(server.calls()) != 0 {
					t.Error("capability query invoked Action")
				}
				if rewriteSnapshot(t, f) != before {
					t.Error("capability query wrote state")
				}
			})
		}
	}
}

func TestDocumentRewriteSessionStateCheckedBeforeCost(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	server := newRewriteServer(t, f)
	token := rewriteToken(t, f)
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("status", "unknown-state").Error; err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	if err := f.db.Callback().Query().After("gorm:query").Register("rewrite-session-model", func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "user_selected_models") {
			reads.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Callback().Query().Remove("rewrite-session-model") })
	before := rewriteSnapshot(t, f)
	for _, phase := range []string{"preview", "execute"} {
		rewriteError(t, f.post(t.Context(), phase, "descriptor-artifact", "descriptor-owner", f.body(phase, token)), 409, "SESSION_NOT_EDITABLE")
	}
	if reads.Load() != 0 || len(server.calls()) != 1 {
		t.Errorf("closed Session reached model/action=%d/%d", reads.Load(), len(server.calls()))
	}
	if rewriteSnapshot(t, f) != before {
		t.Error("closed Session action persisted")
	}
}

func TestDocumentRewriteExecuteCancellationDoesNotSave(t *testing.T) {
	f := newRewriteFixture(t, "markdown")
	seedRewriteTerminalGraph(t, f)
	server := newRewriteServer(t, f)
	token := rewriteToken(t, f)
	gate := &rewriteExecuteGate{started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1), release: make(chan struct{})}
	server.mu.Lock()
	server.executeGate = gate
	server.mu.Unlock()
	t.Cleanup(func() { close(gate.release) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan *httptest.ResponseRecorder, 1)
	before := rewriteSnapshot(t, f)
	go func() {
		done <- f.post(ctx, "execute", "descriptor-artifact", "descriptor-owner", f.body("execute", token))
	}()
	select {
	case <-gate.started:
	case w := <-done:
		t.Fatalf("execute did not reach Algorithm: %d %s", w.Code, w.Body.String())
	case <-time.After(2 * time.Second):
		t.Fatal("execute did not start")
	}
	cancel()
	select {
	case <-gate.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("execute cancellation did not reach Algorithm")
	}
	select {
	case w := <-done:
		if w.Code < 400 {
			t.Errorf("cancelled execute returned success: %d", w.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled execute blocked")
	}
	if rewriteSnapshot(t, f) != before {
		t.Error("cancelled execute saved or invalidated state")
	}
	requireRewriteGraphState(t, f, "effective", true)
}

// This storage control is runnable before the new Action routes exist. In
// PostgreSQL it also proves the real varchar limit rather than trusting SQLite.
func TestDocumentRewriteIRStorageEnvelope(t *testing.T) {
	f := newRewriteFixture(t, "ir")
	expected := map[string]any{"schema": descriptorIRSchema, "data": f.source}
	now := time.Now().UTC()
	seq := 1
	source := orm.SubAgentArtifact{ID: "ir-storage-native", TaskID: "ir-storage-task", Slot: "ir-storage-slot", Seq: seq, ContentType: "json", Value: json.RawMessage(mustJSONRewrite(expected)), CreatedAt: now}
	for _, row := range []any{
		&orm.WorkflowSessionStep{ID: "ir-storage-attempt", SessionID: "descriptor-session", StepID: "ir-storage-step", TaskID: source.TaskID, Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now},
		&source,
		&orm.WorkflowSlotRevision{ID: "ir-storage-revision", SessionID: "descriptor-session", SlotID: source.Slot, Slot: source.Slot, Revision: 1, Selected: true, Validity: "effective", ArtifactSeq: &seq, StepID: "ir-storage-step", Attempt: 1, CreatedAt: now},
	} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var human orm.WorkflowHumanArtifact
	var native orm.SubAgentArtifact
	if err := f.db.First(&human, "id = ?", "descriptor-artifact-human").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&native, "id = ?", source.ID).Error; err != nil {
		t.Fatal(err)
	}
	for name, row := range map[string]struct {
		contentType string
		value       json.RawMessage
	}{"human": {human.ContentType, human.Value}, "native": {native.ContentType, native.Value}} {
		var value any
		if err := json.Unmarshal(row.value, &value); err != nil {
			t.Fatal(err)
		}
		if row.contentType != "json" || !reflect.DeepEqual(value, expected) {
			t.Errorf("%s IR storage=%s/%#v", name, row.contentType, value)
		}
	}
	if f.db.Dialector.Name() == "postgres" {
		var columns []struct {
			TableName              string
			CharacterMaximumLength int
		}
		if err := f.db.Raw(`SELECT table_name, character_maximum_length FROM information_schema.columns WHERE table_schema = current_schema() AND table_name IN ('plugin_human_artifacts', 'sub_agent_artifacts') AND column_name = 'content_type'`).Scan(&columns).Error; err != nil {
			t.Fatal(err)
		}
		if len(columns) != 2 {
			t.Fatalf("Artifact content_type columns=%#v", columns)
		}
		for _, column := range columns {
			if column.CharacterMaximumLength != 32 {
				t.Errorf("%s column limit=%d want32", column.TableName, column.CharacterMaximumLength)
			}
		}
		before := rewriteSnapshot(t, f)
		for _, row := range []any{
			&orm.WorkflowHumanArtifact{ID: "invalid-long-human", SessionID: "descriptor-session", Slot: "probe", ContentType: descriptorIRSchema, Value: human.Value, DraftVersion: 1, CreatedAt: now},
			&orm.SubAgentArtifact{ID: "invalid-long-native", TaskID: source.TaskID, Slot: "probe", Seq: 2, ContentType: descriptorIRSchema, Value: native.Value, CreatedAt: now},
		} {
			err := f.db.Create(row).Error
			var sqlError interface{ SQLState() string }
			if !errors.As(err, &sqlError) || sqlError.SQLState() != "22001" {
				t.Fatalf("long MIME must hit PostgreSQL length constraint: %v", err)
			}
		}
		if rewriteSnapshot(t, f) != before {
			t.Error("rejected length probes changed stored state")
		}
		var nativeCount int64
		if err := f.db.Model(&orm.SubAgentArtifact{}).Count(&nativeCount).Error; err != nil {
			t.Fatal(err)
		}
		if nativeCount != 1 {
			t.Errorf("rejected probe left native rows=%d", nativeCount)
		}
	}
	server := newRewriteServer(t, f)
	before := rewriteSnapshot(t, f)
	for _, id := range []string{"descriptor-artifact", "ir-storage-revision"} {
		record := descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/"+id, "descriptor-owner"))[0]
		requireDescriptorValue(t, record, mustJSONRewrite(expected))
		document, ok := record["document"].(map[string]any)
		if !ok || document["representation"] != "ir" || document["schema"] != descriptorIRSchema || record["content_type"] != "json" || record["artifact_id"] != id {
			t.Errorf("IR identity lost after readback=%#v", record)
		}
		if record["document_error"] != nil {
			t.Errorf("IR inspection error=%#v", record["document_error"])
		}
	}
	if len(server.calls()) != 0 {
		t.Error("storage readback invoked a rewrite Action")
	}
	if rewriteSnapshot(t, f) != before {
		t.Error("IR readback changed stored state")
	}
}
