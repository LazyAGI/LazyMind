package modelconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
	"lazymind/core/store"
)

func TestSavedModelVerificationAdmitsReadingStudyModel(t *testing.T) {
	for _, tc := range []struct {
		name, query, mutation, outcome, wantCode string
		admitted                                 bool
	}{
		{name: "ordinary probe", outcome: `{"status":"passed","code":"ok"}`, wantCode: "ok"},
		{name: "explicit verification", query: "?verify=true", outcome: `{"status":"passed","code":"ok"}`, wantCode: "ok", admitted: true},
		{name: "upstream failure", query: "?verify=true", outcome: `{"status":"failed","code":"permission_denied"}`, wantCode: "permission_denied"},
		{name: "accepted is not verified", query: "?verify=true", outcome: `{"status":"accepted","code":"accepted"}`, wantCode: "invalid_response"},
		{name: "credential revision changed", query: "?verify=true", mutation: "credential", outcome: `{"status":"passed","code":"ok"}`, wantCode: "configuration_changed"},
		{name: "active selection revision changed", query: "?verify=true", mutation: "key", outcome: `{"status":"passed","code":"ok"}`, wantCode: "configuration_changed"},
		{name: "endpoint changed", query: "?verify=true", mutation: "endpoint", outcome: `{"status":"passed","code":"ok"}`, wantCode: "configuration_changed"},
		{name: "protocol changed", query: "?verify=true", mutation: "protocol", outcome: `{"status":"passed","code":"ok"}`, wantCode: "configuration_changed"},
		{name: "model limits changed", query: "?verify=true", mutation: "model", outcome: `{"status":"passed","code":"ok"}`, wantCode: "configuration_changed"},
		{name: "model deleted", query: "?verify=true", mutation: "deletion", outcome: `{"status":"passed","code":"ok"}`, wantCode: "configuration_changed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			seedChatRuntimeModel(t, db, "user", "default", true, true)
			seedChatRuntimeModel(t, db, "user", "manual", false, false)
			base := orm.BaseModel{CreateUserID: "user"}
			if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-manual").Update("api_key", "").Error; err != nil {
				t.Fatal(err)
			}
			for _, group := range []orm.UserModelProviderGroup{
				{ID: "credential-group", UserModelProviderID: "provider-manual", BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-key", CredentialRevision: 1, BaseModel: base},
				{ID: "other-group", UserModelProviderID: "provider-manual", BaseURL: "https://fixture.invalid/v1", BaseModel: base},
			} {
				if err := db.Create(&group).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Listing saved connection settings initializes the pre-existing key pool.
			keys := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/groups", nil)
			r.Header.Set("X-User-Id", "user")
			modelprovider.ListGroups(keys, mux.SetURLVars(r, map[string]string{"model_provider_id": "provider-manual"}))
			if keys.Code != 200 {
				t.Fatalf("list connection groups: %s", keys.Body)
			}
			if _, err := ResolveReadingStudyModel(t.Context(), db, "user", "own:manual"); err == nil {
				t.Fatal("unverified saved model was admitted")
			}
			var calls atomic.Int32
			algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var sent struct {
					Model string `json:"model"`
					Key   string `json:"api_key"`
				}
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil || r.Method != http.MethodPost || r.URL.Path != "/api/model/probe" || sent.Model != "manual" || sent.Key != "fixture-key" {
					t.Errorf("expected one inference probe of the saved model, got %s %s: %v", r.Method, r.URL.Path, err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				provider := db.Model(&orm.UserModelProvider{}).Where("id = ?", "provider-manual")
				model := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "manual")
				var err error
				switch tc.mutation {
				case "key":
					err = provider.Update("active_key_revision", 20).Error
				case "credential":
					err = db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "credential-group").Update("credential_revision", 20).Error
				case "endpoint":
					err = provider.Update("base_url", "https://changed.invalid/v1").Error
				case "protocol":
					err = provider.Update("request_config", `{"default_protocol":"openai-responses"}`).Error
				case "model":
					err = model.Update("max_output_tokens", "400").Error
				case "deletion":
					err = model.Update("deleted_at", time.Now()).Error
				}
				if err != nil {
					t.Error(err)
				}
				_, _ = w.Write([]byte(tc.outcome))
			}))
			defer algorithm.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			req := httptest.NewRequest(http.MethodPost, "/probe"+tc.query, nil)
			req.Header.Set("X-User-Id", "user")
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": "provider-manual", "group_id": "group-manual", "model_id": "manual"})
			rec := httptest.NewRecorder()
			modelprovider.ProbeGroupModel(rec, req)
			var result struct {
				Data modelprovider.ModelProbeResponse
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || rec.Code != 200 || result.Data.Code != tc.wantCode || calls.Load() != 1 {
				t.Fatalf("probe response=%s calls=%d err=%v", rec.Body, calls.Load(), err)
			}
			selected, err := ResolveReadingStudyModel(t.Context(), db, "user", "own:manual")
			if (err == nil) != tc.admitted {
				t.Fatalf("admitted=%t, want %t (err=%v)", err == nil, tc.admitted, err)
			}
			if tc.admitted && PrepareSelectedChatCandidate(t.Context(), db, *selected) == nil {
				t.Fatal("verified reading model cannot be prepared for inference")
			}
			var group orm.UserModelProviderGroup
			if err := db.Take(&group, "id = ?", "other-group").Error; err != nil || group.IsVerified {
				t.Fatalf("verification touched an unrelated group: verified=%t err=%v", group.IsVerified, err)
			}
			defaultModel, err := ResolveReadingStudyModel(t.Context(), db, "user", "")
			if err != nil || defaultModel.ID != "default" {
				t.Fatalf("existing default changed: %v %v", defaultModel, err)
			}
		})
	}
}
