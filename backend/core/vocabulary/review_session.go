package vocabulary

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ReviewBatch struct {
	Session   ReviewSession    `json:"session"`
	Questions []ReviewQuestion `json:"questions"`
}
type ReviewSessionReport struct {
	Session               ReviewSession         `json:"session"`
	Total                 int                   `json:"total"`
	Correct               int                   `json:"correct"`
	Incorrect             int                   `json:"incorrect"`
	Accuracy              float64               `json:"accuracy"`
	RatingCounts          map[string]int        `json:"rating_counts"`
	AverageIntervalBefore float64               `json:"average_interval_before_days"`
	AverageIntervalAfter  float64               `json:"average_interval_after_days"`
	DifficultWords        []string              `json:"difficult_words"`
	Answers               []ReviewSessionAnswer `json:"answers"`
}

type SessionAskQuestion struct {
	ReviewItemID string   `json:"review_item_id"`
	WordID       string   `json:"word_id"`
	Text         string   `json:"text"`
	Type         string   `json:"type"`
	Choices      []string `json:"choices,omitempty"`
}

type SessionIssuedWord struct {
	ReviewItemID string `json:"review_item_id"`
	WordID       string `json:"word_id"`
	Term         string `json:"term"`
}

const reviewSessionLifetime = 12 * time.Hour

func (s *Service) ActiveReviewSession(ctx context.Context, owner string) (*ReviewSession, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return nil, err
	}
	bookID := setting.LocalDefaultWordbookID
	if setting.SelectedProvider == ProviderAnki {
		bookID = setting.AnkiDeckName
	}
	now := time.Now().UTC()
	var session ReviewSession
	err = s.db.WithContext(ctx).Where("owner_id = ? AND provider = ? AND wordbook_id = ? AND completed_at IS NULL", owner, setting.SelectedProvider, bookID).Order("started_at DESC").First(&session).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !session.ExpiresAt.After(now) {
		if err := s.db.WithContext(ctx).Model(&ReviewSession{}).Where("id = ? AND completed_at IS NULL", session.ID).Updates(map[string]any{"status": "expired", "completed_at": now}).Error; err != nil {
			return nil, err
		}
		return nil, nil
	}
	return &session, nil
}

func (s *Service) ensureActiveReviewSession(ctx context.Context, owner string) (ReviewSession, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return ReviewSession{}, err
	}
	bookID, bookName := setting.LocalDefaultWordbookID, "当前单词本"
	if setting.SelectedProvider == ProviderAnki {
		bookID, bookName = setting.AnkiDeckName, setting.AnkiDeckName
	} else if bookID != "" {
		var book Wordbook
		if s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, bookID).First(&book).Error == nil {
			bookName = book.Name
		}
	}
	if active, activeErr := s.ActiveReviewSession(ctx, owner); activeErr != nil {
		return ReviewSession{}, activeErr
	} else if active != nil {
		return *active, nil
	}
	now := time.Now().UTC()
	var session ReviewSession
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session = ReviewSession{ID: uuid.NewString(), OwnerID: owner, Provider: setting.SelectedProvider, WordbookID: bookID, WordbookName: bookName, Status: "active", StartedAt: now, ExpiresAt: now.Add(reviewSessionLifetime), CreatedAt: now}
		return tx.Create(&session).Error
	})
	return session, err
}

func (s *Service) previewReviewQuestions(ctx context.Context, owner string, limit int) (ReviewSession, []ReviewQuestion, error) {
	session, err := s.ensureActiveReviewSession(ctx, owner)
	if err != nil {
		return ReviewSession{}, nil, err
	}
	if limit < 1 {
		limit = 5
	}
	if limit > 200 {
		limit = 200
	}
	fetchLimit := min(2000, limit*10)
	var questions []ReviewQuestion
	if session.Provider == ProviderAnki {
		questions, err = s.NextAnkiQuestions(ctx, owner, fetchLimit)
	} else {
		questions, err = s.NextQuestions(ctx, owner, fetchLimit)
	}
	if err != nil {
		return ReviewSession{}, nil, err
	}
	result, seen := make([]ReviewQuestion, 0, limit), map[string]bool{}
	for _, question := range questions {
		key := strings.TrimSpace(question.Word.ID)
		if key == "" {
			key = normalizeTerm(question.Word.Term)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, question)
		if len(result) == limit {
			break
		}
	}
	return session, result, nil
}

