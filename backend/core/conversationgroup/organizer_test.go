package conversationgroup

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestApplyProposalSkipsChangedScopeAndSmallCandidate(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.AsyncJob{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerChange{})
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	const uid = "apply-user"
	snapshot := organizerSnapshot{ID: "run", Conversations: []snapshotConversation{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}, {ID: "c4"}}, Groups: []snapshotGroup{{ID: "formal", Name: "工作", Scope: "旧范围", Version: 1, Examples: []snapshotConversation{}}}}
	snapshotRaw, _ := json.Marshal(snapshot)
	run := orm.ConversationOrganizerRun{ID: "run", UserID: uid, Status: "running", Stage: "organizing", SnapshotJSON: snapshotRaw, SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), JobID: "job", CreatedAt: now, UpdatedAt: now}
	jobRow := orm.AsyncJob{ID: "job", JobType: organizerJobType, Status: string(asyncjob.StatusRunning), AttemptCount: 2, MaxAttempts: 3, LockUntil: &until, NextRunAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&jobRow).Error; err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"c1", "c2", "c3", "c4"} {
		if err := db.Create(&orm.Conversation{ID: id, DisplayName: id, BaseModel: orm.BaseModel{CreateUserID: uid, CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: run.ID, ConversationID: id, UserID: uid, Title: id, Summary: id, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	formal := orm.ConversationGroup{ID: "formal", UserID: uid, Name: "工作", NormalizedName: "工作", Scope: "新范围", Version: 2, CreatedBy: CreatedByUser, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&formal).Error; err != nil {
		t.Fatal(err)
	}
	proposal := organizerProposal{
		NewGroups:                []proposedNewGroup{{CandidateID: "small", Name: "小组", Scope: "小组范围", ConversationIDs: []string{"c1", "c2"}}},
		ExistingGroupAssignments: []proposedAssignment{{GroupID: formal.ID, GroupVersion: 1, ConversationIDs: []string{"c3"}}},
		FreeConversationIDs:      []string{"c4"},
	}
	raw, _ := json.Marshal(proposal)
	if err := applyProposal(t.Context(), db.DB, run, asyncjob.Job{ID: "job", AttemptCount: 2}, proposal, raw); err != nil {
		t.Fatal(err)
	}
	var memberCount, autoCount int64
	db.Model(&orm.ConversationGroupMember{}).Count(&memberCount)
	db.Model(&orm.ConversationGroup{}).Where("created_by=?", CreatedByOrganizer).Count(&autoCount)
	if memberCount != 0 || autoCount != 0 {
		t.Fatalf("unexpected writes members=%d groups=%d", memberCount, autoCount)
	}
	var stored orm.ConversationOrganizerRun
	db.Where("id=?", run.ID).Take(&stored)
	var result struct {
		Skipped    int               `json:"skipped_count"`
		Reasons    map[string]string `json:"skip_reasons"`
		Unassigned map[string]string `json:"unassigned_reasons"`
	}
	if err := json.Unmarshal(stored.ResultJSON, &result); err != nil {
		t.Fatal(err)
	}
	if stored.Status != "succeeded" || result.Skipped != 1 || result.Unassigned["c1"] != "below_min_group_size" || result.Reasons["c3"] != "group_scope_changed" {
		t.Fatalf("unexpected apply result status=%s result=%s", stored.Status, stored.ResultJSON)
	}
	dto := runDTO(t.Context(), db.DB, stored, true)
	itemsJSON, _ := json.Marshal(dto["items"])
	var items []struct {
		ConversationID string `json:"conversation_id"`
		SkipReason     string `json:"skip_reason"`
		Corrected      bool   `json:"corrected"`
	}
	if err := json.Unmarshal(itemsJSON, &items); err != nil {
		t.Fatal(err)
	}
	foundReason := false
	for _, item := range items {
		if item.ConversationID == "c3" && item.SkipReason == "group_scope_changed" {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatalf("run DTO omitted persisted skip reason: %#v", dto["items"])
	}
}

func TestApplyProposalCreatesAutomaticGroupAtMinimum(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.AsyncJob{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerChange{})
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	const uid = "minimum-user"
	conversations := []snapshotConversation{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}}
	snapshotRaw, _ := json.Marshal(organizerSnapshot{ID: "run", Conversations: conversations, Groups: []snapshotGroup{}})
	run := orm.ConversationOrganizerRun{ID: "run", UserID: uid, Status: "running", SnapshotJSON: snapshotRaw, SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), JobID: "job", CreatedAt: now, UpdatedAt: now}
	jobRow := orm.AsyncJob{ID: "job", JobType: organizerJobType, Status: string(asyncjob.StatusRunning), AttemptCount: 1, MaxAttempts: 3, LockUntil: &until, NextRunAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&jobRow).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range conversations {
		if err := db.Create(&orm.Conversation{ID: item.ID, DisplayName: item.ID, BaseModel: orm.BaseModel{CreateUserID: uid, CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: run.ID, ConversationID: item.ID, UserID: uid, Title: item.ID, Summary: item.ID, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	proposal := organizerProposal{NewGroups: []proposedNewGroup{{CandidateID: "candidate", Name: "项目", Scope: "同一项目的开发会话", ConversationIDs: []string{"c1", "c2", "c3"}}}, ExistingGroupAssignments: []proposedAssignment{}, FreeConversationIDs: []string{}}
	raw, _ := json.Marshal(proposal)
	if err := applyProposal(t.Context(), db.DB, run, asyncjob.Job{ID: "job", AttemptCount: 1}, proposal, raw); err != nil {
		t.Fatal(err)
	}
	var group orm.ConversationGroup
	if err := db.Where("created_run_id=?", run.ID).Take(&group).Error; err != nil {
		t.Fatal(err)
	}
	var members, changes int64
	db.Model(&orm.ConversationGroupMember{}).Where("group_id=?", group.ID).Count(&members)
	db.Model(&orm.ConversationOrganizerChange{}).Where("run_id=?", run.ID).Count(&changes)
	if group.CreatedBy != CreatedByOrganizer || group.Scope == "" || members != 3 || changes != 3 {
		t.Fatalf("unexpected automatic group=%+v members=%d changes=%d", group, members, changes)
	}
}

func TestInvalidProposalIsAtomicAndOldAttemptIsFenced(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}, &orm.ConversationOrganizerRun{})
	now := time.Now().UTC()
	until := now.Add(time.Minute)
	snapshotRaw, _ := json.Marshal(organizerSnapshot{ID: "run", Conversations: []snapshotConversation{{ID: "c1"}}, Groups: []snapshotGroup{}})
	run := orm.ConversationOrganizerRun{ID: "run", UserID: "u", Status: "running", Stage: "organizing", SnapshotJSON: snapshotRaw, SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), JobID: "job", CheckpointJSON: json.RawMessage(`{"step":1}`), CreatedAt: now, UpdatedAt: now}
	jobRow := orm.AsyncJob{ID: "job", JobType: organizerJobType, Status: string(asyncjob.StatusRunning), AttemptCount: 2, MaxAttempts: 3, LockUntil: &until, NextRunAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&jobRow).Error; err != nil {
		t.Fatal(err)
	}
	invalid := organizerProposal{NewGroups: []proposedNewGroup{{CandidateID: "x", Name: "组", Scope: "范围", ConversationIDs: []string{"c1"}}}, FreeConversationIDs: []string{"c1"}}
	if err := applyProposal(t.Context(), db.DB, run, asyncjob.Job{ID: "job", AttemptCount: 2}, invalid, json.RawMessage(`{}`)); err == nil {
		t.Fatal("invalid duplicate proposal succeeded")
	}
	if err := ownedRunUpdate(t.Context(), db.DB, run.ID, asyncjob.Job{ID: "job", AttemptCount: 1}, "running", map[string]any{"checkpoint_json": json.RawMessage(`{"step":2}`)}); !errors.Is(err, errLeaseLost) {
		t.Fatalf("old attempt err=%v", err)
	}
	var stored orm.ConversationOrganizerRun
	db.Where("id=?", run.ID).Take(&stored)
	if stored.Status != "running" || string(stored.CheckpointJSON) != `{"step":1}` {
		t.Fatalf("atomic/fence failed: %+v", stored)
	}
}

func TestRetryOrganizerConflictsWithAnotherActiveRun(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}, &orm.ConversationOrganizerRun{}, &orm.UserSelectedModel{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{})
	store.Init(db.DB, nil, nil)
	now := time.Now().UTC()
	const uid = "retry-user"
	failed := orm.ConversationOrganizerRun{ID: "failed", UserID: uid, Status: "failed", ErrorCode: "connection_error", SnapshotJSON: json.RawMessage(`{}`), SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now.Add(-time.Minute), UpdatedAt: now}
	active := orm.ConversationOrganizerRun{ID: "active", UserID: uid, Status: "running", SnapshotJSON: json.RawMessage(`{}`), SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&failed).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&active).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", uid)
	req = mux.SetURLVars(req, map[string]string{"run_id": failed.ID})
	rec := httptest.NewRecorder()
	RetryOrganizer(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("retry status=%d body=%s", rec.Code, rec.Body.String())
	}
	var stored orm.ConversationOrganizerRun
	if err := db.Where("id=?", failed.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" {
		t.Fatalf("conflicting retry mutated run to %s", stored.Status)
	}
	var jobs int64
	db.Model(&orm.AsyncJob{}).Count(&jobs)
	if jobs != 0 {
		t.Fatalf("conflicting retry enqueued %d jobs", jobs)
	}
}

func TestTerminalJobReconciliationReleasesLockAndFencesRetriedJob(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.AsyncJob{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{})
	now := time.Now().UTC()
	const uid = "reconcile-user"
	conv := orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: uid, CreatedAt: now, UpdatedAt: now}}
	run := orm.ConversationOrganizerRun{ID: "run", UserID: uid, Status: "pending", Stage: "snapshot", SnapshotJSON: json.RawMessage(`{}`), SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), JobID: "old-job", CreatedAt: now, UpdatedAt: now}
	oldJob := orm.AsyncJob{ID: "old-job", JobType: organizerJobType, Status: string(asyncjob.StatusFailed), ErrorCode: asyncjob.ErrorCodeHandlerNotFound, ErrorMessage: "async job handler not found", AttemptCount: 1, MaxAttempts: 3, NextRunAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&oldJob).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: run.ID, ConversationID: conv.ID, UserID: uid, Title: "c", Summary: "c", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	locked, err := IsDeleteLocked(t.Context(), db.DB, uid, []string{conv.ID})
	if err != nil || !locked {
		t.Fatalf("before reconcile lock=%v err=%v", locked, err)
	}
	if err := ReconcileTerminalJobs(t.Context(), db.DB); err != nil {
		t.Fatal(err)
	}
	var stored orm.ConversationOrganizerRun
	if err := db.Where("id=?", run.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "failed" || stored.ErrorCode != asyncjob.ErrorCodeHandlerNotFound {
		t.Fatalf("terminal state not copied: %+v", stored)
	}
	locked, err = IsDeleteLocked(t.Context(), db.DB, uid, []string{conv.ID})
	if err != nil || locked {
		t.Fatalf("terminal run retained delete lock=%v err=%v", locked, err)
	}

	newJob := orm.AsyncJob{ID: "new-job", JobType: organizerJobType, Status: string(asyncjob.StatusPending), AttemptCount: 0, MaxAttempts: 3, NextRunAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&newJob).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&stored).Updates(map[string]any{"status": "pending", "stage": "organizing", "job_id": newJob.ID, "finished_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	if err := ReconcileTerminalJobs(t.Context(), db.DB); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id=?", run.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "pending" || stored.JobID != newJob.ID {
		t.Fatalf("old terminal job overwrote retry: %+v", stored)
	}
}

func TestRunDTOCorrectedIsJSONBoolean(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.ConversationGroupMember{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerChange{})
	now := time.Now().UTC()
	run := orm.ConversationOrganizerRun{ID: "run", UserID: "u", Status: "succeeded", SnapshotJSON: json.RawMessage(`{"conversations":[{"id":"c"}]}`), SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: "u", CreatedAt: now, UpdatedAt: now}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationOrganizerSnapshotItem{RunID: run.ID, ConversationID: "c", UserID: "u", Title: "c", Summary: "c", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ConversationOrganizerChange{ID: "change", RunID: run.ID, ConversationID: "c", Kind: "correction", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(runDTO(t.Context(), db.DB, run, true))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) || !bytes.Contains(raw, []byte(`"corrected":true`)) {
		t.Fatalf("corrected was not a JSON boolean: %s", raw)
	}
}

func TestUndoAutoGroupTracksPanelMetadataButPreservesIndependentEdit(t *testing.T) {
	for _, test := range []struct {
		name            string
		independentEdit bool
		wantDeleted     bool
	}{{name: "panel edit remains controlled", wantDeleted: true}, {name: "later independent edit is preserved", independentEdit: true, wantDeleted: false}} {
		t.Run(test.name, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerChange{})
			store.Init(db.DB, nil, nil)
			now := time.Now().UTC()
			const uid = "metadata-user"
			result, _ := json.Marshal(organizerResult{ControlledGroupVersions: map[string]int64{"group": 1}, SkipReasons: map[string]string{}})
			run := orm.ConversationOrganizerRun{ID: "run", UserID: uid, Status: "succeeded", SnapshotJSON: json.RawMessage(`{}`), SnapshotHash: "hash", ModelConfigJSON: json.RawMessage(`{}`), ResultJSON: result, CreatedAt: now, UpdatedAt: now}
			group := orm.ConversationGroup{ID: "group", UserID: uid, Name: "旧名", NormalizedName: "旧名", Scope: "范围", Version: 1, CreatedBy: CreatedByOrganizer, CreatedRunID: run.ID, CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&group).Error; err != nil {
				t.Fatal(err)
			}
			invokeUpdate := func(body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPatch, "/", bytes.NewBufferString(body))
				req.Header.Set("X-User-Id", uid)
				req = mux.SetURLVars(req, map[string]string{"group_id": group.ID})
				rec := httptest.NewRecorder()
				UpdateGroup(rec, req)
				return rec
			}
			if rec := invokeUpdate(`{"name":"面板名称","organizer_run_id":"run"}`); rec.Code != http.StatusOK {
				t.Fatalf("panel update %d %s", rec.Code, rec.Body.String())
			}
			if test.independentEdit {
				if rec := invokeUpdate(`{"scope":"独立修改"}`); rec.Code != http.StatusOK {
					t.Fatalf("independent update %d %s", rec.Code, rec.Body.String())
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.Header.Set("X-User-Id", uid)
			req = mux.SetURLVars(req, map[string]string{"run_id": run.ID})
			rec := httptest.NewRecorder()
			UndoOrganizer(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("undo %d %s", rec.Code, rec.Body.String())
			}
			var stored orm.ConversationGroup
			err := db.Unscoped().Where("id=?", group.ID).Take(&stored).Error
			if err != nil {
				t.Fatal(err)
			}
			if (stored.DeletedAt != nil) != test.wantDeleted {
				t.Fatalf("deleted=%v want=%v version=%d", stored.DeletedAt != nil, test.wantDeleted, stored.Version)
			}
		})
	}
}

func TestOlderResultCannotMutateConversationLockedByNewRun(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.ConversationGroup{}, &orm.ConversationGroupMember{}, &orm.ConversationGroupState{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerChange{})
	store.Init(db.DB, nil, nil)
	now := time.Now().UTC()
	const uid = "new-run-lock-user"
	conv := orm.Conversation{ID: "c", BaseModel: orm.BaseModel{CreateUserID: uid, CreatedAt: now, UpdatedAt: now}}
	oldRun := orm.ConversationOrganizerRun{ID: "old", UserID: uid, Status: "succeeded", SnapshotJSON: json.RawMessage(`{}`), SnapshotHash: "old", ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now.Add(-time.Minute), UpdatedAt: now}
	newRun := orm.ConversationOrganizerRun{ID: "new", UserID: uid, Status: "running", SnapshotJSON: json.RawMessage(`{}`), SnapshotHash: "new", ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	for _, value := range []any{&conv, &oldRun, &newRun, &orm.ConversationOrganizerSnapshotItem{RunID: oldRun.ID, ConversationID: conv.ID, UserID: uid, Title: "c", Summary: "c", CreatedAt: now}, &orm.ConversationOrganizerSnapshotItem{RunID: newRun.ID, ConversationID: conv.ID, UserID: uid, Title: "c", Summary: "c", CreatedAt: now}, &orm.ConversationGroupState{ConversationID: conv.ID, UserID: uid, Revision: 1, SourceRunID: oldRun.ID, UpdatedAt: now}, &orm.ConversationOrganizerChange{ID: "change", RunID: oldRun.ID, ConversationID: conv.ID, AfterMemberRevision: 1, Kind: "correction", CreatedAt: now}} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	request := func(handler http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
		req.Header.Set("X-User-Id", uid)
		req = mux.SetURLVars(req, map[string]string{"run_id": oldRun.ID, "conversation_id": conv.ID})
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	if rec := request(CorrectOrganizerItem, http.MethodPatch, `{"group_id":null}`); rec.Code != http.StatusConflict {
		t.Fatalf("correction status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := request(UndoOrganizer, http.MethodPost, ""); rec.Code != http.StatusConflict {
		t.Fatalf("undo status=%d body=%s", rec.Code, rec.Body.String())
	}
	var state orm.ConversationGroupState
	if err := db.Where("conversation_id=?", conv.ID).Take(&state).Error; err != nil {
		t.Fatal(err)
	}
	if state.Revision != 1 || state.SourceRunID != oldRun.ID {
		t.Fatalf("locked result mutation changed state: %+v", state)
	}
}

func TestConfirmOrganizerResultIsIdempotentAndPreventsUndo(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.ConversationOpening{}, &orm.Conversation{}, &orm.AsyncJob{}, &orm.ConversationGroupMember{}, &orm.ConversationOrganizerRun{}, &orm.ConversationOrganizerSnapshotItem{}, &orm.ConversationOrganizerChange{})
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	run := orm.ConversationOrganizerRun{ID: "confirm-run", UserID: "confirm-user", Status: "succeeded", SnapshotJSON: json.RawMessage(`{"conversations":[]}`), ModelConfigJSON: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	invoke := func(handler http.HandlerFunc) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.Header.Set("X-User-Id", run.UserID)
		req = mux.SetURLVars(req, map[string]string{"run_id": run.ID})
		rec := httptest.NewRecorder()
		handler(rec, req)
		return rec
	}
	for range 2 {
		response := invoke(ConfirmOrganizer)
		if response.Code != http.StatusOK {
			t.Fatalf("confirm: %d %s", response.Code, response.Body.String())
		}
		var body struct {
			Run struct {
				Status  string `json:"status"`
				CanUndo bool   `json:"can_undo"`
			} `json:"run"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Run.Status != "confirmed" || body.Run.CanUndo {
			t.Fatalf("unexpected confirmed result: %s", response.Body.String())
		}
	}
	if response := invoke(UndoOrganizer); response.Code != http.StatusConflict {
		t.Fatalf("undo after confirmation: %d %s", response.Code, response.Body.String())
	}
}
