package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow"
)

const crossListReference = "builtin:document.list_cross_reference_targets.v1"
const crossUpdateReference = "builtin:document.update_cross_reference.v1"
const crossToken = "11111111111111111111111111111111"

type crossFixture struct {
	portableFixture
	operation string
	candidate any
	selection map[string]any
}

func newCrossFixture(t *testing.T, representation, operation string, withModel ...bool) crossFixture {
	t.Helper()
	f := crossFixture{portableFixture: newPortableFixture(t, representation, len(withModel) > 0 && withModel[0]), operation: operation}
	f.source = "# Title\n\n<a id=\"block-first\"></a>\n## First\n\n<a id=\"block-second\"></a>\n## Second\n\nSee target and [missing](#block-missing).\n"
	f.source = f.source.(string) + "\n<a id=\"block-diagram\"></a>\n![Diagram](diagram.png)\n"
	f.selection = map[string]any{"type": "markdown", "selected_text": "target"}
	if operation == "remove" || operation == "retarget" {
		f.source = strings.Replace(f.source.(string), "See target", "See [target](#block-first)", 1)
	}
	f.candidate = f.source
	switch operation {
	case "add":
		f.candidate = strings.Replace(f.source.(string), "See target", "See [target](#block-first)", 1)
	case "remove":
		f.candidate = strings.Replace(f.source.(string), "[target](#block-first)", "target", 1)
	case "retarget":
		f.candidate = strings.Replace(f.source.(string), "[target](#block-first)", "[target](#block-second)", 1)
	}
	ct := "text/markdown"
	stored := f.source
	if representation == "ir" {
		f.selection = map[string]any{"type": "ir", "node_id": "p", "selected_text": "target"}
		spans := []any{map[string]any{"text": "See "}, map[string]any{"text": "target"}}
		if operation == "remove" || operation == "retarget" {
			spans[1].(map[string]any)["style"] = map[string]any{"link": map[string]any{"type": "internal_ref", "target_node_id": "first"}}
		}
		f.source = map[string]any{"document_id": "cross-doc", "blocks": []any{map[string]any{"node_id": "first", "type": "heading", "content": "First"}, map[string]any{"node_id": "second", "type": "heading", "content": "Second"}, map[string]any{"node_id": "p", "type": "paragraph", "content": "See target", "spans": spans}, map[string]any{"node_id": "diagram", "type": "image", "content": "Diagram"}}}
		var candidate map[string]any
		if err := json.Unmarshal([]byte(mustJSONRewrite(f.source)), &candidate); err != nil {
			t.Fatal(err)
		}
		span := candidate["blocks"].([]any)[2].(map[string]any)["spans"].([]any)[1].(map[string]any)
		if operation == "remove" {
			delete(span, "style")
		} else if operation != "list_targets" {
			target := "first"
			if operation == "retarget" {
				target = "second"
			}
			span["style"] = map[string]any{"link": map[string]any{"type": "internal_ref", "target_node_id": target}}
		}
		f.candidate = candidate
		ct = "json"
		stored = map[string]any{"schema": descriptorIRSchema, "data": f.source}
	}
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"content_type": ct, "value": json.RawMessage(mustJSONRewrite(stored))}).Error; err != nil {
		t.Fatal(err)
	}
	return f
}
func (f crossFixture) input() map[string]any {
	input := map[string]any{"operation": f.operation}
	if f.operation != "list_targets" {
		input["selection"] = f.selection
		if f.operation != "remove" {
			target := "first"
			if f.operation == "retarget" {
				target = "second"
			}
			input["target_id"] = target
		}
	}
	return input
}
func (f crossFixture) body(phase string) map[string]any {
	input := f.input()
	if phase == "execute" {
		input = map[string]any{"commit_token": crossToken}
	}
	body := map[string]any{"action": "cross_reference", "base_revision": 3, "input": input}
	if !f.native {
		body["base_draft_version"] = 7
	}
	return body
}
func (f crossFixture) result(phase string) map[string]any {
	if phase == "preview" && f.operation == "list_targets" {
		invalid := []any{map[string]any{"target_id": "missing"}}
		if f.representation == "ir" {
			invalid = []any{}
		}
		return map[string]any{"representation": f.representation, "targets": []any{map[string]any{"target_id": "first", "type": "heading", "title": "First"}, map[string]any{"target_id": "second", "type": "heading", "title": "Second"}, map[string]any{"target_id": "diagram", "type": "image", "title": "Diagram"}}, "invalid_references": invalid}
	}
	ct := "text"
	patchType := "string_replace_set"
	if f.representation == "ir" {
		ct = "json"
		patchType = "writer_ir_patch"
	}
	result := map[string]any{"representation": f.representation, "artifact": map[string]any{"content_type": ct, "value": f.candidate}}
	if phase == "preview" {
		result["operation"] = f.operation
		result["patch"] = map[string]any{"type": patchType, "payload": map[string]any{"fixture": "confirmed cross-reference patch"}}
		result["commit"] = map[string]any{"token": crossToken}
	}
	return result
}

type crossServer struct {
	mu            sync.Mutex
	calls         []rewriteRequest
	inspections   int
	manifests     map[string]any
	override      map[string]any
	status        int
	code          string
	beforeExecute func()
	gate          *portableGate
	gatePhase     string
	url           string
}

