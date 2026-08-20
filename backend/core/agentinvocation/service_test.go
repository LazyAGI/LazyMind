package agentinvocation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/externalcontext"
)

const testRequestHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestServiceInvocationLifecycleIsIdempotent(t *testing.T) {
	service := newTestService(t)
	input := testStartInput("inv-1", "knowledge.search")
	input.RequestSummary = json.RawMessage(`{"query_length":12,"knowledge_id":"kb-1"}`)

	record, created, err := service.Start(context.Background(), "user-1", input)
	if err != nil || !created {
		t.Fatalf("start: created=%v err=%v", created, err)
	}
	if record.Status != StatusRunning || record.OwnerUserID != "user-1" {
		t.Fatalf("unexpected record: %+v", record)
	}

	replay := input
	replay.RequestSummary = json.RawMessage(`{"knowledge_id":"kb-1","query_length":12}`)
	if _, created, err = service.Start(context.Background(), "user-1", replay); err != nil || created {
		t.Fatalf("idempotent start: created=%v err=%v", created, err)
	}

	conflict := input
	conflict.ClientName = "cursor"
	if _, _, err = service.Start(context.Background(), "user-1", conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("start conflict: %v", err)
	}

	finish := FinishInput{
		Status: StatusSucceeded, ResultSummary: json.RawMessage(`{"session_id":"session-1","status":"running"}`),
		WorkflowID: "workflow-1", SessionID: "session-1", StepID: "draft", AttemptID: "attempt-1",
		ArtifactID: "artifact-1", CommandID: "command-1", ExternalRef: "codex-thread-1",
	}
	finished, err := service.Finish(context.Background(), "user-1", input.ID, finish)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if finished.FinishedAt == nil || finished.SessionID != "session-1" || finished.Status != StatusSucceeded {
		t.Fatalf("unexpected finish: %+v", finished)
	}

	replayFinish := finish
	replayFinish.ResultSummary = json.RawMessage(`{"status":"running","session_id":"session-1"}`)
	if _, err = service.Finish(context.Background(), "user-1", input.ID, replayFinish); err != nil {
		t.Fatalf("idempotent finish: %v", err)
	}
	changed := finish
	changed.ArtifactID = "artifact-2"
	if _, err = service.Finish(context.Background(), "user-1", input.ID, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("finish conflict: %v", err)
	}
	if _, err = service.Finish(context.Background(), "user-2", input.ID, finish); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner isolation: %v", err)
	}
}

