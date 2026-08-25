package service

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestListSkillsKeywordTreatsSpecialCharactersLiterally(t *testing.T) {
	for _, tc := range []struct {
		name     string
		keyword  string
		similar  string
		expected []string
	}{
		{name: "underscore", keyword: "a_b", similar: "axb"},
		{name: "percent", keyword: "a%b", similar: "axxb"},
		{name: "single backslash", keyword: `a\b`, similar: "ab"},
		{name: "double backslash", keyword: `a\\b`, similar: `a\b`},
		{name: "escape marker", keyword: "a!b", similar: "aXb"},
		{name: "unicode and english", keyword: "普通Keyword", similar: "普通Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			ensureSkillSearchIndexTable(t, db)
			for _, fixture := range []struct {
				skillID    string
				revisionID string
				name       string
				tags       []string
				content    string
				indexRev   string
				indexText  string
			}{
				{skillID: "skill-meta", revisionID: "rev-meta", name: "metadata " + tc.keyword, tags: []string{"plain"}, content: "plain head", indexRev: "rev-meta", indexText: "plain index"},
				{skillID: "skill-tag", revisionID: "rev-tag", name: "tag hit", tags: []string{tc.keyword}, content: "plain head", indexRev: "rev-tag", indexText: "plain index"},
				{skillID: "skill-index", revisionID: "rev-index", name: "index hit", tags: []string{"plain"}, content: "plain head", indexRev: "rev-index", indexText: "fresh " + tc.keyword},
				{skillID: "skill-missing", revisionID: "rev-missing", name: "missing fallback", tags: []string{"plain"}, content: "missing " + tc.keyword},
				{skillID: "skill-stale", revisionID: "rev-stale", name: "stale fallback", tags: []string{"plain"}, content: "stale " + tc.keyword, indexRev: "old-rev-stale", indexText: "stale " + tc.similar},
				{skillID: "skill-similar", revisionID: "rev-similar", name: "similar " + tc.similar, tags: []string{tc.similar}, content: "similar " + tc.similar, indexRev: "rev-similar", indexText: "similar " + tc.similar},
			} {
				seedSkillWithHeadRevision(t, db, fixture.skillID, fixture.revisionID)
				setSkillMetadata(t, db, fixture.skillID, fixture.name, "writing", "daily notes", jsonTags(t, fixture.tags...))
				setHeadContent(t, db, fixture.revisionID, fixture.content)
				if fixture.indexRev != "" {
					seedSearchIndex(t, db, fixture.skillID, fixture.indexRev, fixture.indexText)
				}
			}

			got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{
				UserID:  "user_001",
				Keyword: tc.keyword,
				Limit:   2,
			})
			if err != nil {
				t.Fatalf("ListSkills returned error: %v", err)
			}
			if got.Total != 5 {
				t.Fatalf("Total = %d, want 5", got.Total)
			}
			if len(got.Items) != 2 {
				t.Fatalf("Items len = %d, want paginated 2", len(got.Items))
			}
			if len(got.Items) >= int(got.Total) {
				t.Fatalf("pagination did not reduce returned items: %#v", got)
			}

			all, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{
				UserID:  "user_001",
				Keyword: tc.keyword,
				Limit:   10,
			})
			if err != nil {
				t.Fatalf("ListSkills all returned error: %v", err)
			}
			gotIDs := itemIDs(all.Items)
			wantIDs := []string{"skill-tag", "skill-stale", "skill-missing", "skill-meta", "skill-index"}
			if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
				t.Fatalf("item IDs = %v, want literal matches %v", gotIDs, wantIDs)
			}
		})
	}
}

func TestListSkillsTagsTreatSpecialCharactersLiterally(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tag     string
		similar string
	}{
		{name: "underscore", tag: "a_b", similar: "axb"},
		{name: "percent", tag: "a%b", similar: "axxb"},
		{name: "single backslash", tag: `a\b`, similar: "ab"},
		{name: "double backslash", tag: `a\\b`, similar: `a\b`},
		{name: "escape marker", tag: "a!b", similar: "aXb"},
		{name: "unicode and english", tag: "普通Keyword", similar: "普通Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newSkillV2TestDB(t)
			seedSkillWithHeadRevision(t, db, "skill-hit", "rev-hit")
			setSkillMetadata(t, db, "skill-hit", "Planner", "writing", "daily notes", jsonTags(t, tc.tag))
			seedSkillWithHeadRevision(t, db, "skill-similar", "rev-similar")
			setSkillMetadata(t, db, "skill-similar", "Planner similar", "writing", "daily notes", jsonTags(t, tc.similar))

			got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{
				UserID: "user_001",
				Tags:   []string{tc.tag},
				Limit:  10,
			})
			if err != nil {
				t.Fatalf("ListSkills returned error: %v", err)
			}
			if got.Total != 1 || len(got.Items) != 1 || got.Items[0].ID != "skill-hit" {
				t.Fatalf("result = %#v, want only literal tag match", got)
			}
		})
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

func TestListSkillsStableSortUsesExplicitSkillID(t *testing.T) {
	db := newSkillV2TestDB(t)
	ensureSkillSearchIndexTable(t, db)
	for _, item := range []struct {
		skillID    string
		revisionID string
	}{
		{skillID: "skill-m", revisionID: "rev-m"},
		{skillID: "skill-z", revisionID: "rev-z"},
		{skillID: "skill-a", revisionID: "rev-a"},
	} {
		seedSkillWithHeadRevision(t, db, item.skillID, item.revisionID)
		setSkillMetadata(t, db, item.skillID, "Planner "+item.skillID, "writing", "daily notes", `["team"]`)
		seedSearchIndex(t, db, item.skillID, item.revisionID, "needle")
	}

	got, err := newListSkillService(t, db).ListSkills(context.Background(), ListSkillsRequest{
		UserID:  "user_001",
		Keyword: "needle",
	})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	gotIDs := itemIDs(got.Items)
	wantIDs := []string{"skill-z", "skill-m", "skill-a"}
	if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
		t.Fatalf("item IDs = %v, want explicit skills.id DESC order %v", gotIDs, wantIDs)
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

func jsonTags(t *testing.T, tags ...string) string {
	t.Helper()
	raw, err := json.Marshal(tags)
	if err != nil {
		t.Fatalf("marshal tags: %v", err)
	}
	return string(raw)
}

func setHeadContent(t *testing.T, db *gorm.DB, revisionID, content string) {
	t.Helper()
	if err := db.Model(&skillBlobRow{}).
		Where("hash = ?", "h_skill_"+revisionID).
		Updates(map[string]any{"content": []byte(content), "size": len([]byte(content))}).Error; err != nil {
		t.Fatalf("update head content: %v", err)
	}
}
