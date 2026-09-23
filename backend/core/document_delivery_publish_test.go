package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow"
)

const deliveryProvider = "fixture-provider-outside-old-whitelist"

type deliveryCall struct {
	Reference  string                     `json:"reference"`
	Phase      string                     `json:"phase"`
	Artifact   json.RawMessage            `json:"artifact"`
	Arguments  map[string]json.RawMessage `json:"arguments"`
	ToolConfig map[string]any             `json:"tool_config"`
}
type deliveryServer struct {
	provider         string
	catalogCalls     int
	url              string
	mu               sync.Mutex
	calls            []deliveryCall
	authProviders    []string
	capabilities     []string
	representation   string
	source           any
	target           map[string]any
	result           map[string]any
	emptyCredentials bool
	authStatus       int
	writeStatus      int
	afterConvert     func()
	afterWrite       func()
}

func newDeliveryFixture(t *testing.T, representation string) (descriptorFixture, *deliveryServer) {
	t.Helper()
	f := newDescriptorFixture(t)
	if err := f.db.AutoMigrate(&orm.DocumentPublicationOperation{}, &orm.DocumentPublicationBinding{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	s := &deliveryServer{provider: deliveryProvider, representation: representation, source: "# Original", capabilities: []string{"create", "replace", "patch", "load", "revision_check"}, target: map[string]any{"adapter": deliveryProvider, "doc_id": "remote-fixture", "uri": "fixture://remote/document", "meta": map[string]any{"opaque": "preserved"}}}
	stored := any(map[string]any{"schema": "text/markdown", "data": s.source})
	if representation == "ir" {
		s.source = map[string]any{"document_id": "local-ir", "blocks": []any{}}
		stored = map[string]any{"schema": descriptorIRSchema, "data": s.source}
	}
	f.seed(t, "descriptor-artifact", "arbitrary-slot", "json", mustJSONRewrite(stored))
	persisted := s.source
	if representation == "ir" {
		document := map[string]any{}
		for key, value := range s.source.(map[string]any) {
			document[key] = value
		}
		document["provider_binding"] = map[string]any{"provider": deliveryProvider, "document_id": "remote-fixture", "uri": "fixture://remote/document"}
		persisted = document
	}
	s.result = map[string]any{"success": true, "changed": true, "provider_synced": true, "patch_result": map[string]any{"success": true}, "persisted_document": persisted, "representation": representation, "provider": deliveryProvider, "target_document": s.target}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/document/providers":
			s.mu.Lock()
			s.catalogCalls++
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(providerCatalog(providerItem(s.provider, s.capabilities...)))
			return
		case "/api/document:inspect":
			if representation == "ir" {
				_, _ = io.WriteString(w, descriptorIR)
			} else {
				_, _ = io.WriteString(w, descriptorMarkdown)
			}
			return
		case "/api/authservice/v1/cloud/connections/internal/chat-enabled":
			if r.URL.Query().Get("owner_user_id") != "descriptor-owner" {
				t.Error("wrong credential owner")
			}
			s.mu.Lock()
			s.authProviders = append(s.authProviders, r.URL.Query().Get("provider"))
			s.mu.Unlock()
			if s.authStatus != 0 {
				w.WriteHeader(s.authStatus)
				_, _ = io.WriteString(w, `{"private":"do-not-leak-auth"}`)
				return
			}
			items := []any{}
			if !s.emptyCredentials {
				items = append(items, map[string]any{"connection_id": "fixture-connection", "provider": s.provider, "owner_user_id": "descriptor-owner", "status": "ACTIVE"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
			return
		case "/api/authservice/v1/cloud/connections/fixture-connection/token":
			if r.URL.Query().Get("user_id") != "descriptor-owner" {
				t.Error("wrong token owner")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": "fixture-connection", "provider": s.provider, "status": "ACTIVE", "access_token": "fixture-publication-token"}})
			return
		case "/api/document/actions:invoke":
			var call deliveryCall
			if json.NewDecoder(r.Body).Decode(&call) != nil {
				t.Error("invalid action request")
				w.WriteHeader(400)
				return
			}
			s.mu.Lock()
			s.calls = append(s.calls, call)
			s.mu.Unlock()
			if !validDeliveryCall(call) {
				w.WriteHeader(422)
				_, _ = io.WriteString(w, `{"detail":{"code":"DOCUMENT_ACTION_ARGUMENTS_INVALID"}}`)
				return
			}
			switch call.Reference {
			case "builtin:document.convert_document.v1":
				if call.Phase != "preview" {
					t.Error("conversion must use pure preview phase")
				}
				if s.afterConvert != nil {
					s.afterConvert()
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": deliveryConverted(s.provider)})
				return
			case "builtin:document.render_document.v1", "builtin:document.save_document.v1":
				document := s.source
				var artifact map[string]json.RawMessage
				if json.Unmarshal(call.Artifact, &artifact) == nil && len(artifact["data"]) > 0 {
					if err := json.Unmarshal(artifact["data"], &document); err != nil {
						t.Error(err)
					}
				}

				result := map[string]any{"title": "Fixture document", "representation": representation, "document": document, "numbering": map[string]any{"ordered_style": "hierarchical", "entries": map[string]any{}}, "export_document": nil}
				if call.Reference == "builtin:document.save_document.v1" {
					result["source_document"] = document
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
				return
			case "builtin:document.write_document.v1", "builtin:document.sync_document.v1":
				if call.Reference == "builtin:document.write_document.v1" {
					var converted any
					_ = json.Unmarshal(call.Arguments["converted_document"], &converted)
					if !reflect.DeepEqual(converted, deliveryConverted(s.provider)) {
						t.Error("Convert output was not passed intact into Write")
					}
				}

				if call.Phase != "execute" {
					t.Error("write/sync must use execute phase")
				}
				if s.afterWrite != nil {
					s.afterWrite()
				}
				if s.writeStatus != 0 {
					w.WriteHeader(s.writeStatus)
					_, _ = io.WriteString(w, `{"detail":{"code":"WRITE_OUTCOME_UNKNOWN","message":"do-not-leak-provider","retryable":false}}`)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": deliveryActionResult(s, call.Reference)})
				return
			}
		}
		t.Errorf("unexpected external request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}))
	s.url = server.URL
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	return f, s
}
func deliveryBody(key string) map[string]any {
	return map[string]any{"action": "publish_document", "base_revision": 3, "base_draft_version": 7, "input": map[string]any{"provider": deliveryProvider, "mode": "replace", "idempotency_key": key}}
}
func deliveryRequest(ctx context.Context, f descriptorFixture, method, path, owner string, body any) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(mustJSONRewrite(body))
	}
	req := httptest.NewRequest(method, path, reader).WithContext(ctx)
	req.Header.Set("X-User-Id", owner)
	req.Header.Set("Workflow-Contract-Version", "workflow.v1")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}
func deliveryPublish(t *testing.T, f descriptorFixture, body any) *httptest.ResponseRecorder {
	return deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-artifacts/descriptor-artifact/document-actions:execute", "descriptor-owner", body)
}
func (s *deliveryServer) writes() []deliveryCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []deliveryCall
	for _, c := range s.calls {
		if c.Reference == "builtin:document.write_document.v1" || c.Reference == "builtin:document.sync_document.v1" {
			result = append(result, c)
		}
	}
	return result
}
func (s *deliveryServer) allCalls() []deliveryCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]deliveryCall(nil), s.calls...)
}

func TestDocumentDeliveryPublishMDAndIR(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, empty := range []bool{false, true} {
			t.Run(representation+map[bool]string{false: "/credential", true: "/empty"}[empty], func(t *testing.T) {
				f, s := newDeliveryFixture(t, representation)
				s.emptyCredentials = empty
				body := deliveryBody("first-publication")
				w := deliveryPublish(t, f, body)
				data := rewriteData(t, w)
				if data["provider"] != deliveryProvider || data["provider_synced"] != true || data["artifact_saved"] != true || data["revision"] != float64(4) || data["draft_version"] != float64(1) {
					t.Fatalf("publication result %#v", data)
				}
				calls := s.allCalls()
				if len(calls) != 2 || calls[0].Reference != "builtin:document.convert_document.v1" || calls[1].Reference != "builtin:document.write_document.v1" {
					t.Fatalf("wrong publication path %#v", calls)
				}
				if len(s.authProviders) != 1 || s.authProviders[0] != deliveryProvider {
					t.Fatalf("credential fan-out %#v", s.authProviders)
				}
				for _, call := range calls {
					if empty {
						if len(call.ToolConfig) != 0 {
							t.Fatal("empty credentials invented config")
						}
					} else if len(call.ToolConfig) != 1 || call.ToolConfig[deliveryProvider] != "fixture-publication-token" {
						t.Fatalf("wrong scoped credentials %#v", call.ToolConfig)
					}
				}
				var artifact map[string]any
				if json.Unmarshal(calls[0].Artifact, &artifact) != nil || !reflect.DeepEqual(artifact["data"], s.source) {
					t.Fatalf("convert did not use Core source: %s", calls[0].Artifact)
				}
				var binding orm.DocumentPublicationBinding
				if err := f.db.Where("session_id = ? AND slot_id = ? AND item_index = ?", "descriptor-session", "arbitrary-slot", -1).First(&binding).Error; err != nil {
					t.Fatal(err)
				}
				if binding.Provider != deliveryProvider || binding.ResultRevisionID == "" {
					t.Fatalf("target binding missing %#v", binding)
				}
				var target map[string]any
				_ = json.Unmarshal(binding.TargetDocument, &target)
				if !reflect.DeepEqual(target, s.target) {
					t.Fatalf("target metadata lost: %#v", target)
				}
				var revision orm.WorkflowSlotRevision
				if err := f.db.First(&revision, "id = ?", binding.ResultRevisionID).Error; err != nil {
					t.Fatal(err)
				}
				var human orm.WorkflowHumanArtifact
				if revision.HumanArtifactID == nil || f.db.First(&human, "id = ?", *revision.HumanArtifactID).Error != nil {
					t.Fatal("saved human missing")
				}
				var saved map[string]any
				if json.Unmarshal(human.Value, &saved) != nil {
					t.Fatal("published artifact must carry metadata")
				}
				meta, _ := saved["meta"].(map[string]any)
				syncMeta, _ := meta["lazymind_provider_sync"].(map[string]any)
				if syncMeta["provider"] != deliveryProvider || !reflect.DeepEqual(syncMeta["target_document"], s.target) {
					t.Fatalf("per-artifact target missing %#v", syncMeta)
				}
				before := descriptorSnapshot(t, f)
				replay := deliveryPublish(t, f, body)
				again := rewriteData(t, replay)
				if again["artifact_id"] != data["artifact_id"] || len(s.writes()) != 1 || descriptorSnapshot(t, f) != before {
					t.Fatal("same request replayed external/local write")
				}
			})
		}
	}
}

func TestDocumentDeliveryPublishGuardsBeforeExternalWrite(t *testing.T) {
	for _, name := range []string{"unknown owner", "stale revision", "stale draft", "client document", "client target", "client tool config", "missing key", "missing create", "missing replace", "auth failure"} {
		t.Run(name, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			body := deliveryBody("guard")
			input := body["input"].(map[string]any)
			owner := "descriptor-owner"
			status, code := 400, "DOCUMENT_ACTION_INVALID"
			catalog, auth := 0, 0
			switch name {
			case "unknown owner":
				owner = "other"
				status, code = 404, "ARTIFACT_NOT_FOUND"
			case "stale revision":
				body["base_revision"] = 2
				status, code = 409, "REVISION_CONFLICT"
			case "stale draft":
				body["base_draft_version"] = 6
				status, code = 409, "DRAFT_VERSION_CONFLICT"
			case "client document":
				input["document"] = "forged"
			case "client target":
				input["target_document"] = s.target
			case "client tool config":
				input["tool_config"] = map[string]string{"notion": "forged"}
			case "missing key":
				delete(input, "idempotency_key")
			case "missing create":
				s.capabilities = []string{"load", "replace"}
				status, code, catalog = 422, "DOCUMENT_ACTION_UNSUPPORTED", 1
			case "missing replace":
				s.capabilities = []string{"load", "create"}
				status, code, catalog = 422, "DOCUMENT_ACTION_UNSUPPORTED", 1
			case "auth failure":
				s.authStatus = 503
				status, code, catalog, auth = 502, "PROVIDER_CREDENTIALS_UNAVAILABLE", 1, 1
			}
			before := descriptorSnapshot(t, f)
			w := deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-artifacts/descriptor-artifact/document-actions:execute", owner, body)
			rewriteError(t, w, status, code)
			if len(s.allCalls()) != 0 || len(s.authProviders) != auth || s.catalogCalls != catalog || descriptorSnapshot(t, f) != before {
				t.Fatalf("wrong rejection boundary calls=%#v auth=%#v catalog=%d", s.allCalls(), s.authProviders, s.catalogCalls)
			}
			if strings.Contains(w.Body.String(), "do-not-leak") || strings.Contains(w.Body.String(), "fixture-publication-token") {
				t.Fatal("private detail leaked")
			}
		})
	}
}

func TestDocumentDeliveryPublicationConflictAfterExternalSuccess(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	s.afterWrite = func() {
		if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"draft_version": 8, "value": json.RawMessage(`{"schema":"text/markdown","data":"newer draft"}`)}).Error; err != nil {
			t.Error(err)
		}
	}
	body := deliveryBody("external-success-conflict")
	w := deliveryPublish(t, f, body)
	rewriteError(t, w, 409, "PROVIDER_SYNC_LOCAL_CONFLICT")
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	if envelope.Data["provider_synced"] != true || envelope.Data["artifact_saved"] != false || envelope.Data["retryable"] != false {
		t.Fatalf("partial result %#v", envelope.Data)
	}
	before := descriptorSnapshot(t, f)
	_ = deliveryPublish(t, f, body)
	if len(s.writes()) != 1 || descriptorSnapshot(t, f) != before {
		t.Fatal("conflict replay wrote externally or overwrote new draft")
	}
	var op orm.DocumentPublicationOperation
	if err := f.db.Where("idempotency_key = ?", "external-success-conflict").First(&op).Error; err != nil {
		t.Fatal(err)
	}
	if len(op.ReceiptJSON) == 0 {
		t.Fatal("known external success receipt lost")
	}
}

