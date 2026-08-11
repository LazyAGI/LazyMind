package episode

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestRepositoryCreateIsIdempotentForNormalizedSummary(t *testing.T) {
	repo := newSQLiteRepository(t)
	ctx := context.Background()
	input := CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeDecision,
		Summary:          "  ＰＲＯＪＥＣＴ   Alpha  ",
		SearchText:       "project alpha",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1_721_800_000_000,
	}

	first, err := repo.Create(ctx, input)
	if err != nil {
		t.Fatalf("create first episode: %v", err)
	}
	if first.Status != CreateStatusCreated {
		t.Fatalf("first status = %q, want %q", first.Status, CreateStatusCreated)
	}
	if !strings.HasPrefix(first.ID, "ep_") {
		t.Fatalf("id = %q, want ep_ prefix", first.ID)
	}

	input.Summary = "project alpha"
	second, err := repo.Create(ctx, input)
	if err != nil {
		t.Fatalf("create idempotent episode: %v", err)
	}
	if second.Status != CreateStatusIdempotent {
		t.Fatalf("second status = %q, want %q", second.Status, CreateStatusIdempotent)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent id = %q, want %q", second.ID, first.ID)
	}
}

func TestRepositoryCreateIsConcurrentlyIdempotent(t *testing.T) {
	repo := newSQLiteRepository(t)
	sqlDB, err := repo.db.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	const workers = 8
	results := make(chan CreateResult, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			summary := "Concurrent Episode"
			if index%2 == 1 {
				summary = "  ＣＯＮＣＵＲＲＥＮＴ   episode "
			}
			result, createErr := repo.Create(context.Background(), CreateInput{
				UserID:           "user-1",
				ConversationID:   "conversation-1",
				SourceKind:       SourceKindMemoryReview,
				EpisodeType:      EpisodeTypeDecision,
				Summary:          summary,
				SearchText:       "concurrent episode",
				TokenizerVersion: "jieba-v1",
				OccurredAtMS:     1_721_800_000_000,
			})
			results <- result
			errs <- createErr
		}(index)
	}
	wait.Wait()
	close(results)
	close(errs)

	for createErr := range errs {
		if createErr != nil {
			t.Fatalf("concurrent create: %v", createErr)
		}
	}
	var episodeID string
	for result := range results {
		if episodeID == "" {
			episodeID = result.ID
		}
		if result.ID != episodeID {
			t.Fatalf("concurrent result id = %q, want %q", result.ID, episodeID)
		}
	}
	items, err := repo.ListByConversation(context.Background(), "user-1", "conversation-1")
	if err != nil {
		t.Fatalf("list concurrent results: %v", err)
	}
	if len(items) != 1 || items[0].ID != episodeID {
		t.Fatalf("persisted concurrent episodes = %#v", items)
	}
}

func TestRepositoryReadsAreTenantScoped(t *testing.T) {
	repo := newSQLiteRepository(t)
	ctx := context.Background()
	userOne := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "shared-conversation",
		SourceKind:       SourceKindChatExplicit,
		EpisodeType:      EpisodeTypeProgress,
		Summary:          "User one progress",
		SearchText:       "user one progress",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1_721_800_000_000,
	})
	mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-2",
		ConversationID:   "shared-conversation",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeResult,
		Summary:          "User two result",
		SearchText:       "user two result",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1_721_800_100_000,
	})

	items, err := repo.ListByConversation(ctx, "user-1", "shared-conversation")
	if err != nil {
		t.Fatalf("list by conversation: %v", err)
	}
	if len(items) != 1 || items[0].ID != userOne.ID {
		t.Fatalf("tenant list = %#v, want only %q", items, userOne.ID)
	}
	if _, err := repo.Get(ctx, "user-2", userOne.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant get error = %v, want ErrNotFound", err)
	}
}

