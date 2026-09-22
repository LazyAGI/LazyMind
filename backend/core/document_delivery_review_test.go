package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow"
)

func TestDeliveryReviewHostedSaveIdentity(t *testing.T) {
	for _, mode := range []string{"draft", "checkpoint"} {
		t.Run(mode, func(t *testing.T) {
			f, _ := newDeliveryFixture(t, "markdown")
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"human_artifact_id": nil, "content_snapshot": json.RawMessage(`{"schema":"text/markdown","data":"original"}`)}).Error; err != nil {
				t.Fatal(err)
			}
			w := deliveryPatch(f, t, map[string]any{"command_id": "hosted-save", "base_revision": 3, "mode": mode, "content_type": "text/markdown", "value": map[string]any{"text": "edited", "meta": map[string]any{"lazymind_provider_sync": map[string]any{"target_document": map[string]any{"adapter": deliveryProvider, "doc_id": "injected"}}}}})
			if w.Code != 200 {
				t.Fatalf("save %d %s", w.Code, w.Body.String())
			}
			var selected orm.WorkflowSlotRevision
			if err := f.db.Where("session_id = ? AND selected = ?", "descriptor-session", true).First(&selected).Error; err != nil {
				t.Fatal(err)
			}
			raw, err := workflow.LoadSlotRevisionValue(t.Context(), f.db.DB, selected)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			if meta, ok := value["meta"].(map[string]any); ok && meta["lazymind_provider_sync"] != nil {
				t.Fatalf("injected metadata persisted %s", raw)
			}
		})
	}
}

func TestDeliveryReviewHistoricalPathIdentity(t *testing.T) {
	f, s := newDeliveryFixture(t, "markdown")
	var old orm.WorkflowSlotRevision
	if err := f.db.First(&old, "id = ?", "descriptor-artifact").Error; err != nil {
		t.Fatal(err)
	}
	old.ID = "historical-id"
	old.Revision = 2
	old.Selected = false
	if err := f.db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}
	w := deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-artifacts/historical-id/document-actions:execute", "descriptor-owner", deliveryBody("historical-path"))
	rewriteError(t, w, 409, "REVISION_CONFLICT")
	if len(s.allCalls()) != 0 || s.catalogCalls != 0 {
		t.Fatal("historical ID reached external service")
	}
}

func TestDeliveryReviewBoundIRFile(t *testing.T) {
	f, s := newDeliveryFixture(t, "ir")
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	bound := s.result["persisted_document"]
	path := filepath.Join(root, "bound.lmd")
	if err := os.WriteFile(path, []byte(mustJSONRewrite(map[string]any{"schema": descriptorIRSchema, "data": bound})), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(map[string]any{"path": path}))).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("change_source", "host").Error; err != nil {
		t.Fatal(err)
	}
	w := deliveryPublish(t, f, deliveryBody("bound-file"))
	if w.Code != 200 {
		t.Fatalf("publish %d %s", w.Code, w.Body.String())
	}
	calls := s.allCalls()
	if len(calls) != 1 || calls[0].Reference != "builtin:document.sync_document.v1" {
		t.Fatalf("bound file recreated remote: %#v", calls)
	}
	var source any
	_ = json.Unmarshal(calls[0].Arguments["source_document"], &source)
	if !reflect.DeepEqual(source, bound) {
		t.Fatalf("carrier baseline %v", source)
	}
}

