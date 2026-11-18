package hosted

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"github.com/gorilla/mux"
	workflowstream "lazymind/core/workflow/stream"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
	workflowstore "lazymind/core/workflow/store"
)

func paperPreviewService(t *testing.T) (*Service, Execution) {
	t.Helper()
	service, db := prepareControlledService(t)
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("executor_host", HostName).Error; err != nil {
		t.Fatal(err)
	}
	var revision orm.WorkflowRevision
	if err := db.First(&revision, "id = ?", "revision-1").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["write"]
	node.Execution = &graphengine.StepExecution{Version: "1", OutputValidator: graphengine.PaperAgentOutputValidator}
	node.Outputs, node.RequiredOutputs = []string{"write_document"}, []string{"write_document"}
	graph.Nodes["write"] = node
	if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Update("plugin_id", "paper-writing").Error; err != nil {
		t.Fatal(err)
	}
	execution, err := service.Begin(t.Context(), "owner", "session-1", "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	return service, execution
}

func TestPaperPreviewReplayDoesNotPublishOrComplete(t *testing.T) {
	service, execution := paperPreviewService(t)
	input := PreviewInput{ExecutionHandle: execution.ExecutionHandle, Slot: "write_document", Seq: 1, Text: "# Draft", State: "streaming"}
	for range 2 {
		if err := service.Preview(t.Context(), "owner", "session-1", "attempt-1", input); err != nil {
			t.Fatal(err)
		}
	}
	input.Seq, input.Text = 2, "# Draft\n\nWritten while the worker is running."
	if err := service.Preview(t.Context(), "owner", "session-1", "attempt-1", input); err != nil {
		t.Fatal(err)
	}
	previews, err := service.Store.Replay(t.Context(), "session-1", "owner", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range previews {
		if event.EventType == "artifact.preview" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("idempotent preview event count = %d", count)
	}
	current, err := workflowstore.LatestArtifactPreview(t.Context(), service.DB, "session-1", "attempt-1")
	if err != nil || current.Content != input.Text || current.Seq != 2 {
		t.Fatalf("replay = %+v, %v", current, err)
	}
	var slots, reviews int64
	service.DB.Model(&orm.WorkflowSlotRevision{}).Count(&slots)
	service.DB.Model(&orm.WorkflowReviewCheckpoint{}).Count(&reviews)
	row, err := service.Attempts.Attempt(t.Context(), "attempt-1")
	if err != nil || slots != 0 || reviews != 0 || (row.Status != "claimed" && row.Status != "running") {
		t.Fatalf("preview published or completed: slots=%d reviews=%d attempt=%+v err=%v", slots, reviews, row, err)
	}
	input.Seq = 1
	if err := service.Preview(t.Context(), "owner", "session-1", "attempt-1", input); err == nil {
		t.Fatal("accepted out-of-order preview")
	}
	input.Seq, input.State = 3, "aborted"
	if err := service.Preview(t.Context(), "owner", "session-1", "attempt-1", input); err != nil {
		t.Fatal(err)
	}
	current, err = workflowstore.LatestArtifactPreview(t.Context(), service.DB, "session-1", "attempt-1")
	if err != nil || current.Content != "" || current.State != "aborted" {
		t.Fatalf("abort = %+v, %v", current, err)
	}
}

func TestPaperPreviewRejectsWrongOwnerSlotHostAndRetiredLease(t *testing.T) {
	for _, fault := range []string{"owner", "slot", "native", "ppt", "stopped", "failed", "lease", "resumed"} {
		t.Run(fault, func(t *testing.T) {
			service, execution := paperPreviewService(t)
			input := PreviewInput{ExecutionHandle: execution.ExecutionHandle, Slot: "write_document", Seq: 1, Text: "public text", State: "streaming"}
			owner := "owner"
			var err error
			switch fault {
			case "owner":
				owner = "other"
			case "slot":
				input.Slot = "private_pool"
			case "native":
				err = service.DB.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("executor_host", "lazymind").Error
			case "ppt":
				err = service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Update("plugin_id", "ppt-workflow").Error
			case "stopped":
				err = service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Update("status", "stopped").Error
			case "failed":
				err = service.DB.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("status", "failed").Error
			case "lease":
				input.ExecutionHandle = "wrong"
			case "resumed":
				_, err = service.Resume(t.Context(), "owner", "session-1", "attempt-1")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Preview(t.Context(), owner, "session-1", "attempt-1", input); err == nil {
				t.Fatal("accepted invalid preview")
			}
			var count int64
			if err := service.DB.Model(&orm.WorkflowEvent{}).Where("event_type = ?", "artifact.preview").Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("unauthorized event count=%d err=%v", count, err)
			}
		})
	}
}

func TestPaperPreviewArrivesOnWorkflowSSEBeforePublication(t *testing.T) {
	service, execution := paperPreviewService(t)
	router := mux.NewRouter()
	router.HandleFunc("/sessions/{session_id}/{attempt_id}/preview", (Handler{Service: service}).Preview).Methods("POST")
	router.Handle("/sessions/{session_id}/events", workflowstream.Handler{Store: service.Store, PollInterval: time.Millisecond,
		Snapshot: func(_ *http.Request, _, _ string) (any, error) { return map[string]any{"state_version": 1}, nil }})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/sessions/session-1/events", nil)
	request.Header.Set("X-User-Id", "owner")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	input := PreviewInput{ExecutionHandle: execution.ExecutionHandle, Slot: "write_document", Seq: 1, Text: "# First section", State: "streaming"}
	payload, _ := json.Marshal(input)
	update, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/sessions/session-1/attempt-1/preview", bytes.NewReader(payload))
	update.Header.Set("X-User-Id", "owner")
	result, err := server.Client().Do(update)
	if err != nil {
		t.Fatal(err)
	}
	result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("preview HTTP status %d", result.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	received := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && strings.Contains(line, `"type":"artifact.preview"`) {
			if !strings.Contains(line, `"content":"# First section"`) {
				t.Fatalf("wrong SSE payload: %s", line)
			}
			received = true
			break
		}
	}
	if !received {
		t.Fatalf("no live preview before publication: %v", scanner.Err())
	}
	var count int64
	if err := service.DB.Model(&orm.WorkflowSlotRevision{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("preview persisted an artifact: %d, %v", count, err)
	}
}