func TestDocumentDeliveryPublicationUnknownIsNotReplayed(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	s.writeStatus = 502
	body := deliveryBody("ambiguous-write")
	w := deliveryPublish(t, f, body)
	if w.Code < 400 {
		t.Fatalf("ambiguous write reported success %s", w.Body.String())
	}
	if len(s.writes()) != 1 {
		t.Fatalf("write path not reached: %s", w.Body.String())
	}
	before := descriptorSnapshot(t, f)
	_ = deliveryPublish(t, f, body)
	_ = deliveryPublish(t, f, deliveryBody("different-key"))
	if len(s.writes()) != 1 || descriptorSnapshot(t, f) != before {
		t.Fatal("ambiguous effect replayed")
	}
	var op orm.DocumentPublicationOperation
	if err := f.db.Where("idempotency_key = ?", "ambiguous-write").First(&op).Error; err != nil {
		t.Fatal(err)
	}
	if op.Status != "outcome_unknown" {
		t.Fatalf("ambiguous result not durable: %#v", op)
	}
}

func TestDocumentDeliveryExistingTargetAndCrossProvider(t *testing.T) {
	for _, scenario := range []string{"markdown replace", "IR sync", "IR cross-provider", "IR no patch"} {
		t.Run(scenario, func(t *testing.T) {
			representation := "ir"
			if scenario == "markdown replace" {
				representation = "markdown"
			}
			f, s := newDeliveryFixture(t, representation)
			oldProvider := deliveryProvider
			if scenario == "IR cross-provider" {
				oldProvider = "previous-provider"
			}
			target := map[string]any{"adapter": oldProvider, "doc_id": "existing-remote", "uri": "fixture://existing", "meta": map[string]any{"opaque": "keep"}}
			baseline := any("# Previously published")
			if representation == "ir" {
				baseline = map[string]any{"document_id": "local-ir", "blocks": []any{}, "provider_binding": map[string]any{"provider": oldProvider, "document_id": "existing-remote", "uri": "fixture://existing"}}
				s.source = map[string]any{"document_id": "local-ir", "blocks": []any{map[string]any{"node_id": "paragraph", "type": "paragraph", "content": "edited"}}, "provider_binding": baseline.(map[string]any)["provider_binding"]}
			}
			schema := "text/markdown"
			if representation == "ir" {
				schema = descriptorIRSchema
			}
			meta := map[string]any{"lazymind_provider_sync": map[string]any{"provider": oldProvider, "target_document": target}}
			if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(map[string]any{"schema": schema, "data": s.source, "meta": meta}))).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Create(&orm.DocumentPublicationBinding{ID: "existing-binding", SessionID: "descriptor-session", SlotID: "arbitrary-slot", ItemIndex: -1, OwnerUserID: "descriptor-owner", Provider: oldProvider, TargetDocument: json.RawMessage(mustJSONRewrite(target)), RemoteValue: json.RawMessage(mustJSONRewrite(map[string]any{"schema": schema, "data": baseline})), SourceRevisionID: "prior-source", ResultRevisionID: "prior-saved"}).Error; err != nil {
				t.Fatal(err)
			}
			if scenario != "IR cross-provider" {
				s.target = target
			}
			s.result["target_document"] = s.target
			s.result["persisted_document"] = s.source
			if scenario == "IR cross-provider" {
				document := map[string]any{}
				for key, value := range s.source.(map[string]any) {
					document[key] = value
				}
				document["provider_binding"] = map[string]any{"provider": deliveryProvider, "document_id": "remote-fixture", "uri": "fixture://remote/document"}
				s.result["persisted_document"] = document
			}
			if scenario == "IR no patch" {
				s.capabilities = []string{"load", "replace", "create"}
			}
			before := descriptorSnapshot(t, f)
			w := deliveryPublish(t, f, deliveryBody("update"))
			if scenario == "IR no patch" {
				rewriteError(t, w, 422, "DOCUMENT_ACTION_UNSUPPORTED")
				if s.catalogCalls != 1 || len(s.allCalls()) != 0 || descriptorSnapshot(t, f) != before {
					t.Fatalf("unsupported sync wrote: %d %s", w.Code, w.Body.String())
				}
				return
			}
			data := rewriteData(t, w)
			if data["provider_synced"] != true || data["artifact_saved"] != true {
				t.Fatalf("update result %#v", data)
			}
			calls := s.allCalls()
			if scenario == "IR sync" {
				if len(calls) != 1 || calls[0].Reference != "builtin:document.sync_document.v1" {
					t.Fatalf("IR not synced %#v", calls)
				}
				var sentSource, sentRevised any
				_ = json.Unmarshal(calls[0].Arguments["source_document"], &sentSource)
				_ = json.Unmarshal(calls[0].Arguments["revised_document"], &sentRevised)
				if !reflect.DeepEqual(sentSource, baseline) || !reflect.DeepEqual(sentRevised, s.source) {
					t.Fatalf("sync baseline came from client/current instead of published snapshot: %#v %#v", sentSource, sentRevised)
				}
			} else {
				if len(calls) != 2 || calls[1].Reference != "builtin:document.write_document.v1" {
					t.Fatalf("wrong replace/create path %#v", calls)
				}
				if scenario == "markdown replace" {
					var got any
					_ = json.Unmarshal(calls[1].Arguments["target_document"], &got)
					if !reflect.DeepEqual(got, target) {
						t.Fatalf("existing target lost %#v", got)
					}
				} else if raw := calls[1].Arguments["target_document"]; len(raw) != 0 && string(raw) != "null" {
					t.Fatalf("cross-provider reused old target %s", raw)
				}
			}
			var binding orm.DocumentPublicationBinding
			if err := f.db.First(&binding, "id = ?", "existing-binding").Error; err != nil {
				t.Fatal(err)
			}
			var gotTarget any
			_ = json.Unmarshal(binding.TargetDocument, &gotTarget)
			if binding.Provider != deliveryProvider || !reflect.DeepEqual(gotTarget, s.target) {
				t.Fatalf("confirmed target not saved %#v", binding)
			}
		})
	}
}

