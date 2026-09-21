package hosted

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
)

// Both HTTP adapters must implement the same publication and completion contract.
func TestNativeAndExternalProgressivePublication(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprintf("native=%v", native), func(t *testing.T) {
			s, db, grant := controlledService(t)
			t.Setenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN", "test-token")
			if native {
				if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("executor_host", "lazymind").Error; err != nil {
					t.Fatal(err)
				}
			}
			manifest := []byte("slots:\n  - id: report\n    type: text\n    cardinality: list\n")
			if err := db.Create(&orm.WorkflowBlob{Hash: "list-manifest", Content: manifest, Size: int64(len(manifest))}).Error; err != nil {
				t.Fatal(err)
			}
			hash := "list-manifest"
			if err := db.Create(&orm.WorkflowRevisionEntry{RevisionID: "revision-1", Path: "workflow.yaml", EntryType: "file", BlobHash: &hash}).Error; err != nil {
				t.Fatal(err)
			}
			// A material-only dependent step must not consume an intermediate output.
			var revision orm.WorkflowRevision
			if err := db.First(&revision, "id = ?", "revision-1").Error; err != nil {
				t.Fatal(err)
			}
			var graph graphengine.CompiledStateGraph
			if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
				t.Fatal(err)
			}
			graph.Nodes["consume"] = graphengine.CompiledNode{ID: "consume", Route: "all", Input: &graphengine.Expression{Material: "report"}}
			graph.InputExpressions["consume"] = graphengine.Expression{Material: "report"}
			graph.ControlEdges = append(graph.ControlEdges, graphengine.CompiledEdge{ID: "start-consume", From: "__start__", To: "consume"})
			if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
				t.Fatal(err)
			}
			corestore.Init(db, nil, nil)
			t.Cleanup(func() { corestore.Init(nil, nil, nil) })
			remote := executor.RemoteHandler{DB: db, Attempts: s.Attempts, Contexts: s.Contexts, Artifacts: s.Artifacts,
				Finish: func(ctx context.Context, owner, session, id string, input executor.Completion) error {
					_, err := s.Completion.Complete(ctx, owner, session, id, input)
					return err
				}}
			public := Handler{Service: s}
			invoke := func(handler http.HandlerFunc, body any) *httptest.ResponseRecorder {
				data, _ := json.Marshal(body)
				r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(data))
				r = mux.SetURLVars(r, map[string]string{"session_id": "session-1", "attempt_id": "attempt-1"})
				r.Header.Set("X-User-Id", "owner")
				r.Header.Set("Authorization", "Bearer test-token")
				r.Header.Set("X-Workflow-Lease-Token", grant.ExecutionHandle)
				rec := httptest.NewRecorder()
				handler(rec, r)
				return rec
			}
			publish := func(seq int, text string) *httptest.ResponseRecorder {
				body := map[string]any{"slot": "report", "seq": seq, "content_type": "text", "value": map[string]any{"text": text}, "execution_handle": grant.ExecutionHandle}
				if native {
					return invoke(remote.SaveArtifact, body)
				}
				delete(body, "execution_handle")
				return invoke(public.Publish, map[string]any{"artifact": body, "execution_handle": grant.ExecutionHandle})
			}
			finish := func() *httptest.ResponseRecorder {
				if native {
					return invoke(remote.Complete, map[string]any{"result": map[string]any{"summary": "done"}})
				}
				return invoke(public.Complete, executor.Completion{ExecutionHandle: grant.ExecutionHandle, Outcome: "succeeded", Summary: "done"})
			}
			if rec := finish(); rec.Code == 200 {
				t.Fatal("missing outputs accepted")
			}
			for seq := 1; seq <= 40; seq++ {
				if rec := publish(seq, fmt.Sprint(seq)); rec.Code != 200 {
					t.Fatalf("publish %d: %d %s", seq, rec.Code, rec.Body.String())
				}
				var count int64
				if err := db.Model(&orm.WorkflowSlotRevision{}).Count(&count).Error; err != nil || count != int64(seq) {
					t.Fatalf("not durable before completion: %d %v", count, err)
				}
			}
			var selected int64
			db.Model(&orm.WorkflowSlotRevision{}).Where("selected = ?", true).Count(&selected)
			if selected != 40 {
				t.Fatalf("list outputs not all visible: %d", selected)
			}
			if rec := publish(40, "40"); rec.Code != 200 {
				t.Fatal(rec.Body.String())
			}
			if rec := publish(40, "different"); rec.Code == 200 {
				t.Fatal("conflicting duplicate accepted")
			}
			var count int64
			db.Model(&orm.WorkflowSlotRevision{}).Count(&count)
			if count != 40 {
				t.Fatalf("duplicate revisions: %d", count)
			}
			var row orm.WorkflowSessionStep
			db.First(&row, "id = ?", "attempt-1")
			if row.Status != "claimed" {
				t.Fatalf("publication ended execution: %s", row.Status)
			}
			db.Model(&orm.WorkflowReviewCheckpoint{}).Count(&count)
			if count != 0 {
				t.Fatal("publication created review")
			}
			checkBlocked := func() {
				r := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/projection", nil), map[string]string{"session_id": "session-1"})
				rec := httptest.NewRecorder()
				workflowcore.GetSessionProjection(rec, r)
				if rec.Code != 200 {
					t.Fatal(rec.Body.String())
				}
				var response struct {
					Data struct {
						Ready []string `json:"ready"`
					} `json:"data"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				for _, id := range response.Data.Ready {
					if id == "consume" {
						t.Fatal("dependent step became ready before completion/review")
					}
				}
			}
			checkBlocked()
			for i := 0; i < 2; i++ {
				if rec := finish(); rec.Code != 200 {
					t.Fatalf("complete: %d %s", rec.Code, rec.Body.String())
				}
			}
			db.Model(&orm.WorkflowReviewCheckpoint{}).Count(&count)
			if count != 1 {
				t.Fatalf("review count %d", count)
			}
			checkBlocked()
			if rec := publish(41, "late"); rec.Code == 200 {
				t.Fatal("terminal attempt accepted new output")
			}
		})
	}
}

func TestPartialOutputsSurviveFailureAndStopFencesPublication(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, outcome := range []string{"failed", "cancelled", "stopped"} {
			t.Run(fmt.Sprintf("native=%v/%s", native, outcome), func(t *testing.T) {
				s, db, grant := controlledService(t)
				if err := db.AutoMigrate(&orm.SubAgentTask{}); err != nil {
					t.Fatal(err)
				}
				if native {
					db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("executor_host", "lazymind")
				}
				contract, err := s.Contexts.LoadAttemptContext(context.Background(), "attempt-1")
				if err != nil {
					t.Fatal(err)
				}
				contract.ExecutionHandle = grant.ExecutionHandle
				artifact := executor.Artifact{Slot: "report", Seq: 1, ContentType: "text", Value: json.RawMessage(`{"text":"partial"}`)}
				publish := func(a executor.Artifact) error {
					if native {
						return s.Artifacts.Save(context.Background(), contract, a)
					}
					return s.Publish(context.Background(), "owner", "session-1", "attempt-1", Publication{ExecutionHandle: grant.ExecutionHandle, Artifact: a})
				}
				if err := publish(artifact); err != nil {
					t.Fatal(err)
				}
				if outcome == "stopped" {
					if _, err := (workflowcore.WorkflowControlService{DB: db}).Execute(context.Background(), "owner", "session-1", workflowcore.WorkflowControlCommand{CommandID: "stop", Kind: "stop"}); err != nil {
						t.Fatal(err)
					}
				} else {
					input := executor.Completion{ExecutionHandle: grant.ExecutionHandle, Outcome: outcome, Summary: "interrupted"}
					if native {
						remote := executor.RemoteHandler{DB: db, Attempts: s.Attempts, Finish: func(ctx context.Context, owner, session, id string, completion executor.Completion) error {
							_, e := s.Completion.Complete(ctx, owner, session, id, completion)
							return e
						}}
						t.Setenv("LAZYMIND_WORKFLOW_EXECUTOR_TOKEN", "test-token")
						terminalResult := executor.Result{Summary: "interrupted"}
						if outcome == "failed" {
							terminalResult.Summary = "MEDIA_CAPABILITY_DEPENDENCY_MISSING {}"
							terminalResult.PostStepCheckpoint = &executor.PostStepCheckpoint{
								WorkflowRevision: contract.WorkflowRevision, Result: executor.Result{Summary: "analysis complete",
									Artifacts: []executor.Artifact{artifact}, Control: &executor.Control{NextStep: "next"}},
							}
						}
						resultBody, _ := json.Marshal(terminalResult)
						var workerResult map[string]any
						_ = json.Unmarshal(resultBody, &workerResult)
						// Match the unmodified Python worker's failure envelope.
						if outcome == "failed" {
							workerResult["error"] = terminalResult.Summary
							delete(workerResult, "summary")
						}
						raw, _ := json.Marshal(map[string]any{"result": workerResult})
						req := mux.SetURLVars(httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)), map[string]string{"attempt_id": "attempt-1"})
						req.Header.Set("Authorization", "Bearer test-token")
						req.Header.Set("X-Workflow-Lease-Token", grant.ExecutionHandle)
						rec := httptest.NewRecorder()
						if outcome == "failed" {
							remote.Fail(rec, req)
						} else {
							remote.Cancel(rec, req)
						}
						if rec.Code != 200 {
							t.Fatalf("native terminal: %s", rec.Body.String())
						}
						if outcome == "failed" {
							var row orm.WorkflowSessionStep
							if err := db.First(&row, "id = ?", "attempt-1").Error; err != nil {
								t.Fatal(err)
							}
							var saved executor.Result
							if err := json.Unmarshal([]byte(row.ResultJSON), &saved); err != nil {
								t.Fatal(err)
							}
							expected, _ := json.Marshal(terminalResult.PostStepCheckpoint)
							actual, _ := json.Marshal(saved.PostStepCheckpoint)
							if !bytes.Equal(expected, actual) {
								t.Fatalf("checkpoint lost through completion: %s", row.ResultJSON)
							}
						}

					} else {
						_, err = s.Complete(context.Background(), "owner", "session-1", "attempt-1", input)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				artifact.Seq = 2
				if err := publish(artifact); err == nil {
					t.Fatal("terminal or stopped execution accepted output")
				}
				var count int64
				db.Model(&orm.WorkflowSlotRevision{}).Where("selected = ?", true).Count(&count)
				if count != 1 {
					t.Fatalf("partial output lost or late write saved: %d", count)
				}
				db.Model(&orm.WorkflowReviewCheckpoint{}).Count(&count)
				if count != 0 {
					t.Fatal("unsuccessful execution created review")
				}
			})
		}
	}
}

func TestPublishedFileReplayIsIdempotentAndConflictsKeepOriginal(t *testing.T) {
	s, db, grant := controlledService(t)
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	publish := func(content string) error {
		value, _ := json.Marshal(map[string]any{"storage": "inline_base64", "name": "report.txt", "content_base64": base64.StdEncoding.EncodeToString([]byte(content))})
		return s.Publish(context.Background(), "owner", "session-1", "attempt-1", Publication{ExecutionHandle: grant.ExecutionHandle, Artifact: executor.Artifact{Slot: "report", Seq: 1, ContentType: "file", Value: value}})
	}
	for i := 0; i < 2; i++ {
		if err := publish("original"); err != nil {
			t.Fatal(err)
		}
	}
	if err := publish("changed"); err == nil {
		t.Fatal("conflicting file replay accepted")
	}
	var rows []orm.WorkflowHumanArtifact
	if err := db.Find(&rows).Error; err != nil || len(rows) != 1 {
		t.Fatalf("duplicate file rows: %d %v", len(rows), err)
	}
	var metadata struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rows[0].Value, &metadata); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(metadata.Path)
	if err != nil || string(content) != "original" {
		t.Fatalf("original file altered: %q %v", content, err)
	}
}
