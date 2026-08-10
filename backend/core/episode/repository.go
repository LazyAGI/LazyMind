package episode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

type Repository struct {
	db      *gorm.DB
	search  SearchAdapter
	clockMS func() int64
	newID   func() string
}

func NewRepository(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, fmt.Errorf("episode repository requires a database")
	}
	var search SearchAdapter
	switch db.Dialector.Name() {
	case "sqlite":
		search = newSQLiteSearchAdapter(db)
	case "postgres":
		search = newPostgresSearchAdapter(db)
	default:
		search = unavailableSearchAdapter{dialect: db.Dialector.Name()}
	}
	return &Repository{
		db:      db,
		search:  search,
		clockMS: func() int64 { return time.Now().UTC().UnixMilli() },
		newID:   func() string { return common.GeneratePrefixedID("ep_", 36) },
	}, nil
}

// Initialize installs dialect-specific search support after the authoritative
// schema has been created. It is called once during Core startup; repositories
// remain lightweight request-scoped values.
func Initialize(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("episode initialization requires a database")
	}
	if db.Dialector.Name() == "sqlite" {
		return ensureSQLiteSearchSchema(db)
	}
	return nil
}

func (r *Repository) Create(ctx context.Context, input CreateInput) (CreateResult, error) {
	prepared, err := prepareCreateInput(input)
	if err != nil {
		return CreateResult{}, err
	}
	row := orm.EpisodeMemory{
		ID:                r.newID(),
		UserID:            prepared.UserID,
		ConversationID:    prepared.ConversationID,
		SourceKind:        prepared.SourceKind,
		EpisodeType:       prepared.EpisodeType,
		Summary:           prepared.Summary,
		NormalizedSummary: normalizeSummary(prepared.Summary),
		SearchText:        prepared.SearchText,
		TokenizerVersion:  prepared.TokenizerVersion,
		OccurredAtMS:      prepared.OccurredAtMS,
		RecordedAtMS:      r.clockMS(),
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "conversation_id"},
			{Name: "normalized_summary"},
		},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return CreateResult{}, fmt.Errorf("create episode: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		return CreateResult{Status: CreateStatusCreated, ID: row.ID}, nil
	}

	var existing orm.EpisodeMemory
	if err := r.db.WithContext(ctx).
		Where(
			"user_id = ? AND conversation_id = ? AND normalized_summary = ?",
			row.UserID,
			row.ConversationID,
			row.NormalizedSummary,
		).
		Take(&existing).Error; err != nil {
		return CreateResult{}, fmt.Errorf("load idempotent episode: %w", err)
	}
	return CreateResult{Status: CreateStatusIdempotent, ID: existing.ID}, nil
}

func (r *Repository) Get(ctx context.Context, userID, episodeID string) (Episode, error) {
	userID = strings.TrimSpace(userID)
	episodeID = strings.TrimSpace(episodeID)
	if userID == "" || episodeID == "" {
		return Episode{}, ErrNotFound
	}
	var row orm.EpisodeMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND id = ?", userID, episodeID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Episode{}, ErrNotFound
	}
	if err != nil {
		return Episode{}, fmt.Errorf("get episode: %w", err)
	}
	return episodeFromRow(row), nil
}

func (r *Repository) ListByConversation(
	ctx context.Context,
	userID string,
	conversationID string,
) ([]Episode, error) {
	userID = strings.TrimSpace(userID)
	conversationID = strings.TrimSpace(conversationID)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if conversationID == "" {
		return nil, fmt.Errorf("conversation_id is required")
	}
	var rows []orm.EpisodeMemory
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND conversation_id = ?", userID, conversationID).
		Order("recorded_at_ms ASC").
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list conversation episodes: %w", err)
	}
	items := make([]Episode, 0, len(rows))
	for _, row := range rows {
		items = append(items, episodeFromRow(row))
	}
	return items, nil
}

