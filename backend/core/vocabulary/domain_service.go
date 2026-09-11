package vocabulary

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultWordbookName = "默认生词本"

func requireLocalRuntime() error {
	if !Enabled() {
		return errors.New("vocabulary feature is disabled")
	}
	return nil
}

func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_VOCABULARY_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Service) EnsureDefaultWordbook(ctx context.Context, owner string) (Wordbook, error) {
	var book Wordbook
	err := s.db.WithContext(ctx).Where("owner_id = ? AND name = ?", owner, defaultWordbookName).First(&book).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now().UTC()
		book = Wordbook{ID: uuid.NewString(), OwnerID: owner, Name: defaultWordbookName, CreatedAt: now, UpdatedAt: now}
		err = s.db.WithContext(ctx).Create(&book).Error
	}
	return book, err
}
func (s *Service) ListWordbooks(ctx context.Context, owner string) ([]Wordbook, error) {
	if err := requireLocalRuntime(); err != nil {
		return nil, err
	}
	if _, err := s.EnsureDefaultWordbook(ctx, owner); err != nil {
		return nil, err
	}
	var rows []Wordbook
	err := s.db.WithContext(ctx).Where("owner_id = ?", owner).Order("archived_at IS NOT NULL, created_at").Find(&rows).Error
	return rows, err
}
func (s *Service) CreateWordbook(ctx context.Context, owner string, in WordbookInput) (Wordbook, error) {
	if err := requireLocalRuntime(); err != nil {
		return Wordbook{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return Wordbook{}, errors.New("wordbook name is required")
	}
	now := time.Now().UTC()
	row := Wordbook{ID: uuid.NewString(), OwnerID: owner, Name: in.Name, Description: strings.TrimSpace(in.Description), CreatedAt: now, UpdatedAt: now}
	return row, s.db.WithContext(ctx).Create(&row).Error
}
func (s *Service) UpdateWordbook(ctx context.Context, owner, id string, in WordbookInput) (Wordbook, error) {
	if err := requireLocalRuntime(); err != nil {
		return Wordbook{}, err
	}
	updates := map[string]any{"name": strings.TrimSpace(in.Name), "description": strings.TrimSpace(in.Description), "updated_at": time.Now().UTC()}
	if in.Archived {
		updates["archived_at"] = time.Now().UTC()
	} else {
		updates["archived_at"] = nil
	}
	if err := s.db.WithContext(ctx).Model(&Wordbook{}).Where("owner_id = ? AND id = ?", owner, id).Updates(updates).Error; err != nil {
		return Wordbook{}, err
	}
	var row Wordbook
	err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, id).First(&row).Error
	return row, err
}
func (s *Service) DeleteWordbook(ctx context.Context, owner, id, mode, targetID string) error {
	if err := requireLocalRuntime(); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var book Wordbook
		if err := tx.Where("owner_id = ? AND id = ?", owner, id).First(&book).Error; err != nil {
			return err
		}
		if book.Name == defaultWordbookName {
			return errors.New("default wordbook cannot be deleted")
		}
		var entries []WordbookEntry
		if err := tx.Where("owner_id = ? AND wordbook_id = ?", owner, id).Find(&entries).Error; err != nil {
			return err
		}
		if mode == "delete_words" {
			for _, entry := range entries {
				if err := deleteLocalWordData(tx, owner, entry.WordID); err != nil {
					return err
				}
			}
		} else {
			if targetID == "" {
				var target Wordbook
				if err := tx.Where("owner_id = ? AND name = ?", owner, defaultWordbookName).First(&target).Error; err != nil {
					return err
				}
				targetID = target.ID
			}
			if targetID == id {
				return errors.New("target wordbook must be different")
			}
			var target Wordbook
			if err := tx.Where("owner_id = ? AND id = ?", owner, targetID).First(&target).Error; err != nil {
				return err
			}
			for _, entry := range entries {
				moved := WordbookEntry{OwnerID: owner, WordbookID: targetID, WordID: entry.WordID, CreatedAt: time.Now().UTC()}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&moved).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Where("owner_id = ? AND wordbook_id = ?", owner, id).Delete(&WordbookEntry{}).Error; err != nil {
			return err
		}
		return tx.Where("owner_id = ? AND id = ?", owner, id).Delete(&Wordbook{}).Error
	})
}

