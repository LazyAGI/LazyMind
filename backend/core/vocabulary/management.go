package vocabulary

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *Service) GetWord(ctx context.Context, owner, id string) (VocabularyItem, error) {
	var word Word
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, id).First(&word).Error; err != nil {
		return VocabularyItem{}, err
	}
	if word.Provider == "local" {
		if err := requireLocalRuntime(); err != nil {
			return VocabularyItem{}, err
		}
	}
	items, err := s.ListWords(ctx, owner, word.Provider)
	if err != nil {
		return VocabularyItem{}, err
	}
	for _, item := range items {
		if item.Word.ID == id {
			return item, nil
		}
	}
	return VocabularyItem{}, gorm.ErrRecordNotFound
}

func (s *Service) SearchWords(ctx context.Context, owner string, q WordQuery) ([]VocabularyItem, error) {
	if err := validateProvider(q.Provider); err != nil {
		return nil, err
	}
	if q.Provider == "local" {
		if err := requireLocalRuntime(); err != nil {
			return nil, err
		}
	}
	items, err := s.ListWords(ctx, owner, q.Provider)
	if err != nil {
		return nil, err
	}
	match := func(values []string, tags []string, all bool) bool {
		for _, want := range values {
			found := false
			for _, tag := range tags {
				if normalizeTag(tag) == normalizeTag(want) {
					found = true
					break
				}
			}
			if all && !found {
				return false
			}
			if !all && found {
				return true
			}
		}
		return all || len(values) == 0
	}
	out := make([]VocabularyItem, 0, len(items))
	needle := strings.ToLower(strings.TrimSpace(q.Search))
	for _, item := range items {
		if needle != "" && !strings.Contains(strings.ToLower(item.Word.Term+" "+item.Word.Meaning+" "+item.Word.Definition), needle) {
			continue
		}
		if q.State != "" && item.State != q.State {
			continue
		}
		if q.DocumentID != "" {
			var n int64
			s.db.WithContext(ctx).Model(&SourceRef{}).Where("owner_id = ? AND word_id = ? AND document_id = ?", owner, item.Word.ID, q.DocumentID).Count(&n)
			if n == 0 {
				continue
			}
		}
		if q.WordbookID != "" {
			found := false
			for _, book := range item.Wordbooks {
				found = found || book.ID == q.WordbookID
			}
			if !found {
				continue
			}
		}
		if !match(q.TagsAll, item.Tags, true) || len(q.TagsAny) > 0 && !match(q.TagsAny, item.Tags, false) || match(q.TagsNot, item.Tags, false) && len(q.TagsNot) > 0 {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) UpdateWord(ctx context.Context, owner, id string, in WordUpdate) (Word, error) {
	var word Word
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, id).First(&word).Error; err != nil {
		return word, err
	}
	if word.Provider == "local" {
		if err := requireLocalRuntime(); err != nil {
			return word, err
		}
	}
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if in.Term != nil {
		updates["term"] = strings.TrimSpace(*in.Term)
		updates["normalized_term"] = normalizeTerm(*in.Term)
	}
	if in.Phonetic != nil {
		updates["phonetic"] = *in.Phonetic
	}
	if in.PartOfSpeech != nil {
		updates["part_of_speech"] = *in.PartOfSpeech
	}
	if in.Meaning != nil {
		updates["meaning"] = *in.Meaning
	}
	if in.Definition != nil {
		updates["definition"] = *in.Definition
	}
	if in.UserNote != nil {
		updates["user_note"] = *in.UserNote
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Word{}).Where("owner_id = ? AND id = ?", owner, id).Updates(updates).Error; err != nil {
			return err
		}
		if in.WordbookIDs != nil {
			if err := tx.Where("owner_id = ? AND word_id = ?", owner, id).Delete(&WordbookEntry{}).Error; err != nil {
				return err
			}
			if err := s.attachWordbooks(tx, owner, id, in.WordbookIDs); err != nil {
				return err
			}
		}
		if in.Tags != nil {
			if err := s.setTags(tx, owner, id, in.Tags, true); err != nil {
				return err
			}
		}
		if err := tx.Where("owner_id = ? AND id = ?", owner, id).First(&word).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return word, err
	}
	if word.Provider == ProviderAnki && word.ProviderNoteID != "" {
		fields := map[string]string{"Word": word.Term, "Phonetic": word.Phonetic, "PartOfSpeech": word.PartOfSpeech, "Meaning": word.Meaning, "Definition": word.Definition, "UserNote": word.UserNote}
		setting, settingErr := s.Setting(ctx, owner)
		if settingErr != nil {
			return word, settingErr
		}
		client, clientErr := newAnkiClient(setting.AnkiEndpoint)
		if clientErr == nil {
			clientErr = client.updateNote(ctx, word.ProviderNoteID, fields)
		}
		if clientErr != nil {
			if queueErr := s.queueMutation(ctx, owner, "update_word", word.ID, queuedMutation{NoteID: word.ProviderNoteID, Fields: fields, Tags: in.Tags}, clientErr); queueErr != nil {
				return word, queueErr
			}
		}
	}
	return word, nil
}

