package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
	"lazymind/core/userenv"
)

func setupUserEnvTest(t *testing.T) *orm.DB {
	t.Helper()
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY", "test-only-user-env-secret-key-32-bytes")
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY_FILE", "")
	db := newPromptTestDB(t)
	corestore.Init(db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
	return db
}

func newUserEnvRequest(method, path, body, userID string, vars map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	return req
}

func decodeUserEnvEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return payload
}

func TestUserEnvironmentVariableCRUDDoesNotReturnPlaintext(t *testing.T) {
	setupUserEnvTest(t)
	secret := "tvly-secret-value-123456"
	createReq := newUserEnvRequest(
		http.MethodPost,
		"/api/core/user/env-vars",
		`{"name":"tavily_api_key","value":"`+secret+`","description":"search"}`,
		"user-1",
		nil,
	)
	createRec := httptest.NewRecorder()
	CreateUserEnvironmentVariable(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status: %d body=%s", createRec.Code, createRec.Body.String())
	}
	if strings.Contains(createRec.Body.String(), secret) {
		t.Fatalf("create response leaked plaintext secret: %s", createRec.Body.String())
	}
	createPayload := decodeUserEnvEnvelope(t, createRec)
	data, _ := createPayload["data"].(map[string]any)
	if data["name"] != "tavily_api_key" {
		t.Fatalf("expected env name casing to be preserved, got %#v", data["name"])
	}
	if masked, _ := data["masked_value"].(string); masked == "" || masked == secret {
		t.Fatalf("expected masked value, got %q", masked)
	}

	listReq := newUserEnvRequest(http.MethodGet, "/api/core/user/env-vars", "", "user-1", nil)
	listRec := httptest.NewRecorder()
	ListUserEnvironmentVariables(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status: %d body=%s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), secret) {
		t.Fatalf("list response leaked plaintext secret: %s", listRec.Body.String())
	}
}

func TestApplyUserEnvironmentRuntimeConfigOnlyInjectsEnabledVars(t *testing.T) {
	db := setupUserEnvTest(t)
	ctx := context.Background()
	enabled := orm.UserEnvironmentVariable{
		ID:                 "env_enabled",
		UserID:             "user-2",
		Name:               "OPENAI_API_KEY",
		CredentialVersion:  userenv.CredentialVersion,
		CredentialRevision: 1,
		Enabled:            true,
	}
	disabled := orm.UserEnvironmentVariable{
		ID:                 "env_disabled",
		UserID:             "user-2",
		Name:               "TAVILY_API_KEY",
		CredentialVersion:  userenv.CredentialVersion,
		CredentialRevision: 1,
		Enabled:            false,
	}
	var err error
	enabled.ValueCiphertext, err = userenv.EncryptValue(enabled, "enabled-secret")
	if err != nil {
		t.Fatalf("encrypt enabled: %v", err)
	}
	disabled.ValueCiphertext, err = userenv.EncryptValue(disabled, "disabled-secret")
	if err != nil {
		t.Fatalf("encrypt disabled: %v", err)
	}
	if err := db.Create(&enabled).Error; err != nil {
		t.Fatalf("create enabled row: %v", err)
	}
	if err := db.Create(&disabled).Error; err != nil {
		t.Fatalf("create disabled row: %v", err)
	}

	body := map[string]any{}
	if err := applyUserEnvironmentRuntimeConfig(ctx, db.DB, "user-2", body); err != nil {
		t.Fatalf("apply runtime config: %v", err)
	}
	env, _ := body["user_env_vars"].(map[string]string)
	if got := env["OPENAI_API_KEY"]; got != "enabled-secret" {
		t.Fatalf("enabled env value = %q", got)
	}
	if _, ok := env["TAVILY_API_KEY"]; ok {
		t.Fatalf("disabled env should not be injected: %#v", env)
	}
}

func TestPatchUserEnvironmentVariableRenamingReencryptsValue(t *testing.T) {
	db := setupUserEnvTest(t)
	ctx := context.Background()
	row := orm.UserEnvironmentVariable{
		ID:                 "env_rename",
		UserID:             "user-3",
		Name:               "TAVILY_API_KEY",
		CredentialVersion:  userenv.CredentialVersion,
		CredentialRevision: 1,
		Enabled:            true,
	}
	var err error
	row.ValueCiphertext, err = userenv.EncryptValue(row, "rename-secret")
	if err != nil {
		t.Fatalf("encrypt row: %v", err)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create row: %v", err)
	}

	req := newUserEnvRequest(
		http.MethodPatch,
		"/api/core/user/env-vars/env_rename",
		`{"name":"SERPAPI_API_KEY"}`,
		"user-3",
		map[string]string{"id": row.ID},
	)
	rec := httptest.NewRecorder()
	PatchUserEnvironmentVariable(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status: %d body=%s", rec.Code, rec.Body.String())
	}

	body := map[string]any{}
	if err := applyUserEnvironmentRuntimeConfig(ctx, db.DB, "user-3", body); err != nil {
		t.Fatalf("apply runtime config: %v", err)
	}
	env, _ := body["user_env_vars"].(map[string]string)
	if got := env["SERPAPI_API_KEY"]; got != "rename-secret" {
		t.Fatalf("renamed env value = %q", got)
	}
	if _, ok := env["TAVILY_API_KEY"]; ok {
		t.Fatalf("old env name should not be injected: %#v", env)
	}
}

func TestNormalizeUserEnvNameRejectsRuntimeControlNames(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "SSL_CERT_FILE", "BASH_ENV", "PYTHONPATH"} {
		if _, err := normalizeUserEnvName(name); err == nil {
			t.Fatalf("expected %s to be rejected", name)
		}
	}
	if got, err := normalizeUserEnvName("tavily_api_key"); err != nil || got != "tavily_api_key" {
		t.Fatalf("normalize credential env = %q, %v", got, err)
	}
}

func TestUserEnvQueryErrorsDoNotMatchMigrationCatalog(t *testing.T) {
	appErr := common.ResolveAppError("Failed to query user environment variables", http.StatusInternalServerError)
	if appErr.Message == "Database migration configuration is invalid" {
		t.Fatalf("user env query error must not resolve to migration error")
	}
}

func TestEncryptUserEnvValueWorksWithoutOSKeyStore(t *testing.T) {
	setupUserEnvTest(t)
	row := orm.UserEnvironmentVariable{ID: "env_crypto", UserID: "user-crypto", Name: "TEST_API_KEY", CredentialVersion: userenv.CredentialVersion, CredentialRevision: 1}
	secret := "server-side-secret-value"
	ciphertext, err := userenv.EncryptValue(row, secret)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.Contains(ciphertext, secret) {
		t.Fatalf("ciphertext leaked plaintext")
	}
	row.ValueCiphertext = ciphertext
	got, err := userenv.DecryptValue(row)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != secret {
		t.Fatalf("decrypt = %q, want %q", got, secret)
	}
}
