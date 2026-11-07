package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestModelListsHideLegacyDefaultsOnlyForUnverifiedCustomSuppliers(t *testing.T) {
	db := setupListProviderTestDB(t)
	store.Init(db, db, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	base := orm.BaseModel{CreateUserID: "owner", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, fixture := range []struct {
		id       string
		custom   bool
		verified bool
	}{
		{id: "custom-unverified", custom: true},
		{id: "custom-verified", custom: true, verified: true},
		{id: "official-unverified"},
	} {
		groupID := fixture.id + "-group"
		provider := orm.UserModelProvider{
			ID: fixture.id, Name: "OpenAI", ProtocolName: "OpenAI", IsCustom: fixture.custom,
			Category: "model", Capabilities: "has_models", BaseURL: "https://gateway.example/v1/",
			ActiveKeyInitialized: true, ActiveKeyGroupID: groupID,
			ActiveKeyID: groupKeyID(groupID, "saved-key"), BaseModel: base,
		}
		group := orm.UserModelProviderGroup{
			ID: groupID, UserModelProviderID: provider.ID, Name: fixture.id,
			BaseURL: provider.BaseURL, APIKey: "saved-key", IsVerified: fixture.verified,
			BaseModel: base,
		}
		models := []orm.UserModelProviderGroupModel{
			{ID: fixture.id + "-default", UserModelProviderID: provider.ID,
				UserModelProviderGroupID: group.ID, ProviderName: provider.Name,
				Name: "catalog-chat", ModelType: "llm", IsDefault: true, BaseModel: base},
			{ID: fixture.id + "-manual", UserModelProviderID: provider.ID,
				UserModelProviderGroupID: group.ID, ProviderName: provider.Name,
				Name: "manual-chat", ModelType: "llm", BaseModel: base},
		}
		for _, row := range []any{&provider, &group, &models} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}

	readIDs := func(rec *httptest.ResponseRecorder) []string {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("model list returned %d: %s", rec.Code, rec.Body.String())
		}
		var response struct {
			Data groupModelListResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(response.Data.Models))
		for _, model := range response.Data.Models {
			ids = append(ids, model.ID)
		}
		return ids
	}

	for _, tc := range []struct {
		providerID string
		want       []string
	}{
		{providerID: "custom-unverified", want: []string{"custom-unverified-manual"}},
		{providerID: "custom-verified", want: []string{"custom-verified-default", "custom-verified-manual"}},
		{providerID: "official-unverified", want: []string{"official-unverified-default", "official-unverified-manual"}},
	} {
		req := httptest.NewRequest(http.MethodGet, "/groups/models", nil)
		req.Header.Set("X-User-Id", "owner")
		req = mux.SetURLVars(req, map[string]string{
			"model_provider_id": tc.providerID, "group_id": tc.providerID + "-group",
		})
		rec := httptest.NewRecorder()
		ListGroupModels(rec, req)
		if got := readIDs(rec); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("group %s models = %v, want %v", tc.providerID, got, tc.want)
		}
		groupReq := httptest.NewRequest(http.MethodGet, "/groups", nil)
		groupReq.Header.Set("X-User-Id", "owner")
		groupReq = mux.SetURLVars(groupReq, map[string]string{"model_provider_id": tc.providerID})
		groupRec := httptest.NewRecorder()
		ListGroups(groupRec, groupReq)
		if groupRec.Code != http.StatusOK {
			t.Fatalf("group list returned %d: %s", groupRec.Code, groupRec.Body.String())
		}
		var groups struct {
			Data struct {
				Groups []struct {
					ID         string `json:"id"`
					ModelCount int    `json:"model_count"`
				} `json:"groups"`
			} `json:"data"`
		}
		if err := json.Unmarshal(groupRec.Body.Bytes(), &groups); err != nil {
			t.Fatal(err)
		}
		if len(groups.Data.Groups) != 1 || groups.Data.Groups[0].ID != tc.providerID+"-group" || groups.Data.Groups[0].ModelCount != len(tc.want) {
			t.Errorf("group %s count does not match visible models: %s", tc.providerID, groupRec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/models?model_type=llm", nil)
	req.Header.Set("X-User-Id", "owner")
	rec := httptest.NewRecorder()
	ListUserModelsByModelType(rec, req)
	want := []string{
		"custom-unverified-manual", "custom-verified-default", "custom-verified-manual",
		"official-unverified-default", "official-unverified-manual",
	}
	if got := readIDs(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("cross-provider models = %v, want %v", got, want)
	}
}