func newCrossServer(t *testing.T, f crossFixture) *crossServer {
	t.Helper()
	spy := &crossServer{manifests: map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var fields map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
			return
		}
		if r.Method != "POST" {
			t.Error("unexpected method")
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/api/document:inspect" {
			spy.mu.Lock()
			spy.inspections++
			spy.mu.Unlock()
			response := descriptorMarkdown
			if f.representation == "ir" {
				response = descriptorIR
			}
			_, _ = w.Write([]byte(response))
			return
		}
		if r.URL.Path != "/api/document/actions:invoke" {
			t.Errorf("unexpected route=%s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		for key := range fields {
			switch key {
			case "reference", "phase", "artifact", "arguments", "artifact_store":
			default:
				t.Errorf("forbidden cross-reference context=%s", key)
			}
		}
		var req rewriteRequest
		if err := json.Unmarshal([]byte(mustJSONRewrite(fields)), &req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var artifact map[string]any
		if err := json.Unmarshal(req.Artifact, &artifact); err != nil || !reflect.DeepEqual(artifact, map[string]any{"data": f.source}) {
			t.Errorf("cross-reference source=%s", req.Artifact)
		}
		reference := crossUpdateReference
		args := f.input()
		if req.Phase == "preview" && f.operation == "list_targets" {
			reference = crossListReference
			args = map[string]any{}
			if req.ArtifactStore != "" {
				t.Error("target listing allocated manifest")
			}
		}
		if req.Phase == "execute" {
			args = map[string]any{"commit_token": req.Arguments["commit_token"]}
		} else if req.Phase != "preview" {
			t.Error("unknown phase")
		}
		if req.Reference != reference || !reflect.DeepEqual(req.Arguments, args) {
			t.Errorf("cross-reference request=%#v want=%s/%#v", req, reference, args)
		}
		if reference == crossUpdateReference {
			rel, err := filepath.Rel(f.workspace, req.ArtifactStore)
			if err != nil || req.ArtifactStore == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("manifest not server scoped: %s", req.ArtifactStore)
			}
		}
		spy.mu.Lock()
		spy.calls = append(spy.calls, req)
		override, status, code, hook, gate, gatePhase := spy.override, spy.status, spy.code, spy.beforeExecute, spy.gate, spy.gatePhase
		spy.mu.Unlock()
		if gate != nil && req.Phase == gatePhase {
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
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{"code": code, "message": "private-upstream-detail"}})
			return
		}
		token := crossToken
		if req.Phase == "execute" {
			token, _ = req.Arguments["commit_token"].(string)
		}
		key := req.ArtifactStore + "/" + token
		if reference == crossUpdateReference {
			spy.mu.Lock()
			source, exists := spy.manifests[key]
			if req.Phase == "preview" {
				spy.manifests[key] = f.source
			}
			spy.mu.Unlock()
			if req.Phase == "execute" && (!exists || !reflect.DeepEqual(source, artifact["data"])) {
				w.WriteHeader(409)
				_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{"code": "SELECTION_STALE"}})
				return
			}
		}
		if req.Phase == "execute" && hook != nil {
			hook()
		}
		result := f.result(req.Phase)
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
func (s *crossServer) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.calls) }
func crossPreview(t *testing.T, f crossFixture, spy *crossServer) map[string]any {
	t.Helper()
	before := portableState(t, f.portableFixture)
	data := rewriteData(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("preview"), ""))
	if !reflect.DeepEqual(data, f.result("preview")) {
		t.Errorf("cross-reference preview=%#v", data)
	}
	if portableState(t, f.portableFixture) != before {
		t.Error("preview changed Core data or wrote local files")
	}
	return data
}

