package conversationgroup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgconn"
	"lazymind/core/algo"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestOrganizerRecoveryAndRetryEndpoint(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}, &orm.ConversationOrganizerRun{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{})
	store.Init(db.DB, nil, nil)
	snapshotRaw, _ := json.Marshal(organizerSnapshot{Conversations: make([]snapshotConversation, 113)})
	for _, tc := range []struct{ code, config, recovery string }{
		{"connection_error", `{}`, recoveryRetry},
		{"first_response_timeout", `{}`, recoveryRetry},
		{"rate_limited", `{}`, recoveryRetry},
		{"quota_exhausted", `{}`, recoveryRetry},
		{"invalid_output", `{}`, recoveryRetry},
		{"lock_expired", `{}`, recoveryRetry},
		{"model_config_changed", `{}`, recoveryRestart},
		{"invalid_snapshot", `{}`, recoveryRestart},
		{"connection_error", `{"llm":{"model":"old"}}`, recoveryRestart},
		{"authentication_failed", `{}`, recoveryRetry},
		{"authentication_failed", `{"llm":{"model":"old"}}`, recoveryRestart},
		{"input_too_large", `{}`, recoveryNone},
		{"scope_audit_unresolved", `{}`, recoveryRetry},
		{"incremental_step_failed", `{}`, recoveryNone},
		{"handler_not_found", `{}`, recoveryNone},
		{"unknown", `{}`, recoveryNone},
	} {
		t.Run(tc.code+tc.config, func(t *testing.T) {
			now := time.Now().UTC()
			run := orm.ConversationOrganizerRun{ID: uuid.NewString(), UserID: t.Name(), Status: "failed", ErrorCode: tc.code, ModelConfigJSON: json.RawMessage(tc.config), SnapshotJSON: snapshotRaw, CheckpointJSON: json.RawMessage(`{"cursor":50,"stage":"organizing","batch_size":50}`), ProgressCurrent: 50, CreatedAt: now, UpdatedAt: now}
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
			if tc.recovery == recoveryNone {
				startReq := httptest.NewRequest(http.MethodPost, "/", nil)
				startReq.Header.Set("X-User-Id", run.UserID)
				startResponse := httptest.NewRecorder()
				StartOrganizer(startResponse, startReq)
				if startResponse.Code != http.StatusConflict {
					t.Fatalf("blocked task restarted: %d %s", startResponse.Code, startResponse.Body.String())
				}
			}
			if tc.recovery == recoveryRetry {
				if response.Code != 200 || stored.Status != "pending" || jobs != 1 {
					t.Fatalf("retry: %d %s jobs=%d body=%s", response.Code, stored.Status, jobs, response.Body.String())
				}
				var cp incrementalCheckpoint
				if err := json.Unmarshal(stored.CheckpointJSON, &cp); err != nil {
					t.Fatal(err)
				}
				if stored.ProgressCurrent != 50 || cp.Cursor != 50 || (tc.code == "scope_audit_unresolved" && !cp.PreserveExistingCandidates) {
					t.Fatal("retry discarded progress")
				}
			} else if response.Code != 409 || stored.Status != "failed" || jobs != 0 {
				t.Fatalf("rejected retry mutated state: %d %s jobs=%d", response.Code, stored.Status, jobs)
			}
		})
	}
	legacy := orm.ConversationOrganizerRun{SnapshotJSON: json.RawMessage(`{}`), ModelConfigJSON: json.RawMessage(`{}`), Status: "failed", ErrorCode: "incremental_step_failed", ErrorMessage: legacyScopeAuditRejectedMessage}
	if got := organizerEffectiveErrorCode(legacy); got != "scope_audit_unresolved" {
		t.Fatalf("legacy effective code=%q", got)
	}
	if got := organizerRecovery(t.Context(), db.DB, legacy); got != recoveryRetry {
		t.Fatalf("legacy recovery=%q", got)
	}
	legacyDTO := runDTO(t.Context(), db.DB, legacy, false)
	legacyError, _ := legacyDTO["error"].(map[string]any)
	if legacyError["code"] != "scope_audit_unresolved" || legacyDTO["can_restart"] != false || legacyDTO["can_retry"] != true {
		t.Fatalf("legacy dto=%v", legacyDTO)
	}
}

