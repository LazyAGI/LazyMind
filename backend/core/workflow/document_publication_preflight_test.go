package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
)

func TestObsidianPreflightDoesNotWriteAndEnforcesOwnership(t *testing.T) {
	f, input := publicationFixture(t, false)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Reference string `json:"reference"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch req.Reference {
		case documentConvertReference:
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"source_document": map[string]any{"title": "Draft"}, "media_references": map[string]any{}}})
		case "builtin:document.publication_target.v1":
			json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"state": "blocked", "code": "OBSIDIAN_TARGET_EXISTS", "vaults": []any{}}})
		default:
			t.Errorf("preflight attempted unexpected action %s", req.Reference)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	req := DocumentPublicationGroupRequest{BaseRevision: input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, Targets: []DocumentPublicationTargetInput{{Provider: "obsidian", ParentURI: "obsidian://fixture/"}}}
	_, err := PreflightDocumentPublication(t.Context(), f.db.DB, "stranger", f.currentRevisionID, req)
	requirePublicationError(t, err, "PUBLICATION_NOT_FOUND")
	if calls != 0 {
		t.Fatal("unowned artifact reached provider")
	}
	result, err := PreflightDocumentPublication(t.Context(), f.db.DB, "owner", f.currentRevisionID, req)
	mustPublicationErrorNil(t, err)
	if len(result.Targets) != 1 || result.Targets[0].Code != "OBSIDIAN_TARGET_EXISTS" || len(result.Targets[0].TargetDocument) != 0 {
		t.Fatalf("unexpected result %#v", result)
	}
	var count int64
	mustPublicationErrorNil(t, f.db.Model(&orm.DocumentPublicationOperation{}).Count(&count).Error)
	if count != 0 || calls != 2 {
		t.Fatalf("preflight created operations or repeated IO: count=%d calls=%d", count, calls)
	}
}

func TestObsidianPublicationReconcilesWithoutRepeatingWrite(t *testing.T) {
	for _, resolution := range []string{"confirmed", "absent", "unknown", "conflict"} {
		t.Run(resolution, func(t *testing.T) {
			f, input := publicationFixture(t, false)
			mustPublicationErrorNil(t, f.db.AutoMigrate(&orm.AsyncJob{}))
			writes, checks := 0, 0
			target := json.RawMessage(`{"adapter":"obsidian","uri":"obsidian://fixture/Draft.md","title":"Draft","meta":{"create_pending":true}}`)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/document/providers" {
					json.NewEncoder(w).Encode(map[string]any{"providers": []any{map[string]any{"id": "obsidian", "capabilities": []string{"create", "replace"}}}})
					return
				}
				var req struct {
					Reference string                     `json:"reference"`
					Arguments map[string]json.RawMessage `json:"arguments"`
				}
				json.NewDecoder(r.Body).Decode(&req)
				switch req.Reference {
				case documentConvertReference:
					json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"provider": "obsidian", "format": "markdown", "content": "# Draft", "source_document": map[string]any{"title": "Draft"}, "media_references": map[string]any{}}})
				case "builtin:document.publication_target.v1":
					json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"state": "ready", "target_document": target, "vaults": []any{}}})
				case "builtin:document.write_document.v1":
					writes++
					if len(req.Arguments["target_document"]) == 0 {
						t.Error("first write lost its prepared target")
					}
					if resolution == "conflict" {
						json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"success": false, "provider_synced": false, "patch_result": map[string]any{"success": false, "meta": map[string]any{"code": "OBSIDIAN_TARGET_CHANGED", "no_write": true}}}})
						return
					}
					w.WriteHeader(502) // Simulate lost provider response after the write claim.
				case "builtin:document.reconcile.v1":
					checks++
					json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"state": resolution, "target_document": json.RawMessage(`{"adapter":"obsidian","uri":"obsidian://fixture/Draft.md","title":"Draft","meta":{"publication_revision":"fixture-hash"}}`), "persisted_document": "# Draft"}})
				default:
					t.Errorf("unexpected action %s", req.Reference)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			request := DocumentPublicationGroupRequest{BaseRevision: input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, IdempotencyKey: "first-obsidian", Targets: []DocumentPublicationTargetInput{{Provider: "obsidian", ParentURI: "obsidian://fixture/"}}}
			id, err := CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
			mustPublicationErrorNil(t, err)
			var op DocumentPublicationOperation
			mustPublicationErrorNil(t, f.db.First(&op, "publication_id = ?", id).Error)
			_, err = runDocumentPublicationJob(t.Context(), f.db.DB, "owner", op.ID)
			mustPublicationErrorNil(t, err)
			if resolution == "conflict" {
				requirePublicationStatus(t, f, op.ID, "failed_no_write")
				return
			}
			requirePublicationStatus(t, f, op.ID, "outcome_unknown")
			recovered, err := RecoverDocumentPublication(t.Context(), f.db.DB, "owner", op.ID, DocumentPublicationRecoveryRequest{Action: "check"})
			mustPublicationErrorNil(t, err)
			expected := "outcome_unknown"
			if resolution == "confirmed" {
				expected = "succeeded"
			}
			if recovered.Status != expected || writes != 1 || checks != 1 {
				t.Fatalf("status=%s writes=%d checks=%d", recovered.Status, writes, checks)
			}
			if expected == "outcome_unknown" {
				request.IdempotencyKey = "new-confirmation"
				_, err = CreateDocumentPublicationGroup(t.Context(), f.db.DB, "owner", f.currentRevisionID, request)
				requirePublicationError(t, err, "PUBLICATION_IN_PROGRESS")
			}
		})
	}
}
