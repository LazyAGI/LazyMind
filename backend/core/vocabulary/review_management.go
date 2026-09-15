package vocabulary

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	fsrs "github.com/open-spaced-repetition/go-fsrs/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ReviewLogExport struct {
	CardID     string          `json:"card_id"`
	WordID     string          `json:"word_id"`
	Term       string          `json:"term"`
	CardType   string          `json:"card_type"`
	Rating     int             `json:"rating"`
	FSRSLog    json.RawMessage `json:"fsrs_log"`
	ReviewedAt time.Time       `json:"reviewed_at"`
}

func (s *Service) ActiveProfile(ctx context.Context, owner string) (FSRSProfile, error) {
	if err := requireLocalRuntime(); err != nil {
		return FSRSProfile{}, err
	}
	var row FSRSProfile
	err := s.db.WithContext(ctx).Where("owner_id = ? AND active = ?", owner, true).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		p := fsrs.DefaultParam()
		raw, _ := json.Marshal(p.W)
		now := time.Now().UTC()
		row = FSRSProfile{ID: uuid.NewString(), OwnerID: owner, Name: "Default FSRS", WeightsJSON: string(raw), DesiredRetention: p.RequestRetention, MaximumIntervalDays: int(p.MaximumInterval), SchedulerVersion: "go-fsrs/v3.3.1", Source: "default", Active: true, CreatedAt: now, UpdatedAt: now}
		err = s.db.WithContext(ctx).Create(&row).Error
	}
	return row, err
}
func (s *Service) SaveProfile(ctx context.Context, owner string, in FSRSProfile) (FSRSProfile, error) {
	if err := requireLocalRuntime(); err != nil {
		return FSRSProfile{}, err
	}
	var weights fsrs.Weights
	if err := json.Unmarshal([]byte(in.WeightsJSON), &weights); err != nil {
		return FSRSProfile{}, errors.New("FSRS weights must contain 19 numeric values for go-fsrs/v3")
	}
	if in.DesiredRetention < 0.7 || in.DesiredRetention > 0.99 {
		return FSRSProfile{}, errors.New("desired retention must be between 0.70 and 0.99")
	}
	if in.MaximumIntervalDays < 1 || in.MaximumIntervalDays > 36500 {
		return FSRSProfile{}, errors.New("maximum interval must be between 1 and 36500 days")
	}
	now := time.Now().UTC()
	if in.ID == "" {
		in.ID = uuid.NewString()
		in.CreatedAt = now
	}
	in.OwnerID = owner
	in.Active = true
	in.Source = defaultString(in.Source, "imported")
	in.SchedulerVersion = "go-fsrs/v3.3.1"
	in.UpdatedAt = now
	return in, s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&FSRSProfile{}).Where("owner_id = ?", owner).Update("active", false).Error; err != nil {
			return err
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"name", "weights_json", "desired_retention", "maximum_interval_days", "scheduler_version", "source", "active", "updated_at"})}).Create(&in).Error
	})
}
func (s *Service) ExportReviewLogs(ctx context.Context, owner string) ([]ReviewLogExport, error) {
	if err := requireLocalRuntime(); err != nil {
		return nil, err
	}
	var rows []struct {
		CardID      string
		WordID      string
		Term        string
		CardType    string
		Rating      int
		FSRSLogJSON string
		ReviewedAt  time.Time
	}
	if err := s.db.WithContext(ctx).Table("vocabulary_review_logs l").Select("l.card_id,c.word_id,w.term,c.card_type,l.rating,l.fsrs_log_json,l.reviewed_at").Joins("JOIN vocabulary_review_cards c ON c.id=l.card_id AND c.owner_id=l.owner_id").Joins("JOIN vocabulary_words w ON w.id=c.word_id AND w.owner_id=l.owner_id").Where("l.owner_id = ?", owner).Order("l.reviewed_at DESC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ReviewLogExport, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReviewLogExport{CardID: row.CardID, WordID: row.WordID, Term: row.Term, CardType: row.CardType, Rating: row.Rating, FSRSLog: json.RawMessage(row.FSRSLogJSON), ReviewedAt: row.ReviewedAt})
	}
	return out, nil
}
func (s *Service) ReviewHistory(ctx context.Context, owner, wordID string) ([]ReviewLogExport, error) {
	if err := requireLocalRuntime(); err != nil {
		return nil, err
	}
	var rows []struct {
		CardID      string
		Rating      int
		FSRSLogJSON string
		ReviewedAt  time.Time
	}
	err := s.db.WithContext(ctx).Table("vocabulary_review_logs l").Select("l.card_id,l.rating,l.fsrs_log_json,l.reviewed_at").Joins("JOIN vocabulary_review_cards c ON c.id=l.card_id").Where("l.owner_id = ? AND c.word_id = ?", owner, wordID).Order("l.reviewed_at").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]ReviewLogExport, 0, len(rows))
	for _, row := range rows {
		out = append(out, ReviewLogExport{CardID: row.CardID, Rating: row.Rating, FSRSLog: json.RawMessage(row.FSRSLogJSON), ReviewedAt: row.ReviewedAt})
	}
	return out, nil
}
func (s *Service) Stats(ctx context.Context, owner string) (ReviewStats, error) {
	if err := requireLocalRuntime(); err != nil {
		return ReviewStats{}, err
	}
	var stats ReviewStats
	if err := s.db.WithContext(ctx).Model(&Word{}).Where("owner_id = ? AND provider = ? AND archived_at IS NULL", owner, "local").Count(&stats.Total).Error; err != nil {
		return stats, err
	}
	s.db.WithContext(ctx).Model(&Word{}).Where("owner_id = ? AND provider = ? AND mastered_at IS NOT NULL", owner, "local").Count(&stats.Mastered)
	now := time.Now().UTC()
	var cards []ReviewCard
	s.db.WithContext(ctx).Where("owner_id = ? AND suspended_at IS NULL", owner).Find(&cards)
	for _, row := range cards {
		var card fsrs.Card
		if json.Unmarshal([]byte(row.FSRSCardJSON), &card) != nil {
			continue
		}
		if !card.Due.After(now) {
			stats.Due++
		}
		switch card.State {
		case fsrs.New:
			stats.New++
		case fsrs.Learning:
			stats.Learning++
		case fsrs.Review:
			stats.Review++
		case fsrs.Relearning:
			stats.Relearning++
		}
		if card.Lapses >= 3 {
			stats.Weak++
		}
	}
	start := now.Truncate(24 * time.Hour)
	s.db.WithContext(ctx).Table("vocabulary_review_logs").Where("owner_id = ? AND reviewed_at >= ?", owner, start).Count(&stats.ReviewedToday)
	return stats, nil
}

