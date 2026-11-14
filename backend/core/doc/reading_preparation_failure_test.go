package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

type preparationTestReporter struct{}

func (preparationTestReporter) SetProgress(context.Context, int64, int64) error { return nil }
func (preparationTestReporter) Heartbeat(context.Context) error                 { return nil }

func TestReadingPreparationModelFailureIsSafeAndScopeSpecific(t *testing.T) {
	for _, scope := range []string{"formula", "figure", "table"} {
		t.Run(scope, func(t *testing.T) {
			installOriginalSourceTransport(t)
			db := newDocumentTestDB(t)
			if err := db.AutoMigrate(&orm.AsyncJob{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}); err != nil {
				t.Fatal(err)
			}
			for _, row := range []any{
				&orm.UserModelProvider{ID: "p", Name: "OpenAI", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
				&orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-key", IsVerified: true, CredentialRevision: 1, BaseModel: orm.BaseModel{CreateUserID: "owner"}},
				&orm.UserModelProviderGroupModel{ID: "m", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "study", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
				&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "m"},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			seedDocumentListDataset(t, db, "ds", "owner")
			source := filepath.Join(t.TempDir(), "paper.pdf")
			if err := os.WriteFile(source, []byte("%PDF-batch-timeout"), 0600); err != nil {
				t.Fatal(err)
			}
			seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now(), "owner", mustJSON(documentExt{StoredPath: source, OriginalFilename: "paper.pdf"}))
			hash, err := pdfir.SourceHash(source)
			if err != nil {
				t.Fatal(err)
			}
			blocks := map[string]pdfir.Block{
				"formula": {ID: "f", Type: "formula", Latex: "x^2", Text: "$$x^2$$"},
				"figure":  {ID: "v", Type: "image", Image: &pdfir.Image{Captions: []string{"Figure 1"}}},
				"table":   {ID: "t", Type: "table", Table: &pdfir.Table{Captions: []string{"Table 1"}}},
			}
			ir := pdfir.Document{Version: 1, SourceSHA256: hash, CoordinateSpace: "normalized_top_left", Blocks: []pdfir.Block{blocks[scope]}}
			for _, other := range []string{"formula", "figure", "table"} {
				if other != scope {
					ir.Blocks = append(ir.Blocks, blocks[other])
				}
			}
			irPath := filepath.Join(t.TempDir(), "ir.json")
			if err := os.WriteFile(irPath, mustJSON(ir), 0600); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.AsyncJob{ID: "ir", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("ds", "doc"), Status: "succeeded", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash}), ResultJSON: mustJSON(pdfIRResult{IRPath: irPath}), CreatedAt: time.Now()}).Error; err != nil {
				t.Fatal(err)
			}
			payload := readingPreparationPayload{DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash, Scopes: []string{"formula", "figure", "table"}, ItemLimit: 10}
			if err := db.Create(&orm.AsyncJob{ID: "batch", JobType: ReadingPreparationJob, Status: "running", PayloadJSON: mustJSON(payload)}).Error; err != nil {
				t.Fatal(err)
			}
			previous := http.DefaultTransport
			calls := 0
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "identity/internal") {
					return testJSONResponse(200, `{"sub":"owner","tenant_id":"default","role":"user"}`), nil
				}
				if r.URL.Path == "/api/chat/llm-task:run" {
					calls++
					return testJSONResponse(504, `{"detail":{"error_code":"request_timeout","message":"secret-provider-token","usage":{"model_calls":1}}}`), nil
				}
				return previous.RoundTrip(r)
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-token")
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth.test")
			result, err := runReadingPreparation(t.Context(), asyncjob.Job{ID: "batch", CreateUserID: "owner", PayloadJSON: mustJSON(payload)}, preparationTestReporter{})
			wantCalls := 1
			if scope == "formula" {
				wantCalls = 2
			} // Formula generation has one bounded retry.
			var taskErr *algo.StructuredTaskError
			if calls != wantCalls || !errors.As(err, &taskErr) || taskErr.Code != "request_timeout" {
				t.Fatalf("did not reach timeout boundary: calls=%d error=%v", calls, err)
			}
			if !strings.Contains(err.Error(), "超时") || !strings.Contains(err.Error(), "重试") || !strings.Contains(err.Error(), "模型") || strings.Contains(err.Error(), "request_timeout") || strings.Contains(err.Error(), "secret") {
				t.Errorf("unsafe or unhelpful batch error: %v", err)
			}
			var details struct {
				FailedScope   string `json:"failed_scope"`
				ModelRequests int    `json:"model_requests"`
			}
			if e := json.Unmarshal(result.ResultJSON, &details); e != nil {
				t.Fatal(e)
			}
			if details.FailedScope != scope || details.ModelRequests != wantCalls {
				t.Errorf("lost failure scope or accounting: %s", result.ResultJSON)
			}
			// Exercise the real failure finalizer: returning ResultJSON alone is
			// insufficient unless the runner durably saves it alongside the error.
			queued, enqueueErr := asyncjob.Enqueue(t.Context(), db.DB, asyncjob.EnqueueRequest{
				JobType: ReadingPreparationJob, ResourceType: "reading_manual", ResourceID: "ds/doc",
				IdempotencyKey: "runner-timeout", Payload: payload, CreateUserID: "owner", MaxAttempts: 1,
			})
			if enqueueErr != nil {
				t.Fatal(enqueueErr)
			}
			runnerCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			runner := asyncjob.Start(runnerCtx, db.DB, asyncjob.Options{
				JobTypes: []string{ReadingPreparationJob}, WorkerID: "scope-failure-test",
				Concurrency: 1, PollInterval: 5 * time.Millisecond, LockTTL: time.Minute,
			})
			t.Cleanup(func() {
				cancel()
				select {
				case <-runner.Done():
				case <-time.After(5 * time.Second):
					t.Error("preparation runner did not stop")
				}
			})
			var job orm.AsyncJob
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for {
				if e := db.Where("id = ?", queued.ID).Take(&job).Error; e != nil {
					t.Fatal(e)
				}
				if job.Status == string(asyncjob.StatusFailed) {
					break
				}
				select {
				case <-runnerCtx.Done():
					t.Fatalf("preparation did not finalize: status=%s error=%s", job.Status, job.ErrorMessage)
				case <-ticker.C:
				}
			}
			cancel()
			select {
			case <-runner.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("preparation runner did not stop after failure")
			}
			if job.ErrorMessage != err.Error() || job.LockedBy != "" || job.LockUntil != nil || job.FinishedAt == nil {
				t.Fatalf("wrong persisted failure: %+v", job)
			}
			details.FailedScope, details.ModelRequests = "", 0
			if e := json.Unmarshal(job.ResultJSON, &details); e != nil {
				t.Fatal(e)
			}
			if details.FailedScope != scope || details.ModelRequests != wantCalls || calls != 2*wantCalls {
				t.Errorf("runner lost scope/accounting: result=%s calls=%d", job.ResultJSON, calls)
			}
			for _, capability := range []string{"formula", "figure", "table"} {
				c := readingCapabilityStatus{ID: capability, Total: 1}
				finalizeReadingCapability(&c, true, []orm.AsyncJob{job})
				if capability == scope {
					if c.Status != "failed" || c.ErrorMessage != err.Error() {
						t.Errorf("lost targeted failure: %+v", c)
					}
				} else if c.Status != "not_requested" || c.ErrorMessage != "" {
					t.Errorf("unstarted scope incorrectly failed: %+v", c)
				}
			}
		})
	}
}

