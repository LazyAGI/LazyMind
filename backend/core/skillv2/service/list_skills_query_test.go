package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestListSkillsKeywordMatchesCurrentSearchIndex(t *testing.T) {
	db := newSkillV2TestDB(t)
	ensureSkillSearchIndexTable(t, db)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	setSkillMetadata(t, db, "skill1", "Planner", "writing", "daily notes", `["team"]`)
	seedSearchIndex(t, db, "skill1", "rev1", "needle from current index")

	got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001", Keyword: "needle"})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].ID != "skill1" {
		t.Fatalf("result = %#v, want current index match", got)
	}
}

func TestListSkillsKeywordIgnoresStaleSearchIndex(t *testing.T) {
	db := newSkillV2TestDB(t)
	ensureSkillSearchIndexTable(t, db)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	setSkillMetadata(t, db, "skill1", "Planner", "writing", "daily notes", `["team"]`)
	setHeadContent(t, db, "rev1", "current head without requested term")
	seedSearchIndex(t, db, "skill1", "old-rev", "needle from stale index")

	got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001", Keyword: "needle"})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if got.Total != 0 || len(got.Items) != 0 {
		t.Fatalf("result = %#v, want stale index ignored", got)
	}
	assertSearchIndexUnchanged(t, db, "skill1", "old-rev", "needle from stale index")
}

func TestListSkillsKeywordFallsBackToCurrentHeadForStaleSearchIndex(t *testing.T) {
	db := newSkillV2TestDB(t)
	ensureSkillSearchIndexTable(t, db)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	setSkillMetadata(t, db, "skill1", "Planner", "writing", "daily notes", `["team"]`)
	setHeadContent(t, db, "rev1", "needle from current head")
	seedSearchIndex(t, db, "skill1", "old-rev", "stale content without requested term")

	got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001", Keyword: "needle"})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].ID != "skill1" {
		t.Fatalf("result = %#v, want current head fallback match", got)
	}
	assertSearchIndexUnchanged(t, db, "skill1", "old-rev", "stale content without requested term")
}

func TestListSkillsKeywordFallsBackToCurrentHeadForMissingSearchIndex(t *testing.T) {
	db := newSkillV2TestDB(t)
	ensureSkillSearchIndexTable(t, db)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	setSkillMetadata(t, db, "skill1", "Planner", "writing", "daily notes", `["team"]`)
	setHeadContent(t, db, "rev1", "needle from current head")

	got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001", Keyword: "needle"})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].ID != "skill1" {
		t.Fatalf("result = %#v, want current head fallback match", got)
	}
	var count int64
	if err := db.Model(&skillSearchIndexRow{}).Where("skill_id = ?", "skill1").Count(&count).Error; err != nil {
		t.Fatalf("count search index: %v", err)
	}
	if count != 0 {
		t.Fatalf("search index rows = %d, want 0 because list read path does not rebuild", count)
	}
}

func TestListSkillsKeywordFallbackWithoutSearchIndexTable(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	seedSkillWithHeadRevision(t, db, "skill2", "rev2")
	seedSkillWithHeadRevision(t, db, "skill3", "rev3")
	setSkillMetadata(t, db, "skill1", "Alpha Writer", "writing", "metadata hit", `["team","draft"]`)
	setSkillMetadata(t, db, "skill2", "Planner", "writing", "head hit", `["team","draft"]`)
	setSkillMetadata(t, db, "skill3", "Alpha Research", "research", "wrong category", `["team"]`)
	setHeadContent(t, db, "rev2", "alpha content from head text")

	got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{
		UserID:   "user_001",
		Keyword:  " ALPHA ",
		Category: "writing",
		Tags:     []string{"team", "draft"},
		Limit:    1,
	})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if got.Total != 2 {
		t.Fatalf("Total = %d, want 2", got.Total)
	}
	if len(got.Items) != 1 || got.Items[0].ID == "skill3" {
		t.Fatalf("Items = %#v, want one paginated filtered skill", got.Items)
	}
}

func TestListSkillsKeywordTreatsLikeCharactersLiterally(t *testing.T) {
	db := newSkillV2TestDB(t)
	ensureSkillSearchIndexTable(t, db)
	fixtures := map[string]string{
		"skill-hit":        `literal 100%_path\value`,
		"skill-percent":    `literal 100ABC_path\value`,
		"skill-underscore": `literal 100%Xpath\value`,
		"skill-slash":      `literal 100%_pathvalue`,
	}
	for skillID, content := range fixtures {
		revisionID := "rev-" + strings.TrimPrefix(skillID, "skill-")
		seedSkillWithHeadRevision(t, db, skillID, revisionID)
		setSkillMetadata(t, db, skillID, "Planner "+skillID, "writing", "daily notes", `["team"]`)
		setHeadContent(t, db, revisionID, content)
		seedSearchIndex(t, db, skillID, revisionID, content)
	}

	got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{
		UserID:  "user_001",
		Keyword: `100%_path\value`,
	})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].ID != "skill-hit" {
		t.Fatalf("result = %#v, want only literal LIKE character match", got)
	}
}

