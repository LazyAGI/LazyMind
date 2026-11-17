package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
)

func TestModelGenerationCapacityIsTwoPerSession(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowActionJob{}, &orm.WorkflowHostAction{})
	sqlDB, _ := db.DB.DB()
	if db.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	}
	contracts := []executor.AttemptContext{}
	for index, test := range []struct{ workflowID, provider string }{
		{"paper-writing", "codex"}, {"writer-workflow", "workbuddy"}, {"paper-writing", "deepseek-harness"},
	} {
		session := orm.WorkflowSession{ID: fmt.Sprintf("session-%d", index), WorkflowID: test.workflowID, Status: "active",
			ControllerHost: "external-agent", ControlProtocol: "workflow.control.v1",
			ControlBindingJSON: fmt.Sprintf(`{"provider":%q,"connector_id":"connector","driver_session_id":"chat"}`, test.provider)}
		if err := db.Create(&session).Error; err != nil {
			t.Fatal(err)
		}
		expires := time.Now().Add(time.Hour)
		row := orm.WorkflowSessionStep{ID: fmt.Sprintf("attempt-%d", index), SessionID: session.ID,
			StepID: "write_document", Attempt: 1, TaskID: fmt.Sprintf("task-%d", index), Status: "claimed", ExecutorHost: "lazymind", LeaseToken: "lease", LeaseExpiresAt: &expires}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		contracts = append(contracts, executor.AttemptContext{SessionID: session.ID, AttemptID: row.ID, Metadata: map[string]string{"model_executor": graphengine.ExternalModelExecutor}})
	}
	var workers sync.WaitGroup
	for _, contract := range contracts {
		for index := 0; index < 3; index++ {
			workers.Add(1)
			go func(contract executor.AttemptContext, index int) {
				defer workers.Done()
				request := json.RawMessage(fmt.Sprintf(`{"messages":[{"role":"user","content":"chapter %d"}]}`, index))
				if _, err := enqueueModelGeneration(t.Context(), db.DB, contract, "lease", request); err != nil {
					t.Error(err)
				}
			}(contract, index)
		}
	}
	workers.Wait()
	for _, contract := range contracts {
		var admitted, queued int64
		db.Model(&orm.WorkflowActionJob{}).Where("session_id = ? AND status = 'waiting_agent'", contract.SessionID).Count(&admitted)
		db.Model(&orm.WorkflowActionJob{}).Where("session_id = ? AND status = 'queued'", contract.SessionID).Count(&queued)
		if admitted != 2 || queued != 1 {
			t.Fatalf("%s admitted=%d queued=%d", contract.SessionID, admitted, queued)
		}
	}
	contract := contracts[0]
	var job orm.WorkflowActionJob
	if err := db.Where("session_id = ? AND status = 'waiting_agent'", contract.SessionID).First(&job).Error; err != nil {
		t.Fatal(err)
	}
	workerHash, _ := json.Marshal(modelGenerationResult{WorkerHash: controlstore.Hash([]byte("worker"))})
	if err := db.Model(&job).Updates(map[string]any{"status": "running", "result_json": string(workerHash)}).Error; err != nil {
		t.Fatal(err)
	}
	cancelModelGeneration(db.DB, job)
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", contract.SessionID).Error; err != nil {
		t.Fatal(err)
	}
	// Timeout retains its reservation until the original worker exits.
	if err := controlstore.Transaction(t.Context(), db.DB, session.ID, func(tx *gorm.DB, current *orm.WorkflowSession) error {
		return dispatchModelGenerations(tx, *current)
	}); err != nil {
		t.Fatal(err)
	}
	var queued int64
	db.Model(&orm.WorkflowActionJob{}).Where("session_id = ? AND status = 'queued'", session.ID).Count(&queued)
	if queued != 1 {
		t.Fatal("timeout released an occupied worker")
	}
	var request modelGenerationRequest
	_ = json.Unmarshal([]byte(job.RequestJSON), &request)
	response := modelGenerationResponse{WorkerToken: "worker", RequestHash: request.Hash, Cancelled: true}
	settle := func(input modelGenerationResponse) error {
		return controlstore.Transaction(t.Context(), db.DB, session.ID, func(tx *gorm.DB, current *orm.WorkflowSession) error {
			if err := tx.First(&job, "id = ?", job.ID).Error; err != nil {
				return err
			}
			return settleModelGeneration(tx, *current, &job, input)
		})
	}
	if err := settle(response); err != nil {
		t.Fatal(err)
	}
	if err := settle(response); err != nil {
		t.Fatal("identical acknowledgement was not idempotent", err)
	}
	db.Model(&orm.WorkflowActionJob{}).Where("session_id = ? AND status = 'queued'", session.ID).Count(&queued)
	if queued != 0 {
		t.Fatal("worker exit did not admit exactly one replacement", queued)
	}
	response.RequestHash = "stale"
	if settle(response) == nil {
		t.Fatal("accepted a mismatched generation")
	}
}

