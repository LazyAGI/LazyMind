package remotefs

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestAtomicSkillInstallCreatesDisabledPackageAndSafelyReusesIt(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	archive := filepath.Join(t.TempDir(), "skill.zip")
	testutil.WriteSkillZip(t, archive, map[string][]byte{"SKILL.md": []byte("---\nname: example\ndescription: Atomic package.\n---\nBody.\n"), "assets/binary.png": testutil.MinimalPNGBytes()})
	data, _ := os.ReadFile(archive)
	body, _ := json.Marshal(map[string]string{"package_zip_base64": base64.StdEncoding.EncodeToString(data), "expected_name": "example"})
	var first map[string]any
	for _, task := range []string{"task-a", "task-b"} {
		rec := httptest.NewRecorder()
		handler.InstallSkill(rec, httptest.NewRequest(http.MethodPost, "/remote-fs/skills:install?user_id=user&task_id="+task, strings.NewReader(string(body))))
		if rec.Code != 200 {
			t.Fatalf("%s status=%d body=%s", task, rec.Code, rec.Body.String())
		}
		var result map[string]any
		json.Unmarshal(rec.Body.Bytes(), &result)
		if first == nil {
			first = result
		} else if result["skill_id"] != first["skill_id"] || result["status"] != "already_installed" {
			t.Fatalf("first=%v retry=%v", first, result)
		}
	}
	var active int64
	db.Table("skills").Where("deleted_at IS NULL AND is_enabled = ?", false).Count(&active)
	if active != 1 {
		t.Fatalf("disabled active packages=%d", active)
	}
}
func TestAtomicSkillInstallRequiresUserTaskAndValidBody(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	for _, tc := range []struct {
		query, body string
		want        int
	}{{"", "{}", 401}, {"?user_id=user", "{}", 400}, {"?user_id=user&task_id=task", "{\"package_zip_base64\":\"not base64\"}", 400}, {"?user_id=user&task_id=task", "{\"url\":\"https://example.test/a.zip\"} {}", 400}} {
		rec := httptest.NewRecorder()
		handler.InstallSkill(rec, httptest.NewRequest(http.MethodPost, "/remote-fs/skills:install"+tc.query, strings.NewReader(tc.body)))
		if rec.Code != tc.want {
			t.Fatalf("query=%s status=%d want=%d body=%s", tc.query, rec.Code, tc.want, rec.Body.String())
		}
	}
	var count int64
	db.Table("skills").Count(&count)
	if count != 0 {
		t.Fatalf("partial packages=%d", count)
	}
}

func TestAtomicSkillInstallRetainsPackageErrorSemantics(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	archive := filepath.Join(t.TempDir(), "invalid.zip")
	testutil.WriteSkillZip(t, archive, map[string][]byte{"README.md": []byte("Missing root document.")})
	data, _ := os.ReadFile(archive)
	body, _ := json.Marshal(map[string]string{"package_zip_base64": base64.StdEncoding.EncodeToString(data)})
	recorder := httptest.NewRecorder()
	handler.InstallSkill(recorder, httptest.NewRequest(http.MethodPost, "/remote-fs/skills:install?user_id=user&task_id=chat", strings.NewReader(string(body))))
	if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "skill_md_not_found") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	invalidURL := httptest.NewRecorder()
	handler.InstallSkill(invalidURL, httptest.NewRequest(http.MethodPost, "/remote-fs/skills:install?user_id=user&task_id=chat", strings.NewReader(`{"url":"file:///etc/passwd"}`)))
	if invalidURL.Code != http.StatusBadRequest {
		t.Fatalf("invalid URL status=%d body=%s", invalidURL.Code, invalidURL.Body.String())
	}
}
func TestAtomicSkillInstallDatabaseFailureIsInternalError(t *testing.T) {
	db := testutil.NewTestDB(t)
	handler := NewHandler(HandlerDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	db.Exec("DROP TABLE skills")
	recorder := httptest.NewRecorder()
	handler.InstallSkill(recorder, httptest.NewRequest(http.MethodPost, "/remote-fs/skills:install?user_id=user&task_id=chat", strings.NewReader(`{"url":"https://example.test/skill.zip"}`)))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("database failure status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
