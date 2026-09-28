package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
)

func ordinaryFixture(t *testing.T) (*orm.DB, *orm.SubAgentTask) {
	t.Helper()
	db := newTestDB(t)
	task, err := CreateTask(context.Background(), db.DB, CreateTaskInput{TaskID: "ordinary-task", ConversationID: "conversation", TriggerHistoryID: "turn", AgentType: "research", Title: "公开任务", Objective: "SECRET objective", Mode: "manual", CreateUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return db, task
}
func publicStep(revision int64, status string) taskdisplay.PublicProcessStep {
	return taskdisplay.PublicProcessStep{StepID: "read", Revision: revision, Order: 0, Title: "读取文档", Status: status}
}

func TestOrdinaryProcessIdempotencyFencingAndIndependentRevisions(t *testing.T) {
	db, task := ordinaryFixture(t)
	ctx := context.Background()
	accepted, err := PersistPublicProcessStep(ctx, db.DB, task.ID, task.ExecutionID, "event-1", publicStep(1, "running"))
	if err != nil || !accepted {
		t.Fatalf("create: %t %v", accepted, err)
	}
	if accepted, err = PersistPublicProcessStep(ctx, db.DB, task.ID, task.ExecutionID, "event-1", publicStep(1, "running")); err != nil || accepted {
		t.Fatalf("duplicate: %t %v", accepted, err)
	}
	if _, err = PersistPublicProcessStep(ctx, db.DB, task.ID, task.ExecutionID, "event-1", publicStep(1, "failed")); !errors.Is(err, ErrPublicEventConflict) {
		t.Fatalf("conflicting duplicate accepted: %v", err)
	}
	second := publicStep(1, "pending")
	second.StepID = "write"
	second.Order = 1
	if _, err = PersistPublicProcessStep(ctx, db.DB, task.ID, task.ExecutionID, "event-b", second); err != nil {
		t.Fatal(err)
	}
	if _, err = PersistPublicProcessStep(ctx, db.DB, task.ID, task.ExecutionID, "event-2", publicStep(2, "succeeded")); err != nil {
		t.Fatal(err)
	}
	if _, err = PersistPublicProcessStep(ctx, db.DB, task.ID, task.ExecutionID, "event-3", publicStep(3, "running")); err == nil {
		t.Fatal("terminal step regressed")
	}
	old, err := ordinarySnapshot(ctx, db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.ProcessSteps) != 2 || old.ProcessSteps[0].Status != "succeeded" || old.ProcessSteps[1].StepID != "write" {
		t.Fatalf("wrong process: %#v", old.ProcessSteps)
	}
	if err := SaveArtifact(ctx, db.DB, task.ID, "document", "text", json.RawMessage(`{"text":"old output"}`), 1); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSources(ctx, db.DB, task.ID, json.RawMessage(`[{"url":"https://example.com/old"}]`)); err != nil {
		t.Fatal(err)
	}
	if err := beginDisplayExecution(ctx, db.DB, task.ID, "next-generation"); err != nil {
		t.Fatal(err)
	}
	if _, err := PersistPublicProcessStep(ctx, db.DB, task.ID, task.ExecutionID, "late", publicStep(4, "succeeded")); !errors.Is(err, ErrStaleExecution) {
		t.Fatalf("old generation accepted: %v", err)
	}
	current, err := ordinarySnapshot(ctx, db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision <= old.Revision || current.DisplayKey == old.DisplayKey || len(current.ProcessSteps) != 0 || len(current.Sources) != 0 || len(current.StageArtifacts) != 0 {
		t.Fatalf("generation contaminated: %#v", current)
	}
	var historical int64
	db.Model(&orm.SubAgentArtifact{}).Where("task_id = ?", task.ID).Count(&historical)
	if historical != 1 {
		t.Fatal("historical output deleted")
	}
}

func TestOrdinaryConcurrentDuplicateIsPersistedOnce(t *testing.T) {
	db, task := ordinaryFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := PersistPublicProcessStep(context.Background(), db.DB, task.ID, task.ExecutionID, "same-event", publicStep(1, "running"))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	db.Model(&orm.SubAgentStep{}).Where("role = ?", "process_step").Count(&count)
	if count != 1 {
		t.Fatalf("duplicate rows=%d", count)
	}
}

func TestOrdinaryTimingAndProjectionDoNotInventProcess(t *testing.T) {
	db, task := ordinaryFixture(t)
	ctx := context.Background()
	seedSubagentStep(t, db, task.ID, 0, "think", `{"content":"SECRET reasoning"}`)
	seedSubagentStep(t, db, task.ID, 1, "plan", `{"steps":["Planned only"]}`)
	snapshot, err := ordinarySnapshot(ctx, db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Timing.StartedAt != nil || snapshot.Timing.ExecutionElapsedMS != nil || snapshot.Timing.ThinkingElapsedMS != nil || snapshot.ProcessState != "not_provided" || len(snapshot.ProcessSteps) != 0 {
		t.Fatalf("invented timing/process: %#v", snapshot)
	}
	if _, err := AcceptTaskStart(ctx, db.DB, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptFinalStatus(ctx, db.DB, task.ID, StatusSucceeded, "SECRET summary"); err != nil {
		t.Fatal(err)
	}
	finished, err := ordinarySnapshot(ctx, db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Timing.StartedAt == nil || finished.Timing.FinishedAt == nil || finished.Timing.ExecutionElapsedMS == nil {
		t.Fatalf("authoritative timing missing: %#v", finished.Timing)
	}
	payload, _ := json.Marshal(finished)
	if strings.Contains(string(payload), "SECRET") || strings.Contains(string(payload), "objective") || strings.Contains(string(payload), "plan_steps") {
		t.Fatalf("private output: %s", payload)
	}
}

func TestOrdinaryFailedTaskPreservesSafeCapabilityRecovery(t *testing.T) {
	db, task := ordinaryFixture(t)
	summary := `private-prefix MEDIA_CAPABILITY_DEPENDENCY_MISSING {"status":"blocked","workflow":"private-workflow","required":["video_generator","ffmpeg","unknown"],"missing":[{"id":"video_generator","label":"private-label","available":false,"settings_url":"https://evil.example/?token=private-token","reason":"private-error"},{"id":"ffmpeg","available":false},{"id":"video_generator","available":false},{"id":"unknown","available":false}],"message":"private-message","debug":"private-debug"} private-suffix`
	if _, err := AcceptFinalStatus(context.Background(), db.DB, task.ID, StatusFailed, summary); err != nil {
		t.Fatal(err)
	}
	view, err := ordinarySnapshot(context.Background(), db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The recovery contract must survive summary-only REST pages and SSE snapshots.
	page, err := pageOrdinaryTask(view, httptest.NewRequest("GET", "/task", nil), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{page, taskdisplay.SnapshotEvent{Type: "task_snapshot", Data: view}} {
		raw, _ := json.Marshal(value)
		if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "evil.example") || strings.Contains(string(raw), "unknown") {
			t.Fatalf("raw failure data leaked: %s", raw)
		}
		var payload map[string]any
		_ = json.Unmarshal(raw, &payload)
		if data, ok := payload["data"].(map[string]any); ok {
			payload = data
		}
		recovery, ok := payload["capability_dependency"].(map[string]any)
		if !ok || recovery["status"] != "blocked" {
			t.Fatalf("missing public recovery: %s", raw)
		}
		missing := recovery["missing"].([]any)
		if len(missing) != 2 || missing[0].(map[string]any)["settings_url"] != "/settings?section=models&target=video_generator" || missing[1].(map[string]any)["settings_url"] != "/settings?section=system_tools#ffmpeg-dependency" {
			t.Fatalf("unsafe or incomplete recovery: %s", raw)
		}
	}
}

func TestOrdinaryCapabilityRecoveryRejectsNonFailuresAndInvalidPayloads(t *testing.T) {
	for _, tc := range []struct{ name, status, summary string }{
		{"success", StatusSucceeded, `MEDIA_CAPABILITY_DEPENDENCY_MISSING {"status":"blocked","missing":[{"id":"image_generator","available":false}]}`},
		{"malformed", StatusFailed, `MEDIA_CAPABILITY_DEPENDENCY_MISSING {"status":"blocked","missing":`},
		{"unmarked", StatusFailed, `{"status":"blocked","missing":[{"id":"image_generator","available":false}]}`},
		{"ready", StatusFailed, `MEDIA_CAPABILITY_DEPENDENCY_MISSING {"status":"ready","missing":[{"id":"image_generator","available":false}]}`},
		{"unknown", StatusFailed, `MEDIA_CAPABILITY_DEPENDENCY_MISSING {"status":"blocked","missing":[{"id":"unknown","available":false}]}`},
		{"available", StatusFailed, `MEDIA_CAPABILITY_DEPENDENCY_MISSING {"status":"blocked","missing":[{"id":"image_generator","available":true}]}`},
		{"missing_availability", StatusFailed, `MEDIA_CAPABILITY_DEPENDENCY_MISSING {"status":"blocked","missing":[{"id":"image_generator"}]}`},
		{"oversized", StatusFailed, `MEDIA_CAPABILITY_DEPENDENCY_MISSING {"status":"blocked","missing":[{"id":"image_generator","available":false}],"message":"` + strings.Repeat("x", 64*1024) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, task := ordinaryFixture(t)
			if _, err := AcceptFinalStatus(context.Background(), db.DB, task.ID, tc.status, tc.summary); err != nil {
				t.Fatal(err)
			}
			view, err := ordinarySnapshot(context.Background(), db.DB, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(view)
			if strings.Contains(string(raw), "capability_dependency") {
				t.Fatalf("invalid recovery exported: %s", raw)
			}
		})
	}
}

func TestOrdinaryPagesAreBoundedAndRejectStaleCursor(t *testing.T) {
	view := taskdisplay.NewTask()
	view.DisplayKey = "task:current"
	view.Revision = 2
	for i := 0; i < 103; i++ {
		view.Sources = append(view.Sources, taskdisplay.PublicSource{SourceID: fmt.Sprint(i), Title: "source"})
	}
	view.Pages.Sources = taskdisplay.CollectionPage{Total: 103, Revision: 2}
	first, err := pageOrdinaryTask(view, httptest.NewRequest("GET", "/task?limit=100", nil), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Sources) != 100 || first.Pages.Sources.NextCursor == nil || first.Pages.Sources.Total != 103 {
		t.Fatalf("incorrect first page: %#v", first.Pages.Sources)
	}
	target := "/task?collection=sources&cursor=" + url.QueryEscape(*first.Pages.Sources.NextCursor)
	second, err := pageOrdinaryTask(view, httptest.NewRequest("GET", target, nil), false)
	if err != nil || len(second.Sources) != 3 || second.Pages.Sources.NextCursor != nil {
		t.Fatalf("second page: %#v %v", second.Pages.Sources, err)
	}
	view.Revision = 3
	if _, err := pageOrdinaryTask(view, httptest.NewRequest("GET", target, nil), false); !errors.Is(err, errDisplayCursor) {
		t.Fatalf("stale cursor accepted: %v", err)
	}
}

func TestOrdinaryArtifactWhitelistAndMissingFiles(t *testing.T) {
	rows := []orm.SubAgentArtifact{
		{ID: "text", Slot: "报告", ContentType: "text", Value: json.RawMessage(`{"text":"Public report","path":"/private/SECRET","internal":"SECRET"}`)},
		{ID: "hidden", Slot: "hidden", ContentType: "text", Value: json.RawMessage(`{"text":"SECRET hidden"}`), Hidden: true},
		{ID: "bad-url", Slot: "file", ContentType: "file", Value: json.RawMessage(`{"path":"javascript:alert(1)"}`)},
		{ID: "credential", Slot: "file", ContentType: "file", Value: json.RawMessage(`{"url":"https://example.com/download?token=SECRET"}`)},
	}
	artifacts := OrdinaryArtifacts(rows, "", "task:key")
	if len(artifacts) != 3 {
		t.Fatalf("artifacts: %#v", artifacts)
	}
	if artifacts[0].InlineContent != "Public report" || !artifacts[0].Capabilities.Preview || artifacts[0].SizeBytes == nil {
		t.Fatalf("text not previewable: %#v", artifacts[0])
	}
	for _, art := range artifacts[1:] {
		if art.State != "unavailable" || art.Capabilities.Open || art.DownloadURL != "" {
			t.Fatalf("unsafe file exposed: %#v", art)
		}
	}
	payload, _ := json.Marshal(artifacts)
	if strings.Contains(string(payload), "SECRET") {
		t.Fatalf("sensitive metadata leaked: %s", payload)
	}
}

func TestOrdinaryHTTPReadAndStreamEnforceOwnerAndWhitelist(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "owned", "conv", "owner", StatusSucceeded)
	seedSubagentStep(t, db, "owned", 1, "think", `{"content":"SECRET thoughts"}`)
	seedSubagentArtifact(t, db, "owned", "report", 1, "text", `{"text":"Public report"}`)
	for _, handler := range []http.HandlerFunc{GetTaskDetail, GetTaskArtifacts, StreamTask} {
		for _, owner := range []string{"owner", "other"} {
			req := httptest.NewRequest("GET", "/tasks/owned?view=ordinary", nil)
			req = mux.SetURLVars(req, map[string]string{"task_id": "owned"})
			req.Header.Set("X-User-Id", owner)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if owner == "other" {
				if rec.Code != 404 {
					t.Fatalf("cross-user code=%d", rec.Code)
				}
				continue
			}
			if rec.Code != 200 {
				t.Fatalf("owner code=%d body=%s", rec.Code, rec.Body.String())
			}
			for _, forbidden := range []string{"SECRET", "Test objective", "current_phase", "\"think\"", "workspace_path"} {
				if strings.Contains(rec.Body.String(), forbidden) {
					t.Fatalf("private field leaked: %s", rec.Body.String())
				}
			}
		}
	}
}

func TestOrdinaryRunningElapsedIsFrozenAtFinish(t *testing.T) {
	db, task := ordinaryFixture(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-3 * time.Second)
	end := start.Add(time.Second)
	if err := db.Model(task).Updates(map[string]any{"status": StatusSucceeded, "started_at": start, "finished_at": end}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := ordinarySnapshot(ctx, db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Timing.ExecutionElapsedMS == nil || *view.Timing.ExecutionElapsedMS != 1000 {
		t.Fatalf("duration = %#v", view.Timing)
	}
}

func TestOrdinaryLocalEventRoutesWithinExecutionTransaction(t *testing.T) {
	db, task := ordinaryFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	step := publicStep(1, "running")
	events := []TaskEvent{{Type: "process_step", TaskID: task.ID, EventID: "local", ProcessStep: &step}, {Type: "sources", TaskID: task.ID, Sources: json.RawMessage(`[{"url":"https://example.com/"}]`)}, {Type: "artifact", TaskID: task.ID, ArtifactKey: "report", ContentType: "text", Value: json.RawMessage(`{"text":"Output"}`)}}
	for _, event := range events {
		accepted, err := routeExecutionEvent(ctx, db.DB, task.ID, task.ExecutionID, event, func(tx *gorm.DB, event TaskEvent) (bool, error) { return persistTaskEvent(ctx, tx, event) })
		if err != nil || !accepted {
			t.Fatalf("%s within execution transaction: %t %v", event.Type, accepted, err)
		}
	}
	snapshot, err := ordinarySnapshot(ctx, db.DB, task.ID)
	if err != nil || len(snapshot.ProcessSteps) != 1 || len(snapshot.Sources) != 1 || len(snapshot.StageArtifacts) != 1 {
		t.Fatalf("event projection failed: %#v %v", snapshot, err)
	}
}

func TestOrdinaryIngestEndpointFencesLeaseAndPublishesOnlyDurableSteps(t *testing.T) {
	db := remoteSubagentFixture(t)
	if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "task-remote").Update("execution_id", "execution-remote").Error; err != nil {
		t.Fatal(err)
	}
	event := map[string]any{"type": "process_step", "event_id": "event-1", "execution_id": "execution-remote", "process_step": publicStep(1, "running")}
	for _, lease := range []string{"old-lease", "lease-live", "lease-live"} {
		reply := postRemoteTaskEvent(t, lease, event)
		want := 200
		if lease == "old-lease" {
			want = 409
		}
		if reply.Code != want {
			t.Fatalf("lease %s status=%d body=%s", lease, reply.Code, reply.Body.String())
		}
	}
	var count int64
	db.Model(&orm.SubAgentStep{}).Where("task_id = ? AND role = ?", "task-remote", "process_step").Count(&count)
	if count != 1 {
		t.Fatalf("duplicate endpoint event count=%d", count)
	}
	event["execution_id"] = "old-execution"
	event["event_id"] = "late"
	if reply := postRemoteTaskEvent(t, "lease-live", event); reply.Code != 409 {
		t.Fatalf("old execution accepted status=%d", reply.Code)
	}
	expired := time.Now().UTC().Add(-time.Second)
	db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-remote").Update("lease_expires_at", expired)
	event["execution_id"] = "execution-remote"
	if reply := postRemoteTaskEvent(t, "lease-live", event); reply.Code != 409 {
		t.Fatalf("expired lease accepted status=%d", reply.Code)
	}
}

func TestOrdinaryPublicChangesAdvanceWorkflowDurableCursor(t *testing.T) {
	db, task := ordinaryFixture(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowEvent{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	db.Create(&orm.WorkflowSession{ID: "workflow", ConversationID: task.ConversationID, WorkflowID: "example", CreateUserID: "owner", CreatedAt: now, UpdatedAt: now})
	db.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: "workflow", StepID: "step", TaskID: task.ID, Status: "running", CreatedAt: now, UpdatedAt: now})
	if err := db.Model(task).Update("agent_type", "workflow_step").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := PersistPublicProcessStep(ctx, db.DB, task.ID, task.ExecutionID, "public-event", publicStep(1, "running")); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSources(ctx, db.DB, task.ID, json.RawMessage(`[{"url":"https://example.com/"}]`)); err != nil {
		t.Fatal(err)
	}
	if err := SaveArtifact(ctx, db.DB, task.ID, "report", "text", json.RawMessage(`{"text":"Output"}`), 1); err != nil {
		t.Fatal(err)
	}
	var events []orm.WorkflowEvent
	if err := db.Where("session_id = ?", "workflow").Order("id ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("durable notifications=%d", len(events))
	}
	for i, event := range events {
		if event.EventType != "ordinary.task_changed" || event.EntityID != "attempt" || event.OwnerUserID != "owner" || string(event.PayloadJSON) != "{}" {
			t.Fatalf("invalid notification: %#v", event)
		}
		if i > 0 && events[i-1].ID >= event.ID {
			t.Fatal("cursor did not advance")
		}
	}
}

// A body reader models lease replacement after the handler's initial auth read
// and before persistence, without timing/sleep dependent concurrency.
type rotateLeaseReader struct {
	read   bool
	body   *strings.Reader
	rotate func()
}

func (reader *rotateLeaseReader) Read(data []byte) (int, error) {
	if !reader.read {
		reader.read = true
		reader.rotate()
	}
	return reader.body.Read(data)
}

func TestOrdinaryRemoteLeaseRotationAfterAuthorizationRejectsEveryPublicMutation(t *testing.T) {
	for _, kind := range []string{"process_step", "sources", "artifact", "progress", "plan"} {
		t.Run(kind, func(t *testing.T) {
			db := remoteSubagentFixture(t)
			db.Model(&orm.SubAgentTask{}).Where("id = ?", "task-remote").Update("execution_id", "current")
			event := TaskEvent{Type: kind, EventID: "rotation-event", ExecutionID: "current", Sources: json.RawMessage(`[{"url":"https://example.com/"}]`), ArtifactKey: "report", ContentType: "text", Value: json.RawMessage(`{"text":"Output"}`), Progress: 80, Steps: []string{"plan"}}
			step := publicStep(1, "running")
			event.ProcessStep = &step
			body, _ := json.Marshal(event)
			reader := &rotateLeaseReader{body: strings.NewReader(string(body)), rotate: func() {
				if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-remote").Update("lease_token", "new-lease").Error; err != nil {
					t.Fatal(err)
				}
			}}
			req := httptest.NewRequest("POST", "/internal/subagent/tasks/task-remote/events", reader)
			req = mux.SetURLVars(req, map[string]string{"task_id": "task-remote"})
			req.Header.Set("Authorization", "Bearer executor-secret")
			req.Header.Set("X-Workflow-Lease-Token", "lease-live")
			rec := httptest.NewRecorder()
			InternalIngestTaskEvent(rec, req)
			if rec.Code != 409 {
				t.Fatalf("old %s request accepted: %d %s", kind, rec.Code, rec.Body.String())
			}
			var steps, artifacts int64
			db.Model(&orm.SubAgentStep{}).Where("task_id = ?", "task-remote").Count(&steps)
			db.Model(&orm.SubAgentArtifact{}).Where("task_id = ?", "task-remote").Count(&artifacts)
			task, err := GetTask(context.Background(), db.DB, "task-remote")
			if err != nil {
				t.Fatal(err)
			}
			if steps != 0 || artifacts != 0 || task.ProgressPct != 0 || string(task.Sources) != "[]" {
				t.Fatalf("mutation escaped lease fence: steps=%d artifacts=%d task=%#v", steps, artifacts, task)
			}
		})
	}
}

func TestOrdinaryArtifactReplayDoesNotDuplicateOrRewriteVersion(t *testing.T) {
	db, task := ordinaryFixture(t)
	ctx := context.Background()
	first, err := saveArtifactIfChanged(ctx, db.DB, task.ID, "report", "text", json.RawMessage(`{"text":"same"}`), 1)
	if err != nil || !first {
		t.Fatalf("first: %t %v", first, err)
	}
	duplicate, err := saveArtifactIfChanged(ctx, db.DB, task.ID, "report", "text", json.RawMessage(`{ "text": "same" }`), 1)
	if err != nil || duplicate {
		t.Fatalf("replay: %t %v", duplicate, err)
	}
	if _, err := saveArtifactIfChanged(ctx, db.DB, task.ID, "report", "text", json.RawMessage(`{"text":"different"}`), 1); !errors.Is(err, ErrPublicEventConflict) {
		t.Fatalf("conflicting version accepted: %v", err)
	}
	snapshot, err := ordinarySnapshot(ctx, db.DB, task.ID)
	if err != nil || len(snapshot.StageArtifacts) != 1 || snapshot.StageArtifacts[0].InlineContent != "same" {
		t.Fatalf("artifact replay altered output: %#v %v", snapshot.StageArtifacts, err)
	}
}

func TestOrdinaryReadErrorsReturnCorrelatedRequestID(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "owned", "conv", "owner", StatusSucceeded)
	for _, handler := range []http.HandlerFunc{GetTaskDetail, GetTaskArtifacts, StreamTask} {
		req := httptest.NewRequest("GET", "/tasks/owned?view=ordinary&token=SECRET", nil)
		req = mux.SetURLVars(req, map[string]string{"task_id": "owned"})
		req.Header.Set("X-User-Id", "other")
		req.Header.Set("X-Request-ID", "SECRET-client-id")
		response := httptest.NewRecorder()
		handler(response, req)
		if response.Code != 404 {
			t.Fatalf("ownership changed: status=%d", response.Code)
		}
		id := response.Header().Get("X-Request-ID")
		data := getData(response.Body.Bytes())
		if id == "" || data["request_id"] != id || strings.Contains(response.Body.String(), "SECRET") {
			t.Fatalf("missing/unsafe request ID: %s %s", id, response.Body.String())
		}
	}
}
