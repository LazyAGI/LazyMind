package modelprovider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
)

func TestExplicitModelRuntimeUsesEnabledKeyAfterModelListUnavailable(t *testing.T) {
	db, parent, group, _ := setupProbeTest(t)
	checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"reason":"model_list_unavailable"}`))
	}))
	defer checker.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", checker.URL)
	req := httptest.NewRequest(http.MethodPost, "/check", strings.NewReader(`{}`))
	req.Header.Set("X-User-Id", parent.CreateUserID)
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	rec := httptest.NewRecorder()
	CheckGroup(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	endpoint, key, err := ResolveProviderRuntimeCredential(t.Context(), db, group.ID)
	if err != nil || key != "active-test-key" || endpoint != parent.BaseURL {
		t.Fatalf("explicit model blocked by list-only check: %q %v", endpoint, err)
	}
	db.Take(&group, "id = ?", group.ID)
	if group.IsVerified {
		t.Fatal("runtime resolution marked supplier verified")
	}
	db.Model(&parent).Updates(map[string]any{"active_key_id": "", "active_key_group_id": ""})
	if _, _, err := ResolveProviderRuntimeCredential(t.Context(), db, group.ID); !errors.Is(err, ErrProviderKeyDisabled) {
		t.Fatal("disabled key bypassed after list failure", err)
	}
}

func TestSupplierCredentialSaveDoesNotRequireModelsEndpoint(t *testing.T) {
	for _, action := range []string{"create", "update", "add", "replace", "enable"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "credential-save-test")
			db, parent, group, _ := setupProbeTest(t, &orm.UserSelectedModel{})
			var calls atomic.Int32
			checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "models unsupported", http.StatusNotFound)
			}))
			defer checker.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", checker.URL)
			method, body := http.MethodPost, `{"api_key":"new-save-key"}`
			vars := map[string]string{"model_provider_id": parent.ID, "group_id": group.ID, "key_id": groupKeyID(group.ID, "active-test-key")}
			handler := AddKey
			switch action {
			case "create":
				db.Model(&group).Update("deleted_at", time.Now())
				body = `{"name":"Local gateway","api_key":"new-save-key","verify":true}`
				handler = CreateGroup
			case "update":
				method = http.MethodPatch
				body = `{"api_key":"new-save-key","verify":true}`
				handler = UpdateGroup
			case "replace":
				method = http.MethodPut
				handler = ReplaceKey
			case "enable":
				body = `{"group_id":"probe-group","key_id":"` + groupKeyID(group.ID, "inactive-test-key") + `"}`
				handler = SetActiveProviderKey
			}
			req := httptest.NewRequest(method, "/save", strings.NewReader(body))
			req.Header.Set("X-User-Id", parent.CreateUserID)
			req = mux.SetURLVars(req, vars)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("save failed before explicit check: %d %s", rec.Code, rec.Body.String())
			}
			if calls.Load() != 0 {
				t.Fatalf("saving config unexpectedly contacted provider: %d", calls.Load())
			}
			var saved orm.UserModelProviderGroup
			query := db.Where("user_model_provider_id = ? AND deleted_at IS NULL", parent.ID)
			if query.Take(&saved).Error != nil {
				t.Fatal("saved configuration missing")
			}
			if saved.IsVerified {
				t.Fatal("saving credentials must not mark them verified")
			}
			pool, err := readProbeKeyPool(saved)
			if err != nil {
				t.Fatal(err)
			}
			wantKey := "new-save-key"
			if action == "enable" {
				wantKey = "inactive-test-key"
			}
			if !strings.Contains(pool, wantKey) {
				t.Fatal("saved key missing")
			}
			if strings.Contains(rec.Body.String(), wantKey) {
				t.Fatal("save response leaked secret")
			}
			var active orm.UserModelProvider
			db.Take(&active, "id = ?", parent.ID)
			if action == "add" && active.ActiveKeyID != parent.ActiveKeyID {
				t.Fatal("adding inactive key changed current selection")
			}
			if action == "replace" && active.ActiveKeyID != groupKeyID(group.ID, wantKey) {
				t.Fatal("replacement lost active slot")
			}
			if action == "enable" && active.ActiveKeyID != groupKeyID(group.ID, wantKey) {
				t.Fatal("explicit selection was not saved")
			}
		})
	}
}
