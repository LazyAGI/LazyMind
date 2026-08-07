package episode

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

const postgresIntegrationDSNEnv = "EPISODE_POSTGRES_TEST_DSN"

func TestPostgresFTSCandidateContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(postgresIntegrationDSNEnv))
	if dsn == "" {
		t.Skip(postgresIntegrationDSNEnv + " is not configured")
	}

	db, err := orm.Connect(orm.DriverPostgres, dsn)
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("get PostgreSQL sql.DB: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	var databaseName string
	if err := db.Raw("SELECT current_database()").Scan(&databaseName).Error; err != nil {
		t.Fatalf("read PostgreSQL database name: %v", err)
	}
	if !strings.HasPrefix(databaseName, "episode_memory_test_") {
		t.Fatalf(
			"refusing destructive integration setup in database %q; expected episode_memory_test_*",
			databaseName,
		)
	}

	upMigration, downMigration := readPostgresEpisodeMigrations(t)
	if err := db.Exec(downMigration).Error; err != nil {
		t.Fatalf("clean PostgreSQL Episode schema: %v", err)
	}
	if err := db.Exec(upMigration).Error; err != nil {
		t.Fatalf("apply PostgreSQL Episode migration: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Exec(downMigration).Error; err != nil {
			t.Errorf("clean PostgreSQL Episode schema: %v", err)
		}
	})

	repo, err := NewRepository(db.DB)
	if err != nil {
		t.Fatalf("new PostgreSQL repository: %v", err)
	}
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
			mustCreateEpisode(t, repo, CreateInput{
				UserID:           testCase.userID + "-other",
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
				t.Fatalf(
					"lexical score = %v, want finite positive",
					items[0].LexicalScore,
				)
			}

			if err := repo.Delete(context.Background(), testCase.userID, created.ID); err != nil {
				t.Fatalf("delete indexed episode: %v", err)
			}
			items, err = repo.SearchCandidates(
				context.Background(),
				testCase.userID,
				testCase.terms,
				10,
			)
			if err != nil {
				t.Fatalf("search after delete: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("search after delete = %#v, want no candidates", items)
			}
		})
	}
}

func readPostgresEpisodeMigrations(t *testing.T) (string, string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve PostgreSQL integration test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "migrations")
	read := func(suffix string) string {
		t.Helper()
		matches, err := filepath.Glob(filepath.Join(migrationsDir, "*_"+suffix))
		if err != nil {
			t.Fatalf("find %s migration: %v", suffix, err)
		}
		if len(matches) != 1 {
			t.Fatalf("find %s migration: got %d matches, want 1", suffix, len(matches))
		}
		body, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatalf("read %s: %v", matches[0], err)
		}
		return string(body)
	}
	return read(
			"create_episode_memories.up.sql",
		), read(
			"create_episode_memories.down.sql",
		)
}