func TestReadingPreparationErrorPreservesCauseWithoutLeakingIt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cause error
		want  string
	}{
		{"deadline", fmt.Errorf("secret endpoint: %w", context.DeadlineExceeded), "超时"},
		{"structured deadline", fmt.Errorf("secret token: %w", &algo.StructuredTaskError{Code: "request_timeout"}), "超时"},
		{"canceled", fmt.Errorf("secret endpoint: %w", context.Canceled), "中断"},
		{"provider", errors.New("secret token=/private/model.json"), "生成失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := readingPreparationModelError("formula", tc.cause)
			if !errors.Is(err, tc.cause) || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/private") {
				t.Fatalf("wrong public error or cause: %v", err)
			}
			if strings.Contains(tc.name, "deadline") && (!strings.Contains(err.Error(), "重试") || !strings.Contains(err.Error(), "更换研读模型")) {
				t.Fatalf("timeout has no recovery: %v", err)
			}
		})
	}
}

func TestReadingPreparationScopedFailurePreservesCompletedAndPartialWork(t *testing.T) {
	for _, tc := range []struct {
		name, scope   string
		completed     int
		want, message string
	}{
		{"failed scope", "formula", 0, "failed", "safe timeout"},
		{"failed partial scope", "formula", 1, "partial", "safe timeout"},
		{"completed cache beats failed scope", "formula", 2, "succeeded", ""},
		{"other scope partial cache", "figure", 1, "partial", ""},
		{"other scope all cached", "figure", 2, "succeeded", ""},
		{"other scope not started", "figure", 0, "not_requested", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := readingCapabilityStatus{ID: tc.scope, Total: 2, Completed: tc.completed}
			job := orm.AsyncJob{Status: "failed", ErrorMessage: "safe timeout", PayloadJSON: mustJSON(readingPreparationPayload{Scopes: []string{"formula", "figure", "table"}}), ResultJSON: json.RawMessage(`{"failed_scope":"formula","model_requests":1}`)}
			finalizeReadingCapability(&c, true, []orm.AsyncJob{job})
			if c.Status != tc.want || c.ErrorMessage != tc.message {
				t.Fatalf("wrong scope outcome: %+v", c)
			}
		})
	}
}
