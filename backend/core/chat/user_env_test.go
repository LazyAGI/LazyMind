package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

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

func TestUserEnvDeletionLateAutosaveCannotRestoreConfirmation(t *testing.T) {
	for _, decision := range []string{"__ask_user_yes__", "__ask_user_no__"} {
		t.Run(decision, func(t *testing.T) {
			db := setupUserEnvTest(t)
			conv := orm.Conversation{ID: "autosave-conv", BaseModel: orm.BaseModel{CreateUserID: "user-1"}}
			if err := db.Create(&conv).Error; err != nil {
				t.Fatal(err)
			}
			row := orm.UserEnvironmentVariable{ID: "autosave-env", UserID: "user-1", Name: "test_api_key", ValueCiphertext: "synthetic", UpdatedAt: time.Now().UTC().Truncate(time.Microsecond)}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			pending := AskPendingEvent{
				AskID: "autosave-card", Questions: []AskQuestion{{Text: "Delete?", Type: "boolean", Choices: []string{"__ask_user_yes__", "__ask_user_no__"}}},
				UserEnvDelete: &UserEnvDeleteConfirmation{ID: row.ID, Name: row.Name, ExpectedUpdatedAt: row.UpdatedAt},
			}
			ext, err := json.Marshal(map[string]any{"ask_pending": pending, "ask_answered": false})
			if err != nil {
				t.Fatal(err)
			}
			history := orm.ChatHistory{ID: "autosave-history", ConversationID: conv.ID, Ext: ext}
			if err := db.Create(&history).Error; err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(`{"ask_id":"autosave-card","questions":[{"text":"Delete?","type":"boolean","choices":["__ask_user_yes__","__ask_user_no__"],"custom_choices":["__ask_user_yes__","__ask_user_no__"],"answer":{"type":"boolean","value":"__ask_user_no__"}}]}`), &payload); err != nil {
				t.Fatal(err)
			}
			payload["questions"].([]any)[0].(map[string]any)["answer"].(map[string]any)["value"] = decision
			injected := false
			if err := db.Callback().Update().Before("gorm:begin_transaction").Register("test:late-autosave", func(tx *gorm.DB) {
				if injected || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "chat_histories" {
					return
				}
				injected = true
				// Consume the card after autosave reads it, but before its stale write.
				if _, err := submitUserEnvDeletion(context.Background(), db.DB, row.UserID, []orm.ChatHistory{history}, payload); err != nil {
					t.Fatal(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer db.Callback().Update().Remove("test:late-autosave")
			rec := httptest.NewRecorder()
			SaveAskAnswers(rec, newUserEnvRequest(http.MethodPost, "/unused", `{"history_id":"autosave-history","answers":{"0":{"type":"boolean","value":"__ask_user_yes__"}}}`, row.UserID, nil))
			if rec.Code != http.StatusNoContent || !injected {
				t.Fatalf("autosave status=%d injected=%v", rec.Code, injected)
			}
			var saved orm.ChatHistory
			if err := db.First(&saved, "id = ?", history.ID).Error; err != nil {
				t.Fatal(err)
			}
			var state map[string]any
			if err := json.Unmarshal(saved.Ext, &state); err != nil {
				t.Fatal(err)
			}
			if state["ask_answered"] != true {
				t.Fatal("autosave resurrected a consumed confirmation")
			}
			answer := state["ask_saved_answers"].(map[string]any)["0"].(map[string]any)
			if answer["value"] != decision {
				t.Fatalf("autosave overwrote decision: %v", answer)
			}
			payload["questions"].([]any)[0].(map[string]any)["answer"].(map[string]any)["value"] = "__ask_user_yes__"
			if _, err := submitUserEnvDeletion(context.Background(), db.DB, row.UserID, []orm.ChatHistory{saved}, payload); err == nil {
				t.Fatal("consumed confirmation was replayed")
			}
			var count int64
			if err := db.Model(&orm.UserEnvironmentVariable{}).Where("id = ?", row.ID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if (count == 1) != (decision == "__ask_user_no__") {
				t.Fatalf("unexpected variable count: %d", count)
			}
		})
	}
}

func TestUserEnvDeletionRequiresPersistedMatchingConfirmation(t *testing.T) {
	for _, scenario := range []string{"confirm", "cancel", "wrong_owner", "wrong_card", "missing_answer", "changed", "renamed", "replaced", "already_answered", "forged_target", "removed_card"} {
		t.Run(scenario, func(t *testing.T) {
			db := setupUserEnvTest(t)
			row := orm.UserEnvironmentVariable{
				ID: "env-delete", UserID: "user-1", Name: "a_api_key", Enabled: false,
				ValueCiphertext: "unreadable-ciphertext", UpdatedAt: time.Now().UTC().Truncate(time.Microsecond),
			}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			pending := AskPendingEvent{
				AskID:         "delete-card",
				Questions:     []AskQuestion{{Text: "Delete a_api_key?", Type: "boolean", Choices: []string{"__ask_user_yes__", "__ask_user_no__"}}},
				UserEnvDelete: &UserEnvDeleteConfirmation{ID: row.ID, Name: row.Name, ExpectedUpdatedAt: row.UpdatedAt},
			}
			ext, err := json.Marshal(map[string]any{"ask_pending": pending, "ask_answered": scenario == "already_answered"})
			if err != nil {
				t.Fatal(err)
			}
			history := orm.ChatHistory{ID: "history-delete", ConversationID: "conversation-delete", Ext: ext}
			if err := db.Create(&history).Error; err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(`{"ask_id":"delete-card","questions":[{"text":"Delete a_api_key?","type":"boolean","choices":["__ask_user_yes__","__ask_user_no__"],"custom_choices":["__ask_user_yes__","__ask_user_no__"],"answer":{"type":"boolean","value":"__ask_user_yes__"}}]}`), &payload); err != nil {
				t.Fatal(err)
			}
			owner := row.UserID
			answer := payload["questions"].([]any)[0].(map[string]any)["answer"].(map[string]any)
			switch scenario {
			case "cancel":
				answer["value"] = "__ask_user_no__"
			case "missing_answer":
				answer["value"] = nil
			case "wrong_owner":
				owner = "another-user"
			case "wrong_card":
				payload["ask_id"] = "forged-card"
			case "changed", "renamed":
				updates := map[string]any{"updated_at": row.UpdatedAt.Add(time.Second)}
				if scenario == "renamed" {
					updates["name"] = "renamed_api_key"
				}
				if err := db.Model(&row).Updates(updates).Error; err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err := db.Delete(&row).Error; err != nil {
					t.Fatal(err)
				}
				row.ID = "replacement"
				row.DeletedAt.Valid = false
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			case "forged_target":
				payload["user_env_delete"] = map[string]any{"id": "other-env", "name": "other_api_key"}
			case "removed_card":
				if err := db.Model(&orm.ChatHistory{}).Where("id = ?", history.ID).Update("ext", []byte(`{}`)).Error; err != nil {
					t.Fatal(err)
				}
			}
			histories := []orm.ChatHistory{history}
			result, err := submitUserEnvDeletion(context.Background(), db.DB, owner, histories, payload)
			success := scenario == "confirm" || scenario == "cancel" || scenario == "forged_target"
			if (err == nil) != success {
				t.Fatalf("success=%v error=%v", success, err)
			}
			if success && result == "" {
				t.Fatal("missing authoritative continuation")
			}
			var count int64
			if err := db.Model(&orm.UserEnvironmentVariable{}).Where("user_id = ?", row.UserID).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			deleted := scenario == "confirm" || scenario == "forged_target"
			if (count == 0) != deleted {
				t.Fatalf("unexpected active row count: %d", count)
			}
			if success {
				// Re-read persisted state even if a concurrent request holds old history.
				if _, err := submitUserEnvDeletion(context.Background(), db.DB, owner, []orm.ChatHistory{history}, payload); err == nil {
					t.Fatal("replayed confirmation was accepted")
				}
				var saved orm.ChatHistory
				if err := db.First(&saved, "id = ?", history.ID).Error; err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(saved.Ext), `"ask_answered":true`) || !strings.Contains(string(saved.Ext), "ask_saved_answers") {
					t.Fatal("confirmation answer not persisted")
				}
			}
		})
	}
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
