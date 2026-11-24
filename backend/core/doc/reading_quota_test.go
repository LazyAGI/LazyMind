package doc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"lazymind/core/readingpolicy"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutomaticStudyCompletesOver100ItemsWithOneClaimAndCachedRetry(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&orm.UserModelProvider{ID: "p", Name: "OpenAI", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", BaseURL: "https://fixture.invalid/v1", APIKey: "fixture", IsVerified: true, CredentialRevision: 1, BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserModelProviderGroupModel{ID: "m", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "study", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "owner"}},
		&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "m"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	policy := readingpolicy.Defaults()
	policy.Preset = "full"
	policy.Limits.DailyStudyDocuments = 1
	if _, err := readingpolicy.Save(t.Context(), db.DB, "owner", policy); err != nil {
		t.Fatal(err)
	}
	seedDocumentListDataset(t, db, "ds", "owner")
	source := filepath.Join(t.TempDir(), "paper.pdf")
	os.WriteFile(source, []byte("%PDF-many-tables"), 0600)
	seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now().Add(2*time.Hour), "owner", mustJSON(documentExt{StoredPath: source, OriginalFilename: "paper.pdf"}))
	hash, _ := pdfir.SourceHash(source)
	ir := pdfir.Document{Version: 1, ProjectionVersion: pdfir.ProjectionVersion, SourceSHA256: hash, CoordinateSpace: "normalized_top_left"}
	for i := 0; i < 101; i++ {
		ir.Blocks = append(ir.Blocks, pdfir.Block{ID: fmt.Sprintf("t%d", i), Type: "table", Table: &pdfir.Table{Captions: []string{fmt.Sprintf("Table %d: measured result", i)}}})
	}
	irpath := filepath.Join(t.TempDir(), "ir.json")
	os.WriteFile(irpath, mustJSON(ir), 0600)
	if err := db.Create(&orm.AsyncJob{ID: "ir", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("ds", "doc"), Status: "succeeded", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash}), ResultJSON: mustJSON(pdfIRResult{IRPath: irpath}), CreatedAt: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
	payload := readingPreparationPayload{DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash, Scopes: []string{"table"}, Automatic: true, ItemLimit: 1}
	db.Create(&orm.AsyncJob{ID: "batch", JobType: ReadingPreparationJob, Status: "running", PayloadJSON: mustJSON(payload)})
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "identity/internal") {
			return testJSONResponse(200, `{"sub":"owner","tenant_id":"default","role":"user"}`), nil
		}
		if r.URL.Path == "/api/chat/llm-task:run" {
			calls++
			return testJSONResponse(200, `{"status":"succeeded","output":{"summary":"Measured results explained","observations":[]},"usage":{"model_calls":1,"task_type":"paper.visual.explain","task_version":"paper-reading-v2-grounded-findings"}}`), nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-token")
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth.test")
	run := func() (asyncjob.Result, error) {
		return runReadingPreparation(t.Context(), asyncjob.Job{ID: "batch", CreateUserID: "owner", PayloadJSON: mustJSON(payload)}, preparationTestReporter{})
	}
	result, err := run()
	if err != nil {
		t.Fatal(err)
	}
	var outcome map[string]int
	json.Unmarshal(result.ResultJSON, &outcome)
	if calls != 101 || outcome["study_items"] != 101 {
		t.Fatalf("item cap retained: calls=%d result=%s", calls, result.ResultJSON)
	}
	var claims int64
	db.Model(&orm.ReadingUsage{}).Count(&claims)
	if claims != 1 {
		t.Fatalf("claims=%d", claims)
	}
	policy.Preset = "custom"
	policy.Modes[readingpolicy.PDFParse] = readingpolicy.Disabled
	policy.Modes[readingpolicy.AIStudy] = readingpolicy.Automatic
	if _, err := readingpolicy.Save(t.Context(), db.DB, "owner", policy); err != nil {
		t.Fatal(err)
	}
	if _, err = run(); err != nil {
		t.Fatal(err)
	}
	if calls != 101 {
		t.Fatalf("cache not reused: %d", calls)
	}
	db.Model(&orm.ReadingUsage{}).Count(&claims)
	if claims != 1 {
		t.Fatalf("retry charged: %d", claims)
	}
	// A different paper is held before any model transport call, and the handler
	// returns a durable retry deferral instead of exhausting its attempt budget.
	db.Model(&orm.ReadingUsage{}).Where("source_sha256=?", hash).Update("source_sha256", "other-paper")
	os.RemoveAll(readingArtifactDir(source, "ds", "doc", "owner", "paper-reading"))
	result, err = run()
	if !errors.Is(err, readingpolicy.ErrQuotaExceeded) || result.Permanent || !result.DeferAttempt || result.ErrorCode != readingpolicy.QuotaErrorCode || !result.RetryAt.Equal(readingpolicy.NextDay(time.Now())) {
		t.Fatalf("quota not deferred: %+v %v", result, err)
	}
	if calls != 101 {
		t.Fatalf("quota reached upstream: %d", calls)
	}
	// Automatic study can wait for on-demand structure without spending quota
	// or becoming a permanent failed enrollment.
	os.Remove(irpath)
	result, err = run()
	if err == nil || result.Permanent || !result.DeferAttempt || result.ErrorCode != "reading_structure_required" {
		t.Fatalf("missing structure not resumable: %+v %v", result, err)
	}
}

