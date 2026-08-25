package distribution_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	skilldiff "lazymind/core/skillv2/diff"
	skilldistribution "lazymind/core/skillv2/distribution"
	skillreview "lazymind/core/skillv2/review"
	skillrevision "lazymind/core/skillv2/revision"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
)

func TestPrepareCommitAndRollbackDistributionUpgrade(t *testing.T) {
	fixture := newUpgradeFixture(t,
		"user preference: old\nkeep\nplatform behavior: old\n",
		"user preference: personalized\nkeep\nplatform behavior: old\n",
		"user preference: old\nkeep\nplatform behavior: upgraded\n",
	)

	prepared, err := fixture.distributions.Prepare(context.Background(), skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatal(err)
	}
	if !prepared.AutoMerged || len(prepared.Conflicts) != 0 || prepared.DraftVersion <= 1 {
		t.Fatalf("prepared = %#v", prepared)
	}
	draftContent := fixture.draftFile(t, "SKILL.md")
	if !strings.Contains(draftContent, "personalized") || !strings.Contains(draftContent, "upgraded") {
		t.Fatalf("draft content = %q", draftContent)
	}

	commit, err := fixture.revisions.CommitDraft(context.Background(), skillrevision.CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: prepared.DraftVersion})
	if err != nil {
		t.Fatal(err)
	}
	var revision struct {
		ChangeSource  string `gorm:"column:change_source"`
		SourceRefType string `gorm:"column:source_ref_type"`
		SourceRefID   string `gorm:"column:source_ref_id"`
	}
	if err := fixture.db.Table("skill_revisions").Where("id = ?", commit.RevisionID).Take(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if revision.ChangeSource != "distribution_upgrade" || revision.SourceRefType != "builtin_package" || revision.SourceRefID != fixture.latest.ArchiveSHA256 {
		t.Fatalf("revision provenance = %#v", revision)
	}
	if current := fixture.currentArchive(t); current != fixture.latest.ArchiveSHA256 {
		t.Fatalf("current distribution = %q", current)
	}

	if _, err := fixture.revisions.Rollback(context.Background(), skillrevision.RollbackRequest{SkillID: "skill1", UserID: "user_001", TargetRevisionID: "rev1"}); err != nil {
		t.Fatal(err)
	}
	if current := fixture.currentArchive(t); current != fixture.baseArchive {
		t.Fatalf("distribution after rollback = %q", current)
	}
}

func TestDistributionConflictRequiresDraftReview(t *testing.T) {
	fixture := newUpgradeFixture(t,
		"answer: old\n",
		"answer: user\n",
		"answer: platform\n",
	)
	prepared, err := fixture.distributions.Prepare(context.Background(), skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.AutoMerged || len(prepared.Conflicts) != 1 || prepared.Conflicts[0].Kind != "text" {
		t.Fatalf("prepared = %#v", prepared)
	}
	if got := fixture.draftFile(t, "SKILL.md"); !strings.Contains(got, "answer: platform") || strings.Contains(got, "<<<<<<<") {
		t.Fatalf("conflict candidate = %q", got)
	}
	_, err = fixture.revisions.CommitDraft(context.Background(), skillrevision.CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: prepared.DraftVersion})
	if err != skilldistribution.ErrConflictsRequireReview {
		t.Fatalf("commit error = %v", err)
	}
	retried, err := fixture.distributions.Prepare(context.Background(), skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatalf("idempotent prepare: %v", err)
	}
	if retried.DraftVersion != prepared.DraftVersion || retried.Status.PendingVersion != "2.0.0" || retried.Status.PendingArchiveSHA256 != fixture.latest.ArchiveSHA256 {
		t.Fatalf("retried prepare = %#v", retried)
	}
}