func (r *Repository) ListRecent(
	ctx context.Context,
	userID string,
	episodeType string,
	limit int,
) ([]Episode, error) {
	userID = strings.TrimSpace(userID)
	episodeType = strings.TrimSpace(episodeType)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if !validEpisodeType(episodeType) {
		return nil, fmt.Errorf("episode_type is invalid")
	}
	if limit < 1 || limit > MaxRecentLimit {
		return nil, fmt.Errorf("limit must be between 1 and %d", MaxRecentLimit)
	}
	var rows []orm.EpisodeMemory
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND episode_type = ?", userID, episodeType).
		Order("occurred_at_ms DESC").
		Order("recorded_at_ms DESC").
		Order("id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list recent episodes: %w", err)
	}
	items := make([]Episode, 0, len(rows))
	for _, row := range rows {
		items = append(items, episodeFromRow(row))
	}
	return items, nil
}

func (r *Repository) List(
	ctx context.Context,
	userID string,
	pageSize int,
	pageToken string,
) (Page, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return Page{}, fmt.Errorf("user_id is required")
	}
	if pageSize < 1 || pageSize > 100 {
		return Page{}, fmt.Errorf("page_size must be between 1 and 100")
	}
	cursor, err := decodePageCursor(pageToken)
	if err != nil {
		return Page{}, err
	}

	query := r.db.WithContext(ctx).Where("user_id = ?", userID)
	var total int64
	if err := query.Model(&orm.EpisodeMemory{}).Count(&total).Error; err != nil {
		return Page{}, fmt.Errorf("count episodes: %w", err)
	}
	if cursor != nil {
		query = query.Where(
			"(recorded_at_ms < ?) OR (recorded_at_ms = ? AND id < ?)",
			cursor.RecordedAtMS,
			cursor.RecordedAtMS,
			cursor.ID,
		)
	}
	var rows []orm.EpisodeMemory
	if err := query.
		Order("recorded_at_ms DESC").
		Order("id DESC").
		Limit(pageSize + 1).
		Find(&rows).Error; err != nil {
		return Page{}, fmt.Errorf("list episodes: %w", err)
	}
	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}
	items := make([]Episode, 0, len(rows))
	for _, row := range rows {
		items = append(items, episodeFromRow(row))
	}
	nextPageToken := ""
	if hasMore {
		last := rows[len(rows)-1]
		nextPageToken, err = encodePageCursor(pageCursor{
			RecordedAtMS: last.RecordedAtMS,
			ID:           last.ID,
		})
		if err != nil {
			return Page{}, err
		}
	}
	return Page{
		Items:         items,
		TotalSize:     total,
		NextPageToken: nextPageToken,
	}, nil
}

