package vocabulary

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	fsrs "github.com/open-spaced-repetition/go-fsrs/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func stateName(state fsrs.State) string {
	return []string{"new", "learning", "review", "relearning"}[int(state)]
}

func (s *Service) addLocalWord(ctx context.Context, owner string, req AddWordRequest) (AddWordResult, error) {
	if err := requireLocalRuntime(); err != nil {
		return AddWordResult{}, err
	}
	now := time.Now().UTC()
	normalized := normalizeTerm(req.Term)
	if req.DictionaryEntryID != "" {
		entries, _ := s.LookupDictionary(ctx, req.Language, req.Term)
		var entry DictionaryEntry
		for _, candidate := range entries {
			if candidate.ID == req.DictionaryEntryID {
				entry = candidate
				break
			}
		}
		if entry.ID != "" {
			var sense DictionarySense
			if len(entry.Senses) > 0 {
				sense = entry.Senses[0]
			}
			if req.Phonetic == "" {
				req.Phonetic = entry.Phonetic
			}
			if req.PartOfSpeech == "" {
				req.PartOfSpeech = sense.PartOfSpeech
			}
			if req.Meaning == "" {
				req.Meaning = sense.Translation
			}
			if req.Definition == "" {
				req.Definition = sense.Definition
			}
		}
	}
	var result AddWordResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var word Word
		err := tx.Where("owner_id = ? AND provider = ? AND language = ? AND normalized_term = ?", owner, "local", req.Language, normalized).First(&word).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			word = Word{ID: uuid.NewString(), OwnerID: owner, Provider: "local", NormalizedTerm: normalized, Term: req.Term, Language: req.Language, Phonetic: req.Phonetic, PartOfSpeech: req.PartOfSpeech, Meaning: req.Meaning, Definition: req.Definition, UserNote: req.UserNote, OriginType: defaultString(req.OriginType, "user"), CreatedAt: now, UpdatedAt: now}
			if req.DictionaryEntryID != "" {
				entries, _ := s.LookupDictionary(ctx, req.Language, req.Term)
				var entry DictionaryEntry
				for _, candidate := range entries {
					if candidate.ID == req.DictionaryEntryID {
						entry = candidate
						break
					}
				}
				if entry.ID != "" {
					word.SourceName = entry.SourceName
					word.SourceVersion = entry.SourceVersion
					word.LicenseID = entry.LicenseID
					word.SourceLocator = entry.SourceLocator
				}
			}
			if err = tx.Create(&word).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := s.attachWordbooks(tx, owner, word.ID, req.WordbookIDs); err != nil {
			return err
		}
		if err := s.setTags(tx, owner, word.ID, req.Tags, false); err != nil {
			return err
		}
		var example *Example
		sentence := strings.TrimSpace(req.Sentence)
		if sentence == "" {
			sentence = strings.TrimSpace(req.ContextSentence)
		}
		if sentence != "" {
			ex := Example{ID: uuid.NewString(), OwnerID: owner, WordID: word.ID, Sentence: sentence, Translation: req.Translation, ContentOrigin: defaultString(req.OriginType, "document"), CreatedAt: now, UpdatedAt: now}
			if err := tx.Where("owner_id = ? AND word_id = ? AND sentence = ?", owner, word.ID, sentence).FirstOrCreate(&ex).Error; err != nil {
				return err
			}
			if err := s.setExampleTags(tx, owner, ex.ID, req.ExampleTags); err != nil {
				return err
			}
			example = &ex
		}
		if req.DocumentID != "" {
			bbox, _ := json.Marshal(req.BBox)
			ref := SourceRef{ID: uuid.NewString(), OwnerID: owner, WordID: word.ID, DatasetID: req.DatasetID, DocumentID: req.DocumentID, SegmentID: req.SegmentID, Page: req.Page, BBoxJSON: string(bbox), SelectedText: req.SelectedText, ContextSentence: sentence, DocumentRevision: req.DocumentRevision, CreatedAt: now}
			if example != nil {
				ref.ExampleID = example.ID
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ref).Error; err != nil {
				return err
			}
		}
		_, _, err = s.ensureCardType(tx, owner, word.ID, "", "word_to_meaning", now)
		if err == nil {
			_, _, err = s.ensureCardType(tx, owner, word.ID, "", "meaning_to_word", now)
		}
		if err == nil && example != nil {
			_, _, err = s.ensureCardType(tx, owner, word.ID, example.ID, "sentence_cloze", now)
		}
		result = AddWordResult{Word: word, Example: example}
		return err
	})
	return result, err
}

