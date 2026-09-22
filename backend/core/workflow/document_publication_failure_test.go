package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestDocumentPublicationReportsConversionFailureBeforeWriting(t *testing.T) {
	for _, provider := range []string{"feishu", "notion", "github", "wechat", "obsidian"} {
		t.Run(provider, func(t *testing.T) {
			f, input := publicationFixture(t, false)
			input.Provider = provider
			conversions := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/document/providers":
					_ = json.NewEncoder(w).Encode(map[string]any{"providers": []any{map[string]any{
						"id": provider, "capabilities": []string{"create", "replace"}}}})
				case "/api/authservice/v1/cloud/connections/internal/chat-enabled":
					items := []any{}
					if provider != "obsidian" {
						items = append(items, map[string]any{"connection_id": "fixture-connection", "provider": provider, "owner_user_id": "owner", "status": "ACTIVE"})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
				case "/api/authservice/v1/cloud/connections/fixture-connection/token":
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": "fixture-connection", "provider": provider, "status": "ACTIVE", "access_token": "fixture-conversion-token"}})
				case "/api/document/actions:invoke":
					var request struct {
						Reference string `json:"reference"`
					}
					_ = json.NewDecoder(r.Body).Decode(&request)
					if request.Reference != "builtin:document.convert_document.v1" {
						t.Errorf("conversion failure must not call external write: %s", request.Reference)
					}
					conversions++
					w.WriteHeader(http.StatusUnprocessableEntity)
					_, _ = w.Write([]byte(`{"detail":{"code":"WORKFLOW_ACTION_INVALID","message":"private provider details"}}`))
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
			body := DocumentPublishRequest{Action: "publish_document", BaseRevision: &input.BaseRevision,
				BaseDraftVersion: input.BaseDraftVersion, Input: &DocumentPublishInput{Provider: provider, IdempotencyKey: input.IdempotencyKey}}
			result, op, err := PublishDocumentArtifact(t.Context(), f.db.DB, "owner", f.currentRevisionID, body, nil)
			if err == nil || op == nil || publicationFailure(err).code != "DOCUMENT_CONVERSION_FAILED" {
				t.Fatalf("failure=%v operation=%#v", err, op)
			}
			recorder := httptest.NewRecorder()
			replyPublicationResult(recorder, result, op, err)
			if !strings.Contains(recorder.Body.String(), "DOCUMENT_CONVERSION_FAILED") || strings.Contains(recorder.Body.String(), "private provider details") {
				t.Fatalf("public failure=%s", recorder.Body.String())
			}
			var stored orm.DocumentPublicationOperation
			mustPublicationErrorNil(t, f.db.First(&stored, "id = ?", op.ID).Error)
			if stored.Status != "failed_no_write" || stored.ErrorCode != "DOCUMENT_CONVERSION_FAILED" {
				t.Fatalf("durable status=%s code=%s", stored.Status, stored.ErrorCode)
			}
			_, _, replayErr := PublishDocumentArtifact(t.Context(), f.db.DB, "owner", f.currentRevisionID, body, nil)
			if replayErr == nil || publicationFailure(replayErr).code != stored.ErrorCode || conversions != 1 {
				t.Fatalf("replay=%v conversions=%d", replayErr, conversions)
			}
		})
	}
}
