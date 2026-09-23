package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type numberingFixture struct {
	portableFixture
	canonical, view any
	exported        string
	targetID        string
	numbering       map[string]any
}

func newNumberingFixture(t *testing.T, representation string, withModel bool) numberingFixture {
	t.Helper()
	f := numberingFixture{portableFixture: newPortableFixture(t, representation, withModel), targetID: "sec-001"}
	f.source = "# Title\n\n## 2026 Results\n\n`x_1` and $a_2$.\n"
	f.canonical = "# Title\n\n<a id=\"block-sec-001\" numbering=\"mode=unordered\"></a>\n## 2026 Results\n\n`x_1` and $a_2$.\n"
	f.view = "# Title\n\n<a id=\"block-sec-001\"></a>\n## 2026 Results\n\nDisplay only.\n"
	f.exported = "# Title\n\n## 1 2026 Results\n\nExport only.\n"
	ct := "text/markdown"
	var stored any = f.source
	if representation == "ir" {
		f.targetID = "heading-1"
		f.source = map[string]any{"document_id": "numbering-doc", "title": "Title", "blocks": []any{map[string]any{"node_id": f.targetID, "type": "heading", "content": "2026 Results", "numbering": map[string]any{"level": 1}}, map[string]any{"node_id": "p", "type": "paragraph", "content": "x_1 and a_2"}}}
		f.canonical = map[string]any{"document_id": "numbering-doc", "title": "Title", "blocks": []any{map[string]any{"node_id": f.targetID, "type": "heading", "content": "2026 Results", "numbering": map[string]any{"level": 1, "mode": "unordered"}}, map[string]any{"node_id": "p", "type": "paragraph", "content": "x_1 and a_2"}}}
		f.view = map[string]any{"document_id": "numbering-doc", "title": "Display only", "blocks": []any{map[string]any{"node_id": f.targetID, "type": "heading", "content": "Display only", "numbering": map[string]any{"level": 1}}}}
		ct = "json"
		stored = map[string]any{"schema": descriptorIRSchema, "data": f.source}
	}
	f.numbering = map[string]any{"ordered_style": "hierarchical", "entries": map[string]any{f.targetID: map[string]any{"label": "1", "mode": "ordered", "restart": false}}}
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"content_type": ct, "value": json.RawMessage(mustJSONRewrite(stored))}).Error; err != nil {
		t.Fatal(err)
	}
	return f
}
func (f numberingFixture) body(phase string, update any) map[string]any {
	input := map[string]any{}
	if phase == "execute" {
		input["numbering_update"] = update
	}
	body := map[string]any{"action": "numbering", "base_revision": 3, "input": input}
	if !f.native {
		body["base_draft_version"] = 7
	}
	return body
}
func (f numberingFixture) update() map[string]any {
	return map[string]any{"type": "heading", "target_id": f.targetID, "mode": "unordered"}
}

type numberingCall struct {
	Reference, Phase string
	Artifact         map[string]any
	Arguments        map[string]any
}
type numberingServer struct {
	mu            sync.Mutex
	calls         []numberingCall
	inspections   int
	status        int
	raw           string
	override      map[string]any
	beforeExecute func()
	gate          *portableGate
	gatePhase     string
	url           string
}