func TestRepositoryListRecentFiltersTypeAndUsesStableEventTimeOrdering(t *testing.T) {
	repo := newSQLiteRepository(t)
	repo.clockMS = func() int64 { return 5_000 }
	var sequence int
	repo.newID = func() string {
		sequence++
		return fmt.Sprintf("ep_%032d", sequence)
	}
	create := func(userID, episodeType, summary string, occurredAtMS int64) CreateResult {
		return mustCreateEpisode(t, repo, CreateInput{
			UserID:           userID,
			ConversationID:   "conversation-" + userID,
			SourceKind:       SourceKindMemoryReview,
			EpisodeType:      episodeType,
			Summary:          summary,
			SearchText:       summary,
			TokenizerVersion: "jieba-v1",
			OccurredAtMS:     occurredAtMS,
		})
	}
	create("user-1", EpisodeTypeProgress, "oldest", 100)
	firstTie := create("user-1", EpisodeTypeProgress, "first tie", 300)
	secondTie := create("user-1", EpisodeTypeProgress, "second tie", 300)
	third := create("user-1", EpisodeTypeProgress, "third", 200)
	create("user-1", EpisodeTypeResult, "newer result", 500)
	create("user-2", EpisodeTypeProgress, "other user progress", 600)

	items, err := repo.ListRecent(
		context.Background(),
		"user-1",
		EpisodeTypeProgress,
		3,
	)
	if err != nil {
		t.Fatalf("list recent progress: %v", err)
	}
	wantIDs := []string{secondTie.ID, firstTie.ID, third.ID}
	if len(items) != len(wantIDs) {
		t.Fatalf("recent progress = %#v, want IDs %#v", items, wantIDs)
	}
	for index, wantID := range wantIDs {
		if items[index].ID != wantID {
			t.Fatalf("recent progress[%d] = %q, want %q", index, items[index].ID, wantID)
		}
		if items[index].UserID != "user-1" ||
			items[index].EpisodeType != EpisodeTypeProgress {
			t.Fatalf("recent progress[%d] escaped scope: %#v", index, items[index])
		}
	}
}

func TestRepositoryListRecentValidatesArguments(t *testing.T) {
	repo := newSQLiteRepository(t)
	for _, testCase := range []struct {
		name        string
		userID      string
		episodeType string
		limit       int
	}{
		{name: "missing user", episodeType: EpisodeTypeProgress, limit: 3},
		{name: "invalid type", userID: "user-1", episodeType: "unknown", limit: 3},
		{name: "limit too small", userID: "user-1", episodeType: EpisodeTypeProgress, limit: 0},
		{name: "limit too large", userID: "user-1", episodeType: EpisodeTypeProgress, limit: 101},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := repo.ListRecent(
				context.Background(),
				testCase.userID,
				testCase.episodeType,
				testCase.limit,
			); err == nil {
				t.Fatal("invalid recent list arguments unexpectedly accepted")
			}
		})
	}
}

func TestRepositoryDeleteIsTenantScoped(t *testing.T) {
	repo := newSQLiteRepository(t)
	ctx := context.Background()
	created := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindChatExplicit,
		EpisodeType:      EpisodeTypeEvent,
		Summary:          "Delete only for the owner",
		SearchText:       "delete owner",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1_721_800_000_000,
	})

	if err := repo.Delete(ctx, "user-2", created.ID); err != ErrNotFound {
		t.Fatalf("cross-tenant delete error = %v, want ErrNotFound", err)
	}
	if _, err := repo.Get(ctx, "user-1", created.ID); err != nil {
		t.Fatalf("owner episode disappeared after cross-tenant delete: %v", err)
	}
	if err := repo.Delete(ctx, "user-1", created.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := repo.Get(ctx, "user-1", created.ID); err != ErrNotFound {
		t.Fatalf("get after delete error = %v, want ErrNotFound", err)
	}
}

func TestRepositoryRecordHitsIsTenantScopedAndDeduplicated(t *testing.T) {
	repo := newSQLiteRepository(t)
	ctx := context.Background()
	created := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeBlocker,
		Summary:          "A remembered blocker",
		SearchText:       "remembered blocker",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1_721_800_000_000,
	})

	results, err := repo.RecordHits(ctx, "user-1", []string{created.ID, created.ID, "missing"})
	if err != nil {
		t.Fatalf("record hits: %v", err)
	}
	if !results[created.ID] || results["missing"] {
		t.Fatalf("record hit results = %#v", results)
	}
	record, err := repo.Get(ctx, "user-1", created.ID)
	if err != nil {
		t.Fatalf("get hit episode: %v", err)
	}
	if record.HitCount != 1 {
		t.Fatalf("hit_count = %d, want 1", record.HitCount)
	}

	crossTenant, err := repo.RecordHits(ctx, "user-2", []string{created.ID})
	if err != nil {
		t.Fatalf("cross-tenant hits: %v", err)
	}
	if crossTenant[created.ID] {
		t.Fatalf("cross-tenant hit unexpectedly succeeded: %#v", crossTenant)
	}
}

