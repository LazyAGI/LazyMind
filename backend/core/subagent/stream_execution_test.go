package subagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// The gate places the execution change inside an actual Redis read, rather than
// relying on the polling interval to arrange the connection race.
type executionStreamStore struct {
	*mockStateStore
	mu     sync.Mutex
	events []string
	reads  int
	gates  map[int]*executionStreamGate
}

type executionStreamGate struct {
	entered   chan struct{}
	release   chan struct{}
	afterRead bool
}

func (s *executionStreamStore) RPush(_ context.Context, _ string, value []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, string(value))
	return nil
}

func (s *executionStreamStore) LRange(ctx context.Context, _ string, start, _ int64) ([]string, error) {
	s.mu.Lock()
	s.reads++
	gate := s.gates[s.reads]
	var captured []string
	if gate != nil && gate.afterRead && start < int64(len(s.events)) {
		captured = append([]string(nil), s.events[start:]...)
	}
	s.mu.Unlock()
	if gate != nil {
		close(gate.entered)
		select {
		case <-gate.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if gate != nil && gate.afterRead {
		return captured, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if start >= int64(len(s.events)) {
		return nil, nil
	}
	return append([]string(nil), s.events[start:]...), nil
}

type executionStreamFrame struct {
	TaskEvent
	Snapshot bool `json:"snapshot"`
}

type executionStreamRecorder struct {
	*httptest.ResponseRecorder
	frames chan executionStreamFrame
	offset int
}

func (w *executionStreamRecorder) Flush() {
	w.ResponseRecorder.Flush()
	for _, line := range strings.Split(w.Body.String()[w.offset:], "\n") {
		var frame executionStreamFrame
		if strings.HasPrefix(line, "data: ") && json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame) == nil {
			w.frames <- frame
		}
	}
	w.offset = w.Body.Len()
}

func TestTaskStreamFollowsExecutionChangedDuringRedisRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		blockAt int
		kind    string
	}{{"initial_replay", 1, "tool_results"}, {"live_tail", 2, "tool_results"}, {"initial_replay_calls", 1, "tool_calls"}, {"live_tail_calls", 2, "tool_calls"}} {
		t.Run(tc.name, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			task, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "early", ConversationID: "conv", AgentType: "research", CreateUserID: "0", Title: "early"})
			if err != nil {
				t.Fatal(err)
			}
			gate := &executionStreamGate{entered: make(chan struct{}), release: make(chan struct{})}
			cache := &executionStreamStore{mockStateStore: &mockStateStore{}, gates: map[int]*executionStreamGate{tc.blockAt: gate}}
			var replayGate *executionStreamGate
			if tc.blockAt == 2 {
				replayGate = &executionStreamGate{entered: make(chan struct{}), release: make(chan struct{})}
				cache.gates[3] = replayGate
			}
			store.Init(db, db, cache)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			ctx, cancel := context.WithCancel(t.Context())
			recorder := &executionStreamRecorder{ResponseRecorder: httptest.NewRecorder(), frames: make(chan executionStreamFrame, 128)}
			done := make(chan struct{})
			go func() {
				defer close(done)
				req := httptest.NewRequest(http.MethodGet, "/tasks/early:stream", nil).WithContext(ctx)
				StreamTask(recorder, mux.SetURLVars(req, map[string]string{"task_id": "early"}))
			}()
			defer func() { cancel(); <-done }()
			select {
			case <-gate.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("task stream did not reach the controlled Redis read")
			}
			if err := beginDisplayExecution(t.Context(), db, task.ID, "current"); err != nil {
				t.Fatal(err)
			}
			for i, row := range []orm.SubAgentStep{
				{Role: "text", Content: json.RawMessage(`{"content":"COMMITTED","model_output":{"run_id":"current","call_index":1}}`)},
				{Role: "model_runtime", Content: json.RawMessage(`{"model_output":{"run_id":"current","call_index":1,"phase":"commit"}}`)},
			} {
				row.ID, row.TaskID, row.ExecutionID, row.Seq = "early-"+row.Role, task.ID, "current", i
				if err := db.Create(&row).Error; err != nil {
					t.Fatal(err)
				}
			}
			appendEvent := func(event TaskEvent) {
				t.Helper()
				if err := AppendStreamEvent(t.Context(), cache, task.ID, event); err != nil {
					t.Fatal(err)
				}
			}
			persistTool := func(receipt string) TaskEvent {
				t.Helper()
				wire := TaskEvent{Type: tc.kind, TaskID: task.ID, ExecutionID: "current"}
				if tc.kind == "tool_calls" {
					wire.ToolCalls = json.RawMessage(`[{"id":"receipt","name":"save","args":{"receipt":"` + receipt + `"}}]`)
				} else {
					wire.ToolResults = json.RawMessage(`[{"id":"receipt","name":"save","result":"` + receipt + `"}]`)
				}
				accepted, err := routeExecutionEvent(t.Context(), db, task.ID, "current", wire, func(tx *gorm.DB, event TaskEvent) (bool, error) {
					wire = event
					return persistTaskEvent(t.Context(), tx, event)
				})
				if err != nil || !accepted {
					t.Fatalf("persist tool result: accepted=%v err=%v", accepted, err)
				}
				return wire
			}
			appendEvent(persistTool("SAVED-ONCE"))
			saved, err := SaveArtifactWithRecord(t.Context(), db, task.ID, "report", "text", json.RawMessage(`{"text":"DURABLE-ARTIFACT"}`), 1)
			if err != nil {
				t.Fatal(err)
			}
			appendEvent(TaskEvent{Type: "artifact", ExecutionID: "current", ArtifactKey: saved.Row.Slot, ContentType: saved.Row.ContentType, Seq: saved.Row.Seq, Value: saved.Row.Value})
			appendEvent(TaskEvent{Type: "task_start", ExecutionID: "current"})
			appendEvent(TaskEvent{Type: "model_output", ExecutionID: "current", ModelOutput: &common.ModelOutput{RunID: "current", CallIndex: 2, Phase: "begin"}})
			appendEvent(TaskEvent{Type: "text", ExecutionID: "current", Text: "CURRENT-PREVIEW", ModelOutput: &common.ModelOutput{RunID: "current", CallIndex: 2}})
			if replayGate != nil {
				close(gate.release)
				select {
				case <-replayGate.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("task stream did not reach the execution replay read")
				}
			}
			appendEvent(persistTool("IN-REPLAY"))
			delayed := persistTool("DELAYED-PUBLISH")
			if replayGate != nil {
				close(replayGate.release)
			} else {
				close(gate.release)
			}
			var observed []executionStreamFrame
			waitText := func(text string) {
				t.Helper()
				timer := time.NewTimer(3 * time.Second)
				defer timer.Stop()
				for {
					select {
					case frame := <-recorder.frames:
						observed = append(observed, frame)
						if frame.Text == text {
							return
						}
					case <-done:
						t.Fatalf("task stream closed before %s", text)
					case <-timer.C:
						t.Fatalf("task stream lost %s after execution changed during %s; frames=%+v", text, tc.name, observed)
					}
				}
			}
			waitText("CURRENT-PREVIEW")
			// Persistence precedes publication in the real producer. This result
			// may already be in the DB snapshot when its LIST entry arrives later.
			appendEvent(delayed)
			appendEvent(TaskEvent{Type: "task_start", ExecutionID: task.ExecutionID})
			appendEvent(TaskEvent{Type: "text", ExecutionID: task.ExecutionID, Text: "STALE"})
			appendEvent(TaskEvent{Type: "done", ExecutionID: task.ExecutionID, Status: StatusSucceeded})
			appendEvent(TaskEvent{Type: "text", ExecutionID: "current", Text: "STILL-CURRENT", ModelOutput: &common.ModelOutput{RunID: "current", CallIndex: 2}})
			waitText("STILL-CURRENT")
			var starts, committed, artifacts int
			receipts := map[string]int{}
			for _, frame := range observed {
				if frame.Type == "task_start" && frame.ExecutionID == "current" && frame.Snapshot {
					starts++
				}
				if frame.Text == "COMMITTED" {
					committed++
				}
				for _, receipt := range []string{"SAVED-ONCE", "IN-REPLAY", "DELAYED-PUBLISH"} {
					if strings.Contains(string(frame.ToolResults)+string(frame.ToolCalls), receipt) {
						receipts[receipt]++
					}
				}
				if frame.Type == "artifact" && strings.Contains(string(frame.Value), "DURABLE-ARTIFACT") {
					artifacts++
				}
				if frame.Text == "STALE" || frame.Type == "done" {
					t.Fatalf("late old execution escaped the filter: %+v", frame)
				}
			}
			if starts != 1 || committed != 1 || artifacts != 1 || receipts["SAVED-ONCE"] != 1 || receipts["IN-REPLAY"] != 1 || receipts["DELAYED-PUBLISH"] != 1 {
				t.Fatalf("execution refresh lost or duplicated durable effects: starts=%d committed=%d artifacts=%d receipts=%v", starts, committed, artifacts, receipts)
			}
		})
	}
}