func TestOrganizerRecoveryActionsAreExclusive(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{})
	retry := orm.ConversationOrganizerRun{SnapshotJSON: json.RawMessage(`{}`), ID: "retry", Status: "failed", ErrorCode: "connection_error", ModelConfigJSON: json.RawMessage(`{}`), StreamJSON: json.RawMessage(`{"execution_id":"old","settled":false}`)}
	if got := organizerRecovery(t.Context(), db.DB, retry); got != recoveryRetry || organizerCanRestart(retry, got) {
		t.Fatalf("retryable failure exposed restart: recovery=%s", got)
	}
	cancellation := orm.ConversationOrganizerRun{SnapshotJSON: json.RawMessage(`{}`), ID: "cancel-pending", Status: "failed", ErrorCode: "cancellation_unconfirmed", ModelConfigJSON: json.RawMessage(`{}`), StreamJSON: retry.StreamJSON}
	if got := organizerRecovery(t.Context(), db.DB, cancellation); got != recoveryRetry || organizerCanRestart(cancellation, got) {
		t.Fatalf("unconfirmed cancellation exposed restart: recovery=%s", got)
	}
	notRetryable := retry
	notRetryable.ID = "not-retryable"
	notRetryable.StreamJSON = json.RawMessage(`{"error_code":"connection_error","retryable":false,"settled":true}`)
	if got := organizerRecovery(t.Context(), db.DB, notRetryable); got != recoveryRetry || organizerCanRestart(notRetryable, got) {
		t.Fatalf("automatic retry flag incorrectly blocked manual recovery: recovery=%s", got)
	}
	restart := orm.ConversationOrganizerRun{ID: "restart", Status: "failed", ErrorCode: "model_config_changed", ModelConfigJSON: json.RawMessage(`{}`), StreamJSON: retry.StreamJSON}
	if got := organizerRecovery(t.Context(), db.DB, restart); got != recoveryRestart || !organizerCanRestart(restart, got) {
		t.Fatalf("non-retryable failure did not expose restart: recovery=%s", got)
	}
	canceled := orm.ConversationOrganizerRun{ID: "canceled", Status: "canceled", ErrorCode: "connection_error", StreamJSON: json.RawMessage(`{"settled":true}`)}
	if got := organizerRecovery(t.Context(), db.DB, canceled); got != recoveryRestart || !organizerCanRestart(canceled, got) {
		t.Fatalf("canceled run did not expose restart: recovery=%s", got)
	}
	broken := orm.ConversationOrganizerRun{ID: "broken", Status: "failed", ErrorCode: "unknown", StreamJSON: json.RawMessage(`{`)}
	if got := organizerRecovery(t.Context(), db.DB, broken); got != recoveryNone || organizerCanRestart(broken, got) {
		t.Fatalf("corrupt execution state exposed recovery: recovery=%s", got)
	}
}

func TestOrganizerFailurePreservesModelRetryability(t *testing.T) {
	for _, tc := range []struct {
		code      string
		retryable bool
	}{{"first_response_timeout", true}, {"authentication_failed", false}, {"input_too_large", false}, {"connection_error", false}, {"unexpected_code", true}} {
		code, retryable := organizerFailure("incremental_step_failed", failedOrganizerCall(organizerTaskResult{ErrorCode: tc.code, Retryable: tc.retryable}))
		if code != tc.code || retryable != (tc.retryable && organizerAutoRetry(tc.code)) {
			t.Fatalf("%s: code=%s retryable=%v", tc.code, code, retryable)
		}
	}
	if !sameOrganizerModelConfig(map[string]any{"llm": map[string]any{"model": "qwen", "source": "openai"}}, json.RawMessage(`{ "llm": {"source":"openai", "model":"qwen"} }`)) {
		t.Fatal("JSON formatting is not a configuration change")
	}
	if code, retryable := organizerFailure("incremental_step_failed", errScopeAuditUnresolved); code != "scope_audit_unresolved" || retryable {
		t.Fatalf("scope fallback failure code=%s retryable=%v", code, retryable)
	}
}