func newNumberingServer(t *testing.T, f numberingFixture, update any) *numberingServer {
	t.Helper()
	spy := &numberingServer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "POST" || (r.URL.Path != "/api/document:inspect" && r.URL.Path != "/api/document/actions:invoke") {
			t.Errorf("unexpected numbering I/O: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
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
			spy.inspections++
			spy.mu.Unlock()
			response := descriptorMarkdown
			if f.representation == "ir" {
				response = descriptorIR
			}
			_, _ = w.Write([]byte(response))
			return
		}
		for key, value := range raw {
			switch key {
			case "reference", "phase", "artifact", "arguments":
			case "artifact_store":
				if string(value) != `""` {
					t.Error("numbering allocated manifest namespace")
				}
			default:
				t.Errorf("numbering passed forbidden context %s", key)
			}
		}
		var call numberingCall
		if err := json.Unmarshal([]byte(mustJSONRewrite(raw)), &call); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		wantRef := "builtin:document.render_document.v1"
		if call.Phase == "execute" {
			wantRef = "builtin:document.save_document.v1"
		} else if call.Phase != "preview" {
			t.Errorf("unknown phase=%s", call.Phase)
		}
		if call.Reference != wantRef || !jsonEqualNumbering(call.Artifact, map[string]any{"data": f.source}) {
			t.Errorf("numbering dispatch/source=%#v", call)
		}
		wantArgs := map[string]any{}
		if call.Phase == "execute" {
			wantArgs = map[string]any{"base_artifact": map[string]any{"data": f.source}, "numbering_update": update}
		}
		if !jsonEqualNumbering(call.Arguments, wantArgs) {
			t.Errorf("numbering arguments=%#v want=%#v", call.Arguments, wantArgs)
		}
		spy.mu.Lock()
		spy.calls = append(spy.calls, call)
		status, rawResponse, override, hook, gate, gatePhase := spy.status, spy.raw, spy.override, spy.beforeExecute, spy.gate, spy.gatePhase
		spy.mu.Unlock()
		if gate != nil && call.Phase == gatePhase {
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
		if rawResponse != "" {
			_, _ = w.Write([]byte(rawResponse))
			return
		}
		if call.Phase == "execute" && hook != nil {
			hook()
		}
		result := map[string]any{"title": "Title", "representation": f.representation, "document": f.view, "numbering": f.numbering}
		if f.representation == "markdown" {
			result["export_document"] = f.exported
		}
		if call.Phase == "execute" {
			result["source_document"] = f.canonical
		}
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
func (s *numberingServer) count() int        { s.mu.Lock(); defer s.mu.Unlock(); return len(s.calls) }
func (s *numberingServer) inspectCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.inspections }
func jsonEqualNumbering(a, b any) bool       { return mustJSONRewrite(a) == mustJSONRewrite(b) }
func requireNumberingView(t *testing.T, f numberingFixture, data map[string]any) {
	t.Helper()
	want := map[string]any{"title": "Title", "representation": f.representation, "document": f.view, "numbering": f.numbering}
	if f.representation == "markdown" {
		want["export_document"] = f.exported
	}
	for key, value := range want {
		if !jsonEqualNumbering(data[key], value) {
			t.Errorf("numbering view %s=%#v want=%#v", key, data[key], value)
		}
	}
	if data["commit"] != nil || data["provider_synced"] != nil {
		t.Errorf("numbering claimed unrelated effect=%#v", data)
	}
}

func TestDocumentNumberingPreviewAndCanonicalSave(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, carrier := range []string{"human", "native", "file"} {
			for _, list := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/list=%v", representation, carrier, list), func(t *testing.T) {
					f := newNumberingFixture(t, representation, false)
					f.carrier(t, carrier)
					seedRewriteTerminalGraph(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
					if list {
						if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("list_index", 0).Error; err != nil {
							t.Fatal(err)
						}
						f.seed(t, "numbering-other", "other", "text/markdown", `{"text":"other item"}`)
						if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "numbering-other").Updates(map[string]any{"slot_id": "unknown-slot", "slot": "unknown-slot", "list_index": 1}).Error; err != nil {
							t.Fatal(err)
						}
					}
					spy := newNumberingServer(t, f, f.update())
					costs := watchPortableCosts(t, f.portableFixture)
					before := portableState(t, f.portableFixture)
					preview := rewriteData(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("preview", nil), ""))
					requireNumberingView(t, f, preview)
					if portableState(t, f.portableFixture) != before {
						t.Error("render persisted anchors, labels or state")
					}
					var old orm.WorkflowSlotRevision
					if err := f.db.First(&old, "id = ?", "descriptor-artifact").Error; err != nil {
						t.Fatal(err)
					}
					var oldHuman orm.WorkflowHumanArtifact
					var oldNative orm.SubAgentArtifact
					if f.native {
						if err := f.db.First(&oldNative, "id = ?", "portable-source").Error; err != nil {
							t.Fatal(err)
						}
					} else if err := f.db.First(&oldHuman, "id = ?", "descriptor-artifact-human").Error; err != nil {
						t.Fatal(err)
					}
					result := rewriteData(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", f.update()), ""))
					requireNumberingView(t, f, result)
					requirePortableCosts(t, costs)
					if spy.count() != 2 {
						t.Errorf("numbering calls=%d", spy.count())
					}
					var selected []orm.WorkflowSlotRevision
					if err := f.db.Where("slot_id = ? AND selected = ?", "unknown-slot", true).Order("list_index").Find(&selected).Error; err != nil {
						t.Fatal(err)
					}
					count := 1
					if list {
						count = 2
					}
					if len(selected) != count || selected[0].Revision != 4 || selected[0].HumanArtifactID == nil {
						t.Fatalf("new numbering revision=%#v", selected)
					}
					current := selected[0]
					if list && (current.ListIndex == nil || *current.ListIndex != 0 || selected[1].ID != "numbering-other") {
						t.Errorf("numbering changed wrong item=%#v", selected)
					}
					var human orm.WorkflowHumanArtifact
					if err := f.db.First(&human, "id = ?", *current.HumanArtifactID).Error; err != nil {
						t.Fatal(err)
					}
					want := map[string]any{"text": f.canonical}
					ct := "text/markdown"
					if representation == "ir" {
						want = map[string]any{"schema": descriptorIRSchema, "data": f.canonical}
						ct = "json"
					}
					var value any
					if err := json.Unmarshal(human.Value, &value); err != nil {
						t.Fatal(err)
					}
					if human.ContentType != ct || human.DraftVersion != 1 || !jsonEqualNumbering(value, want) {
						t.Errorf("saved noncanonical view: type=%s value=%#v", human.ContentType, value)
					}
					if result["artifact_id"] != current.ID || result["revision"] != float64(4) || result["draft_version"] != float64(1) {
						t.Errorf("save response identity=%#v", result)
					}
					var afterOld orm.WorkflowSlotRevision
					if err := f.db.First(&afterOld, "id = ?", old.ID).Error; err != nil {
						t.Fatal(err)
					}
					if afterOld.Selected {
						t.Error("old revision still selected")
					}
					afterOld.Selected = old.Selected
					if !reflect.DeepEqual(afterOld, old) {
						t.Error("old revision lineage changed")
					}
					if f.native {
						var after orm.SubAgentArtifact
						if err := f.db.First(&after, "id = ?", oldNative.ID).Error; err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(after, oldNative) {
							t.Error("native source changed")
						}
					} else {
						var after orm.WorkflowHumanArtifact
						if err := f.db.First(&after, "id = ?", oldHuman.ID).Error; err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(after, oldHuman) {
							t.Error("old human source changed")
						}
					}
					if carrier == "file" {
						suffix := ".md"
						var wantFile []byte
						if representation == "markdown" {
							wantFile = []byte(f.source.(string))
						} else {
							suffix = ".lmd"
							wantFile = []byte(mustJSONRewrite(map[string]any{"schema": descriptorIRSchema, "data": f.source}))
						}
						actual, err := os.ReadFile(filepath.Join(f.workspace, "source"+suffix))
						if err != nil || string(actual) != string(wantFile) {
							t.Errorf("original file changed: %v", err)
						}
					}
					var events []orm.WorkflowEvent
					if err := f.db.Find(&events).Error; err != nil {
						t.Fatal(err)
					}
					if len(events) != 1 || events[0].EntityID != current.ID || events[0].StateVersion != 10 || events[0].EventType != "artifact.upsert" || events[0].OwnerUserID != "descriptor-owner" || events[0].ContractVersion != "workflow.v1" {
						t.Fatalf("numbering event=%#v", events)
					}
					var payload map[string]any
					if err := json.Unmarshal(events[0].PayloadJSON, &payload); err != nil {
						t.Fatal(err)
					}
					if payload["artifact_id"] != current.ID || payload["revision"] != float64(4) || payload["draft_version"] != float64(1) || payload["state_version"] != float64(10) || payload["change_source"] != "human" {
						t.Errorf("numbering payload=%#v", payload)
					}
					var session orm.WorkflowSession
					if err := f.db.First(&session, "id = ?", "descriptor-session").Error; err != nil {
						t.Fatal(err)
					}
					if session.StateVersion != 10 {
						t.Errorf("state=%d", session.StateVersion)
					}
					requireRewriteGraphState(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "stale", false)
					record := descriptorRecords(t, f.read(t.Context(), "/workflow-artifacts/"+current.ID, "descriptor-owner"))[0]
					doc, _ := record["document"].(map[string]any)
					if doc["representation"] != representation {
						t.Error("canonical revision lost document identity")
					}
					after := portableState(t, f.portableFixture)
					rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", f.update()), ""), 409, "REVISION_CONFLICT")
					if portableState(t, f.portableFixture) != after {
						t.Error("repeated execute saved twice")
					}
				})
			}
		}
	}
}

