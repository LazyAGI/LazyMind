package main

import (
	"net/http"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestDocumentPublicationGroupHTTPAcceptsOnceWithoutWritingInRequest(t *testing.T) {
	f, remote := newDeliveryFixture(t, "markdown")
	if err := f.db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "descriptor-session").Updates(&orm.WorkflowSession{WorkflowID: "writer-workflow"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "descriptor-artifact").Updates(map[string]any{"slot_id": "draft_document", "slot": "draft_document"}).Error; err != nil {
		t.Fatal(err)
	}
	path := "/workflow-artifacts/descriptor-artifact/publications"
	body := map[string]any{"base_revision": 3, "base_draft_version": 7, "idempotency_key": "http-confirmation", "targets": []any{map[string]any{"provider": "feishu", "connection_id": "fixture-account"}}}
	for range 2 {
		response := deliveryRequest(t.Context(), f, http.MethodPost, path, "descriptor-owner", body)
		data := rewriteData(t, response)
		if data["publication_id"] == "" || len(data["targets"].([]any)) != 6 {
			t.Fatalf("missing per-platform state: %#v", data)
		}
		for _, private := range []string{"# Original", "source_value", "request_hash", "receipt_json"} {
			if strings.Contains(response.Body.String(), private) {
				t.Fatalf("response leaks %s", private)
			}
		}
	}
	var jobs int64
	if err := f.db.Model(&orm.AsyncJob{}).Count(&jobs).Error; err != nil || jobs != 1 {
		t.Fatalf("jobs=%d error=%v", jobs, err)
	}
	if len(remote.allCalls()) != 0 {
		t.Fatal("HTTP acceptance performed an external write")
	}
	rewriteError(t, deliveryRequest(t.Context(), f, http.MethodGet, path, "other-owner", nil), 404, "PUBLICATION_NOT_FOUND")
	rewriteError(t, deliveryRequest(t.Context(), f, http.MethodPost, path, "other-owner", body), 404, "PUBLICATION_NOT_FOUND")
	body["source_value"] = "# Client supplied historical body"
	rewriteError(t, deliveryRequest(t.Context(), f, http.MethodPost, path, "descriptor-owner", body), 400, "DOCUMENT_ACTION_INVALID")
}
