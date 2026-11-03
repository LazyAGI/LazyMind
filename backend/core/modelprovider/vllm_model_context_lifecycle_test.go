package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"lazymind/core/common/orm"
)

func TestVLLMDiscoveredContextSurvivesUnclassifiedSaveAndClassification(t *testing.T) {
	db, parent, group := unclassifiedModelFixture(t)
	var servingLength atomic.Int64
	servingLength.Store(262144)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected keyless discovery request: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "opaque-serving-9fdb7", "max_model_len": servingLength.Load()}}})
	}))
	defer server.Close()
	previousAllowPrivateHosts := remoteModelsAllowPrivateHosts
	remoteModelsAllowPrivateHosts = true
	t.Cleanup(func() { remoteModelsAllowPrivateHosts = previousAllowPrivateHosts })
	parent.Name, parent.IsCustom, parent.BaseURL = "vLLM", false, server.URL+"/v1/"
	parent.ActiveKeyID, parent.ActiveKeyGroupID = "", ""
	group.APIKey, group.BaseURL = "", parent.BaseURL
	if err := db.Save(&parent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Save(&group).Error; err != nil {
		t.Fatal(err)
	}
	discover := func() remoteGroupModelItem {
		t.Helper()
		rec := unclassifiedModelCall(t, ListRemoteGroupModels, parent, group, "", "")
		var response struct {
			Data remoteGroupModelsResponse `json:"data"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Data.Models) != 1 {
			t.Fatalf("vLLM discovery=%d %s", rec.Code, rec.Body.String())
		}
		return response.Data.Models[0]
	}
	discovered := discover()
	if discovered.Name != "opaque-serving-9fdb7" || discovered.ModelType != "" || tokenLimitString(discovered.MaxInputTokens) != "262144" {
		t.Fatalf("serving metadata guessed or rounded: %+v", discovered)
	}
	raw, _ := json.Marshal(addGroupModelRequest{Name: discovered.Name, ModelType: discovered.ModelType, MaxInputTokens: discovered.MaxInputTokens})
	rec := unclassifiedModelCall(t, AddGroupModel, parent, group, "", string(raw))
	var added struct {
		Data addGroupModelResponse `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &added) != nil || tokenLimitString(added.Data.MaxInputTokens) != "262144" {
		t.Fatalf("vLLM model save=%d %s", rec.Code, rec.Body.String())
	}
	rec = unclassifiedModelCall(t, ListGroupModels, parent, group, "", "")
	var listed struct {
		Data groupModelListResponse `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listed) != nil {
		t.Fatal(rec.Body.String())
	}
	found := false
	for _, item := range listed.Data.Models {
		if item.ID == added.Data.ID {
			found = item.ModelType == "" && item.Availability == "unavailable" && tokenLimitString(item.MaxInputTokens) == "262144"
		}
	}
	if !found {
		t.Fatalf("saved serving context missing from inventory: %s", rec.Body.String())
	}
	rec = unclassifiedModelCall(t, UpdateGroupModel, parent, group, added.Data.ID, `{"model_type":"llm"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("classify=%d %s", rec.Code, rec.Body.String())
	}
	var saved orm.UserModelProviderGroupModel
	if err := db.Take(&saved, "id = ?", added.Data.ID).Error; err != nil || saved.ModelType != "llm" || tokenLimitString(saved.MaxInputTokens) != "262144" {
		t.Fatalf("classification discarded serving limit: %+v %v", saved, err)
	}
	rec = unclassifiedModelCall(t, UpdateGroupModel, parent, group, saved.ID, `{"max_input_tokens":"131073"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("manual context=%d %s", rec.Code, rec.Body.String())
	}
	servingLength.Store(524288)
	if refreshed := discover(); !refreshed.Added {
		t.Fatal("refreshed catalog lost saved model identity")
	}
	if err := db.Take(&saved, "id = ?", saved.ID).Error; err != nil || saved.ModelType != "llm" || tokenLimitString(saved.MaxInputTokens) != "131073" {
		t.Fatalf("rediscovery overwrote manual metadata: %+v %v", saved, err)
	}
}