func TestDocumentCrossReferenceReadAndMutation(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, operation := range []string{"list_targets", "add", "remove", "retarget"} {
			for _, carrier := range []string{"human", "native", "file"} {
				t.Run(representation+"/"+operation+"/"+carrier, func(t *testing.T) {
					f := newCrossFixture(t, representation, operation)
					f.carrier(t, carrier)
					spy := newCrossServer(t, f)
					seedRewriteTerminalGraph(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
					costs := watchPortableCosts(t, f.portableFixture)
					crossPreview(t, f, spy)
					if operation == "list_targets" {
						requirePortableCosts(t, costs)
						if spy.count() != 1 {
							t.Error("list call count")
						}
						return
					}
					var old orm.WorkflowSlotRevision
					if err := f.db.First(&old, "id = ?", "descriptor-artifact").Error; err != nil {
						t.Fatal(err)
					}
					sourceBefore := crossSourceSnapshot(t, f)
					result := rewriteData(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute"), ""))
					var current orm.WorkflowSlotRevision
					if err := f.db.First(&current, "id = ?", result["artifact_id"]).Error; err != nil {
						t.Fatal(err)
					}
					if current.Revision != 4 || !current.Selected || current.Validity != "effective" || current.HumanArtifactID == nil {
						t.Fatalf("cross-reference selected=%#v", current)
					}
					var human orm.WorkflowHumanArtifact
					if err := f.db.First(&human, "id = ?", *current.HumanArtifactID).Error; err != nil {
						t.Fatal(err)
					}
					ct := "text/markdown"
					want := map[string]any{"text": f.candidate}
					if representation == "ir" {
						ct = "json"
						want = map[string]any{"schema": descriptorIRSchema, "data": f.candidate}
					}
					var value any
					_ = json.Unmarshal(human.Value, &value)
					if human.ContentType != ct || human.DraftVersion != 1 || !reflect.DeepEqual(value, want) {
						t.Errorf("saved cross-reference=%s/%#v", human.ContentType, value)
					}
					if !reflect.DeepEqual(result, map[string]any{"artifact_id": current.ID, "revision": float64(4), "draft_version": float64(1)}) {
						t.Errorf("execute result=%#v", result)
					}
					var afterOld orm.WorkflowSlotRevision
					if err := f.db.First(&afterOld, "id = ?", old.ID).Error; err != nil {
						t.Fatal(err)
					}
					if afterOld.Selected {
						t.Error("old revision still selected")
					}
					if crossSourceSnapshot(t, f) != sourceBefore {
						t.Error("old source mutated")
					}
					afterOld.Selected = old.Selected
					if !reflect.DeepEqual(afterOld, old) {
						t.Error("old lineage changed")
					}
					var events []orm.WorkflowEvent
					if err := f.db.Find(&events).Error; err != nil {
						t.Fatal(err)
					}
					if len(events) != 1 || events[0].EntityID != current.ID || events[0].StateVersion != 10 || events[0].EventType != "artifact.upsert" {
						t.Fatalf("cross-reference events=%#v", events)
					}
					var payload map[string]any
					_ = json.Unmarshal(events[0].PayloadJSON, &payload)
					if payload["artifact_id"] != current.ID || payload["revision"] != float64(4) || payload["draft_version"] != float64(1) || payload["state_version"] != float64(10) || payload["change_source"] != "human" {
						t.Errorf("event payload=%#v", payload)
					}
					requireRewriteGraphState(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "stale", false)
					after := portableState(t, f.portableFixture)
					rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute"), ""), 409, "REVISION_CONFLICT")
					if portableState(t, f.portableFixture) != after || spy.count() != 2 {
						t.Error("duplicate cross-reference executed")
					}
					requirePortableCosts(t, costs)
				})
			}
		}
	}
}

func TestDocumentCrossReferenceExactListItem(t *testing.T) {
	f := newCrossFixture(t, "markdown", "retarget")
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("list_index", 0).Error; err != nil {
		t.Fatal(err)
	}
	f.seed(t, "cross-other", "other", "text/markdown", `{"text":"other item"}`)
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "cross-other").Updates(map[string]any{"slot_id": "unknown-slot", "slot": "unknown-slot", "list_index": 1}).Error; err != nil {
		t.Fatal(err)
	}
	var before orm.WorkflowSlotRevision
	if err := f.db.First(&before, "id = ?", "cross-other").Error; err != nil {
		t.Fatal(err)
	}
	spy := newCrossServer(t, f)
	crossPreview(t, f, spy)
	result := rewriteData(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute"), ""))
	var selected []orm.WorkflowSlotRevision
	if err := f.db.Where("slot_id = ? AND selected = ?", "unknown-slot", true).Order("list_index").Find(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].ListIndex == nil || *selected[0].ListIndex != 0 || selected[0].ID != result["artifact_id"] || !reflect.DeepEqual(selected[1], before) {
		t.Errorf("wrong list item changed=%#v", selected)
	}
}

func TestDocumentCrossReferencePreconditions(t *testing.T) {
	for _, entry := range []string{"list", "preview", "execute"} {
		for _, kind := range []string{"identity", "owner", "scope", "revision missing", "revision", "draft missing", "draft", "historical", "stale", "dismissed", "session"} {
			t.Run(entry+"/"+kind, func(t *testing.T) {
				operation, phase := "add", entry
				if entry == "list" {
					operation = "list_targets"
					phase = "preview"
				}
				f := newCrossFixture(t, "markdown", operation)
				spy := newCrossServer(t, f)
				costs := watchPortableCosts(t, f.portableFixture)
				body := f.body(phase)
				owner, scope := "descriptor-owner", ""
				status, code := 409, "REVISION_CONFLICT"
				var err error
				switch kind {
				case "identity":
					owner = ""
					status = 400
					code = "IDENTITY_REQUIRED"
				case "owner":
					owner = "other"
					status = 403
					code = "PERMISSION_DENIED"
				case "scope":
					scope = "other-conversation"
					status = 403
					code = "PERMISSION_DENIED"
				case "revision missing":
					delete(body, "base_revision")
					status = 400
					code = "REVISION_REQUIRED"
				case "revision":
					body["base_revision"] = 2
				case "draft missing":
					delete(body, "base_draft_version")
					status = 400
					code = "DRAFT_VERSION_REQUIRED"
				case "draft":
					body["base_draft_version"] = 8
					code = "DRAFT_VERSION_CONFLICT"
				case "historical":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false).Error
				case "stale":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", "stale").Error
				case "dismissed":
					err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("dismissed", true).Error
					status = 404
					code = "ARTIFACT_NOT_FOUND"
				case "session":
					err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("status", "unknown-state").Error
					code = "SESSION_NOT_EDITABLE"
				}
				if err != nil {
					t.Fatal(err)
				}
				before := portableState(t, f.portableFixture)
				costs.contentQueries.Store(0)
				rewriteError(t, f.post(t.Context(), phase, owner, body, scope), status, code)
				if spy.count() != 0 || spy.inspections != 0 {
					t.Error("rejected request reached Algorithm")
				}
				if (kind == "identity" || kind == "owner" || kind == "scope") && costs.contentQueries.Load() != 0 {
					t.Error("unauthorized content read")
				}
				requirePortableCosts(t, costs)
				if portableState(t, f.portableFixture) != before {
					t.Error("precondition failure changed state")
				}
			})
		}
	}
}

func TestDocumentCrossReferenceRejectsInvalidInput(t *testing.T) {
	for _, kind := range []string{"missing operation", "unknown operation", "missing selection", "wrong selection type", "IR missing text", "empty text", "missing target", "remove target", "list selection", "body document", "reference", "artifact_store", "llm_config", "tool_config", "provider", "candidate", "execute operation", "execute path token"} {
		t.Run(kind, func(t *testing.T) {
			f := newCrossFixture(t, "markdown", "add")
			spy := newCrossServer(t, f)
			phase := "preview"
			body := f.body(phase)
			input := body["input"].(map[string]any)
			switch kind {
			case "missing operation":
				delete(input, "operation")
			case "unknown operation":
				input["operation"] = "refresh_all"
			case "missing selection":
				delete(input, "selection")
			case "wrong selection type":
				input["selection"] = map[string]any{"type": "ir", "node_id": "p", "selected_text": "target"}
			case "IR missing text":
				input["selection"] = map[string]any{"type": "ir", "node_id": "p"}
			case "empty text":
				input["selection"].(map[string]any)["selected_text"] = ""
			case "missing target":
				delete(input, "target_id")
			case "remove target":
				input["operation"] = "remove"
			case "list selection":
				input["operation"] = "list_targets"
			case "body document":
				input["document"] = "untrusted"
			case "candidate":
				input["candidate"] = "untrusted"
			case "execute operation":
				phase = "execute"
				body = f.body(phase)
				body["input"].(map[string]any)["operation"] = "add"
			case "execute path token":
				phase = "execute"
				body = f.body(phase)
				body["input"].(map[string]any)["commit_token"] = "../manifest"
			default:
				body[kind] = "untrusted"
			}
			before := portableState(t, f.portableFixture)
			rewriteError(t, f.post(t.Context(), phase, "descriptor-owner", body, ""), 400, "DOCUMENT_ACTION_INVALID")
			if spy.count() != 0 || portableState(t, f.portableFixture) != before {
				t.Error("invalid input reached Action or changed state")
			}
		})
	}
}