func TestServiceLinksCodexThreadTurnsToOneConversation(t *testing.T) {
	service := newTestService(t)
	first := testStartInput("inv-source-1", "knowledge.list")
	first.Source = &externalcontext.Source{
		Provider: "codex", ThreadID: "codex-thread-1", TurnID: "codex-turn-1", ThreadSource: "user", Message: "第一轮用户消息",
	}
	started, err := service.StartLinked(context.Background(), "user-1", first)
	if err != nil || started.Source == nil {
		t.Fatalf("start linked: result=%+v err=%v", started, err)
	}
	if started.Source.ConversationID == "" || started.Source.ExternalRef == "" || started.Source.HistoryID == "" {
		t.Fatalf("incomplete source link: %+v", started.Source)
	}

	parallel := testStartInput("inv-source-2", "workflow.list")
	parallel.Source = first.Source
	second, err := service.StartLinked(context.Background(), "user-1", parallel)
	if err != nil || second.Source == nil || *second.Source != *started.Source {
		t.Fatalf("same turn did not reuse source link: first=%+v second=%+v err=%v", started.Source, second.Source, err)
	}
	if _, err := service.Finish(context.Background(), "user-1", first.ID, FinishInput{Status: StatusSucceeded}); err != nil {
		t.Fatal(err)
	}
	var run orm.ExternalChatRun
	if err := service.db.First(&run, "id = ?", started.Source.ExternalRef).Error; err != nil || run.Status != "running" {
		t.Fatalf("parallel turn completed early: run=%+v err=%v", run, err)
	}
	now := time.Now().UTC()
	if err := service.db.Create(&orm.WorkflowSession{
		ID: "legacy-session", WorkflowID: "workflow-1", Status: "completed", CreateUserID: "user-1",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finish(context.Background(), "user-1", parallel.ID, FinishInput{
		Status: StatusSucceeded, SessionID: "legacy-session",
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.db.First(&run, "id = ?", started.Source.ExternalRef).Error; err != nil || run.Status != "completed" {
		t.Fatalf("turn was not completed: run=%+v err=%v", run, err)
	}
	var legacy orm.WorkflowSession
	if err := service.db.First(&legacy, "id = ?", "legacy-session").Error; err != nil ||
		legacy.ConversationID != started.Source.ConversationID || legacy.OriginRef != started.Source.ExternalRef {
		t.Fatalf("legacy Workflow was not attached: session=%+v err=%v", legacy, err)
	}

	nextTurn := testStartInput("inv-source-3", "workflow.start")
	nextTurn.Source = &externalcontext.Source{Provider: "codex", ThreadID: "codex-thread-1", TurnID: "codex-turn-2", Message: "第二轮用户消息"}
	third, err := service.StartLinked(context.Background(), "user-1", nextTurn)
	if err != nil || third.Source == nil || third.Source.ConversationID != started.Source.ConversationID ||
		third.Source.ExternalRef == started.Source.ExternalRef || third.Source.HistoryID == started.Source.HistoryID {
		t.Fatalf("next turn mapping: first=%+v third=%+v err=%v", started.Source, third.Source, err)
	}
	var histories int64
	if err := service.db.Model(&orm.ChatHistory{}).
		Where("conversation_id = ?", started.Source.ConversationID).Count(&histories).Error; err != nil || histories != 2 {
		t.Fatalf("history count=%d err=%v", histories, err)
	}

	foreign := testStartInput("inv-source-foreign", "knowledge.list")
	foreign.Source = first.Source
	if _, err := service.StartLinked(context.Background(), "user-2", foreign); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign owner reused binding: %v", err)
	}
}

func TestServiceInterruptsOnlyRunningInvocationsForReclaimedRun(t *testing.T) {
	service := newTestService(t)
	for _, item := range []struct {
		id, owner, externalRef string
	}{{"inv-running", "user-1", "run-1"}, {"inv-other-run", "user-1", "run-2"}, {"inv-other-owner", "user-2", "run-1"}} {
		input := testStartInput(item.id, "workflow.state")
		input.ExternalRef = item.externalRef
		if _, _, err := service.Start(context.Background(), item.owner, input); err != nil {
			t.Fatalf("start %s: %v", item.id, err)
		}
	}
	terminalInput := testStartInput("inv-terminal", "knowledge.list")
	terminalInput.ExternalRef = "run-1"
	if _, _, err := service.Start(context.Background(), "user-1", terminalInput); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finish(context.Background(), "user-1", terminalInput.ID, FinishInput{Status: StatusSucceeded}); err != nil {
		t.Fatal(err)
	}

	finishedAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	count, err := service.InterruptRunningForExternalRef(context.Background(), "user-1", "run-1", finishedAt)
	if err != nil || count != 1 {
		t.Fatalf("interrupt count=%d err=%v", count, err)
	}
	var interrupted orm.AgentInvocation
	if err := service.db.First(&interrupted, "id = ?", "inv-running").Error; err != nil {
		t.Fatal(err)
	}
	if interrupted.Status != StatusInterrupted || interrupted.ErrorCode != "EXTERNAL_RUN_RECLAIMED" ||
		!interrupted.Retryable || interrupted.FinishedAt == nil || !interrupted.FinishedAt.Equal(finishedAt) {
		t.Fatalf("unexpected interrupted invocation: %#v", interrupted)
	}
	for _, id := range []string{"inv-other-run", "inv-other-owner"} {
		var record orm.AgentInvocation
		if err := service.db.First(&record, "id = ?", id).Error; err != nil || record.Status != StatusRunning {
			t.Fatalf("unrelated invocation %s changed: %#v err=%v", id, record, err)
		}
	}
	var terminal orm.AgentInvocation
	if err := service.db.First(&terminal, "id = ?", "inv-terminal").Error; err != nil || terminal.Status != StatusSucceeded {
		t.Fatalf("terminal invocation changed: %#v err=%v", terminal, err)
	}
}

func TestServiceListIsOwnerScopedFilteredAndCursorPaged(t *testing.T) {
	service := newTestService(t)
	start := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	for index, item := range []struct {
		owner, id, tool string
	}{{"user-1", "inv-1", "knowledge.list"}, {"user-1", "inv-2", "workflow.session.start"}, {"user-2", "inv-3", "knowledge.list"}} {
		if _, _, err := service.Start(context.Background(), item.owner, testStartInput(item.id, item.tool)); err != nil {
			t.Fatalf("start %s: %v", item.id, err)
		}
		if err := service.db.Model(&orm.AgentInvocation{}).Where("id = ?", item.id).
			Update("started_at", start.Add(time.Duration(index)*time.Minute)).Error; err != nil {
			t.Fatalf("set started_at: %v", err)
		}
	}

	page, err := service.List(context.Background(), "user-1", ListQuery{PageSize: 1})
	if err != nil || len(page.Invocations) != 1 || page.Invocations[0].ID != "inv-2" || page.NextPageToken == "" {
		t.Fatalf("first page: %+v err=%v", page, err)
	}
	next, err := service.List(context.Background(), "user-1", ListQuery{PageSize: 1, PageToken: page.NextPageToken})
	if err != nil || len(next.Invocations) != 1 || next.Invocations[0].ID != "inv-1" || next.NextPageToken != "" {
		t.Fatalf("next page: %+v err=%v", next, err)
	}
	filtered, err := service.List(context.Background(), "user-1", ListQuery{ToolName: "knowledge.list"})
	if err != nil || len(filtered.Invocations) != 1 || filtered.Invocations[0].ID != "inv-1" {
		t.Fatalf("filtered: %+v err=%v", filtered, err)
	}
}

func TestServiceRejectsInvalidEvidence(t *testing.T) {
	service := newTestService(t)
	invalid := testStartInput("inv-1", "knowledge.list")
	invalid.RequestHash = "not-a-sha256"
	if _, _, err := service.Start(context.Background(), "user-1", invalid); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid hash: %v", err)
	}
	invalid = testStartInput("inv-2", "knowledge.list")
	invalid.RequestSummary = json.RawMessage(`["not-an-object"]`)
	if _, _, err := service.Start(context.Background(), "user-1", invalid); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid summary: %v", err)
	}
	for _, summary := range []json.RawMessage{
		json.RawMessage(`{"prompt":"must not be persisted"}`),
		json.RawMessage(`{"items_count":"must be numeric"}`),
		json.RawMessage(`{"local_file_name":"/private/file.txt"}`),
	} {
		invalid = testStartInput("inv-private", "knowledge.list")
		invalid.RequestSummary = summary
		if _, _, err := service.Start(context.Background(), "user-1", invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unsafe summary %s: %v", summary, err)
		}
	}
	if _, err := service.List(context.Background(), "user-1", ListQuery{PageToken: "broken"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid cursor: %v", err)
	}
}

func TestHandlerRequiresOwnerAndReturnsCoreEnvelope(t *testing.T) {
	handler := Handler{Service: newTestService(t)}
	body := `{"client_name":"codex-cli","connector_name":"lazymind-mcp","connector_instance_id":"connector-1","transport":"stdio","tool_name":"knowledge.list","read_only":true,"request_hash":"` + testRequestHash + `"}`

	unauthorized := httptest.NewRecorder()
	handler.Start(unauthorized, invocationRequest(http.MethodPost, body, "", "inv-1"))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status: %d", unauthorized.Code)
	}

	created := httptest.NewRecorder()
	handler.Start(created, invocationRequest(http.MethodPost, body, "user-1", "inv-1"))
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"created":true`) {
		t.Fatalf("created response: status=%d body=%s", created.Code, created.Body.String())
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.AgentInvocation{}, &orm.ExternalAgentBinding{}, &orm.Conversation{},
		&orm.ExternalChatRun{}, &orm.ChatHistory{}, &orm.WorkflowSession{},
	); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return New(db)
}

func testStartInput(id, tool string) StartInput {
	return StartInput{
		ID: id, ClientName: "codex-cli", ClientVersion: "1.0.0", ConnectorName: "lazymind-mcp",
		ConnectorVersion: "dev", ConnectorInstanceID: "connector-1", ProtocolVersion: "2025-11-25",
		Transport: "stdio", ToolName: tool, ReadOnly: true, RequestHash: testRequestHash,
	}
}

func invocationRequest(method, body, owner, id string) *http.Request {
	request := httptest.NewRequest(method, "/agent-invocations/"+id+":start", bytes.NewBufferString(body))
	if owner != "" {
		request.Header.Set("X-User-Id", owner)
	}
	return mux.SetURLVars(request, map[string]string{"invocation_id": id})
}