func TestAutoApplyDistributionUpgradeFollowsConflictPolicy(t *testing.T) {
	t.Run("no conflict commits automatically", func(t *testing.T) {
		fixture := newUpgradeFixture(t,
			"user preference: old\nplatform behavior: old\n",
			"user preference: personalized\nplatform behavior: old\n",
			"user preference: old\nplatform behavior: upgraded\n",
		)
		result, err := fixture.distributions.AutoApply(
			context.Background(),
			skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"},
			revisionCommitter{service: fixture.revisions},
		)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Applied || result.PendingReview || result.RevisionID == "" || fixture.currentArchive(t) != fixture.latest.ArchiveSHA256 {
			t.Fatalf("auto apply result = %#v", result)
		}
		if got := testutil.CountRows(t, fixture.db, "skill_revisions", "skill_id = ?", "skill1"); got != 2 {
			t.Fatalf("revision count = %d, want 2", got)
		}
	})

	t.Run("conflict waits for user review", func(t *testing.T) {
		fixture := newUpgradeFixture(t, "answer: old\n", "answer: user\n", "answer: platform\n")
		result, err := fixture.distributions.AutoApply(
			context.Background(),
			skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"},
			revisionCommitter{service: fixture.revisions},
		)
		if err != nil {
			t.Fatal(err)
		}
		if result.Applied || !result.PendingReview || result.RevisionID != "" || fixture.currentArchive(t) != fixture.baseArchive {
			t.Fatalf("auto apply result = %#v", result)
		}
		if got := testutil.CountRows(t, fixture.db, "skill_revisions", "skill_id = ?", "skill1"); got != 1 {
			t.Fatalf("revision count = %d, want 1", got)
		}
	})
}

func TestPrepareContentEquivalentDistributionPromotesWithoutPendingState(t *testing.T) {
	fixture := newUpgradeFixture(t, "unchanged\n", "unchanged\n", "unchanged\n")
	prepared, err := fixture.distributions.Prepare(
		context.Background(),
		skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.DraftVersion != 0 || prepared.Status.Pending || prepared.Status.UpdateAvailable || prepared.Status.PendingArchiveSHA256 != "" {
		t.Fatalf("prepared = %#v", prepared)
	}
	if prepared.Status.CurrentVersion != "2.0.0" || fixture.currentArchive(t) != fixture.latest.ArchiveSHA256 {
		t.Fatalf("distribution was not promoted: %#v", prepared.Status)
	}
	if got := testutil.CountRows(t, fixture.db, "skill_revisions", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("revision count = %d, want 1", got)
	}
}

func TestDiscardUpgradeDraftClearsPendingDistribution(t *testing.T) {
	fixture := newUpgradeFixture(t, "answer: old\n", "answer: user\n", "answer: platform\n")
	if _, err := fixture.distributions.Prepare(context.Background(), skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatal(err)
	}
	skills := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: fixture.db.DB, BlobStore: fixture.blobs})
	if _, err := skills.DiscardDraft(context.Background(), skillservice.DiscardDraftRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.distributions.GetStatus(context.Background(), skilldistribution.StatusRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending || fixture.currentArchive(t) != fixture.baseArchive {
		t.Fatalf("status after discard = %#v", status)
	}
}

func TestActiveUpgradeDraftCannotBeOverwrittenByAutoEvolution(t *testing.T) {
	fixture := newUpgradeFixture(t, "answer: old\n", "answer: user\n", "answer: platform\n")
	if _, err := fixture.distributions.Prepare(context.Background(), skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatal(err)
	}
	skills := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: fixture.db.DB, BlobStore: fixture.blobs})
	err := skills.ApplyAutoEvoDraft(context.Background(), skillservice.AutoEvoDraftRequest{
		SkillID: "skill1", ConversationID: "conversation", Files: map[string][]byte{"SKILL.md": []byte(skillContent("auto evo\n"))},
	})
	if err != skilldistribution.ErrUpgradeDraftActive {
		t.Fatalf("auto evolution error = %v", err)
	}
	if got := fixture.draftFile(t, "SKILL.md"); !strings.Contains(got, "answer: platform") {
		t.Fatalf("upgrade draft was overwritten: %q", got)
	}
}

