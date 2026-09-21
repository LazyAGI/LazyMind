package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
)

func seedWorkflowModelSelection(t *testing.T, db *orm.DB, user string) {
	t.Helper()
	for _, row := range []any{
		&orm.UserModelProviderGroup{
			ID: user + "-group", UserModelProviderID: user + "-provider", Name: "Custom",
			BaseURL: "https://gateway.example/v1", APIKey: "test-only", IsVerified: true,
			BaseModel: orm.BaseModel{CreateUserID: user},
		},
		&orm.UserModelProviderGroupModel{
			ID: user + "-model", UserModelProviderID: user + "-provider", UserModelProviderGroupID: user + "-group",
			ProviderName: "OpenAI", Name: "Qwen/" + user, ModelType: "llm",
			BaseModel: orm.BaseModel{CreateUserID: user},
		},
		&orm.UserSelectedModel{UserID: user, ModelKey: "llm", UserModelProviderGroupModelID: user + "-model"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestGenerationLoadsCurrentOwnerSelectionAtExecution(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	seedWorkflowModelSelection(t, db, "owner")
	seedWorkflowModelSelection(t, db, "other")
	seedWorkflowDraft(t, db, "draft", "owner")
	payload, _ := json.Marshal(workflowDraftGeneratePayload{DraftID: "draft", UserID: "owner", SkillPackage: map[string]any{"files": []any{}}})
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id=?", "owner-model").Update("name", "Qwen/updated-selection").Error; err != nil {
		t.Fatal(err)
	}
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"verdict":"needs_confirmation","message":"Test handoff"}}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	result, err := handleWorkflowDraftGenerateJob(t.Context(), asyncjob.Job{PayloadJSON: payload}, nil)
	if err != nil {
		t.Fatalf("generation=%+v err=%v", result, err)
	}
	select {
	case request := <-requests:
		config, _ := request["llm_config"].(map[string]any)
		llm, _ := config["llm"].(map[string]any)
		if llm["model"] != "Qwen/updated-selection" || llm["source"] != "openai" || llm["base_url"] != "https://gateway.example/v1/" {
			t.Fatal("generation did not receive current owner model configuration")
		}
	default:
		t.Fatal("algorithm service was not called")
	}
	model, modelErr := resolveWorkflowModel(t.Context(), db.DB, "owner")
	public, _ := json.Marshal(model.Public)
	if modelErr != nil || strings.Contains(string(public), "test-only") || strings.Contains(string(public), "gateway.example") {
		t.Fatal("public model status leaked credentials or endpoint")
	}
}

func TestWorkflowModelStaticAndDynamicReadinessUseSameResolver(t *testing.T) {
	for _, dynamic := range []bool{true, false} {
		t.Run(map[bool]string{true: "dynamic", false: "static"}[dynamic], func(t *testing.T) {
			db := newHandlerTestDB(t)
			if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"role": "llm", "is_dynamic": dynamic})
			}))
			t.Cleanup(server.Close)
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			model, err := resolveWorkflowModel(t.Context(), db.DB, "owner")
			if dynamic && (err == nil || err.Code != "MODEL_NOT_CONFIGURED" || model.Public["ready"] != false) {
				t.Fatal("missing dynamic selection accepted")
			}
			if !dynamic && (err != nil || model.Public["ready"] != true || model.Public["source"] != "runtime_config") {
				t.Fatal("static deployment rejected")
			}
		})
	}
}

func TestExternalCapabilitiesMatchFrontendSystemModelSelection(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	for _, user := range []string{"owner", "other"} {
		seedWorkflowModelSelection(t, db, user)
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("X-User-Id", user)
		frontend := httptest.NewRecorder()
		modelprovider.GetSelectedModels(frontend, request)
		var selected struct {
			Data struct {
				Items []struct {
					Name string `json:"name"`
				} `json:"selections"`
			} `json:"data"`
		}
		if err := json.Unmarshal(frontend.Body.Bytes(), &selected); err != nil {
			t.Fatal(err)
		}
		capabilities := httptest.NewRecorder()
		GetExternalWorkflowCapabilities(capabilities, request)
		var external struct {
			Data struct {
				Model map[string]any `json:"model_configuration"`
			} `json:"data"`
		}
		if err := json.Unmarshal(capabilities.Body.Bytes(), &external); err != nil {
			t.Fatal(err)
		}
		if len(selected.Data.Items) != 1 || external.Data.Model["ready"] != true || external.Data.Model["model"] != selected.Data.Items[0].Name || external.Data.Model["model"] != "Qwen/"+user {
			t.Fatalf("frontend=%s external=%s", frontend.Body, capabilities.Body)
		}
	}
}

func TestWorkflowGenerationRejectsMissingOrUnreadableModelSettings(t *testing.T) {
	for _, configuredTables := range []bool{false, true} {
		t.Run(map[bool]string{false: "load-failed", true: "not-configured"}[configuredTables], func(t *testing.T) {
			db := newHandlerTestDB(t)
			if configuredTables {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"role":"llm","type":"llm","source":"dynamic","is_dynamic":true}`))
				}))
				t.Cleanup(server.Close)
				t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
				if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
					t.Fatal(err)
				}
			}
			seedWorkflowDraft(t, db, "draft", "owner")
			payload, _ := json.Marshal(workflowDraftGeneratePayload{DraftID: "draft", UserID: "owner"})
			result, err := handleWorkflowDraftGenerateJob(context.Background(), asyncjob.Job{PayloadJSON: payload}, nil)
			want := "MODEL_CONFIG_LOAD_FAILED"
			if configuredTables {
				want = "MODEL_NOT_CONFIGURED"
			}
			if err == nil || result.ErrorCode != want {
				t.Fatalf("result=%#v err=%v, want %s", result, err, want)
			}
			if configuredTables && !result.Permanent {
				t.Fatal("missing model must not consume automatic retries")
			}
			var draft orm.WorkflowDraft
			if err := db.First(&draft, "id=?", "draft").Error; err != nil {
				t.Fatal(err)
			}
			if draft.GenerateStatus != generateStatusFailed {
				t.Fatalf("generation must fail before model call: %s", draft.GenerateStatus)
			}
			failure := decodeJSONMap(json.RawMessage(draft.GenerateError))
			if failure["code"] != want || failure["phase"] != "model_configuration" {
				t.Fatalf("model error contract lost: %s", draft.GenerateError)
			}
			task := orm.ExternalAgentWorkflowTask{ID: "task", OwnerUserID: "owner", DraftID: draft.ID,
				IdempotencyKey: "model-failure", Status: externalTaskStatusConverting, Stage: "generate",
				RequestJSON: json.RawMessage(`{}`), ResultSummaryJSON: json.RawMessage(`{}`), ResultArtifactsJSON: json.RawMessage(`[]`)}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			task = ensureExternalWorkflowTaskWorkflow(t.Context(), db.DB, task)
			if task.Status != externalTaskStatusFailed || task.ErrorCode != want {
				t.Fatalf("external task lost model failure: status=%s code=%s", task.Status, task.ErrorCode)
			}
		})
	}
}
