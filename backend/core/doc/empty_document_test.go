package doc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestPrepareEmptyDocumentPreservesContentFailure(t *testing.T) {
	for _, tc := range []struct {
		name           string
		upstream, want int
		body, code     string
	}{
		{"empty", 422, 422, `{"detail":{"code":"EMPTY_DOCUMENT","message":"/private/token=secret"}}`, "EMPTY_DOCUMENT"},
		{"ocr", 422, 422, `{"detail":{"code":"OCR_REQUIRED"}}`, "OCR_REQUIRED"},
		{"timeout", 0, 502, "", "READER_SUBMIT_FAILED"},
		{"service", 503, 502, `{"detail":"/private/token=secret"}`, "READER_SUBMIT_FAILED"},
		{"unknown 422", 422, 502, `{"detail":{"code":"OTHER","message":"/private/token=secret"}}`, "READER_SUBMIT_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_RAG_ENABLED", "false")
			t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
			db := newDocumentTestDB(t)
			installOriginalSourceTransport(t)
			previous := http.DefaultTransport
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/internal/documents:prepare" {
					if tc.upstream == 0 {
						return nil, context.DeadlineExceeded
					}
					return testJSONResponse(tc.upstream, tc.body), nil
				}
				return previous.RoundTrip(r)
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			seedDocumentListDataset(t, db, "ds", "owner")
			source := filepath.Join(t.TempDir(), "blank.txt")
			if err := os.WriteFile(source, []byte(" \n\t "), 0600); err != nil {
				t.Fatal(err)
			}
			seedDocumentListDoc(t, db, "ds", "doc", "blank.txt", time.Now(), "owner", mustJSON(documentExt{StoredPath: source}))
			initial := newDocumentProcessingState("ds", "doc", time.Now())
			if err := db.Create(&initial).Error; err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/ensure-parsed", nil)
			r.Header.Set("X-User-Id", "owner")
			r = mux.SetURLVars(r, map[string]string{"dataset": "ds", "document": "doc"})
			w := httptest.NewRecorder()
			EnsureParsed(w, r)
			if w.Code != tc.want {
				t.Errorf("status=%d want=%d body=%s", w.Code, tc.want, w.Body)
			}
			if strings.Contains(w.Body.String(), "/private") || strings.Contains(w.Body.String(), "secret") {
				t.Errorf("unsafe response: %s", w.Body)
			}
			var state orm.DocumentProcessingState
			if err := db.Where("document_id = ?", "doc").Take(&state).Error; err != nil {
				t.Fatal(err)
			}
			if state.ParseStatus != "failed" || state.ParseErrorCode != tc.code {
				t.Errorf("wrong persisted failure: %+v", state)
			}
			if strings.Contains(state.ParseErrorMessage, "/private") {
				t.Error("persisted unsafe diagnostic")
			}
			if tc.want == 422 {
				var body EnsureDocumentParsedResult
				json.Unmarshal(w.Body.Bytes(), &body)
				if body.Status != "failed" || body.Error == nil || body.Error.Code != tc.code {
					t.Errorf("missing content diagnostic: %s", w.Body)
				}
			}
			r.Method = "GET"
			w = httptest.NewRecorder()
			EnsureParsed(w, r)
			var observed EnsureDocumentParsedResult
			json.Unmarshal(w.Body.Bytes(), &observed)
			if w.Code != 200 || observed.Status != "failed" || observed.Error == nil || observed.Error.Code != tc.code {
				t.Errorf("wrong observation: %d %s", w.Code, w.Body)
			}
			var tasks []orm.Task
			db.Find(&tasks)
			if len(tasks) != 1 {
				t.Fatalf("tasks=%d", len(tasks))
			}
			var ext taskExt
			json.Unmarshal(tasks[0].Ext, &ext)
			if ext.TaskState != string(TaskStateFailed) {
				t.Errorf("task not failed: %+v", ext)
			}
			if strings.Contains(ext.ErrorMessage, "/private") {
				t.Error("unsafe task error")
			}
			contents, err := os.ReadFile(source)
			if err != nil || string(contents) != " \n\t " {
				t.Error("original changed")
			}
		})
	}
}