func TestDocumentDeliveryLegacyWriteBackUsesGenericBoundary(t *testing.T) {
	for _, slot := range []string{"draft_document", "flat_draft_document"} {
		t.Run(slot, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("plugin_id", "writer-workflow").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"slot_id": slot, "slot": slot}).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("slot", slot).Error; err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"base_revision": 3, "base_draft_version": 7, "slot": slot, "provider": deliveryProvider}
			path := "/workflow-sessions/descriptor-session/writer-document:write-back"
			w := deliveryRequest(t.Context(), f, http.MethodPost, path, "descriptor-owner", body)
			data := rewriteData(t, w)
			if data["status"] != "synced" || data["provider_synced"] != true || data["artifact_saved"] != true {
				t.Fatalf("legacy response changed %#v", data)
			}
			if len(s.writes()) != 1 {
				t.Fatalf("legacy write not coordinated: %#v", s.allCalls())
			}
			before := descriptorSnapshot(t, f)
			replayed := rewriteData(t, deliveryRequest(t.Context(), f, http.MethodPost, path, "descriptor-owner", body))
			if !reflect.DeepEqual(replayed, data) {
				t.Fatalf("legacy replay lost original success: %#v", replayed)
			}
			if len(s.writes()) != 1 || descriptorSnapshot(t, f) != before {
				t.Fatal("legacy retry replayed write")
			}
			// Existing clients still read the fixed target during the compatibility
			// window. The authoritative per-document target must also be retained.
			var target orm.WorkflowSlotRevision
			if err := f.db.Where("session_id = ? AND slot_id = ? AND selected = ?", "descriptor-session", "target_document", true).First(&target).Error; err != nil {
				t.Fatalf("legacy target projection missing: %v", err)
			}
			var count int64
			if err := f.db.Model(&orm.DocumentPublicationBinding{}).Where("session_id = ? AND slot_id = ?", "descriptor-session", slot).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("per-item binding missing count=%d err=%v", count, err)
			}
		})
	}
}

