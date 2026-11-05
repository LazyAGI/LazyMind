package academic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
)

func TestReviewTopicUsesAccessibleMetadataWithoutLaunchingWorkflow(t *testing.T) {
	db := paperFixture(t)
	base := orm.BaseModel{CreateUserID: "u"}
	for _, model := range []any{
		&orm.UserModelProvider{ID: "provider", Name: "OpenAI", Category: "model", Capabilities: "has_models", BaseModel: base},
		&orm.UserModelProviderGroup{ID: "group", UserModelProviderID: "provider", Name: "test", BaseURL: "https://model.invalid/v1", APIKey: "fixture-key", IsVerified: true, BaseModel: base},
		&orm.UserModelProviderGroupModel{ID: "model", UserModelProviderID: "provider", UserModelProviderGroupID: "group", ProviderName: "OpenAI", Name: "gpt-5", ModelType: "llm", BaseModel: base},
		&orm.UserSelectedModel{UserID: "u", ModelKey: "llm", UserModelProviderGroupModelID: "model"},
	} {
		if err := db.Create(model).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.AcademicWork{}).Where("id=?", "a").Update("abstract", "Selected paper abstract").Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Input struct {
				Data struct {
					Papers       []map[string]string `json:"papers"`
					Requirements string              `json:"requirements"`
				} `json:"data"`
			} `json:"input"`
			LLM map[string]map[string]any `json:"llm_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/api/chat/llm-task:run" || len(req.Input.Data.Papers) != 2 || req.Input.Data.Papers[0]["abstract"] != "Selected paper abstract" || req.Input.Data.Requirements != "比较方法" || req.LLM["llm"]["model"] != "gpt-5" {
			t.Errorf("unexpected topic context: %+v", req)
		}
		if calls == 1 {
			fmt.Fprint(w, `{"status":"succeeded","output":{"topic":"工具学习方法比较"}}`)
		} else {
			fmt.Fprint(w, `{"status":"succeeded","output":{"topic":""}}`)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	body := `{"dataset_id":"papers","document_ids":["a","b"],"user_instruction":"比较方法"}`
	w := httptest.NewRecorder()
	GenerateReviewTopic(w, paperRequest(body))
	if w.Code != 200 || w.Body.String() != "{\"topic\":\"工具学习方法比较\"}\n" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	GenerateReviewTopic(w, paperRequest(body))
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	req := paperRequest(body)
	req.Header.Set("X-User-Id", "other")
	GenerateReviewTopic(w, req)
	if w.Code != 403 || calls != 2 {
		t.Fatal("unauthorized model request", w.Code, calls)
	}
	for _, model := range []any{&orm.AcademicExpansion{}, &orm.AcademicHandoff{}} {
		var count int64
		db.Model(model).Count(&count)
		if count != 0 {
			t.Fatal("topic generation created workflow state")
		}
	}
}