func TestModelGenerationNativeRequestReachesBusinessContext(t *testing.T) {
	for _, test := range []struct{ workflowID, provider, current, want string }{
		{"paper-writing", "workbuddy", "", "原始研究主题"},
		{"writer-workflow", "deepseek-harness", "本轮修改要求", "本轮修改要求"},
	} {
		t.Run(test.workflowID, func(t *testing.T) {
			graph := graphengine.CompiledStateGraph{StartRoute: "all",
				Runtime:      graphengine.RuntimePolicy{Execution: &graphengine.ExecutionPolicy{Version: "1", ModelExecutor: graphengine.ExternalModelExecutor}},
				Nodes:        map[string]graphengine.CompiledNode{"draft": {ID: "draft", Mode: "auto"}},
				ControlEdges: []graphengine.CompiledEdge{{ID: "start", From: "__start__", To: "draft"}}}
			db, session := newControllerTestSession(t, test.provider, graph)
			if err := db.Model(&session).Updates(map[string]any{"plugin_id": test.workflowID, "intent_context": `{"text":"原始研究主题"}`}).Error; err != nil {
				t.Fatal(err)
			}
			result, err := (WorkflowControlService{DB: db.DB}).Execute(t.Context(), session.CreateUserID, session.ID,
				WorkflowControlCommand{CommandID: "begin", Kind: "begin", StepID: "draft", StateVersion: session.StateVersion, UserInput: test.current})
			if err != nil {
				t.Fatal(err)
			}
			var step orm.WorkflowSessionStep
			var task orm.SubAgentTask
			if err := db.First(&step, "id = ?", result.Receipt.ExecutionID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&task, "id = ?", step.TaskID).Error; err != nil {
				t.Fatal(err)
			}
			var params map[string]any
			if err := json.Unmarshal(task.Params, &params); err != nil {
				t.Fatal(err)
			}
			if params["current_user_input"] != test.want || params["launch_user_input"] != "原始研究主题" {
				t.Fatalf("native request was lost: %v", params)
			}
		})
	}
}

func TestModelGenerationKeepsNativeExecutionAndAssistantMessages(t *testing.T) {
	policy := graphengine.RuntimePolicy{Execution: &graphengine.ExecutionPolicy{Version: "1", ModelExecutor: graphengine.ExternalModelExecutor}}
	node := graphengine.CompiledNode{Execution: &graphengine.StepExecution{Version: "1"}, LegacyTools: []string{"native_generator"}}
	for _, provider := range []string{"codex", "workbuddy", "deepseek-harness"} {
		if executorHostForStep("external-agent", provider, node, policy) != "lazymind" {
			t.Fatalf("%s business execution escaped the native runtime", provider)
		}
	}
	if executorHostForStep("external-agent", "", node, policy) != "external-agent" {
		t.Fatal("unbound execution changed its routing")
	}
	message := json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"run_paper_step","arguments":"{}"}}]}`)
	for _, streaming := range []bool{false, true} {
		recorder := httptest.NewRecorder()
		writeModelGenerationResult(recorder, "generation-1", message, streaming)
		if !strings.Contains(recorder.Body.String(), `"tool_calls"`) || !strings.Contains(recorder.Body.String(), `"run_paper_step"`) {
			t.Fatal(recorder.Body.String())
		}
		if streaming && (!strings.Contains(recorder.Body.String(), `"index":0`) || !strings.Contains(recorder.Body.String(), "data: [DONE]")) {
			t.Fatal("incomplete native tool-call stream")
		}
	}
}

func TestModelGenerationErrorPreservesWorkerDetails(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, status := range []string{"failed", "cancelled"} {
			recorder := httptest.NewRecorder()
			message := "图件生成失败\n\"finding_ids\" missing"
			writeModelGenerationError(recorder, status, message, streaming)
			body := recorder.Body.String()
			if streaming {
				body = strings.SplitN(strings.TrimPrefix(body, "data: "), "\n\n", 2)[0]
				if !strings.HasSuffix(recorder.Body.String(), "data: [DONE]\n\n") {
					t.Fatal("unterminated failure stream")
				}
			} else if recorder.Code != http.StatusBadGateway {
				t.Fatalf("failure status = %d", recorder.Code)
			}
			var result struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal([]byte(body), &result); err != nil || result.Error.Message != message {
				t.Fatalf("worker error lost: %s (%v)", body, err)
			}
			if status == "cancelled" && result.Error.Code != "EXTERNAL_MODEL_GENERATION_CANCELLED" ||
				status == "failed" && result.Error.Code != "EXTERNAL_MODEL_GENERATION_FAILED" {
				t.Fatalf("wrong failure classification: %s", result.Error.Code)
			}
		}
	}
}