func (s *Service) AnkiStats(ctx context.Context, owner string) (ReviewStats, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return ReviewStats{}, err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return ReviewStats{}, err
	}
	deck := strings.ReplaceAll(setting.AnkiDeckName, "\"", "")
	notes, err := client.vocabularyNotes(ctx, deck)
	if err != nil {
		return ReviewStats{}, err
	}
	var ids []int64
	if err = client.invoke(ctx, "findCards", map[string]any{"query": "deck:\"" + deck + "\" note:\"" + vocabularyModel + "\""}, &ids); err != nil {
		return ReviewStats{}, err
	}
	var cards []ankiCardInfo
	if len(ids) > 0 {
		if err = client.invoke(ctx, "cardsInfo", map[string]any{"cards": ids}, &cards); err != nil {
			return ReviewStats{}, err
		}
	}
	stats := ReviewStats{Total: int64(len(notes))}
	now := time.Now()
	startID := now.Add(-24 * time.Hour).UnixMilli()
	for _, card := range cards {
		switch card.Queue {
		case 0:
			stats.New++
		case 1, 3:
			stats.Learning++
		case 2:
			stats.Review++
		}
		if card.Lapses >= 3 {
			stats.Weak++
		}
	}
	var due []int64
	if err = client.invoke(ctx, "findCards", map[string]any{"query": "deck:\"" + deck + "\" is:due note:\"" + vocabularyModel + "\""}, &due); err == nil {
		stats.Due = int64(len(due))
	}
	var reviews [][]any
	if err = client.invoke(ctx, "cardReviews", map[string]any{"deck": setting.AnkiDeckName, "startID": startID}, &reviews); err == nil {
		stats.ReviewedToday = int64(len(reviews))
	}
	return stats, nil
}

