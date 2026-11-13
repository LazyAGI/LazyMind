package doc

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedPaperImportPreparation(t *testing.T, scene, level, preset string) (*orm.DB, orm.Dataset, orm.Document, orm.Task) {
	t.Helper()
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}, &orm.UserUIPreferences{}); err != nil {
		t.Fatal(err)
	}
	policy := readingpolicy.Defaults()
	policy.Preset = preset
	if preset == "custom" {
		policy.Modes[readingpolicy.ChatPrepare] = readingpolicy.Automatic
	}
	if _, err := readingpolicy.Save(context.Background(), db.DB, "owner", policy); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ds := orm.Dataset{ID: "ds", KbID: "ds", Scene: scene, ProcessingLevel: level, BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	source := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(source, []byte("%PDF-original"), 0600); err != nil {
		t.Fatal(err)
	}
	document := orm.Document{ID: "paper", DatasetID: "ds", DisplayName: "paper.pdf", Ext: mustJSON(documentExt{StoredPath: source, OriginalFilename: "paper.pdf"}), BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	task := orm.Task{ID: "import", DatasetID: "ds", DocID: "paper", TaskType: string(TaskTypeParseUploaded), Ext: mustJSON(taskExt{TaskState: string(TaskStateUploaded)}), BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}}
	state := newDocumentProcessingState(ds.ID, document.ID, now)
	for _, row := range []any{&ds, &document, &task, &state} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, ds, document, task
}
func TestPaperImportUsesOnlyBoundedReadingPreparation(t *testing.T) {
	for _, entry := range []string{"original_service", "page_upload"} {
		for _, level := range []string{ProcessingLevelParsed, ProcessingLevelChunked, ProcessingLevelIndexed} {
			for _, preset := range []string{"demand", "full", "custom"} {
				t.Run(entry+"/"+level+"/"+preset, func(t *testing.T) {
					t.Setenv("LAZYMIND_RAG_ENABLED", "true")
					db, ds, document, task := seedPaperImportPreparation(t, "academic_papers", level, preset)
					if entry == "original_service" {
						svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
						result := svc.SubmitOriginalProcessing(context.Background(), DatasetCatalogCaller{UserID: "owner"}, OriginalImportResult{Document: document, Task: task})
						if result.SubmissionStatus != "not_requested" || result.Start.Message != "document stored without parsing" {
							t.Fatalf("paper import submitted parsing: %+v", result)
						}
					} else {
						r := httptest.NewRequest("POST", "/tasks:start", nil)
						r.Header.Set("X-User-Id", "owner")
						result, err := startTasksInternal(r, ds.ID, []string{task.ID})
						if err != nil || len(result) != 1 || result[0].Message != "document stored without parsing" {
							t.Fatalf("page upload submitted parsing: %+v %v", result, err)
						}
					}
					var jobs int64
					db.Model(&orm.AsyncJob{}).Count(&jobs)
					if jobs != 0 {
						t.Fatal("legacy import bypassed bounded queue")
					}
					if err := enrollReadingPreparation(context.Background(), db.DB, nil); err != nil {
						t.Fatal(err)
					}
					db.Model(&orm.AsyncJob{}).Count(&jobs)
					want := int64(1)
					if preset == "demand" {
						want = 0
					}
					if jobs != want {
						t.Fatalf("scheduler jobs=%d want=%d", jobs, want)
					}
					if want == 1 {
						var job orm.AsyncJob
						db.Take(&job)
						if job.JobType != ReadingPreparationJob {
							t.Fatalf("unbounded queue %s", job.JobType)
						}
						var payload readingPreparationPayload
						json.Unmarshal(job.PayloadJSON, &payload)
						if !payload.Automatic {
							t.Fatal("automatic provenance missing")
						}
					}
					var saved orm.Document
					if err := db.Where("id=?", document.ID).Take(&saved).Error; err != nil {
						t.Fatal("import lost source document")
					}
				})
			}
		}
	}
}

