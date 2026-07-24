package episode

import (
	"strings"
	"testing"
)

func TestPostgresSearchStatementIsTenantScopedAndRanksDescending(t *testing.T) {
	query, webSearchQuery := buildPostgresSearchStatement([]string{"postgresql", `release "42"`})

	for _, token := range []string{
		"ts_rank_cd",
		"websearch_to_tsquery('simple', ?)",
		"e.search_vector @@ websearch_to_tsquery('simple', ?)",
		"e.user_id = ?",
		"ORDER BY lexical_score DESC, e.recorded_at_ms DESC, e.id ASC",
		"LIMIT ?",
	} {
		if !strings.Contains(query, token) {
			t.Fatalf("postgres search query missing %q:\n%s", token, query)
		}
	}
	if webSearchQuery != `"postgresql" OR "release ""42"""` {
		t.Fatalf("web search query = %q", webSearchQuery)
	}
}
