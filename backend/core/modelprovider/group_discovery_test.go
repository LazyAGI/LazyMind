package modelprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestCreateGroupDefersSupplierModelsToRemoteDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name              string
		category          string
		existingSelection bool
		wantSeeded        int64
	}{
		{name: "new supplier", category: defaultProviderCategory},
		{name: "supplier with existing default", category: defaultProviderCategory, existingSelection: true},
		{name: "other service retains catalog seeding", category: "legacy-service", wantSeeded: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "group-discovery-test")
			db := orm.MigrateTestDB(t,
				&orm.DefaultModelProvider{}, &orm.DefaultModel{}, &orm.UserModelProvider{},
				&orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{}, &orm.UserSelectedModel{},
			).DB
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			now := time.Now()
			base := orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}
			catalog := orm.DefaultModelProvider{ID: "catalog", Name: "Discovery fixture", BaseURL: "https://discovery.example/v1/", Category: tc.category, CreatedAt: now, UpdatedAt: now}
			parent := orm.UserModelProvider{ID: "supplier", DefaultModelProviderID: catalog.ID, Name: catalog.Name, BaseURL: catalog.BaseURL, Category: tc.category, Capabilities: "custom_base_url,has_models", BaseModel: base}
			if err := db.Create(&catalog).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&parent).Error; err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 6; i++ {
				model := orm.DefaultModel{ID: fmt.Sprintf("catalog-%d", i), DefaultModelProviderID: catalog.ID, ProviderName: catalog.Name, Name: fmt.Sprintf("catalog-model-%d", i), ModelType: "llm", CreatedAt: now, UpdatedAt: now}
				if err := db.Create(&model).Error; err != nil {
					t.Fatal(err)
				}
			}
			preserved := orm.UserModelProviderGroupModel{ID: "existing-model", UserModelProviderID: "existing-supplier", UserModelProviderGroupID: "existing-group", ProviderName: "Existing", Name: "existing-chat", ModelType: "llm", BaseModel: base}
			if err := db.Create(&preserved).Error; err != nil {
				t.Fatal(err)
			}
			if tc.existingSelection {
				selection := orm.UserSelectedModel{UserID: base.CreateUserID, ModelKey: "llm", UserModelProviderGroupModelID: preserved.ID, CreatedAt: now, UpdatedAt: now}
				if err := db.Create(&selection).Error; err != nil {
					t.Fatal(err)
				}
			}

			checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			defer checker.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", checker.URL)
			req := httptest.NewRequest(http.MethodPost, "/groups", strings.NewReader(`{"name":"Discovery fixture","base_url":"https://discovery.example/v1/","api_key":"fixture-key","verify":true}`))
			req.Header.Set("X-User-Id", base.CreateUserID)
			req = mux.SetURLVars(req, map[string]string{"model_provider_id": parent.ID})
			rec := httptest.NewRecorder()
			CreateGroup(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("create group: %d %s", rec.Code, rec.Body.String())
			}
			var response struct {
				Data createGroupResponse `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if !response.Data.IsVerified || response.Data.ID == "" {
				t.Fatalf("expected verified saved credentials: %s", rec.Body.String())
			}
			var seeded int64
			if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("user_model_provider_group_id = ?", response.Data.ID).Count(&seeded).Error; err != nil {
				t.Fatal(err)
			}
			if seeded != tc.wantSeeded {
				t.Fatalf("model count = %d, want %d; supplier models must come from discovery", seeded, tc.wantSeeded)
			}
			var selections []orm.UserSelectedModel
			if err := db.Find(&selections).Error; err != nil {
				t.Fatal(err)
			}
			wantSelections := 0
			if tc.existingSelection {
				wantSelections = 1
			}
			if len(selections) != wantSelections || len(selections) > 0 && selections[0].UserModelProviderGroupModelID != preserved.ID {
				t.Fatalf("creating credentials changed default models: %+v", selections)
			}
			var existing orm.UserModelProviderGroupModel
			if err := db.First(&existing, "id = ?", preserved.ID).Error; err != nil || existing.Name != preserved.Name || existing.DeletedAt != nil {
				t.Fatalf("existing user model changed: %+v %v", existing, err)
			}
		})
	}
}
