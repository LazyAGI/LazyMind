package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/google/uuid"
	fsrs "github.com/open-spaced-repetition/go-fsrs/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/algo"
	"lazymind/core/modelconfig"
)

type SessionView struct {
	Session ReviewSession `json:"session"`
	Items   []struct {
		ReviewSessionItem
		Question QuestionInstance `json:"question"`
		Card     Card             `json:"card"`
	} `json:"items"`
}

func decodeObject(raw string) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func questionFromContent(questionType, surface string, value map[string]any) (map[string]any, map[string]any, bool) {
	answer := contentAnswer(value)
	switch questionType {
	case "text_input":
		if answer == "" {
			return nil, nil, false
		}
		return map[string]any{"prompt": surface, "response_kind": "text"}, map[string]any{"accepted": []string{answer}, "grading": "normalized_exact"}, true
	case "cloze":
		examples, _ := value["examples"].([]any)
		if len(examples) == 0 {
			return nil, nil, false
		}
		example := fmt.Sprint(examples[0])
		if !strings.Contains(strings.ToLower(example), strings.ToLower(surface)) {
			return nil, nil, false
		}
		return map[string]any{"prompt": strings.Replace(example, surface, "____", 1), "response_kind": "text"}, map[string]any{"accepted": []string{surface}, "grading": "normalized_exact"}, true
	case "rubric_self_assessment":
		return map[string]any{"prompt": surface, "response_kind": "self_assessment", "reference": value}, map[string]any{"grading": "self_assessment"}, true
	}
	return nil, nil, false
}

func contentAnswer(value map[string]any) string {
	for _, key := range []string{"meaning", "meaning_in_context", "translation", "pinyin", "techniques"} {
		if v := strings.TrimSpace(fmt.Sprint(value[key])); v != "" && v != "<nil>" {
			return v
		}
	}
	return ""
}
func (s *Service) singleChoiceQuestion(tx *gorm.DB, owner string, content Content, surface string, value map[string]any) (map[string]any, map[string]any, bool) {
	answer := contentAnswer(value)
	if answer == "" {
		return nil, nil, false
	}
	var rows []Content
	if err := tx.Where("owner_id = ? AND capability_key = ? AND id <> ? AND status = ?", owner, content.CapabilityKey, content.ID, "published").Limit(12).Find(&rows).Error; err != nil {
		return nil, nil, false
	}
	options := []string{answer}
	seen := map[string]bool{normalize(answer): true}
	for _, row := range rows {
		candidate := contentAnswer(decodeObject(row.ContentJSON))
		key := normalize(candidate)
		if candidate != "" && !seen[key] {
			seen[key] = true
			options = append(options, candidate)
		}
		if len(options) == 4 {
			break
		}
	}
	if len(options) < 3 {
		return nil, nil, false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(content.ID))
	shift := int(h.Sum32()) % len(options)
	options = append(options[shift:], options[:shift]...)
	return map[string]any{"prompt": surface, "response_kind": "single_choice", "options": options}, map[string]any{"accepted": []string{answer}, "grading": "normalized_exact"}, true
}

func (s *Service) dynamicQuestion(ctx context.Context, owner, questionType, locale, surface string, value map[string]any) (map[string]any, map[string]any, error) {
	config, err := modelconfig.LoadLLMConfig(ctx, s.db, owner)
	if err != nil {
		return nil, nil, err
	}
	prompt := fmt.Sprintf("Return JSON only with payload and answer_spec. Create one %s learning question in locale %s from subject %q and verified content %s. answer_spec must contain grading (normalized_exact, semantic_or_self, or self_assessment), and never add unsupported facts.", questionType, locale, surface, marshal(value))
	for attempt := 0; attempt < 2; attempt++ {
		raw, generateErr := algo.GenerateLearning(ctx, algo.LearningGenerateRequest{Content: surface, UserInstruct: prompt, LLMConfig: config})
		if generateErr != nil {
			err = generateErr
			continue
		}
		obj, parseErr := extractJSONObject(raw)
		if parseErr != nil {
			err = parseErr
			continue
		}
		payload, ok1 := obj["payload"].(map[string]any)
		answer, ok2 := obj["answer_spec"].(map[string]any)
		if ok1 && ok2 && validateQuestionInstance(questionType, payload, answer) == nil {
			return payload, answer, nil
		}
		err = errors.New("model returned invalid question schema")
	}
	return nil, nil, err
}