func TestQuotaCounterDoesNotChargeManualOrCacheHit(t *testing.T) {
	db := newDocumentTestDB(t)
	db.AutoMigrate(&orm.AsyncJob{})
	db.Create(&orm.AsyncJob{ID: "manual"})
	ctx := context.WithValue(t.Context(), readingCallCounterKey{}, &readingCallCounter{jobID: "manual", user: "owner", hash: "h"})
	if err := recordReadingModelCall(ctx); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&orm.ReadingUsage{}).Count(&n)
	if n != 0 {
		t.Fatal("manual charged")
	}
}

func TestEnrolledAutomaticWorkSurvivesNewPolicyEpoch(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	db.AutoMigrate(&orm.AsyncJob{})
	seedDocumentListDataset(t, db, "ds", "owner")
	source := filepath.Join(t.TempDir(), "paper.pdf")
	os.WriteFile(source, []byte("%PDF-enrolled"), 0600)
	seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now(), "owner", mustJSON(documentExt{StoredPath: source}))
	hash, _ := pdfir.SourceHash(source)
	irpath := filepath.Join(t.TempDir(), "ir.json")
	os.WriteFile(irpath, mustJSON(pdfir.Document{Version: 1, SourceSHA256: hash, CoordinateSpace: "normalized_top_left"}), 0600)
	db.Create(&orm.AsyncJob{ID: "ir", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("ds", "doc"), Status: "succeeded", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, SourceSHA256: hash}), ResultJSON: mustJSON(pdfIRResult{IRPath: irpath})})
	payload := readingPreparationPayload{DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash, Scopes: []string{"table"}, Automatic: true}
	db.Create(&orm.AsyncJob{ID: "enrolled", JobType: ReadingPreparationJob, PayloadJSON: mustJSON(payload), CreateUserID: "owner", Status: "pending"})
	// Switching the parser mode creates a new enrollment epoch for new documents,
	// but this already enrolled job must keep its authorization.
	policy := readingpolicy.Defaults()
	policy.Preset = "full"
	if _, err := readingpolicy.Save(t.Context(), db.DB, "owner", policy); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "identity/internal") {
			return testJSONResponse(200, `{"sub":"owner","tenant_id":"default","role":"user"}`), nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-token")
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth.test")
	_, err := runReadingPreparation(t.Context(), asyncjob.Job{ID: "enrolled", CreateUserID: "owner", PayloadJSON: mustJSON(payload)}, preparationTestReporter{})
	if err != nil {
		t.Fatalf("existing enrollment revoked by new epoch: %v", err)
	}
}