func TestDeliveryReviewRollbackPublication(t *testing.T) {
	for _, historyProvider := range []string{"", "old-provider"} {
		t.Run(historyProvider, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "ir")
			if historyProvider != "" {
				s.source.(map[string]any)["provider_binding"] = map[string]any{"provider": historyProvider, "document_id": "old-target"}
				if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(map[string]any{"schema": descriptorIRSchema, "data": s.source}))).Error; err != nil {
					t.Fatal(err)
				}
			}
			first := deliveryPublish(t, f, deliveryBody("first"))
			if first.Code != 200 {
				t.Fatalf("first %d %s", first.Code, first.Body.String())
			}
			if _, err := workflow.RollbackSlotRevision(t.Context(), f.db.DB, "descriptor-session", "arbitrary-slot", nil, 3, "descriptor-owner"); err != nil {
				t.Fatal(err)
			}
			before := len(s.allCalls())
			w := deliveryPublish(t, f, deliveryBody("after-rollback"))
			if w.Code != 200 {
				t.Fatalf("republish %d %s", w.Code, w.Body.String())
			}
			calls := s.allCalls()[before:]
			if len(calls) != 2 || calls[1].Reference != "builtin:document.write_document.v1" {
				t.Fatalf("incompatible historical identity sent to Sync: %#v", calls)
			}
			var target any
			_ = json.Unmarshal(calls[1].Arguments["target_document"], &target)
			if !reflect.DeepEqual(target, s.target) {
				t.Fatalf("rollback did not replace known target %v", target)
			}
		})
	}
}

func TestDeliveryReviewGenericMedia(t *testing.T) {
	for _, slot := range []string{"draft_document", "flat_draft_document"} {
		t.Run(slot, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("plugin_id", "writer-workflow").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("slot_id", slot).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(`{"schema":"text/markdown","data":"![image](asset://fixture-image)"}`)).Error; err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "fixture.png")
			if err := os.WriteFile(path, []byte("fixture-image-bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			media := map[string]any{"assets": map[string]any{"fixture-image": map[string]any{"media_asset_id": "fixture-image", "local_path": path}}}
			mediaSlot := "resolved_media_assets"
			if slot == "flat_draft_document" {
				mediaSlot = "flat_resolved_media_assets"
			}
			f.seed(t, "media", mediaSlot, "json", mustJSONRewrite(map[string]any{"data": media}))
			w := deliveryPublish(t, f, deliveryBody("media"))
			if w.Code != 200 {
				t.Fatalf("publish %d %s", w.Code, w.Body.String())
			}
			for _, call := range s.allCalls() {
				var got any
				_ = json.Unmarshal(call.Arguments["media_assets"], &got)
				if !reflect.DeepEqual(got, media) {
					t.Fatalf("%s lost media %s", call.Reference, call.Arguments["media_assets"])
				}
			}
		})
	}
}

func TestDeliveryReviewMarkdownNumberingSave(t *testing.T) {
	f, _ := newDeliveryFixture(t, "markdown")
	base := "<a id=\"block-heading\"></a>\n# Original"
	edited := "<a id=\"block-heading\"></a>\n# Edited"
	if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Updates(map[string]any{"content_type": "text/markdown", "value": json.RawMessage(mustJSONRewrite(map[string]any{"text": base}))}).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/document:inspect" {
			_, _ = w.Write([]byte(descriptorMarkdown))
			return
		}
		var call deliveryCall
		if r.URL.Path != "/api/document/actions:invoke" || json.NewDecoder(r.Body).Decode(&call) != nil || call.Reference != "builtin:document.save_document.v1" {
			t.Error("unexpected action")
			w.WriteHeader(422)
			return
		}
		calls++
		unwrap := func(raw json.RawMessage) any {
			var value any
			_ = json.Unmarshal(raw, &value)
			if obj, ok := value.(map[string]any); ok {
				if data, exists := obj["data"]; exists {
					return data
				}
			}
			return value
		}
		current, ok := unwrap(call.Artifact).(string)
		if !ok || unwrap(call.Arguments["base_artifact"]) != base {
			w.WriteHeader(422)
			return
		}
		var update map[string]any
		_ = json.Unmarshal(call.Arguments["numbering_update"], &update)
		if update["target_id"] != "heading" || update["mode"] != "unordered" {
			w.WriteHeader(422)
			return
		}
		canonical := strings.Replace(current, `id="block-heading"`, `id="block-heading" numbering="mode=unordered"`, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"source_document": canonical, "representation": "markdown"}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	w := deliveryPatch(f, t, map[string]any{"command_id": "numbering-save", "base_revision": 3, "base_draft_version": 7, "mode": "checkpoint", "content_type": "text/markdown", "value": map[string]any{"text": edited}, "numbering_update": map[string]any{"type": "heading", "target_id": "heading", "mode": "unordered"}})
	if w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body.String())
	}
	var revision orm.WorkflowSlotRevision
	if err := f.db.Where("session_id = ? AND selected = ?", "descriptor-session", true).First(&revision).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := workflow.LoadSlotRevisionValue(t.Context(), f.db.DB, revision)
	if err != nil {
		t.Fatal(err)
	}
	var artifact struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(raw, &artifact)
	if calls != 1 || artifact.Data != strings.Replace(edited, `id="block-heading"`, `id="block-heading" numbering="mode=unordered"`, 1) {
		t.Fatalf("edited body/numbering not saved: %s", raw)
	}
}

