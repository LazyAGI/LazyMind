package episode

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

const sqliteEpisodeFTSTable = "episode_memories_fts"

var sqliteEpisodeFTSTriggerNames = []string{
	"episode_memories_fts_ai",
	"episode_memories_fts_ad",
	"episode_memories_fts_au",
}

var sqliteEpisodeFTSSchemaQueries = []string{
	`CREATE VIRTUAL TABLE IF NOT EXISTS episode_memories_fts USING fts5(
		search_text,
		content='episode_memories',
		content_rowid='row_id',
		tokenize='unicode61'
	)`,
	`CREATE TRIGGER IF NOT EXISTS episode_memories_fts_ai AFTER INSERT ON episode_memories BEGIN
		INSERT INTO episode_memories_fts(rowid, search_text) VALUES (new.row_id, new.search_text);
	END`,
	`CREATE TRIGGER IF NOT EXISTS episode_memories_fts_ad AFTER DELETE ON episode_memories BEGIN
		INSERT INTO episode_memories_fts(episode_memories_fts, rowid, search_text)
		VALUES ('delete', old.row_id, old.search_text);
	END`,
	`CREATE TRIGGER IF NOT EXISTS episode_memories_fts_au AFTER UPDATE OF search_text ON episode_memories BEGIN
		INSERT INTO episode_memories_fts(episode_memories_fts, rowid, search_text)
		VALUES ('delete', old.row_id, old.search_text);
		INSERT INTO episode_memories_fts(rowid, search_text) VALUES (new.row_id, new.search_text);
	END`,
}

type sqliteSearchAdapter struct {
	db *gorm.DB
}

func newSQLiteSearchAdapter(db *gorm.DB) *sqliteSearchAdapter {
	return &sqliteSearchAdapter{db: db}
}

func ensureSQLiteSearchSchema(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var tableCount int64
		if err := tx.Raw(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			sqliteEpisodeFTSTable,
		).Scan(&tableCount).Error; err != nil {
			return fmt.Errorf("inspect sqlite episode FTS schema: %w", err)
		}

		var triggerCount int64
		if err := tx.Raw(
			`SELECT COUNT(*) FROM sqlite_master
			 WHERE type = 'trigger' AND name IN (?, ?, ?)`,
			sqliteEpisodeFTSTriggerNames[0],
			sqliteEpisodeFTSTriggerNames[1],
			sqliteEpisodeFTSTriggerNames[2],
		).Scan(&triggerCount).Error; err != nil {
			return fmt.Errorf("inspect sqlite episode FTS triggers: %w", err)
		}
		needsRebuild := tableCount == 0 ||
			triggerCount != int64(len(sqliteEpisodeFTSTriggerNames))

		for _, query := range sqliteEpisodeFTSSchemaQueries {
			if err := tx.Exec(query).Error; err != nil {
				return fmt.Errorf("initialize sqlite episode FTS schema: %w", err)
			}
		}

		var missingRows int64
		if err := tx.Raw(`
			SELECT COUNT(*)
			FROM episode_memories AS e
			LEFT JOIN episode_memories_fts_docsize AS f ON f.id = e.row_id
			WHERE f.id IS NULL
		`).Scan(&missingRows).Error; err != nil {
			return fmt.Errorf("inspect sqlite episode FTS missing rows: %w", err)
		}
		var staleRows int64
		if err := tx.Raw(`
			SELECT COUNT(*)
			FROM episode_memories_fts_docsize AS f
			LEFT JOIN episode_memories AS e ON e.row_id = f.id
			WHERE e.row_id IS NULL
		`).Scan(&staleRows).Error; err != nil {
			return fmt.Errorf("inspect sqlite episode FTS stale rows: %w", err)
		}
		needsRebuild = needsRebuild || missingRows > 0 || staleRows > 0

		if needsRebuild {
			if err := tx.Exec(
				"INSERT INTO episode_memories_fts(episode_memories_fts) VALUES ('rebuild')",
			).Error; err != nil {
				return fmt.Errorf("rebuild sqlite episode FTS index: %w", err)
			}
		}
		return nil
	})
}

func (a *sqliteSearchAdapter) Search(
	ctx context.Context,
	userID string,
	terms []string,
	limit int,
) ([]lexicalHit, error) {
	match := make([]string, 0, len(terms))
	for _, term := range terms {
		match = append(match, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	type searchRow struct {
		orm.EpisodeMemory
		LexicalScore float64 `gorm:"column:lexical_score"`
	}
	var rows []searchRow
	err := a.db.WithContext(ctx).Raw(`
		SELECT e.*, -bm25(episode_memories_fts) AS lexical_score
		FROM episode_memories_fts
		JOIN episode_memories AS e ON e.row_id = episode_memories_fts.rowid
		WHERE episode_memories_fts MATCH ? AND e.user_id = ?
		ORDER BY lexical_score DESC, e.recorded_at_ms DESC, e.id ASC
		LIMIT ?
	`, strings.Join(match, " OR "), userID, limit).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("search sqlite episode FTS: %w", err)
	}
	hits := make([]lexicalHit, 0, len(rows))
	for _, row := range rows {
		hits = append(hits, lexicalHit{Row: row.EpisodeMemory, Score: row.LexicalScore})
	}
	return hits, nil
}
