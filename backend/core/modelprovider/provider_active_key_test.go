package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
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

func TestSupplierHasOneActiveKeyAcrossAllConnections(t *testing.T) {
	db, parent, first := setupEncryptedGroupKeyTest(t, "first-key\nsecond-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	second := first
	second.ID = "second-group"
	second.Name = "Second"
	second.APIKey = "third-key"
	second.APIKeyCiphertext = ""
	second.CredentialVersion = 1
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	resolve := func(group, want string) {
		t.Helper()
		got, err := ResolveProviderAPIKey(context.Background(), db, group)
		if err != nil || got != want {
			t.Fatalf("key for %s = %q want %q: %v", group, got, want, err)
		}
	}
	resolve(first.ID, "first-key")
	resolve(second.ID, "first-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body algoModelCheckBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.APIKey != "third-key" {
			t.Errorf("activation checked wrong key: %q", body.APIKey)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	activate := func(group, key string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"group_id": group, "key_id": key})
		req := httptest.NewRequest(http.MethodPut, "/active", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID})
		rec := httptest.NewRecorder()
		SetActiveProviderKey(rec, req)
		return rec
	}
	if rec := activate(second.ID, groupKeyID(second.ID, "third-key")); rec.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", rec.Code, rec.Body.String())
	}
	resolve(first.ID, "third-key")
	resolve(second.ID, "third-key")
	if rec := activate("", ""); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, err := ResolveProviderAPIKey(context.Background(), db, first.ID); !errors.Is(err, ErrProviderKeyDisabled) {
		t.Fatalf("disabled pool still usable: %v", err)
	}
	// A read cannot silently revive an explicitly disabled supplier.
	if _, err := ResolveProviderAPIKey(context.Background(), db, second.ID); !errors.Is(err, ErrProviderKeyDisabled) {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.UserModelProviderGroup{}).Where("user_model_provider_id = ?", parent.ID).Count(&count)
	if count != 2 {
		t.Fatal("activation removed connection")
	}
}

