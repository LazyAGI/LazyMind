package doc

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
	"lazymind/core/readingpolicy"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func guideHTTPFixture(t *testing.T) (*orm.DB, string, string) {
	t.Helper()
	installOriginalSourceTransport(t)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-internal")
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/identity/internal") {
			parts := strings.Split(r.URL.Path, "/")
			user := parts[len(parts)-3]
			return testJSONResponse(200, `{"sub":"`+user+`","tenant_id":"default","role":"user"}`), nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	db := newDocumentTestDB(t)
	if e := db.AutoMigrate(&orm.AsyncJob{}, &orm.PaperGuide{}, &orm.PaperGuideRevision{}, &orm.PaperGuideStep{}, &orm.PaperGuideProgress{}, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{}); e != nil {
		t.Fatal(e)
	}
	seedDocumentListDataset(t, db, "guide-ds", "owner")
	db.Model(&orm.Dataset{}).Where("id = ?", "guide-ds").Update("scene", "academic_papers")
	seedDocumentListDoc(t, db, "guide-ds", "guide-doc", "paper.pdf", time.Now(), "owner", nil)
	source := filepath.Join(t.TempDir(), "paper.pdf")
	if e := os.WriteFile(source, []byte("%PDF-guide-source"), 0600); e != nil {
		t.Fatal(e)
	}
	db.Model(&orm.Document{}).Where("id = ?", "guide-doc").Update("ext", mustJSON(documentExt{StoredPath: source, OriginalFilename: "paper.pdf"}))
	hash, _ := pdfir.SourceHash(source)
	rawArchive, ir := guidePrecisionFixture(t, hash)
	path := filepath.Join(t.TempDir(), "ir.json")
	if e := os.WriteFile(path, mustJSON(ir), 0600); e != nil {
		t.Fatal(e)
	}
	rawPath := filepath.Join(filepath.Dir(path), "mineru-result.zip")
	os.WriteFile(rawPath, rawArchive, 0600)
	db.Create(&orm.AsyncJob{ID: "guide-ir", JobType: pdfIRJobType, ResourceType: "document_pdf_ir", ResourceID: pdfIRResource("guide-ds", "guide-doc"), Status: "succeeded", PayloadJSON: mustJSON(pdfIRPayload{Revision: pdfir.Revision, DatasetID: "guide-ds", DocumentID: "guide-doc", SourceSHA256: hash}), ResultJSON: mustJSON(pdfIRResult{IRPath: path, RawPath: rawPath}), CreatedAt: time.Now()})
	for _, row := range []any{&orm.UserModelProvider{ID: "p", Name: "OpenAI", BaseModel: orm.BaseModel{CreateUserID: "owner"}}, &orm.UserModelProviderGroup{ID: "g", UserModelProviderID: "p", BaseURL: "https://fixture.invalid/v1", APIKey: "secret-do-not-persist", IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "owner"}}, &orm.UserModelProviderGroupModel{ID: "a", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "first", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "owner"}}, &orm.UserModelProviderGroupModel{ID: "b", UserModelProviderID: "p", UserModelProviderGroupID: "g", ProviderName: "OpenAI", Name: "second", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "owner"}}, &orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "a"}} {
		if e := db.Create(row).Error; e != nil {
			t.Fatal(e)
		}
	}
	return db, hash, source
}
func guideRequest(method, body, user string, vars map[string]string) *http.Request {
	r := httptest.NewRequest(method, "/", strings.NewReader(body))
	r.Header.Set("X-User-Id", user)
	if vars == nil {
		vars = map[string]string{}
	}
	vars["dataset"] = "guide-ds"
	vars["document"] = "guide-doc"
	return mux.SetURLVars(r, vars)
}
func TestPaperGuideHTTPGateReuseModelOverrideAndACL(t *testing.T) {
	db, hash, _ := guideHTTPFixture(t)
	req := `{"language":"zh","expected_source_hash":"` + hash + `","model_ref":"own:b","idempotency_key":"one"}`
	call := func(user string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		GeneratePaperGuide(w, guideRequest("POST", req, user, nil))
		return w
	}
	if w := call("owner"); w.Code != 403 {
		t.Fatalf("default gate: %d %s", w.Code, w.Body)
	}
	t.Setenv("LAZYMIND_PAPER_GUIDE_ENABLED", "true")
	w := call("owner")
	if w.Code != 202 {
		t.Fatalf("generate: %d %s", w.Code, w.Body)
	}
	var envelope struct {
		Data paperguide.RevisionView `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	if envelope.Data.Model.Ref != "own:b" {
		t.Fatal("ignored per-run model")
	}
	p, e := readingpolicy.Load(t.Context(), db.DB, "owner")
	if e != nil || p.StudyModelRef != "" {
		t.Fatal("changed global preferences")
	}
	var revision orm.PaperGuideRevision
	db.Where("id = ?", envelope.Data.ID).Take(&revision)
	if strings.Contains(revision.ModelJSON, "secret") || strings.Contains(w.Body.String(), "fixture.invalid") {
		t.Fatal("secret or endpoint leaked")
	}
	t.Setenv("LAZYMIND_PAPER_GUIDE_ENABLED", "false")
	if w = call("owner"); w.Code != 200 {
		t.Fatalf("disabled cached guide unavailable: %d %s", w.Code, w.Body)
	}
	if w = call("stranger"); w.Code != 404 {
		t.Fatalf("cross-owner access: %d %s", w.Code, w.Body)
	}
	get := httptest.NewRecorder()
	GetPaperGuide(get, guideRequest("GET", "", "owner", nil))
	if get.Code != 200 || get.Header().Get("ETag") == "" {
		t.Fatalf("status %d %s", get.Code, get.Body)
	}
	r := guideRequest("GET", "", "owner", nil)
	r.Header.Set("If-None-Match", get.Header().Get("ETag"))
	get = httptest.NewRecorder()
	GetPaperGuide(get, r)
	if get.Code != 304 {
		t.Fatal("ETag not honored")
	}
	oldETag := r.Header.Get("If-None-Match")
	db.Model(&orm.PaperGuideRevision{}).Where("id = ?", revision.ID).Update("calls_used", 3)
	changed := httptest.NewRecorder()
	GetPaperGuide(changed, r)
	if changed.Code != 200 || changed.Header().Get("ETag") == oldETag {
		t.Fatal("ETag hid a changed request budget")
	}
	db.Model(&orm.Document{}).Where("id = ?", "guide-doc").Update("deleted_at", time.Now())
	get = httptest.NewRecorder()
	GetPaperGuide(get, guideRequest("GET", "", "owner", nil))
	if get.Code != 404 {
		t.Fatal("deleted document readable")
	}
}
func TestPaperGuideReadOnlyMissingIRDoesNotEnqueueParse(t *testing.T) {
	db, _, _ := guideHTTPFixture(t)
	scope := paperguide.Scope{OwnerID: "owner", DatasetID: "guide-ds", DocumentID: "guide-doc", Language: "zh"}
	state, e := (paperGuideSource{}).Load(t.Context(), scope, false)
	if e != nil || state.Snapshot == nil {
		t.Fatalf("source: %v", e)
	}
	db.Delete(&orm.AsyncJob{}, "id = ?", "guide-ir")
	state, e = (paperGuideSource{}).Load(t.Context(), scope, false)
	if e != nil || state.Snapshot != nil {
		t.Fatal("missing IR should be read-only")
	}
	var n int64
	db.Model(&orm.AsyncJob{}).Count(&n)
	if n != 0 {
		t.Fatal("read triggered a parse")
	}
}

func TestPaperGuideModelFreezeIgnoresKeysButTracksProtocolAndSettings(t *testing.T) {
	db, _, _ := guideHTTPFixture(t)
	scope := paperguide.Scope{OwnerID: "owner", DatasetID: "guide-ds", DocumentID: "guide-doc", Language: "zh"}
	first, _, e := resolvePaperGuideModel(t.Context(), scope, "own:b")
	if e != nil {
		t.Fatal(e)
	}
	db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "g").Updates(map[string]any{"api_key": "rotated-secret", "credential_revision": 2})
	second, _, e := resolvePaperGuideModel(t.Context(), scope, "own:b")
	if e != nil || first != second {
		t.Fatal("key rotation changed frozen model")
	}
	db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "g").Update("base_url", "https://changed.invalid/v1")
	changed, _, e := resolvePaperGuideModel(t.Context(), scope, "own:b")
	if e != nil || changed.ParametersHash == first.ParametersHash {
		t.Fatal("endpoint change silently resumed")
	}
	safe := safeGuideModelConfig(map[string]any{"llm": map[string]any{"api_key": "secret", "source": "openai_responses", "base_url": "https://user:password@example.invalid/v1?api_key=secret&version=1", "advanced": map[string]any{"Authorization": "Bearer secret", "max_tokens": 512}}})
	raw, _ := json.Marshal(safe)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "password") || !strings.Contains(string(raw), "max_tokens") {
		t.Fatalf("unsafe/incorrect config projection %s", raw)
	}
}

func TestPaperGuideReaderCanGenerateButCannotPrepareMissingIR(t *testing.T) {
	db, hash, _ := guideHTTPFixture(t)
	if err := db.AutoMigrate(&orm.ACLModel{}, &orm.UserGroupModel{}, &orm.ACLGroupModel{}); err != nil {
		t.Fatal(err)
	}
	aclIndependenceGrant(t, db, "guide-ds", "reader", acl.PermissionDatasetRead)
	db.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "owner").Update("share", true)
	t.Setenv("LAZYMIND_PAPER_GUIDE_ENABLED", "true")
	body := `{"expected_source_hash":"` + hash + `","model_ref":"shared:a","idempotency_key":"reader-one"}`
	response := httptest.NewRecorder()
	GeneratePaperGuide(response, guideRequest("POST", body, "reader", nil))
	if response.Code != 202 {
		t.Fatalf("read-only generation: %d %s", response.Code, response.Body)
	}
	db.Delete(&orm.AsyncJob{}, "id = ?", "guide-ir")
	scope := paperguide.Scope{OwnerID: "reader", DatasetID: "guide-ds", DocumentID: "guide-doc", Language: "zh"}
	_, err := (paperGuideSource{}).Load(t.Context(), scope, true)
	if err != paperguide.ErrPreparation {
		t.Fatalf("read-only parser request: %v", err)
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Where("job_type = ?", pdfIRJobType).Count(&count)
	if count != 0 {
		t.Fatal("reader enqueued a parser")
	}
	db.Delete(&orm.ACLModel{}, "resource_id = ? AND target_id = ?", "guide-ds", "reader")
	response = httptest.NewRecorder()
	GetPaperGuide(response, guideRequest("GET", "", "reader", nil))
	if response.Code != 404 {
		t.Fatal("revoked reader still read the guide")
	}
}

func TestPaperGuideGeneralPDFRejectsGenerationAndResume(t *testing.T) {
	db, hash, _ := guideHTTPFixture(t)
	t.Setenv("LAZYMIND_PAPER_GUIDE_ENABLED", "true")
	db.Model(&orm.Dataset{}).Where("id = ?", "guide-ds").Update("scene", "general")
	body := `{"expected_source_hash":"` + hash + `","model_ref":"own:a","idempotency_key":"general"}`
	w := httptest.NewRecorder()
	GeneratePaperGuide(w, guideRequest("POST", body, "owner", nil))
	if w.Code != 403 {
		t.Fatalf("general PDF generated: %d %s", w.Code, w.Body)
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Where("job_type = ?", paperguide.JobType).Count(&count)
	if count != 0 {
		t.Fatal("general PDF queued guide")
	}
	db.Model(&orm.Dataset{}).Where("id = ?", "guide-ds").Update("scene", "academic_papers")
	w = httptest.NewRecorder()
	GeneratePaperGuide(w, guideRequest("POST", body, "owner", nil))
	if w.Code != 202 {
		t.Fatalf("academic PDF rejected: %d %s", w.Code, w.Body)
	}
	var rev orm.PaperGuideRevision
	db.Take(&rev)
	db.Model(&orm.Dataset{}).Where("id = ?", "guide-ds").Update("scene", "general")
	calls := 0
	service := paperGuideService()
	service.Call = func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
		calls++
		return nil, nil
	}
	_, err := service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(paperguide.JobPayload{RevisionID: rev.ID, Epoch: rev.Epoch})}, guideCheckpointFence{db: db.DB})
	if err == nil || calls != 0 {
		t.Fatalf("ineligible stage invoked model: %d %v", calls, err)
	}
	w = httptest.NewRecorder()
	ResumePaperGuide(w, guideRequest("POST", "", "owner", map[string]string{"run": rev.ID}))
	if w.Code != 403 {
		t.Fatalf("ineligible resume: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	GetPaperGuide(w, guideRequest("GET", "", "owner", nil))
	if w.Code != 200 {
		t.Fatalf("historical read gated: %d %s", w.Code, w.Body)
	}
}

type guideCheckpointFence struct {
	db     *gorm.DB
	inside *bool
}

func (f guideCheckpointFence) WithLease(ctx context.Context, fn func(*gorm.DB) error) error {
	return f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if f.inside != nil {
			*f.inside = true
			defer func() { *f.inside = false }()
		}
		return fn(tx)
	})
}
func (guideCheckpointFence) SetProgress(context.Context, int64, int64) error { return nil }
func (guideCheckpointFence) Heartbeat(context.Context) error                 { return nil }

func TestPaperGuideSharedReaderCheckpointDoesNotResolveGroupsInTransaction(t *testing.T) {
	db, hash, _ := guideHTTPFixture(t)
	if err := db.AutoMigrate(&orm.ACLModel{}, &orm.UserGroupModel{}, &orm.ACLGroupModel{}); err != nil {
		t.Fatal(err)
	}
	aclIndependenceGrant(t, db, "guide-ds", "reader", acl.PermissionDatasetRead)
	t.Setenv("LAZYMIND_PAPER_GUIDE_ENABLED", "true")
	service := paperGuideService()
	scope := paperguide.Scope{OwnerID: "reader", DatasetID: "guide-ds", DocumentID: "guide-doc", Language: "zh"}
	rev, err := service.Repo.Create(t.Context(), paperguide.CreateRevision{Scope: scope, SourceSHA256: hash, Model: paperguide.ModelIdentity{ID: "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	inside := false
	type requestMarker struct{}
	ctx := context.WithValue(t.Context(), requestMarker{}, true)
	outsideAuth := 0
	previous := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if inside {
			t.Error("checkpoint made HTTP call while lease/domain transaction held")
		}
		outsideAuth++
		if strings.HasSuffix(r.URL.Path, "/groups/internal") && r.Context().Value(requestMarker{}) != true {
			t.Error("group resolution lost request context")
		}
		return previous.RoundTrip(r)
	})
	defer func() { http.DefaultTransport = previous }()
	fence := guideCheckpointFence{db.DB, &inside}
	if err = service.Repo.PutStep(ctx, fence, rev.ID, rev.Epoch, paperguide.StepWrite{Key: "fixture", InputHash: "h", Output: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if outsideAuth == 0 {
		t.Fatal("group membership was not prepared outside checkpoint")
	}
	db.Delete(&orm.ACLModel{}, "resource_id = ? AND target_id = ?", "guide-ds", "reader")
	err = service.Repo.PutStep(ctx, fence, rev.ID, rev.Epoch, paperguide.StepWrite{Key: "revoked", InputHash: "h", Output: json.RawMessage(`{}`)})
	if !errors.Is(err, paperguide.ErrForbidden) {
		t.Fatalf("revoked reader checkpoint: %v", err)
	}
}

func TestPaperGuideModelTokenLimitsUseConfiguredUnitsAndMinimum(t *testing.T) {
	db, _, _ := guideHTTPFixture(t)
	scope := paperguide.Scope{OwnerID: "owner", DatasetID: "guide-ds", DocumentID: "guide-doc", Language: "zh"}
	for _, tc := range []struct {
		window, input string
		want          int
	}{{"1M", "128K", 131072}, {"128K", "1M", 131072}, {"32768", "128K", 32768}, {"invalid", "64K", 65536}, {"", "", 32768}, {"999999999999999999999M", "", 32768}} {
		db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "a").Updates(map[string]any{"max_input_tokens": tc.window, "input_token_limit": tc.input})
		model, _, err := resolvePaperGuideModel(t.Context(), scope, "own:a")
		if err != nil || model.ContextTokens != tc.want {
			t.Fatalf("%s/%s: got %d want %d (%v)", tc.window, tc.input, model.ContextTokens, tc.want, err)
		}
	}
}

func TestPaperGuideCatalogPathDoesNotReuseLegacySnapshot(t *testing.T) {
	_, hash, _ := guideHTTPFixture(t)
	scope := paperguide.Scope{OwnerID: "owner", DatasetID: "guide-ds", DocumentID: "guide-doc", Language: "zh"}
	state, err := (paperGuideSource{}).Load(t.Context(), scope, false)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(filepath.Dir(state.Path), hash+"-"+state.Snapshot.IRDigest+".json")
	if err = os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(legacyPath, []byte("legacy sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err = (paperGuideSource{}).Load(t.Context(), scope, true)
	if err != nil || state.Path == legacyPath || !strings.Contains(state.Path, paperguide.SourceCatalogVersion) {
		t.Fatalf("snapshot cache mixed versions: %s %v", state.Path, err)
	}
	raw, err := os.ReadFile(legacyPath)
	if err != nil || string(raw) != "legacy sentinel" {
		t.Fatal("overwrote legacy file")
	}
	current, err := (paperGuideSource{}).Read(t.Context(), state.Path)
	if err != nil || paperguide.ValidateSnapshot(current, current.Identity, current.IRDigest) != nil {
		t.Fatal("new snapshot not verifiable")
	}
}

func TestPaperGuideOldContractReportsRegenerationWithoutSourceChange(t *testing.T) {
	db, hash, _ := guideHTTPFixture(t)
	t.Setenv("LAZYMIND_PAPER_GUIDE_ENABLED", "true")
	body := `{"expected_source_hash":"` + hash + `","model_ref":"own:a","idempotency_key":"old-contract"}`
	w := httptest.NewRecorder()
	GeneratePaperGuide(w, guideRequest("POST", body, "owner", nil))
	if w.Code != 202 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var rev orm.PaperGuideRevision
	if err := db.Take(&rev).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&rev).Update("fingerprint", "legacy-contract")
	w = httptest.NewRecorder()
	ResumePaperGuide(w, guideRequest("POST", "", "owner", map[string]string{"run": rev.ID}))
	if w.Code != 409 || !strings.Contains(w.Body.String(), "paper_guide_regeneration_required") || !strings.Contains(w.Body.String(), "2004413") {
		t.Fatalf("wrong version error: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	GetPaperGuide(w, guideRequest("GET", "", "owner", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "paper_guide_regeneration_required") {
		t.Fatalf("old candidate status: %d %s", w.Code, w.Body)
	}
	var jobs int64
	db.Model(&orm.AsyncJob{}).Where("job_type = ?", paperguide.JobType).Count(&jobs)
	if jobs != 1 {
		t.Fatal("old resume enqueued a new paid run")
	}
}

func TestPaperGuideAdapterPreservesDeadlineAndCancellation(t *testing.T) {
	guideHTTPFixture(t)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://fixture.invalid")
	for _, deadline := range []bool{true, false} {
		var ctx context.Context
		var cancel context.CancelFunc
		want := context.Canceled
		if deadline {
			ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			want = context.DeadlineExceeded
		} else {
			ctx, cancel = context.WithCancel(t.Context())
			cancel()
		}
		_, err := paperGuideService().Call(ctx, "paper.guide.plan", map[string]any{}, map[string]any{})
		cancel()
		if !errors.Is(err, want) {
			t.Fatalf("lost safe context cause: deadline=%t err=%v", deadline, err)
		}
	}
}

func TestPaperGuideModelFailureHTTPMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{paperguide.ErrModelTimeout, 504, "2004414"},
		{paperguide.ErrModelRequest, 502, "2004415"},
		{paperguide.ErrEvidence, 422, "2004406"},
	} {
		w := httptest.NewRecorder()
		paperGuideError(w, tc.err)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || !strings.Contains(w.Body.String(), tc.err.Error()) {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
}

func TestPaperGuideClientContractFailuresReachRunnerAsEvidenceErrors(t *testing.T) {
	for _, kind := range []string{"task_type", "task_version", "model_calls", "transport", "timeout"} {
		t.Run(kind, func(t *testing.T) {
			db, hash, _ := guideHTTPFixture(t)
			t.Setenv("LAZYMIND_PAPER_GUIDE_ENABLED", "true")
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://fixture.invalid")
			previous := http.DefaultTransport
			calls := 0
			http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/chat/llm-task:run" {
					return previous.RoundTrip(r)
				}
				calls++
				if kind == "transport" {
					return nil, errors.New("provider-secret")
				}
				if kind == "timeout" {
					return testJSONResponse(504, `{"detail":{"error_code":"task_timeout","usage":{"model_calls":1,"elapsed_ms":120000}}}`), nil
				}
				usage := map[string]any{"task_type": "paper.guide.plan", "task_version": "paper-guide-v8", "model_calls": 1, "elapsed_ms": 19}
				if kind == "model_calls" {
					usage[kind] = 2
				} else {
					usage[kind] = "wrong"
				}
				return testJSONResponse(200, string(mustJSON(map[string]any{"status": "succeeded", "output": map[string]any{}, "usage": usage}))), nil
			})
			t.Cleanup(func() { http.DefaultTransport = previous })
			w := httptest.NewRecorder()
			GeneratePaperGuide(w, guideRequest("POST", `{"expected_source_hash":"`+hash+`","model_ref":"own:a","idempotency_key":"contract"}`, "owner", nil))
			if w.Code != 202 {
				t.Fatalf("generate: %d %s", w.Code, w.Body)
			}
			var rev orm.PaperGuideRevision
			if err := db.Take(&rev).Error; err != nil {
				t.Fatal(err)
			}
			service := paperGuideService()
			result, err := service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(paperguide.JobPayload{RevisionID: rev.ID, Epoch: rev.Epoch})}, guideCheckpointFence{db: db.DB})
			want, class := paperguide.ErrEvidence, "schema"
			if kind == "transport" {
				want, class = paperguide.ErrModelRequest, "transport"
			}
			if kind == "timeout" {
				want, class = paperguide.ErrModelTimeout, "timeout"
			}
			if !errors.Is(err, want) || result.ErrorCode != want.Error() || calls != 2 {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, calls)
			}
			if err = db.First(&rev, "id = ?", rev.ID).Error; err != nil {
				t.Fatal(err)
			}
			if rev.ErrorCode != want.Error() || rev.CallsUsed != 2 || rev.RecoveryCalls != 1 {
				t.Fatalf("bad persisted failure: %+v", rev)
			}
			var markers []orm.PaperGuideStep
			if err = db.Where("revision_id = ? AND step_key LIKE ?", rev.ID, "request:%").Find(&markers).Error; err != nil {
				t.Fatal(err)
			}
			if len(markers) != 2 {
				t.Fatalf("markers=%d", len(markers))
			}
			for _, marker := range markers {
				var diagnostic struct {
					Class string `json:"error_class"`
				}
				if json.Unmarshal([]byte(marker.OutputJSON), &diagnostic) != nil || diagnostic.Class != class || strings.Contains(marker.OutputJSON, "provider-secret") {
					t.Fatalf("wrong safe diagnostic: %s", marker.OutputJSON)
				}
			}
		})
	}
}

func TestPaperGuideServiceHTTPVersionMatchesDurableAndPythonContract(t *testing.T) {
	// This fixture is independently checked against the real Python registry and
	// measured task templates. Never derive the server response from the request:
	// echoing it would accept an obsolete Go adapter version on both sides.
	raw, err := os.ReadFile("../paperguide/testdata/request-budget.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		Version string `json:"task_version"`
	}
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if expected.Version == "" || expected.Version != paperguide.TaskVersion {
		t.Fatalf("durable/Python contract mismatch: fixture=%s durable=%s", expected.Version, paperguide.TaskVersion)
	}
	for _, task := range []string{"paper.guide.extract", "paper.guide.plan", "paper.guide.section", "paper.guide.review"} {
		t.Run(task, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Task    string `json:"task_type"`
					Options struct {
						Version string `json:"task_version"`
					} `json:"options"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode actual HTTP request: %v", err)
				}
				if r.URL.Path != "/api/chat/llm-task:run" || request.Task != task || request.Options.Version != expected.Version {
					t.Errorf("worker HTTP contract: task=%s version=%s; want %s %s", request.Task, request.Options.Version, task, expected.Version)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "succeeded", "output": map[string]any{"accepted": true},
					"usage": map[string]any{"task_type": task, "task_version": expected.Version, "model_calls": 1},
				})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			// Exercise the actual worker callback, not a hand-built client request.
			output, err := paperGuideService().Call(t.Context(), task, map[string]any{"language": "en"}, nil)
			if err != nil || string(output) != `{"accepted":true}` || calls != 1 {
				t.Fatalf("worker did not receive successful contract output: output=%s err=%v calls=%d", output, err, calls)
			}
		})
	}
}

