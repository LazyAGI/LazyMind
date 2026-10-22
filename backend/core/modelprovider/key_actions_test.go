package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func savedKeyRequest(method, user, providerID, groupID, keyID, body string) *http.Request {
	req := httptest.NewRequest(method, "/keys/"+keyID, strings.NewReader(body))
	req.Header.Set("X-User-Id", user)
	return mux.SetURLVars(req, map[string]string{"model_provider_id": providerID, "group_id": groupID, "key_id": keyID})
}

func TestRevealKeyIsExplicitOwnerScopedAndNeverCached(t *testing.T) {
	_, parent, group := setupEncryptedGroupKeyTest(t, "saved-secret-one\nsaved-secret-two")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, tc := range []struct {
		user, keyID string
		status      int
	}{
		{"user-1", groupKeyID(group.ID, "saved-secret-two"), http.StatusOK},
		{"another-user", groupKeyID(group.ID, "saved-secret-two"), http.StatusNotFound},
		{"user-1", "missing", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		RevealKey(rec, savedKeyRequest(http.MethodPost, tc.user, parent.ID, group.ID, tc.keyID, ""))
		if rec.Code != tc.status || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status/cache=%d/%s", rec.Code, rec.Header().Get("Cache-Control"))
		}
		if tc.status == http.StatusOK {
			var response struct {
				Data struct {
					APIKey string `json:"api_key"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.Data.APIKey != "saved-secret-two" {
				t.Fatal("explicit reveal did not return exactly the selected key")
			}
		} else if strings.Contains(rec.Body.String(), "saved-secret") {
			t.Fatal("failed reveal leaked a credential")
		}
	}
}

func TestReplaceKeyRetainsOneEnabledSlotAndEncryptedPool(t *testing.T) {
	for _, target := range []string{"first-key", "second-key"} {
		t.Run(target, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, "first-key\nsecond-key")
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body algoModelCheckBody
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.APIKey != "replacement-key" {
					t.Error("wrong replacement check")
				}
				w.Write([]byte(`{"success":true}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			rec := httptest.NewRecorder()
			ReplaceKey(rec, savedKeyRequest(http.MethodPut, "user-1", parent.ID, group.ID, groupKeyID(group.ID, target), `{"api_key":"replacement-key"}`))
			if rec.Code != http.StatusOK {
				t.Fatalf("replace status=%d: %s", rec.Code, rec.Body.String())
			}
			wantPool, wantActive := "first-key\nreplacement-key", "first-key"
			if target == "first-key" {
				wantPool, wantActive = "replacement-key\nsecond-key", "replacement-key"
			}
			assertStoredEncryptedAPIKeys(t, db, group.ID, wantPool, true)
			var current orm.UserModelProvider
			db.Take(&current, "id = ?", parent.ID)
			if current.ActiveKeyID != groupKeyID(group.ID, wantActive) {
				t.Fatal("replacement lost or changed enabled key slot")
			}
			if strings.Contains(rec.Body.String(), "replacement-key") {
				t.Fatal("replace response leaked plaintext")
			}
			var response struct {
				Data struct {
					KeyID   string `json:"key_id"`
					Enabled bool   `json:"enabled"`
				} `json:"data"`
			}
			if json.Unmarshal(rec.Body.Bytes(), &response) != nil || response.Data.KeyID != groupKeyID(group.ID, "replacement-key") || response.Data.Enabled != (target == "first-key") {
				t.Fatal("replacement metadata incorrect")
			}
		})
	}
}

func TestReplaceKeyRejectsFailedVerificationWithoutChangingStoredKey(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "first-key\nsecond-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"message":"rejected"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	rec := httptest.NewRecorder()
	ReplaceKey(rec, savedKeyRequest(http.MethodPut, "user-1", parent.ID, group.ID, groupKeyID(group.ID, "first-key"), `{"api_key":"replacement-key"}`))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", rec.Code)
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "first-key\nsecond-key", true)
}