func TestDocumentDeliveryPublicationMetadataSurvivesPatch(t *testing.T) {
	f, _ := newDeliveryFixture(t, "markdown")
	data := rewriteData(t, deliveryPublish(t, f, deliveryBody("before-edit")))
	id, _ := data["artifact_id"].(string)
	if id == "" {
		t.Fatal("published artifact_id missing")
	}
	body := map[string]any{"base_revision": 4, "base_draft_version": 1, "content_type": "text/markdown", "value": map[string]any{"text": "edited locally"}, "command_id": "edit-published"}
	w := deliveryRequest(t.Context(), f, http.MethodPatch, "/workflow-artifacts/"+id, "descriptor-owner", body)
	if w.Code != 200 {
		t.Fatalf("edit published document %d %s", w.Code, w.Body.String())
	}
	var rev orm.WorkflowSlotRevision
	if err := f.db.Where("session_id = ? AND slot_id = ? AND selected = ?", "descriptor-session", "arbitrary-slot", true).First(&rev).Error; err != nil || rev.HumanArtifactID == nil {
		t.Fatal("edited document missing")
	}
	var human orm.WorkflowHumanArtifact
	if err := f.db.First(&human, "id = ?", *rev.HumanArtifactID).Error; err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	_ = json.Unmarshal(human.Value, &value)
	meta, _ := value["meta"].(map[string]any)
	binding, _ := meta["lazymind_provider_sync"].(map[string]any)
	if binding["provider"] != deliveryProvider || binding["target_document"] == nil {
		t.Fatalf("ordinary edit lost provider target %#v", value)
	}
}

func TestDocumentDeliveryPublicationInvalidReceipt(t *testing.T) {
	for _, field := range []string{"success", "provider_synced", "provider", "representation", "persisted_document", "target_document"} {
		t.Run(field, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			switch field {
			case "success", "provider_synced":
				s.result[field] = false
			case "provider":
				s.result[field] = "wrong-provider"
			case "representation":
				s.result[field] = "image"
			case "persisted_document":
				s.result[field] = nil
			case "target_document":
				s.result[field] = map[string]any{"adapter": "wrong-provider", "doc_id": "wrong-target"}
			}
			before := descriptorSnapshot(t, f)
			w := deliveryPublish(t, f, deliveryBody("bad-receipt"))
			if w.Code < 400 || len(s.writes()) != 1 {
				t.Fatalf("invalid receipt path not reached/rejected: %d %s", w.Code, w.Body.String())
			}
			if descriptorSnapshot(t, f) != before {
				t.Fatal("invalid provider result persisted")
			}
			_ = deliveryPublish(t, f, deliveryBody("bad-receipt"))
			if len(s.writes()) != 1 {
				t.Fatal("invalid receipt replayed external write")
			}
		})
	}
}

func TestDocumentDeliveryPublicationBaselineChangesDuringConvert(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	s.afterConvert = func() {
		if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"draft_version": 8, "value": json.RawMessage(`{"schema":"text/markdown","data":"edited during convert"}`)}).Error; err != nil {
			t.Error(err)
		}
	}
	w := deliveryPublish(t, f, deliveryBody("changed-before-write"))
	if w.Code != 409 || len(s.allCalls()) != 1 || len(s.writes()) != 0 {
		t.Fatalf("convert baseline ignored/not reached: %d %s calls=%#v", w.Code, w.Body.String(), s.allCalls())
	}
	var human orm.WorkflowHumanArtifact
	if err := f.db.First(&human, "id = ?", "descriptor-artifact-human").Error; err != nil || human.DraftVersion != 8 {
		t.Fatal("concurrent editor state lost")
	}
}

