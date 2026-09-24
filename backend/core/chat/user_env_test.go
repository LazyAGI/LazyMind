package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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

func seedEnvironmentInput(t *testing.T, db *gorm.DB, target EnvironmentInputRequest) orm.ChatHistory {
	t.Helper()
	conv := orm.Conversation{ID: "input-conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	ext, _ := json.Marshal(map[string]any{"ask_pending": AskPendingEvent{AskID: "input-card", EnvInput: &target}, "ask_answered": false})
	history := orm.ChatHistory{ID: "input-history", ConversationID: conv.ID, Seq: 1, Ext: ext}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	return history
}

func sendEnvironmentInput(owner, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	SubmitEnvironmentInput(rec, newUserEnvRequest(http.MethodPost, "/unused", body, owner, map[string]string{"name": "input-conv"}))
	return rec
}

func TestEnvironmentInputUserValueBypassesHistoryAndIsIdempotent(t *testing.T) {
	db := setupUserEnvTest(t)
	h := seedEnvironmentInput(t, db.DB, EnvironmentInputRequest{Name: "SERVICE_BASE_URL", Scope: "user"})
	body := `{"history_id":"input-history","ask_id":"input-card","value":" synthetic-input-secret "}`
	rec := sendEnvironmentInput("owner", body)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "synthetic-input-secret") {
		t.Fatalf("status=%d", rec.Code)
	}
	var row orm.UserEnvironmentVariable
	if err := db.Where("user_id = ?", "owner").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if value, err := userenv.DecryptValue(row); err != nil || value != " synthetic-input-secret " {
		t.Fatal("input was not encrypted correctly")
	}
	if err := db.First(&h, "id = ?", h.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(h.Ext), "synthetic-input-secret") || strings.Contains(string(h.Ext), "ask_saved_answers") {
		t.Fatal("secret entered history")
	}
	item := chatHistoryToResponseItem(h)
	if item["env_input_result"] == nil || item["ask_answered"] != true {
		t.Fatal("missing safe receipt")
	}
	rec = sendEnvironmentInput("owner", strings.ReplaceAll(body, "synthetic-input-secret", "replayed-secret"))
	if rec.Code != http.StatusOK {
		t.Fatalf("replay status=%d", rec.Code)
	}
	var unchanged orm.UserEnvironmentVariable
	db.First(&unchanged, "id = ?", row.ID)
	if unchanged.ValueCiphertext != row.ValueCiphertext || unchanged.CredentialRevision != row.CredentialRevision {
		t.Fatal("replay changed value")
	}
}

func TestEnvironmentInputRejectsCrossUserStaleAndOrdinaryAnswerPaths(t *testing.T) {
	for _, scenario := range []string{"other_owner", "wrong_card", "old_history", "fork", "archived", "autosave", "structured"} {
		t.Run(scenario, func(t *testing.T) {
			db := setupUserEnvTest(t)
			h := seedEnvironmentInput(t, db.DB, EnvironmentInputRequest{Name: "APP_ID", Scope: "user"})
			owner := "owner"
			body := `{"history_id":"input-history","ask_id":"input-card","value":"synthetic-secret"}`
			switch scenario {
			case "other_owner":
				owner = "other"
			case "wrong_card":
				body = strings.ReplaceAll(body, "input-card", "wrong")
			case "old_history":
				db.Create(&orm.ChatHistory{ID: "new-history", ConversationID: h.ConversationID, Seq: 2})
			case "fork":
				db.Model(&h).Update("ext", strings.Replace(string(h.Ext), "{", `{"fork_read_only":true,`, 1))
			case "archived":
				db.Model(&orm.Conversation{}).Where("id = ?", h.ConversationID).Update("archived_at", time.Now())
			case "autosave":
				rec := httptest.NewRecorder()
				SaveAskAnswers(rec, newUserEnvRequest(http.MethodPatch, "/unused", `{"history_id":"input-history","answers":{"0":{"value":"synthetic-secret"}}}`, owner, nil))
				if rec.Code != http.StatusConflict {
					t.Fatalf("autosave status=%d", rec.Code)
				}
				return
			case "structured":
				if validAskSubmission(map[string]any{"ask_id": "input-card", "env_input": map[string]any{}},
					map[string]any{"ask_id": "input-card", "questions": []any{}}) {
					t.Fatal("ordinary answer accepted")
				}
				return
			}
			rec := sendEnvironmentInput(owner, body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status=%d", rec.Code)
			}
			var count int64
			db.Model(&orm.UserEnvironmentVariable{}).Count(&count)
			if count != 0 {
				t.Fatal("rejected input persisted a value")
			}
		})
	}
}

func TestEnvironmentInputUpdatePreservesMetadataAndRejectsStaleVersion(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			db := setupUserEnvTest(t)
			enabled := false
			created, err := userenv.Create(db.DB, "owner", userenv.CreateRequest{Name: "AWS_REGION", Value: "old", Enabled: &enabled, Description: "keep"})
			if err != nil {
				t.Fatal(err)
			}
			target := EnvironmentInputRequest{Name: created.Name, Scope: "user", ID: created.ID, ExpectedUpdatedAt: &created.UpdatedAt}
			seedEnvironmentInput(t, db.DB, target)
			if stale {
				value := "other"
				if _, err := userenv.Patch(db.DB, "owner", created.ID, userenv.PatchRequest{Value: &value}); err != nil {
					t.Fatal(err)
				}
			}
			rec := sendEnvironmentInput("owner", `{"history_id":"input-history","ask_id":"input-card","value":"new"}`)
			expected := http.StatusOK
			if stale {
				expected = http.StatusConflict
			}
			if rec.Code != expected {
				t.Fatalf("status=%d", rec.Code)
			}
			if !stale && !strings.Contains(rec.Body.String(), `"enabled":false`) {
				t.Fatal("receipt must report that the updated variable remains disabled")
			}
			var row orm.UserEnvironmentVariable
			db.First(&row, "id = ?", created.ID)
			value, _ := userenv.DecryptValue(row)
			want := "new"
			if stale {
				want = "other"
			}
			if row.Enabled || row.Description != "keep" || value != want {
				t.Fatal("unexpected value or metadata")
			}
		})
	}
}

