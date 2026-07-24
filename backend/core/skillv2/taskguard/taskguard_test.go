package taskguard

import (
	"context"
	"testing"
	"time"

	"lazymind/core/skillv2/testutil"
	"lazymind/core/state"
)

func TestEvaluateSkillOperationRules(t *testing.T) {
	db := testutil.NewTestDB(t)
	createStatsTable(t, db)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	stateStore, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	ctx := context.Background()
	assertDecision := func(name string, req SkillOperationRequest, allowed bool, reason, disposition string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			decision, err := EvaluateSkillOperation(ctx, db.DB, stateStore, req)
			if err != nil {
				t.Fatalf("EvaluateSkillOperation: %v", err)
			}
			if decision.Allowed != allowed || decision.ReasonCode != reason || decision.Disposition != disposition {
				t.Fatalf("decision = %#v, want allowed=%v reason=%q disposition=%q", decision, allowed, reason, disposition)
			}
		})
	}

	base := SkillOperationRequest{UserID: "user_001"}
	assertDecision("review allowed without maintenance", withOperation(base, TriggerSkillReview), true, "", "")

	insertStats(t, db, "review_preparing", "review_req", "other_user", "preparing")
	assertDecision("other user does not block", withOperation(base, TriggerSkillReview), true, "", "")

	insertStats(t, db, "review_analyzing_own", "review_req_own", "user_001", "analyzing")
	assertDecision("non-terminal maintenance rejects manual review", withOperation(base, TriggerSkillReview), false, ReasonMaintenanceTaskRunning, DispositionReject)
	scheduled := withOperation(base, TriggerSkillReview)
	scheduled.TriggerSource = triggerSourceScheduled
	assertDecision("non-terminal maintenance defers scheduled review", scheduled, false, ReasonMaintenanceTaskRunning, DispositionDefer)
	if err := db.Table("skill_review_stats").Where("userid = ?", "user_001").Update("status", "completed").Error; err != nil {
		t.Fatalf("complete maintenance: %v", err)
	}
	insertStats(t, db, "review_skipped_own", "review_req_skipped", "user_001", "skipped")
	insertStats(t, db, "review_failed_own", "review_req_failed", "user_001", "failed")
	assertDecision("terminal maintenance statuses do not block", withOperation(base, TriggerSkillReview), true, "", "")

	testutil.SeedTextBlob(t, db, "draft_hash", "draft")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "draft_hash")
	organize := SkillOperationRequest{UserID: "user_001", SkillIDs: []string{"skill1"}, Operation: TriggerSkillOrganize}
	decision, err := EvaluateSkillOperation(ctx, db.DB, stateStore, organize)
	if err != nil {
		t.Fatalf("organize decision: %v", err)
	}
	if decision.Allowed || decision.ReasonCode != ReasonOrganizeDraftConflict || len(decision.BlockingSkills) != 1 || decision.BlockingSkills[0] != "skills/research/论文精读" {
		t.Fatalf("unexpected organize decision: %#v", decision)
	}
	assertDecision("draft does not block review", withOperation(base, TriggerSkillReview), true, "", "")

	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("task_id", "session_a").Error; err != nil {
		t.Fatalf("set editor owner: %v", err)
	}
	if err := stateStore.Set(ctx, conversationIdleTTLKeyPrefix+"session_a", []byte("active"), time.Minute); err != nil {
		t.Fatalf("set editor ttl: %v", err)
	}
	startEdit := SkillOperationRequest{UserID: "user_001", SkillID: "skill1", Operation: StartUserEdit}
	assertDecision("active editor blocks user edit", startEdit, false, ReasonDraftStillEditing, DispositionReject)
	if err := stateStore.Del(ctx, conversationIdleTTLKeyPrefix+"session_a"); err != nil {
		t.Fatalf("delete editor ttl: %v", err)
	}
	assertDecision("inactive editor allows user takeover", startEdit, true, "", "")

	writeOwn := SkillOperationRequest{UserID: "user_001", SkillID: "skill1", TaskID: "session_a", Operation: WriteSkillDraft}
	assertDecision("editor can append own draft", writeOwn, true, "", "")
	writeOther := writeOwn
	writeOther.TaskID = "session_b"
	assertDecision("other editor cannot write draft", writeOther, false, ReasonDraftOwnedByOtherTask, DispositionReject)
}

