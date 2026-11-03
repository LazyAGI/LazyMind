package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestAddressEditsPreserveSavedKeysAndEnabledSelection(t *testing.T) {
	for _, tc := range []struct {
		name, category, body string
		handler              http.HandlerFunc
		verify               bool
	}{
		{"supplier endpoint", "model", `{"base_url":"https://new.example/v1/","api_key":""}`, UpdateProviderEndpoint, false},
		{"request protocol", "model", `{"default_protocol":"anthropic","endpoints":{"anthropic":"https://new.example/anthropic"}}`, SetProviderRequestConfig, false},
		{"model group mirror", "model", `{"base_url":"https://new.example/v1/","api_key":""}`, UpdateGroup, false},
		{"auxiliary group", "ocr", `{"base_url":"https://new.example/v1/","api_key":""}`, UpdateGroup, false},
		{"auxiliary group with check", "ocr", `{"base_url":"https://new.example/v1/","verify":true}`, UpdateGroup, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}).DB
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			const pool = "first-saved-key\nsecond-saved-key"
			base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			parent := orm.UserModelProvider{ID: "provider", Name: "Fixture", IsCustom: true, ProtocolName: "OpenAI", Category: tc.category, Capabilities: "custom_base_url,has_models", BaseURL: "https://old.example/v1/", ActiveKeyInitialized: true, ActiveKeyGroupID: "group", ActiveKeyID: groupKeyID("group", "second-saved-key"), BaseModel: base}
			if tc.name == "model group mirror" {
				parent.BaseURL = "https://new.example/v1/"
			}
			ciphertext, err := encryptModelProviderAPIKeyForGroup("owner", "group", 1, pool)
			if err != nil {
				t.Fatal(err)
			}
			group := orm.UserModelProviderGroup{ID: "group", UserModelProviderID: parent.ID, Name: parent.Name, BaseURL: "https://old.example/v1/", APIKeyCiphertext: ciphertext, CredentialVersion: modelProviderCredentialVersion, CredentialRevision: 1, IsVerified: true, BaseModel: base}
			if err := db.Create(&parent).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&group).Error; err != nil {
				t.Fatal(err)
			}
			if tc.verify {
				algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request algoModelCheckBody
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.APIKey != pool || request.SkipAuth {
						t.Error("address verification discarded the saved credential")
					}
					_, _ = w.Write([]byte(`{"success":true}`))
				}))
				defer algorithm.Close()
				t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
			}
			request := httptest.NewRequest(http.MethodPut, "/address", strings.NewReader(tc.body))
			request.Header.Set("X-User-Id", "owner")
			request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
			response := httptest.NewRecorder()
			tc.handler(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("address edit=%d: %s", response.Code, response.Body.String())
			}
			if err := db.Take(&group, "id = ?", group.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Take(&parent, "id = ?", parent.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got, err := apiKeyForGroup(db, &group); err != nil || got != pool {
				t.Fatalf("address edit lost saved keys: %v", err)
			}
			if group.CredentialVersion != modelProviderCredentialVersion || group.CredentialRevision != 2 || group.APIKey != "" || group.APIKeyCiphertext == "" {
				t.Fatalf("address edit corrupted credential envelope/version: version=%d revision=%d", group.CredentialVersion, group.CredentialRevision)
			}
			if parent.ActiveKeyGroupID != "group" || parent.ActiveKeyID != groupKeyID("group", "second-saved-key") {
				t.Fatal("address edit changed enabled key")
			}
			if group.IsVerified != tc.verify {
				t.Fatalf("verified=%v, want %v", group.IsVerified, tc.verify)
			}
			if tc.category == "model" {
				listRequest := httptest.NewRequest(http.MethodGet, "/groups", nil)
				listRequest.Header.Set("X-User-Id", "owner")
				listRequest = mux.SetURLVars(listRequest, map[string]string{"model_provider_id": parent.ID})
				listed := httptest.NewRecorder()
				ListGroups(listed, listRequest)
				var payload struct {
					Data groupListResponse `json:"data"`
				}
				if err := json.Unmarshal(listed.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if listed.Code != http.StatusOK || len(payload.Data.Groups) != 1 || len(payload.Data.Groups[0].Keys) != 2 || !payload.Data.Groups[0].Keys[1].Enabled {
					t.Fatal("address edit hid saved/enabled keys in API metadata")
				}
			}
		})
	}
}