func (s *Service) ExportAnkiReviewLogs(ctx context.Context, owner string) ([]ReviewLogExport, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return nil, err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return nil, err
	}
	var rows [][]any
	if err = client.invoke(ctx, "cardReviews", map[string]any{"deck": setting.AnkiDeckName, "startID": 0}, &rows); err != nil {
		return nil, err
	}
	cardIDs := make([]int64, 0, len(rows))
	seen := map[int64]bool{}
	for _, row := range rows {
		if len(row) > 1 {
			id := anyInt64(row[1])
			if id > 0 && !seen[id] {
				seen[id] = true
				cardIDs = append(cardIDs, id)
			}
		}
	}
	var cards []ankiCardInfo
	if len(cardIDs) > 0 {
		if err = client.invoke(ctx, "cardsInfo", map[string]any{"cards": cardIDs}, &cards); err != nil {
			return nil, err
		}
	}
	byID := map[int64]ankiCardInfo{}
	for _, card := range cards {
		byID[card.CardID] = card
	}
	out := make([]ReviewLogExport, 0, len(rows))
	for _, row := range rows {
		if len(row) < 9 {
			continue
		}
		cardID := anyInt64(row[1])
		card := byID[cardID]
		if card.ModelName != vocabularyModel && card.ModelName != sentenceModel {
			continue
		}
		term := card.Fields["Word"].Value
		if term == "" {
			term = card.Fields["TargetWord"].Value
		}
		payload, _ := json.Marshal(map[string]any{"interval_before_days": anyInt64(row[5]), "interval_after_days": anyInt64(row[4]), "review_time_ms": anyInt64(row[7])})
		out = append(out, ReviewLogExport{CardID: strconv.FormatInt(cardID, 10), Term: term, CardType: card.ModelName, Rating: int(anyInt64(row[3])), FSRSLog: payload, ReviewedAt: time.UnixMilli(anyInt64(row[0])).UTC()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReviewedAt.After(out[j].ReviewedAt) })
	return out, nil
}

func anyInt64(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

func (s *Service) NextAnkiQuestion(ctx context.Context, owner string) (*ReviewQuestion, error) {
	items, err := s.NextAnkiQuestions(ctx, owner, 1)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (s *Service) NextAnkiQuestions(ctx context.Context, owner string, limit int) ([]ReviewQuestion, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return nil, err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err = client.invoke(ctx, "findCards", map[string]any{"query": "deck:\"" + setting.AnkiDeckName + "\" is:due"}, &ids); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []ReviewQuestion{}, nil
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 2000 {
		limit = 2000
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	var cards []ankiCardInfo
	if err = client.invoke(ctx, "cardsInfo", map[string]any{"cards": ids}, &cards); err != nil || len(cards) == 0 {
		return nil, err
	}
	items := make([]ReviewQuestion, 0, len(cards))
	now := time.Now().UTC()
	for index, card := range cards {
		items = append(items, ReviewQuestion{CardID: strconv.FormatInt(card.CardID, 10), Prompt: card.Question, Answer: card.Answer, Remaining: int64(len(cards) - index), PreviewedAt: now, VocabularyItem: VocabularyItem{State: "review"}})
	}
	return items, nil
}
func (s *Service) ReviewAnki(ctx context.Context, owner string, req ReviewRequest) error {
	rating, err := ratingValue(req.Rating)
	if err != nil {
		return err
	}
	cardID, err := strconv.ParseInt(req.CardID, 10, 64)
	if err != nil {
		return err
	}
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return err
	}
	return client.answerCard(ctx, cardID, int(rating))
}