func TestDocumentCrossReferenceLiveLookupOnly(t *testing.T) {
	f := newCrossFixture(t, "markdown", "list_targets")
	seedRewriteConsumer(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "running")
	spy := newCrossServer(t, f)
	before := portableState(t, f.portableFixture)
	crossPreview(t, f, spy)
	f.operation = "add"
	for _, phase := range []string{"preview", "execute"} {
		rewriteError(t, f.post(t.Context(), phase, "descriptor-owner", f.body(phase), ""), 409, "ARTIFACT_IN_USE")
	}
	if spy.count() != 1 || portableState(t, f.portableFixture) != before {
		t.Error("live update executed or persisted")
	}
}

func TestDocumentCrossReferenceFinalCASAndRollback(t *testing.T) {
	for _, kind := range []string{"revision", "draft", "live", "human failure", "event failure"} {
		t.Run(kind, func(t *testing.T) {
			f := newCrossFixture(t, "markdown", "add")
			seedRewriteTerminalGraph(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
			spy := newCrossServer(t, f)
			crossPreview(t, f, spy)
			before := portableState(t, f.portableFixture)
			status, code := 409, "REVISION_CONFLICT"
			if kind == "human failure" || kind == "event failure" {
				status = 500
				code = "DOCUMENT_ACTION_SAVE_FAILED"
				table := "workflow_events"
				if kind == "human failure" {
					table = "plugin_human_artifacts"
				}
				if err := f.db.Callback().Create().Before("gorm:create").Register("cross-failure", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Table == table {
						tx.AddError(errors.New("forced cross-reference write failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { f.db.Callback().Create().Remove("cross-failure") })
			} else {
				if kind == "draft" {
					code = "DRAFT_VERSION_CONFLICT"
				}
				if kind == "live" {
					code = "ARTIFACT_IN_USE"
				}
				spy.beforeExecute = func() {
					var err error
					switch kind {
					case "draft":
						err = f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"draft_version": 8, "value": json.RawMessage(`{"text":"concurrent edit"}`)}).Error
					case "live":
						err = f.db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "rewrite-downstream").Update("status", "running").Error
					default:
						base, draft := 3, int64(7)
						_, err = workflow.WriteSlotRevisionWithHumanArtifact(t.Context(), f.db.DB, "descriptor-session", "unknown-slot", "unknown-slot", "source", 1, "single", nil, "text/markdown", json.RawMessage(`{"text":"concurrent revision"}`), nil, "human", &base, &draft)
					}
					if err != nil {
						t.Error(err)
					}
					before = portableState(t, f.portableFixture)
				}
			}
			rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute"), ""), status, code)
			if spy.count() != 2 || portableState(t, f.portableFixture) != before {
				t.Error("final CAS/rollback left partial effects")
			}
		})
	}
}

func TestDocumentCrossReferenceErrorsAndResultValidation(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, entry := range []string{"list", "preview", "execute"} {
			kinds := []string{"upstream", "selection error", "target error", "representation", "missing artifact", "wrong value"}
			if entry == "preview" {
				kinds = append(kinds, "missing patch", "wrong patch", "missing commit", "bad token", "wrong operation")
			}
			if entry == "list" {
				kinds = []string{"upstream", "representation", "missing targets", "null targets", "invalid target type", "missing invalid references"}
			}
			for _, kind := range kinds {
				t.Run(representation+"/"+entry+"/"+kind, func(t *testing.T) {
					operation, phase := "add", entry
					if entry == "list" {
						operation = "list_targets"
						phase = "preview"
					}
					f := newCrossFixture(t, representation, operation)
					spy := newCrossServer(t, f)
					if phase == "execute" {
						crossPreview(t, f, spy)
					}
					status, code := 502, "DOCUMENT_ACTION_RESULT_INVALID"
					result := f.result(phase)
					switch kind {
					case "upstream":
						spy.status = 502
						spy.code = "WORKFLOW_ACTION_FAILED"
						code = "DOCUMENT_ACTION_FAILED"
					case "selection error", "target error":
						spy.status = 422
						spy.code = "CROSS_REFERENCE_SELECTION_INVALID"
						if kind == "target error" {
							spy.code = "CROSS_REFERENCE_TARGET_NOT_FOUND"
						}
						status = 400
						code = spy.code
					case "representation":
						result["representation"] = "unknown"
					case "missing patch":
						delete(result, "patch")
					case "wrong patch":
						result["patch"].(map[string]any)["type"] = "unknown_patch"
					case "missing commit":
						delete(result, "commit")
					case "bad token":
						result["commit"].(map[string]any)["token"] = "../invalid"
					case "wrong operation":
						result["operation"] = "remove"
					case "missing artifact":
						delete(result, "artifact")
					case "wrong value":
						result["artifact"].(map[string]any)["value"] = nil
					case "missing targets":
						delete(result, "targets")
					case "null targets":
						result["targets"] = nil
					case "invalid target type":
						result["targets"].([]any)[0].(map[string]any)["type"] = "script"
					case "missing invalid references":
						delete(result, "invalid_references")
					}
					spy.override = result
					before := portableState(t, f.portableFixture)
					rewriteError(t, f.post(t.Context(), phase, "descriptor-owner", f.body(phase), ""), status, code)
					if portableState(t, f.portableFixture) != before {
						t.Error("bad response persisted")
					}
					expected := 1
					if phase == "execute" {
						expected = 2
					}
					if spy.count() != expected {
						t.Error("response boundary not reached")
					}
				})
			}
		}
	}
}

func TestDocumentCrossReferenceCancellation(t *testing.T) {
	for _, phase := range []string{"preview", "execute"} {
		t.Run(phase, func(t *testing.T) {
			f := newCrossFixture(t, "markdown", "add")
			spy := newCrossServer(t, f)
			if phase == "execute" {
				crossPreview(t, f, spy)
			}
			gate := &portableGate{started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1), release: make(chan struct{})}
			spy.gate = gate
			spy.gatePhase = phase
			t.Cleanup(func() { close(gate.release) })
			before := portableState(t, f.portableFixture)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- f.post(ctx, phase, "descriptor-owner", f.body(phase), "") }()
			select {
			case <-gate.started:
			case w := <-done:
				t.Fatalf("cancel boundary not reached: %d", w.Code)
			case <-time.After(2 * time.Second):
				t.Fatal("action never started")
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
					t.Error("cancelled action reported success")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("cancel did not finish")
			}
			if portableState(t, f.portableFixture) != before {
				t.Error("cancel persisted")
			}
		})
	}
}

