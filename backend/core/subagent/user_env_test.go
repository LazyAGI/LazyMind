package subagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/userenv"
)

func seedTaskUserEnv(t *testing.T, db *orm.DB, id, user, name, value string, enabled bool) orm.UserEnvironmentVariable {
	t.Helper()
	row := orm.UserEnvironmentVariable{ID: id, UserID: user, Name: name, Enabled: enabled,
		CredentialVersion: userenv.CredentialVersion, CredentialRevision: 1}
	var err error
	row.ValueCiphertext, err = userenv.EncryptValue(row, value)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func TestRunRequestRefreshesEnvironmentFromTaskOwner(t *testing.T) {
	db := remoteSubagentFixture(t)
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY", "test-only-user-env-secret-key-32-bytes")
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY_FILE", "")
	row := seedTaskUserEnv(t, db, "env-1", "user-1", "Mixed_API_KEY", "owner-secret", true)
	seedTaskUserEnv(t, db, "env-2", "user-2", "Mixed_API_KEY", "other-secret", true)
	seedTaskUserEnv(t, db, "env-3", "user-1", "DISABLED_TOKEN", "disabled-secret", false)
	deleted := seedTaskUserEnv(t, db, "env-4", "user-1", "DELETED_TOKEN", "deleted-secret", true)
	if err := db.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	req := RunRequest{TaskID: "task-remote", UserEnvVars: map[string]string{"FORGED_TOKEN": "forged-secret"}}
	check := func(want map[string]string) {
		t.Helper()
		if err := hydrateRunRequest(context.Background(), db.DB, &req); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(req.UserEnvVars, want) {
			t.Fatal("unexpected runtime environment")
		}
		snapshot, err := json.Marshal(req.TaskSpec)
		if err != nil || strings.Contains(string(snapshot), "secret") || strings.Contains(string(snapshot), "user_env_vars") {
			t.Fatal("credentials entered the task snapshot")
		}
	}
	check(map[string]string{"Mixed_API_KEY": "owner-secret"})
	// A supplied snapshot must not select a different user's credentials on resume.
	req.TaskSpec["create_user_id"] = "user-2"
	req.Resume = true
	row.CredentialRevision++
	ciphertext, err := userenv.EncryptValue(row, "updated-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&row).Updates(map[string]any{"credential_revision": row.CredentialRevision, "value_ciphertext": ciphertext}).Error; err != nil {
		t.Fatal(err)
	}
	check(map[string]string{"Mixed_API_KEY": "updated-secret"})
	if err := db.Model(&row).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	check(nil)
	if err := db.Model(&row).Updates(map[string]any{"enabled": true, "value_ciphertext": "corrupt"}).Error; err != nil {
		t.Fatal(err)
	}
	req.UserEnvVars = map[string]string{"STALE_TOKEN": "stale-secret"}
	if err := hydrateRunRequest(context.Background(), db.DB, &req); err == nil || err.Error() != "user environment configuration unavailable" {
		t.Fatal("unreadable enabled credentials must fail closed with a generic error")
	}
	if req.UserEnvVars != nil {
		t.Fatal("failed resolution retained stale credentials")
	}
}

func TestRemoteExecutionSpecRefreshesPrivateEnvironment(t *testing.T) {
	db := remoteSubagentFixture(t)
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "task-remote").Update("params", json.RawMessage(`{}`)).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY", "test-only-user-env-secret-key-32-bytes")
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY_FILE", "")
	row := seedTaskUserEnv(t, db, "env-1", "user-1", "Mixed_API_KEY", "owner-secret", true)
	seedTaskUserEnv(t, db, "env-2", "user-2", "Mixed_API_KEY", "other-secret", true)
	fetch := func(lease string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/internal/subagent/tasks/task-remote/execution-spec", nil)
		req = mux.SetURLVars(req, map[string]string{"task_id": "task-remote"})
		req.Header.Set("Authorization", "Bearer executor-secret")
		req.Header.Set("X-Workflow-Lease-Token", lease)
		rec := httptest.NewRecorder()
		InternalGetExecutionSpec(rec, req)
		return rec
	}
	if rec := fetch("stale"); rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "owner-secret") {
		t.Fatal("stale lease received execution credentials")
	}
	rec := fetch("lease-live")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	data := getData(rec.Body.Bytes())
	if !reflect.DeepEqual(data["user_env_vars"], map[string]any{"Mixed_API_KEY": "owner-secret"}) {
		t.Fatal("execution spec did not resolve task owner's credentials")
	}
	delete(data, "user_env_vars")
	public, _ := json.Marshal(data)
	if strings.Contains(string(public), "owner-secret") || strings.Contains(rec.Body.String(), "other-secret") {
		t.Fatal("credentials leaked outside private runtime field")
	}
	if err := db.Delete(&row).Error; err != nil {
		t.Fatal(err)
	}
	rec = fetch("lease-live")
	if rec.Code != http.StatusOK || getData(rec.Body.Bytes())["user_env_vars"] != nil {
		t.Fatal("deleted credentials remained available on next execution")
	}
	row = seedTaskUserEnv(t, db, "env-3", "user-1", "Mixed_API_KEY", "replacement-secret", true)
	if err := db.Model(&row).Update("value_ciphertext", "corrupt").Error; err != nil {
		t.Fatal(err)
	}
	rec = fetch("lease-live")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "replacement-secret") {
		t.Fatal("unreadable credentials did not fail closed")
	}
	task, err := GetTask(context.Background(), db.DB, "task-remote")
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := json.Marshal(task)
	if strings.Contains(string(persisted), "secret") || strings.Contains(string(persisted), "user_env_vars") {
		t.Fatal("runtime credentials persisted in task")
	}
}
