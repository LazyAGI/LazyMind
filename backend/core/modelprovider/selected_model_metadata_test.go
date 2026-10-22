package modelprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
)

type selectedMetadataCloudCatalog struct{}

func (selectedMetadataCloudCatalog) CloudModelCatalog(context.Context) (CloudModelCatalog, error) {
	return CloudModelCatalog{Known: true, Available: true, Models: []CloudCatalogModel{{
		ModelKey: "metadata-cloud-voice", DisplayName: "Cloud voice", ModelType: "tts", Status: "available", Lifecycle: "active",
	}}}, nil
}

func TestGetSelectedModelsPreservesActualVisionAndTechnicalType(t *testing.T) {
	db, text := modelEditFixture(t, "llm", false)
	addEditorSelection(t, db, text, "llm")
	vision := text
	vision.ID, vision.Name, vision.Vision = "vision-selection", "vision-api-model", true
	if err := db.Create(&vision).Error; err != nil {
		t.Fatal(err)
	}
	addEditorSelection(t, db, vision, "vlm")
	if err := db.AutoMigrate(&orm.UserSelectedCloudModel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserSelectedCloudModel{UserID: "user-1", ModelType: "tts", PublicModelKey: "metadata-cloud-voice", DisplayNameSnapshot: "Cloud voice"}).Error; err != nil {
		t.Fatal(err)
	}
	SetCloudCatalogProvider(selectedMetadataCloudCatalog{})
	t.Cleanup(func() { SetCloudCatalogProvider(nil) })
	// Exercise the real edit->selected GET round trip: an explicit false
	// must survive serialization instead of reverting to a name heuristic.
	if err := db.Model(&text).Update("vision", true).Error; err != nil {
		t.Fatal(err)
	}
	edit := modelEditCall(t, text.ID, "user-1", `{"vision":false}`)
	if edit.Code != http.StatusOK {
		t.Fatalf("edit vision: %d %s", edit.Code, edit.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/model_providers/selected", nil)
	request.Header.Set("X-User-Id", "user-1")
	recorder := httptest.NewRecorder()
	GetSelectedModels(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("selected GET: %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Selections []map[string]json.RawMessage `json:"selections"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Selections) != 3 {
		t.Fatalf("selected rows: %s", recorder.Body.String())
	}
	byRole := make(map[string]map[string]json.RawMessage)
	for _, item := range response.Data.Selections {
		var role string
		if err := json.Unmarshal(item["model_key"], &role); err != nil {
			t.Fatal(err)
		}
		byRole[role] = item
	}
	for _, test := range []struct{ role, vision string }{{"llm", "false"}, {"vlm", "true"}} {
		item := byRole[test.role]
		if string(item["vision"]) != test.vision {
			t.Errorf("%s selection must preserve explicit vision=%s, got %s", test.role, test.vision, item["vision"])
		}
		if string(item["model_type"]) != `"llm"` {
			t.Errorf("%s role must retain stored technical type llm, got %s", test.role, item["model_type"])
		}
	}
	cloud := byRole["tts"]
	if string(cloud["source"]) != `"cloud"` {
		t.Fatalf("cloud selection missing: %s", recorder.Body.String())
	}
	if _, exists := cloud["vision"]; exists {
		t.Errorf("cloud unknown vision must be omitted, got %s", cloud["vision"])
	}
	if _, exists := cloud["model_type"]; exists {
		t.Errorf("cloud without stored technical type must not substitute its role: %s", cloud["model_type"])
	}
}
