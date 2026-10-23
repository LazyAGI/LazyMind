package hosted

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
	workflowcore "lazymind/core/workflow"
	"lazymind/core/workflow/executor"
	workflowstore "lazymind/core/workflow/store"
)

func hostedTrackedTask(t *testing.T, db *gorm.DB) orm.TaskCenterTask {
	t.Helper()
	if err := db.AutoMigrate(&orm.TaskCenterTask{}, &orm.TaskNotification{}, &orm.UserNotificationPreferences{},
		&orm.WorkflowApprovalPreference{}, &orm.WorkflowTransitionCommand{}, &orm.WorkflowResource{}, &orm.WorkflowStepIntent{}); err != nil {
		t.Fatal(err)
	}
	sessionID := "session-1"
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", sessionID).Update("conversation_id", "conversation").Error; err != nil {
		t.Fatal(err)
	}
	task := orm.TaskCenterTask{ID: "tracked", UserID: "owner", ConversationID: "conversation", TaskType: "workflow_run", WorkflowSessionID: &sessionID, Status: "running"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func hostedSubmission(outcome string) Submission {
	submission := Submission{Outcome: outcome, Summary: "provider result"}
	if outcome == "succeeded" {
		submission.Artifacts = []executor.Artifact{{Slot: "report", ContentType: "text/plain", Seq: 1, Value: json.RawMessage(`{"text":"result"}`)}}
	}
	return submission
}

func TestHostedOldFailedReplayDoesNotEndReplacement(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			service, db := hostedTestService(t)
			task := hostedTrackedTask(t, db)
			if _, err := service.Begin(t.Context(), "owner", "session-1", "attempt-1"); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Submit(t.Context(), "owner", "session-1", "attempt-1", hostedSubmission("failed")); err != nil {
				t.Fatal(err)
			}
			var session orm.WorkflowSession
			if err := db.First(&session, "id = ?", "session-1").Error; err != nil {
				t.Fatal(err)
			}
			store.Init(db, nil, nil)
			defer store.Init(nil, nil, nil)
			body, _ := json.Marshal(map[string]any{"command_id": "retry", "operation": "retry", "retry_origin": "user", "target_step_id": "write", "task_id": "replacement", "expected_state_version": session.StateVersion, "graph_hash": session.GraphHash})
			req := httptest.NewRequest("POST", "/workflow-sessions/session-1:transition", bytes.NewReader(body))
			req = mux.SetURLVars(req, map[string]string{"session_id": "session-1"})
			req.Header.Set("X-User-Id", "owner")
			rec := httptest.NewRecorder()
			workflowcore.TransitionWorkflowSession(rec, req)
			if rec.Code != 200 {
				t.Fatalf("retry: %d %s", rec.Code, rec.Body.String())
			}
			if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
				t.Fatal(err)
			}
			version, updatedAt := session.StateVersion, session.UpdatedAt
			for range 2 {
				replay, err := service.Submit(t.Context(), "owner", session.ID, "attempt-1", hostedSubmission("failed"))
				if err != nil || !replay.AlreadyTerminal {
					t.Fatalf("old terminal replay: %#v err=%v", replay, err)
				}
			}
			if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			var noticeCount int64
			if err := db.Model(&orm.TaskNotification{}).Count(&noticeCount).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != "running" || task.FinishedAt != nil || noticeCount != 1 || session.Status != "active" || session.StateVersion != version || !session.UpdatedAt.Equal(updatedAt) {
				t.Errorf("stale replay changed replacement: task=%s notices=%d session=%s version=%d/%d updated=%v/%v", task.Status, noticeCount, session.Status, session.StateVersion, version, session.UpdatedAt, updatedAt)
			}
			if _, err := service.Begin(t.Context(), "owner", session.ID, "replacement"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := service.Submit(t.Context(), "owner", session.ID, "replacement", hostedSubmission(outcome)); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			var notices []orm.TaskNotification
			if err := db.Order("created_at,id").Find(&notices).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != outcome || len(notices) != 2 || notices[0].Event != "failed" || notices[1].Event != outcome {
				t.Fatalf("replacement completion: task=%s notices=%#v, want [failed,%s]", task.Status, notices, outcome)
			}
		})
	}
}