func stringValues(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		if values, yes := value.([]string); yes {
			return values
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, strings.TrimSpace(fmt.Sprint(item)))
	}
	return out
}

func validateQuestionInstance(questionType string, payload, answer map[string]any) error {
	prompt := strings.TrimSpace(fmt.Sprint(payload["prompt"]))
	if prompt == "" || prompt == "<nil>" {
		return errors.New("question prompt is required")
	}
	grading := strings.TrimSpace(fmt.Sprint(answer["grading"]))
	if grading == "" || grading == "<nil>" {
		return errors.New("question grading is required")
	}
	if grading == "self_assessment" || grading == "semantic_or_self" {
		return nil
	}
	accepted := stringValues(answer["accepted"])
	if len(accepted) == 0 || accepted[0] == "" {
		return errors.New("question answer is required")
	}
	if questionType == "single_choice" || questionType == "true_false" {
		options := stringValues(payload["options"])
		if questionType == "true_false" && len(options) == 0 {
			options = []string{"true", "false"}
		}
		seen, found := map[string]bool{}, false
		for _, option := range options {
			key := normalize(option)
			if key == "" || seen[key] {
				return errors.New("question options must be unique")
			}
			seen[key] = true
			found = found || normalizedEqual(option, accepted[0])
		}
		if len(options) < 2 || !found {
			return errors.New("question answer must be one of the options")
		}
	}
	return nil
}

func (s *Service) CreateReviewSession(ctx context.Context, owner, bookID, locale string, limit int) (SessionView, error) {
	if err := requireLocal(); err != nil {
		return SessionView{}, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if locale == "" {
		locale = "zh-CN"
	}
	var book Book
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ? AND archived_at IS NULL", bookID, owner).First(&book).Error; err != nil {
		return SessionView{}, err
	}
	var questionTypes []string
	if json.Unmarshal([]byte(book.QuestionTypesJSON), &questionTypes) != nil || len(questionTypes) == 0 {
		return SessionView{}, errors.New("learning collection has no question types")
	}
	var entries []BookEntry
	if err := s.db.WithContext(ctx).Where("book_id = ? AND owner_id = ? AND status = ?", bookID, owner, "active").Limit(limit).Find(&entries).Error; err != nil {
		return SessionView{}, err
	}
	if len(entries) == 0 {
		return SessionView{}, errors.New("learning collection is empty")
	}
	now := time.Now().UTC()
	session := ReviewSession{ID: uuid.NewString(), OwnerID: owner, BookID: bookID, Locale: locale, Status: "active", CreatedAt: now}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&session).Error; err != nil {
			return err
		}
		position := 0
		for _, entry := range entries {
			var content Content
			var subject Subject
			if err := tx.Where("id = ? AND owner_id = ?", entry.ContentID, owner).First(&content).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ? AND owner_id = ?", content.SubjectID, owner).First(&subject).Error; err != nil {
				return err
			}
			value := decodeObject(content.ContentJSON)
			for _, qt := range questionTypes {
				var card Card
				err := tx.Where("owner_id = ? AND book_entry_id = ? AND question_type = ?", owner, entry.ID, qt).First(&card).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					fsrsCard := fsrs.NewCard()
					raw, _ := json.Marshal(fsrsCard)
					card = Card{ID: uuid.NewString(), OwnerID: owner, BookID: bookID, BookEntryID: entry.ID, ContentID: content.ID, QuestionType: qt, FSRSCardJSON: string(raw), SchedulerVersion: "go-fsrs/v3.3.1", RowVersion: 1, CreatedAt: now, UpdatedAt: now}
					if err = tx.Create(&card).Error; err != nil {
						return err
					}
				} else if err != nil {
					return err
				}
				var fcard fsrs.Card
				if json.Unmarshal([]byte(card.FSRSCardJSON), &fcard) != nil || card.Suspended || fcard.Due.After(now) {
					continue
				}
				payload, answer, ok := questionFromContent(qt, subject.DisplayText, value)
				if qt == "single_choice" {
					payload, answer, ok = s.singleChoiceQuestion(tx, owner, content, subject.DisplayText, value)
				}
				generator := "deterministic"
				if !ok {
					payload, answer, err = s.dynamicQuestion(ctx, owner, qt, locale, subject.DisplayText, value)
					if err != nil {
						continue
					}
					generator = "llm"
				}
				if err := validateQuestionInstance(qt, payload, answer); err != nil {
					continue
				}
				question := QuestionInstance{ID: uuid.NewString(), OwnerID: owner, CardID: card.ID, SessionID: session.ID, QuestionType: qt, QuestionTypeVersion: 1, PayloadJSON: marshal(payload), AnswerSpecJSON: marshal(answer), ExplanationJSON: "{}", Locale: locale, GeneratorType: generator, GeneratorVersion: "learning-v1", ContentVersion: fmt.Sprint(content.SchemaVersion), Status: "ready", CreatedAt: now}
				if err := tx.Create(&question).Error; err != nil {
					return err
				}
				position++
				if err := tx.Create(&ReviewSessionItem{ID: uuid.NewString(), OwnerID: owner, SessionID: session.ID, CardID: card.ID, QuestionInstanceID: question.ID, Position: position, CreatedAt: now}).Error; err != nil {
					return err
				}
			}
		}
		session.Total = position
		if position == 0 {
			return errors.New("no cards are due")
		}
		return tx.Model(&session).Update("total", position).Error
	})
	if err != nil {
		return SessionView{}, err
	}
	return s.GetReviewSession(ctx, owner, session.ID)
}