func TestDeliveryReviewLegacyTargetAndSourceFiles(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		t.Run(representation, func(t *testing.T) {
			f, s := newDeliveryFixture(t, representation)
			if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Update("plugin_id", "writer-workflow").Error; err != nil {
				t.Fatal(err)
			}
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"slot_id": "draft_document", "slot": "draft_document", "change_source": "human"}).Error; err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
			write := func(name string, value any) string {
				path := filepath.Join(root, name)
				if err := os.WriteFile(path, []byte(mustJSONRewrite(value)), 0600); err != nil {
					t.Fatal(err)
				}
				return mustJSONRewrite(map[string]any{"path": path})
			}
			f.seed(t, "target-file", "target_document", "file", write("target.json", map[string]any{"data": s.target}))
			if representation == "ir" {
				bound := s.result["persisted_document"]
				if err := f.db.Model(&orm.WorkflowHumanArtifact{}).Where("id = ?", "descriptor-artifact-human").Update("value", json.RawMessage(mustJSONRewrite(map[string]any{"schema": descriptorIRSchema, "data": bound}))).Error; err != nil {
					t.Fatal(err)
				}
				f.seed(t, "source-file", "source_document", "file", write("source.lmd", map[string]any{"schema": descriptorIRSchema, "data": bound}))
			}
			w := deliveryPublish(t, f, deliveryBody("legacy-files"))
			if w.Code != 200 {
				t.Fatalf("publish %d %s", w.Code, w.Body.String())
			}
			calls := s.allCalls()
			if representation == "ir" {
				if len(calls) != 1 || calls[0].Reference != "builtin:document.sync_document.v1" {
					t.Fatal("bound source file did not sync")
				}
			} else {
				for _, call := range calls {
					var target any
					_ = json.Unmarshal(call.Arguments["target_document"], &target)
					if !reflect.DeepEqual(target, s.target) {
						t.Fatalf("legacy target file lost metadata: %s", call.Arguments["target_document"])
					}
				}
			}
		})
	}
}