func TestDocumentNumberingUpdateProtocol(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, kind := range []string{"hierarchical", "chinese", "parenthesized", "ordered", "unordered", "restart", "continue"} {
			t.Run(representation+kind, func(t *testing.T) {
				f := newNumberingFixture(t, representation, false)
				update := map[string]any{"type": "heading", "target_id": f.targetID}
				switch kind {
				case "hierarchical", "chinese", "parenthesized":
					update = map[string]any{"type": "ordered_style", "ordered_style": kind}
				case "ordered", "unordered":
					update["mode"] = kind
				case "restart":
					update["restart"] = true
				case "continue":
					update["restart"] = false
				}
				spy := newNumberingServer(t, f, update)
				costs := watchPortableCosts(t, f.portableFixture)
				result := rewriteData(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", update), ""))
				if result["revision"] != float64(4) || spy.count() != 1 {
					t.Errorf("direct numbering apply=%#v calls=%d", result, spy.count())
				}
				requirePortableCosts(t, costs)
			})
		}
	}
}

func TestDocumentNumberingRejectsInjectionAndInvalidUpdate(t *testing.T) {
	invalid := []any{nil, map[string]any{}, map[string]any{"type": "ordered_style", "ordered_style": "roman"}, map[string]any{"type": "heading", "mode": "ordered"}, map[string]any{"type": "heading", "target_id": "h"}, map[string]any{"type": "heading", "target_id": "h", "mode": "other"}, map[string]any{"type": "heading", "target_id": "h", "restart": 1}, map[string]any{"type": "heading", "target_id": "h", "mode": "unordered", "restart": true}, map[string]any{"type": "heading", "target_id": "h", "mode": "ordered", "path": "/private"}}
	for _, update := range invalid {
		t.Run(mustJSONRewrite(update), func(t *testing.T) {
			f := newNumberingFixture(t, "markdown", false)
			spy := newNumberingServer(t, f, update)
			costs := watchPortableCosts(t, f.portableFixture)
			before := portableState(t, f.portableFixture)
			rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", update), ""), 400, "DOCUMENT_ACTION_INVALID")
			if spy.count() != 0 {
				t.Error("invalid update reached save Action")
			}
			requirePortableCosts(t, costs)
			if portableState(t, f.portableFixture) != before {
				t.Error("invalid update persisted")
			}
		})
	}
	for _, phase := range []string{"preview", "execute"} {
		for _, field := range []string{"document", "base_artifact", "reference", "artifact_store", "llm_config", "tool_config", "provider"} {
			t.Run(phase+field, func(t *testing.T) {
				f := newNumberingFixture(t, "markdown", false)
				spy := newNumberingServer(t, f, f.update())
				body := f.body(phase, f.update())
				if field == "document" || field == "base_artifact" || field == "provider" {
					body["input"].(map[string]any)[field] = "injected"
				} else {
					body[field] = "injected"
				}
				before := portableState(t, f.portableFixture)
				rewriteError(t, f.post(t.Context(), phase, "descriptor-owner", body, ""), 400, "DOCUMENT_ACTION_INVALID")
				if spy.count() != 0 {
					t.Error("injection reached Action")
				}
				if portableState(t, f.portableFixture) != before {
					t.Error("injection persisted")
				}
			})
		}
	}
	t.Run("preview cannot apply update", func(t *testing.T) {
		f := newNumberingFixture(t, "markdown", false)
		spy := newNumberingServer(t, f, f.update())
		body := f.body("execute", f.update())
		rewriteError(t, f.post(t.Context(), "preview", "descriptor-owner", body, ""), 400, "DOCUMENT_ACTION_INVALID")
		if spy.count() != 0 {
			t.Error("preview applied configuration")
		}
	})
}

