package workflow

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
	"lazymind/core/store"
	"lazymind/core/workflow/attempt"
	"lazymind/core/workflow/graphengine"
)

func ordinaryFixture(t *testing.T) (*orm.DB, orm.WorkflowSession, time.Time) {
	t.Helper()
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowTransitionCommand{}, &orm.DocumentPublicationBinding{}, &orm.WorkflowInputBinding{}, &orm.WorkflowApprovalPreference{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, GraphHash: "ordinary-graph", StaticOrder: []string{"write", "review"}, Nodes: map[string]graphengine.CompiledNode{
		"write":  {ID: "write", Label: "撰写报告", Prompt: "SECRET_SYSTEM_PROMPT", Outputs: []string{"report"}},
		"review": {ID: "review", Label: "校对报告", Outputs: []string{"final"}},
	}, MaterialTypes: map[string]string{"report": "text", "final": "text"}, ControlEdges: []graphengine.CompiledEdge{{From: "write", To: "review"}, {From: "review", To: "__end__"}}}
	if err := db.Create(&orm.WorkflowRevision{ID: "ordinary-revision", WorkflowResourceID: "resource", RevisionNo: 1, GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion, CompiledGraph: graph.JSON()}).Error; err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "ordinary-session", ConversationID: "conversation", WorkflowID: "writer", WorkflowRevisionID: "ordinary-revision", GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion, CreateUserID: "owner", Status: "active", IntentContext: "SECRET_INTENT", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	return db, session, now
}

