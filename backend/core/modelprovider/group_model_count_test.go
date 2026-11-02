package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestListGroupsReturnsOwnedActiveModelCountsWithoutCredentials(t *testing.T) {
	for _, withModelTable := range []bool{true, false} {
		name := "model-only and empty groups"
		if !withModelTable {
			name = "minimal schema without model table"
		}
		t.Run(name, func(t *testing.T) {
			database := orm.MigrateTestDB(t, &orm.UserModelProvider{}, &orm.UserModelProviderGroup{})
			db := database.DB
			store.Init(db, db, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if withModelTable {
				if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}); err != nil {
					t.Fatal(err)
				}
			}
			base := orm.BaseModel{CreateUserID: "count-owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
			provider := orm.UserModelProvider{ID: "count-provider", Name: "OpenAI", Category: "model", Capabilities: "has_models", ActiveKeyInitialized: true, BaseModel: base}
			if err := db.Create(&provider).Error; err != nil {
				t.Fatal(err)
			}
			groups := []orm.UserModelProviderGroup{
				{ID: "count-model-only", UserModelProviderID: provider.ID, Name: "Models", BaseModel: base},
				{ID: "count-empty", UserModelProviderID: provider.ID, Name: "Empty", BaseModel: base},
			}
			if err := db.Create(&groups).Error; err != nil {
				t.Fatal(err)
			}
			if withModelTable {
				deleted := time.Now()
				models := []orm.UserModelProviderGroupModel{
					{ID: "count-active-own", Name: "own-model-detail", UserModelProviderID: provider.ID, UserModelProviderGroupID: groups[0].ID, ModelType: "llm", BaseModel: base},
					{ID: "count-active-catalog", Name: "catalog-model-detail", UserModelProviderID: provider.ID, UserModelProviderGroupID: groups[0].ID, ModelType: "embed", IsDefault: true, BaseModel: base},
					{ID: "count-deleted", Name: "deleted-model-detail", UserModelProviderID: provider.ID, UserModelProviderGroupID: groups[0].ID, ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: base.CreateUserID, DeletedAt: &deleted}},
					{ID: "count-foreign-owner", Name: "foreign-model-detail", UserModelProviderID: provider.ID, UserModelProviderGroupID: groups[0].ID, ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "another-owner"}},
					{ID: "count-foreign-provider", Name: "other-provider-detail", UserModelProviderID: "another-provider", UserModelProviderGroupID: groups[0].ID, ModelType: "llm", BaseModel: base},
				}
				if err := db.Create(&models).Error; err != nil {
					t.Fatal(err)
				}
			}
			request := httptest.NewRequest(http.MethodGet, "/groups", nil)
			request.Header.Set("X-User-Id", base.CreateUserID)
			request = mux.SetURLVars(request, map[string]string{"model_provider_id": provider.ID})
			recorder := httptest.NewRecorder()
			ListGroups(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("list groups: %d %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Data struct {
					Groups []struct {
						ID         string `json:"id"`
						ModelCount *int   `json:"model_count"`
						HasAPIKey  bool   `json:"has_api_key"`
					} `json:"groups"`
				} `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Data.Groups) != 2 {
				t.Fatalf("groups: %s", recorder.Body.String())
			}
			for _, group := range response.Data.Groups {
				want := 0
				if withModelTable && group.ID == groups[0].ID {
					want = 2
				}
				if group.ModelCount == nil || *group.ModelCount != want {
					t.Errorf("group %s model_count=%v want %d; response=%s", group.ID, group.ModelCount, want, recorder.Body.String())
				}
				if group.HasAPIKey {
					t.Error("model-only fixture unexpectedly has credentials")
				}
			}
			if strings.Contains(recorder.Body.String(), "model-detail") || strings.Contains(recorder.Body.String(), "provider-detail") {
				t.Fatal("group metadata exposed model content")
			}
		})
	}
}
