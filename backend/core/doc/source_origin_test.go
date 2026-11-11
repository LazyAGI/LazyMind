package doc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
)

const originTestAccessPath = "/api/scan/internal/source-access/by-dataset:batch"

func originTestDB(t *testing.T) *orm.DB {
	t.Helper()
	t.Setenv("LAZYMIND_RAG_ENABLED", "0")
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", "http://scan.test")
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "origin-test-token")
	db := newDocumentTestDB(t)
	if err := db.AutoMigrate(&orm.ACLModel{}, &orm.KBModel{}, &orm.UserGroupModel{}, &orm.ACLGroupModel{}, &orm.UserUIPreferences{}); err != nil {
		t.Fatal(err)
	}
	acl.InitStore(db)
	return db
}

// Only the external Scan decision is replaced; Core ACL, handlers and storage remain real.
func originTestTransport(t *testing.T, scan func(*http.Request) (*http.Response, error)) *int {
	t.Helper()
	previous := http.DefaultTransport
	calls := 0
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasPrefix(r.URL.Path, "/api/scan/") {
			calls++
			if r.URL.Path == originTestAccessPath {
				return scan(r)
			}
			if r.URL.Path == "/api/scan/internal/sources/by-datasets" {
				return testJSONResponse(200, `{"source_map":{}}`), nil
			}
			return testJSONResponse(404, `{}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/identity/internal") {
			return testJSONResponse(200, `{"sub":"user-1","tenant_id":"default","role":"admin","permissions":["document.read","document.write"]}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/groups/internal") {
			return testJSONResponse(200, `{"groups":[]}`), nil
		}
		return testJSONResponse(404, `{}`), nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	return &calls
}

func originTestSeed(t *testing.T, db *orm.DB, id, info string) {
	t.Helper()
	seedDatasetCatalogDataset(t, db, id, "user-1", id, "", []string{"test"}, time.Now().UTC())
	if err := db.Model(&orm.Dataset{}).Where("id = ?", id).Updates(map[string]any{
		"dataset_info": json.RawMessage(info), "processing_level": ProcessingLevelStored, "resource_version": 7,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func originTestRow(t *testing.T, db *orm.DB, id string) orm.Dataset {
	t.Helper()
	var row orm.Dataset
	if err := db.Where("id = ?", id).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func originTestAssert(t *testing.T, raw json.RawMessage, kind, basis string) {
	t.Helper()
	var info struct {
		Origin struct {
			Version int    `json:"version"`
			Kind    string `json:"kind"`
			Basis   string `json:"basis"`
		} `json:"source_origin"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	if info.Origin.Version != 1 || info.Origin.Kind != kind || info.Origin.Basis != basis {
		t.Errorf("source_origin = %s, want version=1 kind=%s basis=%s", raw, kind, basis)
	}
}

func originTestRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("X-User-Id", "user-1")
	return r
}

func originTestGet(id string) DatasetGetRequest {
	return DatasetGetRequest{UserID: "user-1", DatasetID: id,
		Caller: DatasetCatalogCaller{UserID: "user-1", Permissions: []string{"document.write"}}}
}

func TestSourceOriginCreationIsServerOwnedAndAtomic(t *testing.T) {
	for _, tc := range []struct {
		name, extra, hint, token, want string
	}{
		{"ordinary", ``, "", "", "none"},
		{"public injection", `,"dataset_info":{"source_origin":{"version":1,"kind":"scan","basis":"learned"}}`, "", "", "none"},
		{"legacy scan tag", `,"tags":["scan"]`, "", "", "scan"},
		{"legacy scan key", `,"idempotency_key":"create-source:user-1:request-1:dataset"`, "", "", "scan"},
		{"trusted scan", ``, "scan", "origin-test-token", "scan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := originTestDB(t)
			originTestTransport(t, func(*http.Request) (*http.Response, error) { return testJSONResponse(200, `{"items":[]}`), nil })
			var inserted json.RawMessage
			if err := db.Callback().Create().After("gorm:create").Register("test:origin-at-insert", func(tx *gorm.DB) {
				if row, ok := tx.Statement.Dest.(*orm.Dataset); ok {
					inserted = append(json.RawMessage(nil), row.DatasetInfo...)
				}
			}); err != nil {
				t.Fatal(err)
			}
			body := `{"display_name":"Created","tags":["test"],"processing_level":"stored"` + tc.extra + `}`
			r := originTestRequest("POST", "/datasets?dataset_id=created", body)
			if tc.hint != "" {
				r.Header.Set("X-LazyMind-Source-Origin", tc.hint)
				r.Header.Set("X-LazyMind-Internal-Token", tc.token)
			}
			w := httptest.NewRecorder()
			CreateDataset(w, r)
			if w.Code != 200 {
				t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
			}
			originTestAssert(t, inserted, tc.want, "created")
			originTestAssert(t, originTestRow(t, db, "created").DatasetInfo, tc.want, "created")
		})
	}
}

func TestSourceOriginUntrustedHintCannotCreateOrdinaryProof(t *testing.T) {
	for _, token := range []string{"", "wrong-token"} {
		t.Run("token="+token, func(t *testing.T) {
			db := originTestDB(t)
			originTestTransport(t, func(*http.Request) (*http.Response, error) { return testJSONResponse(200, `{"items":[]}`), nil })
			r := originTestRequest("POST", "/datasets?dataset_id=untrusted", `{"display_name":"Untrusted","tags":["test"],"processing_level":"stored"}`)
			r.Header.Set("X-LazyMind-Source-Origin", "scan")
			r.Header.Set("X-LazyMind-Internal-Token", token)
			w := httptest.NewRecorder()
			CreateDataset(w, r)
			var count int64
			db.Model(&orm.Dataset{}).Where("id = ?", "untrusted").Count(&count)
			if w.Code < 400 || count != 0 {
				t.Fatalf("untrusted hint created data: status=%d rows=%d body=%s", w.Code, count, w.Body.String())
			}
		})
	}
}

func TestSourceOriginNoneUploadsWithoutScanAndRetainsACL(t *testing.T) {
	db := originTestDB(t)
	originTestSeed(t, db, "ordinary", `{"source_origin":{"version":1,"kind":"none","basis":"created"}}`)
	calls := originTestTransport(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("Scan is offline") })
	caller := DatasetCatalogCaller{UserID: "user-1"}
	if _, err := ResolveResourceDataset(t.Context(), db.DB, caller, "ordinary", acl.PermissionDatasetUpload); err != nil {
		t.Errorf("ordinary binding depends on Scan: %v", err)
	}
	for _, denied := range []DatasetCatalogCaller{{UserID: "other"}, {UserID: "user-1", TenantID: "other"}} {
		if _, err := ResolveResourceDataset(t.Context(), db.DB, denied, "ordinary", acl.PermissionDatasetUpload); err == nil {
			t.Errorf("none marker bypassed Core ACL/tenant for %+v", denied)
		}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("files", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("retained original"))
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/datasets/ordinary/tasks:batchUpload", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("X-User-Id", "user-1")
	r = mux.SetURLVars(r, map[string]string{"dataset": "ordinary"})
	w := httptest.NewRecorder()
	BatchUploadTasks(w, r)
	var documents, tasks int64
	db.Model(&orm.Document{}).Where("dataset_id = ?", "ordinary").Count(&documents)
	db.Model(&orm.Task{}).Where("dataset_id = ?", "ordinary").Count(&tasks)
	if w.Code != 200 || documents != 1 || tasks != 1 || *calls != 0 {
		t.Fatalf("offline upload: status=%d documents=%d tasks=%d Scan calls=%d body=%s", w.Code, documents, tasks, *calls, w.Body.String())
	}
}

func TestSourceOriginCurrentLegacyLearnsOnceAndReturnsCurrentVersion(t *testing.T) {
	db := originTestDB(t)
	originTestSeed(t, db, "legacy", `{"keep":{"number":42}}`)
	calls := originTestTransport(t, func(*http.Request) (*http.Response, error) {
		return testJSONResponse(200, `{"items":[{"dataset_id":"legacy","exists":false,"allowed":true}]}`), nil
	})
	service := mustDatasetCatalogService(t, db.DB)
	got, err := service.GetDataset(t.Context(), originTestGet("legacy"))
	if err != nil {
		t.Fatal(err)
	}
	stored := originTestRow(t, db, "legacy")
	originTestAssert(t, stored.DatasetInfo, "none", "learned")
	if !bytes.Contains(stored.DatasetInfo, []byte(`"number":42`)) || stored.ResourceVersion != 8 || got.ResourceVersion != 8 {
		t.Errorf("learn lost JSON/version: stored=%s version=%d returned=%d", stored.DatasetInfo, stored.ResourceVersion, got.ResourceVersion)
	}
	if *calls != 1 {
		t.Errorf("first current-library open made %d Scan calls, want one reused check", *calls)
	}
	before := *calls
	if _, err = service.GetDataset(t.Context(), originTestGet("legacy")); err != nil {
		t.Fatal(err)
	}
	if _, err = ResolveResourceDataset(t.Context(), db.DB, originTestGet("legacy").Caller, "legacy", acl.PermissionDatasetUpload); err != nil {
		t.Fatal(err)
	}
	if *calls != before {
		t.Errorf("confirmed ordinary library queried Scan again: %d additional calls", *calls-before)
	}
}

func TestSourceOriginCurrentSourceLearnsClassificationWithoutGrantingPermission(t *testing.T) {
	db := originTestDB(t)
	originTestSeed(t, db, "managed", `{"keep":true}`)
	originTestTransport(t, func(*http.Request) (*http.Response, error) {
		return testJSONResponse(200, `{"items":[{"dataset_id":"managed","source_id":"source-1","exists":true,"allowed":false}]}`), nil
	})
	_, err := mustDatasetCatalogService(t, db.DB).GetDatasetAccess(t.Context(), originTestGet("managed"))
	if codeOfDatasetServiceError(err) != DatasetServiceForbidden {
		t.Errorf("source denial was bypassed: %v", err)
	}
	originTestAssert(t, originTestRow(t, db, "managed").DatasetInfo, "scan", "learned")
}

func TestSourceOriginIncompleteDecisionNeverBecomesOrdinaryProof(t *testing.T) {
	for _, body := range []string{
		`{"items":[]}`,
		`{"items":[{"dataset_id":"legacy","allowed":true}]}`,
		`{"items":[{"dataset_id":"legacy","exists":false}]}`,
		`{"items":[{"dataset_id":"other","exists":false,"allowed":true}]}`,
		`{"items":[{"dataset_id":"legacy","exists":false,"allowed":true},{"dataset_id":"legacy","exists":false,"allowed":true}]}`,
		`{"items":[{"dataset_id":"legacy","exists":"false","allowed":true}]}`,
		`{"data":{"items":[{"dataset_id":"legacy","exists":false,"allowed":true}]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			db := originTestDB(t)
			const original = `{"keep":true}`
			originTestSeed(t, db, "legacy", original)
			originTestTransport(t, func(*http.Request) (*http.Response, error) { return testJSONResponse(200, body), nil })
			if _, err := mustDatasetCatalogService(t, db.DB).GetDatasetAccess(t.Context(), originTestGet("legacy")); err != nil {
				t.Errorf("learning validation changed the existing access result: %v", err)
			}
			stored := originTestRow(t, db, "legacy")
			if string(stored.DatasetInfo) != original || stored.ResourceVersion != 7 {
				t.Fatalf("incomplete response became persistent evidence: %s version=%d", stored.DatasetInfo, stored.ResourceVersion)
			}
		})
	}
}

func TestSourceOriginOfflineAndScanNotFoundDoNotChangeClassification(t *testing.T) {
	for _, tc := range []struct {
		name, info string
		offline    bool
	}{
		{"legacy offline", `{"keep":true}`, true},
		{"known scan not found", `{"source_origin":{"version":1,"kind":"scan","basis":"created"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := originTestDB(t)
			originTestSeed(t, db, "dataset", tc.info)
			calls := originTestTransport(t, func(*http.Request) (*http.Response, error) {
				if tc.offline {
					return nil, errors.New("Scan is offline")
				}
				return testJSONResponse(200, `{"items":[{"dataset_id":"dataset","exists":false,"allowed":true}]}`), nil
			})
			_, _ = mustDatasetCatalogService(t, db.DB).GetDatasetAccess(t.Context(), originTestGet("dataset"))
			err := strictResourceSourceAccess(t.Context(), originTestGet("dataset").Caller, "dataset", acl.PermissionDatasetUpload)
			if (err != nil) != tc.offline {
				t.Errorf("original strict failure/not-found semantics changed: %v", err)
			}
			stored := originTestRow(t, db, "dataset")
			if string(stored.DatasetInfo) != tc.info || *calls < 2 {
				t.Fatalf("classification downgraded: %s Scan calls=%d", stored.DatasetInfo, *calls)
			}
		})
	}
}

func TestSourceOriginLearningCASDoesNotOverwriteConcurrentOrigin(t *testing.T) {
	db := originTestDB(t)
	originTestSeed(t, db, "legacy", `{"keep":true}`)
	const concurrent = `{"keep":true,"source_origin":{"version":1,"kind":"scan","basis":"created"}}`
	originTestTransport(t, func(r *http.Request) (*http.Response, error) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		// This write also proves the HTTP is not performed while holding the dataset transaction.
		if err := db.WithContext(ctx).Model(&orm.Dataset{}).Where("id = ?", "legacy").Updates(map[string]any{
			"dataset_info": json.RawMessage(concurrent), "resource_version": 11,
		}).Error; err != nil {
			t.Fatalf("concurrent publisher could not update outside HTTP transaction: %v", err)
		}
		return testJSONResponse(200, `{"items":[{"dataset_id":"legacy","exists":false,"allowed":true}]}`), nil
	})
	_, _ = mustDatasetCatalogService(t, db.DB).GetDatasetAccess(t.Context(), originTestGet("legacy"))
	stored := originTestRow(t, db, "legacy")
	if string(stored.DatasetInfo) != concurrent || stored.ResourceVersion != 11 {
		t.Fatalf("stale learning overwrote new origin: %s version=%d", stored.DatasetInfo, stored.ResourceVersion)
	}
}

func TestSourceOriginListsDoNotLearnAndMixedFailurePreservesOrdinaryActions(t *testing.T) {
	db := originTestDB(t)
	originTestSeed(t, db, "ordinary", `{"source_origin":{"version":1,"kind":"none","basis":"created"}}`)
	originTestSeed(t, db, "legacy", `{"keep":true}`)
	offline := false
	originTestTransport(t, func(r *http.Request) (*http.Response, error) {
		if offline {
			var req scanSourceAccessByDatasetBatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(req.DatasetIDs, "ordinary") {
				t.Error("mixed batch sent known ordinary library to Scan")
			}
			return nil, errors.New("Scan is offline")
		}
		return testJSONResponse(200, `{"items":[{"dataset_id":"ordinary","exists":false,"allowed":true},{"dataset_id":"legacy","exists":false,"allowed":true}]}`), nil
	})
	service := mustDatasetCatalogService(t, db.DB)
	if _, err := service.ListDatasets(t.Context(), DatasetListRequest{UserID: "user-1", Limit: 20}); err != nil {
		t.Fatal(err)
	}
	if raw := originTestRow(t, db, "legacy").DatasetInfo; string(raw) != `{"keep":true}` {
		t.Errorf("listing learned unrelated library: %s", raw)
	}
	offline = true
	rows := []Dataset{{DatasetID: "ordinary", IsOwner: true}, {DatasetID: "legacy", IsOwner: true}}
	service.addAllowedActions(t.Context(), originTestGet("ordinary").Caller, rows)
	for _, action := range []string{"read", "upload", "write", "delete"} {
		if !slices.Contains(rows[0].AllowedActions, action) {
			t.Errorf("ordinary action %s lost on mixed failure: %v", action, rows[0].AllowedActions)
		}
	}
	if fmt.Sprint(rows[1].AllowedActions) != "[read]" {
		t.Errorf("mixed failure granted unknown library actions: %v", rows[1].AllowedActions)
	}
}

func TestSourceOriginPublicPatchCannotReplaceServerClassification(t *testing.T) {
	db := originTestDB(t)
	const original = `{"keep":true,"source_origin":{"version":1,"kind":"scan","basis":"created"}}`
	originTestSeed(t, db, "managed", original)
	originTestTransport(t, func(*http.Request) (*http.Response, error) {
		return testJSONResponse(200, `{"items":[{"dataset_id":"managed","source_id":"source-1","exists":true,"allowed":true}]}`), nil
	})
	r := originTestRequest("PATCH", "/datasets/managed", `{"desc":"updated","dataset_info":{"source_origin":{"version":1,"kind":"none","basis":"created"}},"source_origin":{"kind":"none"}}`)
	r = mux.SetURLVars(r, map[string]string{"dataset": "managed"})
	w := httptest.NewRecorder()
	UpdateDataset(w, r)
	stored := originTestRow(t, db, "managed")
	if w.Code != 200 || stored.Desc != "updated" || string(stored.DatasetInfo) != original {
		t.Fatalf("public PATCH changed protected classification: status=%d row=%+v body=%s", w.Code, stored, w.Body.String())
	}
}

func TestSourceOriginStaleDeletionDoesNotOverwriteNewClassification(t *testing.T) {
	for _, market := range []bool{false, true} {
		t.Run(fmt.Sprintf("market=%t", market), func(t *testing.T) {
			db := originTestDB(t)
			originTestSeed(t, db, "dataset", `{"keep":true}`)
			stale := originTestRow(t, db, "dataset")
			const latest = `{"keep":true,"source_origin":{"version":1,"kind":"scan","basis":"created"}}`
			publish := func() {
				if err := db.Model(&orm.Dataset{}).Where("id = ?", "dataset").Updates(map[string]any{
					"dataset_info": json.RawMessage(latest), "resource_version": 11,
				}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if market {
				previous := http.DefaultTransport
				http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if r.Method != "DELETE" || !strings.HasPrefix(r.URL.Path, "/v1/kbs/") {
						t.Fatalf("unexpected market delete dependency: %s %s", r.Method, r.URL.Path)
					}
					publish()
					return testJSONResponse(200, `{}`), nil
				})
				t.Cleanup(func() { http.DefaultTransport = previous })
				_ = DeleteMarketDataset(t.Context(), "user-1", "dataset")
			} else {
				publish()
				_ = db.Transaction(func(tx *gorm.DB) error {
					return softDeleteDataset(t.Context(), tx, &stale, "user-1", time.Now().UTC())
				})
			}
			stored := originTestRow(t, db, "dataset")
			if string(stored.DatasetInfo) != latest || stored.ResourceVersion < 11 {
				t.Fatalf("stale full-row save overwrote classification: info=%s version=%d", stored.DatasetInfo, stored.ResourceVersion)
			}
		})
	}
}
