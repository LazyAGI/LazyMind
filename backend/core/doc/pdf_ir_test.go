package doc

import (
	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPDFIRPermissionAndSourceChange(t *testing.T) {
	installOriginalSourceTransport(t)
	db := newDocumentTestDB(t)
	db.AutoMigrate(&orm.AsyncJob{})
	seedDocumentListDataset(t, db, "ds", "owner")
	seedDocumentListDoc(t, db, "ds", "doc", "paper.pdf", time.Now(), "owner", nil)
	p := filepath.Join(t.TempDir(), "paper.pdf")
	os.WriteFile(p, []byte("%PDF-one"), 0600)
	db.Model(&orm.Document{}).Where("id = ?", "doc").Update("ext", mustJSON(documentExt{StoredPath: p, OriginalFilename: "paper.pdf"}))
	hash, _ := pdfir.SourceHash(p)
	ir := pdfir.Document{SourceSHA256: hash, Version: 1}
	irpath := filepath.Join(t.TempDir(), "ir.json")
	os.WriteFile(irpath, mustJSON(ir), 0600)
	db.Create(&orm.AsyncJob{ID: "job", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("ds", "doc"), Status: "succeeded", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash}), ResultJSON: mustJSON(pdfIRResult{IRPath: irpath}), CreatedAt: time.Now()})
	call := func(user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-User-Id", user)
		r = mux.SetURLVars(r, map[string]string{"dataset": "ds", "document": "doc"})
		w := httptest.NewRecorder()
		GetPDFIR(w, r)
		return w
	}
	if w := call("stranger"); w.Code != http.StatusNotFound {
		t.Fatalf("permission leak: %d %s", w.Code, w.Body)
	}
	if w := call("owner"); !strings.Contains(w.Body.String(), `"status":"ready"`) {
		t.Fatalf("expected cached IR without credentials: %s", w.Body)
	}
	db.Model(&orm.AsyncJob{}).Where("id = ?", "job").Update("payload_json", mustJSON(pdfIRPayload{Revision: "old-parser", DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash}))
	if w := call("owner"); strings.Contains(w.Body.String(), `"status":"ready"`) {
		t.Fatal("obsolete parser revision returned")
	}
	db.Model(&orm.AsyncJob{}).Where("id = ?", "job").Update("payload_json", mustJSON(pdfIRPayload{Revision: pdfir.Revision, DatasetID: "ds", DocumentID: "doc", SourceSHA256: hash}))
	denied := httptest.NewRequest("POST", "/", strings.NewReader(`{"force":true}`))
	denied.Header.Set("X-User-Id", "stranger")
	denied = mux.SetURLVars(denied, map[string]string{"dataset": "ds", "document": "doc"})
	out := httptest.NewRecorder()
	CreatePDFIR(out, denied)
	if out.Code != http.StatusNotFound {
		t.Fatal("unauthorized parsing allowed")
	}
	os.WriteFile(p, []byte("%PDF-two"), 0600)
	if w := call("owner"); strings.Contains(w.Body.String(), `"status":"ready"`) {
		t.Fatal("stale source cache returned")
	}
}

func TestPDFIRWorkerRehydratesNondefaultTenantAndRejectsRevokedIdentity(t *testing.T) {
	db := newDocumentTestDB(t)
	seedDocumentListDataset(t, db, "tenant-dataset", "owner")
	db.Model(&orm.Dataset{}).Where("id = ?", "tenant-dataset").Update("tenant_id", "company")
	seedDocumentListDoc(t, db, "tenant-dataset", "tenant-doc", "paper.pdf", time.Now(), "owner", nil)
	path := filepath.Join(t.TempDir(), "paper.pdf")
	os.WriteFile(path, []byte("%PDF-original"), 0600)
	db.Model(&orm.Document{}).Where("id = ?", "tenant-doc").Update("ext", mustJSON(documentExt{StoredPath: path, OriginalFilename: "paper.pdf"}))
	tenant, status := "company", http.StatusOK
	identityCalls, scanCalls := 0, 0
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/authservice/user/owner/identity/internal":
			identityCalls++
			if r.Header.Get("X-LazyMind-Internal-Token") != "service-token" {
				t.Error("identity must use service authorization")
			}
			return testJSONResponse(status, `{"sub":"owner","tenant_id":"`+tenant+`","role":"user"}`), nil
		case "/api/scan/internal/source-access/by-dataset:batch":
			scanCalls++
			if r.Header.Get("X-Tenant-ID") != "company" {
				t.Error("scan authorization lost current tenant")
			}
			return testJSONResponse(200, `{"items":[{"dataset_id":"tenant-dataset","exists":false,"allowed":true}]}`), nil
		default:
			return testJSONResponse(404, `{}`), nil
		}
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "service-token")
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", "http://auth.test")
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	job := asyncjob.Job{CreateUserID: "owner", PayloadJSON: mustJSON(pdfIRPayload{DatasetID: "tenant-dataset", DocumentID: "tenant-doc", SourceSHA256: "changed", Revision: pdfir.Revision})}
	_, err := handlePDFIRJob(t.Context(), job, nil)
	if err == nil || err.Error() != "PDF source changed; request parsing again" || identityCalls != 1 || scanCalls != 1 {
		t.Fatalf("worker denied legitimate tenant before validating source: identity=%d scan=%d err=%v", identityCalls, scanCalls, err)
	}
	for _, test := range []struct {
		tenant string
		status int
	}{{"other", 200}, {"company", 403}} {
		tenant, status = test.tenant, test.status
		scanCalls = 0
		_, err = handlePDFIRJob(t.Context(), job, nil)
		if err == nil || err.Error() == "PDF source changed; request parsing again" || scanCalls != 0 {
			t.Fatalf("revoked/moved account reached PDF source: %v", err)
		}
	}
}

func TestPDFIRStatusReportsPermanentArtifactSizeFailure(t *testing.T) {
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	row := orm.Document{ID: "doc", DatasetID: "ds"}
	job := orm.AsyncJob{ID: "oversized", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("ds", "doc"), Status: "failed", ErrorCode: "pdf_ir_too_large", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, SourceSHA256: "hash"}), CreatedAt: time.Now()}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	state, err := pdfIRStatus(t.Context(), row, "hash")
	if err != nil || state.Status != "failed" || !strings.Contains(state.ErrorMessage, "smaller PDF") || strings.Contains(state.ErrorMessage, "Retry") {
		t.Fatalf("size failure must explain recovery rather than suggest identical retry: %+v %v", state, err)
	}
	// Older versions may already have recorded a successful oversized artifact.
	path := filepath.Join(t.TempDir(), "oversized.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate((32 << 20) + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := db.Model(&job).Updates(map[string]any{"status": "succeeded", "result_json": mustJSON(pdfIRResult{IRPath: path})}).Error; err != nil {
		t.Fatal(err)
	}
	state, err = pdfIRStatus(t.Context(), row, "hash")
	if err != nil || state.Status != "failed" || !strings.Contains(state.ErrorMessage, "smaller PDF") {
		t.Fatalf("legacy oversized artifact must report size failure: %+v %v", state, err)
	}
}