func TestRetryWaitsForSettlementAndPreservesAudit(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}, &orm.ConversationOrganizerRun{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{})
	store.Init(db.DB, nil, nil)
	settled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"settled": settled})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	cp := incrementalCheckpoint{Cursor: 1, NextOrdinal: 1, BatchSize: 50, Repair: 2, Pending: &incrementalPending{Operation: 0, AuditOrdinal: 0, Operations: []candidateOperation{{Op: "update", ID: "cand_a", Scope: "scope"}}, Assignments: []incrementalAssignment{{ID: "new", GroupID: "cand_a"}}}}
	cpRaw, _ := json.Marshal(cp)
	snapshot, _ := json.Marshal(organizerSnapshot{Conversations: []snapshotConversation{{ID: "old"}, {ID: "new"}}})
	run := orm.ConversationOrganizerRun{ID: "audit-run", UserID: "u", Status: "failed", ErrorCode: "invalid_output", ModelConfigJSON: json.RawMessage(`{}`), SnapshotJSON: snapshot, CheckpointJSON: cpRaw, StreamJSON: json.RawMessage(`{"execution_id":"old","settled":false}`), ProgressCurrent: 1}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	retry := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-User-Id", run.UserID)
		req = mux.SetURLVars(req, map[string]string{"run_id": run.ID})
		response := httptest.NewRecorder()
		RetryOrganizer(response, req)
		return response
	}
	if response := retry(); response.Code != 409 {
		t.Fatalf("unsettled retry: %d %s", response.Code, response.Body.String())
	}
	var count int64
	db.Model(&orm.AsyncJob{}).Count(&count)
	if count != 0 {
		t.Fatal("started a job before settlement")
	}
	settled = true
	if response := retry(); response.Code != 200 {
		t.Fatalf("settled retry: %d %s", response.Code, response.Body.String())
	}
	if err := db.Where("id=?", run.ID).Take(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(run.CheckpointJSON, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Cursor != 1 || cp.Repair != 0 || cp.Pending == nil || cp.Pending.AuditOrdinal != 0 || cp.Pending.Assignments[0].GroupID != "cand_a" {
		t.Fatalf("retry lost audit checkpoint: %+v", cp)
	}
}

func TestOrganizerFailureSeparatesAutomaticAndManualRecovery(t *testing.T) {
	for _, tc := range []struct {
		code string
		auto bool
	}{
		{"rate_limited", true}, {"service_unavailable", true}, {"quota_exhausted", false},
		{"authentication_failed", false}, {"invalid_output", false}, {"scope_audit_unresolved", false},
	} {
		_, auto := organizerFailure("incremental_step_failed", &organizerCallFailure{code: tc.code, retryable: true})
		if auto != tc.auto || recoveryForCode(tc.code) != recoveryRetry {
			t.Fatalf("%s auto=%v", tc.code, auto)
		}
	}
	for _, tc := range []struct {
		status int
		code   string
		auto   bool
	}{
		{429, "rate_limited", true}, {503, "service_unavailable", true}, {401, "authentication_failed", false}, {409, "conflict", false},
	} {
		code, auto := organizerFailure("incremental_step_failed", &algo.ConversationGroupingHTTPError{StatusCode: tc.status})
		if code != tc.code || auto != tc.auto {
			t.Fatalf("HTTP %d: %s %v", tc.status, code, auto)
		}
	}
	for _, tc := range []struct {
		state string
		auto  bool
	}{{"40001", true}, {"40P01", true}, {"23505", false}, {"08006", false}} {
		_, auto := organizerFailure("apply_failed", &pgconn.PgError{Code: tc.state})
		if auto != tc.auto {
			t.Fatalf("apply SQLSTATE %s auto=%v", tc.state, auto)
		}
	}
}