func TestDocumentCrossReferenceTokenScope(t *testing.T) {
	for _, kind := range []string{"artifact", "draft", "owner"} {
		t.Run(kind, func(t *testing.T) {
			f := newCrossFixture(t, "markdown", "add")
			spy := newCrossServer(t, f)
			crossPreview(t, f, spy)
			body := f.body("execute")
			id, owner := "descriptor-artifact", "descriptor-owner"
			switch kind {
			case "artifact":
				f.seed(t, "cross-other", "other", "text/markdown", mustJSONRewrite(f.source))
				id = "cross-other"
			case "draft":
				if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("draft_version", 8).Error; err != nil {
					t.Fatal(err)
				}
				body["base_draft_version"] = 8
			case "owner":
				owner = "new-owner"
				if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("create_user_id", owner).Error; err != nil {
					t.Fatal(err)
				}
			}
			before := portableState(t, f.portableFixture)
			rw := rewriteFixture{descriptorFixture: f.descriptorFixture}
			rewriteError(t, rw.post(t.Context(), "execute", id, owner, body), 409, "SELECTION_STALE")
			if spy.count() != 2 || portableState(t, f.portableFixture) != before {
				t.Error("foreign baseline token accepted or changed state")
			}
		})
	}
	t.Run("action separation", func(t *testing.T) {
		f := newCrossFixture(t, "markdown", "add", true)
		rw := rewriteFixture{descriptorFixture: f.descriptorFixture, representation: f.representation, source: f.source, candidate: f.candidate, selection: f.selection}
		rewriteSpy := newRewriteServer(t, rw)
		token := rewriteToken(t, rw)
		spy := newCrossServer(t, f)
		crossPreview(t, f, spy)
		if spy.calls[0].ArtifactStore == rewriteSpy.calls()[0].ArtifactStore {
			t.Error("cross-reference and rewrite share manifest namespace")
		}
		body := f.body("execute")
		body["input"].(map[string]any)["commit_token"] = token
		before := portableState(t, f.portableFixture)
		rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", body, ""), 409, "SELECTION_STALE")
		t.Setenv("LAZYMIND_CHAT_SERVICE_URL", rewriteSpy.url)
		rewriteError(t, rw.post(t.Context(), "execute", "descriptor-artifact", "descriptor-owner", rw.body("execute", crossToken)), 409, "SELECTION_STALE")
		if portableState(t, f.portableFixture) != before {
			t.Error("cross-action token persisted")
		}
	})
}

