package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow"
)

func deliveryPatch(f descriptorFixture, t *testing.T, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/workflow-artifacts/descriptor-artifact", strings.NewReader(mustJSONRewrite(body))).WithContext(t.Context())
	req.Header.Set("X-User-Id", "descriptor-owner")
	req.Header.Set("Workflow-Contract-Version", "workflow.v1")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}
func deliveryFacadeError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != status || body.Error.Code != code {
		t.Fatalf("PATCH status=%d body=%s want %d/%s", w.Code, w.Body.String(), status, code)
	}
}

func TestDocumentDeliveryArtifactPatchDraftBaseline(t *testing.T) {
	for _, name := range []string{"missing", "stale", "concurrent in-place edit"} {
		t.Run(name, func(t *testing.T) {
			f := newDescriptorFixture(t)
			f.seed(t, "descriptor-artifact", "arbitrary-document", "text/markdown", `{"text":"original"}`)
			if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Update("change_source", "human").Error; err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"base_revision": 3, "content_type": "text/markdown", "value": map[string]any{"text": "stale client overwrite"}, "command_id": "delivery-save"}
			status, code := 400, "DRAFT_VERSION_REQUIRED"
			if name == "stale" {
				body["base_draft_version"] = 6
				status, code = 409, "DRAFT_VERSION_CONFLICT"
			}
			if name == "concurrent in-place edit" {
				body["base_draft_version"] = 7
				status, code = 409, "DRAFT_VERSION_CONFLICT"
				rev, draft := 3, int64(7)
				saved, version, inPlace, err := workflow.UpdateSelectedHumanArtifactValue(t.Context(), f.db.DB, "descriptor-session", "arbitrary-document", nil, "text/markdown", json.RawMessage(`{"text":"newer editor content"}`), nil, &rev, &draft)
				if err != nil || !inPlace || saved == nil || version != 8 {
					t.Fatalf("concurrent edit control %#v %d %v %v", saved, version, inPlace, err)
				}
			}
			before := descriptorSnapshot(t, f)
			w := deliveryPatch(f, t, body)
			deliveryFacadeError(t, w, status, code)
			if descriptorSnapshot(t, f) != before {
				t.Fatal("rejected full-document PATCH changed revision/human/state/event")
			}
		})
	}
}

func TestDocumentDeliveryArtifactPatchValidBaseline(t *testing.T) {
	f := newDescriptorFixture(t)
	f.seed(t, "descriptor-artifact", "arbitrary-document", "text/markdown", `{"text":"original"}`)
	w := deliveryPatch(f, t, map[string]any{"base_revision": 3, "base_draft_version": 7, "content_type": "text/markdown", "value": map[string]any{"text": "saved"}, "command_id": "delivery-save-valid"})
	if w.Code != 200 {
		t.Fatalf("valid save status=%d %s", w.Code, w.Body.String())
	}
	var selected []orm.WorkflowSlotRevision
	if err := f.db.Where("session_id = ? AND slot_id = ? AND selected = ?", "descriptor-session", "arbitrary-document", true).Find(&selected).Error; err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Revision != 4 || selected[0].HumanArtifactID == nil {
		t.Fatalf("saved selection %#v", selected)
	}
	var human orm.WorkflowHumanArtifact
	if err := f.db.First(&human, "id = ?", *selected[0].HumanArtifactID).Error; err != nil {
		t.Fatal(err)
	}
	if human.DraftVersion != 1 {
		t.Fatalf("saved human %#v", human)
	}
	var content map[string]any
	if json.Unmarshal(human.Value, &content) != nil || len(content) != 1 || content["text"] != "saved" {
		t.Fatalf("saved content %s", human.Value)
	}
}
