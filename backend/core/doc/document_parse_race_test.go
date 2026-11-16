package doc

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

// The transport schedules the newer parse to completion before delivering the
// older failure, without sleeps or goroutines. All persistence uses temp SQLite.
func TestLateParseFailurePreservesNewerSuccess(t *testing.T) {
	for _, revision := range []int64{1, 2} {
		for _, status := range []int{422, 503} {
			t.Run(fmt.Sprintf("revision_%d_status_%d", revision, status), func(t *testing.T) {
				t.Setenv("LAZYMIND_RAG_ENABLED", "false")
				t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
				db := newDocumentTestDB(t)
				installOriginalSourceTransport(t)
				seedDocumentListDataset(t, db, "ds", "owner")
				source := filepath.Join(t.TempDir(), "document.txt")
				contents := []byte("New successfully parsed content")
				if err := os.WriteFile(source, contents, 0600); err != nil {
					t.Fatal(err)
				}
				seedDocumentListDoc(t, db, "ds", "doc", "document.txt", time.Now(), "owner", mustJSON(documentExt{StoredPath: source}))
				initial := newDocumentProcessingState("ds", "doc", time.Now())
				if err := db.Create(&initial).Error; err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest("POST", "/ensure-parsed", nil)
				r.Header.Set("X-User-Id", "owner")
				r = mux.SetURLVars(r, map[string]string{"dataset": "ds", "document": "doc"})
				artifact := source + baseArtifactSuffix
				hash := fmt.Sprintf("%x", sha256.Sum256(contents))
				previous := http.DefaultTransport
				prepareCalls := 0
				http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.Path != "/internal/documents:prepare" {
						return previous.RoundTrip(request)
					}
					prepareCalls++
					if prepareCalls == 1 {
						// A manual reparse can supersede the in-flight on-demand task.
						if err := db.Model(&orm.DocumentProcessingState{}).Where("document_id = ?", "doc").Update("revision", revision).Error; err != nil {
							t.Fatal(err)
						}
						newer := orm.Task{ID: "newer-task", DatasetID: "ds", DocID: "doc", TaskType: string(TaskTypeReparse), Ext: mustJSON(taskExt{TaskState: string(TaskStateCreating)}), BaseModel: orm.BaseModel{CreatedAt: time.Now().Add(time.Second)}}
						if err := db.Create(&newer).Error; err != nil {
							t.Fatal(err)
						}
						if err := prepareBaseDocument(r, "ds", newer); err != nil {
							t.Fatal(err)
						}
						return testJSONResponse(status, `{"detail":{"code":"EMPTY_DOCUMENT"}}`), nil
					}
					if err := os.WriteFile(artifact, mustJSON(baseArtifact{SourceSHA: hash, Roots: []baseNode{{ID: "root", Text: string(contents)}}}), 0600); err != nil {
						t.Fatal(err)
					}
					return testJSONResponse(200, fmt.Sprintf(`{"artifact_ref":%q,"source_sha256":%q,"parser_version":"test","chunker_version":"test"}`, artifact, hash)), nil
				})
				t.Cleanup(func() { http.DefaultTransport = previous })
				w := httptest.NewRecorder()
				EnsureParsed(w, r)
				if prepareCalls != 2 {
					t.Fatalf("prepare calls=%d", prepareCalls)
				}
				if w.Code != status && !(status == 503 && w.Code == 502) {
					t.Fatalf("old request status=%d: %s", w.Code, w.Body)
				}
				var state orm.DocumentProcessingState
				if err := db.Where("document_id = ?", "doc").Take(&state).Error; err != nil {
					t.Fatal(err)
				}
				if state.ParseStatus != "succeeded" || state.ParseErrorCode != "" || state.ParseErrorMessage != "" || state.ParseArtifactRef != artifact || state.Revision != revision {
					t.Errorf("late failure overwrote newer successful parse: %+v", state)
				}
				r.Method = http.MethodGet
				w = httptest.NewRecorder()
				EnsureParsed(w, r)
				var observed EnsureDocumentParsedResult
				if err := json.Unmarshal(w.Body.Bytes(), &observed); err != nil {
					t.Fatal(err)
				}
				if w.Code != 200 || observed.Status != "parsed" || observed.Error != nil {
					t.Errorf("current parse unavailable: %d %s", w.Code, w.Body)
				}
			})
		}
	}
}