func deleteLocalWordData(tx *gorm.DB, owner, wordID string) error {
	var examples []Example
	if err := tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Find(&examples).Error; err != nil {
		return err
	}
	for _, example := range examples {
		if err := tx.Where("owner_id = ? AND example_id = ?", owner, example.ID).Delete(&ExampleTag{}).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&WordTag{}).Error; err != nil {
		return err
	}
	if err := tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&WordbookEntry{}).Error; err != nil {
		return err
	}
	var cardIDs []string
	if err := tx.Model(&ReviewCard{}).Where("owner_id = ? AND word_id = ?", owner, wordID).Pluck("id", &cardIDs).Error; err != nil {
		return err
	}
	if len(cardIDs) > 0 {
		if err := tx.Where("owner_id = ? AND card_id IN ?", owner, cardIDs).Delete(&ReviewLogRow{}).Error; err != nil {
			return err
		}
	}
	if err := tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&ReviewCard{}).Error; err != nil {
		return err
	}
	if err := tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&Example{}).Error; err != nil {
		return err
	}
	if err := tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&SourceRef{}).Error; err != nil {
		return err
	}
	return tx.Where("owner_id = ? AND id = ?", owner, wordID).Delete(&Word{}).Error
}

func (s *Service) attachWordbooks(tx *gorm.DB, owner, wordID string, ids []string) error {
	if len(ids) == 0 {
		var book Wordbook
		err := tx.Where("owner_id = ? AND name = ?", owner, defaultWordbookName).First(&book).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			now := time.Now().UTC()
			book = Wordbook{ID: uuid.NewString(), OwnerID: owner, Name: defaultWordbookName, CreatedAt: now, UpdatedAt: now}
			err = tx.Create(&book).Error
		}
		if err != nil {
			return err
		}
		ids = []string{book.ID}
	}
	for _, id := range ids {
		row := WordbookEntry{OwnerID: owner, WordbookID: id, WordID: wordID, CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) setExampleTags(tx *gorm.DB, owner, exampleID string, names []string) error {
	for _, raw := range names {
		name := normalizeTag(raw)
		if name == "" {
			continue
		}
		tag := Tag{ID: uuid.NewString(), OwnerID: owner, Name: name, CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tag).Error; err != nil {
			return err
		}
		if err := tx.Where("owner_id = ? AND name = ?", owner, name).First(&tag).Error; err != nil {
			return err
		}
		link := ExampleTag{OwnerID: owner, ExampleID: exampleID, TagID: tag.ID, CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
			return err
		}
	}
	return nil
}
func normalizeTag(v string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(v)), "_"))
}
func (s *Service) setTags(tx *gorm.DB, owner, wordID string, names []string, replace bool) error {
	if replace {
		if err := tx.Where("owner_id = ? AND word_id = ?", owner, wordID).Delete(&WordTag{}).Error; err != nil {
			return err
		}
	}
	for _, raw := range names {
		name := normalizeTag(raw)
		if name == "" {
			continue
		}
		tag := Tag{ID: uuid.NewString(), OwnerID: owner, Name: name, CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tag).Error; err != nil {
			return err
		}
		if err := tx.Where("owner_id = ? AND name = ?", owner, name).First(&tag).Error; err != nil {
			return err
		}
		link := WordTag{OwnerID: owner, WordID: wordID, TagID: tag.ID, CreatedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) LookupDictionary(ctx context.Context, language, term string) ([]DictionaryEntry, error) {
	if language == "" {
		language = "en"
	}
	rows, err := bundledDictionaries.Lookup(ctx, language, term)
	if err != nil {
		return nil, err
	}
	var imported []DictionaryEntry
	if err := s.db.WithContext(ctx).Where("language = ? AND normalized_term = ?", language, normalizeTerm(term)).Order("priority, source_name").Find(&imported).Error; err != nil {
		return nil, err
	}
	for i := range imported {
		s.db.WithContext(ctx).Where("entry_id = ?", imported[i].ID).Order("sense_order").Find(&imported[i].Senses)
		s.db.WithContext(ctx).Where("entry_id = ?", imported[i].ID).Order("example_order").Find(&imported[i].Examples)
	}
	rows = append(rows, imported...)
	return rows, nil
}

func extractSentence(term, contextText string) string {
	contextText = strings.TrimSpace(contextText)
	if contextText == "" {
		return strings.TrimSpace(term)
	}
	lower, needle := strings.ToLower(contextText), strings.ToLower(strings.TrimSpace(term))
	at := strings.Index(lower, needle)
	if at < 0 {
		return contextText
	}
	start, end := at, at+len(needle)
	for start > 0 {
		r, size := utf8DecodeLast(contextText[:start])
		if strings.ContainsRune(".!?。！？\n", r) {
			break
		}
		start -= size
	}
	for end < len(contextText) {
		r, size := utf8DecodeFirst(contextText[end:])
		end += size
		if strings.ContainsRune(".!?。！？\n", r) {
			break
		}
	}
	return strings.TrimSpace(contextText[start:end])
}
func utf8DecodeLast(v string) (rune, int) {
	rs := []rune(v)
	if len(rs) == 0 {
		return 0, 0
	}
	r := rs[len(rs)-1]
	return r, len(string(r))
}
func utf8DecodeFirst(v string) (rune, int) {
	for _, r := range v {
		return r, len(string(r))
	}
	return 0, 0
}

func (s *Service) ResolveSelection(ctx context.Context, owner string, in SelectionResolveRequest) (SelectionResolveResult, error) {
	term := strings.TrimFunc(strings.TrimSpace(in.Term), func(r rune) bool { return unicode.IsPunct(r) })
	if term == "" {
		return SelectionResolveResult{}, errors.New("term is required")
	}
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return SelectionResolveResult{}, err
	}
	provider := in.Provider
	if provider == "" {
		provider = setting.SelectedProvider
	}
	if provider == "local" {
		if err = requireLocalRuntime(); err != nil {
			return SelectionResolveResult{}, err
		}
	}
	dictionary, err := s.LookupDictionary(ctx, in.Language, term)
	if err != nil {
		return SelectionResolveResult{}, err
	}
	var existing Word
	if s.db.WithContext(ctx).Where("owner_id = ? AND provider = ? AND language = ? AND normalized_term = ?", owner, provider, defaultString(in.Language, "en"), normalizeTerm(term)).First(&existing).Error != nil {
		existing = Word{}
	}
	var books []Wordbook
	if provider == ProviderAnki {
		decks, deckErr := s.ListAnkiDecks(ctx, owner)
		if deckErr != nil {
			return SelectionResolveResult{}, deckErr
		}
		for _, deck := range decks {
			books = append(books, Wordbook{ID: deck.Name, Name: deck.Name})
		}
	} else {
		books, err = s.ListWordbooks(ctx, owner)
		if err != nil {
			return SelectionResolveResult{}, err
		}
	}
	result := SelectionResolveResult{Term: term, Sentence: extractSentence(term, in.Context), Provider: provider, Dictionary: dictionary, Wordbooks: books}
	if existing.ID != "" {
		result.Existing = &existing
	}
	return result, nil
}
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) AddExample(ctx context.Context, owner, wordID string, in ExampleInput) (Example, error) {
	if strings.TrimSpace(in.Sentence) == "" {
		return Example{}, errors.New("sentence is required")
	}
	var word Word
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, wordID).First(&word).Error; err != nil {
		return Example{}, err
	}
	if word.Provider == "local" {
		if err := requireLocalRuntime(); err != nil {
			return Example{}, err
		}
	}
	now := time.Now().UTC()
	ex := Example{ID: uuid.NewString(), OwnerID: owner, WordID: wordID, Sentence: strings.TrimSpace(in.Sentence), Translation: strings.TrimSpace(in.Translation), ContentOrigin: defaultString(in.ContentOrigin, "user"), CreatedAt: now, UpdatedAt: now}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&ex).Error; err != nil {
			return err
		}
		if err := s.setExampleTags(tx, owner, ex.ID, in.Tags); err != nil {
			return err
		}
		if word.Provider == "local" {
			_, _, err := s.ensureCardType(tx, owner, wordID, ex.ID, "sentence_cloze", now)
			return err
		}
		return nil
	})
	return ex, err
}

