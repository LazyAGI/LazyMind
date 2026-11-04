package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestWriterGitHubWriteBackPreflightsOriginalIntent(t *testing.T) {
	for _, intent := range []string{"Publish the guide to https://github.com/fixture/docs/tree/release/guides", "Write a guide"} {
		t.Run(intent, func(t *testing.T) {
			f := newWriterCredentialFixture(t, "writeback", "github")
			newWriterCredentialSpy(t, f, 1, "") // Supply credentials through the existing auth boundary.
			if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "credential-session").Update("intent_context", string(credentialJSON(t, map[string]string{"text": intent}))).Error; err != nil {
				t.Fatal(err)
			}
			target := json.RawMessage(`{"adapter":"github","uri":"https://github.com/fixture/docs/tree/release/guides","title":"Draft","meta":{"create_pending":true,"owner":"fixture","repo":"docs","ref":"release","directory":"guides"}}`)
			checks, writes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/document/providers" {
					_ = json.NewEncoder(w).Encode(map[string]any{"providers": []any{map[string]any{"id": "github", "capabilities": []string{"create", "replace"}}}})
					return
				}
				if r.URL.Path != "/api/document/actions:invoke" {
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				var request struct {
					Reference string                     `json:"reference"`
					Phase     string                     `json:"phase"`
					Arguments map[string]json.RawMessage `json:"arguments"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				switch request.Reference {
				case "builtin:document.convert_document.v1":
					_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"provider": "github", "format": "markdown", "content": "# Draft", "source_document": map[string]any{"title": "Draft"}, "media_references": map[string]any{}}})
				case "builtin:document.publication_target.v1":
					checks++
					var parent string
					_ = json.Unmarshal(request.Arguments["parent_uri"], &parent)
					if request.Phase != "preview" || parent != intent {
						t.Errorf("preflight lost original intent: phase=%s parent=%q", request.Phase, parent)
					}
					if !strings.Contains(parent, "https://github.com/fixture/docs/tree/release/guides") {
						_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"state": "blocked", "code": "PUBLICATION_TARGET_UNAVAILABLE"}})
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"state": "ready", "target_document": target}})
				case "builtin:document.write_document.v1":
					writes++
					var op orm.DocumentPublicationOperation
					if err := f.db.First(&op, "session_id = ?", "credential-session").Error; err != nil {
						t.Error(err)
					}
					var submitted, persisted, expected any
					_ = json.Unmarshal(request.Arguments["target_document"], &submitted)
					_ = json.Unmarshal(op.TargetDocument, &persisted)
					_ = json.Unmarshal(target, &expected)
					if string(credentialJSON(t, submitted)) != string(credentialJSON(t, expected)) || string(credentialJSON(t, persisted)) != string(credentialJSON(t, expected)) {
						t.Error("write did not reuse the persisted preflight destination")
					}
					if request.Arguments["user_input"] != nil {
						t.Error("write must not rediscover a destination after preflight")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"success": true, "changed": true, "provider_synced": true, "provider": "github", "patch_result": map[string]any{"success": true}, "persisted_document": "# Draft", "representation": "markdown", "target_document": target, "write_result": map[string]any{"doc_id": "fixture-document"}}})
				default:
					t.Errorf("unexpected action: %s", request.Reference)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			response := f.call(t, t.Context(), writerCredentialOwner, f.body())
			if strings.Contains(intent, "https://github.com/") {
				if response.Code != 200 || writes != 1 {
					t.Fatalf("status=%d writes=%d body=%s", response.Code, writes, response.Body.String())
				}
			} else if response.Code != 422 || writes != 0 {
				t.Fatalf("missing destination reached write: status=%d writes=%d", response.Code, writes)
			}
			if checks != 1 {
				t.Fatalf("preflight calls=%d", checks)
			}
		})
	}
}
