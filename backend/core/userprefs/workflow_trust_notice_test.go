package userprefs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/store"
)

func TestWorkflowTrustNoticePreferencePersistsAndIsUserScoped(t *testing.T) {
	db := newUIPreferencesTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	request := func(method, user, body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, "/api/core/user/ui-preferences", strings.NewReader(body))
		req.Header.Set("X-User-Id", user)
		rec := httptest.NewRecorder()
		if method == http.MethodGet {
			GetUIPreferences(rec, req)
		} else {
			PatchUIPreferences(rec, req)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Data
	}
	if value := request(http.MethodGet, "u1", "")["workflow_trust_notice_dismissed"]; value != false {
		t.Fatalf("new users must see notice, got %v", value)
	}
	request(http.MethodPatch, "u1", `{"workflow_trust_notice_dismissed":true}`)
	request(http.MethodPatch, "u1", `{"chat_preference_notice_dismissed":true}`)
	prefs := request(http.MethodGet, "u1", "")
	if prefs["workflow_trust_notice_dismissed"] != true || prefs["chat_preference_notice_dismissed"] != true || prefs["workflows_enabled"] != true || prefs["developer_mode_active"] != false {
		t.Fatalf("dismissal changed unrelated settings or was not retained: %#v", prefs)
	}
	if value := request(http.MethodGet, "u2", "")["workflow_trust_notice_dismissed"]; value != false {
		t.Fatalf("dismissal leaked to another user: %v", value)
	}
	request(http.MethodPatch, "u2", `{"developer_mode_active":true}`)
	request(http.MethodPatch, "u2", `{"workflow_trust_notice_dismissed":true}`)
	if value := request(http.MethodGet, "u2", "")["workflow_trust_notice_dismissed"]; value != true {
		t.Fatalf("existing row not updated: %v", value)
	}
}