func TestRepositoryListUsesStableCreationTimePagination(t *testing.T) {
	repo := newSQLiteRepository(t)
	var clock int64 = 1000
	var sequence int
	repo.clockMS = func() int64 {
		clock += 100
		return clock
	}
	repo.newID = func() string {
		sequence++
		return fmt.Sprintf("ep_%032d", sequence)
	}
	for index, summary := range []string{"first", "second", "third"} {
		mustCreateEpisode(t, repo, CreateInput{
			UserID:           "user-1",
			ConversationID:   "conversation-1",
			SourceKind:       SourceKindChatExplicit,
			EpisodeType:      EpisodeTypeEvent,
			Summary:          summary,
			SearchText:       summary,
			TokenizerVersion: "jieba-v1",
			OccurredAtMS:     int64(index + 1),
		})
	}

	firstPage, err := repo.List(context.Background(), "user-1", 2, "")
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if firstPage.TotalSize != 3 || len(firstPage.Items) != 2 {
		t.Fatalf("first page = %#v", firstPage)
	}
	if firstPage.Items[0].Summary != "third" || firstPage.Items[1].Summary != "second" {
		t.Fatalf("first page order = %#v", firstPage.Items)
	}
	if firstPage.NextPageToken == "" {
		t.Fatal("first page next_page_token is empty")
	}

	secondPage, err := repo.List(context.Background(), "user-1", 2, firstPage.NextPageToken)
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(secondPage.Items) != 1 || secondPage.Items[0].Summary != "first" {
		t.Fatalf("second page = %#v", secondPage)
	}
	if secondPage.NextPageToken != "" {
		t.Fatalf("second page next_page_token = %q, want empty", secondPage.NextPageToken)
	}
}

func TestRepositorySearchCandidatesUsesSQLiteFTSAndTenantFilter(t *testing.T) {
	repo := newSQLiteRepository(t)
	ctx := context.Background()
	match := mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-1",
		SourceKind:       SourceKindChatExplicit,
		EpisodeType:      EpisodeTypeDecision,
		Summary:          "Use PostgreSQL migration rollback",
		SearchText:       "use postgresql migration rollback",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     1000,
	})
	mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-1",
		ConversationID:   "conversation-2",
		SourceKind:       SourceKindMemoryReview,
		EpisodeType:      EpisodeTypeResult,
		Summary:          "Frontend card was completed",
		SearchText:       "frontend card completed",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     2000,
	})
	mustCreateEpisode(t, repo, CreateInput{
		UserID:           "user-2",
		ConversationID:   "conversation-3",
		SourceKind:       SourceKindChatExplicit,
		EpisodeType:      EpisodeTypeDecision,
		Summary:          "Another PostgreSQL rollback",
		SearchText:       "postgresql rollback",
		TokenizerVersion: "jieba-v1",
		OccurredAtMS:     3000,
	})

	items, err := repo.SearchCandidates(ctx, "user-1", []string{"postgresql", "rollback"}, 10)
	if err != nil {
		t.Fatalf("search candidates: %v", err)
	}
	if len(items) != 1 || items[0].Episode.ID != match.ID {
		t.Fatalf("search candidates = %#v, want only %q", items, match.ID)
	}
	if math.IsNaN(items[0].LexicalScore) || math.IsInf(items[0].LexicalScore, 0) || items[0].LexicalScore <= 0 {
		t.Fatalf("lexical_score = %v, want finite positive score", items[0].LexicalScore)
	}

	if err := repo.Delete(ctx, "user-1", match.ID); err != nil {
		t.Fatalf("delete indexed episode: %v", err)
	}
	items, err = repo.SearchCandidates(ctx, "user-1", []string{"postgresql"}, 10)
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("search after delete = %#v, want no candidates", items)
	}
}

func TestSQLiteFTSCandidateContract(t *testing.T) {
	repo := newSQLiteRepository(t)
	testCases := []struct {
		name       string
		userID     string
		summary    string
		searchText string
		terms      []string
	}{
		{
			name:       "chinese tokens",
			userID:     "user-cn",
			summary:    "数据库迁移需要回滚",
			searchText: "数据库 迁移 回滚",
			terms:      []string{"数据库", "回滚"},
		},
		{
			name:       "multiple terms use any matching",
			userID:     "user-multi",
			summary:    "Alpha beta decision",
			searchText: "alpha beta decision",
			terms:      []string{"missing", "beta"},
		},
		{
			name:       "identifier tokens",
			userID:     "user-identifier",
			summary:    "ERR-42 was resolved",
			searchText: "err 42 resolved",
			terms:      []string{"err", "42"},
		},
		{
			name:       "hyphenated numeric tokens",
			userID:     "user-numeric",
			summary:    "Ticket 123-456 was closed",
			searchText: "ticket 123 456 closed",
			terms:      []string{"123", "456"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			created := mustCreateEpisode(t, repo, CreateInput{
				UserID:           testCase.userID,
				ConversationID:   "conversation-" + testCase.userID,
				SourceKind:       SourceKindMemoryReview,
				EpisodeType:      EpisodeTypeResult,
				Summary:          testCase.summary,
				SearchText:       testCase.searchText,
				TokenizerVersion: "jieba-v1",
				OccurredAtMS:     1000,
			})
			items, err := repo.SearchCandidates(
				context.Background(),
				testCase.userID,
				testCase.terms,
				10,
			)
			if err != nil {
				t.Fatalf("search candidates: %v", err)
			}
			if len(items) != 1 || items[0].Episode.ID != created.ID {
				t.Fatalf("items = %#v, want only %q", items, created.ID)
			}
			if items[0].LexicalScore <= 0 ||
				math.IsNaN(items[0].LexicalScore) ||
				math.IsInf(items[0].LexicalScore, 0) {
				t.Fatalf("lexical score = %v, want finite positive", items[0].LexicalScore)
			}
		})
	}
}