func TestDocumentDeliveryPublicationEventFailureAndLocalRecovery(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	before := descriptorSnapshot(t, f)
	forced := func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "workflow_events" {
			tx.AddError(errors.New("fixture-publication-event-failure"))
		}
	}
	if err := f.db.Callback().Create().Before("gorm:create").Register("delivery:event-failure", forced); err != nil {
		t.Fatal(err)
	}
	body := deliveryBody("local-recovery")
	w := deliveryPublish(t, f, body)
	rewriteError(t, w, 500, "PROVIDER_SYNC_LOCAL_PERSIST_FAILED")
	if len(s.writes()) != 1 || descriptorSnapshot(t, f) != before {
		t.Fatal("local failure changed artifact or skipped external boundary")
	}
	var op orm.DocumentPublicationOperation
	if err := f.db.Where("idempotency_key = ?", "local-recovery").First(&op).Error; err != nil || len(op.ReceiptJSON) == 0 {
		t.Fatalf("durable receipt missing %#v %v", op, err)
	}
	if err := f.db.Callback().Create().Remove("delivery:event-failure"); err != nil {
		t.Fatal(err)
	}
	result := deliveryRequest(t.Context(), f, http.MethodPost, "/document-publications/"+op.ID+":retry-local", "descriptor-owner", nil)
	data := rewriteData(t, result)
	if data["artifact_saved"] != true || len(s.writes()) != 1 {
		t.Fatalf("local-only retry wrote externally: %#v", data)
	}
	query := deliveryRequest(t.Context(), f, http.MethodGet, "/document-publications/"+op.ID, "descriptor-owner", nil)
	if query.Code != 200 || strings.Contains(query.Body.String(), "fixture-publication-token") || strings.Contains(query.Body.String(), "receipt_json") {
		t.Fatalf("unsafe operation query %s", query.Body.String())
	}
	foreign := deliveryRequest(t.Context(), f, http.MethodGet, "/document-publications/"+op.ID, "other", nil)
	if foreign.Code != 404 {
		t.Fatalf("operation ownership bypass: %d %s", foreign.Code, foreign.Body.String())
	}
}

func TestDocumentDeliveryPublicationExactListItem(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	f.seed(t, "neighbor-artifact", "neighbor-slot", "json", `{"schema":"text/markdown","data":"neighbor"}`)
	for id, index := range map[string]int{"descriptor-artifact": 2, "neighbor-artifact": 7} {
		if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", id).Updates(map[string]any{"slot_id": "arbitrary-slot", "list_index": index}).Error; err != nil {
			t.Fatal(err)
		}
	}
	var neighbor orm.WorkflowSlotRevision
	if err := f.db.First(&neighbor, "id = ?", "neighbor-artifact").Error; err != nil {
		t.Fatal(err)
	}
	_ = rewriteData(t, deliveryPublish(t, f, deliveryBody("list-publish")))
	var after orm.WorkflowSlotRevision
	if err := f.db.First(&after, "id = ?", neighbor.ID).Error; err != nil || !reflect.DeepEqual(neighbor, after) {
		t.Fatal("neighbor item changed")
	}
	var bindings []orm.DocumentPublicationBinding
	if err := f.db.Find(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].ItemIndex != 2 || len(s.writes()) != 1 {
		t.Fatalf("wrong item publication %#v", bindings)
	}
}

func TestDocumentDeliveryLegacySyncRejectsClientTargetSubstitution(t *testing.T) {
	for _, forged := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid bound sync", true: "target identity changed"}[forged], func(t *testing.T) {
			f, s := newDeliveryFixture(t, "ir")
			s.provider = "obsidian"
			if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("plugin_id", "writer-workflow").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").
				Updates(map[string]any{"slot_id": "draft_document", "slot": "draft_document"}).Error; err != nil {
				t.Fatal(err)
			}
			document := map[string]any{"document_id": "local-ir", "blocks": []any{}, "provider_binding": map[string]any{"provider": "obsidian", "document_id": "remote-fixture", "uri": "fixture://remote/document"}}
			s.source = document
			s.result["persisted_document"] = document
			s.target["adapter"] = "obsidian"
			stored := map[string]any{"schema": descriptorIRSchema, "data": document}
			if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(stored))).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Create(&orm.DocumentPublicationBinding{ID: "sync-binding", SessionID: "descriptor-session", SlotID: "draft_document", ItemIndex: -1, OwnerUserID: "descriptor-owner", Provider: "obsidian", TargetDocument: json.RawMessage(mustJSONRewrite(s.target)), RemoteValue: json.RawMessage(mustJSONRewrite(stored))}).Error; err != nil {
				t.Fatal(err)
			}
			client := document
			if forged {
				client = map[string]any{"document_id": "local-ir", "blocks": []any{}, "provider_binding": map[string]any{"provider": "obsidian", "document_id": "substituted-target", "uri": "fixture://substituted"}}
			}
			before := descriptorSnapshot(t, f)
			w := deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-sessions/descriptor-session/slots/draft_document/items/idx/-1:sync-writer-document", "descriptor-owner", map[string]any{"base_revision": 3, "base_draft_version": 7, "source_document": client, "revised_document": client, "mode": "checkpoint"})
			if forged {
				rewriteError(t, w, 409, "PROVIDER_BINDING_CONFLICT")
				if len(s.allCalls()) != 0 || descriptorSnapshot(t, f) != before {
					t.Fatal("target substitution reached Algorithm or changed state")
				}
				return
			}
			data := rewriteData(t, w)
			calls := s.allCalls()
			if data["provider_synced"] != true || data["artifact_saved"] != true || len(calls) != 1 || calls[0].Reference != "builtin:document.sync_document.v1" {
				t.Fatalf("valid legacy sync did not reach common boundary: %#v %#v", data, calls)
			}
			before = descriptorSnapshot(t, f)
			replay := deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-sessions/descriptor-session/slots/draft_document/items/idx/-1:sync-writer-document", "descriptor-owner", map[string]any{"base_revision": 3, "base_draft_version": 7, "source_document": client, "revised_document": client, "mode": "checkpoint"})
			if got := rewriteData(t, replay); !reflect.DeepEqual(got, data) {
				t.Fatalf("legacy sync replay changed result: %#v", got)
			}
			if len(s.allCalls()) != len(calls) || descriptorSnapshot(t, f) != before {
				t.Fatal("legacy sync replay repeated side effects")
			}
		})
	}
}

func TestDocumentDeliveryLegacyRenderAndSaveUseBuiltins(t *testing.T) {
	for _, action := range []string{"render", "save"} {
		t.Run(action, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("plugin_id", "writer-workflow").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"slot_id": "draft_document", "slot": "draft_document", "change_source": "human"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("slot", "draft_document").Error; err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"slot": "draft_document"}
			if action == "save" {
				body["base_revision"] = 3
				body["base_draft_version"] = 7
				body["document"] = "# Edited"
				body["mode"] = "draft"
			}
			before := descriptorSnapshot(t, f)
			w := deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-sessions/descriptor-session/writer-document:"+action, "descriptor-owner", body)
			data := rewriteData(t, w)
			calls := s.allCalls()
			if len(calls) != 1 || calls[0].Reference != "builtin:document."+action+"_document.v1" {
				t.Fatalf("legacy %s bypassed shared builtin: %#v", action, calls)
			}
			if len(s.writes()) != 0 || len(s.authProviders) != 0 {
				t.Fatal("local edit/render loaded publication credentials or wrote externally")
			}
			if action == "render" {
				if descriptorSnapshot(t, f) != before {
					t.Fatal("render changed local artifact")
				}
			} else if data["revision"] != float64(3) || data["draft_version"] != float64(8) || data["document"] != "# Edited" {
				t.Fatalf("legacy draft save changed version contract %#v", data)
			}
			if action == "save" {
				var base map[string]any
				_ = json.Unmarshal(calls[0].Arguments["base_artifact"], &base)
				if base["data"] != "# Original" {
					t.Fatalf("Save base_artifact not original Core value: %#v", base)
				}
				var edited map[string]any
				_ = json.Unmarshal(calls[0].Artifact, &edited)
				if edited["data"] != "# Edited" {
					t.Fatalf("Save artifact not editor content: %#v", edited)
				}
				var h orm.WorkflowHumanArtifact
				if err := f.db.First(&h, "id = ?", "descriptor-artifact-human").Error; err != nil || !strings.Contains(string(h.Value), "# Edited") {
					t.Fatalf("Save persisted old text: %s %v", h.Value, err)
				}
			}

		})
	}
}

