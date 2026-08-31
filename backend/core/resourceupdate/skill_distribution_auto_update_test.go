package resourceupdate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	skillbuiltin "lazymind/core/skillv2/builtin"
	skilldistribution "lazymind/core/skillv2/distribution"
	"lazymind/core/skillv2/testutil"
)

func TestSkillDistributionAutoUpdaterAppliesOnlyEligibleCatalogChange(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"auto_evo": true, "origin_builtin_skill_uid": "bsk_demo",
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := testutil.TimeFixture()
	if err := db.Create(&orm.SkillDistributionBinding{
		SkillID: "skill1", BuiltinSkillUID: "bsk_demo",
		CurrentArchiveSHA256: strings.Repeat("a", 64),
		Conflicts:            []byte("[]"), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	catalogPath := writeDistributionCatalog(t, "bsk_demo", strings.Repeat("b", 64))
	applyCalls := 0
	updater := &skillDistributionAutoUpdater{
		db:          db.DB,
		catalogPath: func() string { return catalogPath },
		apply: func(_ context.Context, skillID, userID string) (skilldistribution.AutoApplyResult, error) {
			applyCalls++
			if skillID != "skill1" || userID != "user_001" {
				t.Fatalf("unexpected apply target %s/%s", userID, skillID)
			}
			if err := db.Model(&orm.SkillDistributionBinding{}).Where("skill_id = ?", skillID).
				Update("current_archive_sha256", strings.Repeat("b", 64)).Error; err != nil {
				t.Fatal(err)
			}
			return skilldistribution.AutoApplyResult{Applied: true, RevisionID: "rev2"}, nil
		},
	}

	result, err := updater.RunOnce(context.Background(), 10)
	if err != nil || result.Applied != 1 || applyCalls != 1 {
		t.Fatalf("first scan result=%#v calls=%d err=%v", result, applyCalls, err)
	}
	result, err = updater.RunOnce(context.Background(), 10)
	if err != nil || result.Applied != 0 || applyCalls != 1 {
		t.Fatalf("second scan result=%#v calls=%d err=%v", result, applyCalls, err)
	}
}

func TestSkillDistributionAutoUpdaterLeavesConflictPending(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Updates(map[string]any{
		"auto_evo": true, "origin_builtin_skill_uid": "bsk_demo",
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := testutil.TimeFixture()
	if err := db.Create(&orm.SkillDistributionBinding{
		SkillID: "skill1", BuiltinSkillUID: "bsk_demo",
		CurrentArchiveSHA256: strings.Repeat("a", 64), PendingArchiveSHA256: strings.Repeat("b", 64),
		Conflicts: []byte(`[{"path":"SKILL.md","kind":"text"}]`), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	catalogPath := writeDistributionCatalog(t, "bsk_demo", strings.Repeat("b", 64))
	updater := &skillDistributionAutoUpdater{
		db:          db.DB,
		catalogPath: func() string { return catalogPath },
		apply: func(context.Context, string, string) (skilldistribution.AutoApplyResult, error) {
			t.Fatal("conflicting pending update must not be auto-applied")
			return skilldistribution.AutoApplyResult{}, nil
		},
	}

	result, err := updater.RunOnce(context.Background(), 10)
	if err != nil || result.Applied != 0 || result.PendingReview != 0 {
		t.Fatalf("scan result=%#v err=%v", result, err)
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

func writeDistributionCatalog(t *testing.T, uid, archiveSHA string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	body, err := json.Marshal(skillbuiltin.Catalog{SchemaVersion: skillbuiltin.CatalogSchemaVersion, Skills: []skillbuiltin.CatalogSkill{{
		Key: "demo", UID: uid, SourceURL: "builtin://demo", ResolvedURL: "builtin://demo",
		Version: "2.0.0", Name: "demo", Description: "demo", Category: "demo",
		ArchiveSHA256: archiveSHA, TreeSHA256: strings.Repeat("c", 64), ArchiveSize: 1,
		PackageFile: "packages/" + uid + ".zip",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