func (s *Service) StartReviewSession(ctx context.Context, owner string, limit int) (ReviewBatch, error) {
	session, questions, err := s.previewReviewQuestions(ctx, owner, limit)
	if err != nil {
		return ReviewBatch{}, err
	}
	if err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sequence int64
		tx.Model(&ReviewSessionItem{}).Where("session_id = ?", session.ID).Count(&sequence)
		for index, question := range questions {
			expected := strings.TrimSpace(question.Answer)
			if expected == "" {
				expected = question.Word.Term
				if question.CardType == "word_to_meaning" {
					expected = question.Word.Meaning
				}
			}
			item := ReviewSessionItem{ID: uuid.NewString(), SessionID: session.ID, OwnerID: owner, CardID: question.CardID, WordID: question.Word.ID, Term: question.Word.Term, Meaning: question.Word.Meaning, Prompt: question.Prompt, Expected: expected, CardType: question.CardType, Status: "issued", Sequence: int(sequence) + index + 1, RowVersion: question.RowVersion, PreviewedAt: question.PreviewedAt, CreatedAt: time.Now().UTC()}
			if err := tx.Where("session_id = ? AND word_id = ?", session.ID, question.Word.ID).FirstOrCreate(&item).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return ReviewBatch{}, err
	}
	return ReviewBatch{Session: session, Questions: questions}, nil
}

func (s *Service) PreviewReviewSession(ctx context.Context, owner string, limit int) (ReviewBatch, error) {
	session, questions, err := s.previewReviewQuestions(ctx, owner, limit)
	return ReviewBatch{Session: session, Questions: questions}, err
}

func (s *Service) IssueReviewSessionWords(ctx context.Context, owner string, terms []string) (ReviewSession, []SessionIssuedWord, error) {
	if len(terms) == 0 || len(terms) > 20 {
		return ReviewSession{}, nil, errors.New("ask_words requires 1 to 20 target words")
	}
	wanted := make(map[string]bool, len(terms))
	for _, term := range terms {
		key := normalizeTerm(term)
		if key == "" || wanted[key] {
			return ReviewSession{}, nil, errors.New("target words must be unique and non-empty")
		}
		wanted[key] = true
	}
	session, candidates, err := s.previewReviewQuestions(ctx, owner, 200)
	if err != nil {
		return ReviewSession{}, nil, err
	}
	byTerm := make(map[string]ReviewQuestion, len(candidates))
	for _, candidate := range candidates {
		byTerm[normalizeTerm(candidate.Word.Term)] = candidate
	}
	issued := make([]SessionIssuedWord, 0, len(terms))
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sequence int64
		if err := tx.Model(&ReviewSessionItem{}).Where("session_id = ?", session.ID).Count(&sequence).Error; err != nil {
			return err
		}
		for index, term := range terms {
			question, ok := byTerm[normalizeTerm(term)]
			if !ok {
				return errors.New("target word is not currently available for review: " + strings.TrimSpace(term))
			}
			var existing ReviewSessionItem
			if err := tx.Where("session_id = ? AND word_id = ?", session.ID, question.Word.ID).First(&existing).Error; err == nil {
				issued = append(issued, SessionIssuedWord{ReviewItemID: existing.ID, WordID: existing.WordID, Term: existing.Term})
				continue
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			now := time.Now().UTC()
			item := ReviewSessionItem{ID: uuid.NewString(), SessionID: session.ID, OwnerID: owner, CardID: question.CardID, WordID: question.Word.ID, Term: question.Word.Term, Meaning: question.Word.Meaning, Prompt: question.Prompt, Expected: question.Word.Term, CardType: "create", Status: "issued", Sequence: int(sequence) + index + 1, RowVersion: question.RowVersion, PreviewedAt: question.PreviewedAt, IssuedAt: &now, CreatedAt: now}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			issued = append(issued, SessionIssuedWord{ReviewItemID: item.ID, WordID: item.WordID, Term: item.Term})
		}
		return nil
	})
	return session, issued, err
}