func TestTaskStreamResumesWhileInitialTerminalReplayIsReading(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	task, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "resume", ConversationID: "conv", AgentType: "research", CreateUserID: "0", Title: "resume"})
	if err != nil {
		t.Fatal(err)
	}
	if accepted, err := AcceptFinalStatus(t.Context(), db, task.ID, StatusSucceeded, "OLD"); err != nil || !accepted {
		t.Fatalf("finish old execution: accepted=%v err=%v", accepted, err)
	}
	gate := &executionStreamGate{entered: make(chan struct{}), release: make(chan struct{})}
	cache := &executionStreamStore{mockStateStore: &mockStateStore{}, gates: map[int]*executionStreamGate{1: gate}}
	store.Init(db, db, cache)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	ctx, cancel := context.WithCancel(t.Context())
	recorder := &executionStreamRecorder{ResponseRecorder: httptest.NewRecorder(), frames: make(chan executionStreamFrame, 128)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/tasks/resume:stream", nil).WithContext(ctx)
		StreamTask(recorder, mux.SetURLVars(req, map[string]string{"task_id": task.ID}))
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("terminal replay did not reach Redis")
	}
	if err := UpdateStatus(t.Context(), db, task.ID, StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := beginDisplayExecution(t.Context(), db, task.ID, "current"); err != nil {
		t.Fatal(err)
	}
	appendEvent := func(event TaskEvent) {
		t.Helper()
		if err := AppendStreamEvent(t.Context(), cache, task.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(TaskEvent{Type: "model_output", ExecutionID: "current", ModelOutput: &common.ModelOutput{RunID: "current", CallIndex: 1, Phase: "begin"}})
	appendEvent(TaskEvent{Type: "text", ExecutionID: "current", Text: "CURRENT-PREVIEW", ModelOutput: &common.ModelOutput{RunID: "current", CallIndex: 1}})
	close(gate.release)
	waitFrame := func(want string) {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case frame := <-recorder.frames:
				if frame.ModelOutput != nil && frame.ModelOutput.RunID == "current" && frame.ModelOutput.Phase == "rollback" {
					t.Fatal("resumed running execution was rolled back by the old terminal snapshot")
				}
				if frame.Type == "done" || frame.Type == "error" {
					if frame.ExecutionID != "current" || want != "done" {
						t.Fatalf("old terminal snapshot closed the current execution: %+v", frame)
					}
				}
				if frame.Text == want || frame.Type == want {
					return
				}
			case <-timer.C:
				t.Fatalf("resumed stream did not emit %s", want)
			}
		}
	}
	waitFrame("CURRENT-PREVIEW")
	appendEvent(TaskEvent{Type: "model_output", ExecutionID: "current", ModelOutput: &common.ModelOutput{RunID: "current", CallIndex: 1, Phase: "commit"}})
	if accepted, err := AcceptFinalStatus(t.Context(), db, task.ID, StatusSucceeded, "CURRENT"); err != nil || !accepted {
		t.Fatalf("finish current execution: accepted=%v err=%v", accepted, err)
	}
	appendEvent(TaskEvent{Type: "done", ExecutionID: "current", Status: StatusSucceeded, Summary: "CURRENT"})
	waitFrame("done")
}