func TestSQLiteFTSInitializeRepairsCompleteButUnbuiltIndex(t *testing.T) {
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.EpisodeMemory{}); err != nil {
		t.Fatalf("migrate episode memory: %v", err)
	}
	row := orm.EpisodeMemory{
		ID:                "ep_partial_initialization",
		UserID:            "user-1",
		ConversationID:    "conversation-1",
		SourceKind:        SourceKindMemoryReview,
		EpisodeType:       EpisodeTypeResult,
		Summary:           "SQLite initialization repaired the missing index",
		NormalizedSummary: "sqlite initialization repaired the missing index",
		SearchText:        "sqlite initialization repaired missing index",
		TokenizerVersion:  "jieba-v1",
		OccurredAtMS:      1000,
		RecordedAtMS:      2000,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed authoritative episode: %v", err)
	}

	// Simulate an interrupted pre-v1 initialization: the virtual table and all
	// triggers exist, but rows that predate them were never rebuilt into FTS.
	for _, query := range sqliteEpisodeFTSSchemaQueries {
		if err := db.Exec(query).Error; err != nil {
			t.Fatalf("create partial sqlite FTS schema: %v", err)
		}
	}
	if err := Initialize(db); err != nil {
		t.Fatalf("repair sqlite FTS initialization: %v", err)
	}
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	items, err := repo.SearchCandidates(
		context.Background(),
		"user-1",
		[]string{"initialization"},
		10,
	)
	if err != nil {
		t.Fatalf("search repaired sqlite FTS index: %v", err)
	}
	if len(items) != 1 || items[0].Episode.ID != row.ID {
		t.Fatalf("repaired search items = %#v, want %q", items, row.ID)
	}
}

func TestSQLiteEpisodeSchemaKeepsFTSRowIDAndOrderingIndexes(t *testing.T) {
	repo := newSQLiteRepository(t)
	type schemaRow struct {
		SQL string `gorm:"column:sql"`
	}
	for name, required := range map[string][]string{
		"idx_episode_memories_user_recorded": {
			"`user_id`",
			"`recorded_at_ms` desc",
			"`id` desc",
		},
		"idx_episode_memories_user_conversation_recorded": {
			"`user_id`",
			"`conversation_id`",
			"`recorded_at_ms`",
			"`id`",
		},
	} {
		var row schemaRow
		if err := repo.db.Raw(
			"SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?",
			name,
		).Scan(&row).Error; err != nil {
			t.Fatalf("inspect index %s: %v", name, err)
		}
		lowerSQL := strings.ToLower(row.SQL)
		for _, token := range required {
			if !strings.Contains(lowerSQL, token) {
				t.Fatalf("index %s SQL %q missing %q", name, row.SQL, token)
			}
		}
	}

	var primaryKeyColumns int64
	if err := repo.db.Raw(
		"SELECT COUNT(*) FROM pragma_table_info('episode_memories') WHERE name = 'row_id' AND pk = 1",
	).Scan(&primaryKeyColumns).Error; err != nil {
		t.Fatalf("inspect row_id primary key: %v", err)
	}
	if primaryKeyColumns != 1 {
		t.Fatalf("row_id primary key count = %d, want 1", primaryKeyColumns)
	}
}

func mustCreateEpisode(t *testing.T, repo *Repository, input CreateInput) CreateResult {
	t.Helper()
	result, err := repo.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	return result
}

func newSQLiteRepository(t *testing.T) *Repository {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.EpisodeMemory{}); err != nil {
		t.Fatalf("migrate episode memory: %v", err)
	}
	if err := Initialize(db); err != nil {
		t.Fatalf("initialize episode search: %v", err)
	}
	repo, err := NewRepository(db)
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	return repo
}