func (s *Service) ResetWord(ctx context.Context, owner, id string) error {
	if err := requireLocalRuntime(); err != nil {
		return err
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_id = ? AND word_id = ?", owner, id).Delete(&ReviewCard{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Word{}).Where("owner_id = ? AND id = ?", owner, id).Update("mastered_at", nil).Error; err != nil {
			return err
		}
		_, _, err := s.ensureCard(tx, owner, id, now)
		return err
	})
}

// ResetWords returns the selected words to their initial learning state. Scope may
// be "today" (words reviewed since local midnight) or "wordbook".
func (s *Service) ResetWords(ctx context.Context, owner, scope, wordbookID string) (int64, error) {
	if err := requireLocalRuntime(); err != nil {
		return 0, err
	}
	var ids []string
	query := s.db.WithContext(ctx).Model(&Word{}).Where("owner_id = ?", owner)
	switch scope {
	case "today":
		now := time.Now()
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UTC()
		query = query.Where("id IN (?)", s.db.Model(&ReviewLogRow{}).Select("DISTINCT word_id").Where("owner_id = ? AND reviewed_at >= ?", owner, start))
	case "wordbook":
		if wordbookID == "" {
			return 0, errors.New("wordbook_id is required")
		}
		query = query.Where("id IN (?)", s.db.Model(&WordbookEntry{}).Select("word_id").Where("owner_id = ? AND wordbook_id = ?", owner, wordbookID))
	default:
		return 0, errors.New("invalid reset scope")
	}
	if err := query.Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := s.ResetWord(ctx, owner, id); err != nil {
			return 0, err
		}
	}
	return int64(len(ids)), nil
}
func (s *Service) ResumeWord(ctx context.Context, owner, id string) error {
	if err := requireLocalRuntime(); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&Word{}).Where("owner_id = ? AND id = ?", owner, id).Update("mastered_at", nil).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&ReviewCard{}).Where("owner_id = ? AND word_id = ?", owner, id).Update("suspended_at", nil).Error
}

func (s *Service) RemoveDocumentSource(ctx context.Context, owner, documentID, wordID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var refs []SourceRef
		if err := tx.Where("owner_id = ? AND document_id = ? AND word_id = ?", owner, documentID, wordID).Find(&refs).Error; err != nil {
			return err
		}
		for _, ref := range refs {
			if ref.ExampleID == "" {
				continue
			}
			var count int64
			tx.Model(&SourceRef{}).Where("owner_id = ? AND example_id = ? AND id <> ?", owner, ref.ExampleID, ref.ID).Count(&count)
			if count == 0 {
				tx.Where("owner_id = ? AND id = ? AND content_origin = ?", owner, ref.ExampleID, "document").Delete(&Example{})
			}
		}
		return tx.Where("owner_id = ? AND document_id = ? AND word_id = ?", owner, documentID, wordID).Delete(&SourceRef{}).Error
	})
}

func (s *Service) DeleteDocumentWord(ctx context.Context, owner, documentID, wordID string) error {
	var word Word
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, wordID).First(&word).Error; err != nil {
		return err
	}
	var current, other int64
	s.db.WithContext(ctx).Model(&SourceRef{}).Where("owner_id = ? AND word_id = ? AND document_id = ?", owner, wordID, documentID).Count(&current)
	s.db.WithContext(ctx).Model(&SourceRef{}).Where("owner_id = ? AND word_id = ? AND document_id <> ?", owner, wordID, documentID).Count(&other)
	if current == 0 {
		return errors.New("word is not sourced from this document")
	}
	if other > 0 || word.OriginType == "user" {
		return errors.New("word has other sources; remove this document source instead")
	}
	return s.DeleteWord(ctx, owner, wordID)
}

func validateProvider(value string) error {
	if value != ProviderAnki && value != "local" {
		return errors.New("invalid vocabulary provider")
	}
	return nil
}