func TestTaskStreamCommitAfterRedisSliceBeforeTerminalRead(t *testing.T) {
	for _, blockAt := range []int{1, 2} {
		name := "initial_replay"
		if blockAt == 2 {
			name = "live_tail"
		}
		t.Run(name, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			task, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "terminal-race", ConversationID: "conv", AgentType: "research", CreateUserID: "0", Title: "terminal race"})
			if err != nil {
				t.Fatal(err)
			}
			gate := &executionStreamGate{entered: make(chan struct{}), release: make(chan struct{}), afterRead: true}
			cache := &executionStreamStore{mockStateStore: &mockStateStore{}, gates: map[int]*executionStreamGate{blockAt: gate}}
			store.Init(db, db, cache)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			request := &RunRequest{TaskID: task.ID}
			call := &taskModelCall{cursor: common.ModelOutput{RunID: task.ExecutionID, Phase: "commit"}}
			publish := func(event TaskEvent) {
				t.Helper()
				event.TaskID, event.ExecutionID = task.ID, task.ExecutionID
				accepted, err := routeExecutionEvent(t.Context(), db, task.ID, task.ExecutionID, event, func(tx *gorm.DB, event TaskEvent) (bool, error) {
					return persistTaskModelEvent(t.Context(), tx, request, call, &event)
				})
				if err != nil || !accepted {
					t.Fatalf("persist %s: accepted=%v err=%v", event.Type, accepted, err)
				}
				if err := AppendStreamEvent(t.Context(), cache, task.ID, event); err != nil {
					t.Fatal(err)
				}
			}
			boundary := func(index int, phase string) TaskEvent {
				return TaskEvent{Type: "model_output", ModelOutput: &common.ModelOutput{RunID: task.ExecutionID, CallIndex: index, Phase: phase}}
			}
			publish(boundary(1, "begin"))
			publish(TaskEvent{Type: "text", Text: "PREVIOUS-COMMITTED", ModelOutput: &common.ModelOutput{RunID: task.ExecutionID, CallIndex: 1}})
			publish(boundary(1, "commit"))
			publish(TaskEvent{Type: "tool_results", ToolResults: json.RawMessage(`[{"id":"saved-once","name":"save","result":"SAVED-ONCE"}]`)})
			if err := SaveArtifact(t.Context(), db, task.ID, "report", "text", json.RawMessage(`{"text":"SAVED-ARTIFACT"}`), 1); err != nil {
				t.Fatal(err)
			}
			publish(boundary(2, "begin"))
			publish(TaskEvent{Type: "think", Think: "COMMITTED-THINK", ModelOutput: &common.ModelOutput{RunID: task.ExecutionID, CallIndex: 2}})
			publish(TaskEvent{Type: "text", Text: "NEWLY-COMMITTED", ModelOutput: &common.ModelOutput{RunID: task.ExecutionID, CallIndex: 2}})
			ctx, cancel := context.WithCancel(t.Context())
			recorder := &executionStreamRecorder{ResponseRecorder: httptest.NewRecorder(), frames: make(chan executionStreamFrame, 128)}
			done := make(chan struct{})
			go func() {
				defer close(done)
				req := httptest.NewRequest(http.MethodGet, "/tasks/terminal-race:stream", nil).WithContext(ctx)
				StreamTask(recorder, mux.SetURLVars(req, map[string]string{"task_id": task.ID}))
			}()
			defer func() { cancel(); <-done }()
			select {
			case <-gate.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("stream did not capture the controlled LIST slice")
			}
			// The LIST response has already been copied. Commit is durable before
			// terminal state, but its cache frame is outside that response.
			publish(boundary(2, "commit"))
			if accepted, err := AcceptFinalStatus(t.Context(), db, task.ID, StatusSucceeded, "FINISHED"); err != nil || !accepted {
				t.Fatalf("finish execution: accepted=%v err=%v", accepted, err)
			}
			close(gate.release)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal stream did not close")
			}
			var finalText, finalThink string
			var finalTools, artifacts, terminals int
			var cursor *common.ModelOutput
			for len(recorder.frames) > 0 {
				frame := <-recorder.frames
				if frame.Type == "task_start" && frame.Snapshot {
					finalText, finalThink, finalTools, cursor = "", "", 0, nil
				}
				finalText += frame.Text
				finalThink += frame.Think
				if strings.Contains(string(frame.ToolResults), "SAVED-ONCE") {
					finalTools++
				}
				if frame.Type == "artifact" {
					artifacts++
				}
				if frame.ModelOutput != nil {
					cursor = frame.ModelOutput
					if cursor.CallIndex == 2 && cursor.Phase == "rollback" {
						t.Error("durably committed call 2 was rolled back by a stale LIST slice")
					}
				}
				if frame.Type == "done" && frame.ExecutionID == task.ExecutionID && frame.Status == StatusSucceeded {
					terminals++
				}
			}
			if finalText != "PREVIOUS-COMMITTEDNEWLY-COMMITTED" || finalThink != "COMMITTED-THINK" || finalTools != 1 || artifacts != 1 || terminals != 1 || cursor == nil || cursor.CallIndex != 2 || cursor.Phase != "commit" {
				t.Fatalf("terminal projection lost committed effects: text=%q think=%q tools=%d artifacts=%d terminals=%d cursor=%+v", finalText, finalThink, finalTools, artifacts, terminals, cursor)
			}
			var toolRows int64
			if err := db.Model(&orm.SubAgentStep{}).Where("task_id = ? AND role = ?", task.ID, "tool").Count(&toolRows).Error; err != nil || toolRows != 1 {
				t.Fatalf("stream changed durable tool count: rows=%d err=%v", toolRows, err)
			}
		})
	}
}

