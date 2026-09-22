package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/userenv"
)

func TestUserEnvRejectsStaleClientEdits(t *testing.T) {
	db := setupUserEnvTest(t)
	row := userEnvFixture()
	row.UpdatedAt = time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	var err error
	row.ValueCiphertext, err = userenv.EncryptValue(row, "synthetic-original-value")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	patch := func(payload map[string]any, status int) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		PatchUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPatch, "/user/env-vars/"+row.ID, string(body), row.UserID, map[string]string{"id": row.ID}))
		if rec.Code != status {
			t.Fatalf("patch status=%d, want %d", rec.Code, status)
		}
	}
	read := func() orm.UserEnvironmentVariable {
		t.Helper()
		var stored orm.UserEnvironmentVariable
		if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
			t.Fatal(err)
		}
		return stored
	}
	patch(map[string]any{"enabled": false, "expected_updated_at": row.UpdatedAt}, http.StatusOK)
	disabled := read()
	for _, changes := range []map[string]any{
		{"description": "stale note"},
		{"enabled": true},
		{"name": "RENAMED_TOKEN"},
		{"value": "synthetic-stale-value"},
	} {
		changes["expected_updated_at"] = row.UpdatedAt
		patch(changes, http.StatusConflict)
		stored := read()
		if stored.Enabled || stored.Name != disabled.Name || stored.Description != disabled.Description ||
			stored.ValueCiphertext != disabled.ValueCiphertext || stored.CredentialRevision != disabled.CredentialRevision ||
			!stored.UpdatedAt.Equal(disabled.UpdatedAt) {
			t.Fatal("stale client changed the current record")
		}
	}
	patch(map[string]any{"description": "fresh note", "expected_updated_at": disabled.UpdatedAt}, http.StatusOK)
	stored := read()
	if stored.Enabled || stored.Description != "fresh note" {
		t.Fatal("fresh partial edit lost the disabled state")
	}
	// Chat callers can still omit the client precondition without replaying UI fields.
	patch(map[string]any{"value": "synthetic-tool-value"}, http.StatusOK)
	stored = read()
	if stored.Enabled || stored.Description != "fresh note" {
		t.Fatal("tool replacement lost metadata")
	}
}

func TestUserEnvClientVersionUsesDatabaseTimestampPrecision(t *testing.T) {
	db := setupUserEnvTest(t)
	row := userEnvFixture()
	row.UpdatedAt = time.Date(2026, 9, 20, 1, 2, 3, 123456000, time.UTC)
	var err error
	row.ValueCiphertext, err = userenv.EncryptValue(row, "synthetic-original-value")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	// An old response may include sub-microsecond precision lost in PostgreSQL storage.
	body, _ := json.Marshal(map[string]any{
		"description":         "precision note",
		"expected_updated_at": row.UpdatedAt.Add(789 * time.Nanosecond).In(time.FixedZone("offset", 8*60*60)),
	})
	rec := httptest.NewRecorder()
	PatchUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPatch, "/user/env-vars/"+row.ID, string(body), row.UserID, map[string]string{"id": row.ID}))
	if rec.Code != http.StatusOK {
		t.Fatalf("equivalent client timestamp rejected: %d", rec.Code)
	}
	var response struct {
		Data userEnvVariableResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	var stored orm.UserEnvironmentVariable
	if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !stored.UpdatedAt.Equal(response.Data.UpdatedAt) || stored.UpdatedAt.Nanosecond()%1000 != 0 {
		t.Fatal("write response timestamp differs from stored client version")
	}
}