func (r *Repository) Delete(ctx context.Context, userID, episodeID string) error {
	userID = strings.TrimSpace(userID)
	episodeID = strings.TrimSpace(episodeID)
	if userID == "" || episodeID == "" {
		return ErrNotFound
	}
	result := r.db.WithContext(ctx).
		Where("user_id = ? AND id = ?", userID, episodeID).
		Delete(&orm.EpisodeMemory{})
	if result.Error != nil {
		return fmt.Errorf("delete episode: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repository) RecordHits(
	ctx context.Context,
	userID string,
	episodeIDs []string,
) (map[string]bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	results := make(map[string]bool)
	uniqueIDs := make([]string, 0, len(episodeIDs))
	for _, rawID := range episodeIDs {
		episodeID := strings.TrimSpace(rawID)
		if episodeID == "" {
			continue
		}
		if _, exists := results[episodeID]; exists {
			continue
		}
		results[episodeID] = false
		uniqueIDs = append(uniqueIDs, episodeID)
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, episodeID := range uniqueIDs {
			result := tx.Model(&orm.EpisodeMemory{}).
				Where("user_id = ? AND id = ?", userID, episodeID).
				UpdateColumn("hit_count", gorm.Expr("hit_count + ?", 1))
			if result.Error != nil {
				return result.Error
			}
			results[episodeID] = result.RowsAffected == 1
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("record episode hits: %w", err)
	}
	return results, nil
}

func (r *Repository) SearchCandidates(
	ctx context.Context,
	userID string,
	terms []string,
	limit int,
) ([]SearchCandidate, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if limit < 1 || limit > MaxCandidateLimit {
		return nil, fmt.Errorf("limit must be between 1 and %d", MaxCandidateLimit)
	}
	normalizedTerms := make([]string, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, rawTerm := range terms {
		term := strings.TrimSpace(rawTerm)
		if term == "" {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		normalizedTerms = append(normalizedTerms, term)
	}
	if len(normalizedTerms) == 0 {
		return []SearchCandidate{}, nil
	}
	hits, err := r.search.Search(ctx, userID, normalizedTerms, limit)
	if err != nil {
		return nil, err
	}
	items := make([]SearchCandidate, 0, len(hits))
	for _, hit := range hits {
		if math.IsNaN(hit.Score) || math.IsInf(hit.Score, 0) || hit.Score <= 0 {
			continue
		}
		items = append(items, SearchCandidate{
			Episode:      episodeFromRow(hit.Row),
			LexicalScore: hit.Score,
		})
	}
	return items, nil
}

type pageCursor struct {
	RecordedAtMS int64  `json:"recorded_at_ms"`
	ID           string `json:"id"`
}

func encodePageCursor(cursor pageCursor) (string, error) {
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func decodePageCursor(token string) (*pageCursor, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	body, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("invalid page_token")
	}
	var cursor pageCursor
	if err := json.Unmarshal(body, &cursor); err != nil || cursor.RecordedAtMS <= 0 || strings.TrimSpace(cursor.ID) == "" {
		return nil, fmt.Errorf("invalid page_token")
	}
	return &cursor, nil
}

func episodeFromRow(row orm.EpisodeMemory) Episode {
	return Episode{
		ID:             row.ID,
		UserID:         row.UserID,
		ConversationID: row.ConversationID,
		SourceKind:     row.SourceKind,
		EpisodeType:    row.EpisodeType,
		Summary:        row.Summary,
		OccurredAtMS:   row.OccurredAtMS,
		RecordedAtMS:   row.RecordedAtMS,
		HitCount:       row.HitCount,
	}
}

func prepareCreateInput(input CreateInput) (CreateInput, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	input.EpisodeType = strings.TrimSpace(input.EpisodeType)
	input.Summary = strings.TrimSpace(input.Summary)
	input.SearchText = strings.TrimSpace(input.SearchText)
	input.TokenizerVersion = strings.TrimSpace(input.TokenizerVersion)
	switch {
	case input.UserID == "":
		return CreateInput{}, fmt.Errorf("user_id is required")
	case input.ConversationID == "":
		return CreateInput{}, fmt.Errorf("conversation_id is required")
	case input.SourceKind != SourceKindChatExplicit && input.SourceKind != SourceKindMemoryReview:
		return CreateInput{}, fmt.Errorf("source_kind is invalid")
	case !validEpisodeType(input.EpisodeType):
		return CreateInput{}, fmt.Errorf("episode_type is invalid")
	case input.Summary == "":
		return CreateInput{}, fmt.Errorf("summary is required")
	case utf8.RuneCountInString(input.Summary) > 200:
		return CreateInput{}, fmt.Errorf("summary must be at most 200 characters")
	case input.SearchText == "":
		return CreateInput{}, fmt.Errorf("search_text is required")
	case input.TokenizerVersion == "":
		return CreateInput{}, fmt.Errorf("tokenizer_version is required")
	case input.OccurredAtMS <= 0:
		return CreateInput{}, fmt.Errorf("occurred_at_ms must be positive")
	}
	return input, nil
}

func validEpisodeType(value string) bool {
	switch value {
	case EpisodeTypeDecision, EpisodeTypeProgress, EpisodeTypeResult, EpisodeTypeBlocker, EpisodeTypeEvent:
		return true
	default:
		return false
	}
}

func normalizeSummary(value string) string {
	return strings.Join(strings.Fields(cases.Fold().String(norm.NFKC.String(value))), " ")
}