func (s *Service) ListWords(ctx context.Context, owner, provider string) ([]VocabularyItem, error) {
	if provider == "" {
		provider = ProviderAnki
	}
	var words []Word
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND provider = ?", owner, provider).Order("created_at DESC").Find(&words).Error; err != nil {
		return nil, err
	}
	items := make([]VocabularyItem, 0, len(words))
	for _, word := range words {
		item := VocabularyItem{Word: word, State: "new"}
		var example Example
		if s.db.WithContext(ctx).Where("owner_id = ? AND word_id = ?", owner, word.ID).Order("created_at DESC").First(&example).Error == nil {
			item.Example = &example
		}
		var row ReviewCard
		if s.db.WithContext(ctx).Where("owner_id = ? AND word_id = ?", owner, word.ID).Order("suspended_at IS NOT NULL, updated_at DESC").First(&row).Error == nil {
			var card fsrs.Card
			if json.Unmarshal([]byte(row.FSRSCardJSON), &card) == nil {
				item.State = stateName(card.State)
				item.DueAt = &card.Due
				item.Reps, item.Lapses = card.Reps, card.Lapses
			}
			if row.SuspendedAt != nil || word.MasteredAt != nil {
				item.State = "mastered"
			}
		}
		var tags []Tag
		s.db.WithContext(ctx).Table("vocabulary_tags t").Joins("JOIN vocabulary_word_tags wt ON wt.tag_id=t.id AND wt.owner_id=t.owner_id").Where("wt.owner_id = ? AND wt.word_id = ?", owner, word.ID).Find(&tags)
		for _, tag := range tags {
			item.Tags = append(item.Tags, tag.Name)
		}
		s.db.WithContext(ctx).Table("vocabulary_wordbooks b").Joins("JOIN vocabulary_wordbook_entries e ON e.wordbook_id=b.id AND e.owner_id=b.owner_id").Where("e.owner_id = ? AND e.word_id = ?", owner, word.ID).Find(&item.Wordbooks)
		if provider == ProviderAnki && word.ProviderNoteID != "" {
			if setting, e := s.Setting(ctx, owner); e == nil {
				if client, e := newAnkiClient(setting.AnkiEndpoint); e == nil {
					if cards, e := client.cardsForNote(ctx, word.ProviderNoteID); e == nil && len(cards) > 0 {
						c := cards[0]
						item.Reps = uint64(c.Reps)
						item.Lapses = uint64(c.Lapses)
						if c.Queue < 0 {
							item.State = "suspended"
						} else if c.Reps == 0 {
							item.State = "new"
						} else {
							item.State = "review"
						}
					}
				}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) ensureCard(tx *gorm.DB, owner, wordID string, now time.Time) (ReviewCard, fsrs.Card, error) {
	return s.ensureCardType(tx, owner, wordID, "", "word_to_meaning", now)
}
func (s *Service) ensureCardType(tx *gorm.DB, owner, wordID, exampleID, cardType string, now time.Time) (ReviewCard, fsrs.Card, error) {
	var row ReviewCard
	err := tx.Where("owner_id = ? AND word_id = ? AND example_id = ? AND card_type = ?", owner, wordID, exampleID, cardType).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		card := fsrs.NewCard()
		card.Due = now
		raw, _ := json.Marshal(card)
		row = ReviewCard{ID: uuid.NewString(), OwnerID: owner, WordID: wordID, ExampleID: exampleID, CardType: cardType, SchedulerVersion: "go-fsrs/v3.3.1", ParametersVersion: "default-v3", FSRSCardJSON: string(raw), RowVersion: 1, CreatedAt: now, UpdatedAt: now}
		err = tx.Create(&row).Error
		return row, card, err
	}
	if err != nil {
		return row, fsrs.Card{}, err
	}
	var card fsrs.Card
	err = json.Unmarshal([]byte(row.FSRSCardJSON), &card)
	return row, card, err
}

func schedule(card fsrs.Card, now time.Time, parameters fsrs.Parameters) fsrs.RecordLog {
	engine := fsrs.NewFSRS(parameters)
	return engine.Repeat(card, now)
}
func (s *Service) schedulerParameters(ctx context.Context, owner string) fsrs.Parameters {
	p := fsrs.DefaultParam()
	profile, err := s.ActiveProfile(ctx, owner)
	if err == nil {
		var weights fsrs.Weights
		if json.Unmarshal([]byte(profile.WeightsJSON), &weights) == nil {
			p.W = weights
		}
		p.RequestRetention = profile.DesiredRetention
		p.MaximumInterval = float64(profile.MaximumIntervalDays)
	}
	return p
}

func (s *Service) NextQuestion(ctx context.Context, owner string) (*ReviewQuestion, error) {
	items, err := s.NextQuestions(ctx, owner, 1)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (s *Service) NextQuestions(ctx context.Context, owner string, limit int) ([]ReviewQuestion, error) {
	if err := requireLocalRuntime(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	parameters := s.schedulerParameters(ctx, owner)
	var rows []ReviewCard
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND suspended_at IS NULL", owner).Find(&rows).Error; err != nil {
		return nil, err
	}
	type candidate struct {
		row  ReviewCard
		card fsrs.Card
		rank int
	}
	var due []candidate
	for _, row := range rows {
		var card fsrs.Card
		if json.Unmarshal([]byte(row.FSRSCardJSON), &card) != nil || card.Due.After(now) {
			continue
		}
		rank := map[fsrs.State]int{fsrs.Relearning: 0, fsrs.Review: 1, fsrs.Learning: 2, fsrs.New: 3}[card.State]
		due = append(due, candidate{row, card, rank})
	}
	sort.SliceStable(due, func(i, j int) bool {
		if due[i].rank != due[j].rank {
			return due[i].rank < due[j].rank
		}
		return due[i].card.Due.Before(due[j].card.Due)
	})
	if limit < 1 {
		limit = 1
	}
	if limit > 2000 {
		limit = 2000
	}
	items := make([]ReviewQuestion, 0, limit)
	for _, candidate := range due {
		row, card := candidate.row, candidate.card
		var word Word
		if s.db.WithContext(ctx).Where("owner_id = ? AND id = ? AND mastered_at IS NULL", owner, row.WordID).First(&word).Error != nil {
			continue
		}
		item := VocabularyItem{Word: word, State: stateName(card.State), DueAt: &card.Due, CardType: row.CardType, Reps: card.Reps, Lapses: card.Lapses}
		var example Example
		if row.ExampleID != "" && s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, row.ExampleID).First(&example).Error == nil {
			item.Example = &example
		}
		options := map[string]time.Time{}
		for rating, info := range schedule(card, now, parameters) {
			options[strings.ToLower(rating.String())] = info.Card.Due
		}
		prompt, answer := word.Term, strings.TrimSpace(word.PartOfSpeech+" "+word.Meaning)
		interaction := "multiple_choice"
		if row.CardType == "meaning_to_word" {
			prompt, answer = answer, word.Term
			if row.ID[len(row.ID)-1]%2 == 0 {
				interaction = "text_input"
			}
		} else if row.CardType == "sentence_cloze" && item.Example != nil {
			prompt = strings.ReplaceAll(item.Example.Sentence, word.Term, "____")
			answer = word.Term + " — " + item.Example.Sentence
			interaction = "text_input"
		}
		var choices []ReviewChoice
		if interaction == "multiple_choice" {
			choices = reviewChoices(word, row.CardType)
		}
		publicAnswer := answer
		if interaction != "self_assessment" {
			publicAnswer = ""
		}
		items = append(items, ReviewQuestion{VocabularyItem: item, Options: options, Interaction: interaction, Choices: choices, RowVersion: row.RowVersion, CardID: row.ID, Prompt: prompt, Answer: publicAnswer, Remaining: int64(len(due) - len(items)), PreviewedAt: now})
		if len(items) >= limit {
			break
		}
	}
	return items, nil
}

func reviewChoices(word Word, cardType string) []ReviewChoice {
	rows := dictionaryReviewCandidates(word.Term, 20)
	choices := make([]ReviewChoice, 0, 6)
	used := map[string]bool{}
	if cardType == "word_to_meaning" {
		choices = append(choices, ReviewChoice{Value: word.Meaning, Label: word.Meaning, PartOfSpeech: word.PartOfSpeech})
		used[normalizeTerm(word.Meaning)] = true
		for _, row := range rows {
			key := normalizeTerm(row.Translation)
			if !used[key] {
				choices = append(choices, ReviewChoice{Value: row.Translation, Label: row.Translation, PartOfSpeech: reviewPOS(row.POS)})
				used[key] = true
			}
			if len(choices) == 6 {
				break
			}
		}
	} else {
		choices = append(choices, ReviewChoice{Value: word.Term, Label: word.Term, PartOfSpeech: word.PartOfSpeech})
		used[normalizeTerm(word.Term)] = true
		for _, row := range rows {
			key := normalizeTerm(row.Term)
			if !used[key] {
				choices = append(choices, ReviewChoice{Value: row.Term, Label: row.Term, PartOfSpeech: reviewPOS(row.POS)})
				used[key] = true
			}
			if len(choices) == 6 {
				break
			}
		}
	}
	sort.SliceStable(choices, func(i, j int) bool {
		return stableDictionaryID(word.Term, choices[i].Value) < stableDictionaryID(word.Term, choices[j].Value)
	})
	return choices
}

func reviewPOS(value string) string {
	return strings.TrimSpace(strings.Split(strings.Split(value, "/")[0], ":")[0])
}

func (s *Service) gradeResponse(ctx context.Context, owner string, req ReviewRequest) (string, error) {
	var row ReviewCard
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", req.CardID, owner).First(&row).Error; err != nil {
		return "", err
	}
	var word Word
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", row.WordID, owner).First(&word).Error; err != nil {
		return "", err
	}
	expected := word.Term
	if row.CardType == "word_to_meaning" {
		expected = word.Meaning
	}
	actual := strings.TrimSpace(req.Response)
	if strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return "good", nil
	}
	return "again", nil
}

