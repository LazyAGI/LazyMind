package episode

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type postgresSearchAdapter struct {
	db *gorm.DB
}

func newPostgresSearchAdapter(db *gorm.DB) *postgresSearchAdapter {
	return &postgresSearchAdapter{db: db}
}

func (a *postgresSearchAdapter) Search(
	ctx context.Context,
	userID string,
	terms []string,
	limit int,
) ([]lexicalHit, error) {
	query, webSearchQuery := buildPostgresSearchStatement(terms)
	type searchRow struct {
		orm.EpisodeMemory
		LexicalScore float64 `gorm:"column:lexical_score"`
	}
	var rows []searchRow
	err := a.db.WithContext(ctx).Raw(
		query,
		webSearchQuery,
		webSearchQuery,
		userID,
		limit,
	).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("search postgres episodes: %w", err)
	}
	hits := make([]lexicalHit, 0, len(rows))
	for _, row := range rows {
		hits = append(hits, lexicalHit{Row: row.EpisodeMemory, Score: row.LexicalScore})
	}
	return hits, nil
}

func buildPostgresSearchStatement(terms []string) (string, string) {
	quotedTerms := make([]string, 0, len(terms))
	for _, term := range terms {
		quotedTerms = append(quotedTerms, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	const query = `
		SELECT e.*,
		       ts_rank_cd(e.search_vector, websearch_to_tsquery('simple', ?)) AS lexical_score
		FROM episode_memories AS e
		WHERE e.search_vector @@ websearch_to_tsquery('simple', ?)
		  AND e.user_id = ?
		ORDER BY lexical_score DESC, e.recorded_at_ms DESC, e.id ASC
		LIMIT ?
	`
	return query, strings.Join(quotedTerms, " OR ")
}