func TestDistributionConflictCanCommitThroughExistingDraftReview(t *testing.T) {
	fixture := newUpgradeFixture(t, "answer: old\n", "answer: user\n", "answer: platform\n")
	if _, err := fixture.distributions.Prepare(context.Background(), skilldistribution.PrepareRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatal(err)
	}
	resolver := skilldiff.NewRefResolver(skilldiff.RefResolverDeps{DB: fixture.db.DB})
	oldFS, newFS, err := resolver.ResolvePair(context.Background(), skilldiff.ResolvePairRequest{
		UserID: "user_001",
		Old:    skilldiff.DiffRef{Type: "head", SkillID: "skill1"},
		New:    skilldiff.DiffRef{Type: "draft", SkillID: "skill1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := skilldiff.NewService(skilldiff.ServiceDeps{}).CompareFile(context.Background(), oldFS, newFS, skilldiff.DiffOptions{Path: "SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	reviews := skillreview.NewService(skillreview.ServiceDeps{DB: fixture.db.DB, BlobStore: fixture.blobs})
	file, err = reviews.PrepareFile(context.Background(), skillreview.PrepareFileRequest{SkillID: "skill1", UserID: "user_001", File: file})
	if err != nil {
		t.Fatal(err)
	}
	items := make([]skillreview.ActionItem, 0, file.HunkCount)
	for _, line := range file.DiffEntryLines {
		if line.Type == "HUNK" {
			items = append(items, skillreview.ActionItem{Path: "SKILL.md", HunkID: line.HunkID, Decision: "accepted"})
		}
	}
	action, err := reviews.Action(context.Background(), skillreview.ActionRequest{
		SkillID: "skill1", UserID: "user_001", ReviewID: file.ReviewID, ExpectedReviewVersion: file.ReviewVersion, Items: items,
	})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := reviews.Commit(context.Background(), skillreview.CommitRequest{
		SkillID: "skill1", UserID: "user_001", ReviewID: file.ReviewID, ExpectedReviewVersion: action.ReviewVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if current := fixture.currentArchive(t); current != fixture.latest.ArchiveSHA256 {
		t.Fatalf("current distribution = %q", current)
	}
	var revision struct {
		ChangeSource string `gorm:"column:change_source"`
		SourceRefID  string `gorm:"column:source_ref_id"`
	}
	if err := fixture.db.Table("skill_revisions").Where("id = ?", commit.RevisionID).Take(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if revision.ChangeSource != "distribution_upgrade" || revision.SourceRefID != fixture.latest.ArchiveSHA256 {
		t.Fatalf("revision = %#v", revision)
	}
}

func TestStatusLazilyBackfillsLegacyBuiltinInstallBinding(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	archiveSHA := strings.Repeat("d", 64)
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "skill1").Update("origin_builtin_skill_uid", "bsk_demo").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("skill_revisions").Where("id = ?", "rev1").Updates(map[string]any{
		"source_ref_type": "builtin_package", "source_ref_id": "bsk_demo@1.0.0#" + archiveSHA,
	}).Error; err != nil {
		t.Fatal(err)
	}
	provider := staticProvider{pkg: skilldistribution.Package{UID: "bsk_demo", Version: "1.0.0", ArchiveSHA256: archiveSHA, TreeSHA256: strings.Repeat("e", 64)}}
	service := skilldistribution.NewService(skilldistribution.ServiceDeps{DB: db.DB, Provider: provider, Clock: fixedClock{now: testutil.TimeFixture()}})
	status, err := service.GetStatus(context.Background(), skilldistribution.StatusRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Managed || status.UpdateAvailable || status.CurrentArchiveSHA256 != archiveSHA {
		t.Fatalf("status = %#v", status)
	}
	if got := testutil.CountRows(t, db, "skill_distribution_bindings", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("binding count = %d", got)
	}
}

type upgradeFixture struct {
	t             *testing.T
	db            *testutil.TestDB
	blobs         *skillservice.BlobStore
	distributions *skilldistribution.Service
	revisions     *skillrevision.Service
	latest        skilldistribution.Package
	baseArchive   string
}

func newUpgradeFixture(t *testing.T, baseBody, oursBody, theirsBody string) *upgradeFixture {
	t.Helper()
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	objectRoot := t.TempDir()
	blobs := skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(objectRoot))
	clock := fixedClock{now: testutil.TimeFixture()}
	baseContent := skillContent(baseBody)
	baseBlob, err := blobs.StoreDistributionBlob(context.Background(), db.DB, "SKILL.md", []byte(baseContent), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Table("skill_revision_entries").Where("revision_id = ? AND path = ?", "rev1", "SKILL.md").Updates(map[string]any{
		"blob_hash": baseBlob.Hash, "size": baseBlob.Size,
	}).Error; err != nil {
		t.Fatal(err)
	}
	baseArchive := strings.Repeat("a", 64)
	if err := db.Transaction(func(tx *gorm.DB) error {
		return skilldistribution.BindInitialTx(context.Background(), tx, skilldistribution.InitialBinding{
			SkillID: "skill1", RevisionID: "rev1", BuiltinUID: "bsk_demo", Version: "1.0.0",
			ArchiveSHA256: baseArchive, TreeSHA256: strings.Repeat("b", 64),
		}, clock.Now())
	}); err != nil {
		t.Fatal(err)
	}
	oursContent := skillContent(oursBody)
	oursBlob, err := blobs.StoreDistributionBlob(context.Background(), db.DB, "SKILL.md", []byte(oursContent), clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Table("skill_revision_entries").Where("revision_id = ? AND path = ?", "rev1", "SKILL.md").Updates(map[string]any{
		"blob_hash": oursBlob.Hash, "size": oursBlob.Size,
	}).Error; err != nil {
		t.Fatal(err)
	}
	latestContent := skillContent(theirsBody)
	latestHash := sha256.Sum256([]byte(latestContent))
	latest := skilldistribution.Package{
		UID: "bsk_demo", Version: "2.0.0", ArchiveSHA256: strings.Repeat("c", 64), TreeSHA256: hex.EncodeToString(latestHash[:]),
		Files: map[string][]byte{"SKILL.md": []byte(latestContent)},
	}
	provider := staticProvider{pkg: latest}
	return &upgradeFixture{
		t: t, db: db, blobs: blobs, latest: latest, baseArchive: baseArchive,
		distributions: skilldistribution.NewService(skilldistribution.ServiceDeps{DB: db.DB, Blobs: blobs, Provider: provider, Clock: clock}),
		revisions:     skillrevision.NewService(skillrevision.ServiceDeps{DB: db.DB, BlobStore: skillrevision.NewBlobStore(db.DB, skillrevision.NewLocalObjectStore(objectRoot))}),
	}
}

func (fixture *upgradeFixture) draftFile(t *testing.T, path string) string {
	t.Helper()
	var entry struct {
		BlobHash *string `gorm:"column:blob_hash"`
	}
	if err := fixture.db.Table("skill_draft_entries").Where("skill_id = ? AND path = ?", "skill1", path).Take(&entry).Error; err != nil {
		t.Fatal(err)
	}
	var blob struct {
		Content []byte `gorm:"column:content"`
	}
	if err := fixture.db.Table("skill_blobs").Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		t.Fatal(err)
	}
	return string(blob.Content)
}

func (fixture *upgradeFixture) currentArchive(t *testing.T) string {
	t.Helper()
	var row struct {
		Current string `gorm:"column:current_archive_sha256"`
	}
	if err := fixture.db.Table("skill_distribution_bindings").Where("skill_id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.Current
}

func skillContent(body string) string {
	return "---\nname: demo\ndescription: demo skill\n---\n" + body
}

type staticProvider struct {
	pkg skilldistribution.Package
}

type revisionCommitter struct {
	service *skillrevision.Service
}

func (committer revisionCommitter) CommitDraft(ctx context.Context, skillID, userID string, draftVersion int64) (string, error) {
	response, err := committer.service.CommitDraft(ctx, skillrevision.CommitDraftRequest{
		SkillID: skillID, UserID: userID, DraftVersion: draftVersion,
	})
	return response.RevisionID, err
}

func (provider staticProvider) Latest(uid string) (skilldistribution.Package, bool, error) {
	return provider.pkg, uid == provider.pkg.UID, nil
}

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time { return clock.now }
