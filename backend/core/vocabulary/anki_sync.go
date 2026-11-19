package vocabulary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Only Anki operations use this lock. Vocabulary requests never wait for it.
var ankiFlushMu sync.Mutex

type ankiWordMirror struct {
	WordID string `json:"word_id"`
	Deck   string `json:"deck,omitempty"`
}

func enqueueAnkiWord(ctx context.Context, tx *gorm.DB, word Word) (bool, error) {
	if !ankiSyncAvailable() {
		return false, nil
	}
	setting, err := New(tx).Setting(ctx, word.OwnerID)
	if err != nil || !setting.AnkiSyncEnabled {
		return false, err
	}
	err = createAnkiOperation(tx, word.OwnerID, "mirror_word", ankiWordMirror{WordID: word.ID})
	return err == nil, err
}

func enqueueAnkiMutation(ctx context.Context, tx *gorm.DB, word Word, kind string) error {
	if word.ProviderNoteID == "" || !ankiSyncAvailable() {
		return nil
	}
	setting, err := New(tx).Setting(ctx, word.OwnerID)
	if err != nil || !setting.AnkiSyncEnabled {
		return err
	}
	return createAnkiOperation(tx, word.OwnerID, kind, queuedMutation{NoteID: word.ProviderNoteID, WordID: word.ID})
}

func createAnkiOperation(tx *gorm.DB, owner, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	id := uuid.NewString()
	op := providerOperation{ID: id, OwnerID: owner, Provider: ProviderAnki, OperationType: kind, PayloadJSON: string(raw), IdempotencyKey: id, Status: "pending"}
	return tx.Create(&op).Error
}

// A stored Anki note ID belongs to the collection at its original endpoint.
// Keep the endpoint fixed while mappings or pending operations exist so an
// update or deletion cannot be sent to a different collection with that ID.
func checkAnkiEndpointChange(ctx context.Context, tx *gorm.DB, owner, endpoint string) error {
	setting, err := New(tx).Setting(ctx, owner)
	if err != nil || setting.AnkiEndpoint == endpoint {
		return err
	}
	var linked bool
	err = tx.Raw(`SELECT EXISTS (SELECT 1 FROM vocabulary_words WHERE owner_id = ? AND provider_note_id <> '')
		OR EXISTS (SELECT 1 FROM vocabulary_examples WHERE owner_id = ? AND provider_note_id <> '')
		OR EXISTS (SELECT 1 FROM vocabulary_provider_operations WHERE owner_id = ? AND provider = ? AND status = ?)`,
		owner, owner, owner, ProviderAnki, "pending").Scan(&linked).Error
	if err == nil && linked {
		err = errors.New("Anki endpoint cannot change while synced notes or pending operations exist")
	}
	return err
}

func (s *Service) flushPending(ctx context.Context, owner string) error {
	ankiFlushMu.Lock()
	defer ankiFlushMu.Unlock()
	setting, err := s.ankiSetting(ctx, owner)
	if err != nil {
		return err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return err
	}
	if err := s.recoverLegacyAnkiAdds(ctx, owner); err != nil {
		return err
	}
	var operations []providerOperation
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND provider = ? AND status = ?", owner, ProviderAnki, "pending").Order("created_at ASC, id ASC").Find(&operations).Error; err != nil {
		return err
	}
	for _, op := range operations {
		current, err := s.ankiSetting(ctx, owner)
		if err != nil {
			return err
		}
		if current.AnkiEndpoint != setting.AnkiEndpoint {
			return errors.New("Anki endpoint changed during sync")
		}
		err = s.applyAnkiOperation(ctx, owner, current.AnkiDeckName, client, op)
		if err != nil {
			_ = s.db.WithContext(ctx).Model(&op).Updates(map[string]any{"last_error": err.Error(), "retry_count": op.RetryCount + 1}).Error
			return fmt.Errorf("Anki sync operation %s: %w", op.OperationType, err)
		}
		if err := s.db.WithContext(ctx).Model(&op).Updates(map[string]any{"status": "completed", "last_error": ""}).Error; err != nil {
			return err
		}
	}
	return nil
}