func TestPaperGuideFragmentFixtureUsesCurrentActualTransport(t *testing.T) {
	guideHTTPFixture(t)
	raw, err := os.ReadFile("../paperguide/testdata/table-fragment-transport.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version string          `json:"task_version"`
		Input   map[string]any  `json:"input"`
		Output  json.RawMessage `json:"output"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	calls := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var wire struct {
			Options map[string]string `json:"options"`
			Input   struct {
				Data map[string]any `json:"data"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if wire.Options["task_version"] != fixture.Version || fixture.Version != paperguide.TaskVersion || !reflect.DeepEqual(wire.Input.Data, fixture.Input) {
			t.Fatalf("changed canonical physical transport %+v", wire)
		}
		return testJSONResponse(200, string(mustJSON(map[string]any{"status": "succeeded", "output": fixture.Output, "usage": map[string]any{"model_calls": 1, "task_type": "paper.guide.extract", "task_version": fixture.Version}}))), nil
	})
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://fixture.invalid")
	got, err := paperGuideService().Call(t.Context(), "paper.guide.extract", fixture.Input, map[string]any{})
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v output=%s", calls, err, got)
	}
	var actual, expected any
	json.Unmarshal(got, &actual)
	json.Unmarshal(fixture.Output, &expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("canonical output changed")
	}
}