func TestListSkillsKeywordTotalPagingAndStableSort(t *testing.T) {
	db := newSkillV2TestDB(t)
	ensureSkillSearchIndexTable(t, db)
	for i := 0; i < 5; i++ {
		skillID := fmt.Sprintf("skill-%03d", i)
		revisionID := fmt.Sprintf("rev-%03d", i)
		seedSkillWithHeadRevision(t, db, skillID, revisionID)
		setSkillMetadata(t, db, skillID, "Planner "+skillID, "writing", "daily notes", `["team"]`)
		seedSearchIndex(t, db, skillID, revisionID, "needle")
	}

	got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{
		UserID:  "user_001",
		Keyword: "needle",
		Offset:  1,
		Limit:   2,
	})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if got.Total != 5 {
		t.Fatalf("Total = %d, want 5", got.Total)
	}
	gotIDs := itemIDs(got.Items)
	wantIDs := []string{"skill-003", "skill-002"}
	if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("item IDs = %v, want %v", gotIDs, wantIDs)
	}
}

func TestListSkillsKeywordQueryCountDoesNotScaleWithCandidates(t *testing.T) {
	one := listKeywordQueryCountForCandidates(t, 1)
	fifty := listKeywordQueryCountForCandidates(t, 50)
	t.Logf("keyword list query count: 1 candidate=%d 50 candidates=%d", one, fifty)
	if one != fifty {
		t.Fatalf("query count with 1 candidate = %d, with 50 candidates = %d; want constant", one, fifty)
	}
}

func listKeywordQueryCountForCandidates(t *testing.T, candidates int) int {
	t.Helper()
	db := newSkillV2TestDB(t)
	ensureSkillSearchIndexTable(t, db)
	for i := 0; i < candidates; i++ {
		skillID := fmt.Sprintf("skill-%03d", i)
		revisionID := fmt.Sprintf("rev-%03d", i)
		seedSkillWithHeadRevision(t, db, skillID, revisionID)
		setSkillMetadata(t, db, skillID, "Planner "+skillID, "writing", "daily notes", `["team"]`)
		content := "ordinary content"
		if i == 0 {
			content = "needle current content"
		}
		setHeadContent(t, db, revisionID, content)
	}

	count := 0
	countedDB := db.Session(&gorm.Session{Logger: countingLogger{
		Interface: logger.Default.LogMode(logger.Silent),
		count:     &count,
	}})
	svc := newListSkillService(t, countedDB)
	got, err := svc.ListSkills(context.Background(), ListSkillsRequest{
		UserID:  "user_001",
		Keyword: "needle",
		Limit:   1,
	})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].ID != "skill-000" {
		t.Fatalf("result = %#v, want one keyword match", got)
	}
	return count
}

type countingLogger struct {
	logger.Interface
	count *int
}

func (l countingLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.count != nil {
		(*l.count)++
	}
}

func itemIDs(items []SkillSummary) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func assertSearchIndexUnchanged(t *testing.T, db *gorm.DB, skillID, headRevisionID, content string) {
	t.Helper()
	var row skillSearchIndexRow
	if err := db.Where("skill_id = ?", skillID).Take(&row).Error; err != nil {
		t.Fatalf("query search index: %v", err)
	}
	if row.HeadRevisionID != headRevisionID || row.Content != content {
		t.Fatalf("search index = %#v, want head_revision_id %q content %q", row, headRevisionID, content)
	}
}

func newListSkillService(t *testing.T, db *gorm.DB) *SkillService {
	t.Helper()
	return NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})
}

func ensureSkillSearchIndexTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&skillSearchIndexRow{}); err != nil {
		t.Fatalf("auto migrate search index: %v", err)
	}
}

func seedSearchIndex(t *testing.T, db *gorm.DB, skillID, headRevisionID, content string) {
	t.Helper()
	if err := db.Create(&skillSearchIndexRow{
		SkillID:        skillID,
		OwnerUserID:    "user_001",
		HeadRevisionID: headRevisionID,
		Content:        content,
		UpdatedAt:      fixedClock().Now(),
	}).Error; err != nil {
		t.Fatalf("seed search index: %v", err)
	}
}

func setSkillMetadata(t *testing.T, db *gorm.DB, skillID, name, category, description, tags string) {
	t.Helper()
	if err := db.Model(&skillRow{}).Where("id = ?", skillID).Updates(map[string]any{
		"skill_name":  name,
		"category":    category,
		"description": description,
		"tags":        []byte(tags),
	}).Error; err != nil {
		t.Fatalf("update skill metadata: %v", err)
	}
}

func setHeadContent(t *testing.T, db *gorm.DB, revisionID, content string) {
	t.Helper()
	if err := db.Model(&skillBlobRow{}).
		Where("hash = ?", "h_skill_"+revisionID).
		Updates(map[string]any{"content": []byte(content), "size": len([]byte(content))}).Error; err != nil {
		t.Fatalf("update head content: %v", err)
	}
}
