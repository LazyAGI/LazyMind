package episode

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEpisodeMemoryPostgresMigrationContract(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "migrations")
	up, err := os.ReadFile(filepath.Join(
		migrationsDir,
		"20260724120000_create_episode_memories.up.sql",
	))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join(
		migrationsDir,
		"20260724120000_create_episode_memories.down.sql",
	))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	for _, token := range []string{
		"row_id BIGSERIAL PRIMARY KEY",
		"id VARCHAR(36) NOT NULL",
		"UNIQUE (user_id, id)",
		"UNIQUE (user_id, conversation_id, normalized_summary)",
		"setweight(to_tsvector('simple', COALESCE(search_text, '')), 'A')",
		"setweight(to_tsvector('simple', COALESCE(summary, '')), 'B')",
		"USING GIN (search_vector)",
		"(user_id, recorded_at_ms DESC, id DESC)",
		"(user_id, conversation_id, recorded_at_ms ASC, id ASC)",
	} {
		if !strings.Contains(string(up), token) {
			t.Fatalf("up migration missing %q", token)
		}
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS public.episode_memories") {
		t.Fatalf("unexpected down migration:\n%s", down)
	}
}