func ratingValue(value string) (fsrs.Rating, error) {
	switch strings.ToLower(value) {
	case "again":
		return fsrs.Again, nil
	case "hard":
		return fsrs.Hard, nil
	case "good":
		return fsrs.Good, nil
	case "easy":
		return fsrs.Easy, nil
	}
	return 0, errors.New("invalid rating")
}

func (s *Service) Review(ctx context.Context, owner, wordID string, req ReviewRequest) error {
	if err := requireLocalRuntime(); err != nil {
		return err
	}
	rating, err := ratingValue(req.Rating)
	if err != nil {
		return err
	}
	now := req.PreviewedAt.UTC()
	if now.IsZero() {
		return errors.New("previewed_at is required")
	}
	parameters := s.schedulerParameters(ctx, owner)
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row ReviewCard
		query := tx.Where("owner_id = ?", owner)
		if req.CardID != "" {
			query = query.Where("id = ?", req.CardID)
		} else {
			query = query.Where("word_id = ? AND card_type = ?", wordID, "word_to_meaning")
		}
		if err := query.First(&row).Error; err != nil {
			return err
		}
		var card fsrs.Card
		err := json.Unmarshal([]byte(row.FSRSCardJSON), &card)
		if err != nil {
			return err
		}
		if req.RowVersion <= 0 || row.RowVersion != req.RowVersion {
			return errors.New("review card changed")
		}
		info := schedule(card, now, parameters)[rating]
		raw, _ := json.Marshal(info.Card)
		logRaw, _ := json.Marshal(map[string]any{
			"review_log":           info.ReviewLog,
			"interval_before_days": card.ScheduledDays,
			"interval_after_days":  info.Card.ScheduledDays,
			"due_at":               info.Card.Due,
		})
		result := tx.Model(&ReviewCard{}).Where("id = ? AND owner_id = ? AND row_version = ?", row.ID, owner, row.RowVersion).Updates(map[string]any{"fsrs_card_json": string(raw), "row_version": row.RowVersion + 1, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("review card changed")
		}
		var siblings []ReviewCard
		if err := tx.Where("owner_id = ? AND word_id = ? AND id <> ? AND suspended_at IS NULL", owner, wordID, row.ID).Find(&siblings).Error; err != nil {
			return err
		}
		for _, sibling := range siblings {
			var siblingCard fsrs.Card
			if json.Unmarshal([]byte(sibling.FSRSCardJSON), &siblingCard) == nil && !siblingCard.Due.After(now) {
				siblingCard.Due = now.Add(5 * time.Minute)
				raw, _ := json.Marshal(siblingCard)
				if err := tx.Model(&ReviewCard{}).Where("id = ?", sibling.ID).Updates(map[string]any{"fsrs_card_json": string(raw), "updated_at": now}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Table("vocabulary_review_logs").Create(map[string]any{"id": uuid.NewString(), "owner_id": owner, "card_id": row.ID, "rating": int(rating), "fsrs_log_json": string(logRaw), "reviewed_at": now, "created_at": now, "idempotency_key": req.IdempotencyKey}).Error
	})
}

func (s *Service) Master(ctx context.Context, owner, wordID string) error {
	var word Word
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, wordID).First(&word).Error; err != nil {
		return err
	}
	if word.Provider == "local" {
		if err := requireLocalRuntime(); err != nil {
			return err
		}
	}
	if word.Provider == ProviderAnki && word.ProviderNoteID != "" {
		setting, err := s.Setting(ctx, owner)
		if err != nil {
			return err
		}
		client, err := newAnkiClient(setting.AnkiEndpoint)
		if err == nil {
			err = client.suspendNote(ctx, word.ProviderNoteID)
		}
		if err != nil {
			if queueErr := s.queueMutation(ctx, owner, "master_word", word.ID, queuedMutation{NoteID: word.ProviderNoteID}, err); queueErr != nil {
				return queueErr
			}
		}
	}
	now := time.Now().UTC()
	if err := s.db.WithContext(ctx).Model(&Word{}).Where("owner_id = ? AND id = ?", owner, wordID).Update("mastered_at", now).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&ReviewCard{}).Where("owner_id = ? AND word_id = ?", owner, wordID).Update("suspended_at", now).Error
}
func (s *Service) DeleteWord(ctx context.Context, owner, wordID string) error {
	var word Word
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, wordID).First(&word).Error; err != nil {
		return err
	}
	if word.Provider == "local" {
		if err := requireLocalRuntime(); err != nil {
			return err
		}
	}
	if word.Provider == ProviderAnki && word.ProviderNoteID != "" {
		setting, err := s.Setting(ctx, owner)
		if err != nil {
			return err
		}
		client, err := newAnkiClient(setting.AnkiEndpoint)
		if err == nil {
			err = client.deleteNote(ctx, word.ProviderNoteID)
		}
		if err != nil {
			if queueErr := s.queueMutation(ctx, owner, "delete_word", word.ID, queuedMutation{NoteID: word.ProviderNoteID}, err); queueErr != nil {
				return queueErr
			}
		}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&ReviewCard{})
		tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&Example{})
		tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&SourceRef{})
		return tx.Where("owner_id = ? AND id = ?", owner, wordID).Delete(&Word{}).Error
	})
}