func (s *Service) GetReviewSession(ctx context.Context, owner, id string) (SessionView, error) {
	var out SessionView
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, owner).First(&out.Session).Error; err != nil {
		return out, err
	}
	var items []ReviewSessionItem
	if err := s.db.WithContext(ctx).Where("session_id = ? AND owner_id = ?", id, owner).Order("position").Find(&items).Error; err != nil {
		return out, err
	}
	for _, item := range items {
		var row struct {
			ReviewSessionItem
			Question QuestionInstance `json:"question"`
			Card     Card             `json:"card"`
		}
		row.ReviewSessionItem = item
		if err := s.db.WithContext(ctx).Where("id = ?", item.QuestionInstanceID).First(&row.Question).Error; err != nil {
			return out, err
		}
		if err := s.db.WithContext(ctx).Where("id = ?", item.CardID).First(&row.Card).Error; err != nil {
			return out, err
		}
		out.Items = append(out.Items, row)
	}
	return out, nil
}

type AnswerResult struct {
	Correct  bool           `json:"correct"`
	Score    float64        `json:"score"`
	Rating   string         `json:"rating"`
	Feedback map[string]any `json:"feedback"`
}

func normalizedEqual(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

func (s *Service) AnswerQuestion(ctx context.Context, owner, sessionID, questionID, response, requestedRating, idempotency string) (AnswerResult, error) {
	var previous ReviewAnswer
	if idempotency != "" && s.db.WithContext(ctx).Where("owner_id = ? AND idempotency_key = ?", owner, idempotency).First(&previous).Error == nil {
		if previous.SessionID != sessionID || previous.QuestionInstanceID != questionID {
			return AnswerResult{}, errors.New("idempotency key belongs to another answer")
		}
		return AnswerResult{Correct: previous.Correct, Score: previous.Score, Rating: previous.Rating, Feedback: decodeObject(previous.FeedbackJSON)}, nil
	}
	if idempotency == "" {
		idempotency = uuid.NewString()
	}
	var q QuestionInstance
	if err := s.db.WithContext(ctx).Where("id = ? AND session_id = ? AND owner_id = ?", questionID, sessionID, owner).First(&q).Error; err != nil {
		return AnswerResult{}, err
	}
	spec := decodeObject(q.AnswerSpecJSON)
	grading := fmt.Sprint(spec["grading"])
	correct, score := false, 0.0
	if grading == "self_assessment" || grading == "semantic_or_self" {
		if requestedRating == "" {
			return AnswerResult{}, errors.New("rating is required for self-assessed question")
		}
		correct = requestedRating != "again"
		if correct {
			score = 1
		}
	} else if values, ok := spec["accepted"].([]any); ok {
		for _, expected := range values {
			if normalizedEqual(response, fmt.Sprint(expected)) {
				correct, score = true, 1
				break
			}
		}
	}
	rating := requestedRating
	if rating == "" {
		if correct {
			rating = "good"
		} else {
			rating = "again"
		}
	}
	fsrsRating := map[string]fsrs.Rating{"again": fsrs.Again, "hard": fsrs.Hard, "good": fsrs.Good, "easy": fsrs.Easy}[rating]
	if fsrsRating == 0 {
		return AnswerResult{}, errors.New("invalid rating")
	}
	now := time.Now().UTC()
	feedback := map[string]any{"answer_spec": spec}
	var replay *AnswerResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var duplicate ReviewAnswer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("owner_id = ? AND idempotency_key = ?", owner, idempotency).First(&duplicate).Error; err == nil {
			if duplicate.SessionID != sessionID || duplicate.QuestionInstanceID != questionID {
				return errors.New("idempotency key belongs to another answer")
			}
			value := AnswerResult{Correct: duplicate.Correct, Score: duplicate.Score, Rating: duplicate.Rating, Feedback: decodeObject(duplicate.FeedbackJSON)}
			replay = &value
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var session ReviewSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", sessionID, owner).First(&session).Error; err != nil {
			return err
		}
		if session.Status != "active" {
			return errors.New("review session is not active")
		}
		var sessionItem ReviewSessionItem
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id = ? AND question_instance_id = ? AND owner_id = ?", sessionID, questionID, owner).First(&sessionItem).Error; err != nil {
			return err
		}
		if sessionItem.AnsweredAt != nil {
			return errors.New("question has already been answered")
		}
		var card Card
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", q.CardID, owner).First(&card).Error; err != nil {
			return err
		}
		var state fsrs.Card
		if err := json.Unmarshal([]byte(card.FSRSCardJSON), &state); err != nil {
			return err
		}
		log := fsrs.NewFSRS(fsrs.DefaultParam()).Repeat(state, now)[fsrsRating]
		stateRaw, _ := json.Marshal(log.Card)
		logRaw, _ := json.Marshal(log.ReviewLog)
		answerRow := ReviewAnswer{ID: uuid.NewString(), OwnerID: owner, SessionID: sessionID, QuestionInstanceID: questionID, AnswerJSON: marshal(map[string]any{"response": response}), FeedbackJSON: marshal(feedback), Rating: rating, IdempotencyKey: idempotency, Score: score, Correct: correct, AnsweredAt: now}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&answerRow)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			if err := tx.Where("owner_id = ? AND idempotency_key = ?", owner, idempotency).First(&duplicate).Error; err != nil {
				return err
			}
			if duplicate.SessionID != sessionID || duplicate.QuestionInstanceID != questionID {
				return errors.New("idempotency key belongs to another answer")
			}
			value := AnswerResult{Correct: duplicate.Correct, Score: duplicate.Score, Rating: duplicate.Rating, Feedback: decodeObject(duplicate.FeedbackJSON)}
			replay = &value
			return nil
		}
		result := tx.Model(&Card{}).Where("id = ? AND row_version = ?", card.ID, card.RowVersion).Updates(map[string]any{"fsrs_card_json": string(stateRaw), "row_version": card.RowVersion + 1, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return errors.New("review card changed")
		}
		if err := tx.Create(&ReviewLog{ID: uuid.NewString(), OwnerID: owner, CardID: card.ID, Rating: rating, FSRSLogJSON: string(logRaw), IdempotencyKey: idempotency, ReviewedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&ReviewSessionItem{}).Where("session_id = ? AND question_instance_id = ? AND answered_at IS NULL", sessionID, questionID).Update("answered_at", now).Error; err != nil {
			return err
		}
		updates := map[string]any{"answered": gorm.Expr("answered + 1")}
		if correct {
			updates["correct"] = gorm.Expr("correct + 1")
		}
		if err := tx.Model(&ReviewSession{}).Where("id = ? AND owner_id = ?", sessionID, owner).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Model(&ReviewSession{}).Where("id = ? AND answered >= total", sessionID).Updates(map[string]any{"status": "completed", "completed_at": now}).Error
	})
	if err == nil && replay != nil {
		return *replay, nil
	}
	return AnswerResult{Correct: correct, Score: score, Rating: rating, Feedback: feedback}, err
}