func TestInactiveKeyChecksDoNotChangeSelectionOrVerification(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "chosen-key\ninactive-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
		t.Fatal(err)
	}
	if err := setProviderEndpoint(t.Context(), db, &parent, "https://new.example/v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	call := func(handler http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/keys", strings.NewReader(body))
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	if rec := call(AddKey, http.MethodPost, `{"api_key":"new-inactive-key"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var reloaded orm.UserModelProviderGroup
	db.First(&reloaded, "id = ?", group.ID)
	if reloaded.IsVerified {
		t.Fatal("checking an inactive addition verified the active credential")
	}
	if _, err := ResolveProviderAPIKey(t.Context(), db, group.ID); !errors.Is(err, ErrProviderKeyUnverified) {
		t.Fatalf("unverified active key usable: %v", err)
	}
	if rec := call(CheckGroup, http.MethodPost, `{"api_key":"inactive-key"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	db.First(&reloaded, "id = ?", group.ID)
	if reloaded.IsVerified {
		t.Fatal("explicit inactive key probe verified current key")
	}
	if rec := call(CheckGroup, http.MethodPost, `{}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if got, err := ResolveProviderAPIKey(t.Context(), db, group.ID); err != nil || got != "chosen-key" {
		t.Fatalf("active recheck wrong credential: %v", err)
	}
}

func TestFailedActivationAndForeignKeysKeepPreviousChoice(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "chosen-key\nrejected-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
		t.Fatal(err)
	}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":false,"message":"invalid credential"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	activate := func(user, groupID, keyID string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(activeProviderKeyRequest{GroupID: groupID, KeyID: keyID})
		req := httptest.NewRequest(http.MethodPut, "/active", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", user)
		req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID})
		rec := httptest.NewRecorder()
		SetActiveProviderKey(rec, req)
		return rec
	}
	if rec := activate("other-user", group.ID, groupKeyID(group.ID, "chosen-key")); rec.Code != http.StatusNotFound {
		t.Fatal("cross-owner activation accepted")
	}
	if rec := activate("user-1", "other-group", groupKeyID(group.ID, "chosen-key")); rec.Code != http.StatusNotFound {
		t.Fatal("foreign connection accepted")
	}
	if rec := activate("user-1", group.ID, groupKeyID(group.ID, "missing-key")); rec.Code != http.StatusNotFound {
		t.Fatal("nonexistent key accepted")
	}
	if calls != 0 {
		t.Fatal("invalid requests probed an upstream")
	}
	if rec := activate("user-1", group.ID, groupKeyID(group.ID, "rejected-key")); rec.Code != http.StatusBadGateway {
		t.Fatalf("rejected key status=%d", rec.Code)
	}
	if got, err := ResolveProviderAPIKey(t.Context(), db, group.ID); err != nil || got != "chosen-key" {
		t.Fatal("failed switch replaced chosen key", err)
	}
}

func TestExplicitDisableSurvivesAddingAndListingKeys(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "chosen-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.Transaction(func(tx *gorm.DB) error { return saveActiveProviderKey(tx, &parent, "", "", false, time.Now()) }); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	req := httptest.NewRequest(http.MethodPost, "/keys", strings.NewReader(`{"api_key":"saved-inactive-key"}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	AddKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	listReq := httptest.NewRequest(http.MethodGet, "/groups", nil)
	listReq.Header.Set("X-User-Id", "user-1")
	listReq = mux.SetURLVars(listReq, map[string]string{"model_provider_id": parent.ID})
	list := httptest.NewRecorder()
	ListGroups(list, listReq)
	var response struct {
		Data groupListResponse `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Groups) != 1 || len(response.Data.Groups[0].Keys) != 2 {
		t.Fatal("saved keys were lost")
	}
	for _, key := range response.Data.Groups[0].Keys {
		if key.Enabled {
			t.Fatal("listing/addition revived disabled key")
		}
	}
	if _, err := ResolveProviderAPIKey(t.Context(), db, group.ID); !errors.Is(err, ErrProviderKeyDisabled) {
		t.Fatal("disabled supplier resolved", err)
	}
}

func TestRemovingActiveKeyDoesNotEnableNextSavedKey(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "chosen-key\nother-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if _, err := ResolveProviderAPIKey(t.Context(), db, group.ID); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/keys", strings.NewReader(`{"api_key":"chosen-key"}`))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	RemoveKey(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, err := ResolveProviderAPIKey(t.Context(), db, group.ID); !errors.Is(err, ErrProviderKeyDisabled) {
		t.Fatal("successor was silently enabled", err)
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "other-key", false)
}

func TestActiveKeyAppliesToConnectionWithoutItsOwnKeys(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "chosen-key\nother-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	empty := group
	empty.ID = "empty-connection"
	empty.APIKey = ""
	empty.APIKeyCiphertext = ""
	empty.IsVerified = false
	if err := db.Create(&empty).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		key, err := ResolveProviderAPIKey(t.Context(), db, empty.ID)
		if err != nil || key != "chosen-key" {
			t.Fatalf("request %d used different saved key: %v", i, err)
		}
	}
	db.First(&parent, "id = ?", parent.ID)
	if !parent.ActiveKeyInitialized || parent.ActiveKeyGroupID != group.ID {
		t.Fatal("legacy selection was not persisted")
	}
}

func TestNoSavedKeysAllowsVerifiedCustomEndpoint(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.DefaultModelProvider{}); err != nil {
		t.Fatal(err)
	}
	parent.DefaultModelProviderID = "official"
	parent.BaseURL = "https://custom.example/v1"
	if err := db.Save(&parent).Error; err != nil {
		t.Fatal(err)
	}
	catalog := orm.DefaultModelProvider{ID: "official", Name: parent.Name, BaseURL: "https://official.example/v1"}
	if err := db.Create(&catalog).Error; err != nil {
		t.Fatal(err)
	}
	baseURL, key, err := ResolveProviderRuntimeCredential(t.Context(), db, group.ID)
	if err != nil || key != "" || baseURL != parent.BaseURL {
		t.Fatalf("keyless custom endpoint unavailable: %s %v", baseURL, err)
	}
}

func TestOlderActivationCannotOverrideLaterExplicitDisable(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "chosen-key\nnext-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	request := func(groupID, keyID string) *http.Request {
		body, _ := json.Marshal(activeProviderKeyRequest{GroupID: groupID, KeyID: keyID})
		req := httptest.NewRequest(http.MethodPut, "/active", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", "user-1")
		return mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID})
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		SetActiveProviderKey(rec, request(group.ID, groupKeyID(group.ID, "next-key")))
		result <- rec
	}()
	<-started
	disabled := httptest.NewRecorder()
	SetActiveProviderKey(disabled, request("", ""))
	close(release)
	if disabled.Code != http.StatusOK {
		t.Fatal(disabled.Body.String())
	}
	if rec := <-result; rec.Code != http.StatusConflict {
		t.Fatalf("older activation revived disabled keys: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := ResolveProviderAPIKey(t.Context(), db, group.ID); !errors.Is(err, ErrProviderKeyDisabled) {
		t.Fatal("later disable was lost", err)
	}
}

func TestAddKeyRejectsUnrestorablePoolBeforeUpstreamCheck(t *testing.T) {
	keys := make([]string, 31)
	for i := range keys {
		keys[i] = fmt.Sprintf("%02d-", i) + strings.Repeat("x", 509)
	}
	db, parent, group := setupEncryptedGroupKeyTest(t, strings.Join(keys, "\n"))
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := validateSavedKeyPool(parent, group.Name, parent.BaseURL, strings.Join(keys, "\n")); err != nil {
		t.Fatal("fixture must fit backup payload", err)
	}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"success":true}`)) }))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	body, _ := json.Marshal(addKeyRequest{APIKey: strings.Repeat("z", 512)})
	req := httptest.NewRequest(http.MethodPost, "/keys", strings.NewReader(string(body)))
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	AddKey(rec, req)
	if rec.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("oversized pool accepted/probed: status=%d calls=%d", rec.Code, calls)
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, strings.Join(keys, "\n"), true)
}

func TestTrustedRestoreRetainsCombinedPoolOver512Bytes(t *testing.T) {
	sink, db, scope := newCredentialRestoreSinkFixture(t)
	pool := strings.Repeat("a", 300) + "\n" + strings.Repeat("b", 300)
	restored := credentialvault.RestoredCredential{RecordID: "00000000-0000-7000-8000-000000000401", Revision: 7, VaultID: "00000000-0000-7000-8000-000000000201", ETag: strings.Repeat("e", 64), Provider: credentialvault.ProviderCredential{ProviderCatalogKey: "fixture-provider", DisplayName: "Saved pool", BaseURL: "https://provider.example.test", APIKey: []byte(pool)}}
	if err := sink.PersistTrusted(t.Context(), scope, []credentialvault.RestoredCredential{restored}, map[string]credentialvault.ConflictResolution{restored.RecordID: credentialvault.ConflictFail}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var group orm.UserModelProviderGroup
	if err := db.Take(&group).Error; err != nil {
		t.Fatal(err)
	}
	if got, err := apiKeyForGroup(db, &group); err != nil || got != pool {
		t.Fatal("restored pool lost keys", err)
	}
	if got, err := ResolveProviderAPIKey(t.Context(), db, group.ID); err != nil || got != strings.Repeat("a", 300) {
		t.Fatal("restore exposed entire pool or wrong active key", err)
	}
}