func rejectHostedNotices(t *testing.T, db *gorm.DB) func() {
	t.Helper()
	create, drop := "CREATE TRIGGER reject_hosted_notice BEFORE INSERT ON task_notifications BEGIN SELECT RAISE(ABORT, 'synthetic notification failure'); END", "DROP TRIGGER reject_hosted_notice"
	if db.Dialector.Name() == "postgres" {
		if err := db.Exec("CREATE FUNCTION reject_hosted_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic notification failure'; END $$").Error; err != nil {
			t.Fatal(err)
		}
		create, drop = "CREATE TRIGGER reject_hosted_notice BEFORE INSERT ON task_notifications FOR EACH ROW EXECUTE FUNCTION reject_hosted_notice()", "DROP TRIGGER reject_hosted_notice ON task_notifications"
	}
	if err := db.Exec(create).Error; err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := db.Exec(drop).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func hostedServiceWithDB(service *Service, db *gorm.DB) *Service {
	return &Service{DB: db, Store: workflowstore.New(db), Attempts: service.Attempts.WithDB(db),
		Contexts: executor.DBContextLoader{DB: db}, Artifacts: executor.DBArtifactSink{DB: db}}
}

func TestHostedNotificationFailurePreservesSessionForRecovery(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed"} {
		for _, mode := range []string{"standalone", "nested", "nested_without_implicit_savepoints"} {
			t.Run(outcome+"/"+mode, func(t *testing.T) {
				service, db := hostedTestService(t)
				task := hostedTrackedTask(t, db)
				if _, err := service.Begin(t.Context(), "owner", "session-1", "attempt-1"); err != nil {
					t.Fatal(err)
				}
				restore := rejectHostedNotices(t, db)
				submit := func(tx *gorm.DB) error {
					_, err := hostedServiceWithDB(service, tx).Submit(t.Context(), "owner", "session-1", "attempt-1", hostedSubmission(outcome))
					if err != nil {
						return err
					}
					// PostgreSQL must still accept writes after the optional insert failed.
					return tx.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").UpdateColumn("current_step_id", "outer-transaction-usable").Error
				}
				var err error
				if mode == "nested_without_implicit_savepoints" {
					err = db.Session(&gorm.Session{DisableNestedTransaction: true}).Transaction(submit)
				} else if mode == "nested" {
					err = db.Transaction(submit)
				} else {
					err = submit(db)
				}
				if err != nil {
					t.Fatalf("notification failure rejected durable execution completion: %v", err)
				}
				var session orm.WorkflowSession
				if err := db.First(&session, "id = ?", "session-1").Error; err != nil {
					t.Fatal(err)
				}
				wantSession := "failed"
				if outcome == "succeeded" {
					wantSession = "completed"
				}
				if session.Status != wantSession || session.CurrentStepID != "outer-transaction-usable" {
					t.Fatalf("authoritative session did not survive notification failure: %#v", session)
				}
				version, updatedAt := session.StateVersion, session.UpdatedAt
				completedAt, err := taskcenter.WorkflowNotificationCompletedAt(t.Context(), db, session)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := service.Submit(t.Context(), "owner", session.ID, "attempt-1", hostedSubmission(outcome)); err != nil {
					t.Fatalf("completion replay while notifications unavailable: %v", err)
				}
				if err := db.First(&task, "id = ?", task.ID).Error; err != nil || task.Status != "running" || task.FinishedAt != nil {
					t.Fatalf("task and optional notice must roll back together: task=%#v err=%v", task, err)
				}
				restore()
				for range 3 {
					if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
					t.Fatal(err)
				}
				var notices []orm.TaskNotification
				if err := db.Find(&notices).Error; err != nil {
					t.Fatal(err)
				}
				if task.Status != outcome || task.FinishedAt == nil || !task.FinishedAt.Equal(completedAt) || len(notices) != 1 || notices[0].Event != outcome {
					t.Fatalf("recovery did not converge on durable completion: task=%#v completed_at=%v notices=%#v", task, completedAt, notices)
				}
				if err := db.First(&session, "id = ?", session.ID).Error; err != nil || session.StateVersion != version || !session.UpdatedAt.Equal(updatedAt) {
					t.Fatalf("replay changed session metadata: version=%d/%d updated=%v/%v err=%v", session.StateVersion, version, session.UpdatedAt, updatedAt, err)
				}
			})
		}
	}
}

