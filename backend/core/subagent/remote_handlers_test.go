package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
)

func remoteSubagentFixture(t *testing.T) *orm.DB {
	t.Helper()
	authService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
	}))
	t.Cleanup(authService.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", authService.URL)
	db := newTestDB(t)
	if err := db.AutoMigrate(
		&orm.WorkflowSessionStep{},
		&orm.UserSelectedModel{},
		&orm.UserSelectedProvider{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroupModel{},
		&orm.UserModelProviderGroup{},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := db.Create(&orm.SubAgentTask{ID: "task-remote", ConversationID: "conversation-1",
		AgentType: "workflow_step", Title: "remote", Objective: "run", Mode: "auto", Status: StatusPending,
		WorkspacePath: "/core/path/must-not-be-used", InputSlots: json.RawMessage(`[]`),
		OutputSlots: json.RawMessage(`["report"]`), CreateUserID: "user-1", LastHeartbeat: now,
		CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Minute)
	if err := db.Create(&orm.WorkflowSessionStep{ID: "attempt-remote", SessionID: "session-1", StepID: "step-1",
		Attempt: 1, TaskID: "task-remote", Status: "running", Validity: "effective", LeaseToken: "lease-live",
		LeaseExpiresAt: &expires, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	t.Setenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN", "executor-secret")
	return db
}

func TestRemoteArtifactEventEnforcesDeclaredFileType(t *testing.T) {
	db := remoteSubagentFixture(t)
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "task-remote").Update("params",
		json.RawMessage(`{"output_slot_types":{"report":"file"}}`)).Error; err != nil {
		t.Fatal(err)
	}
	rejected := postRemoteTaskEvent(t, "lease-live", map[string]any{
		"type": "artifact", "slot": "report", "content_type": "text", "value": map[string]any{"text": "draft"},
	})
	if rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	var count int64
	if err := db.Model(&orm.SubAgentArtifact{}).Where("task_id = ?", "task-remote").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func postRemoteTaskEvent(t *testing.T, lease string, event map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(event)
	req := httptest.NewRequest(http.MethodPost, "/internal/subagent/tasks/task-remote/events", bytes.NewReader(body))
	req = mux.SetURLVars(req, map[string]string{"task_id": "task-remote"})
	req.Header.Set("Authorization", "Bearer executor-secret")
	req.Header.Set("X-Workflow-Lease-Token", lease)
	rec := httptest.NewRecorder()
	InternalIngestTaskEvent(rec, req)
	return rec
}

func seedRemoteSearchProvider(t *testing.T, db *orm.DB) {
	t.Helper()
	now := time.Now()
	base := orm.BaseModel{CreateUserID: "user-1", CreateUserName: "user-1", CreatedAt: now, UpdatedAt: now}
	provider := orm.UserModelProvider{ID: "provider-tavily", DefaultModelProviderID: "default-tavily",
		Name: "Tavily", Category: "search", BaseModel: base}
	group := orm.UserModelProviderGroup{ID: "group-tavily", UserModelProviderID: provider.ID,
		Name: "Tavily", APIKey: "workflow-search-token", IsVerified: true, BaseModel: base}
	selected := orm.UserSelectedProvider{UserID: "user-1", UserName: "user-1", Category: "search",
		UserModelProviderGroupID: group.ID, CreatedAt: now, UpdatedAt: now}
	for _, value := range []any{&provider, &group, &selected} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteTaskEventsRequireBoundAttemptLease(t *testing.T) {
	remoteSubagentFixture(t)
	for _, lease := range []string{"", "stale"} {
		rec := postRemoteTaskEvent(t, lease, map[string]any{"type": "progress", "progress": 10})
		if rec.Code != http.StatusConflict && rec.Code != http.StatusUnauthorized {
			t.Fatalf("lease=%q status=%d body=%s", lease, rec.Code, rec.Body.String())
		}
	}
}

func TestRemoteExecutionSpecReturnsTaskParamsAndDurableSteps(t *testing.T) {
	db := remoteSubagentFixture(t)
	authService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/token") {
			_, _ = w.Write([]byte(`{"data":{"access_token":"feishu-token"}}`))
			return
		}
		if r.URL.Query().Get("provider") == "feishu" {
			_, _ = w.Write([]byte(`{"data":{"items":[{"connection_id":"connection-1"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
	}))
	t.Cleanup(authService.Close)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", authService.URL)
	seedRemoteSearchProvider(t, db)
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "task-remote").Update("params",
		json.RawMessage(`{"operation":"execute"}`)).Error; err != nil {
		t.Fatal(err)
	}
	if err := AppendRemoteStep(context.Background(), db.DB, "task-remote", "text",
		json.RawMessage(`{"content":"checkpoint"}`)); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/internal/subagent/tasks/task-remote/execution-spec", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "task-remote"})
	req.Header.Set("Authorization", "Bearer executor-secret")
	req.Header.Set("X-Workflow-Lease-Token", "lease-live")
	rec := httptest.NewRecorder()
	InternalGetExecutionSpec(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	data := getData(rec.Body.Bytes())
	params := data["params"].(map[string]any)
	steps := data["steps"].([]any)
	if params["operation"] != "execute" || len(steps) != 1 {
		t.Fatalf("data=%#v", data)
	}
	toolConfig := data["tool_config"].(map[string]any)
	if toolConfig["tavily"] != "workflow-search-token" {
		t.Fatalf("tool_config=%#v", toolConfig)
	}
	if toolConfig["feishu"] != "feishu-token" {
		t.Fatalf("tool_config=%#v", toolConfig)
	}
	if data["workspace_path"] != "/core/path/must-not-be-used" {
		t.Fatalf("workspace_path=%#v", data["workspace_path"])
	}
}

func TestRemoteTaskEventsPersistStreamStateAndInvalidatePanel(t *testing.T) {
	db := remoteSubagentFixture(t)
	previousHooks := EventHooks
	EventHooks = &eventHooks{}
	t.Cleanup(func() { EventHooks = previousHooks })
	updates := []string{}
	streamUpdates := []TaskEvent{}
	EventHooks.RegisterConversationEventHook(func(_ context.Context, _ state.Store, convID, _ string,
		eventType string, payload map[string]any) {
		if convID == "conversation-1" && eventType == "workflow_runtime_updated" {
			updates = append(updates, payload["change"].(string))
		}
		if convID == "conversation-1" && eventType == "task_updated" {
			streamUpdates = append(streamUpdates, payload["event"].(TaskEvent))
		}
	})

	events := []map[string]any{
		{"type": "task_start"},
		{"type": "text", "text": "hello"},
		{"type": "think", "think": "reason"},
		{"type": "tool_calls", "tool_calls": []map[string]any{{"id": "1", "name": "read"}}},
		{"type": "tool_results", "tool_results": []map[string]any{{"id": "1", "result": "ok"}}},
		{"type": "progress", "progress": 42, "current_phase": "working"},
		{"type": "artifact_stream_start", "slot": "draft_document", "content_type": "text/markdown",
			"stream_id": "stream-1", "chunk_index": 1},
		{"type": "artifact_stream", "slot": "draft_document", "content_type": "text/markdown",
			"stream_id": "stream-1", "chunk_index": 2, "delta": "hello"},
		{"type": "artifact_stream_end", "slot": "draft_document", "content_type": "text/markdown",
			"stream_id": "stream-1", "chunk_index": 3},
		{"type": "artifact_stream_abort", "slot": "outline_document", "content_type": "text/markdown",
			"stream_id": "stream-2", "chunk_index": 2, "message": "stopped"},
		{"type": "artifact", "slot": "report", "content_type": "text", "seq": 1,
			"value": map[string]any{"text": "result"}},
	}
	for _, event := range events {
		rec := postRemoteTaskEvent(t, "lease-live", event)
		if rec.Code != http.StatusOK {
			t.Fatalf("event=%v status=%d body=%s", event["type"], rec.Code, rec.Body.String())
		}
	}
	steps, err := LoadSteps(context.Background(), db.DB, "task-remote")
	if err != nil || len(steps) != 4 {
		t.Fatalf("steps=%#v err=%v", steps, err)
	}
	for i, role := range []string{"text", "think", "assistant", "tool"} {
		if steps[i].Seq != i || steps[i].Role != role {
			t.Fatalf("step[%d]=%#v", i, steps[i])
		}
	}
	task, _ := GetTask(context.Background(), db.DB, "task-remote")
	if task.Status != StatusRunning || task.ProgressPct != 42 || task.CurrentPhase != "working" {
		t.Fatalf("task=%#v", task)
	}
	artifacts, _ := LoadArtifacts(context.Background(), db.DB, "task-remote")
	if len(artifacts) != 1 || artifacts[0].Slot != "report" {
		t.Fatalf("artifacts=%#v", artifacts)
	}
	wantUpdates := []string{"task_start", "progress", "artifact"}
	if !reflect.DeepEqual(updates, wantUpdates) {
		t.Fatalf("updates=%v want=%v", updates, wantUpdates)
	}
	wantStreamTypes := []string{"artifact_stream_start", "artifact_stream", "artifact_stream_end", "artifact_stream_abort"}
	gotStreamTypes := make([]string, 0, len(streamUpdates))
	for _, event := range streamUpdates {
		gotStreamTypes = append(gotStreamTypes, event.Type)
	}
	if !reflect.DeepEqual(gotStreamTypes, wantStreamTypes) {
		t.Fatalf("stream updates=%v want=%v", gotStreamTypes, wantStreamTypes)
	}
	if streamUpdates[1].Delta != "hello" || streamUpdates[1].StreamID != "stream-1" || streamUpdates[1].ChunkIndex != 2 {
		t.Fatalf("stream delta=%#v", streamUpdates[1])
	}
}

func TestAppendRemoteStepAllocatesMonotonicSequence(t *testing.T) {
	db := remoteSubagentFixture(t)
	for _, role := range []string{"text", "think", "tool"} {
		if err := AppendRemoteStep(context.Background(), db.DB, "task-remote", role,
			json.RawMessage(`{"content":"x"}`)); err != nil {
			t.Fatal(err)
		}
	}
	steps, _ := LoadSteps(context.Background(), db.DB, "task-remote")
	for i := range steps {
		if steps[i].Seq != i {
			t.Fatalf("steps=%#v", steps)
		}
	}
}
