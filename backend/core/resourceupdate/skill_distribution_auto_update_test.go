package resourceupdate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	skilldistribution "lazymind/core/skillv2/distribution"
	"lazymind/core/skillv2/testutil"
)

func TestScannerLeavesBuiltinDistributionForExplicitUpgrade(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"auto_evo": true, "origin_builtin_skill_uid": "bsk_demo",
	}).Error; err != nil {
		t.Fatal(err)
	}
	current := strings.Repeat("a", 64)
	now := testutil.TimeFixture()
	if err := db.Create(&orm.SkillDistributionBinding{
		SkillID: "skill1", BuiltinSkillUID: "bsk_demo",
		CurrentArchiveSHA256: current, Conflicts: []byte("[]"), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	result, err := NewScanner(db.DB, Config{}, "scanner").RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.SkillDraftTasksCreated != 0 {
		t.Fatalf("scanner result = %#v", result)
	}
	var binding orm.SkillDistributionBinding
	if err := db.Where("skill_id = ?", "skill1").Take(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if binding.CurrentArchiveSHA256 != current || binding.PendingArchiveSHA256 != "" {
		t.Fatalf("scanner changed installed distribution: %#v", binding)
	}
}

func TestGenericDraftAutoCommitIgnoresDistributionUpgrade(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Update("auto_evo", true).Error; err != nil {
		t.Fatal(err)
	}
	taskID := skilldistribution.UpgradeTaskID(strings.Repeat("b", 64))
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id": taskID, "version": 2,
	}).Error; err != nil {
		t.Fatal(err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "hash")

	created, err := scanAutoEvoSkillDrafts(context.Background(), db.DB, testutil.TimeFixture())
	if err != nil || created != 0 {
		t.Fatalf("created=%d err=%v", created, err)
	}
	request, err := json.Marshal(skillDraftAutoCommitRequestJSON{TaskID: taskID, DraftVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	outcome := NewWorker(db.DB, Config{}, "worker").handleAutoCommitSkillDraft(context.Background(), orm.ResourceUpdateTask{RequestJSON: request})
	if outcome.Status != orm.ResourceUpdateTaskStatusSkipped || outcome.ErrorCode != "distribution_upgrade_managed_separately" {
		t.Fatalf("outcome=%#v", outcome)
	}
}