func TestEnvironmentInputSessionAndCancelNeverPersistValue(t *testing.T) {
	for _, scenario := range []string{"save", "cancel", "lost-response"} {
		t.Run(scenario, func(t *testing.T) {
			cancel := scenario != "save"
			status := "configured"
			if scenario == "cancel" {
				status = "canceled"
			}
			db := setupUserEnvTest(t)
			h := seedEnvironmentInput(t, db.DB, EnvironmentInputRequest{Name: "a_api_key", Scope: "conversation"})
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("X-LazyMind-Internal-Token") != "test-internal" {
					t.Error("missing service authentication")
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload["conversation_id"] != h.ConversationID || payload["ask_id"] != "input-card" || payload["cancel"] != cancel {
					t.Error("invalid internal input")
				}
				if (cancel && payload["value"] != nil) || (!cancel && payload["value"] != "temporary-secret") {
					t.Error("incorrect value transmission")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "status": status})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "test-internal")
			body := fmt.Sprintf(`{"history_id":"input-history","ask_id":"input-card","value":"temporary-secret","cancel":%t}`, cancel)
			for i := 0; i < 2; i++ {
				rec := sendEnvironmentInput("owner", body)
				if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "temporary-secret") {
					t.Fatalf("status=%d", rec.Code)
				}
				if !strings.Contains(rec.Body.String(), `"status":"`+status+`"`) {
					t.Fatal("receipt must match the worker's actual outcome")
				}
			}
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			var count int64
			db.Model(&orm.UserEnvironmentVariable{}).Count(&count)
			if count != 0 {
				t.Fatal("session value persisted as user configuration")
			}
			db.First(&h, "id = ?", h.ID)
			if strings.Contains(string(h.Ext), "temporary-secret") {
				t.Fatal("session value persisted in history")
			}
		})
	}
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

func TestUserEnvReservedPersistedNameCannotLoadOrEnable(t *testing.T) {
	db := setupUserEnvTest(t)
	row := orm.UserEnvironmentVariable{
		ID: "env_old_reserved", UserID: "user-reserved", Name: "NODE_TLS_REJECT_UNAUTHORIZED",
		CredentialVersion: userenv.CredentialVersion, CredentialRevision: 1, Enabled: true,
	}
	var err error
	row.ValueCiphertext, err = userenv.EncryptValue(row, "0")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	env, loadErr := userenv.LoadEnabled(context.Background(), db.DB, row.UserID)
	if loadErr == nil || len(env) != 0 {
		t.Fatal("previously stored reserved variable must not enter runtime")
	}
	rec := httptest.NewRecorder()
	replyUserEnvError(rec, loadErr)
	if !strings.Contains(rec.Body.String(), "user_env_invalid_name") || strings.Contains(rec.Body.String(), "credential key") {
		t.Fatalf("policy error must not suggest changing encryption keys: %s", rec.Body.String())
	}
	items, err := userenv.List(db.DB, row.UserID)
	if err != nil || len(items) != 1 || items[0].CredentialStatus != "invalid_name" {
		t.Fatalf("list must identify the invalid name: %v", err)
	}
	enabled := false
	if saved, err := userenv.Patch(db.DB, row.UserID, row.ID, userenv.PatchRequest{Enabled: &enabled}); err != nil || saved.CredentialStatus != "invalid_name" {
		t.Fatalf("must allow disabling reserved variable: %v", err)
	}
	if _, err := userenv.LoadEnabled(context.Background(), db.DB, row.UserID); err != nil {
		t.Fatalf("disabled reserved variable should not block chat: %v", err)
	}
	enabled = true
	if _, err := userenv.Patch(db.DB, row.UserID, row.ID, userenv.PatchRequest{Enabled: &enabled}); err == nil {
		t.Fatal("must not re-enable reserved variable")
	}
	if err := userenv.Delete(db.DB, row.UserID, row.ID, "", nil); err != nil {
		t.Fatalf("must allow deleting reserved variable: %v", err)
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
		if _, err := userenv.NormalizeName(name); err == nil {
			t.Fatalf("expected %s to be rejected", name)
		}
	}
	if got, err := userenv.NormalizeName("tavily_api_key"); err != nil || got != "tavily_api_key" {
		t.Fatalf("normalize credential env = %q, %v", got, err)
	}
}

func TestUserEnvNameSharedContract(t *testing.T) {
	raw, err := os.ReadFile("../../../tests/contracts/env_names.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name  string `json:"name"`
		Valid bool   `json:"valid"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		got, err := userenv.NormalizeName(c.Name)
		if (err == nil) != c.Valid || (c.Valid && got != c.Name) {
			t.Errorf("name %q: got %q, err %v", c.Name, got, err)
		}
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
