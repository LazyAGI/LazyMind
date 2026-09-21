package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestTTSReadinessWithoutRuntimeRole(t *testing.T) {
	for _, tc := range []struct {
		name, owner, source    string
		shared, deleted, ready bool
	}{
		{name: "unconfigured"},
		{name: "own", owner: "viewer", source: "own", ready: true},
		{name: "shared", owner: "admin", shared: true, source: "shared", ready: true},
		{name: "private other user", owner: "admin"},
		{name: "deleted shared", owner: "admin", shared: true, deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/model/role_type" || r.URL.Query().Get("role") != "tts" {
					t.Errorf("unexpected role request: %s", r.URL)
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"detail":"role 'tts' not found in runtime config"}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			roleTypeCache.Delete("tts")
			t.Cleanup(func() { roleTypeCache.Delete("tts") })
			db := orm.MigrateTestDB(t, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{})
			store.Init(db.DB, db.DB, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if tc.owner != "" {
				now := time.Now().UTC()
				model := orm.UserModelProviderGroupModel{
					ID: "speech-model", Name: "Demo speech", ProviderName: "Demo", ModelType: "tts",
					BaseModel: orm.BaseModel{CreateUserID: tc.owner, CreatedAt: now, UpdatedAt: now},
				}
				selection := orm.UserSelectedModel{
					UserID: tc.owner, UserName: "Demo admin", ModelKey: "tts", Share: tc.shared,
					UserModelProviderGroupModelID: model.ID, CreatedAt: now, UpdatedAt: now,
				}
				for _, row := range []any{&model, &selection} {
					if err := db.DB.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
				if tc.deleted {
					if err := db.DB.Delete(&model).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			req := httptest.NewRequest(http.MethodGet, "/model_providers/models/ready?model_type=tts", nil)
			req.Header.Set("X-User-Id", "viewer")
			rec := httptest.NewRecorder()
			GetModelReady(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var response struct {
				Data modelReadyResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Data.Ready != tc.ready || response.Data.Source != tc.source {
				t.Fatalf("readiness=%+v, want ready=%v source=%s", response.Data, tc.ready, tc.source)
			}
			if tc.source == "shared" && (response.Data.ModelName != "Demo speech" || response.Data.SharedByName != "Demo admin") {
				t.Fatalf("missing shared model detail: %+v", response.Data)
			}
			ready, err := IsModelReady(t.Context(), db.DB, "viewer", "tts")
			if err != nil || ready != tc.ready {
				t.Fatalf("IsModelReady=(%v, %v), want %v", ready, err, tc.ready)
			}
		})
	}
}

func TestRoleReadinessPreservesRuntimeFailuresAndStaticConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, role, body   string
		status             int
		dynamic, wantError bool
	}{
		{"tts service failure", "tts", `{}`, 503, false, true},
		{"tts malformed response", "tts", `invalid`, 200, false, true},
		{"other missing role", "llm", `{}`, 404, false, true},
		{"configured static tts", "tts", `{"type":"tts","is_dynamic":false}`, 200, false, false},
		{"configured dynamic tts", "tts", `{"type":"tts","is_dynamic":true}`, 200, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			roleTypeCache.Delete(tc.role)
			t.Cleanup(func() { roleTypeCache.Delete(tc.role) })
			dynamic, err := requiresDynamicSelection(t.Context(), tc.role)
			if (err != nil) != tc.wantError || dynamic != tc.dynamic {
				t.Fatalf("dynamic=(%v, %v), want (%v, error=%v)", dynamic, err, tc.dynamic, tc.wantError)
			}
		})
	}
}

func TestSharedModelDetailUsesSelectionColumns(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{})
	for _, modelType := range []string{"llm", "tts"} {
		row, err := getSharedModelDetail(t.Context(), db.DB, modelType)
		if err != nil || row != nil {
			t.Fatalf("empty %s detail=(%+v, %v)", modelType, row, err)
		}
	}
}
