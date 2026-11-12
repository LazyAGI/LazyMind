package workflow

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"lazymind/core/common/orm"
	appLog "lazymind/core/log"
)

func TestDocumentPublicationReportsFailureReasons(t *testing.T) {
	for _, test := range []struct{ provider, code string }{
		{"feishu", "DOCUMENT_CONVERSION_FAILED"},
		{"notion", "DOCUMENT_CONVERSION_FAILED"},
		{"github", "DOCUMENT_CONVERSION_FAILED"},
		{"wechat", "DOCUMENT_CONVERSION_FAILED"},
		{"obsidian", "DOCUMENT_CONVERSION_FAILED"},
		{"wechat", "WECHAT_BODY_IMAGE_TOO_LARGE"},
		{"wechat", "WECHAT_IMAGE_FORMAT_UNSUPPORTED"},
		{"wechat", "WECHAT_HTML_TOO_LARGE"},
	} {
		t.Run(test.provider+"/"+test.code, func(t *testing.T) {
			provider := test.provider
			write := test.code != "DOCUMENT_CONVERSION_FAILED"
			var logs bytes.Buffer
			previousLogger := appLog.Logger
			appLog.Logger = zerolog.New(&logs)
			t.Cleanup(func() { appLog.Logger = previousLogger })
			f, input := publicationFixture(t, false)
			input.Provider = provider
			failures := 0
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
					if write && request.Reference == documentConvertReference {
						json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"provider": provider, "format": "html", "content": "<p>Draft</p>", "source_document": map[string]any{"title": "Draft"}, "media_references": map[string]any{}}})
						return
					}
					reference, code := documentConvertReference, "WORKFLOW_ACTION_INVALID"
					if write {
						reference, code = "builtin:document.write_document.v1", test.code
					}
					if request.Reference != reference {
						t.Errorf("unexpected action: %s", request.Reference)
					}
					failures++
					w.WriteHeader(http.StatusUnprocessableEntity)
					json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{"code": code, "message": "private provider details"}})
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
			if err == nil || op == nil || publicationFailure(err).code != test.code {
				t.Fatalf("failure=%v operation=%#v", err, op)
			}
			recorder := httptest.NewRecorder()
			replyPublicationResult(recorder, result, op, err)
			if !strings.Contains(recorder.Body.String(), test.code) || strings.Contains(recorder.Body.String(), "private provider details") {
				t.Fatalf("public failure=%s", recorder.Body.String())
			}
			var stored orm.DocumentPublicationOperation
			mustPublicationErrorNil(t, f.db.First(&stored, "id = ?", op.ID).Error)
			if stored.Status != "failed_no_write" || stored.ErrorCode != test.code {
				t.Fatalf("durable status=%s code=%s", stored.Status, stored.ErrorCode)
			}
			var event map[string]any
			if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
				t.Fatalf("missing publication failure log: %v", err)
			}
			if event["operation_id"] != stored.ID || event["session_id"] != stored.SessionID || event["artifact_id"] != stored.SourceRevisionID || event["error_code"] != stored.ErrorCode {
				t.Fatalf("failure log cannot identify the publication: %#v", event)
			}
			for _, private := range []string{"private provider details", "fixture-conversion-token", "# Draft"} {
				if strings.Contains(logs.String(), private) {
					t.Fatal("publication log contains private content")
				}
			}
			_, _, replayErr := PublishDocumentArtifact(t.Context(), f.db.DB, "owner", f.currentRevisionID, body, nil)
			if replayErr == nil || publicationFailure(replayErr).code != stored.ErrorCode || failures != 1 {
				t.Fatalf("replay=%v failures=%d", replayErr, failures)
			}
		})
	}
}