func TestEvaluateSkillOperationRemoteMaintenanceAndAutoUpdate(t *testing.T) {
	db := testutil.NewTestDB(t)
	createStatsTable(t, db)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "draft_hash", "draft")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "draft_hash")
	stateStore, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })

	insertStats(t, db, "org_task", "org_request", "user_001", "organizing")
	orgWrite := SkillOperationRequest{UserID: "user_001", SkillID: "skill1", TaskID: "org_request", Operation: WriteSkillDraft, TriggerSource: "remote_fs"}
	decision, err := EvaluateSkillOperation(context.Background(), db.DB, stateStore, orgWrite)
	if err != nil {
		t.Fatalf("org write decision: %v", err)
	}
	if decision.Allowed || decision.ReasonCode != ReasonDraftOwnedByOtherTask {
		t.Fatalf("org unexpectedly acquired existing draft: %#v", decision)
	}

	if err := db.Table("skill_review_stats").Where("id = ?", "org_task").Update("status", "completed").Error; err != nil {
		t.Fatalf("complete org task: %v", err)
	}
	insertStats(t, db, "review_task", "review_request", "user_001", "generating")
	reviewWrite := orgWrite
	reviewWrite.TaskID = "review_request"
	decision, err = EvaluateSkillOperation(context.Background(), db.DB, stateStore, reviewWrite)
	if err != nil || !decision.Allowed {
		t.Fatalf("review should take over existing draft: decision=%#v err=%v", decision, err)
	}

	if err := db.Table("skill_review_stats").Where("id = ?", "review_task").Update("status", "completed").Error; err != nil {
		t.Fatalf("complete review task: %v", err)
	}
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("task_id", "session_a").Error; err != nil {
		t.Fatalf("set editor owner: %v", err)
	}
	if err := stateStore.Set(context.Background(), conversationIdleTTLKeyPrefix+"session_a", []byte("active"), time.Minute); err != nil {
		t.Fatalf("set editor ttl: %v", err)
	}
	auto := SkillOperationRequest{UserID: "user_001", SkillID: "skill1", Operation: AutoUpdateSkill, TriggerSource: triggerSourceScheduled}
	decision, err = EvaluateSkillOperation(context.Background(), db.DB, stateStore, auto)
	if err != nil || decision.Allowed || decision.Disposition != DispositionDefer || decision.ReasonCode != ReasonDraftStillEditing {
		t.Fatalf("active editor auto update decision=%#v err=%v", decision, err)
	}
}

func withOperation(req SkillOperationRequest, operation SkillOperation) SkillOperationRequest {
	req.Operation = operation
	return req
}

func createStatsTable(t *testing.T, db *testutil.TestDB) {
	t.Helper()
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS skill_review_stats (
		id TEXT NOT NULL PRIMARY KEY,
		requestid TEXT NOT NULL,
		userid TEXT NOT NULL,
		status TEXT NOT NULL,
		started_at TEXT NOT NULL,
		duration_ms INTEGER NOT NULL DEFAULT 0,
		summary TEXT NOT NULL DEFAULT '{}'
	)`).Error; err != nil {
		t.Fatalf("create skill_review_stats: %v", err)
	}
}

func insertStats(t *testing.T, db *testutil.TestDB, id, requestID, userID, status string) {
	t.Helper()
	if err := db.Table("skill_review_stats").Create(map[string]any{
		"id": id, "requestid": requestID, "userid": userID, "status": status,
		"started_at": "2026-07-13T10:00:00Z", "duration_ms": 0, "summary": "{}",
	}).Error; err != nil {
		t.Fatalf("insert skill_review_stats: %v", err)
	}
}