func (s *Service) UpdateExample(ctx context.Context, owner, id string, in ExampleInput) (Example, error) {
	var ex Example
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, id).First(&ex).Error; err != nil {
		return ex, err
	}
	updates := map[string]any{"sentence": strings.TrimSpace(in.Sentence), "translation": strings.TrimSpace(in.Translation), "updated_at": time.Now().UTC()}
	if in.ContentOrigin != "" {
		updates["content_origin"] = in.ContentOrigin
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ex).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("owner_id = ? AND example_id = ?", owner, id).Delete(&ExampleTag{}).Error; err != nil {
			return err
		}
		if err := s.setExampleTags(tx, owner, id, in.Tags); err != nil {
			return err
		}
		return tx.Where("owner_id = ? AND id = ?", owner, id).First(&ex).Error
	})
	return ex, err
}

func (s *Service) DeleteExample(ctx context.Context, owner, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_id = ? AND example_id = ?", owner, id).Delete(&ExampleTag{}).Error; err != nil {
			return err
		}
		if err := tx.Where("owner_id = ? AND example_id = ?", owner, id).Delete(&ReviewCard{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&SourceRef{}).Where("owner_id = ? AND example_id = ?", owner, id).Update("example_id", nil).Error; err != nil {
			return err
		}
		return tx.Where("owner_id = ? AND id = ?", owner, id).Delete(&Example{}).Error
	})
}

func (s *Service) RemoveWordTag(ctx context.Context, owner, wordID, name string) error {
	return s.db.WithContext(ctx).Exec("DELETE FROM vocabulary_word_tags WHERE owner_id = ? AND word_id = ? AND tag_id IN (SELECT id FROM vocabulary_tags WHERE owner_id = ? AND name = ?)", owner, wordID, owner, normalizeTag(name)).Error
}