func TestDocumentDeliveryFixtureControl(t *testing.T) {
	_, s := newDeliveryFixture(t, "ir")
	for _, call := range []deliveryCall{
		{Reference: "builtin:document.convert_document.v1", Phase: "preview", Artifact: json.RawMessage(`{"data":{"document_id":"local-ir","blocks":[]}}`), Arguments: map[string]json.RawMessage{"provider": json.RawMessage(`"fixture-provider-outside-old-whitelist"`)}},
		{Reference: "builtin:document.write_document.v1", Phase: "execute", Arguments: map[string]json.RawMessage{"mode": json.RawMessage(`"replace"`), "converted_document": json.RawMessage(mustJSONRewrite(deliveryConverted(deliveryProvider)))}},
	} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.url+"/api/document/actions:invoke", strings.NewReader(mustJSONRewrite(call)))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		err = json.NewDecoder(response.Body).Decode(&value)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || value["result"] == nil {
			t.Fatalf("fixture control %v %#v", err, value)
		}
	}
	if len(s.writes()) != 1 {
		t.Fatal("write observer did not count fixture request")
	}
}

func TestDocumentDeliveryOpenAPIContract(t *testing.T) {
	f := newDescriptorFixture(t)
	raw, err := buildOpenAPISpecFromRouter(f.router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if json.Unmarshal(raw, &spec) != nil {
		t.Fatal("invalid OpenAPI")
	}
	op := openAPIOperationForTest(t, spec, "post", "/api/core/workflow-artifacts/{artifact_id}/document-actions:execute")
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	var expand func(any, map[string]bool) any
	expand = func(value any, seen map[string]bool) any {
		switch v := value.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				if seen[ref] {
					return nil
				}
				next := map[string]bool{}
				for k, b := range seen {
					next[k] = b
				}
				next[ref] = true
				return expand(schemas[strings.TrimPrefix(ref, "#/components/schemas/")], next)
			}
			result := map[string]any{}
			for k, x := range v {
				result[k] = expand(x, seen)
			}
			return result
		case []any:
			result := []any{}
			for _, x := range v {
				result = append(result, expand(x, seen))
			}
			return result
		}
		return value
	}
	expandedBody := expand(op["requestBody"], map[string]bool{})
	foundPublish := false
	var inspectRequired func(any)
	inspectRequired = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			props, _ := v["properties"].(map[string]any)
			action, _ := props["action"].(map[string]any)
			if strings.Contains(mustJSONRewrite(action["enum"]), `"publish_document"`) {
				foundPublish = true
				required := schemaStringList(v["required"])
				for _, field := range []string{"action", "base_revision", "input"} {
					if !containsPortable(required, field) {
						t.Errorf("publish request must require %s", field)
					}
				}
				input, _ := props["input"].(map[string]any)
				req := schemaStringList(input["required"])
				for _, field := range []string{"provider", "idempotency_key"} {
					if !containsPortable(req, field) {
						t.Errorf("publish input must require %s", field)
					}
				}
			}
			for _, child := range v {
				inspectRequired(child)
			}
		case []any:
			for _, child := range v {
				inspectRequired(child)
			}
		}
	}
	inspectRequired(expandedBody)
	if !foundPublish {
		t.Error("publish request schema branch missing")
	}
	body := mustJSONRewrite(expandedBody)
	for _, field := range []string{"publish_document", "base_revision", "base_draft_version", "provider", "idempotency_key"} {
		if !strings.Contains(body, `"`+field+`"`) {
			t.Errorf("execute OpenAPI missing %s", field)
		}
	}
	for path, method := range map[string]string{"/api/core/document-publications/{operation_id}": "get", "/api/core/document-publications/{operation_id}:retry-local": "post", "/api/core/document-publications/{operation_id}:cancel": "post"} {
		item, _ := spec["paths"].(map[string]any)[path].(map[string]any)
		operation, _ := item[method].(map[string]any)
		if operation == nil {
			t.Errorf("operation API missing %s %s", method, path)
			continue
		}
		params, _ := operation["parameters"].([]any)
		requiredID := false
		for _, param := range params {
			p, _ := param.(map[string]any)
			if p["name"] == "operation_id" && p["in"] == "path" && p["required"] == true {
				requiredID = true
			}
		}
		if !requiredID {
			t.Errorf("%s operation_id must be required path input", path)
		}
		if operation["requestBody"] != nil {
			t.Errorf("%s must not accept client source/receipt body", path)
		}
		responses, _ := operation["responses"].(map[string]any)
		if responses["200"] == nil || responses["404"] == nil {
			t.Errorf("%s missing success/owner failure", path)
		}
	}
	responses, _ := op["responses"].(map[string]any)
	for _, status := range []string{"200", "409", "500"} {
		response := mustJSONRewrite(expand(responses[status], map[string]bool{}))
		for _, field := range []string{"provider", "provider_synced", "artifact_saved", "operation_id"} {
			if !strings.Contains(response, `"`+field+`"`) {
				t.Errorf("publication %s response lacks %s", status, field)
			}
		}
		if status != "200" && !strings.Contains(response, `"retryable"`) {
			t.Errorf("publication %s lacks retryable", status)
		}
	}

}

func TestDocumentDeliveryDescriptorOffersPublication(t *testing.T) {
	f, _ := newDeliveryFixture(t, "markdown")
	records := descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/descriptor-artifact", "descriptor-owner"))
	document, _ := records[0]["document"].(map[string]any)
	caps, _ := document["capabilities"].([]any)
	found := false
	for _, cap := range caps {
		if cap == "publish_document" {
			found = true
		}
	}
	if !found {
		t.Fatalf("arbitrary document cannot discover publication: %#v", document)
	}
}

