package hosted

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/controlpolicy"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/execution"
	"lazymind/core/workflow/executor"
)

func controlledService(t *testing.T) (*Service, *gorm.DB, Execution) {
	t.Helper()
	service, db := hostedTestService(t)
	service.Completion = &execution.Service{DB: db, Store: service.Store, Attempts: service.Attempts, Contexts: service.Contexts}
	if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowApprovalPreference{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Updates(map[string]any{
		"control_protocol": controlpolicy.Protocol, "control_binding_json": `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("review_required", true).Error; err != nil {
		t.Fatal(err)
	}
	execution, err := service.Begin(context.Background(), "owner", "session-1", "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	return service, db, execution
}

func successfulSubmission(handle string) testCompletion {
	return testCompletion{Outcome: "succeeded", ExecutionHandle: handle,
		Artifacts: []executor.Artifact{{Slot: "report", ContentType: "text/plain", Seq: 1, Value: json.RawMessage(`{"text":"review me"}`)}}}
}

func TestControlSnapshotSharesNativeDocumentCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/document:inspect" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_document":true,"representation":"markdown","schema":"text/markdown","features":{"headings":true,"numbering":false,"cross_references":false,"provider_binding":false}}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	service, db, execution := controlledService(t)
	input := successfulSubmission(execution.ExecutionHandle)
	input.Artifacts[0].ContentType = "json"
	input.Artifacts[0].Value = json.RawMessage(`{"schema":"text/markdown","data":"# Review me"}`)
	if _, err := publishAndComplete(service, context.Background(), "owner", "session-1", "attempt-1", input); err != nil {
		t.Fatal(err)
	}
	oldDB, oldState := corestore.DB(), corestore.State()
	corestore.Init(db, db, nil)
	t.Cleanup(func() { corestore.Init(oldDB, oldDB, oldState) })
	read := func(handler http.HandlerFunc) []map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/workflow-sessions/session-1", nil)
		r = mux.SetURLVars(r, map[string]string{"session_id": "session-1"})
		r.Header.Set("X-User-Id", "owner")
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("snapshot: %d %s", w.Code, w.Body.String())
		}
		var result struct {
			Data struct {
				Session struct {
					Slots []map[string]any `json:"slots"`
				} `json:"session"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data.Session.Slots
	}
	native := read(workflowcore.GetSessionDetail)
	external := read((workflowcore.WorkflowControlHandler{Service: workflowcore.WorkflowControlService{DB: db}}).Read)
	if len(native) != 1 || len(external) != 1 {
		t.Fatalf("slots: native=%v external=%v", native, external)
	}
	if native[0]["document"] == nil {
		t.Fatalf("native fixture has no document descriptor: %v", native)
	}
	for _, field := range []string{"artifact_id", "document", "document_error", "revision_count", "artifact_value"} {
		if !reflect.DeepEqual(native[0][field], external[0][field]) {
			t.Errorf("%s differs: native=%v external=%v", field, native[0][field], external[0][field])
		}
	}
}

func TestControlledHumanSubmitConfirmAndFreshReplay(t *testing.T) {
	service, db, execution := controlledService(t)
	ctx := context.Background()
	input := successfulSubmission(execution.ExecutionHandle)
	result, err := publishAndComplete(service, ctx, "owner", "session-1", "attempt-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Control.Continuation != "awaiting_user" || result.Control.Admission.CanBegin || len(result.Control.Reviews) != 1 {
		t.Fatalf("human result is not guarded: %+v", result.Control)
	}
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", "session-1").Error; err != nil {
		t.Fatal(err)
	}
	if err := controlstore.GuardBegin(db, session); err == nil {
		t.Fatal("direct Core admission bypassed the review")
	}
	review := result.Control.Reviews[0]
	controller := workflowcore.WorkflowControlService{DB: db}
	command := workflowcore.WorkflowControlCommand{CommandID: "confirm-1", Kind: "confirm",
		ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash}
	confirmed, err := controller.Execute(ctx, "owner", session.ID, command)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Control.Continuation != "completed" || confirmed.Control.Reviews[0].Status != "accepted" {
		t.Fatalf("last step did not complete after confirmation: %+v", confirmed.Control)
	}
	replay, err := publishAndComplete(service, ctx, "owner", session.ID, "attempt-1", input)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.AlreadyTerminal || replay.Control.Continuation != "completed" || replay.Receipt.CommandID != result.Receipt.CommandID {
		t.Fatalf("historical receipt restored stale control: %+v", replay)
	}
	input.Summary = "different submission"
	if _, err := publishAndComplete(service, ctx, "owner", session.ID, "attempt-1", input); err == nil {
		t.Fatal("conflicting terminal replay was accepted")
	}
	command.ManifestHash = "different content"
	if _, err := controller.Execute(ctx, "owner", session.ID, command); err == nil {
		t.Fatal("command id accepted different confirmation content")
	}
}

func TestControlledStaleHandleCannotPublishArtifacts(t *testing.T) {
	service, db, previous := controlledService(t)
	current, err := service.Resume(context.Background(), "owner", "session-1", "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if previous.ExecutionHandle == current.ExecutionHandle {
		t.Fatal("resume did not rotate the execution handle")
	}
	_, err = publishAndComplete(service, context.Background(), "owner", "session-1", "attempt-1", successfulSubmission(previous.ExecutionHandle))
	var rejected *controlstore.Error
	if !errors.As(err, &rejected) || rejected.Code != "EXECUTION_FENCED" {
		t.Fatalf("stale handle: %v", err)
	}
	var count int64
	db.Model(&orm.WorkflowSlotRevision{}).Count(&count)
	if count != 0 {
		t.Fatal("a fenced worker polluted the selected artifacts")
	}
}

func TestControlledCompletionPreservesPublishedArtifactsOnReviewFailure(t *testing.T) {
	service, db, execution := controlledService(t)
	if err := db.Callback().Create().Before("gorm:create").Register("test:reject-review", func(tx *gorm.DB) {
		if tx.Statement.Table == "workflow_review_checkpoints" {
			tx.AddError(errors.New("review storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("test:reject-review")
	if _, err := publishAndComplete(service, context.Background(), "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle)); err == nil {
		t.Fatal("expected injected storage failure")
	}
	var attempt orm.WorkflowSessionStep
	db.First(&attempt, "id = ?", "attempt-1")
	if attempt.Status != "claimed" || attempt.SubmissionHash != "" {
		t.Fatalf("partial terminal commit: %+v", attempt)
	}
	var published int64
	if err := db.Model(&orm.WorkflowSlotRevision{}).Count(&published).Error; err != nil || published != 1 {
		t.Fatalf("published result lost after failed completion: %d %v", published, err)
	}
	for _, model := range []any{&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowCommand{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial write to %T: %d", model, count)
		}
	}
}

func TestConfirmationRejectsStaleContentAndRollsBackRouteFailure(t *testing.T) {
	service, db, execution := controlledService(t)
	result, err := publishAndComplete(service, context.Background(), "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle))
	if err != nil {
		t.Fatal(err)
	}
	review := result.Control.Reviews[0]
	controller := workflowcore.WorkflowControlService{DB: db}
	command := workflowcore.WorkflowControlCommand{CommandID: "confirm-1", Kind: "confirm", ReviewID: review.ID,
		ReviewVersion: review.Version, ManifestHash: "old hash"}
	if _, err := controller.Execute(context.Background(), "owner", "session-1", command); err == nil {
		t.Fatal("stale content was confirmed")
	}
	command.ManifestHash = review.ManifestHash
	if err := db.Callback().Create().Before("gorm:create").Register("test:reject-route", func(tx *gorm.DB) {
		if tx.Statement.Table == "plugin_route_decisions" {
			tx.AddError(errors.New("route storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Create().Remove("test:reject-route")
	if _, err := controller.Execute(context.Background(), "owner", "session-1", command); err == nil {
		t.Fatal("expected route transaction failure")
	}
	if err := db.First(&review, "id = ?", review.ID).Error; err != nil {
		t.Fatal(err)
	}
	if review.Status != "pending" || review.AcceptedAt != nil {
		t.Fatalf("partial confirmation: %+v", review)
	}
}

func TestAutoFinalSubmissionReturnsCommittedCompletion(t *testing.T) {
	service, db, execution := controlledService(t)
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("review_required", false).Error; err != nil {
		t.Fatal(err)
	}
	result, err := publishAndComplete(service, context.Background(), "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle))
	if err != nil || result.Control.Continuation != "completed" {
		t.Fatalf("submit returned pre-commit lifecycle: %+v %v", result, err)
	}
}

func TestConfirmationCannotAcceptADeletedRequiredOutput(t *testing.T) {
	service, db, execution := controlledService(t)
	ctx := context.Background()
	result, err := publishAndComplete(service, ctx, "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle))
	if err != nil {
		t.Fatal(err)
	}
	if err := controlstore.Transaction(ctx, db, "session-1", func(tx *gorm.DB, session *orm.WorkflowSession) error {
		if err := tx.Model(&orm.WorkflowSlotRevision{}).Where("session_id = ?", "session-1").Update("selected", false).Error; err != nil {
			return err
		}
		return controlstore.RefreshReviews(tx, session)
	}); err != nil {
		t.Fatal(err)
	}
	review := result.Control.Reviews[0]
	if err := db.First(&review, "id = ?", review.ID).Error; err != nil {
		t.Fatal(err)
	}
	_, err = (workflowcore.WorkflowControlService{DB: db}).Execute(ctx, "owner", "session-1", workflowcore.WorkflowControlCommand{CommandID: "confirm-empty", Kind: "confirm", ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash})
	var problem *controlstore.Error
	if !errors.As(err, &problem) || problem.Code != "REVIEW_OUTPUT_MISSING" {
		t.Fatalf("accepted missing required output: %v", err)
	}
	db.First(&review, "id = ?", review.ID)
	if review.Status != "pending" {
		t.Fatal("invalid confirmation changed the checkpoint")
	}
}

func TestApprovalPreferenceEntryPointsShareFuturePolicy(t *testing.T) {
	for _, scope := range []string{"step", "following"} {
		for _, entry := range []string{"preference", "confirmation"} {
			t.Run(scope+"/"+entry, func(t *testing.T) {
				service, db, execution := controlledService(t)
				ctx := context.Background()
				result, err := publishAndComplete(service, ctx, "owner", "session-1", "attempt-1", successfulSubmission(execution.ExecutionHandle))
				if err != nil {
					t.Fatal(err)
				}
				review := result.Control.Reviews[0]
				if entry == "preference" {
					corestore.Init(db, nil, nil)
					t.Cleanup(func() { corestore.Init(nil, nil, nil) })
					body, _ := json.Marshal(map[string]any{"step_id": review.StepID, "scope": scope, "approval_required": false})
					request := func(mcp bool) *http.Request {
						r := httptest.NewRequest(http.MethodPost, "/workflow-sessions/session-1:approval-preference", strings.NewReader(string(body)))
						r = mux.SetURLVars(r, map[string]string{"session_id": "session-1"})
						r.Header.Set("X-User-Id", "owner")
						r.Header.Set("Origin", "http://localhost:8090")
						if mcp {
							r.Header.Set("X-LazyMind-Invocation-Id", "mcp-call")
						}
						return r
					}
					denied := httptest.NewRecorder()
					workflowcore.SetWorkflowApprovalPreference(denied, request(true))
					if denied.Code != http.StatusForbidden {
						t.Fatalf("MCP changed approval policy: %d %s", denied.Code, denied.Body.String())
					}
					response := httptest.NewRecorder()
					workflowcore.SetWorkflowApprovalPreference(response, request(false))
					if response.Code != http.StatusOK {
						t.Fatalf("preference: %d %s", response.Code, response.Body.String())
					}
				} else {
					_, err := (workflowcore.WorkflowControlService{DB: db}).Execute(ctx, "owner", "session-1", workflowcore.WorkflowControlCommand{
						CommandID: "confirm", Kind: "confirm", ReviewID: review.ID, ReviewVersion: review.Version, ManifestHash: review.ManifestHash, PreferenceScope: scope,
					})
					if err != nil {
						t.Fatal(err)
					}
				}
				var preferences []orm.WorkflowApprovalPreference
				if err := db.Where("user_id = ?", "owner").Find(&preferences).Error; err != nil {
					t.Fatal(err)
				}
				step := review.StepID
				if scope == "following" {
					step = "*"
				}
				if len(preferences) != 1 || preferences[0].StepID != step || preferences[0].ApprovalRequired {
					t.Fatalf("future approval policy: %+v", preferences)
				}
				var current orm.WorkflowReviewCheckpoint
				if err := db.First(&current, "id = ?", review.ID).Error; err != nil {
					t.Fatal(err)
				}
				want := "pending"
				if entry == "confirmation" {
					want = "accepted"
				}
				if current.Status != want {
					t.Fatalf("future preference changed current review: %s", current.Status)
				}
			})
		}
	}
}

func TestNativeExecutionSharesAtomicReviewAndHostContinuation(t *testing.T) {
	for _, review := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "human"}[review], func(t *testing.T) {
			service, db := hostedTestService(t)
			service.Completion = &execution.Service{DB: db, Store: service.Store, Attempts: service.Attempts, Contexts: service.Contexts}
			if err := db.AutoMigrate(&orm.WorkflowReviewCheckpoint{}, &orm.WorkflowHostAction{}, &orm.WorkflowApprovalPreference{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Updates(map[string]any{
				"control_protocol":     controlpolicy.Protocol,
				"control_binding_json": `{"driver_session_id":"driver","connector_id":"connector","generation":1}`,
			}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Updates(map[string]any{"executor_host": "lazymind", "review_required": review, "task_id": "native-task"}).Error; err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, err := service.Attempts.ClaimForHost(ctx, "external", HostName); err == nil {
				t.Fatal("external host claimed a native execution")
			}
			observed, err := service.Begin(ctx, "owner", "session-1", "attempt-1")
			if err != nil || observed.ExecutorHost != "lazymind" || observed.ExecutionHandle != "" || observed.StepContract.Prompt != "" {
				t.Fatalf("native handoff leaked a contract: %+v %v", observed, err)
			}
			claim, err := service.Attempts.ClaimForHost(ctx, "native", "lazymind")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Resume(ctx, "owner", "session-1", "attempt-1"); err != nil {
				t.Fatal(err)
			}
			if err := service.Attempts.ValidateLease(ctx, claim.AttemptID, claim.LeaseToken); err != nil {
				t.Fatal("external resume rotated the native lease", err)
			}
			var session orm.WorkflowSession
			db.First(&session, "id = ?", "session-1")
			waiting, err := controlstore.Read(db, session)
			if err != nil || waiting.Continuation != "awaiting_executor" || waiting.Admission.CanBegin || len(waiting.NativeExecutionIDs) != 1 {
				t.Fatalf("native wait not guarded: %+v %v", waiting, err)
			}
			input := successfulSubmission(claim.LeaseToken)
			if _, err := publishAndComplete(service, ctx, "owner", session.ID, claim.AttemptID, input); err == nil {
				t.Fatal("public submit accepted native artifacts")
			}
			missing := json.RawMessage(`{"summary":"missing artifacts"}`)
			if err := finishNative(service, ctx, claim.AttemptID, claim.LeaseToken, "succeeded", "", missing); err == nil {
				t.Fatal("native completion skipped required outputs")
			}
			var count int64
			db.Model(&orm.WorkflowSlotRevision{}).Count(&count)
			if count != 0 {
				t.Fatal("failed native completion published artifacts")
			}
			raw, _ := json.Marshal(executor.Result{Summary: "native result"})
			if err := finishNative(service, ctx, claim.AttemptID, "stale", "succeeded", "", raw); err == nil {
				t.Fatal("stale native lease accepted")
			}
			contract, err := service.Contexts.LoadAttemptContext(ctx, claim.AttemptID)
			if err != nil {
				t.Fatal(err)
			}
			contract.ExecutionHandle = claim.LeaseToken
			for _, artifact := range input.Artifacts {
				if err := service.Artifacts.Save(ctx, contract, artifact); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := finishNative(service, ctx, claim.AttemptID, claim.LeaseToken, "succeeded", "", raw); err != nil {
					t.Fatal(err)
				}
			}
			db.First(&session, "id = ?", "session-1")
			settled, err := controlstore.Read(db, session)
			if err != nil {
				t.Fatal(err)
			}
			if review {
				if settled.Continuation != "awaiting_user" || len(settled.Reviews) != 1 || settled.Delivery != nil {
					t.Fatalf("review barrier bypassed: %+v", settled)
				}
			} else {
				if settled.Continuation != "completed" || settled.Delivery == nil || settled.Delivery.ExecutionID != claim.AttemptID {
					t.Fatalf("native completion did not wake driver: %+v", settled)
				}
				if _, err := service.Begin(ctx, "owner", session.ID, claim.AttemptID); err != nil {
					t.Fatal(err)
				}
				db.Model(&orm.WorkflowHostAction{}).Where("consumed_at IS NULL").Count(&count)
				if count != 0 {
					t.Fatal("completed execution notification was not consumed")
				}
			}
			db.Model(&orm.WorkflowSlotRevision{}).Count(&count)
			if count != 1 {
				t.Fatalf("replay duplicated native artifacts: %d", count)
			}
		})
	}
}
