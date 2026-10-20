package coreclient

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSyncRequestsCarryCreatorIdentityAndKeepLegacyFallback(t *testing.T) {
	for _, action := range []string{ActionCreate, ActionReparse} {
		for _, name := range []string{"admin", ""} {
			t.Run(action+"/"+name, func(t *testing.T) {
				userID := "11111111-1111-4111-8111-111111111111"
				expectedName := name
				if expectedName == "" {
					expectedName = userID
				}
				var uploadID, uploadName, taskID, taskName string
				client := newHTTPTestCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/datasets/kb-fixture/uploads":
						uploadID, uploadName = r.Header.Get("X-User-Id"), r.Header.Get("X-User-Name")
						writeJSON(t, w, http.StatusOK, map[string]any{"files": []map[string]string{{"upload_file_id": "upload-fixture"}}})
					case "/datasets/kb-fixture/tasks":
						taskID, taskName = r.Header.Get("X-User-Id"), r.Header.Get("X-User-Name")
						writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]string{{"task_id": "task-fixture", "document_id": "document-fixture"}}})
					case "/datasets/kb-fixture/tasks:start":
						writeJSON(t, w, http.StatusOK, map[string]any{"tasks": []map[string]any{{"task_id": "task-fixture", "status": "STARTED"}}})
					case "/datasets/kb-fixture/documents/old-document-fixture":
						if action != ActionReparse || r.Method != http.MethodDelete || r.Header.Get("X-User-Id") != userID {
							t.Error("unexpected replacement document deletion identity")
						}
						w.WriteHeader(http.StatusOK)
					default:
						t.Errorf("unexpected path: %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
					}
				}))
				_, err := client.SubmitParseTask(context.Background(), SubmitParseTaskRequest{
					IdempotencyKey: "sync-fixture", DatasetID: "kb-fixture", ParentDocumentID: "folder-fixture",
					SourceDocumentID: "old-document-fixture", UserID: userID, UserName: name,
					DisplayName: "fixture.md", Content: strings.NewReader("synthetic sync content"), Action: action,
				})
				if err != nil {
					t.Fatal(err)
				}
				if uploadID != userID || taskID != userID || uploadName != expectedName || taskName != expectedName {
					t.Fatalf("sync identity mismatch: upload=(%q,%q) task=(%q,%q), want=(%q,%q)", uploadID, uploadName, taskID, taskName, userID, expectedName)
				}
			})
		}
	}
}