// Legacy offline adds stored the only copy of the word in the Anki outbox.
// Recover them locally even if Anki is disabled, then retain the optional
// mirror job. Claiming and replacing each job in one transaction makes
// retries safe and prevents two readers from materializing the same request.
func (s *Service) recoverLegacyAnkiAdds(ctx context.Context, owner string) error {
	db := s.db.WithContext(ctx)
	var operations []providerOperation
	err := db.Where("owner_id = ? AND provider = ? AND operation_type = ? AND status = ?", owner, ProviderAnki, "add_word", "pending").
		Order("created_at ASC, id ASC").Find(&operations).Error
	if err != nil {
		return err
	}
	for _, op := range operations {
		err := db.Transaction(func(tx *gorm.DB) error {
			claim := tx.Model(&op).Where("operation_type = ? AND status = ?", "add_word", "pending").Update("operation_type", "mirror_word")
			if claim.Error != nil || claim.RowsAffected == 0 {
				return claim.Error
			}
			var req AddWordRequest
			err := json.Unmarshal([]byte(op.PayloadJSON), &req)
			if err != nil || normalizeTerm(req.Term) == "" {
				return tx.Model(&op).Updates(map[string]any{"operation_type": "add_word", "status": "failed", "last_error": "invalid legacy vocabulary request"}).Error
			}
			var mirror ankiWordMirror
			if len(req.WordbookIDs) > 0 {
				mirror.Deck = req.WordbookIDs[0]
			}
			req.WordbookIDs = nil
			req.Language = defaultString(req.Language, "en")
			result, err := New(tx).addLocalWord(ctx, owner, req, false)
			if err != nil {
				return err
			}
			mirror.WordID = result.Word.ID
			raw, err := json.Marshal(mirror)
			if err != nil {
				return err
			}
			return tx.Model(&op).Update("payload_json", string(raw)).Error
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) applyAnkiOperation(ctx context.Context, owner, deck string, client *ankiClient, op providerOperation) error {
	switch op.OperationType {
	case "mirror_word":
		var mirror ankiWordMirror
		if err := json.Unmarshal([]byte(op.PayloadJSON), &mirror); err != nil {
			return err
		}
		return s.mirrorAnkiWord(ctx, owner, defaultString(mirror.Deck, deck), mirror.WordID, client)
	}
	var mutation queuedMutation
	if err := json.Unmarshal([]byte(op.PayloadJSON), &mutation); err != nil {
		return err
	}
	noteID, err := s.resolveAnkiMutationNote(ctx, owner, client, mutation)
	if err != nil || noteID == "" {
		return err
	}
	switch op.OperationType {
	case "update_word":
		if err := client.updateNote(ctx, noteID, mutation.Fields); err != nil {
			return err
		}
		if len(mutation.Tags) > 0 {
			return client.updateTags(ctx, noteID, sanitizeTags(mutation.Tags, "vocabulary"))
		}
	case "delete_word":
		return client.deleteNote(ctx, noteID)
	case "master_word":
		return client.suspendNote(ctx, noteID)
	default:
		return fmt.Errorf("unsupported queued Anki operation %q", op.OperationType)
	}
	return nil
}

func (s *Service) resolveAnkiMutationNote(ctx context.Context, owner string, client *ankiClient, mutation queuedMutation) (string, error) {
	wordID := mutation.WordID
	if wordID == "" {
		// Old mutation payloads identify only the numeric Anki note ID.
		// Look up the word when still present so the remote note can be
		// verified. A legacy delete with no local identity is unsafe to send.
		var word Word
		err := s.db.WithContext(ctx).Where("owner_id = ? AND provider_note_id = ?", owner, mutation.NoteID).First(&word).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		wordID = word.ID
	}
	return client.findMirrorNote(ctx, vocabularyModel, wordID)
}

func (s *Service) mirrorAnkiWord(ctx context.Context, owner, deck, wordID string, client *ankiClient) error {
	db := s.db.WithContext(ctx)
	var word Word
	err := db.Where("owner_id = ? AND id = ?", owner, wordID).First(&word).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // The queued word was deleted locally before sync.
	}
	if err != nil {
		return err
	}
	if err := client.initialize(ctx, deck); err != nil {
		return err
	}
	var tags []string
	if err := db.Table("vocabulary_tags t").Joins("JOIN vocabulary_word_tags wt ON wt.tag_id=t.id AND wt.owner_id=t.owner_id").Where("wt.owner_id = ? AND wt.word_id = ?", owner, wordID).Pluck("t.name", &tags).Error; err != nil {
		return err
	}
	fields := map[string]string{"LazyMindID": word.ID, "Word": word.Term, "Language": word.Language, "Phonetic": word.Phonetic, "PartOfSpeech": word.PartOfSpeech, "Meaning": word.Meaning, "Definition": word.Definition, "DictionarySource": word.SourceName, "UserNote": word.UserNote, "SourceRefsJSON": "[]", "SchemaVersion": "1"}
	noteID, err := client.upsertMirrorNote(ctx, deck, vocabularyModel, fields, sanitizeTags(tags, "vocabulary"))
	if err != nil {
		return err
	}
	if noteID != word.ProviderNoteID {
		result := db.Model(&Word{}).Where("owner_id = ? AND id = ?", owner, wordID).UpdateColumn("provider_note_id", noteID)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return client.deleteNote(ctx, noteID)
		}
	}
	if word.MasteredAt != nil {
		if err := client.suspendNote(ctx, noteID); err != nil {
			return err
		}
	}
	var examples []Example
	if err := db.Where("owner_id = ? AND word_id = ?", owner, wordID).Find(&examples).Error; err != nil {
		return err
	}
	for _, ex := range examples {
		var tags []string
		if err := db.Table("vocabulary_tags t").Joins("JOIN vocabulary_example_tags et ON et.tag_id=t.id AND et.owner_id=t.owner_id").Where("et.owner_id = ? AND et.example_id = ?", owner, ex.ID).Pluck("t.name", &tags).Error; err != nil {
			return err
		}
		fields := map[string]string{"LazyMindID": ex.ID, "VocabularyID": word.ID, "TargetWord": word.Term, "Sentence": ex.Sentence, "Translation": ex.Translation, "ContentOrigin": ex.ContentOrigin, "SourceRefsJSON": "[]", "SchemaVersion": "1"}
		noteID, err := client.upsertMirrorNote(ctx, deck, sentenceModel, fields, sanitizeTags(tags, "sentence"))
		if err != nil {
			return err
		}
		if noteID != ex.ProviderNoteID {
			if err := db.Model(&ex).UpdateColumn("provider_note_id", noteID).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *ankiClient) findMirrorNote(ctx context.Context, model, lazyMindID string) (string, error) {
	var ids []int64
	query := fmt.Sprintf("note:%q LazyMindID:%s", model, lazyMindID)
	if err := c.invoke(ctx, "findNotes", map[string]any{"query": query}, &ids); err != nil || len(ids) == 0 {
		return "", err
	}
	var notes []ankiNoteInfo
	if err := c.invoke(ctx, "notesInfo", map[string]any{"notes": ids}, &notes); err != nil {
		return "", err
	}
	for _, note := range notes {
		if note.Fields["LazyMindID"].Value == lazyMindID {
			return strconv.FormatInt(note.NoteID, 10), nil
		}
	}
	return "", nil
}

func (c *ankiClient) upsertMirrorNote(ctx context.Context, deck, model string, fields map[string]string, tags []string) (string, error) {
	// Match by LazyMind ID across retries and Anki collection changes.
	// Numeric Anki note IDs may be reused in a different collection.
	noteID, err := c.findMirrorNote(ctx, model, fields["LazyMindID"])
	if err != nil {
		return "", err
	}
	if noteID == "" {
		return c.addNote(ctx, deck, model, fields, tags)
	}
	if err := c.updateNote(ctx, noteID, fields); err != nil {
		return "", err
	}
	return noteID, c.updateTags(ctx, noteID, tags)
}

// StartAnkiSync drains the durable Anki outbox independently of vocabulary
// requests. A disconnected desktop is retried on the next tick.
func StartAnkiSync(ctx context.Context, db *gorm.DB) <-chan struct{} {
	done := make(chan struct{})
	if !ankiSyncAvailable() {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		s := New(db)
		for {
			s.drainAnkiOutbox(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func (s *Service) drainAnkiOutbox(ctx context.Context) {
	var owners []string
	if err := s.db.WithContext(ctx).Model(&providerOperation{}).
		Joins("JOIN vocabulary_provider_settings s ON s.owner_id = vocabulary_provider_operations.owner_id").
		Where("provider = ? AND status = ? AND s.selected_provider = ?", ProviderAnki, "pending", ProviderAnki).
		Distinct("vocabulary_provider_operations.owner_id").Pluck("vocabulary_provider_operations.owner_id", &owners).Error; err != nil {
		return
	}
	for _, owner := range owners {
		attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.flushPending(attempt, owner)
		cancel()
		s.recordAnkiSync(ctx, owner, err)
	}
}
