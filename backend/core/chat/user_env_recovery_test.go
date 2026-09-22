package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
	"lazymind/core/userenv"
)

func TestUserEnvMaskBoundaries(t *testing.T) {
	for _, value := range []string{"", "12345678", "123456789", "1234567890123456", strings.Repeat("界", 16)} {
		if maskUserEnvValue(value) != "••••••••" {
			t.Fatal("short credential must be fully masked")
		}
	}
	for _, value := range []string{"12345678901234567", strings.Repeat("界", 17)} {
		runes := []rune(value)
		if maskUserEnvValue(value) != string(runes[:4])+"****"+string(runes[len(runes)-4:]) {
			t.Fatal("long credential must expose only four characters at each end")
		}
	}
}

func TestUserEnvPreservesCredentialWhitespace(t *testing.T) {
	db := setupUserEnvTest(t)
	for _, value := range []string{" leading-password", "trailing-password ", " both-password "} {
		body, _ := json.Marshal(map[string]string{"name": "SERVICE_PASSWORD", "value": value})
		rec := httptest.NewRecorder()
		CreateUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPost, "/user/env-vars", string(body), "owner", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("create status=%d", rec.Code)
		}
		var row orm.UserEnvironmentVariable
		if err := db.First(&row, "user_id = ?", "owner").Error; err != nil {
			t.Fatal(err)
		}
		if env, err := userenv.LoadEnabled(context.Background(), db.DB, "owner"); err != nil || env[row.Name] != value {
			t.Fatal("create changed credential whitespace")
		}
		for _, replacement := range []string{value, " replacement-password "} {
			body, _ = json.Marshal(map[string]string{"value": replacement})
			rec = httptest.NewRecorder()
			PatchUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPatch, "/user/env-vars/"+row.ID, string(body), "owner", map[string]string{"id": row.ID}))
			if rec.Code != http.StatusOK {
				t.Fatalf("patch status=%d", rec.Code)
			}
			env, err := userenv.LoadEnabled(context.Background(), db.DB, "owner")
			if err != nil || env[row.Name] != replacement {
				t.Fatal("credential whitespace changed before runtime injection")
			}
		}
		if err := db.Delete(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestUserEnvUnreadableCredentialRecovery(t *testing.T) {
	for _, missingKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt", true: "missing-key"}[missingKey], func(t *testing.T) {
			db := setupUserEnvTest(t)
			row := userEnvFixture()
			var err error
			row.ValueCiphertext, err = userenv.EncryptValue(row, "synthetic-private-value")
			if err != nil {
				t.Fatal(err)
			}
			if missingKey {
				t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY", "")
				restore := modelprovider.SetCredentialKeyManager(nil)
				defer restore()
			} else {
				row.ValueCiphertext = "corrupt"
			}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			patch := func(body string, status int) {
				t.Helper()
				rec := httptest.NewRecorder()
				PatchUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPatch, "/user/env-vars/"+row.ID, body, row.UserID, map[string]string{"id": row.ID}))
				if rec.Code != status {
					t.Fatalf("patch status=%d, want %d", rec.Code, status)
				}
			}
			if _, err := userenv.LoadEnabled(context.Background(), db.DB, row.UserID); err == nil {
				t.Fatal("unreadable enabled credential must fail closed")
			}
			rec := httptest.NewRecorder()
			ListUserEnvironmentVariables(rec, newUserEnvRequest(http.MethodGet, "/user/env-vars", "", row.UserID, nil))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"credential_status":"unavailable"`) || !strings.Contains(rec.Body.String(), row.Name) || strings.Contains(rec.Body.String(), row.ValueCiphertext) {
				t.Fatal("unreadable credential metadata must remain manageable without exposing ciphertext")
			}
			patch(`{"name":"OTHER_TOKEN"}`, http.StatusInternalServerError)
			patch(`{"description":"recovery note"}`, http.StatusOK)
			patch(`{"enabled":false}`, http.StatusOK)
			var stored orm.UserEnvironmentVariable
			if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.ValueCiphertext != row.ValueCiphertext || stored.CredentialRevision != row.CredentialRevision || stored.Name != row.Name || stored.Enabled {
				t.Fatal("metadata recovery modified the unreadable credential")
			}
			patch(`{"enabled":true}`, http.StatusInternalServerError)
			if env, err := userenv.LoadEnabled(context.Background(), db.DB, row.UserID); err != nil || len(env) != 0 {
				t.Fatal("disabled unreadable credential blocked runtime")
			}
			if missingKey {
				patch(`{"value":" replacement-password "}`, http.StatusInternalServerError)
				t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY", "test-only-user-env-secret-key-32-bytes")
			}
			patch(`{"name":"RENAMED_TOKEN","value":" replacement-password ","enabled":true}`, http.StatusOK)
			if env, err := userenv.LoadEnabled(context.Background(), db.DB, row.UserID); err != nil || env["RENAMED_TOKEN"] != " replacement-password " {
				t.Fatal("replacement did not restore runtime credential")
			}
		})
	}
}

func TestUserEnvListKeepsHealthyRowsAndDeletesUnreadableRows(t *testing.T) {
	db := setupUserEnvTest(t)
	for _, broken := range []bool{false, true} {
		row := userEnvFixture()
		row.ID = map[bool]string{false: "healthy", true: "broken"}[broken]
		row.Name = strings.ToUpper(row.ID) + "_TOKEN"
		var err error
		row.ValueCiphertext, err = userenv.EncryptValue(row, "synthetic-private-value")
		if err != nil {
			t.Fatal(err)
		}
		if broken {
			row.ValueCiphertext = "corrupt"
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	ListUserEnvironmentVariables(rec, newUserEnvRequest(http.MethodGet, "/user/env-vars", "", "user-crypto", nil))
	var response struct {
		Data listUserEnvVariablesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || rec.Code != http.StatusOK || len(response.Data.Items) != 2 {
		t.Fatal("one bad credential must not hide the rest of the list")
	}
	for _, item := range response.Data.Items {
		want := "available"
		if item.ID == "broken" {
			want = "unavailable"
		}
		if item.CredentialStatus != want {
			t.Fatal("incorrect credential status")
		}
	}
	rec = httptest.NewRecorder()
	DeleteUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodDelete, "/user/env-vars/broken", "", "user-crypto", map[string]string{"id": "broken"}))
	if rec.Code != http.StatusOK {
		t.Fatal("unreadable credential could not be deleted")
	}
}

func TestUserEnvReplacesUnreadableLegacyCredential(t *testing.T) {
	db := setupUserEnvTest(t)
	row := userEnvFixture()
	row.CredentialVersion = 1
	row.ValueCiphertext = "corrupt-legacy"
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	PatchUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPatch, "/user/env-vars/"+row.ID, `{"value":"new-private-value"}`, row.UserID, map[string]string{"id": row.ID}))
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy replacement status=%d", rec.Code)
	}
	var stored orm.UserEnvironmentVariable
	if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CredentialVersion != userenv.CredentialVersion || stored.CredentialRevision != row.CredentialRevision+1 {
		t.Fatal("replacement did not upgrade the credential version")
	}
	if value, err := userenv.DecryptValue(stored); err != nil || value != "new-private-value" {
		t.Fatal("replacement is not decryptable")
	}
}
