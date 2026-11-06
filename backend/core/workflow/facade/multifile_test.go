package facade

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestPrepareConsumeOrderedFilesAndRejectInvalidBindings(t *testing.T) {
	h, db := testHandler(t)
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.TaskCenterTask{}, &orm.WorkflowResource{}, &orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.WorkflowResource{ID: "files-workflow", WorkflowRef: "builtin:files", WorkflowID: "files", OwnerUserID: "owner", OwnerScope: "owner", RelativeRoot: "files", HeadRevisionID: "files-revision", Version: 1, Status: "active", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.WorkflowRevision{ID: "files-revision", WorkflowResourceID: "files-workflow", RevisionNo: 1, CreatedAt: now, CompiledGraph: json.RawMessage(`{"nodes":{},"material_producers":{"files":{"kind":"external"},"brief":{"kind":"external","optional":true}},"material_cardinalities":{"files":"list","brief":"single"}}`)}).Error; err != nil {
		t.Fatal(err)
	}
	refs := []any{}
	for index := range 13 {
		resource, _, err := h.Store.ImportInputResource(t.Context(), "owner", "same-name.txt", "text/plain", fmt.Sprintf("sha256:%d", index), []byte(fmt.Sprintf("file %d", index)))
		if err != nil {
			t.Fatal(err)
		}
		refs = append(refs, map[string]any{"resource_id": resource.ID, "revision": float64(resource.Revision), "content_hash": resource.ContentHash})
	}
	refs = append(refs, refs[0]) // Repeated content retains its position.
	prepare := func(key string, bindings map[string]any) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"workflow_id": "files", "idempotency_key": key, "input_bindings": bindings, "origin_host": "workbuddy", "controller_host": "lazymind"})
		w := httptest.NewRecorder()
		h.Prepare(w, request(http.MethodPost, "/workflow-preparations", "owner", body))
		return w
	}
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"empty", []any{}}, {"null", nil}, {"string", "bad"}, {"mixed", []any{refs[0], "bad"}},
		{"fraction", map[string]any{"resource_id": refs[0].(map[string]any)["resource_id"], "revision": 1.5}},
		{"hash", map[string]any{"resource_id": refs[0].(map[string]any)["resource_id"], "content_hash": "wrong"}},
		{"version", map[string]any{"resource_id": refs[0].(map[string]any)["resource_id"], "revision": 2}},
		{"foreign", map[string]any{"resource_id": "not-owned"}},
	} {
		w := prepare(tc.name, map[string]any{"files": tc.value})
		if w.Code != 422 || decodeEnvelope(t, w).Error.Code != "INVALID_INPUT_BINDINGS" {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}
	if w := prepare("scalar-list", map[string]any{"files": refs[0], "brief": refs}); w.Code != 422 {
		t.Fatalf("single cardinality accepted multiple: %s", w.Body.String())
	}
	w := prepare("ordered", map[string]any{"files": refs, "brief": refs[0]})
	if w.Code != 200 {
		t.Fatalf("prepare %d: %s", w.Code, w.Body.String())
	}
	data := decodeEnvelope(t, w).Data.(map[string]any)
	id := data["preparation_id"].(string)
	for range 2 {
		r := mux.SetURLVars(request(http.MethodPost, "/consume", "owner", []byte(`{"session_id":"file-session"}`)), map[string]string{"preparation_id": id})
		w := httptest.NewRecorder()
		h.Consume(w, r)
		if w.Code != 200 {
			t.Fatalf("consume %d: %s", w.Code, w.Body.String())
		}
	}
	bindings, err := h.Store.ListInputBindings(t.Context(), "owner", "file-session")
	if err != nil {
		t.Fatal(err)
	}
	var got, want []string
	for _, binding := range bindings {
		if binding.MaterialID == "files" {
			got = append(got, binding.ResourceID)
		}
	}
	for _, ref := range refs {
		want = append(want, ref.(map[string]any)["resource_id"].(string))
	}
	if !reflect.DeepEqual(got, want) || len(bindings) != 15 {
		t.Fatalf("ordered/idempotent binding mismatch: got %v want %v total %d", got, want, len(bindings))
	}
}