func (s *Service) ActiveSessionItemByWord(ctx context.Context, owner, wordID string) (ReviewSession, ReviewSessionItem, error) {
	session, err := s.ensureActiveReviewSession(ctx, owner)
	if err != nil {
		return ReviewSession{}, ReviewSessionItem{}, err
	}
	var item ReviewSessionItem
	err = s.db.WithContext(ctx).Where("session_id = ? AND owner_id = ? AND word_id = ?", session.ID, owner, wordID).First(&item).Error
	return session, item, err
}

func (s *Service) NextSessionQuestion(ctx context.Context, owner, sessionID string) (*ReviewSessionItem, int64, error) {
	items, remain, err := s.NextSessionQuestions(ctx, owner, sessionID, 1)
	if err != nil || len(items) == 0 {
		return nil, remain, err
	}
	return &items[0], remain, nil
}

func (s *Service) NextSessionQuestions(ctx context.Context, owner, sessionID string, limit int) ([]ReviewSessionItem, int64, error) {
	if limit < 1 || limit > 200 {
		limit = 5
	}
	var results []ReviewSessionItem
	var remain int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session ReviewSession
		if err := tx.Where("id = ? AND owner_id = ? AND completed_at IS NULL", sessionID, owner).First(&session).Error; err != nil {
			return err
		}
		if err := tx.Model(&ReviewSessionItem{}).Where("session_id = ? AND owner_id = ? AND status <> ?", sessionID, owner, "answered").Count(&remain).Error; err != nil {
			return err
		}
		if remain == 0 {
			return nil
		}
		if err := tx.Where("session_id = ? AND owner_id = ? AND status = ?", sessionID, owner, "issued").Order("sequence").Find(&results).Error; err != nil {
			return err
		} else if len(results) > 0 {
			return nil
		}
		if err := tx.Where("session_id = ? AND owner_id = ? AND status = ?", sessionID, owner, "queued").Order("sequence").Limit(limit).Find(&results).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if remain == 0 {
		return nil, 0, nil
	}
	return results, remain, nil
}

// IssueClozeSessionWords turns only the candidate words used as cloze answers
// into review items. All other previewed candidates are released from this
// session and remain due in the underlying vocabulary scheduler.
func (s *Service) IssueClozeSessionWords(ctx context.Context, owner, sessionID string, answers []string) ([]SessionIssuedWord, error) {
	if len(answers) < 10 || len(answers) > 20 {
		return nil, errors.New("cloze requires 10 to 20 correct answers")
	}
	wanted := make(map[string]bool, len(answers))
	for _, answer := range answers {
		key := normalizeTerm(answer)
		if key == "" || wanted[key] {
			return nil, errors.New("cloze correct answers must be 10 to 20 unique candidate words")
		}
		wanted[key] = true
	}
	issued := make([]SessionIssuedWord, 0, len(wanted))
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session ReviewSession
		if err := tx.Where("id = ? AND owner_id = ? AND completed_at IS NULL", sessionID, owner).First(&session).Error; err != nil {
			return err
		}
		var candidates []ReviewSessionItem
		if err := tx.Where("session_id = ? AND owner_id = ? AND status = ?", sessionID, owner, "queued").Order("sequence").Find(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) < 20 {
			return errors.New("cloze requires at least 20 previewed candidate words; call get_review_words with count >= 20")
		}
		byTerm := make(map[string]ReviewSessionItem, len(candidates))
		for _, item := range candidates {
			byTerm[normalizeTerm(item.Term)] = item
		}
		selectedIDs := make([]string, 0, len(wanted))
		for _, answer := range answers {
			item, ok := byTerm[normalizeTerm(answer)]
			if !ok {
				return errors.New("cloze correct answer is not in the previewed candidates: " + strings.TrimSpace(answer))
			}
			selectedIDs = append(selectedIDs, item.ID)
			issued = append(issued, SessionIssuedWord{ReviewItemID: item.ID, WordID: item.WordID, Term: item.Term})
		}
		now := time.Now().UTC()
		result := tx.Model(&ReviewSessionItem{}).Where("session_id = ? AND owner_id = ? AND status = ? AND id IN ?", sessionID, owner, "queued", selectedIDs).Updates(map[string]any{"status": "issued", "issued_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(selectedIDs)) {
			return errors.New("some cloze candidates could not be issued")
		}
		for _, item := range issued {
			if err := tx.Model(&ReviewSessionItem{}).Where("id = ? AND session_id = ?", item.ReviewItemID, sessionID).
				Updates(map[string]any{"expected_answer": item.Term, "card_type": "create_cloze"}).Error; err != nil {
				return err
			}
		}
		return tx.Where("session_id = ? AND owner_id = ? AND status = ? AND id NOT IN ?", sessionID, owner, "queued", selectedIDs).Delete(&ReviewSessionItem{}).Error
	})
	return issued, err
}