func TestDocumentCrossReferenceConcurrentExecute(t *testing.T) {
	f := newCrossFixture(t, "markdown", "add")
	spy := newCrossServer(t, f)
	crossPreview(t, f, spy)
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	spy.beforeExecute = func() { ready <- struct{}{}; <-release }
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { results <- f.post(t.Context(), "execute", "descriptor-owner", f.body("execute"), "") }()
	}
	for range 2 {
		select {
		case <-ready:
		case w := <-results:
			t.Fatalf("concurrent action not started: %d", w.Code)
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent action timeout")
		}
	}
	unblock()
	success, conflict := 0, 0
	id := ""
	for range 2 {
		select {
		case w := <-results:
			if w.Code == 200 {
				success++
				id, _ = rewriteData(t, w)["artifact_id"].(string)
			} else {
				rewriteError(t, w, 409, "REVISION_CONFLICT")
				conflict++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent execute blocked")
		}
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
	if success != 1 || conflict != 1 || len(revisions) != 2 || len(humans) != 2 || len(events) != 1 || session.StateVersion != 10 {
		t.Fatalf("duplicate apply success=%d conflict=%d rows=%d/%d/%d state=%d", success, conflict, len(revisions), len(humans), len(events), session.StateVersion)
	}
	selected := 0
	for _, row := range revisions {
		if row.Selected {
			selected++
			if row.ID != id || row.Validity != "effective" {
				t.Error("selected identity")
			}
		}
	}
	if selected != 1 || events[0].EntityID != id || events[0].StateVersion != 10 {
		t.Error("selected/event not unique")
	}
}

func TestDocumentCrossReferenceReadLease(t *testing.T) {
	f := newCrossFixture(t, "markdown", "add")
	spy := newCrossServer(t, f)
	if err := f.db.AutoMigrate(&orm.ExternalChatRun{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiry := now.Add(time.Minute)
	run := orm.ExternalChatRun{ID: "cross-run", RequestID: "cross-request", ConversationID: "descriptor-conversation", HistoryID: "cross-history", Provider: "codex", ActorUserID: "descriptor-owner", Status: "running", HostID: "cross-host", LeaseToken: "cross-test-lease", LeaseExpiresAt: &expiry, CreatedAt: now, UpdatedAt: now}
	if err := f.db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	costs := watchPortableCosts(t, f.portableFixture)
	before := portableState(t, f.portableFixture)
	costs.contentQueries.Store(0)
	for _, phase := range []string{"preview", "execute"} {
		req := httptest.NewRequest("POST", "/workflow-artifacts/descriptor-artifact/document-actions:"+phase, strings.NewReader(mustJSONRewrite(f.body(phase))))
		for key, value := range map[string]string{"X-User-Id": "descriptor-owner", "X-LazyMind-External-Ref": run.ID, "X-LazyMind-External-Lease": run.LeaseToken, "X-LazyMind-External-Host": run.HostID, "X-LazyMind-Conversation-Id": run.ConversationID} {
			req.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		f.router.ServeHTTP(w, req)
		if w.Code != 409 {
			t.Errorf("lease action status=%d", w.Code)
		}
	}
	if spy.count() != 0 || spy.inspections != 0 || costs.contentQueries.Load() != 0 || portableState(t, f.portableFixture) != before {
		t.Error("lease action reached content/action or persisted")
	}
	requirePortableCosts(t, costs)
}

func TestDocumentCrossReferenceCapabilities(t *testing.T) {
	for _, state := range []string{"editable", "live", "historical", "stale", "session"} {
		for _, endpoint := range []string{descriptorRoutes[0], descriptorRoutes[1], descriptorRoutes[5], descriptorRoutes[6]} {
			t.Run(state+endpoint, func(t *testing.T) {
				f := newCrossFixture(t, "markdown", "list_targets")
				spy := newCrossServer(t, f)
				var err error
				switch state {
				case "live":
					seedRewriteConsumer(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "running")
				case "historical":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false).Error
				case "stale":
					err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", "stale").Error
				case "session":
					err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("status", "unknown-state").Error
				}
				if err != nil {
					t.Fatal(err)
				}
				before := portableState(t, f.portableFixture)
				records := descriptorRecords(t, f.read(t.Context(), endpoint, "descriptor-owner"))
				found := false
				for _, record := range records {
					if record["artifact_id"] != "descriptor-artifact" {
						continue
					}
					found = true
					doc := record["document"].(map[string]any)
					caps := schemaStringList(doc["capabilities"])
					sort.Strings(caps)
					want := []string{}
					if state == "editable" {
						want = []string{"convert_document", "cross_reference", "numbering", "publish_document", "save"}
					} else if state == "live" {
						want = []string{"convert_document", "cross_reference", "numbering"}
					}
					if !reflect.DeepEqual(caps, want) || doc["editable"] != (state == "editable") {
						t.Errorf("capability=%#v want=%v", doc, want)
					}
				}
				if !found || spy.count() != 0 || portableState(t, f.portableFixture) != before {
					t.Error("capability query incomplete or had effects")
				}
			})
		}
	}
}

func TestDocumentCrossReferenceFixtureControl(t *testing.T) {
	for _, operation := range []string{"list_targets", "add"} {
		t.Run(operation, func(t *testing.T) {
			f := newCrossFixture(t, "markdown", operation)
			spy := newCrossServer(t, f)
			reference, args, namespace := crossUpdateReference, f.input(), filepath.Join(f.workspace, "fixture")
			if operation == "list_targets" {
				reference = crossListReference
				args = map[string]any{}
				namespace = ""
			}
			body := map[string]any{"reference": reference, "phase": "preview", "artifact": map[string]any{"data": f.source}, "arguments": args, "artifact_store": namespace}
			response, err := http.Post(spy.url+"/api/document/actions:invoke", "application/json", strings.NewReader(mustJSONRewrite(body)))
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || !reflect.DeepEqual(value["result"], f.result("preview")) {
				t.Errorf("fixture response=%s", raw)
			}
			if operation == "add" {
				body["phase"] = "execute"
				body["arguments"] = map[string]any{"commit_token": crossToken}
				for _, foreign := range []bool{false, true} {
					if foreign {
						body["artifact_store"] = filepath.Join(f.workspace, "foreign")
					}
					response, err := http.Post(spy.url+"/api/document/actions:invoke", "application/json", strings.NewReader(mustJSONRewrite(body)))
					if err != nil {
						t.Fatal(err)
					}
					raw, err := io.ReadAll(response.Body)
					response.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					if foreign {
						if response.StatusCode != 409 {
							t.Error("fixture accepted foreign manifest")
						}
					} else {
						var result map[string]any
						if err := json.Unmarshal(raw, &result); err != nil {
							t.Fatal(err)
						}
						if response.StatusCode != 200 || !reflect.DeepEqual(result["result"], f.result("execute")) {
							t.Error("fixture did not execute confirmed candidate")
						}
					}
				}
			}
		})
	}
}

func TestDocumentCrossReferenceEmptyList(t *testing.T) {
	f := newCrossFixture(t, "markdown", "list_targets")
	spy := newCrossServer(t, f)
	spy.override = map[string]any{"representation": "markdown", "targets": []any{}, "invalid_references": []any{}}
	before := portableState(t, f.portableFixture)
	result := rewriteData(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("preview"), ""))
	if !reflect.DeepEqual(result, spy.override) || portableState(t, f.portableFixture) != before {
		t.Error("empty target list contract")
	}
}

func TestDocumentCrossReferenceOpenAPI(t *testing.T) {
	f := newCrossFixture(t, "markdown", "add")
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
	for _, phase := range []string{"preview", "execute"} {
		t.Run(phase, func(t *testing.T) {
			op := openAPIOperationForTest(t, spec, "post", "/api/core/workflow-artifacts/{artifact_id}/document-actions:"+phase)
			request := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
			body := documentActionRequestSchemaForTest(t, request, schemas, "cross_reference")
			props := body["properties"].(map[string]any)
			for key, kind := range map[string]string{"action": "string", "base_revision": "integer", "base_draft_version": "integer", "input": "object"} {
				if resolve(props[key])["type"] != kind {
					t.Errorf("request %s must be %s", key, kind)
				}
			}
			for _, key := range []string{"action", "base_revision", "input"} {
				if !containsPortable(schemaStringList(body["required"]), key) {
					t.Errorf("required request %s", key)
				}
			}
			if containsPortable(schemaStringList(body["required"]), "base_draft_version") {
				t.Error("native draft globally required")
			}
			input := resolve(props["input"])
			ip, _ := input["properties"].(map[string]any)
			if phase == "preview" {
				operation := resolve(ip["operation"])
				values := schemaStringList(operation["enum"])
				sort.Strings(values)
				if !reflect.DeepEqual(values, []string{"add", "list_targets", "remove", "retarget"}) {
					t.Errorf("operation schema=%#v", operation)
				}
				if !containsPortable(schemaStringList(input["required"]), "operation") {
					t.Error("operation not required")
				}
				if ip["selection"] == nil || resolve(ip["target_id"])["type"] != "string" {
					t.Error("selection/target contract missing")
				}
				requireCrossReferenceSelectionSchema(t, ip["selection"], schemas)
			} else if len(ip) != 1 || resolve(ip["commit_token"])["type"] != "string" || !containsPortable(schemaStringList(input["required"]), "commit_token") {
				t.Errorf("execute input=%#v", input)
			}
			responses := op["responses"].(map[string]any)
			success := resolve(responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
			root := success["properties"].(map[string]any)["data"]
			var find func(any, string) map[string]any
			find = func(value any, field string) map[string]any {
				obj := resolve(value)
				properties, _ := obj["properties"].(map[string]any)
				if properties[field] != nil {
					return obj
				}
				for _, key := range []string{"oneOf", "anyOf", "allOf"} {
					branches, _ := obj[key].([]any)
					for _, branch := range branches {
						if found := find(branch, field); found != nil {
							return found
						}
					}
				}
				return nil
			}
			if phase == "preview" {
				for _, field := range []string{"targets", "operation"} {
					result := find(root, field)
					if result == nil {
						t.Fatalf("cross-reference result %s missing", field)
					}
					rp := result["properties"].(map[string]any)
					required := []string{"representation", field}
					if field == "targets" {
						required = append(required, "invalid_references")
						for _, key := range []string{"targets", "invalid_references"} {
							if resolve(rp[key])["type"] != "array" {
								t.Errorf("result %s not array", key)
							}
						}
					} else {
						required = append(required, "patch", "artifact", "commit")
					}
					for _, key := range required {
						if !containsPortable(schemaStringList(result["required"]), key) {
							t.Errorf("result lacks required %s", key)
						}
					}
				}
			} else {
				result := find(root, "artifact_id")
				if result == nil {
					t.Fatal("execute result missing")
				}
				rp := result["properties"].(map[string]any)
				for key, kind := range map[string]string{"artifact_id": "string", "revision": "integer", "draft_version": "integer"} {
					if resolve(rp[key])["type"] != kind {
						t.Errorf("execute %s type", key)
					}
				}
			}
			failure := resolve(responses["400"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"])
			data := resolve(failure["properties"].(map[string]any)["data"])
			codes := schemaStringList(resolve(data["properties"].(map[string]any)["code"])["enum"])
			for _, code := range []string{"CROSS_REFERENCE_SELECTION_INVALID", "CROSS_REFERENCE_TARGET_NOT_FOUND"} {
				if !containsPortable(codes, code) {
					t.Errorf("missing public error %s", code)
				}
			}
		})
	}
}

func crossSourceSnapshot(t *testing.T, f crossFixture) string {
	t.Helper()
	var humans []orm.WorkflowHumanArtifact
	var native []orm.SubAgentArtifact
	if err := f.db.Where("id = ?", "descriptor-artifact-human").Find(&humans).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("id = ?", "portable-source").Find(&native).Error; err != nil {
		t.Fatal(err)
	}
	sourceFile := ""
	suffix := ".md"
	if f.representation == "ir" {
		suffix = ".lmd"
	}
	raw, err := os.ReadFile(filepath.Join(f.workspace, "source"+suffix))
	if err == nil {
		sourceFile = string(raw)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return mustJSONRewrite([]any{humans, native, sourceFile})
}

func TestDocumentCrossReferenceIRSelectionRequiredFields(t *testing.T) {
	for _, kind := range []string{"missing text", "empty text", "missing node", "empty node"} {
		t.Run(kind, func(t *testing.T) {
			f := newCrossFixture(t, "ir", "add")
			spy := newCrossServer(t, f)
			costs := watchPortableCosts(t, f.portableFixture)
			body := f.body("preview")
			selection := body["input"].(map[string]any)["selection"].(map[string]any)
			switch kind {
			case "missing text":
				delete(selection, "selected_text")
			case "empty text":
				selection["selected_text"] = ""
			case "missing node":
				delete(selection, "node_id")
			case "empty node":
				selection["node_id"] = ""
			}
			before := portableState(t, f.portableFixture)
			rewriteError(t, f.post(t.Context(), "preview", "descriptor-owner", body, ""), 400, "DOCUMENT_ACTION_INVALID")
			if spy.count() != 0 || spy.inspections != 0 {
				t.Error("invalid IR selection reached Algorithm")
			}
			requirePortableCosts(t, costs)
			if portableState(t, f.portableFixture) != before {
				t.Error("invalid IR selection changed state")
			}
		})
	}
}

func TestDocumentCrossReferenceStrictResponseFields(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, entry := range []string{"list", "preview", "execute"} {
			kinds := []string{"other representation", "content type", "non-null value type", "artifact missing value"}
			if entry == "list" {
				kinds = []string{"other representation", "targets object", "target null", "target missing id", "target id number", "target missing title", "target title number", "target missing type", "invalid null", "invalid object", "invalid item null", "invalid missing id", "invalid id number"}
			}
			if entry == "preview" {
				kinds = append(kinds, "missing payload", "null payload", "payload array", "other patch type")
			}
			for _, kind := range kinds {
				t.Run(representation+"/"+entry+"/"+kind, func(t *testing.T) {
					operation, phase := "add", entry
					if entry == "list" {
						operation = "list_targets"
						phase = "preview"
					}
					f := newCrossFixture(t, representation, operation)
					seedRewriteTerminalGraph(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
					spy := newCrossServer(t, f)
					if phase == "execute" {
						crossPreview(t, f, spy)
					}
					result := f.result(phase)
					switch kind {
					case "other representation":
						if representation == "markdown" {
							result["representation"] = "ir"
						} else {
							result["representation"] = "markdown"
						}
					case "content type":
						result["artifact"].(map[string]any)["content_type"] = "file"
					case "non-null value type":
						if representation == "markdown" {
							result["artifact"].(map[string]any)["value"] = map[string]any{"text": f.candidate}
						} else {
							result["artifact"].(map[string]any)["value"] = "a Markdown string"
						}
					case "artifact missing value":
						delete(result["artifact"].(map[string]any), "value")
					case "missing payload":
						delete(result["patch"].(map[string]any), "payload")
					case "null payload":
						result["patch"].(map[string]any)["payload"] = nil
					case "payload array":
						result["patch"].(map[string]any)["payload"] = []any{}
					case "other patch type":
						if representation == "markdown" {
							result["patch"].(map[string]any)["type"] = "writer_ir_patch"
						} else {
							result["patch"].(map[string]any)["type"] = "string_replace_set"
						}
					case "targets object":
						result["targets"] = map[string]any{}
					case "target null":
						result["targets"] = []any{nil}
					case "target missing id":
						delete(result["targets"].([]any)[0].(map[string]any), "target_id")
					case "target id number":
						result["targets"].([]any)[0].(map[string]any)["target_id"] = 12
					case "target missing title":
						delete(result["targets"].([]any)[0].(map[string]any), "title")
					case "target title number":
						result["targets"].([]any)[0].(map[string]any)["title"] = 12
					case "target missing type":
						delete(result["targets"].([]any)[0].(map[string]any), "type")
					case "invalid null":
						result["invalid_references"] = nil
					case "invalid object":
						result["invalid_references"] = map[string]any{}
					case "invalid item null":
						result["invalid_references"] = []any{nil}
					case "invalid missing id":
						result["invalid_references"] = []any{map[string]any{}}
					case "invalid id number":
						result["invalid_references"] = []any{map[string]any{"target_id": 12}}
					}
					spy.override = result
					before := portableState(t, f.portableFixture)
					w := f.post(t.Context(), phase, "descriptor-owner", f.body(phase), "")
					rewriteError(t, w, 502, "DOCUMENT_ACTION_RESULT_INVALID")
					if strings.Contains(w.Body.String(), crossToken) {
						t.Error("invalid result exposed confirmation token")
					}
					if portableState(t, f.portableFixture) != before {
						t.Error("invalid response changed Core data, source or graph")
					}
					expected := 1
					if phase == "execute" {
						expected = 2
					}
					if spy.count() != expected {
						t.Error("result validation boundary not reached")
					}
				})
			}
		}
	}
}

func requireCrossReferenceSelectionSchema(t *testing.T, value any, schemas map[string]any) {
	t.Helper()
	resolve := func(value any) map[string]any {
		obj, _ := value.(map[string]any)
		if ref, ok := obj["$ref"].(string); ok {
			obj, _ = schemas[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
		}
		return obj
	}
	root := resolve(value)
	branches, _ := root["oneOf"].([]any)
	if len(branches) == 0 {
		branches, _ = root["anyOf"].([]any)
	}
	if len(branches) != 2 {
		t.Fatalf("selection must describe Markdown and IR branches: %#v", root)
	}
	found := map[string]bool{}
	for _, branch := range branches {
		schema := resolve(branch)
		props, _ := schema["properties"].(map[string]any)
		types := schemaStringList(resolve(props["type"])["enum"])
		if len(types) != 1 || (types[0] != "markdown" && types[0] != "ir") {
			t.Fatalf("selection representation not discriminated: %#v", schema)
		}
		representation := types[0]
		if found[representation] {
			t.Fatal("duplicate selection branch")
		}
		found[representation] = true
		fields := []string{"type", "selected_text"}
		if representation == "ir" {
			fields = append(fields, "node_id")
		}
		for _, field := range fields {
			if resolve(props[field])["type"] != "string" || !containsPortable(schemaStringList(schema["required"]), field) {
				t.Errorf("%s selection requires string %s", representation, field)
			}
		}
	}
	if !found["markdown"] || !found["ir"] {
		t.Error("selection representation branch missing")
	}
}

func TestDocumentCrossReferenceEmptyTargetTitle(t *testing.T) {
	f := newCrossFixture(t, "markdown", "list_targets")
	spy := newCrossServer(t, f)
	// Image/heading display text may be empty. Presence of a string title is
	// required, but Core must not equate an empty title with an absent field.
	spy.override = map[string]any{"representation": "markdown", "targets": []any{map[string]any{"target_id": "diagram", "type": "image", "title": ""}}, "invalid_references": []any{}}
	before := portableState(t, f.portableFixture)
	result := rewriteData(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("preview"), ""))
	if !reflect.DeepEqual(result, spy.override) || portableState(t, f.portableFixture) != before {
		t.Error("empty display title rejected or altered")
	}
}