func TestHostedCompletionTimeLookupFailurePreservesSessionForRecovery(t *testing.T) {
	for _, outcome := range []string{"succeeded", "failed"} {
		for _, nested := range []bool{false, true} {
			mode := "standalone"
			if nested {
				mode = "nested"
			}
			t.Run(outcome+"/"+mode, func(t *testing.T) {
				service, db := hostedTestService(t)
				task := hostedTrackedTask(t, db)
				if _, err := service.Begin(t.Context(), "owner", "session-1", "attempt-1"); err != nil {
					t.Fatal(err)
				}
				injected := false
				if err := db.Callback().Query().Before("gorm:query").Register("test:completion-time-query-failure", func(tx *gorm.DB) {
					if injected || tx.Statement.Table != (orm.WorkflowSessionStep{}).TableName() || len(tx.Statement.Selects) != 1 || tx.Statement.Selects[0] != "created_at" {
						return
					}
					injected = true
					// A real PostgreSQL statement error aborts the subtransaction. The
					// optional lookup must roll back its savepoint before outer writes.
					tx.AddError(tx.Exec("SELECT missing_completion_time_test_column").Error)
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Callback().Query().Remove("test:completion-time-query-failure") })
				submit := func(tx *gorm.DB) error {
					_, err := hostedServiceWithDB(service, tx).Submit(t.Context(), "owner", "session-1", "attempt-1", hostedSubmission(outcome))
					if err != nil {
						return err
					}
					return tx.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").UpdateColumn("current_step_id", "outer-transaction-usable").Error
				}
				var err error
				if nested {
					err = db.Transaction(submit)
				} else {
					err = submit(db)
				}
				if err != nil || !injected {
					t.Fatalf("optional completion lookup rejected execution: injected=%v err=%v", injected, err)
				}
				var session orm.WorkflowSession
				if err := db.First(&session, "id = ?", "session-1").Error; err != nil {
					t.Fatal(err)
				}
				want := "failed"
				if outcome == "succeeded" {
					want = "completed"
				}
				if session.Status != want || session.CurrentStepID != "outer-transaction-usable" {
					t.Fatalf("optional time lookup rolled back authoritative session: %#v", session)
				}
				if err := db.First(&task, "id = ?", task.ID).Error; err != nil || task.Status != "running" || task.FinishedAt != nil {
					t.Fatalf("unavailable optional projection must remain recoverable: task=%#v err=%v", task, err)
				}
				completedAt, err := taskcenter.WorkflowNotificationCompletedAt(t.Context(), db, session)
				if err != nil {
					t.Fatal(err)
				}
				for range 3 {
					if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := service.Submit(t.Context(), "owner", session.ID, "attempt-1", hostedSubmission(outcome)); err != nil {
					t.Fatal(err)
				}
				if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
					t.Fatal(err)
				}
				var notices []orm.TaskNotification
				if err := db.Find(&notices).Error; err != nil {
					t.Fatal(err)
				}
				if task.Status != outcome || task.FinishedAt == nil || !task.FinishedAt.Equal(completedAt) || len(notices) != 1 || notices[0].Event != outcome {
					t.Fatalf("completion time lookup recovery did not converge: task=%#v notices=%#v", task, notices)
				}
			})
		}
	}
}

