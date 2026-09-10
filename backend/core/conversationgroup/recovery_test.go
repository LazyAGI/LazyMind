package conversationgroup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestOrganizerRecoveryAndRetryEndpoint(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}, &orm.ConversationOrganizerRun{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{})
	store.Init(db.DB, nil, nil)
	for _, tc := range []struct{ code, config, recovery string }{
		{"connection_error", `{}`, recoveryRetry},
		{"first_response_timeout", `{}`, recoveryRetry},
		{"rate_limited", `{}`, recoveryRetry},
		{"lock_expired", `{}`, recoveryRetry},
		{"model_config_changed", `{}`, recoveryRestart},
		{"invalid_snapshot", `{}`, recoveryRestart},
		{"connection_error", `{"llm":{"model":"old"}}`, recoveryRestart},
		{"authentication_failed", `{}`, recoveryRestart},
		{"authentication_failed", `{"llm":{"model":"old"}}`, recoveryRestart},
		{"input_too_large", `{}`, recoveryRestart},
		{"incremental_step_failed", `{}`, recoveryNone},
		{"handler_not_found", `{}`, recoveryNone},
		{"unknown", `{}`, recoveryNone},
	} {
		t.Run(tc.code+tc.config, func(t *testing.T) {
			now := time.Now().UTC()
			run := orm.ConversationOrganizerRun{ID: uuid.NewString(), UserID: t.Name(), Status: "failed", ErrorCode: tc.code, ModelConfigJSON: json.RawMessage(tc.config), SnapshotJSON: json.RawMessage(`{}`), CheckpointJSON: json.RawMessage(`{"cursor":50,"stage":"organizing"}`), ProgressCurrent: 50, CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			if got := organizerRecovery(t.Context(), db.DB, run); got != tc.recovery {
				t.Fatalf("recovery=%s want=%s", got, tc.recovery)
			}
			dto := runDTO(t.Context(), db.DB, run, false)
			if dto["can_retry"] != (tc.recovery == recoveryRetry) || dto["can_restart"] != (tc.recovery == recoveryRestart) {
				t.Fatalf("incorrect capabilities: %v", dto)
			}
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("X-User-Id", run.UserID)
			req = mux.SetURLVars(req, map[string]string{"run_id": run.ID})
			response := httptest.NewRecorder()
			RetryOrganizer(response, req)
			var stored orm.ConversationOrganizerRun
			db.Where("id=?", run.ID).Take(&stored)
			var jobs int64
			db.Model(&orm.AsyncJob{}).Where("resource_id=?", run.ID).Count(&jobs)
			if tc.recovery == recoveryRetry {
				if response.Code != 200 || stored.Status != "pending" || jobs != 1 {
					t.Fatalf("retry: %d %s jobs=%d body=%s", response.Code, stored.Status, jobs, response.Body.String())
				}
				if stored.ProgressCurrent != 50 || string(stored.CheckpointJSON) != string(run.CheckpointJSON) {
					t.Fatal("retry discarded progress")
				}
			} else if response.Code != 409 || stored.Status != "failed" || jobs != 0 {
				t.Fatalf("rejected retry mutated state: %d %s jobs=%d", response.Code, stored.Status, jobs)
			}
		})
	}
}

func TestFailedManualRetryOffersRestartButUnsettledExecutionDoesNot(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{})
	run := orm.ConversationOrganizerRun{ID: "run", Status: "failed", ErrorCode: "connection_error"}
	for i, id := range []string{"initial", "manual-retry"} {
		job := orm.AsyncJob{ID: id, JobType: organizerJobType, ResourceType: "conversation_organizer_run", ResourceID: run.ID, Status: string(asyncjob.StatusFailed), AttemptCount: 3, MaxAttempts: 3, NextRunAt: time.Now()}
		if err := db.Create(&job).Error; err != nil {
			t.Fatal(err)
		}
		if got := organizerCanRestart(t.Context(), db.DB, run, recoveryRetry); got != (i == 1) {
			t.Fatalf("restart=%v after %d jobs", got, i+1)
		}
	}
	// The count is durable: a restored run still offers both recovery choices.
	run.StreamJSON = json.RawMessage(`{"execution_id":"old","settled":false}`)
	if organizerCanRestart(t.Context(), db.DB, run, recoveryRetry) {
		t.Fatal("restart bypasses old execution")
	}
	if organizerRecovery(t.Context(), db.DB, run) != recoveryRetry {
		t.Fatal("must allow settlement retry")
	}
	run.StreamJSON = settledOrganizerStream(run.StreamJSON)
	if !organizerCanRestart(t.Context(), db.DB, run, recoveryRetry) {
		t.Fatal("confirmed settlement must unlock restart")
	}
}

func TestOrganizerFailurePreservesModelRetryability(t *testing.T) {
	for _, tc := range []struct {
		code      string
		retryable bool
	}{{"first_response_timeout", true}, {"authentication_failed", false}, {"input_too_large", false}, {"connection_error", false}, {"unexpected_code", true}} {
		code, retryable := organizerFailure("incremental_step_failed", failedOrganizerCall(organizerTaskResult{ErrorCode: tc.code, Retryable: tc.retryable}))
		if code != tc.code || retryable != (tc.retryable && recoveryForCode(tc.code) == recoveryRetry) {
			t.Fatalf("%s: code=%s retryable=%v", tc.code, code, retryable)
		}
	}
	if !sameOrganizerModelConfig(map[string]any{"llm": map[string]any{"model": "qwen", "source": "openai"}}, json.RawMessage(`{ "llm": {"source":"openai", "model":"qwen"} }`)) {
		t.Fatal("JSON formatting is not a configuration change")
	}
}
