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
)

// A previously saved legacy credential may be unreadable under the current key.
// Listing must still return newly saved groups when the settings page is reopened.
func TestListGroupsIsolatesUnreadableCredentials(t *testing.T) {
	db, parent, healthy := setupEncryptedGroupKeyTest(t, "fixture-healthy-secret")
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "previous-fixture-key")
	ciphertext, err := encodeLegacyModelProviderCiphertext("fixture-old-secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "current-fixture-key")
	unreadable := healthy
	unreadable.ID = "unreadable-group"
	unreadable.APIKeyCiphertext = ciphertext
	unreadable.CredentialVersion = legacyModelProviderCredentialVersion
	unreadable.CreatedAt = healthy.CreatedAt.Add(-time.Hour)
	if err := db.Create(&unreadable).Error; err != nil {
		t.Fatal(err)
	}
	otherOwner := unreadable
	otherOwner.ID = "other-owner-group"
	otherOwner.CreateUserID = "other-user"
	if err := db.Create(&otherOwner).Error; err != nil {
		t.Fatal(err)
	}

	for read := 0; read < 2; read++ {
		req := httptest.NewRequest(http.MethodGet, "/groups", nil)
		req.Header.Set("X-User-Id", "user-1")
		req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID})
		rec := httptest.NewRecorder()
		ListGroups(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("reopening settings returned %d", rec.Code)
		}
		var response struct {
			Data struct {
				Groups []struct {
					ID               string         `json:"id"`
					HasAPIKey        bool           `json:"has_api_key"`
					Keys             []groupKeyItem `json:"keys"`
					IsVerified       bool           `json:"is_verified"`
					CredentialStatus string         `json:"credential_status"`
				} `json:"groups"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data.Groups) != 2 {
			t.Fatalf("got %d groups, want both owned groups", len(response.Data.Groups))
		}
		for _, group := range response.Data.Groups {
			if !group.HasAPIKey {
				t.Fatal("saved credential presence was lost")
			}
			switch group.ID {
			case healthy.ID:
				if group.CredentialStatus != "" || len(group.Keys) != 1 || !group.IsVerified {
					t.Fatal("healthy group changed")
				}
			case unreadable.ID:
				if group.CredentialStatus != "unavailable" || len(group.Keys) != 0 || group.IsVerified {
					t.Fatal("unreadable group must be visible and require credential recovery")
				}
			default:
				t.Fatal("another owner's group was exposed")
			}
		}
		if strings.Contains(rec.Body.String(), "fixture-healthy-secret") || strings.Contains(rec.Body.String(), "fixture-old-secret") || strings.Contains(rec.Body.String(), ciphertext) {
			t.Fatal("list response exposes credentials")
		}
	}
	var stored orm.UserModelProviderGroup
	if err := db.Take(&stored, "id = ?", unreadable.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.APIKeyCiphertext != ciphertext || !stored.IsVerified {
		t.Fatal("reading metadata modified the saved unreadable group")
	}
}

func TestUpdateGroupCanReplaceUnreadableCredential(t *testing.T) {
	for _, tc := range []struct {
		name         string
		category     string
		wantCalls    int
		wantVerified bool
		apiKey       string
		accepted     bool
		wantStatus   int
	}{
		{"model supplier saves before separate verification", defaultProviderCategory, 0, false, "fixture-replacement-secret", true, http.StatusOK},
		{"auxiliary service verifies replacement", "service", 1, true, "fixture-replacement-secret", true, http.StatusOK},
		{"auxiliary service failed verification preserves credential", "service", 1, false, "fixture-replacement-secret", false, http.StatusBadGateway},
		{"missing replacement preserves credential", defaultProviderCategory, 0, false, "", true, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, "fixture-original-secret")
			if err := db.Model(&parent).Update("category", tc.category).Error; err != nil {
				t.Fatal(err)
			}
			const unreadableCiphertext = `{"enc":"aes-gcm","nonce":"invalid","v":"invalid"}`
			if err := db.Model(&group).Updates(map[string]any{"api_key_ciphertext": unreadableCiphertext, "credential_version": 1}).Error; err != nil {
				t.Fatal(err)
			}
			calls := 0
			checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"success": tc.accepted, "message": "fixture check"})
			}))
			defer checker.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", checker.URL)
			body, err := json.Marshal(updateGroupRequest{Name: "Recovered", BaseURL: group.BaseURL, APIKey: tc.apiKey, Verify: true})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPatch, "/groups", strings.NewReader(string(body)))
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
			rec := httptest.NewRecorder()
			UpdateGroup(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d", rec.Code, tc.wantStatus)
			}
			var stored orm.UserModelProviderGroup
			if err := db.Take(&stored, "id = ?", group.ID).Error; err != nil {
				t.Fatal(err)
			}
			if tc.wantStatus == http.StatusOK {
				key, err := apiKeyForGroup(db, &stored)
				if err != nil || key != tc.apiKey || stored.APIKey != "" || stored.IsVerified != tc.wantVerified || stored.CredentialRevision != 2 {
					t.Fatal("replacement was not saved encrypted and verified with a new revision")
				}
			} else if stored.APIKeyCiphertext != unreadableCiphertext || stored.CredentialRevision != 1 || stored.Name != group.Name {
				t.Fatal("unsuccessful replacement modified saved configuration")
			}
			if calls != tc.wantCalls {
				t.Fatalf("unexpected verification calls: %d", calls)
			}
		})
	}
}