func TestPaperOriginalImportAndUploadWithoutRAG(t *testing.T) {
	t.Setenv("LAZYMIND_RAG_ENABLED", "false")
	for _, entry := range []string{"original_service", "page_upload"} {
		t.Run(entry, func(t *testing.T) {
			db, ds, document, task := seedPaperImportPreparation(t, "academic_papers", ProcessingLevelIndexed, "demand")
			if entry == "original_service" {
				svc, _ := NewOriginalService(OriginalServiceDeps{DB: db.DB})
				result := svc.SubmitOriginalProcessing(context.Background(), DatasetCatalogCaller{UserID: "owner"}, OriginalImportResult{Document: document, Task: task})
				if result.SubmissionStatus != "not_requested" || result.Start.Message != "document stored without parsing" {
					t.Fatalf("paper original depended on RAG: %+v", result)
				}
			} else {
				r := httptest.NewRequest("POST", "/tasks:start", nil)
				r.Header.Set("X-User-Id", "owner")
				result, err := startTasksInternal(r, ds.ID, []string{task.ID})
				if err != nil || len(result) != 1 || result[0].Message != "document stored without parsing" {
					t.Fatalf("paper upload depended on RAG: %+v %v", result, err)
				}
			}
			var ext documentExt
			json.Unmarshal(document.Ext, &ext)
			if _, err := os.Stat(ext.StoredPath); err != nil {
				t.Fatal("original is no longer readable", err)
			}
			var jobs int64
			db.Model(&orm.AsyncJob{}).Count(&jobs)
			if jobs != 0 {
				t.Fatal("import unexpectedly enqueued parsing")
			}
		})
	}
}
func TestPaperExplicitEnsureStillSubmitsAfterStoreOnlyImport(t *testing.T) {
	installOriginalSourceTransport(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "true")
	db, ds, document, task := seedPaperImportPreparation(t, "academic_papers", ProcessingLevelParsed, "demand")
	if _, err := SubmitOriginalTask(context.Background(), DatasetCatalogCaller{UserID: "owner"}, ds.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/ensure-parsed", nil)
	r.Header.Set("X-User-Id", "owner")
	service := mustDocumentService(t, db)
	result, err := service.EnsureDocumentParsed(r, EnsureDocumentParsedRequest{UserID: "owner", DatasetID: ds.ID, DocumentID: document.ID, Caller: DatasetCatalogCaller{UserID: "owner"}})
	if err != nil || result.Status != "parsing" || result.TaskID == "" {
		t.Fatalf("explicit ensure lost: %+v %v", result, err)
	}
	var jobs int64
	db.Model(&orm.AsyncJob{}).Where("job_type=?", paperProcessingJob).Count(&jobs)
	if jobs != 1 {
		t.Fatalf("explicit ensure submissions=%d", jobs)
	}
}
func TestNonPaperOriginalImportStillUsesParsingControls(t *testing.T) {
	db, ds, _, task := seedPaperImportPreparation(t, "general", ProcessingLevelParsed, "demand")
	now := time.Now()
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{"user_id": "owner", "document_parsing_enabled": false, "created_at": now, "updated_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	_, err := SubmitOriginalTask(context.Background(), DatasetCatalogCaller{UserID: "owner"}, ds.ID, task.ID)
	if err == nil || !strings.Contains(err.Error(), "parsing is paused") {
		t.Fatalf("nonpaper pipeline skipped: %v", err)
	}
}

func TestLegacyQueuedPaperImportSkipsUnboundedParsing(t *testing.T) {
	installOriginalSourceTransport(t)
	t.Setenv("LAZYMIND_RAG_ENABLED", "false")
	db, ds, _, task := seedPaperImportPreparation(t, "academic_papers", ProcessingLevelParsed, "demand")
	db.Model(&orm.Dataset{}).Where("id=?", ds.ID).Update("tenant_id", "company")
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/internal/documents:prepare" {
			return nil, fmt.Errorf("native parser test boundary")
		}
		if r.URL.Path == "/api/authservice/user/owner/identity/internal" {
			return testJSONResponse(200, `{"sub":"owner","tenant_id":"company","role":"user"}`), nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-token")
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth.test")
	payload := paperProcessingPayload{DatasetID: ds.ID, TaskID: task.ID, UserID: "owner"}
	result, err := runPaperProcessing(t.Context(), asyncjob.Job{ID: "legacy-import", CreateUserID: "owner", PayloadJSON: mustJSON(payload)}, nil)
	if err != nil || !strings.Contains(string(result.ResultJSON), "not_requested") {
		t.Fatalf("legacy import still parsed: %+v %v", result, err)
	}
	var saved orm.Task
	db.Where("id=?", task.ID).Take(&saved)
	var ext taskExt
	json.Unmarshal(saved.Ext, &ext)
	if ext.TaskState != string(TaskStateSucceeded) {
		t.Fatalf("stored import marked failed: %s", ext.TaskState)
	}
	// A nonempty requested level belongs to explicit ensure or bounded automatic
	// chat preparation. It must still enter the parser path, not be skipped.
	payload.Level = ProcessingLevelParsed
	result, err = runPaperProcessing(t.Context(), asyncjob.Job{ID: "explicit-parse", CreateUserID: "owner", PayloadJSON: mustJSON(payload)}, nil)
	if err == nil || !strings.Contains(err.Error(), "native parser test boundary") || strings.Contains(string(result.ResultJSON), "not_requested") {
		t.Fatalf("explicit parse was skipped: %+v %v", result, err)
	}
}

func TestPaperExplicitReparseRetainedFilePreservesRequestedLevel(t *testing.T) {
	for _, level := range []string{ProcessingLevelParsed, ProcessingLevelChunked, ProcessingLevelIndexed} {
		t.Run(level, func(t *testing.T) {
			installOriginalSourceTransport(t)
			t.Setenv("LAZYMIND_RAG_ENABLED", "true")
			db, ds, document, task := seedPaperImportPreparation(t, "academic_papers", level, "demand")
			if level == ProcessingLevelIndexed {
				seedSelectedEmbeddingForDocTest(t, db, "owner")
			}
			if document.LazyllmDocID != "" {
				t.Fatal("fixture must exercise first submission of a retained file")
			}
			if err := db.Model(&orm.Task{}).Where("id=?", task.ID).Updates(map[string]any{"task_type": string(TaskTypeReparse), "ext": mustJSON(taskExt{TaskState: string(TaskStateCreating)})}).Error; err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/datasets/ds/tasks:start", strings.NewReader(`{"task_ids":["import"]}`))
			r.Header.Set("X-User-Id", "owner")
			r = mux.SetURLVars(r, map[string]string{"dataset": ds.ID})
			w := httptest.NewRecorder()
			StartTask(w, r)
			var response StartTasksResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.StartedCount != 1 {
				t.Fatalf("explicit reparse not accepted: %d %s %v", w.Code, w.Body, err)
			}
			var job orm.AsyncJob
			if err := db.Where("job_type=?", paperProcessingJob).Take(&job).Error; err != nil {
				t.Fatal(err)
			}
			var payload paperProcessingPayload
			if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Level != level || payload.Automatic {
				t.Fatalf("explicit reparse became a legacy import job: %+v", payload)
			}
		})
	}
}

func TestQueuedPaperPreparationRevalidatesReadPermissionAndClearsFailure(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(fmt.Sprint("revoked=", revoked), func(t *testing.T) {
			installOriginalSourceTransport(t)
			t.Setenv("LAZYMIND_RAG_ENABLED", "true")
			db, ds, document, task := seedPaperImportPreparation(t, "academic_papers", ProcessingLevelParsed, "demand")
			if err := db.AutoMigrate(&orm.ACLModel{}, &orm.ACLGroupModel{}, &orm.UserGroupModel{}, &orm.KBModel{}, &orm.VisibilityModel{}); err != nil {
				t.Fatal(err)
			}
			acl.InitStore(db)
			if _, err := SubmitOriginalTask(t.Context(), DatasetCatalogCaller{UserID: "owner"}, ds.ID, task.ID); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.ACLModel{ResourceType: acl.ResourceTypeDB, ResourceID: ds.ID, GranteeType: acl.GranteeUser, TargetID: "reader", Permission: acl.PermissionDatasetRead}).Error; err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/ensure-parsed", nil)
			r.Header.Set("X-User-Id", "reader")
			result, err := mustDocumentService(t, db).EnsureDocumentParsed(r, EnsureDocumentParsedRequest{UserID: "reader", DatasetID: ds.ID, DocumentID: document.ID, Caller: DatasetCatalogCaller{UserID: "reader"}})
			if err != nil || result.Status != "parsing" {
				t.Fatalf("reader enqueue: %+v %v", result, err)
			}
			var job orm.AsyncJob
			if err := db.Where("job_type=?", paperProcessingJob).Take(&job).Error; err != nil {
				t.Fatal(err)
			}
			// Copies reference the same source document but do not own its parse latch.
			if err := db.Create(&orm.Task{ID: "newer-copy", DatasetID: ds.ID, DocID: document.ID, TaskType: string(TaskTypeCopy), BaseModel: orm.BaseModel{CreatedAt: time.Now().Add(time.Minute)}}).Error; err != nil {
				t.Fatal(err)
			}
			if revoked {
				db.Where("target_id=?", "reader").Delete(&orm.ACLModel{})
			}
			previous := http.DefaultTransport
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/internal/documents:prepare" {
					return nil, fmt.Errorf("native parser test boundary")
				}
				if req.URL.Path == "/api/authservice/user/reader/identity/internal" {
					return testJSONResponse(200, `{"sub":"reader","tenant_id":"default","role":"user"}`), nil
				}
				return previous.RoundTrip(req)
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-token")
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth.test")
			// Bounded AI preparation still requires write, unlike document chat parsing.
			if _, err := preparationRequest(t.Context(), "reader", readingPreparationPayload{DatasetID: ds.ID}); err == nil {
				t.Fatal("read-only user gained AI preparation write permission")
			}
			// Stop at the controlled parser dependency boundary, without external calls.
			t.Setenv("LAZYMIND_RAG_ENABLED", "false")
			_, err = runPaperProcessing(t.Context(), asyncjob.Job{ID: job.ID, CreateUserID: job.CreateUserID, PayloadJSON: job.PayloadJSON}, nil)
			if err == nil {
				t.Fatal("expected execution boundary failure")
			}
			if revoked && !strings.Contains(err.Error(), "权限") {
				t.Fatalf("revoked read reached parser: %v", err)
			}
			if !revoked && !strings.Contains(err.Error(), "native parser test boundary") {
				t.Fatalf("read-only collaborator blocked before parser: %v", err)
			}
			var saved orm.Task
			db.Where("id=?", result.TaskID).Take(&saved)
			var ext taskExt
			json.Unmarshal(saved.Ext, &ext)
			var state orm.DocumentProcessingState
			db.Where("dataset_id=? AND document_id=?", ds.ID, document.ID).Take(&state)
			if ext.TaskState != string(TaskStateFailed) || state.ParseStatus != "failed" {
				t.Fatalf("failure left parsing latch: task=%s state=%s", ext.TaskState, state.ParseStatus)
			}
		})
	}
}