func TestDocumentDeliveryWriterPublicationSlots(t *testing.T) {
	for _, slot := range []string{"source_document", "outline_document", "target_document", "draft_document", "flat_draft_document"} {
		t.Run(slot, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("plugin_id", "writer-workflow").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").
				Updates(map[string]any{"slot_id": slot, "slot": slot}).Error; err != nil {
				t.Fatal(err)
			}
			records := descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/descriptor-artifact", "descriptor-owner"))
			document, _ := records[0]["document"].(map[string]any)
			caps, _ := document["capabilities"].([]any)
			found := false
			for _, cap := range caps {
				found = found || cap == "publish_document"
			}
			allowed := slot == "draft_document" || slot == "flat_draft_document"
			if found != allowed {
				t.Fatalf("publication capability for %s: %#v", slot, document)
			}
			w := deliveryPublish(t, f, deliveryBody("writer-slot"))
			if allowed {
				_ = rewriteData(t, w)
				if len(s.writes()) != 1 {
					t.Fatalf("draft publication writes=%d", len(s.writes()))
				}
				return
			}
			rewriteError(t, w, 422, "DOCUMENT_ACTION_UNSUPPORTED")
			var count int64
			if err := f.db.Model(&orm.DocumentPublicationOperation{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 || len(s.allCalls()) != 0 || s.catalogCalls != 0 || len(s.authProviders) != 0 {
				t.Fatalf("rejected slot created operations or called Runtime: operations=%d calls=%#v", count, s.allCalls())
			}
		})
	}
}

func TestDocumentDeliveryLegacyAndGenericSharePublicationReservation(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("plugin_id", "writer-workflow").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"slot_id": "draft_document", "slot": "draft_document"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("slot", "draft_document").Error; err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var enterOnce sync.Once
	s.afterConvert = func() { enterOnce.Do(func() { close(entered) }); <-release }
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- deliveryPublish(t, f, deliveryBody("generic-in-flight")) }()
	select {
	case <-entered:
	case result := <-first:
		t.Fatalf("generic publication did not reach conversion: %d %s", result.Code, result.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("conversion not reached")
	}
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		second <- deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-sessions/descriptor-session/writer-document:write-back", "descriptor-owner", map[string]any{"base_revision": 3, "base_draft_version": 7, "slot": "draft_document", "provider": deliveryProvider})
	}()
	select {
	case result := <-second:
		if result.Code != 409 {
			t.Fatalf("legacy call did not honor common reservation: %d %s", result.Code, result.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("legacy request blocked on external conversion instead of a durable reservation")
	}
	once.Do(func() { close(release) })
	select {
	case result := <-first:
		_ = rewriteData(t, result)
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not finish")
	}
	if len(s.writes()) != 1 {
		t.Fatal("legacy and generic both wrote remotely")
	}
}

func deliveryConverted(provider string) map[string]any {
	return map[string]any{"provider": provider, "format": "fixture-native", "content": "converted fixture", "source_document": map[string]any{"document_id": "local-ir", "blocks": []any{}}, "media_references": map[string]any{}}
}
func deliveryActionResult(s *deliveryServer, reference string) map[string]any {
	if reference != "builtin:document.sync_document.v1" {
		return s.result
	}
	// resources.sync_writer_documents returns no provider/representation/target;
	// Core must retain its verified binding instead of requiring Write's fields.
	return map[string]any{"success": true, "changed": true, "provider_synced": true, "patch_result": map[string]any{"success": true}, "patch_set": map[string]any{}, "persisted_document": s.result["persisted_document"]}
}
func validDeliveryCall(c deliveryCall) bool {
	allowed := map[string]bool{}
	required := []string{}
	switch c.Reference {
	case "builtin:document.render_document.v1":
	case "builtin:document.save_document.v1":
		allowed = map[string]bool{"base_artifact": true, "numbering_update": true}
		required = []string{"base_artifact"}
	case "builtin:document.convert_document.v1":
		for _, key := range []string{"provider", "output_format", "document", "target_document", "media_assets", "template"} {
			allowed[key] = true
		}
		required = []string{"provider"}
	case "builtin:document.write_document.v1":
		for _, key := range []string{"converted_document", "target_document", "media_assets", "title", "parent_uri", "mode"} {
			allowed[key] = true
		}
		required = []string{"converted_document"}
	case "builtin:document.sync_document.v1":
		allowed = map[string]bool{"source_document": true, "revised_document": true, "media_assets": true}
		required = []string{"source_document", "revised_document"}
	default:
		return false
	}
	for key, raw := range c.Arguments {
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return false
		}
		switch key {
		case "provider", "title", "parent_uri", "template":
			if _, ok := value.(string); !ok {
				return false
			}
		case "mode":
			if value != "replace" && value != "append" {
				return false
			}
		case "output_format":
			if value != "native" && value != "markdown" && value != "latex" && value != "text" {
				return false
			}
		case "media_assets", "target_document", "numbering_update":
			if value != nil {
				if _, ok := value.(map[string]any); !ok {
					return false
				}
			}
		case "document":
			if value != nil {
				switch value.(type) {
				case string, map[string]any:
				default:
					return false
				}
			}
		}
		if !allowed[key] {
			return false
		}
	}
	for _, key := range required {
		raw, ok := c.Arguments[key]
		if !ok {
			return false
		}
		if key != "base_artifact" && key != "provider" {
			var value map[string]any
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return false
			}
		}
	}
	return true
}

func TestDocumentDeliveryLegacySharedTargetAcrossSlots(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("plugin_id", "writer-workflow").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"slot_id": "draft_document", "slot": "draft_document"}).Error; err != nil {
		t.Fatal(err)
	}
	f.seed(t, "flat-artifact", "flat_draft_document", "json", `{"schema":"text/markdown","data":"# Other view of shared document"}`)
	target := map[string]any{"adapter": deliveryProvider, "doc_id": "shared-remote", "uri": "fixture://shared-target", "meta": map[string]any{"opaque": "legacy-metadata-must-survive"}}
	f.seed(t, "legacy-target", "target_document", "json", mustJSONRewrite(map[string]any{"schema": "lazyllm.tools.writer.data_models.task.TargetDocument", "data": target}))
	s.target = target
	s.result["target_document"] = target
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	s.afterConvert = func() { enteredOnce.Do(func() { close(entered) }); <-release }
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- deliveryPublish(t, f, deliveryBody("shared-new-api")) }()
	select {
	case <-entered:
	case w := <-first:
		t.Fatalf("shared target not resolved: %d %s", w.Code, w.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("first publication did not start")
	}
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		second <- deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-sessions/descriptor-session/writer-document:write-back", "descriptor-owner", map[string]any{"slot": "flat_draft_document", "base_revision": 3, "base_draft_version": 7, "provider": deliveryProvider})
	}()
	select {
	case w := <-second:
		rewriteError(t, w, 409, "PUBLICATION_IN_PROGRESS")
	case <-time.After(5 * time.Second):
		t.Fatal("shared target did not reject competing writer")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case w := <-first:
		_ = rewriteData(t, w)
	case <-time.After(5 * time.Second):
		t.Fatal("first publication did not finish")
	}
	writes := s.writes()
	if len(writes) != 1 {
		t.Fatalf("shared target wrote %d times", len(writes))
	}
	for _, call := range s.allCalls() {
		var supplied any
		_ = json.Unmarshal(call.Arguments["target_document"], &supplied)
		if !reflect.DeepEqual(supplied, target) {
			t.Fatalf("shared target not reused in %s: %#v", call.Reference, supplied)
		}
	}
	var revision orm.WorkflowSlotRevision
	if err := f.db.Where("session_id = ? AND slot_id = ? AND selected = ?", "descriptor-session", "target_document", true).First(&revision).Error; err != nil || revision.HumanArtifactID == nil {
		t.Fatal("legacy target disappeared")
	}
	var human orm.WorkflowHumanArtifact
	if err := f.db.First(&human, "id = ?", *revision.HumanArtifactID).Error; err != nil || !strings.Contains(string(human.Value), "legacy-metadata-must-survive") {
		t.Fatal("legacy target metadata lost")
	}
}