func TestOrdinaryWorkflowHostedAttemptIdentityAndPublicReplay(t *testing.T) {
	db, session, now := ordinaryFixture(t)
	for _, row := range []orm.WorkflowSessionStep{
		{ID: "old", SessionID: session.ID, StepID: "write", TaskID: "old", Attempt: 1, Status: "failed", Validity: "stale", CreatedAt: now, UpdatedAt: now},
		{ID: "current", SessionID: session.ID, StepID: "write", TaskID: "current", Attempt: 2, Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now.Add(time.Hour)},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	public, _ := json.Marshal(attempt.PublicDisplay{SchemaVersion: 1, EventKey: "step", ProcessSteps: []taskdisplay.PublicProcessStep{{StepID: "draft", Revision: 1, Order: 1, Title: "撰写初稿", Status: "succeeded"}}, Sources: json.RawMessage(`[{"url":"https://example.com/report?section=a","title":"参考资料","prompt":"SECRET_SOURCE"},{"url":"javascript:alert(1)"}]`)})
	for _, event := range []orm.WorkflowEvent{
		{EntityID: "old", EventType: "attempt.public_display", PayloadJSON: json.RawMessage(`{"schema_version":1,"event_key":"old","process_steps":[{"step_id":"old","revision":1,"order":0,"title":"OLD_ATTEMPT","status":"failed"}]}`), CreatedAt: now},
		{EntityID: "current", EventType: "attempt.patch", PayloadJSON: json.RawMessage(`{"status":"running"}`), CreatedAt: now.Add(time.Second)},
		{EntityID: "current", EventType: "attempt.progress", PayloadJSON: json.RawMessage(`{"think":"SECRET_REASONING"}`), CreatedAt: now.Add(2 * time.Second)},
		{EntityID: "current", EventType: "attempt.public_display", PayloadJSON: public, CreatedAt: now.Add(3 * time.Second)},
		{EntityID: "current", EventType: "attempt.patch", PayloadJSON: json.RawMessage(`{"status":"succeeded"}`), CreatedAt: now.Add(4 * time.Second)},
	} {
		event.SessionID = session.ID
		event.OwnerUserID = "owner"
		if err := db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	view, err := projectOrdinarySession(t.Context(), db.DB, &session)
	if err != nil {
		t.Fatal(err)
	}
	got := view.Tasks[0]
	if got.TaskID != nil || got.AttemptID == nil || *got.AttemptID != "current" || got.DisplayKey != "workflow:ordinary-session:write:current" || got.Title != "撰写报告" {
		t.Fatalf("hosted identity: %#v", got)
	}
	if len(got.ProcessSteps) != 1 || got.ProcessSteps[0].StepID != "draft" || len(got.Sources) != 1 {
		t.Fatalf("public data: %#v", got)
	}
	if got.Timing.ExecutionElapsedMS == nil || *got.Timing.ExecutionElapsedMS != 3000 || got.Timing.ThinkingElapsedMS != nil {
		t.Fatalf("timing: %#v", got.Timing)
	}
	encoded, _ := json.Marshal(view)
	for _, secret := range []string{"SECRET", "OLD_ATTEMPT", "graph_hash", "intent_context", "prompt"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("private payload %q leaked", secret)
		}
	}
	if view.Tasks[1].ProcessState != "not_provided" || view.Tasks[1].Timing.StartedAt != nil {
		t.Fatal("pending step must not invent process or timing")
	}
}

func TestOrdinaryWorkflowFinalArtifactsExcludeUnchosenEndBranches(t *testing.T) {
	db, session, now := ordinaryFixture(t)
	graph, err := loadSessionGraph(t.Context(), db.DB, &session)
	if err != nil {
		t.Fatal(err)
	}
	graph.ControlEdges = append(graph.ControlEdges, graphengine.CompiledEdge{From: "write", To: "__end__", When: "only preparation requested"})
	if err := db.Model(&orm.WorkflowRevision{}).Where("id = ?", session.WorkflowRevisionID).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ step, slot string }{{"write", "report"}, {"review", "final"}} {
		row := orm.WorkflowSessionStep{ID: item.step + "-attempt", SessionID: session.ID, StepID: item.step, Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.WorkflowSlotRevision{ID: item.slot + "-artifact", SessionID: session.ID, StepID: item.step, Attempt: 1, ProducerAttemptID: row.ID, SlotID: item.slot, Slot: item.slot, Revision: 1, Selected: true, Validity: "effective", ContentSnapshot: json.RawMessage(`{"text":"Public output"}`), CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&orm.WorkflowRouteDecision{ID: "chosen-route", SessionID: session.ID, FromStepID: "write", SourceAttemptID: "write-attempt", ActivatedJSON: json.RawMessage(`["review"]`), PrunedJSON: json.RawMessage(`["__end__"]`), BypassedJSON: json.RawMessage(`[]`), WitnessJSON: json.RawMessage(`[]`), Validity: "effective", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := projectOrdinarySession(t.Context(), db.DB, &session)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tasks[0].StageArtifacts) != 1 || len(view.Runs[0].FinalArtifacts) != 1 || view.Runs[0].FinalArtifacts[0].ArtifactID != "final-artifact" {
		t.Fatalf("unchosen end branch promoted stage outputs: %#v", view.Runs)
	}
}

func TestOrdinaryWorkflowFinalArtifactsSurviveStagePagination(t *testing.T) {
	db, session, now := ordinaryFixture(t)
	row := orm.WorkflowSessionStep{ID: "review-attempt", SessionID: session.ID, StepID: "review", Attempt: 1, Status: "succeeded", Validity: "effective", CreatedAt: now, UpdatedAt: now.Add(time.Hour)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"final-a", "final-b"} {
		if err := db.Create(&orm.WorkflowSlotRevision{ID: id, SessionID: session.ID, StepID: "review", Attempt: 1, ProducerAttemptID: row.ID, SlotID: "final", Slot: "final", Revision: 1, Selected: true, Validity: "effective", ContentSnapshot: json.RawMessage(`{"text":"Final report"}`), CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	full, err := projectOrdinarySession(t.Context(), db.DB, &session)
	if err != nil {
		t.Fatal(err)
	}
	if full.Tasks[1].Timing.ExecutionElapsedMS != nil {
		t.Fatal("legacy timestamps must not invent execution duration")
	}
	first := full
	first.Tasks = append([]taskdisplay.OrdinaryTaskView{}, full.Tasks...)
	if err := paginateOrdinaryProjection(&first, httptest.NewRequest("GET", "/projection?limit=1", nil)); err != nil {
		t.Fatal(err)
	}
	if len(first.Tasks[1].StageArtifacts) != 1 || len(first.Runs[0].FinalArtifacts) != 2 || len(first.Runs[0].FinalOutputRefs) != 2 {
		t.Fatalf("pagination lost final outputs: %#v", first)
	}
	cursor := first.Tasks[1].Pages.StageArtifacts.NextCursor
	if cursor == nil {
		t.Fatal("missing continuation")
	}
	query := url.Values{"display_key": {first.Tasks[1].DisplayKey}, "collection": {"stage_artifacts"}, "limit": {"1"}, "cursor": {*cursor}}
	if err := paginateOrdinaryProjection(&full, httptest.NewRequest("GET", "/projection?"+query.Encode(), nil)); err != nil {
		t.Fatal(err)
	}
	if len(full.Tasks[1].StageArtifacts) != 1 || full.Tasks[1].StageArtifacts[0].ArtifactID == first.Tasks[1].StageArtifacts[0].ArtifactID {
		t.Fatal("continuation did not advance")
	}
	full.Revision++
	if err := paginateOrdinaryProjection(&full, httptest.NewRequest("GET", "/projection?"+query.Encode(), nil)); !errors.Is(err, errOrdinaryCursor) {
		t.Fatalf("stale cursor accepted: %v", err)
	}
}

func TestOrdinaryWorkflowOnlyExplicitParallelCommandsGroupTasks(t *testing.T) {
	db, session, _ := ordinaryFixture(t)
	groups, err := ordinaryParallelGroups(t.Context(), db.DB, session.ID)
	if err != nil || len(groups) != 0 {
		t.Fatalf("historical group was invented: %v %#v", err, groups)
	}
	response, _ := json.Marshal(transitionCommandResponse{Accepted: true, Tasks: []transitionTaskResponse{{StepID: "write", TaskID: "one"}, {StepID: "review", TaskID: "two"}}})
	if err := db.Create(&orm.WorkflowTransitionCommand{CommandID: "batch", SessionID: session.ID, Operation: "execute_batch", Status: "accepted", ResponseJSON: response}).Error; err != nil {
		t.Fatal(err)
	}
	groups, err = ordinaryParallelGroups(t.Context(), db.DB, session.ID)
	if err != nil || groups["one"] != "batch" || groups["two"] != "batch" {
		t.Fatalf("batch grouping: %v %#v", err, groups)
	}
}

func TestOrdinaryWorkflowProjectionRequiresOwnerAndMasksPrivateFields(t *testing.T) {
	db, session, _ := ordinaryFixture(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, owner := range []string{"owner", "intruder", ""} {
		req := mux.SetURLVars(httptest.NewRequest("GET", "/projection?view=ordinary", nil), map[string]string{"session_id": session.ID})
		req.Header.Set("X-User-Id", owner)
		response := httptest.NewRecorder()
		GetSessionProjection(response, req)
		requestID := response.Header().Get("X-Request-ID")
		if requestID == "" {
			t.Fatal("missing request correlation header")
		}
		if owner == "owner" {
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"tasks"`) || strings.Contains(response.Body.String(), "SECRET") {
				t.Fatalf("public response: %d %s", response.Code, response.Body.String())
			}
		} else if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), `"tasks"`) || !strings.Contains(response.Body.String(), requestID) {
			t.Fatalf("unauthorized response: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestOrdinaryWorkflowProjectionRestoresCurrentAttemptPlan(t *testing.T) {
	db, session, now := ordinaryFixture(t)
	for _, id := range []string{"old", "current"} {
		if err := db.Create(&orm.SubAgentTask{ID: id, ConversationID: session.ConversationID, AgentType: "workflow_step", ExecutionID: id, StartedAt: &now, Status: "running", ProgressPct: 50, InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)}).Error; err != nil {
			t.Fatal(err)
		}
		content, _ := json.Marshal(map[string]any{"scope_version": 2, "steps": []string{id + " read", id + " check", id + " write"}, "private_log": "SECRET"})
		if err := db.Create(&orm.SubAgentStep{ID: id + "-plan", TaskID: id, Role: "plan", Content: content, CreatedAt: now.Add(time.Second)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for index, id := range []string{"old", "current"} {
		if err := db.Create(&orm.WorkflowSessionStep{ID: id, TaskID: id, SessionID: session.ID, StepID: "write", Attempt: index + 1, Status: "running", Validity: "effective", CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	req := mux.SetURLVars(httptest.NewRequest("GET", "/projection?view=ordinary", nil), map[string]string{"session_id": session.ID})
	req.Header.Set("X-User-Id", "owner")
	response := httptest.NewRecorder()
	GetSessionProjection(response, req)
	var body struct {
		Data ordinaryProjection `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatalf("projection failed: %d", response.Code)
	}
	if len(body.Data.Tasks) != 2 || len(body.Data.Tasks[0].PlanSteps) != 3 || body.Data.Tasks[0].PlanSteps[0] != "current read" || len(body.Data.Tasks[0].ProcessSteps) != 0 {
		t.Fatalf("current plan missing: %#v", body.Data.Tasks)
	}
	if strings.Contains(response.Body.String(), "SECRET") || strings.Contains(response.Body.String(), "old read") {
		t.Fatal("private or old plan content leaked")
	}
	if body.Data.Tasks[0].ProgressPct == nil || *body.Data.Tasks[0].ProgressPct != 50 {
		t.Fatal("overall progress missing from current attempt")
	}
}