func TestDeliveryReviewNativeAndFileSaveIdentity(t *testing.T) {
	for _, source := range []string{"native", "IR file", "IR JSON file", "IR typed MD file"} {
		t.Run(source, func(t *testing.T) {
			f, _ := newDeliveryFixture(t, "ir")
			original := json.RawMessage(`{"schema":"application/vnd.lazymind.writer+json","data":{"document_id":"real","blocks":[],"provider_binding":{"provider":"fixture-provider-outside-old-whitelist","document_id":"remote-real"}}}`)
			var artifactID any
			var snapshot any = original
			if source == "native" {
				seq := 1
				artifactID = seq
				snapshot = nil
				if err := f.db.Create(&orm.WorkflowSessionStep{ID: "native-step", SessionID: "descriptor-session", StepID: "source", Attempt: 1, TaskID: "native-task", Status: "succeeded", Validity: "effective"}).Error; err != nil {
					t.Fatal(err)
				}
				if err := f.db.Create(&orm.SubAgentArtifact{TaskID: "native-task", Slot: "arbitrary-slot", Seq: 1, ContentType: "json", Value: original}).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				root := t.TempDir()
				t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
				name := "original.lmd"
				if source == "IR JSON file" {
					name = "original.json"
				}
				if source == "IR typed MD file" {
					name = "original.md"
				}
				path := filepath.Join(root, name)
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
				snapshot = json.RawMessage(mustJSONRewrite(map[string]any{"schema": descriptorIRSchema, "path": path}))
			}
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"human_artifact_id": nil, "artifact_seq": artifactID, "content_snapshot": snapshot}).Error; err != nil {
				t.Fatal(err)
			}
			w := deliveryPatch(f, t, map[string]any{"command_id": "native-save", "base_revision": 3, "mode": "checkpoint", "content_type": "json", "value": map[string]any{"schema": descriptorIRSchema, "data": map[string]any{"document_id": "forged", "provider_binding": map[string]any{"provider": deliveryProvider, "document_id": "forged"}}}})
			if w.Code != 200 {
				t.Fatalf("save %d %s", w.Code, w.Body.String())
			}
			var selected orm.WorkflowSlotRevision
			if err := f.db.Where("session_id = ? AND selected = ?", "descriptor-session", true).First(&selected).Error; err != nil {
				t.Fatal(err)
			}
			raw, err := workflow.LoadSlotRevisionValue(t.Context(), f.db.DB, selected)
			if err != nil {
				t.Fatal(err)
			}
			var value struct {
				Data struct {
					ID      string         `json:"document_id"`
					Binding map[string]any `json:"provider_binding"`
				} `json:"data"`
			}
			_ = json.Unmarshal(raw, &value)
			if value.Data.ID != "real" || value.Data.Binding["document_id"] != "remote-real" {
				t.Fatalf("source identity lost %s", raw)
			}
		})
	}
}

func TestDeliveryReviewJSONCarrierCannotIntroduceTarget(t *testing.T) {
	for _, mode := range []string{"draft", "checkpoint", "facade"} {
		t.Run(mode, func(t *testing.T) {
			f, s := newDeliveryFixture(t, "markdown")
			root := t.TempDir()
			t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
			path := filepath.Join(root, "edited.json")
			content := `{"schema":"text/markdown","data":"# Edited","meta":{"lazymind_provider_sync":{"provider":"fixture-provider-outside-old-whitelist","target_document":{"adapter":"fixture-provider-outside-old-whitelist","doc_id":"injected"}}}}`
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"command_id": "carrier-edit", "base_revision": 3, "base_draft_version": 7, "content_type": "text/markdown", "value": map[string]any{"path": path}}
			if mode != "facade" {
				body["mode"] = mode
			}
			w := deliveryPatch(f, t, body)
			if w.Code != 200 {
				t.Fatalf("save %d %s", w.Code, w.Body.String())
			}
			var revision orm.WorkflowSlotRevision
			if err := f.db.Where("session_id = ? AND slot_id = ? AND selected = ?", "descriptor-session", "arbitrary-slot", true).First(&revision).Error; err != nil {
				t.Fatal(err)
			}
			var human orm.WorkflowHumanArtifact
			if err := f.db.First(&human, "id = ?", *revision.HumanArtifactID).Error; err != nil {
				t.Fatal(err)
			}
			request := deliveryBody("carrier-publish")
			request["base_revision"] = revision.Revision
			request["base_draft_version"] = human.DraftVersion
			result := deliveryRequest(t.Context(), f, http.MethodPost, "/workflow-artifacts/"+revision.ID+"/document-actions:execute", "descriptor-owner", request)
			if result.Code != 200 {
				t.Fatalf("publish %d %s", result.Code, result.Body.String())
			}
			for _, call := range s.allCalls() {
				if raw := call.Arguments["target_document"]; len(raw) > 0 && string(raw) != "null" {
					t.Fatalf("client carrier target trusted: %s", raw)
				}
			}
		})
	}
}