func (s *Service) SessionRemaining(ctx context.Context, owner, sessionID string) (int64, error) {
	var remain int64
	err := s.db.WithContext(ctx).Model(&ReviewSessionItem{}).Where("session_id = ? AND owner_id = ? AND status <> ?", sessionID, owner, "answered").Count(&remain).Error
	return remain, err
}

func (s *Service) SessionItem(ctx context.Context, owner, sessionID, itemID string) (ReviewSessionItem, error) {
	var item ReviewSessionItem
	err := s.db.WithContext(ctx).Where("id = ? AND session_id = ? AND owner_id = ?", itemID, sessionID, owner).First(&item).Error
	return item, err
}

func (s *Service) PrepareSessionQuestions(ctx context.Context, owner, sessionID, mode, questionType string) ([]SessionAskQuestion, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	questionType = strings.ToLower(strings.TrimSpace(questionType))
	if mode != "e2c" && mode != "c2e" {
		return nil, errors.New("mode must be e2c or c2e")
	}
	if questionType != "choice" && questionType != "fill" {
		return nil, errors.New("question_type must be choice or fill")
	}
	if mode == "e2c" && questionType == "fill" {
		return nil, errors.New("e2c only supports choice questions")
	}
	var items []ReviewSessionItem
	if err := s.db.WithContext(ctx).Where("session_id = ? AND owner_id = ? AND status IN ?", sessionID, owner, []string{"queued", "issued"}).Order("sequence").Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("review session has no previewed questions")
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&ReviewSessionItem{}).Where("session_id = ? AND owner_id = ? AND status = ?", sessionID, owner, "queued").Updates(map[string]any{"status": "issued", "issued_at": now}).Error; err != nil {
		return nil, err
	}
	questions := make([]SessionAskQuestion, 0, len(items))
	for _, item := range items {
		var word Word
		if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", item.WordID, owner).First(&word).Error; err != nil {
			return nil, err
		}
		prompt, expected, askType := word.Term, word.Meaning, "single"
		var choices []string
		if mode == "c2e" {
			prompt, expected = strings.TrimSpace(word.PartOfSpeech+" "+word.Meaning), word.Term
		}
		if questionType == "fill" {
			askType = "text"
		} else {
			for _, choice := range reviewChoices(word, map[bool]string{true: "word_to_meaning", false: "meaning_to_word"}[mode == "e2c"]) {
				label := strings.TrimSpace(choice.Label)
				if mode == "c2e" && strings.TrimSpace(choice.PartOfSpeech) != "" {
					label += "（" + strings.TrimSpace(choice.PartOfSpeech) + "）"
				}
				choices = append(choices, label)
				if choice.Value == expected {
					expected = label
				}
			}
		}
		if err := s.db.WithContext(ctx).Model(&ReviewSessionItem{}).Where("id = ? AND status = ?", item.ID, "issued").Updates(map[string]any{"prompt": prompt, "expected_answer": expected, "card_type": mode + "_" + questionType}).Error; err != nil {
			return nil, err
		}
		questions = append(questions, SessionAskQuestion{ReviewItemID: item.ID, WordID: item.WordID, Text: prompt, Type: askType, Choices: choices})
	}
	return questions, nil
}

func (s *Service) SessionAnswerCorrect(ctx context.Context, owner, sessionID, cardID string) (bool, error) {
	var answer ReviewSessionAnswer
	if err := s.db.WithContext(ctx).Where("session_id = ? AND owner_id = ? AND card_id = ?", sessionID, owner, cardID).First(&answer).Error; err != nil {
		return false, err
	}
	return answer.Rating >= 3, nil
}