func TestDocumentNumberingPreconditionsBeforeCost(t *testing.T) {
	for _, phase := range []string{"preview", "execute"} {
		for _, kind := range []string{"missing owner", "wrong owner", "scope", "missing revision", "revision", "missing draft", "draft", "historical", "stale", "deleted", "dismissed", "session"} {
			t.Run(phase+kind, func(t *testing.T) {
				f := newNumberingFixture(t, "markdown", false)
				spy := newNumberingServer(t, f, f.update())
				costs := watchPortableCosts(t, f.portableFixture)
				body := f.body(phase, f.update())
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
				case "scope":
					scope = "other-conversation"
					status = 403
					code = "PERMISSION_DENIED"
				case "missing revision":
					delete(body, "base_revision")
					status = 400
					code = "REVISION_REQUIRED"
				case "revision":
					body["base_revision"] = 2
				case "missing draft":
					delete(body, "base_draft_version")
					status = 400
					code = "DRAFT_VERSION_REQUIRED"
				case "draft":
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
				if spy.count() != 0 || spy.inspectCount() != 0 {
					t.Error("rejected target reached Algorithm")
				}
				if (kind == "missing owner" || kind == "wrong owner" || kind == "scope") && costs.contentQueries.Load() != 0 {
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

func TestDocumentNumberingLivePreviewAndExecute(t *testing.T) {
	f := newNumberingFixture(t, "markdown", true)
	seedRewriteConsumer(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "running")
	spy := newNumberingServer(t, f, f.update())
	costs := watchPortableCosts(t, f.portableFixture)
	before := portableState(t, f.portableFixture)
	requireNumberingView(t, f, rewriteData(t, f.post(t.Context(), "preview", "descriptor-owner", f.body("preview", nil), "")))
	rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", f.update()), ""), 409, "ARTIFACT_IN_USE")
	if spy.count() != 1 {
		t.Error("live update reached Algorithm")
	}
	requirePortableCosts(t, costs)
	if portableState(t, f.portableFixture) != before {
		t.Error("live numbering read/update mutated state")
	}
}

func TestDocumentNumberingFinalCASAndRollback(t *testing.T) {
	for _, kind := range []string{"writer", "live", "event failure", "human failure"} {
		t.Run(kind, func(t *testing.T) {
			f := newNumberingFixture(t, "markdown", false)
			seedRewriteTerminalGraph(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
			spy := newNumberingServer(t, f, f.update())
			before := portableState(t, f.portableFixture)
			status := 500
			code := "DOCUMENT_ACTION_SAVE_FAILED"
			if kind == "writer" || kind == "live" {
				status = 409
				code = "REVISION_CONFLICT"
				if kind == "live" {
					code = "ARTIFACT_IN_USE"
				}
				spy.beforeExecute = func() {
					if kind == "live" {
						if err := f.db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "rewrite-downstream").Update("status", "running").Error; err != nil {
							t.Error(err)
						}
					} else {
						base, draft := 3, int64(7)
						if _, err := workflow.WriteSlotRevisionWithHumanArtifact(t.Context(), f.db.DB, "descriptor-session", "unknown-slot", "unknown-slot", "source", 1, "single", nil, "text/markdown", json.RawMessage(`{"text":"concurrent"}`), nil, "human", &base, &draft); err != nil {
							t.Error(err)
						}
					}
					before = portableState(t, f.portableFixture)
				}
			} else {
				table := "workflow_events"
				if kind == "human failure" {
					table = "plugin_human_artifacts"
				}
				if err := f.db.Callback().Create().Before("gorm:create").Register("numbering-fail", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Table == table {
						tx.AddError(errors.New("forced numbering save failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { f.db.Callback().Create().Remove("numbering-fail") })
			}
			rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", f.update()), ""), status, code)
			if spy.count() != 1 {
				t.Error("did not reach intended failure boundary")
			}
			if portableState(t, f.portableFixture) != before {
				t.Error("failed numbering save left partial effects")
			}
		})
	}
}

func TestDocumentNumberingFailureAndCancellation(t *testing.T) {
	for _, phase := range []string{"preview", "execute"} {
		for _, kind := range []string{"upstream", "invalid target", "invalid result", "cancel"} {
			t.Run(phase+kind, func(t *testing.T) {
				f := newNumberingFixture(t, "ir", false)
				seedRewriteTerminalGraph(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
				spy := newNumberingServer(t, f, f.update())
				costs := watchPortableCosts(t, f.portableFixture)
				status := 502
				code := "DOCUMENT_ACTION_FAILED"
				spy.mu.Lock()
				switch kind {
				case "upstream":
					spy.status = 502
					spy.raw = `{"detail":{"code":"WORKFLOW_ACTION_FAILED","message":"private-upstream-detail"}}`
				case "invalid target":
					spy.status = 422
					spy.raw = `{"detail":{"code":"WORKFLOW_ACTION_INVALID","message":"private-upstream-detail"}}`
					status = 400
					code = "DOCUMENT_ACTION_INVALID"
				case "invalid result":
					spy.override = map[string]any{"representation": "markdown", "document": "not canonical", "numbering": map[string]any{}}
					code = "DOCUMENT_ACTION_RESULT_INVALID"
				case "cancel":
					spy.gate = &portableGate{started: make(chan struct{}, 1), cancelled: make(chan struct{}, 1), release: make(chan struct{})}
					spy.gatePhase = phase
				}
				gate := spy.gate
				spy.mu.Unlock()
				before := portableState(t, f.portableFixture)
				if gate != nil {
					t.Cleanup(func() { close(gate.release) })
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					done := make(chan *httptest.ResponseRecorder, 1)
					go func() { done <- f.post(ctx, phase, "descriptor-owner", f.body(phase, f.update()), "") }()
					select {
					case <-gate.started:
					case w := <-done:
						t.Fatalf("numbering did not start: %d", w.Code)
					case <-time.After(2 * time.Second):
						t.Fatal("numbering not started")
					}
					cancel()
					select {
					case <-gate.cancelled:
					case <-time.After(2 * time.Second):
						t.Fatal("numbering cancel not propagated")
					}
					select {
					case w := <-done:
						if w.Code < 400 {
							t.Error("cancel returned success")
						}
					case <-time.After(2 * time.Second):
						t.Fatal("cancel did not finish")
					}
				} else {
					rewriteError(t, f.post(t.Context(), phase, "descriptor-owner", f.body(phase, f.update()), ""), status, code)
				}
				requirePortableCosts(t, costs)
				if portableState(t, f.portableFixture) != before {
					t.Error("failure/cancel changed state")
				}
			})
		}
	}
}

func TestDocumentNumberingCapabilities(t *testing.T) {
	for _, withModel := range []bool{false, true} {
		for _, state := range []string{"editable", "live", "historical", "stale", "dismissed", "session"} {
			for index, endpoint := range descriptorRoutes {
				t.Run(fmt.Sprint(withModel)+state+endpoint, func(t *testing.T) {
					f := newNumberingFixture(t, "markdown", withModel)
					spy := newNumberingServer(t, f, f.update())
					var err error
					switch state {
					case "live":
						seedRewriteConsumer(t, rewriteFixture{descriptorFixture: f.descriptorFixture}, "running")
					case "historical":
						err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("selected", false).Error
					case "stale":
						err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("validity", "stale").Error
					case "dismissed":
						err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("dismissed", true).Error
					case "session":
						err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("status", "unknown-state").Error
					}
					if err != nil {
						t.Fatal(err)
					}
					before := portableState(t, f.portableFixture)
					w := f.read(t.Context(), endpoint, "descriptor-owner")
					if state == "dismissed" && index < 2 {
						if w.Code != 404 {
							t.Errorf("dismissed status=%d", w.Code)
						}
					} else {
						records := descriptorOptionalRecords(t, w)
						filtered := (state == "dismissed" && (index == 2 || index == 3)) || (state == "historical" && index == 4) || (state == "session" && index == 2)
						if filtered {
							if len(records) != 0 {
								t.Error("filter changed")
							}
						} else {
							found := false
							for _, record := range records {
								if record["artifact_id"] != "descriptor-artifact" {
									continue
								}
								found = true
								doc, _ := record["document"].(map[string]any)
								caps := schemaStringList(doc["capabilities"])
								sort.Strings(caps)
								want := []string{}
								if state == "editable" {
									want = []string{"convert_document", "cross_reference", "numbering", "publish_document", "save"}
									if withModel {
										want = []string{"convert_document", "cross_reference", "numbering", "publish_document", "rewrite_selection", "save"}
									}
								} else if state == "live" {
									want = []string{"convert_document", "cross_reference", "numbering"}
								}
								if !reflect.DeepEqual(caps, want) || doc["editable"] != (state == "editable") {
									t.Errorf("numbering capability=%#v want=%#v", doc, want)
								}
							}
							if !found {
								t.Error("document missing")
							}
						}
					}
					if spy.count() != 0 {
						t.Error("capability query executed numbering")
					}
					if portableState(t, f.portableFixture) != before {
						t.Error("capability read persisted")
					}
				})
			}
		}
	}
}

// These tests check the Core boundary; target editability and numbering itself
// remain owned by Algorithm, whose invalid-input response is exercised here.
func TestDocumentNumberingInvalidTarget(t *testing.T) {
	for _, kind := range []string{"missing", "readonly"} {
		t.Run(kind, func(t *testing.T) {
			f := newNumberingFixture(t, "ir", false)
			update := f.update()
			if kind == "missing" {
				update["target_id"] = "absent-heading"
			} else {
				f.source.(map[string]any)["blocks"].([]any)[0].(map[string]any)["editable"] = false
				if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(map[string]any{"schema": descriptorIRSchema, "data": f.source}))).Error; err != nil {
					t.Fatal(err)
				}
			}
			spy := newNumberingServer(t, f, update)
			spy.status = 422
			spy.raw = `{"detail":{"code":"WORKFLOW_ACTION_INVALID","message":"private-upstream-detail"}}`
			before := portableState(t, f.portableFixture)
			rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", update), ""), 400, "DOCUMENT_ACTION_INVALID")
			if spy.count() != 1 || portableState(t, f.portableFixture) != before {
				t.Error("invalid target was not validated without persistence")
			}
		})
	}
}

func TestDocumentNumberingReadLeaseRemainsRestricted(t *testing.T) {
	f := newNumberingFixture(t, "markdown", false)
	spy := newNumberingServer(t, f, f.update())
	if err := f.db.AutoMigrate(&orm.ExternalChatRun{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiry := now.Add(time.Minute)
	run := orm.ExternalChatRun{ID: "numbering-run", RequestID: "numbering-request", ConversationID: "descriptor-conversation", HistoryID: "numbering-history", Provider: "codex", ActorUserID: "descriptor-owner", Status: "running", HostID: "numbering-host", LeaseToken: "numbering-test-lease", LeaseExpiresAt: &expiry, CreatedAt: now, UpdatedAt: now}
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
	record := descriptorRecords(t, call("GET", "/workflow-artifacts/descriptor-artifact", ""))[0]
	doc, _ := record["document"].(map[string]any)
	if containsPortable(schemaStringList(doc["capabilities"]), "numbering") {
		t.Error("lease advertised numbering")
	}
	costs := watchPortableCosts(t, f.portableFixture)
	before := portableState(t, f.portableFixture)
	inspections := spy.inspectCount()
	costs.contentQueries.Store(0)
	for _, phase := range []string{"preview", "execute"} {
		w := call("POST", "/workflow-artifacts/descriptor-artifact/document-actions:"+phase, mustJSONRewrite(f.body(phase, f.update())))
		if w.Code != 409 {
			t.Errorf("lease action status=%d", w.Code)
		}
	}
	if costs.contentQueries.Load() != 0 || spy.count() != 0 || spy.inspectCount() != inspections {
		t.Error("lease reached content or Algorithm")
	}
	requirePortableCosts(t, costs)
	if portableState(t, f.portableFixture) != before {
		t.Error("lease action persisted")
	}
}

func TestDocumentNumberingOpenAPI(t *testing.T) {
	f := newNumberingFixture(t, "markdown", false)
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
			body := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
			request := documentActionRequestSchemaForTest(t, body, schemas, "numbering")
			props := request["properties"].(map[string]any)
			for field, want := range map[string]string{"action": "string", "base_revision": "integer", "base_draft_version": "integer", "input": "object"} {
				if resolve(props[field])["type"] != want {
					t.Errorf("request %s lacks %s", field, want)
				}
			}
			for _, field := range []string{"action", "base_revision", "input"} {
				if !containsPortable(schemaStringList(request["required"]), field) {
					t.Errorf("request missing required %s", field)
				}
			}
			if containsPortable(schemaStringList(request["required"]), "base_draft_version") {
				t.Error("native draft globally required")
			}
			input := resolve(props["input"])
			ip, _ := input["properties"].(map[string]any)
			if phase == "preview" && len(ip) != 0 {
				t.Error("preview accepts configuration or body")
			}
			if phase == "execute" {
				if len(ip) != 1 || ip["numbering_update"] == nil || !containsPortable(schemaStringList(input["required"]), "numbering_update") {
					t.Errorf("execute input=%#v", input)
				}
				update := resolve(ip["numbering_update"])
				// A typed object or discriminated union must expose the real update fields.
				encoded := mustJSONRewrite(update)
				if len(update) == 0 || encoded == `{"type":"object"}` {
					t.Error("numbering_update is untyped")
				}
			}
			response := op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
			data := documentActionResultSchemaForTest(t, resolve(response)["properties"].(map[string]any)["data"], schemas, "numbering")
			dp := data["properties"].(map[string]any)
			for _, field := range []string{"title", "representation", "document", "numbering"} {
				if dp[field] == nil || !containsPortable(schemaStringList(data["required"]), field) {
					t.Errorf("result lacks required %s", field)
				}
			}
			for _, field := range []string{"title", "representation", "export_document"} {
				if resolve(dp[field])["type"] != "string" {
					t.Errorf("result %s not string", field)
				}
			}
			if resolve(dp["numbering"])["type"] != "object" {
				t.Error("numbering result not object")
			}
			if phase == "execute" {
				for field, want := range map[string]string{"artifact_id": "string", "revision": "integer", "draft_version": "integer"} {
					if resolve(dp[field])["type"] != want || !containsPortable(schemaStringList(data["required"]), field) {
						t.Errorf("save result identity %s", field)
					}
				}
			}
			if dp["commit"] != nil || dp["commit_token"] != nil {
				t.Error("numbering requires rewrite commit")
			}
		})
	}
}

func TestDocumentNumberingRejectsIncompleteResult(t *testing.T) {
	for _, phase := range []string{"preview", "execute"} {
		for _, field := range []string{"title", "representation", "document", "numbering", "source_document"} {
			if phase == "preview" && field == "source_document" {
				continue
			}
			t.Run(phase+field, func(t *testing.T) {
				f := newNumberingFixture(t, "markdown", false)
				spy := newNumberingServer(t, f, f.update())
				result := map[string]any{"title": "Title", "representation": f.representation, "document": f.view, "numbering": f.numbering, "source_document": f.canonical, "export_document": f.exported}
				delete(result, field)
				spy.override = result
				before := portableState(t, f.portableFixture)
				rewriteError(t, f.post(t.Context(), phase, "descriptor-owner", f.body(phase, f.update()), ""), 502, "DOCUMENT_ACTION_RESULT_INVALID")
				if spy.count() != 1 || portableState(t, f.portableFixture) != before {
					t.Error("incomplete result boundary or rollback")
				}
			})
		}
	}
}

func TestDocumentNumberingFixtureProtocolControl(t *testing.T) {
	f := newNumberingFixture(t, "markdown", false)
	spy := newNumberingServer(t, f, f.update())
	for _, phase := range []string{"preview", "execute"} {
		ref := "builtin:document.render_document.v1"
		args := map[string]any{}
		if phase == "execute" {
			ref = "builtin:document.save_document.v1"
			args = map[string]any{"base_artifact": map[string]any{"data": f.source}, "numbering_update": f.update()}
		}
		payload := map[string]any{"reference": ref, "phase": phase, "artifact": map[string]any{"data": f.source}, "arguments": args}
		response, err := http.Post(spy.url+"/api/document/actions:invoke", "application/json", strings.NewReader(mustJSONRewrite(payload)))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Result map[string]any `json:"result"`
		}
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("fixture response: %v", err)
		}
		requireNumberingView(t, f, result.Result)
		if phase == "execute" && !jsonEqualNumbering(result.Result["source_document"], f.canonical) {
			t.Error("fixture lost canonical source")
		}
	}
	if spy.count() != 2 {
		t.Error("fixture dispatch missing")
	}
}

func TestDocumentNumberingFinalDraftCAS(t *testing.T) {
	f := newNumberingFixture(t, "markdown", false)
	seedRewriteTerminalGraph(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
	spy := newNumberingServer(t, f, f.update())
	afterEdit := ""
	spy.beforeExecute = func() {
		if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"draft_version": 8, "value": json.RawMessage(`{"text":"concurrent draft edit"}`)}).Error; err != nil {
			t.Error(err)
		}
		afterEdit = portableState(t, f.portableFixture)
	}
	rewriteError(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", f.update()), ""), 409, "DRAFT_VERSION_CONFLICT")
	if spy.count() != 1 || afterEdit == "" {
		t.Fatal("final draft boundary not reached")
	}
	if portableState(t, f.portableFixture) != afterEdit {
		t.Error("draft conflict lost concurrent edit or left action effects")
	}
}

func TestDocumentNumberingConcurrentExecuteAppliesOnce(t *testing.T) {
	f := newNumberingFixture(t, "markdown", false)
	spy := newNumberingServer(t, f, f.update())
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	spy.beforeExecute = func() { ready <- struct{}{}; <-release }
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			results <- f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", f.update()), "")
		}()
	}
	// Both requests must pass authorization/baselines and reach Algorithm before
	// either response allows the final transaction to begin.
	for range 2 {
		select {
		case <-ready:
		case w := <-results:
			t.Fatalf("concurrent numbering never reached Algorithm: %d %s", w.Code, w.Body.String())
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent numbering did not rendezvous")
		}
	}
	unblock()
	success, conflicts := 0, 0
	var savedID string
	for range 2 {
		select {
		case w := <-results:
			if w.Code == 200 {
				success++
				savedID, _ = rewriteData(t, w)["artifact_id"].(string)
			} else {
				rewriteError(t, w, 409, "REVISION_CONFLICT")
				conflicts++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent numbering blocked")
		}
	}
	if success != 1 || conflicts != 1 || spy.count() != 2 {
		t.Errorf("success=%d conflicts=%d calls=%d", success, conflicts, spy.count())
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
		t.Fatalf("duplicate effects revisions=%d humans=%d events=%d state=%d", len(revisions), len(humans), len(events), session.StateVersion)
	}
	selected := 0
	for _, row := range revisions {
		if row.Selected {
			selected++
			if row.ID != savedID || row.Revision != 4 || row.Validity != "effective" || row.HumanArtifactID == nil {
				t.Errorf("selected identity=%#v", row)
			}
		}
	}
	if selected != 1 || events[0].EntityID != savedID || events[0].StateVersion != 10 || events[0].EventType != "artifact.upsert" {
		t.Errorf("selected/event selected=%d event=%#v", selected, events[0])
	}
}

func TestDocumentNumberingRejectsWrongResultType(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		for _, phase := range []string{"preview", "execute"} {
			kinds := []string{"representation", "document type"}
			if phase == "execute" {
				kinds = append(kinds, "null source", "source type")
				if representation == "ir" {
					kinds = append(kinds, "invalid IR source")
				}
			}
			for _, kind := range kinds {
				t.Run(representation+phase+kind, func(t *testing.T) {
					f := newNumberingFixture(t, representation, false)
					seedRewriteTerminalGraph(t, rewriteFixture{descriptorFixture: f.descriptorFixture})
					spy := newNumberingServer(t, f, f.update())
					result := map[string]any{"title": "Title", "representation": representation, "document": f.view, "numbering": f.numbering}
					if representation == "markdown" {
						result["export_document"] = f.exported
					}
					if phase == "execute" {
						result["source_document"] = f.canonical
					}
					switch kind {
					case "representation":
						if representation == "markdown" {
							result["representation"] = "ir"
						} else {
							result["representation"] = "markdown"
						}
					case "document type":
						if representation == "markdown" {
							result["document"] = map[string]any{"blocks": []any{}}
						} else {
							result["document"] = "wrong IR type"
						}
					case "null source":
						result["source_document"] = nil
					case "source type":
						if representation == "markdown" {
							result["source_document"] = map[string]any{"blocks": []any{}}
						} else {
							result["source_document"] = "wrong IR type"
						}
					case "invalid IR source":
						result["source_document"] = map[string]any{"document_id": "numbering-doc", "title": "Title", "blocks": "not an IR block array"}
					}
					spy.override = result
					before := portableState(t, f.portableFixture)
					rewriteError(t, f.post(t.Context(), phase, "descriptor-owner", f.body(phase, f.update()), ""), 502, "DOCUMENT_ACTION_RESULT_INVALID")
					if spy.count() != 1 {
						t.Error("result boundary not reached")
					}
					if portableState(t, f.portableFixture) != before {
						t.Error("invalid result persisted canonical content or graph effects")
					}
				})
			}
		}
	}
}