func TestHostedFinalizationIgnoresMismatchedCallbacks(t *testing.T) {
	for _, outcome := range []string{"failed", "succeeded"} {
		for _, mismatch := range []string{"session", "step", "attempt", "stale", "superseded", "status"} {
			t.Run(outcome+"/"+mismatch, func(t *testing.T) {
				_, db := hostedTestService(t)
				task := hostedTrackedTask(t, db)
				if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("status", outcome).Error; err != nil {
					t.Fatal(err)
				}
				stepID, attemptID := "write", "attempt-1"
				switch mismatch {
				case "session":
					if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", attemptID).Update("session_id", "another-session").Error; err != nil {
						t.Fatal(err)
					}
				case "step":
					stepID = "another-step"
				case "attempt":
					attemptID = "another-attempt"
				case "stale":
					if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", attemptID).Update("validity", "stale").Error; err != nil {
						t.Fatal(err)
					}
				case "superseded":
					if err := db.Create(&orm.WorkflowSessionStep{ID: "newer", SessionID: "session-1", StepID: "write", Attempt: 2, Status: "queued", Validity: "effective"}).Error; err != nil {
						t.Fatal(err)
					}
				case "status":
					if err := db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", attemptID).Update("status", "running").Error; err != nil {
						t.Fatal(err)
					}
				}
				var before, after orm.WorkflowSession
				if err := db.First(&before, "id = ?", "session-1").Error; err != nil {
					t.Fatal(err)
				}
				var eventsBefore int64
				if err := db.Model(&orm.WorkflowEvent{}).Count(&eventsBefore).Error; err != nil {
					t.Fatal(err)
				}
				if err := workflowcore.FinalizeHostAttempt(t.Context(), db, before.ID, stepID, attemptID, outcome); err != nil {
					t.Fatalf("obsolete callback should be harmless: %v", err)
				}
				if err := db.First(&after, "id = ?", before.ID).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
					t.Fatal(err)
				}
				var notices, routes, events int64
				if err := db.Model(&orm.TaskNotification{}).Count(&notices).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&orm.WorkflowRouteDecision{}).Count(&routes).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&orm.WorkflowEvent{}).Count(&events).Error; err != nil {
					t.Fatal(err)
				}
				if after.Status != before.Status || after.StateVersion != before.StateVersion || !after.UpdatedAt.Equal(before.UpdatedAt) || task.Status != "running" || notices != 0 || routes != 0 || events != eventsBefore {
					t.Fatalf("obsolete callback had side effects: session=%#v task=%s notices=%d routes=%d events=%d", after, task.Status, notices, routes, events)
				}
			})
		}
	}
}

func TestHostedFinalizationRechecksAttemptAfterSessionLock(t *testing.T) {
	service, db := hostedTestService(t)
	if db.Dialector.Name() != "postgres" {
		t.Skip("requires PostgreSQL row locking")
	}
	task := hostedTrackedTask(t, db)
	execution, err := service.Begin(t.Context(), "owner", "session-1", "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Attempts.Fail(t.Context(), "attempt-1", execution.ExecutionHandle, "TEST_FAILURE", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	mutation := db.Begin()
	if mutation.Error != nil {
		t.Fatal(mutation.Error)
	}
	defer mutation.Rollback()
	var session orm.WorkflowSession
	if err := mutation.Clauses(clause.Locking{Strength: "UPDATE"}).First(&session, "id = ?", "session-1").Error; err != nil {
		t.Fatal(err)
	}
	if err := mutation.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Update("validity", "stale").Error; err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- workflowcore.FinalizeHostAttempt(t.Context(), db, "session-1", "write", "attempt-1", "failed")
	}()
	select {
	case err := <-done:
		t.Fatalf("completion escaped session lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := mutation.Commit().Error; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&session, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	var notices int64
	if err := db.Model(&orm.TaskNotification{}).Count(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if session.Status != "active" || task.Status != "running" || notices != 0 {
		t.Fatalf("callback ignored invalidation committed under session lock: session=%s task=%s notices=%d", session.Status, task.Status, notices)
	}
}

func TestHostedNestedCompletionRetriesSQLiteBusy(t *testing.T) {
	service, db := hostedTestService(t)
	if db.Dialector.Name() != "sqlite" {
		t.Skip("SQLite transaction retry classification")
	}
	task := hostedTrackedTask(t, db)
	if _, err := service.Begin(t.Context(), "owner", "session-1", "attempt-1"); err != nil {
		t.Fatal(err)
	}
	insertAttempts := 0
	if err := db.Callback().Create().Before("gorm:create").Register("test:notice_busy", func(tx *gorm.DB) {
		if tx.Statement.Table == "task_notifications" {
			insertAttempts++
			if insertAttempts == 1 {
				tx.AddError(errors.New("database is locked (517)"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Create().Remove("test:notice_busy") })
	transactions := 0
	err := common.TransactionWithSQLiteBusyRetry(t.Context(), db, func(tx *gorm.DB) error {
		transactions++
		_, err := hostedServiceWithDB(service, tx).Submit(t.Context(), "owner", "session-1", "attempt-1", hostedSubmission("failed"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if transactions != 2 || insertAttempts != 2 || task.Status != "failed" || task.FinishedAt == nil {
		t.Fatalf("busy must retry caller transaction: transactions=%d inserts=%d task=%#v", transactions, insertAttempts, task)
	}
}
