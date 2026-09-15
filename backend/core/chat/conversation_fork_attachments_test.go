package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
)

func TestForkLocalSourceRevalidationOnlyInspectsInheritedHistory(t *testing.T) {
	for _, tc := range []struct {
		name           string
		inherited      bool
		inheritedLocal bool
		available      bool
		serviceFailure bool
		wantRequests   int
	}{
		{name: "ordinary history", available: true},
		{name: "only ordinary history uses local files", inherited: true, available: true},
		{name: "inherited source available", inherited: true, inheritedLocal: true, available: true, wantRequests: 2},
		{name: "inherited source revoked", inherited: true, inheritedLocal: true, wantRequests: 1},
		{name: "inherited source lookup fails", inherited: true, inheritedLocal: true, serviceFailure: true, wantRequests: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
			previousClient := localFSScanHTTPClient
			t.Cleanup(func() { localFSScanHTTPClient = previousClient })
			requests := 0
			localFSScanHTTPClient = &http.Client{Transport: chatModelRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				status, body := http.StatusOK, `{"items":[],"total":0}`
				if tc.serviceFailure {
					status, body = http.StatusServiceUnavailable, "unavailable"
				} else if tc.available {
					switch r.URL.Path {
					case "/api/scan/sources":
						body = `{"items":[{"source_id":"source-1","status":"ACTIVE"}],"total":1}`
					case "/api/scan/sources/source-1":
						body = `{"bindings":[{"connector_type":"local_fs","target_ref":"/test/docs","status":"ACTIVE","chat_enabled":true,"include_extensions":["txt"]}]}`
					default:
						t.Errorf("unexpected scan request: %s", r.URL.Path)
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			ordinary := orm.ChatHistory{RawContent: "question", Content: "extracted context", Result: "ordinary answer <tool_result>retrieved fragment</tool_result>", RetrievalResult: json.RawMessage(`[{"content":"retrieved fragment"}]`), Ext: marshalChatHistoryExt(map[string]any{
				"conversation_config_snapshot": conversationConfigSnapshot{Version: 1, LocalFSSourceIDs: []string{"source-1"}},
			})}
			histories := []orm.ChatHistory{ordinary}
			if tc.inherited {
				inherited := ordinary
				var ids []string
				if tc.inheritedLocal {
					ids = []string{"source-1"}
				}
				inherited.Ext = marshalChatHistoryExt(map[string]any{
					"fork_read_only": true, "conversation_config_snapshot": conversationConfigSnapshot{Version: 1, LocalFSSourceIDs: ids},
				})
				histories = append(histories, inherited)
			}
			before, _ := json.Marshal(histories)
			projected, err := revalidateForkHistoryAttachments(context.Background(), nil, doc.DatasetCatalogCaller{UserID: "u1"}, histories)
			if requests != tc.wantRequests {
				t.Errorf("scan requests = %d, want %d", requests, tc.wantRequests)
			}
			if tc.serviceFailure {
				if err == nil {
					t.Fatal("scan failure silently accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(projected[0], ordinary) {
				t.Fatal("ordinary history changed")
			}
			if tc.inherited {
				got := projected[1]
				if tc.inheritedLocal && !tc.available {
					if got.Content != ordinary.RawContent || len(got.RetrievalResult) != 0 || strings.Contains(got.Result, "retrieved fragment") || !strings.Contains(got.Result, "ordinary answer") || !strings.Contains(string(got.Ext), `"fork_restricted_context":true`) {
						t.Fatalf("revoked source payload retained: %#v", got)
					}
				} else if got.Content != ordinary.Content || got.Result != ordinary.Result || string(got.RetrievalResult) != string(ordinary.RetrievalResult) {
					t.Fatal("available inherited context changed")
				}
			}
			after, _ := json.Marshal(histories)
			if string(before) != string(after) {
				t.Fatal("stored transcript mutated")
			}
		})
	}
}