func (s *Service) RecordSessionAnswer(ctx context.Context, owner, sessionID, wordID, term string, req ReviewRequest) error {
	var session ReviewSession
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", sessionID, owner).First(&session).Error; err != nil {
		return err
	}
	var item ReviewSessionItem
	if err := s.db.WithContext(ctx).Where("session_id = ? AND owner_id = ? AND card_id = ?", sessionID, owner, req.CardID).First(&item).Error; err != nil {
		return err
	}
	if item.Status == "answered" {
		return errors.New("review item already answered")
	}
	if strings.TrimSpace(req.Response) != "" {
		if strings.EqualFold(strings.TrimSpace(req.Response), strings.TrimSpace(item.Expected)) {
			req.Rating = "good"
		} else {
			req.Rating = "again"
		}
	}
	key := req.IdempotencyKey
	if key == "" {
		key = uuid.NewString()
	}
	req.IdempotencyKey = key
	var err error
	if session.Provider == ProviderAnki {
		err = s.ReviewAnki(ctx, owner, req)
	} else {
		err = s.Review(ctx, owner, item.WordID, req)
	}
	if err != nil {
		return err
	}
	rating, _ := ratingValue(req.Rating)
	before, after := int64(0), int64(0)
	if session.Provider != "anki" {
		var row ReviewLogRow
		if s.db.WithContext(ctx).Where("owner_id = ? AND idempotency_key = ?", owner, key).First(&row).Error == nil {
			var payload struct {
				IntervalBefore uint64 `json:"interval_before_days"`
				IntervalAfter  uint64 `json:"interval_after_days"`
			}
			if json.Unmarshal([]byte(row.FSRSLogJSON), &payload) == nil {
				before, after = int64(payload.IntervalBefore), int64(payload.IntervalAfter)
			}
		}
	}
	now := time.Now().UTC()
	answer := ReviewSessionAnswer{ID: uuid.NewString(), SessionID: sessionID, OwnerID: owner, CardID: req.CardID, WordID: item.WordID, Term: item.Term, Rating: int(rating), IntervalBefore: before, IntervalAfter: after, AnsweredAt: now, CreatedAt: now}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&answer).Error; err != nil {
			return err
		}
		result := tx.Model(&ReviewSessionItem{}).Where("id = ? AND status <> ?", item.ID, "answered").Updates(map[string]any{"status": "answered", "answered_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("review item already answered")
		}
		return nil
	})
}

func (s *Service) CompleteReviewSession(ctx context.Context, owner, sessionID string) (ReviewSessionReport, error) {
	var session ReviewSession
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", sessionID, owner).First(&session).Error; err != nil {
		return ReviewSessionReport{}, err
	}
	var answers []ReviewSessionAnswer
	if err := s.db.WithContext(ctx).Where("session_id = ? AND owner_id = ?", sessionID, owner).Order("answered_at").Find(&answers).Error; err != nil {
		return ReviewSessionReport{}, err
	}
	var remain int64
	if err := s.db.WithContext(ctx).Model(&ReviewSessionItem{}).Where("session_id = ? AND owner_id = ? AND status <> ?", sessionID, owner, "answered").Count(&remain).Error; err != nil {
		return ReviewSessionReport{}, err
	}
	if remain > 0 {
		return ReviewSessionReport{}, errors.New("review session still has remaining questions")
	}
	now := time.Now().UTC()
	_ = s.db.WithContext(ctx).Model(&session).Updates(map[string]any{"completed_at": now, "status": "completed"}).Error
	session.CompletedAt = &now
	session.Status = "completed"
	report := ReviewSessionReport{Session: session, Total: len(answers), RatingCounts: map[string]int{"again": 0, "hard": 0, "good": 0, "easy": 0}, Answers: answers}
	if len(answers) == 0 {
		return report, nil
	}
	var before, after int64
	seen := map[string]bool{}
	for _, a := range answers {
		name := map[int]string{1: "again", 2: "hard", 3: "good", 4: "easy"}[a.Rating]
		report.RatingCounts[name]++
		if a.Rating < 3 {
			report.Incorrect++
			if !seen[a.Term] {
				report.DifficultWords = append(report.DifficultWords, a.Term)
				seen[a.Term] = true
			}
		} else {
			report.Correct++
		}
		before += a.IntervalBefore
		after += a.IntervalAfter
	}
	report.Accuracy = float64(report.Correct) / float64(report.Total)
	report.AverageIntervalBefore = float64(before) / float64(report.Total)
	report.AverageIntervalAfter = float64(after) / float64(report.Total)
	return report, nil
}