func TestTaskStreamReplayDoesNotResurrectHiddenArtifact(t *testing.T) {
	for _, tc := range []struct {
		agentType         string
		previousExecution bool
	}{{"workflow_step", false}, {"research", false}, {"workflow_step", true}, {"research", true}} {
		name := tc.agentType
		if tc.previousExecution {
			name += "_previous_execution"
		}
		t.Run(name, func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			task, err := CreateTask(t.Context(), db, CreateTaskInput{TaskID: "hidden-artifact", ConversationID: "conv", AgentType: tc.agentType, CreateUserID: "0", Title: "hidden artifact"})
			if err != nil {
				t.Fatal(err)
			}
			cache := &executionStreamStore{mockStateStore: &mockStateStore{}}
			hiddenExecution := task.ExecutionID
			save := func(seq int, text string) {
				t.Helper()
				saved, err := SaveArtifactWithRecord(t.Context(), db, task.ID, "report", "text", json.RawMessage(`{"text":"`+text+`"}`), seq)
				if err != nil {
					t.Fatal(err)
				}
				if err := AppendStreamEvent(t.Context(), cache, task.ID, TaskEvent{Type: "artifact", ExecutionID: task.ExecutionID, ArtifactKey: saved.Row.Slot, ContentType: saved.Row.ContentType, Seq: saved.Row.Seq, Value: saved.Row.Value}); err != nil {
					t.Fatal(err)
				}
			}
			save(1, "HIDDEN")
			// HideSlotItem's authoritative mutation is an exact task/slot/seq
			// Hidden update. Its old LIST copy intentionally remains within TTL.
			if err := db.Model(&orm.SubAgentArtifact{}).Where("task_id = ? AND slot = ? AND seq = ?", task.ID, "report", 1).Update("hidden", true).Error; err != nil {
				t.Fatal(err)
			}
			visibleSeq := 2
			if tc.previousExecution {
				if err := beginDisplayExecution(t.Context(), db, task.ID, "next"); err != nil {
					t.Fatal(err)
				}
				task.ExecutionID, visibleSeq = "next", 1
			}
			save(visibleSeq, "VISIBLE")
			if accepted, err := AcceptFinalStatus(t.Context(), db, task.ID, StatusSucceeded, "FINISHED"); err != nil || !accepted {
				t.Fatalf("finish execution: accepted=%v err=%v", accepted, err)
			}
			store.Init(db, db, cache)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			for i := 0; i < 2; i++ {
				recorder := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/tasks/hidden-artifact:stream", nil)
				StreamTask(recorder, mux.SetURLVars(req, map[string]string{"task_id": task.ID}))
				body := recorder.Body.String()
				if strings.Contains(body, "HIDDEN") || strings.Count(body, `"type":"artifact"`) != 1 || !strings.Contains(body, "VISIBLE") || !strings.Contains(body, "[DONE]") {
					t.Fatalf("reconnect %d resurrected hidden artifact or lost visible revision: %s", i, body)
				}
			}
			// A duplicate hidden frame can also arrive after the initial replay.
			// Keep the task running until the tail has consumed that cached copy.
			if err := UpdateStatus(t.Context(), db, task.ID, StatusRunning); err != nil {
				t.Fatal(err)
			}
			gate := &executionStreamGate{entered: make(chan struct{}), release: make(chan struct{})}
			afterTail := &executionStreamGate{entered: make(chan struct{}), release: make(chan struct{})}
			cache.reads, cache.gates = 0, map[int]*executionStreamGate{2: gate, 3: afterTail}
			ctx, cancel := context.WithCancel(t.Context())
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				req := httptest.NewRequest(http.MethodGet, "/tasks/hidden-artifact:stream", nil).WithContext(ctx)
				StreamTask(recorder, mux.SetURLVars(req, map[string]string{"task_id": task.ID}))
			}()
			defer func() { cancel(); <-done }()
			waitRead := func(gate *executionStreamGate) {
				t.Helper()
				select {
				case <-gate.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("stream did not reach controlled tail read")
				}
			}
			waitRead(gate)
			if err := AppendStreamEvent(t.Context(), cache, task.ID, TaskEvent{Type: "artifact", ExecutionID: hiddenExecution, ArtifactKey: "report", ContentType: "text", Seq: 1, Value: json.RawMessage(`{"text":"HIDDEN"}`)}); err != nil {
				t.Fatal(err)
			}
			close(gate.release)
			waitRead(afterTail)
			cancel()
			<-done
			body := recorder.Body.String()
			if strings.Contains(body, "HIDDEN") || strings.Count(body, `"type":"artifact"`) != 1 || !strings.Contains(body, "VISIBLE") {
				t.Fatalf("late cached copy resurrected hidden artifact: %s", body)
			}
		})
	}
}
