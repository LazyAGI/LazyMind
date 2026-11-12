package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lazymind/core/common"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
	"path/filepath"
)

func seedTaskModel(t *testing.T, db *gorm.DB, owner, id string, shared bool) {
	t.Helper()
	window := "8192"
	for _, row := range []any{
		&orm.UserModelProvider{ID: "provider-" + id, Name: "OpenAI", BaseModel: orm.BaseModel{CreateUserID: owner}},
		&orm.UserModelProviderGroup{ID: "group-" + id, UserModelProviderID: "provider-" + id, BaseURL: "https://fixture.invalid/v1", APIKey: "fixture-key-" + id, IsVerified: true, CredentialRevision: 1, BaseModel: orm.BaseModel{CreateUserID: owner}},
		&orm.UserModelProviderGroupModel{ID: id, UserModelProviderID: "provider-" + id, UserModelProviderGroupID: "group-" + id, ProviderName: "OpenAI", Name: id, ModelType: "llm", MaxInputTokens: &window, BaseModel: orm.BaseModel{CreateUserID: owner}},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ? AND model_type = ?", owner, "llm").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		if err := db.Create(&orm.UserSelectedModel{UserID: owner, ModelKey: "llm", UserModelProviderGroupModelID: id, Share: shared}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

type modelFallbackStateStore struct {
	*state.SQLiteStore
	rpushCalls []rpushCall
}

func newModelFallbackStateStore(t *testing.T) *modelFallbackStateStore {
	t.Helper()
	ss, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	return &modelFallbackStateStore{SQLiteStore: ss}
}

func (s *modelFallbackStateStore) RPush(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := s.SQLiteStore.RPush(ctx, key, value, ttl); err != nil {
		return err
	}
	s.rpushCalls = append(s.rpushCalls, rpushCall{key: key, value: append([]byte(nil), value...)})
	return nil
}

func TestOrdinaryModelFallbackReconnectHydratesCommittedThenOpenCall(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	_, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "replay", ConversationID: "conv", AgentType: "research", CreateUserID: "0", Title: "replay"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "replay").Updates(map[string]any{"execution_id": "current", "status": StatusRunning}).Error; err != nil {
		t.Fatal(err)
	}
	for i, row := range []orm.SubAgentStep{
		{Role: "text", Content: json.RawMessage(`{"content":"COMMITTED","model_output":{"run_id":"current","call_index":1}}`)},
		{Role: "model_runtime", Content: json.RawMessage(`{"model_output":{"run_id":"current","call_index":2,"phase":"rollback"},"current_model":{"candidate_id":"own:a","source":"own","model_id":"a","provider_group_id":"group-a"}}`)},
	} {
		row.ID = fmt.Sprintf("replay-%d", i)
		row.TaskID = "replay"
		row.ExecutionID = "current"
		row.Seq = i
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	stateStore := &mockStateStore{lrangeResult: []string{
		`{"type":"model_output","execution_id":"old","model_output":{"run_id":"old","call_index":1,"phase":"begin"}}`,
		`{"type":"text","text":"OLD-RUN","model_output":{"run_id":"old","call_index":1}}`,
		`{"type":"model_output","model_output":{"run_id":"current","call_index":2,"phase":"begin"}}`,
		`{"type":"text","text":"BAD","model_output":{"run_id":"current","call_index":2}}`,
		`{"type":"model_output","model_output":{"run_id":"current","call_index":2,"phase":"rollback"}}`,
		`{"type":"model_output","model_output":{"run_id":"current","call_index":3,"phase":"begin"}}`,
		`{"type":"think","think":"OPEN","model_output":{"run_id":"current","call_index":3}}`,
	}}
	store.Init(db, db, stateStore)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/tasks/replay:stream", nil).WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"task_id": "replay"})
	rec := httptest.NewRecorder()
	StreamTask(rec, req)
	body := rec.Body.String()
	closed := strings.Index(body, `"phase":"rollback"`)
	opened := strings.Index(body, `"phase":"begin"`)
	if !strings.Contains(body, "COMMITTED") || !strings.Contains(body, "OPEN") || strings.Contains(body, "BAD") || strings.Contains(body, "OLD-RUN") || closed < 0 || opened < closed || !strings.Contains(body, `"execution_id":"current"`) {
		t.Fatalf("raw reconnect lost committed identity/open preview or resurrected closed output: %s", body)
	}
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "replay").Update("status", StatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	terminal := httptest.NewRecorder()
	StreamTask(terminal, mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/tasks/replay:stream", nil), map[string]string{"task_id": "replay"}))
	if strings.Contains(terminal.Body.String(), "OPEN") || !strings.Contains(terminal.Body.String(), `"call_index":3,"phase":"rollback"`) || !strings.Contains(terminal.Body.String(), "COMMITTED") {
		t.Fatalf("status-only terminal resurrected an open cached call: %s", terminal.Body.String())
	}
}

func TestOrdinaryModelFallbackHydratesOwnerBeforeTaskSpec(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedTaskModel(t, db, "owner", "a", false)
	seedTaskModel(t, db, "other", "private", false)
	_, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "task-model", ConversationID: "conv", AgentType: "research", CreateUserID: "owner", Title: "research"})
	if err != nil {
		t.Fatal(err)
	}
	var req RunRequest
	if err := json.Unmarshal([]byte(`{"task_id":"task-model","agent_type":"workflow_step","task_spec":{"params":{}},"llm_config":{"llm":{"model_name":"FORGED"}},"model_fallback_current_id":"own:private","model_fallback_candidates":[{"candidate_id":"own:private"}]}`), &req); err != nil {
		t.Fatal(err)
	}
	if err := hydrateRunRequest(t.Context(), db, &req); err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(req)
	var body map[string]any
	_ = json.Unmarshal(wire, &body)
	if body["model_fallback_current_id"] != "own:a" || strings.Contains(string(wire), "FORGED") || strings.Contains(string(wire), "fixture-key-private") {
		t.Fatalf("ordinary execution did not rebuild the owner's authorized input before TaskSpec return: %s", wire)
	}
}

func TestOrdinaryModelFallbackTrustedOriginRevalidatesSharingAndActor(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedTaskModel(t, db, "actor", "a", false)
	seedTaskModel(t, db, "shared-owner", "b", true)
	for _, id := range []string{"old-child", "new-child", "other-user"} {
		owner := "actor"
		if id == "other-user" {
			owner = "outsider"
		}
		if _, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: id, ConversationID: "conv", AgentType: "research", CreateUserID: owner, Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	window := int64(8192)
	origin := &InheritedModelOrigin{Identity: common.ModelSwitchIdentity{CandidateID: "shared:b", Source: "shared", ModelID: "b", ProviderGroupID: "group-b"}, MaxInputTokens: &window}
	old := RunRequest{TaskID: "old-child", InheritedModelOrigin: origin}
	if err := hydrateRunRequest(t.Context(), db, &old); err != nil {
		t.Fatal(err)
	}
	if old.ModelFallbackCurrentID != "shared:b" || old.ModelFallbackInitialSwitch != nil {
		t.Fatal("authorized inherited model was not selected")
	}
	if err := db.Model(&orm.UserSelectedModel{}).Where("user_id = ?", "shared-owner").Update("share", false).Error; err != nil {
		t.Fatal(err)
	}
	next := RunRequest{TaskID: "new-child", InheritedModelOrigin: origin, TaskSpec: map[string]any{"params": map[string]any{}}, LLMConfig: cloneTaskConfig(old.LLMConfig), ModelFallbackCurrentID: "shared:b", ModelFallbackCandidates: old.ModelFallbackCandidates}
	if err := hydrateRunRequest(t.Context(), db, &next); err != nil {
		t.Fatal(err)
	}
	if next.ModelFallbackCurrentID != "own:a" || len(next.ModelFallbackCandidates) != 1 || next.ModelFallbackInitialSwitch == nil || next.ModelFallbackInitialSwitch.From.CandidateID != "shared:b" || old.ModelFallbackCurrentID != "shared:b" || len(old.ModelFallbackCandidates) != 2 {
		t.Fatal("revoked shared model crossed execution boundary or mutated an already started child")
	}
	next.LLMConfig["llm"].(map[string]any)["model"] = "changed-new-child"
	if old.LLMConfig["llm"].(map[string]any)["model"] != "b" {
		t.Fatal("child runtime configurations alias")
	}
	wire, _ := json.Marshal(next)
	if strings.Contains(string(wire), "InheritedModelOrigin") || strings.Contains(string(wire), "fixture-key-b") {
		t.Fatal("trust marker or revoked credentials entered private execution input")
	}
	outsider := RunRequest{TaskID: "other-user", InheritedModelOrigin: origin, LLMConfig: old.LLMConfig, ModelFallbackCurrentID: "shared:b"}
	if err := hydrateRunRequest(t.Context(), db, &outsider); !errors.Is(err, errTaskModelsExhausted) {
		t.Fatalf("inherited identity granted another user's model: %v", err)
	}
}

func TestOrdinaryModelFallbackDefaultUnavailableAndEmptyPoolAreExplicit(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedTaskModel(t, db, "actor", "a", false)
	seedTaskModel(t, db, "actor", "b", false)
	if _, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "bootstrap", ConversationID: "conv", AgentType: "research", CreateUserID: "actor", Title: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-a").Update("api_key_ciphertext", "fixture-invalid-envelope").Error; err != nil {
		t.Fatal(err)
	}
	req := RunRequest{TaskID: "bootstrap"}
	if err := hydrateRunRequest(t.Context(), db, &req); err != nil {
		t.Fatal(err)
	}
	if req.ModelFallbackCurrentID != "own:b" || req.ModelFallbackInitialSwitch == nil || req.ModelFallbackInitialSwitch.From.CandidateID != "own:a" || req.ModelFallbackInitialSwitch.FromMaxInputTokens == nil {
		t.Fatal("unavailable DB default silently became a successful model")
	}
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-b").Update("api_key_ciphertext", "fixture-invalid-envelope").Error; err != nil {
		t.Fatal(err)
	}
	if err := Run(t.Context(), db, &mockStateStore{}, RunRequest{TaskID: "bootstrap"}); !errors.Is(err, errTaskModelsExhausted) {
		t.Fatalf("empty pool did not stop before provider request: %v", err)
	}
	task, _ := GetTask(t.Context(), db, "bootstrap")
	view, err := OrdinaryTask(t.Context(), db, task)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != StatusFailed || view.FailureCode != "model_fallback_exhausted" || view.ModelSwitch != nil {
		t.Fatalf("unavailable pool has no stable recovery state: %+v", view)
	}
	for _, code := range []string{"model_context_not_fit", "completion_evaluation_failed"} {
		if err := db.Model(task).Update("current_phase", code).Error; err != nil {
			t.Fatal(err)
		}
		task.CurrentPhase = code
		view, _ = OrdinaryTask(t.Context(), db, task)
		if code == "model_context_not_fit" && view.FailureCode != code || code == "completion_evaluation_failed" && view.FailureCode != "" {
			t.Fatal("evaluation failure became a model recovery instruction")
		}
	}
	if err := db.Model(task).Updates(map[string]any{"status": StatusRunning, "current_phase": "model_fallback_exhausted"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := beginDisplayExecution(t.Context(), db, task.ID, "next-execution"); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptFinalStatusWithPhase(t.Context(), db, task.ID, StatusFailed, "local failure", ""); err != nil {
		t.Fatal(err)
	}
	task, _ = GetTask(t.Context(), db, task.ID)
	view, _ = OrdinaryTask(t.Context(), db, task)
	if view.FailureCode != "" {
		t.Fatal("a later local failure inherited the prior execution's exhaustion")
	}
}

func TestOrdinaryModelFallbackCancellationRollsBackOnlyOpenCall(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedTaskModel(t, db, "owner", "a", false)
	if _, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "cancel", ConversationID: "conv", AgentType: "research", CreateUserID: "owner", Title: "cancel"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(subagentStreamDirEnv, t.TempDir())
	stateStore, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req RunRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		run := req.TaskSpec["execution_id"].(string)
		for _, frame := range []string{
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":1,"phase":"begin"}}`,
			`{"type":"text","text":"COMMITTED","model_output":{"run_id":%q,"call_index":1}}`,
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":1,"phase":"commit"}}`,
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":2,"phase":"begin"}}`,
			`{"type":"text","text":"BAD","model_output":{"run_id":%q,"call_index":2}}`,
		} {
			fmt.Fprintf(w, "data: "+frame+"\n\n", run)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, db, stateStore, RunRequest{TaskID: "cancel"}) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		events, _ := StreamEventsFrom(t.Context(), stateStore, "cancel", 0)
		if strings.Contains(strings.Join(events, ""), "BAD") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("temporary output was not visible before cancellation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled task did not finish")
	}
	task, _ := GetTask(t.Context(), db, "cancel")
	steps, _ := LoadSteps(t.Context(), db, "cancel")
	raw, _ := json.Marshal(steps)
	if task.Status != StatusCanceled || strings.Contains(string(raw), "BAD") || !strings.Contains(string(raw), "COMMITTED") || !strings.Contains(string(raw), `"phase":"rollback"`) {
		t.Fatalf("cancellation lost committed output or retained preview: status=%s rows=%s", task.Status, raw)
	}
}

func TestOrdinaryModelFallbackDoesNotRelabelAnOldExecutionEvent(t *testing.T) {
	db, task := ordinaryFixture(t)
	if err := db.Model(task).Update("execution_id", "current").Error; err != nil {
		t.Fatal(err)
	}
	called := false
	accepted, err := routeExecutionEvent(t.Context(), db.DB, task.ID, "current", TaskEvent{Type: "text", Text: "OLD", ExecutionID: "old"}, func(tx *gorm.DB, event TaskEvent) (bool, error) { called = true; return true, nil })
	if !errors.Is(err, ErrStaleExecution) || accepted || called {
		t.Fatal("old event was relabeled and routed into current execution")
	}
	if err := db.Model(task).Update("status", StatusCanceled).Error; err != nil {
		t.Fatal(err)
	}
	accepted, err = routeExecutionEvent(t.Context(), db.DB, task.ID, "current", TaskEvent{Type: "model_output", ModelOutput: &common.ModelOutput{RunID: "current", CallIndex: 1, Phase: "commit"}}, func(tx *gorm.DB, event TaskEvent) (bool, error) { called = true; return true, nil })
	if !errors.Is(err, ErrTaskTerminal) || accepted || called {
		t.Fatal("a stopped execution accepted a later model commit")
	}
}

func TestOrdinaryModelFallbackActualConsumerCommitsOnlyClosedCalls(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	seedTaskModel(t, db, "owner", "a", false)
	_, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "task-model", ConversationID: "conv", AgentType: "research", CreateUserID: "owner", Title: "research"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		run, _ := req["task_spec"].(map[string]any)["execution_id"].(string)
		for _, frame := range []string{
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":1,"phase":"begin"}}`,
			`{"type":"text","text":"COMMITTED","model_output":{"run_id":%q,"call_index":1}}`,
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":1,"phase":"commit"}}`,
			`{"type":"tool_results","tool_results":[{"tool_call_id":"tool1","content":"RECEIPT"}],"execution_id":%q}`,
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":2,"phase":"begin"}}`,
			`{"type":"text","text":"FAILED-PREVIEW","model_output":{"run_id":%q,"call_index":2}}`,
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":2,"phase":"rollback"}}`,
			`{"type":"done","status":"succeeded","summary":"ok","execution_id":%q}`,
		} {
			fmt.Fprintf(w, "data: "+frame+"\n\n", run)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	if err := Run(t.Context(), db, newModelFallbackStateStore(t), RunRequest{TaskID: "task-model"}); err != nil {
		t.Fatal(err)
	}
	steps, err := LoadSteps(t.Context(), db, "task-model")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(steps)
	if strings.Contains(string(raw), "FAILED-PREVIEW") || !strings.Contains(string(raw), "COMMITTED") || !strings.Contains(string(raw), "RECEIPT") || !strings.Contains(string(raw), "model_runtime") {
		t.Fatalf("call rollback/closed cursor was not persisted correctly: %s", raw)
	}
}

func TestOrdinaryModelFallbackActualSwitchPersistsAuthorizedFact(t *testing.T) {
	for _, localFailure := range []bool{false, true} {
		name := "success"
		if localFailure {
			name = "local_commit_failure"
		}
		t.Run(name, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			seedTaskModel(t, db, "owner", "a", false)
			seedTaskModel(t, db, "owner", "b", false)
			_, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "switch", ConversationID: "conv", AgentType: "research", CreateUserID: "owner", Title: "switch"})
			if err != nil {
				t.Fatal(err)
			}
			if localFailure {
				if err := db.Callback().Create().Before("gorm:create").Register("reject_model_commit", func(tx *gorm.DB) {
					if row, ok := tx.Statement.Dest.(*orm.SubAgentStep); ok && row.Role == "model_runtime" && strings.Contains(string(row.Content), `"model_switch"`) {
						tx.AddError(errors.New("fixture local commit failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				var req RunRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				run := req.TaskSpec["execution_id"].(string)
				emit := func(ev map[string]any) {
					ev["execution_id"] = run
					raw, _ := json.Marshal(ev)
					fmt.Fprintf(w, "data: %s\n\n", raw)
				}
				model := func(index int, phase string, ev map[string]any) {
					ev["model_output"] = &common.ModelOutput{RunID: run, CallIndex: index, Phase: phase}
					emit(ev)
				}
				model(1, "begin", map[string]any{"type": "model_output"})
				model(1, "", map[string]any{"type": "text", "text": "COMMITTED"})
				model(1, "", map[string]any{"type": "think", "think": "kept-think"})
				model(1, "commit", map[string]any{"type": "model_output"})
				emit(map[string]any{"type": "tool_results", "tool_results": []any{map[string]any{"tool_call_id": "tool1", "content": "RECEIPT"}}})
				emit(map[string]any{"type": "artifact", "slot": "report", "content_type": "text", "value": map[string]any{"text": "real artifact"}, "seq": 1})
				model(2, "begin", map[string]any{"type": "model_output"})
				model(2, "", map[string]any{"type": "text", "text": "FAILED-PREVIEW"})
				model(2, "rollback", map[string]any{"type": "model_output"})
				model(2, "", map[string]any{"type": "text", "text": "LATE"})
				fmt.Fprintln(w, `data: {"type":"text","execution_id":"old","text":"OLD","model_output":{"run_id":"old","call_index":3}}`)
				model(3, "begin", map[string]any{"type": "model_output"})
				model(3, "", map[string]any{"type": "text", "text": "REPLACEMENT"})
				data := common.ModelSwitchedData{SwitchIndex: 1, From: common.ModelSwitchIdentity{CandidateID: "own:a", Source: "own", ModelID: "a", ProviderGroupID: "group-a"}, To: common.ModelSwitchIdentity{CandidateID: "own:b", Source: "own", ModelID: "b", ProviderGroupID: "group-b"}, ReasonCode: "service_unavailable"}
				data.To.ModelName = "UNTRUSTED-NAME"
				raw, _ := json.Marshal(data)
				event := common.ModelRuntimeEvent{SchemaVersion: 1, EventID: "success-b", RunID: run, Type: "model_switched", Data: raw}
				model(3, "commit", map[string]any{"type": "model_output", "runtime_event": event})
				model(3, "commit", map[string]any{"type": "model_output", "runtime_event": event})
				emit(map[string]any{"type": "done", "status": "succeeded", "summary": "ok"})
				model(4, "begin", map[string]any{"type": "model_output"})
				model(4, "", map[string]any{"type": "text", "text": "AFTER-TERMINAL"})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			stateStore := newModelFallbackStateStore(t)
			err = Run(t.Context(), db, stateStore, RunRequest{TaskID: "switch"})
			if localFailure != (err != nil) {
				t.Fatalf("local failure=%v err=%v", localFailure, err)
			}
			task, _ := GetTask(t.Context(), db, "switch")
			steps, _ := LoadSteps(t.Context(), db, "switch")
			raw, _ := json.Marshal(steps)
			if strings.Contains(string(raw), "FAILED-PREVIEW") || strings.Contains(string(raw), "LATE") || strings.Contains(string(raw), "AFTER-TERMINAL") || strings.Contains(string(raw), `"OLD"`) || !strings.Contains(string(raw), "COMMITTED") || !strings.Contains(string(raw), "RECEIPT") || posts != 1 {
				t.Fatalf("durable projection/tool replay mismatch: posts=%d rows=%s", posts, raw)
			}
			view, err := OrdinaryTask(t.Context(), db, task)
			if err != nil {
				t.Fatal(err)
			}
			if len(view.StageArtifacts) != 1 {
				t.Fatal("rollback removed real artifact")
			}
			var last orm.SubAgentStep
			_ = db.Where("task_id = ? AND role = ?", "switch", "model_runtime").Order("seq DESC").Take(&last).Error
			var record taskModelRecord
			_ = json.Unmarshal(last.Content, &record)
			if localFailure {
				if task.Status != StatusFailed || record.CurrentModel.ModelID != "a" || record.ModelOutput.Phase != "rollback" || view.ModelSwitch != nil || strings.Contains(string(raw), "REPLACEMENT") {
					t.Fatalf("local transaction failure promoted model or pending text: task=%s record=%s", task.Status, last.Content)
				}
			} else {
				if task.Status != StatusSucceeded || record.CurrentModel.ModelID != "b" || record.ModelOutput.CallIndex != 3 || view.ModelSwitch == nil || len(view.ProcessSteps) != 1 || view.ModelSwitch.ToMaxInputTokens == nil || *view.ModelSwitch.ToMaxInputTokens != 8192 || !strings.Contains(string(raw), "REPLACEMENT") {
					t.Fatalf("host success missing after DB reload: status=%s view=%+v record=%s", task.Status, view, last.Content)
				}
				public, _ := json.Marshal(view)
				if strings.Contains(string(public), "fixture-key") || strings.Contains(string(public), "UNTRUSTED-NAME") {
					t.Fatal("private candidate configuration leaked")
				}
			}
		})
	}
}

func TestOrdinaryModelFallbackRejectsUnconfirmedRuntimeAndClosesErrors(t *testing.T) {
	for _, kind := range []string{"forged_candidate", "mixed_boundary", "error", "missing_commit", "disconnect"} {
		t.Run(kind, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			seedTaskModel(t, db, "owner", "a", false)
			_, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "reject", ConversationID: "conv", AgentType: "research", CreateUserID: "owner", Title: "reject"})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request RunRequest
				_ = json.NewDecoder(r.Body).Decode(&request)
				run := request.TaskSpec["execution_id"].(string)
				emit := func(ev map[string]any) {
					ev["execution_id"] = run
					raw, _ := json.Marshal(ev)
					fmt.Fprintf(w, "data: %s\n\n", raw)
				}
				emit(map[string]any{"type": "model_output", "model_output": common.ModelOutput{RunID: run, CallIndex: 1, Phase: "begin"}})
				emit(map[string]any{"type": "text", "text": "BAD", "model_output": common.ModelOutput{RunID: run, CallIndex: 1}})
				switch kind {
				case "forged_candidate":
					data := common.ModelSwitchedData{SwitchIndex: 1, From: common.ModelSwitchIdentity{CandidateID: "own:a", Source: "own", ModelID: "a", ProviderGroupID: "group-a"}, To: common.ModelSwitchIdentity{CandidateID: "own:private", Source: "own", ModelID: "private", ProviderGroupID: "private"}, ReasonCode: "service_unavailable"}
					raw, _ := json.Marshal(data)
					emit(map[string]any{"type": "model_output", "model_output": common.ModelOutput{RunID: run, CallIndex: 1, Phase: "commit"}, "runtime_event": common.ModelRuntimeEvent{SchemaVersion: 1, EventID: "forged", RunID: run, Type: "model_switched", Data: raw}})
				case "mixed_boundary":
					emit(map[string]any{"type": "model_output", "model_output": common.ModelOutput{RunID: run, CallIndex: 1, Phase: "commit"}, "slot": "FORGED-ARTIFACT"})
				case "error":
					emit(map[string]any{"type": "error", "current_phase": "model_context_not_fit", "message": "no fitting candidate"})
				case "missing_commit":
					emit(map[string]any{"type": "done", "summary": "must not be successful"})
				}
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			stateStore := newModelFallbackStateStore(t)
			_ = Run(t.Context(), db, stateStore, RunRequest{TaskID: "reject"})
			task, _ := GetTask(t.Context(), db, "reject")
			steps, _ := LoadSteps(t.Context(), db, "reject")
			raw, _ := json.Marshal(steps)
			if task.Status != StatusFailed || strings.Contains(string(raw), "BAD") || strings.Contains(string(raw), `"model_switch"`) {
				t.Fatalf("unconfirmed call became successful output: status=%s steps=%s", task.Status, raw)
			}
			var last taskModelRecord
			_ = json.Unmarshal(steps[len(steps)-1].Content, &last)
			if last.ModelOutput.Phase != "rollback" || last.CurrentModel.ModelID != "a" {
				t.Fatalf("error did not close original model call: %s", raw)
			}
			if kind == "error" && task.CurrentPhase != "model_context_not_fit" {
				t.Fatal("authoritative failure code was overwritten")
			}
			var rollback, terminal int
			for i, frame := range stateStore.rpushCalls {
				var event TaskEvent
				_ = json.Unmarshal(frame.value, &event)
				if event.ModelOutput != nil && event.ModelOutput.Phase == "rollback" {
					rollback = i + 1
				}
				if event.Type == "error" {
					terminal = i + 1
				}
			}
			if rollback == 0 || terminal <= rollback {
				t.Fatal("terminal was published before rollback")
			}
		})
	}
}

func TestOrdinaryModelFallbackPlainCommitRetriesFromConfirmedState(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	if db.Dialector.Name() != "sqlite" {
		t.Skip("SQLite busy retry regression")
	}
	seedTaskModel(t, db, "owner", "a", false)
	if _, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "plain-retry", ConversationID: "conv", AgentType: "research", CreateUserID: "owner", Title: "retry"}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if err := db.Callback().Create().Before("gorm:create").Register("retry_model_commit", func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*orm.SubAgentStep); ok && row.Role == "model_runtime" && strings.Contains(string(row.Content), `"phase":"commit"`) {
			attempts++
			if attempts == 1 {
				tx.AddError(errors.New("database is locked (517)"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		var req RunRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		run := req.TaskSpec["execution_id"].(string)
		for _, frame := range []string{
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":1,"phase":"begin"}}`,
			`{"type":"text","text":"COMMITTED-RESULT","model_output":{"run_id":%q,"call_index":1}}`,
			`{"type":"model_output","model_output":{"run_id":%q,"call_index":1,"phase":"commit"}}`,
			`{"type":"done","summary":"ok","execution_id":%q}`,
		} {
			fmt.Fprintf(w, "data: "+frame+"\n\n", run)
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	stateStore := newModelFallbackStateStore(t)
	err := Run(t.Context(), db, stateStore, RunRequest{TaskID: "plain-retry"})
	task, _ := GetTask(t.Context(), db, "plain-retry")
	steps, _ := LoadSteps(t.Context(), db, "plain-retry")
	var textRows, closedRows, closedEvents int
	for _, row := range steps {
		if row.Role == "text" && strings.Contains(string(row.Content), "COMMITTED-RESULT") {
			textRows++
		}
		if row.Role == "model_runtime" {
			closedRows++
		}
	}
	for _, frame := range stateStore.rpushCalls {
		var event TaskEvent
		_ = json.Unmarshal(frame.value, &event)
		if event.ModelOutput != nil && event.ModelOutput.Phase == "commit" {
			closedEvents++
		}
	}
	if attempts != 2 || posts != 1 || err != nil || task.Status != StatusSucceeded || textRows != 1 || closedRows != 1 || closedEvents != 1 {
		raw, _ := json.Marshal(steps)
		t.Fatalf("retry did not commit/publish once: attempts=%d posts=%d err=%v status=%s text=%d cursors=%d published=%d rows=%s", attempts, posts, err, task.Status, textRows, closedRows, closedEvents, raw)
	}
}

func TestOrdinaryModelFallbackSwitchCommitRetryKeepsOriginalWireIdempotent(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	if db.Dialector.Name() != "sqlite" {
		t.Skip("SQLite busy retry regression")
	}
	seedTaskModel(t, db, "owner", "a", false)
	seedTaskModel(t, db, "owner", "b", false)
	if _, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "switch-retry", ConversationID: "conv", AgentType: "research", CreateUserID: "owner", Title: "retry"}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if err := db.Callback().Create().Before("gorm:create").Register("retry_model_switch", func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*orm.SubAgentStep); ok && row.Role == "model_runtime" && strings.Contains(string(row.Content), `"model_switch"`) {
			attempts++
			if attempts == 1 {
				tx.AddError(errors.New("database is locked (517)"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		var req RunRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		run := req.TaskSpec["execution_id"].(string)
		emit := func(ev map[string]any) {
			ev["execution_id"] = run
			raw, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		model := func(index int, phase string, ev map[string]any) {
			ev["model_output"] = &common.ModelOutput{RunID: run, CallIndex: index, Phase: phase}
			emit(ev)
		}
		model(1, "begin", map[string]any{"type": "model_output"})
		model(1, "", map[string]any{"type": "text", "text": "KEPT-A"})
		model(1, "commit", map[string]any{"type": "model_output"})
		emit(map[string]any{"type": "tool_results", "tool_results": []any{map[string]any{"tool_call_id": "tool1", "content": "RECEIPT"}}})
		emit(map[string]any{"type": "artifact", "slot": "report", "content_type": "text", "value": map[string]any{"text": "real artifact"}, "seq": 1})
		model(2, "begin", map[string]any{"type": "model_output"})
		model(2, "", map[string]any{"type": "text", "text": "REPLACEMENT-B"})
		model(2, "", map[string]any{"type": "think", "think": "B-thinking"})
		data := common.ModelSwitchedData{SwitchIndex: 1,
			From:       common.ModelSwitchIdentity{CandidateID: "own:a", Source: "own", ModelID: "a", ProviderGroupID: "group-a"},
			To:         common.ModelSwitchIdentity{CandidateID: "own:b", Source: "own", ModelID: "b", ProviderGroupID: "group-b", ModelName: "UNTRUSTED-NAME"},
			ReasonCode: "service_unavailable"}
		raw, _ := json.Marshal(data) // Nullable windows and display names differ from Core's public normalization.
		event := common.ModelRuntimeEvent{SchemaVersion: 1, EventID: "retried-switch", RunID: run, Type: "model_switched", Data: raw}
		model(2, "commit", map[string]any{"type": "model_output", "runtime_event": event})
		model(2, "commit", map[string]any{"type": "model_output", "runtime_event": event})
		emit(map[string]any{"type": "done", "summary": "ok"})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	stateStore := newModelFallbackStateStore(t)
	err := Run(t.Context(), db, stateStore, RunRequest{TaskID: "switch-retry"})
	task, _ := GetTask(t.Context(), db, "switch-retry")
	steps, _ := LoadSteps(t.Context(), db, "switch-retry")
	var bText, bThink, tools, switches int
	var record taskModelRecord
	for _, row := range steps {
		if row.Role == "text" && strings.Contains(string(row.Content), "REPLACEMENT-B") {
			bText++
		}
		if row.Role == "think" && strings.Contains(string(row.Content), "B-thinking") {
			bThink++
		}
		if row.Role == "tool" {
			tools++
		}
		if row.Role == "model_runtime" {
			_ = json.Unmarshal(row.Content, &record)
			if record.ModelSwitch != nil {
				switches++
			}
		}
	}
	view, viewErr := OrdinaryTask(t.Context(), db, task)
	var published int
	for _, frame := range stateStore.rpushCalls {
		var event TaskEvent
		_ = json.Unmarshal(frame.value, &event)
		if event.RuntimeEvent != nil {
			published++
		}
	}
	if attempts != 2 || posts != 1 || err != nil || viewErr != nil || task.Status != StatusSucceeded || bText != 1 || bThink != 1 || tools != 1 || switches != 1 || published != 1 || record.CurrentModel.ModelID != "b" || record.ModelOutput.Phase != "commit" || len(view.ProcessSteps) != 1 || len(view.StageArtifacts) != 1 || view.ModelSwitch == nil || view.ModelSwitch.To.ModelName != "b" || view.ModelSwitch.ToMaxInputTokens == nil || *view.ModelSwitch.ToMaxInputTokens != 8192 {
		raw, _ := json.Marshal(steps)
		t.Fatalf("retried switch/original-wire duplicate changed outcome: attempts=%d posts=%d err=%v status=%s text=%d think=%d tools=%d switches=%d published=%d viewErr=%v rows=%s", attempts, posts, err, task.Status, bText, bThink, tools, switches, published, viewErr, raw)
	}
}

func TestTaskFallbackDeploymentSwitch(t *testing.T) {
	t.Setenv("LAZYMIND_CHAT_MODEL_FALLBACK_ENABLED", "false")
	db := orm.MigrateAllModelsForTest(t).DB
	seedTaskModel(t, db, "actor", "a", false)
	seedTaskModel(t, db, "actor", "b", false)
	task := &orm.SubAgentTask{CreateUserID: "actor"}
	req := &RunRequest{}
	if err := prepareTaskModelInput(t.Context(), db, task, req); err != nil {
		t.Fatal(err)
	}
	if req.ModelFallbackCurrentID != "own:a" || len(req.ModelFallbackCandidates) != 1 || req.ModelFallbackInitialSwitch != nil {
		t.Fatal("disabled task fallback authorized another model")
	}
	req.InheritedModelOrigin = &InheritedModelOrigin{Identity: common.ModelSwitchIdentity{CandidateID: "own:missing", ModelID: "missing", Source: "own"}}
	if err := prepareTaskModelInput(t.Context(), db, task, req); !errors.Is(err, errTaskModelsExhausted) {
		t.Fatalf("missing inherited model must fail without switching: %v", err)
	}
}
