package modelprovider

import (
	"encoding/json"
	"lazymind/core/credentialvault"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestProviderEndpointUpdatePreservesAllConnectionCredentials(t *testing.T) {
	db, parent, first := setupEncryptedGroupKeyTest(t, "first-key\nsecond-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	second := first
	second.ID = "group-2"
	second.Name = "second connection"
	second.APIKeyCiphertext = ""
	second.APIKey = "legacy-key"
	second.CredentialVersion = 0
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	call := func(user, baseURL string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"base_url": baseURL})
		request := httptest.NewRequest(http.MethodPut, "/endpoint", strings.NewReader(string(body)))
		request.Header.Set("X-User-Id", user)
		request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID})
		recorder := httptest.NewRecorder()
		UpdateProviderEndpoint(recorder, request)
		return recorder
	}
	if response := call("other-user", "https://proxy.example/v1"); response.Code != http.StatusNotFound {
		t.Fatalf("ownership: %s", response.Body.String())
	}
	if response := call("user-1", "https://proxy.example/v1"); response.Code != http.StatusOK {
		t.Fatalf("update: %d %s", response.Code, response.Body.String())
	}
	for id, want := range map[string]string{first.ID: "first-key\nsecond-key", second.ID: "legacy-key"} {
		var row orm.UserModelProviderGroup
		if err := db.First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.BaseURL != "https://proxy.example/v1" || row.IsVerified || row.CredentialRevision != 2 {
			t.Fatalf("connection %s endpoint/revision/verification wrong: %+v", id, row)
		}
		if got, err := apiKeyForGroup(db, &row); err != nil || got != want {
			t.Fatalf("key lost for %s: %v", id, err)
		}
	}
	// Saving an unchanged endpoint must not repeatedly invalidate or revise keys.
	if response := call("user-1", "https://proxy.example/v1/"); response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var row orm.UserModelProviderGroup
	db.First(&row, "id = ?", first.ID)
	if row.CredentialRevision != 2 {
		t.Fatal("unchanged endpoint incremented revision")
	}
	var reloaded orm.UserModelProvider
	db.First(&reloaded, "id = ?", parent.ID)
	if normalizeBaseURLForCompare(reloaded.BaseURL) != "https://proxy.example/v1" {
		t.Fatal("supplier endpoint was not persisted")
	}
}

func TestCheckGroupUsesStoredKeyAndSharedEndpoint(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "first-key\nsecond-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		encoded, _ := json.Marshal(body)
		if !strings.Contains(string(encoded), "first-key") || strings.Contains(string(encoded), "second-key") || strings.Contains(string(encoded), "stale.example") {
			t.Errorf("wrong check payload: %s", encoded)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"message":"accepted"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	request := httptest.NewRequest(http.MethodPost, "/check", strings.NewReader(`{"base_url":"https://stale.example/v1"}`))
	request.Header.Set("X-User-Id", "user-1")
	request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	recorder := httptest.NewRecorder()
	CheckGroup(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("check failed: %s", recorder.Body.String())
	}
	var reloaded orm.UserModelProviderGroup
	db.First(&reloaded, "id = ?", group.ID)
	if !reloaded.IsVerified {
		t.Fatal("successful check not persisted")
	}
}

func TestEndpointChangeRejectsInFlightAddKeyWithoutLosingKeys(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "old-key\nkeep-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	request := httptest.NewRequest(http.MethodPost, "/keys", strings.NewReader(`{"api_key":"new-key"}`))
	request.Header.Set("X-User-Id", "user-1")
	request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { recorder := httptest.NewRecorder(); AddKey(recorder, request); result <- recorder }()
	<-started
	err := setProviderEndpoint(t.Context(), db, &parent, "https://new.example/v1", time.Now())
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	response := <-result
	if response.Code != http.StatusConflict {
		t.Fatalf("stale key check persisted: %d %s", response.Code, response.Body.String())
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "old-key\nkeep-key", false)
}

func TestRemoveKeyDoesNotReverifyConnectionAfterEndpointChange(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "old-key\nkeep-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := setProviderEndpoint(t.Context(), db, &parent, "https://new.example/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodDelete, "/keys", strings.NewReader(`{"api_key":"old-key"}`))
	request.Header.Set("X-User-Id", "user-1")
	request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	recorder := httptest.NewRecorder()
	RemoveKey(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatal(recorder.Body.String())
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "keep-key", false)
}

func TestRestoreConnectionKeepsSupplierEndpointAndRequiresRecheck(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	restored := credentialvault.RestoredCredential{
		RecordID: "00000000-0000-7000-8000-000000000401", Revision: 7,
		VaultID: "00000000-0000-7000-8000-000000000201", ETag: strings.Repeat("e", 64),
		Provider: credentialvault.ProviderCredential{ProviderCatalogKey: "fixture-provider", DisplayName: "Backup connection", BaseURL: "https://discarded.example/v1", APIKey: []byte("restored-key")},
	}
	if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var group orm.UserModelProviderGroup
	if err := db.Take(&group).Error; err != nil {
		t.Fatal(err)
	}
	if group.BaseURL != "https://provider.example.test" || group.IsVerified {
		t.Fatalf("old backup changed shared endpoint/verification: %s %v", group.BaseURL, group.IsVerified)
	}
	if key, err := apiKeyForGroup(db, &group); err != nil || key != "restored-key" {
		t.Fatal("restored key unavailable", err)
	}
}

func TestModelSupplierAllowsMultipleConnectionsAtSharedEndpoint(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "shared-endpoint-test")
	parent := orm.UserModelProvider{ID: "p", Name: "OpenAI", BaseURL: "https://shared.example/v1", Category: "model", Capabilities: "custom_base_url,has_models", BaseModel: orm.BaseModel{CreateUserID: "user"}}
	if err := db.DB.Create(&parent).Error; err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one", "two"} {
		request := httptest.NewRequest(http.MethodPost, "/groups", strings.NewReader(`{"name":"`+name+`","base_url":"https://discarded.example/v1","api_key":"key"}`))
		request.Header.Set("X-User-Id", "user")
		request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID})
		recorder := httptest.NewRecorder()
		CreateGroup(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("connection create: %d %s", recorder.Code, recorder.Body.String())
		}
	}
	var groups []orm.UserModelProviderGroup
	db.DB.Where("user_model_provider_id = ?", parent.ID).Find(&groups)
	if len(groups) != 2 {
		t.Fatalf("connections=%d", len(groups))
	}
	for _, group := range groups {
		if normalizeBaseURLForCompare(group.BaseURL) != normalizeBaseURLForCompare(parent.BaseURL) {
			t.Fatal("instance overrode shared endpoint")
		}
	}
}
