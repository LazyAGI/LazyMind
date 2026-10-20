package doc

import (
	"encoding/json"
	"io"
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

func TestDocumentVisibilityHTTPResolvesCurrentTenantFromAccount(t *testing.T) {
	db := newDocumentTestDB(t)
	now := time.Now().UTC()
	for _, tenant := range []string{"company", "other", "default"} {
		seedDocumentServiceDatasetWithKBAndAlgo(t, db, tenant, "kb-"+tenant, "general_algo", "owner", now)
		if err := db.Model(&orm.Dataset{}).Where("id = ?", tenant).Update("tenant_id", tenant).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.Document{ID: "core-" + tenant, DatasetID: tenant, LazyllmDocID: "doc-" + tenant, BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := NewDocumentVisibilityService(db.DB, db.DB)
	svc.ready.Store(true)
	previous := documentVisibility.Swap(svc)
	t.Cleanup(func() { documentVisibility.Store(previous) })

	accountStatus, accountBody := http.StatusOK, `{"sub":"owner","tenant_id":"company","role":"user","permissions":["qa.read"]}`
	authCalls := 0
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/authservice/user/owner/groups/internal" {
			_, _ = io.WriteString(w, `{"groups":[]}`)
			return
		}
		authCalls++
		if r.URL.Path != "/api/authservice/user/owner/identity/internal" || r.Header.Get("X-LazyMind-Internal-Token") != "service-token" {
			t.Error("account identity lookup did not use the trusted service credentials")
		}
		w.WriteHeader(accountStatus)
		_, _ = io.WriteString(w, accountBody)
	}))
	t.Cleanup(auth.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", auth.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "service-token")
	scan := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/scan/internal/source-access/by-dataset:batch" {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if r.Header.Get("X-User-Id") != "owner" || r.Header.Get("X-Tenant-Id") != "company" {
			t.Error("source policy did not receive the resolved account identity")
		}
		_, _ = io.WriteString(w, `{"items":[{"dataset_id":"company","exists":false,"allowed":true}]}`)
	}))
	t.Cleanup(scan.Close)
	t.Setenv("LAZYMIND_SCAN_CONTROL_PLANE_URL", scan.URL)
	core := httptest.NewServer(http.HandlerFunc(FilterDocumentVisibilityHTTP))
	t.Cleanup(core.Close)
	for _, test := range []struct {
		name, tenantHeader, body string
		status                   int
		allowed                  bool
	}{
		{"native headers without tenant", "", accountBody, 200, true},
		{"spoofed tenant cannot change identity", "other", accountBody, 200, true},
		{"mismatched account", "", `{"sub":"intruder","tenant_id":"company"}`, 200, false},
		{"disabled account", "", `{}`, 403, false},
		{"identity service unavailable", "", `{}`, 503, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			accountBody, accountStatus = test.body, test.status
			req, err := http.NewRequest(http.MethodPost, core.URL, strings.NewReader(`{"items":[{"knowledge_id":"kb-company","document_id":"doc-company"},{"knowledge_id":"kb-other","document_id":"doc-other"},{"knowledge_id":"kb-default","document_id":"doc-default"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			// These are the actual native Chat core_api_client headers.
			req.Header.Set("X-User-Id", "owner")
			req.Header.Set("X-LazyMind-Internal-Token", "service-token")
			if test.tenantHeader != "" {
				req.Header.Set("X-Tenant-Id", test.tenantHeader)
			}
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if !test.allowed {
				if response.StatusCode == http.StatusOK {
					t.Fatal("unverified account was allowed")
				}
				return
			}
			var result struct{ Items []DocumentVisibilityRef }
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != 200 || len(result.Items) != 1 || result.Items[0].DocumentID != "doc-company" {
				t.Fatalf("valid tenant denied or cross-tenant document admitted: status=%d items=%+v", response.StatusCode, result.Items)
			}
		})
	}
	if authCalls != 5 {
		t.Fatalf("identity must be resolved on each request, got %d", authCalls)
	}
	t.Run("invalid internal credential cannot fall back to tenant headers", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, core.URL, strings.NewReader(`{"items":[{"knowledge_id":"kb-company","document_id":"doc-company"}]}`))
		req.Header.Set("X-User-Id", "owner")
		req.Header.Set("X-Tenant-Id", "company")
		req.Header.Set("X-LazyMind-Internal-Token", "wrong-token")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK || authCalls != 5 {
			t.Fatal("invalid internal credential was allowed or forwarded")
		}
	})

	// Exercise the other native HTTP readers, not just the shared caller helper.
	accountBody, accountStatus = `{"sub":"owner","tenant_id":"company","role":"user","permissions":["qa.read"]}`, 200
	originalRoot := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", originalRoot)
	path := filepath.Join(originalRoot, "document.txt")
	if err := os.WriteFile(path, []byte("nondefault account original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.Document{}).Where("id = ?", "core-company").Updates(map[string]any{"display_name": "document.txt", "ext": mustJSON(documentExt{StoredPath: path, OriginalFilename: "document.txt", ContentType: "text/plain"})}).Error; err != nil {
		t.Fatal(err)
	}
	router := mux.NewRouter()
	router.HandleFunc("/datasets", ListDatasets)
	router.HandleFunc("/datasets/{dataset}", GetDataset)
	router.HandleFunc("/datasets/{dataset}/documents/{document}:read", ReadDocument)
	readers := httptest.NewServer(router)
	t.Cleanup(readers.Close)
	for _, route := range []string{"/datasets", "/datasets/company", "/datasets/company/documents/core-company:read"} {
		t.Run(route, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, readers.URL+route, nil)
			req.Header.Set("X-User-Id", "owner")
			req.Header.Set("X-LazyMind-Internal-Token", "service-token")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("native reader denied account tenant: %d %s", response.StatusCode, body)
			}
			if route == "/datasets" {
				var result ListDatasetsResponse
				if err := json.Unmarshal(body, &result); err != nil || len(result.Datasets) != 1 || result.Datasets[0].DatasetID != "company" {
					t.Fatalf("catalog leaked or omitted tenant: %s (%v)", body, err)
				}
			}
			if strings.HasSuffix(route, ":read") && !strings.Contains(string(body), "nondefault account original") {
				t.Fatalf("reader did not return the original text: %s", body)
			}
			// Even a forged tenant header cannot turn an unavailable Auth lookup
			// into a default-tenant fallback in legacy catalog service requests.
			accountStatus = 403
			req.Header.Set("X-Tenant-Id", "default")
			response, err = http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				t.Fatal("reader accepted account lookup failure")
			}
			accountStatus = 200
		})
	}
	for _, route := range []string{"/datasets/other", "/datasets/other/documents/core-other:read"} {
		req, _ := http.NewRequest(http.MethodGet, readers.URL+route, nil)
		req.Header.Set("X-User-Id", "owner")
		req.Header.Set("X-Tenant-Id", "other")
		req.Header.Set("X-LazyMind-Internal-Token", "service-token")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatalf("cross-tenant native reader allowed %s", route)
		}
	}
}
