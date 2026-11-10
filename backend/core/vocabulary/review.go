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

func (s *Service) addLocalWord(ctx context.Context, owner string, req AddWordRequest, mirrorAnki bool) (AddWordResult, error) {
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
		// Serialize repeated saves for the same word because example identity
		// is enforced by the lookup, without a unique database constraint.
		lockWord := func() (Word, error) {
			return findStoredWord(tx.Clauses(clause.Locking{Strength: "UPDATE"}), owner, req.Language, normalized)
		}
		word, err := lockWord()
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
			insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&word)
			if insert.Error != nil {
				return insert.Error
			}
			if insert.RowsAffected == 0 {
				word, err = lockWord()
				if err != nil {
					return err
				}
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
			attrs := Example{ID: uuid.NewString(), OwnerID: owner, WordID: word.ID, Sentence: sentence, Translation: req.Translation, ContentOrigin: defaultString(req.OriginType, "document"), CreatedAt: now, UpdatedAt: now}
			var ex Example
			if err := tx.Where("owner_id = ? AND word_id = ? AND sentence = ?", owner, word.ID, sentence).
				Order("created_at ASC").Attrs(attrs).FirstOrCreate(&ex).Error; err != nil {
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
		if err != nil {
			return err
		}
		result = AddWordResult{Word: word, Example: example}
		result.CloudQueued, err = enqueueCloudConnections(ctx, tx, word)
		if err != nil || !mirrorAnki {
			return err
		}
		result.Queued, err = enqueueAnkiWord(ctx, tx, word)
		return err
	})
	return result, err
}

func findStoredWord(tx *gorm.DB, owner, language, normalized string) (Word, error) {
	var word Word
	err := tx.Where("owner_id = ? AND language = ? AND normalized_term = ?", owner, language, normalized).
		Order("CASE WHEN provider = 'local' THEN 0 ELSE 1 END").First(&word).Error
	return word, err
}

func (s *Service) ListWords(ctx context.Context, owner, provider string) ([]VocabularyItem, error) {
	if err := s.recoverLegacyAnkiAdds(ctx, owner); err != nil {
		return nil, err
	}
	var words []Word
	query := s.db.WithContext(ctx).Where("owner_id = ?", owner)
	if provider != "" {
		if err := validateProvider(provider); err != nil {
			return nil, err
		}
	}
	if err := query.Order("created_at DESC").Find(&words).Error; err != nil {
		return nil, err
	}
	if err := s.ensureLegacyWordbooks(ctx, owner); err != nil {
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
		if word.MasteredAt != nil {
			item.State = "mastered"
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
	find := func() error {
		return tx.Where("owner_id = ? AND word_id = ? AND example_id = ? AND card_type = ?", owner, wordID, exampleID, cardType).First(&row).Error
	}
	err := find()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		card := fsrs.NewCard()
		card.Due = now
		raw, _ := json.Marshal(card)
		row = ReviewCard{ID: uuid.NewString(), OwnerID: owner, WordID: wordID, ExampleID: exampleID, CardType: cardType, SchedulerVersion: "go-fsrs/v3.3.1", ParametersVersion: "default-v3", FSRSCardJSON: string(raw), RowVersion: 1, CreatedAt: now, UpdatedAt: now}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil || result.RowsAffected != 0 {
			return row, card, result.Error
		}
		row = ReviewCard{}
		err = find()
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
	return s.nextQuestions(ctx, owner, limit, "", "")
}

func (s *Service) nextQuestions(ctx context.Context, owner string, limit int, wordbookID, sessionID string) ([]ReviewQuestion, error) {
	if err := s.recoverLegacyAnkiAdds(ctx, owner); err != nil {
		return nil, err
	}
	if err := s.ensureLegacyWordbooks(ctx, owner); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if err := s.prepareLegacyReviewCards(ctx, owner, now); err != nil {
		return nil, err
	}
	parameters := s.schedulerParameters(ctx, owner)
	db := s.db.WithContext(ctx)
	query := db.Where("owner_id = ? AND suspended_at IS NULL", owner)
	if wordbookID != "" {
		query = query.Where("word_id IN (?)", wordbookMembers(db, owner, wordbookID))
	}
	var pendingItems []ReviewSessionItem
	if sessionID != "" {
		activeWords := db.Model(&Word{}).Select("id").Where("owner_id = ? AND mastered_at IS NULL AND archived_at IS NULL", owner)
		query = query.Where("word_id IN (?)", activeWords)
		sessionItems := func() *gorm.DB {
			return db.Model(&ReviewSessionItem{}).Where("owner_id = ? AND session_id = ?", owner, sessionID)
		}
		seenWords := sessionItems().Select("word_id")
		pending := pendingSessionItems(db, owner, sessionID)
		if err := pending.Select("id", "card_id").Find(&pendingItems).Error; err != nil {
			return nil, err
		}
		pendingCards := pendingSessionItems(db, owner, sessionID).Select("card_id")
		query = query.Where("(word_id NOT IN (?) OR id IN (?))", seenWords, pendingCards)
	}
	var rows []ReviewCard
	if err := query.Find(&rows).Error; err != nil {
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
	if len(pendingItems) > 0 {
		eligible := make(map[string]bool, len(due))
		for _, candidate := range due {
			eligible[candidate.row.ID] = true
		}
		if err := releaseIneligibleSessionItems(db, owner, sessionID, pendingItems, eligible); err != nil {
			return nil, err
		}
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
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var word Word
		if err := tx.Where("owner_id = ? AND id = ?", owner, wordID).First(&word).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&word).Update("mastered_at", now).Error; err != nil {
			return err
		}
		if err := tx.Model(&ReviewCard{}).Where("owner_id = ? AND word_id = ?", owner, wordID).Update("suspended_at", now).Error; err != nil {
			return err
		}
		return enqueueAnkiMutation(ctx, tx, word, "master_word")
	})
}

func (s *Service) DeleteWord(ctx context.Context, owner, wordID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return deleteLocalWordData(tx, owner, wordID)
	})
}

// Older Anki words were already stored in LazyMind but did not receive local
// review cards. Prepare missing cards in place, preserving the word IDs and
// their existing document, example, and tag associations.
func (s *Service) prepareLegacyReviewCards(ctx context.Context, owner string, now time.Time) error {
	db := s.db.WithContext(ctx)
	var words []Word
	cards := db.Model(&ReviewCard{}).Select("word_id").Where("owner_id = ? AND card_type = ?", owner, "word_to_meaning")
	err := db.Where("owner_id = ? AND provider = ? AND mastered_at IS NULL AND archived_at IS NULL AND id NOT IN (?)", owner, ProviderAnki, cards).Find(&words).Error
	if err != nil || len(words) == 0 {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		for _, word := range words {
			if _, _, err := s.ensureCard(tx, owner, word.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Anki-origin words from older releases have no local wordbook membership.
func (s *Service) ensureLegacyWordbooks(ctx context.Context, owner string) error {
	db := s.db.WithContext(ctx)
	var ids []string
	entries := db.Model(&WordbookEntry{}).Select("word_id").Where("owner_id = ?", owner)
	err := db.Model(&Word{}).Where("owner_id = ? AND provider = ? AND id NOT IN (?)", owner, ProviderAnki, entries).Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		book, err := ensureDefaultWordbook(tx, owner)
		if err != nil {
			return err
		}
		rows := make([]WordbookEntry, 0, len(ids))
		now := time.Now().UTC()
		for _, id := range ids {
			rows = append(rows, WordbookEntry{OwnerID: owner, WordbookID: book.ID, WordID: id, CreatedAt: now})
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(rows, 100).Error
	})
}