func deliveryPreparedOperation(t *testing.T, f descriptorFixture) *workflow.DocumentPublicationOperation {
	t.Helper()
	draft := int64(7)
	op, err := workflow.PrepareDocumentPublication(t.Context(), f.db.DB, workflow.DocumentPublicationInput{OwnerUserID: "descriptor-owner", SessionID: "descriptor-session", SlotID: "arbitrary-slot", BaseRevision: 3, BaseDraftVersion: &draft, Provider: deliveryProvider, IdempotencyKey: "recovery-fixture"})
	if err != nil || op == nil {
		t.Fatalf("prepare recovery fixture: %#v %v", op, err)
	}
	return op
}
func TestDocumentDeliveryPublicCancelStatesAndOwners(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "preparing", true: "write_started"}[started], func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			op := deliveryPreparedOperation(t, f)
			if started {
				if err := workflow.ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "descriptor-owner", op.ID); err != nil {
					t.Fatal(err)
				}
			}
			path := "/document-publications/" + op.ID + ":cancel"
			before := descriptorSnapshot(t, f)
			foreign := deliveryRequest(t.Context(), f, http.MethodPost, path, "other", nil)
			rewriteError(t, foreign, 404, "PUBLICATION_NOT_FOUND")
			owner := deliveryRequest(t.Context(), f, http.MethodPost, path, "descriptor-owner", nil)
			if started {
				rewriteError(t, owner, 409, "PUBLICATION_STATE_CONFLICT")
			} else {
				data := rewriteData(t, owner)
				if data["status"] != "canceled" {
					t.Fatalf("cancel status %#v", data)
				}
			}
			var saved orm.DocumentPublicationOperation
			if err := f.db.First(&saved, "id = ?", op.ID).Error; err != nil {
				t.Fatal(err)
			}
			want := "canceled"
			if started {
				want = "write_started"
			}
			if saved.Status != want || len(s.writes()) != 0 || descriptorSnapshot(t, f) != before {
				t.Fatalf("cancel altered source/external state %#v", saved)
			}
		})
	}
}
func TestDocumentDeliveryPublicRecoveryRejectsForeignAndNewBaseline(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	op := deliveryPreparedOperation(t, f)
	if err := workflow.ClaimDocumentPublicationWrite(t.Context(), f.db.DB, "descriptor-owner", op.ID); err != nil {
		t.Fatal(err)
	}
	receipt := workflow.DocumentPublicationReceipt{Provider: deliveryProvider, TargetDocument: json.RawMessage(mustJSONRewrite(s.target)), ContentType: "json", Value: json.RawMessage(`{"schema":"text/markdown","data":"PRIVATE_REMOTE_RECEIPT_CONTENT"}`)}
	if err := workflow.ConfirmDocumentPublication(t.Context(), f.db.DB, "descriptor-owner", op.ID, receipt); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"draft_version": 8, "value": json.RawMessage(`{"schema":"text/markdown","data":"PRIVATE_NEW_DRAFT_CONTENT"}`)}).Error; err != nil {
		t.Fatal(err)
	}
	before := descriptorSnapshot(t, f)
	path := "/document-publications/" + op.ID + ":retry-local"
	foreign := deliveryRequest(t.Context(), f, http.MethodPost, path, "other", nil)
	rewriteError(t, foreign, 404, "PUBLICATION_NOT_FOUND")
	owner := deliveryRequest(t.Context(), f, http.MethodPost, path, "descriptor-owner", nil)
	rewriteError(t, owner, 409, "PROVIDER_SYNC_LOCAL_CONFLICT")
	if descriptorSnapshot(t, f) != before || len(s.allCalls()) != 0 {
		t.Fatal("local retry overwrote new draft or called Algorithm")
	}
	query := deliveryRequest(t.Context(), f, http.MethodGet, "/document-publications/"+op.ID, "descriptor-owner", nil)
	_ = rewriteData(t, query)
	for _, forbidden := range []string{"# Original", "PRIVATE_REMOTE_RECEIPT_CONTENT", "PRIVATE_NEW_DRAFT_CONTENT", "source_value", "receipt_json", "persisted_document", "fixture-publication-token"} {
		if strings.Contains(query.Body.String(), forbidden) {
			t.Errorf("query leaked %s", forbidden)
		}
	}
	var queryJSON any
	if err := json.Unmarshal(query.Body.Bytes(), &queryJSON); err != nil {
		t.Fatal(err)
	}
	var checkFields func(any)
	checkFields = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				normalized := strings.ReplaceAll(strings.ToLower(key), "_", "")
				switch normalized {
				case "sourcevalue", "receiptjson", "receipt", "persisteddocument", "sourcedocument":
					t.Errorf("private query field %s", key)
				}
				checkFields(child)
			}
		case []any:
			for _, child := range v {
				checkFields(child)
			}
		}
	}
	checkFields(queryJSON)

}

func TestDocumentDeliveryActionFixtureRejectsMissingInputs(t *testing.T) {
	_, s := newDeliveryFixture(t, "markdown")
	for _, reference := range []string{"builtin:document.write_document.v1", "builtin:document.save_document.v1", "builtin:document.sync_document.v1"} {
		response, err := http.Post(s.url+"/api/document/actions:invoke", "application/json", strings.NewReader(mustJSONRewrite(deliveryCall{Reference: reference, Phase: "execute", Artifact: json.RawMessage(`{"data":"edited"}`), Arguments: map[string]json.RawMessage{}})))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 422 {
			t.Fatalf("fixture accepted missing inputs for %s", reference)
		}
	}
}
