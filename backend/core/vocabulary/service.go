package vocabulary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct{ db *gorm.DB }

type ChatWordbook struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type ChatWord struct {
	ID           string   `json:"id"`
	Term         string   `json:"term"`
	Meaning      string   `json:"meaning"`
	PartOfSpeech string   `json:"part_of_speech,omitempty"`
	State        string   `json:"state,omitempty"`
	Reps         uint64   `json:"review_count,omitempty"`
	Lapses       uint64   `json:"lapses,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}

func New(db *gorm.DB) *Service { return &Service{db: db} }

func defaults(owner string) ProviderSetting {
	return ProviderSetting{OwnerID: owner, SelectedProvider: ProviderAnki, AnkiEndpoint: defaultAnkiURL, AnkiDeckName: defaultAnkiDeck, AnkiModelVersion: ankiModelVersion}
}

func (s *Service) Setting(ctx context.Context, owner string) (ProviderSetting, error) {
	setting := defaults(owner)
	err := s.db.WithContext(ctx).Where("owner_id = ?", owner).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return setting, nil
	}
	return setting, err
}

func (s *Service) SaveSetting(ctx context.Context, owner string, input ProviderSetting) (ProviderSetting, error) {
	if input.SelectedProvider != ProviderAnki && input.SelectedProvider != "local" {
		return ProviderSetting{}, errors.New("invalid vocabulary provider")
	}
	if input.SelectedProvider == "local" && !Enabled() {
		return ProviderSetting{}, errors.New("vocabulary feature is disabled")
	}
	if input.SelectedProvider == ProviderAnki {
		if _, err := newAnkiClient(input.AnkiEndpoint); err != nil {
			return ProviderSetting{}, err
		}
	}
	if strings.TrimSpace(input.AnkiDeckName) == "" {
		return ProviderSetting{}, errors.New("Anki deck name is required")
	}
	input.OwnerID, input.AnkiModelVersion = owner, ankiModelVersion
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "owner_id"}}, DoUpdates: clause.AssignmentColumns([]string{"selected_provider", "anki_endpoint", "anki_deck_name", "anki_model_version", "local_default_wordbook_id", "updated_at"})}).Create(&input).Error
	return input, err
}

func (s *Service) Status(ctx context.Context, owner string) ProviderStatus {
	setting, err := s.Setting(ctx, owner)
	status := ProviderStatus{Provider: ProviderAnki, DeckName: setting.AnkiDeckName}
	if err != nil {
		status.Message = err.Error()
		return status
	}
	_ = s.db.WithContext(ctx).Model(&providerOperation{}).Where("owner_id = ? AND provider = ? AND status = ?", owner, ProviderAnki, "pending").Count(&status.PendingOperations).Error
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	status.Version, err = client.version(ctx)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	status.Connected = true
	status.Permission = "granted"
	status.ReviewCapability = "manage_only"
	if actions, actionErr := client.actionNames(ctx); actionErr == nil {
		hasCards, hasAnswer := false, false
		for _, action := range actions {
			hasCards = hasCards || action == "cardsInfo"
			hasAnswer = hasAnswer || action == "answerCards"
		}
		if hasCards {
			status.ReviewCapability = "read_only"
		}
		if hasCards && hasAnswer {
			status.ReviewCapability = "full"
		}
	}
	status.LastSyncAt, status.LastSyncError = setting.AnkiLastSyncAt, setting.AnkiLastSyncError
	names, err := client.modelNames(ctx)
	if err != nil {
		status.Message = err.Error()
		return status
	}
	hasVocabulary, hasSentence := false, false
	for _, name := range names {
		hasVocabulary = hasVocabulary || name == vocabularyModel
		hasSentence = hasSentence || name == sentenceModel
	}
	status.Initialized = hasVocabulary && hasSentence
	return status
}

func (s *Service) ListAnkiDecks(ctx context.Context, owner string) ([]AnkiDeck, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return nil, err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return nil, err
	}
	return client.decks(ctx)
}

func (s *Service) CreateAnkiDeck(ctx context.Context, owner, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("Anki deck name is required")
	}
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return err
	}
	return client.createDeck(ctx, name)
}

func (s *Service) DeleteAnkiDeck(ctx context.Context, owner, name, mode, target string) error {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return err
	}
	return client.deleteDeck(ctx, strings.TrimSpace(name), mode, strings.TrimSpace(target))
}

func (s *Service) ChatWordbooks(ctx context.Context, owner string) (string, []ChatWordbook, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return "", nil, err
	}
	if setting.SelectedProvider == ProviderAnki {
		names, err := s.ListAnkiDecks(ctx, owner)
		items := make([]ChatWordbook, 0, len(names))
		for _, deck := range names {
			items = append(items, ChatWordbook{ID: deck.Name, Name: deck.Name, Provider: ProviderAnki})
		}
		return ProviderAnki, items, err
	}
	books, err := s.ListWordbooks(ctx, owner)
	items := make([]ChatWordbook, 0, len(books))
	for _, book := range books {
		items = append(items, ChatWordbook{ID: book.ID, Name: book.Name, Provider: "local"})
	}
	return "local", items, err
}

func (s *Service) ChatWords(ctx context.Context, owner, wordbookID, search string) (string, []ChatWord, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return "", nil, err
	}
	search = strings.ToLower(strings.TrimSpace(search))
	if setting.SelectedProvider == ProviderAnki {
		deck := strings.TrimSpace(wordbookID)
		if deck == "" {
			deck = setting.AnkiDeckName
		}
		client, err := newAnkiClient(setting.AnkiEndpoint)
		if err != nil {
			return ProviderAnki, nil, err
		}
		notes, err := client.vocabularyNotes(ctx, deck)
		items := make([]ChatWord, 0, len(notes))
		for _, note := range notes {
			term, meaning := note.Fields["Word"].Value, note.Fields["Meaning"].Value
			if search != "" && !strings.Contains(strings.ToLower(term+" "+meaning), search) {
				continue
			}
			items = append(items, ChatWord{ID: strconv.FormatInt(note.NoteID, 10), Term: term, Meaning: meaning, PartOfSpeech: note.Fields["PartOfSpeech"].Value, Tags: note.Tags})
		}
		return ProviderAnki, items, err
	}
	rows, err := s.SearchWords(ctx, owner, WordQuery{Provider: "local", WordbookID: wordbookID, Search: search})
	items := make([]ChatWord, 0, len(rows))
	for _, row := range rows {
		items = append(items, ChatWord{ID: row.Word.ID, Term: row.Word.Term, Meaning: row.Word.Meaning, PartOfSpeech: row.Word.PartOfSpeech, State: row.State, Reps: row.Reps, Lapses: row.Lapses, Tags: row.Tags})
	}
	return "local", items, err
}

func (s *Service) Initialize(ctx context.Context, owner string) error {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return err
	}
	if err := client.initialize(ctx, setting.AnkiDeckName); err != nil {
		return err
	}
	return s.flushPending(ctx, owner)
}

func (s *Service) RequestAnkiPermission(ctx context.Context, owner string) (string, error) {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return "", err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return "", err
	}
	return client.requestPermission(ctx)
}

type providerOperation struct {
	ID             string    `gorm:"column:id;primaryKey"`
	OwnerID        string    `gorm:"column:owner_id"`
	Provider       string    `gorm:"column:provider"`
	OperationType  string    `gorm:"column:operation_type"`
	PayloadJSON    string    `gorm:"column:payload_json"`
	IdempotencyKey string    `gorm:"column:idempotency_key"`
	Status         string    `gorm:"column:status"`
	RetryCount     int       `gorm:"column:retry_count"`
	LastError      string    `gorm:"column:last_error"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (providerOperation) TableName() string { return "vocabulary_provider_operations" }

func normalizeTerm(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func operationKey(owner string, req AddWordRequest) string {
	b, _ := json.Marshal(req)
	sum := sha256.Sum256(append([]byte(owner+":"), b...))
	return hex.EncodeToString(sum[:])
}

func (s *Service) queue(ctx context.Context, owner string, req AddWordRequest, cause error) error {
	req.Provider = ProviderAnki
	payload, _ := json.Marshal(req)
	op := providerOperation{ID: uuid.NewString(), OwnerID: owner, Provider: ProviderAnki, OperationType: "add_word", PayloadJSON: string(payload), IdempotencyKey: operationKey(owner, req), Status: "pending", LastError: cause.Error()}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&op).Error
}

type queuedMutation struct {
	NoteID string            `json:"note_id"`
	Fields map[string]string `json:"fields,omitempty"`
	Tags   []string          `json:"tags,omitempty"`
}

func (s *Service) queueMutation(ctx context.Context, owner, kind, entity string, payload queuedMutation, cause error) error {
	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256([]byte(owner + ":" + kind + ":" + entity + ":" + string(raw)))
	op := providerOperation{ID: uuid.NewString(), OwnerID: owner, Provider: ProviderAnki, OperationType: kind, PayloadJSON: string(raw), IdempotencyKey: hex.EncodeToString(sum[:]), Status: "pending", LastError: cause.Error()}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&op).Error
}

func (s *Service) AddWord(ctx context.Context, owner string, req AddWordRequest) (AddWordResult, error) {
	req.Term = strings.TrimSpace(req.Term)
	if req.Term == "" {
		return AddWordResult{}, errors.New("term is required")
	}
	if req.Language == "" {
		req.Language = "en"
	}
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return AddWordResult{}, err
	}
	provider := setting.SelectedProvider
	if err := validateProvider(provider); err != nil {
		return AddWordResult{}, err
	}
	if provider == "local" {
		return s.addLocalWord(ctx, owner, req)
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return AddWordResult{}, err
	}
	if _, err = client.version(ctx); err != nil {
		if queueErr := s.queue(ctx, owner, req, err); queueErr != nil {
			return AddWordResult{}, queueErr
		}
		return AddWordResult{Queued: true}, nil
	}
	deckName := setting.AnkiDeckName
	if len(req.WordbookIDs) > 0 && strings.TrimSpace(req.WordbookIDs[0]) != "" {
		deckName = strings.TrimSpace(req.WordbookIDs[0])
	}
	if err := client.initialize(ctx, deckName); err != nil {
		return AddWordResult{}, err
	}
	now := time.Now().UTC()
	var word Word
	found := s.db.WithContext(ctx).Where("owner_id = ? AND provider = ? AND language = ? AND normalized_term = ?", owner, ProviderAnki, req.Language, normalizeTerm(req.Term)).First(&word).Error
	if errors.Is(found, gorm.ErrRecordNotFound) {
		word = Word{ID: uuid.NewString(), OwnerID: owner, Provider: ProviderAnki, NormalizedTerm: normalizeTerm(req.Term), Term: req.Term, Language: req.Language, Phonetic: req.Phonetic, PartOfSpeech: req.PartOfSpeech, Meaning: req.Meaning, Definition: req.Definition, UserNote: req.UserNote, OriginType: defaultString(req.OriginType, "user"), CreatedAt: now, UpdatedAt: now}
		fields := map[string]string{"LazyMindID": word.ID, "Word": word.Term, "Language": word.Language, "Phonetic": word.Phonetic, "PartOfSpeech": word.PartOfSpeech, "Meaning": word.Meaning, "Definition": word.Definition, "DictionarySource": "", "UserNote": word.UserNote, "SourceRefsJSON": "[]", "SchemaVersion": "1"}
		word.ProviderNoteID, err = client.addNote(ctx, deckName, vocabularyModel, fields, sanitizeTags(req.Tags, "vocabulary"))
		if err != nil {
			return AddWordResult{}, err
		}
		if err = s.db.WithContext(ctx).Create(&word).Error; err != nil {
			return AddWordResult{}, err
		}
	} else if found != nil {
		return AddWordResult{}, found
	} else {
		updates := map[string]any{"updated_at": now}
		if req.Phonetic != "" {
			updates["phonetic"] = req.Phonetic
		}
		if req.PartOfSpeech != "" {
			updates["part_of_speech"] = req.PartOfSpeech
		}
		if req.Meaning != "" {
			updates["meaning"] = req.Meaning
		}
		if req.Definition != "" {
			updates["definition"] = req.Definition
		}
		if req.UserNote != "" {
			updates["user_note"] = req.UserNote
		}
		if err = s.db.WithContext(ctx).Model(&word).Updates(updates).Error; err != nil {
			return AddWordResult{}, err
		}
		_ = s.db.WithContext(ctx).Where("id = ?", word.ID).First(&word).Error
		_ = client.updateNote(ctx, word.ProviderNoteID, map[string]string{"Word": word.Term, "Phonetic": word.Phonetic, "PartOfSpeech": word.PartOfSpeech, "Meaning": word.Meaning, "Definition": word.Definition, "UserNote": word.UserNote})
	}
	if err = s.setTags(s.db.WithContext(ctx), owner, word.ID, req.Tags, false); err != nil {
		return AddWordResult{}, err
	}
	if word.ProviderNoteID != "" {
		_ = client.updateTags(ctx, word.ProviderNoteID, sanitizeTags(req.Tags, "vocabulary"))
	}
	var example *Example
	sentence := strings.TrimSpace(req.Sentence)
	if sentence == "" {
		sentence = strings.TrimSpace(req.ContextSentence)
	}
	if sentence != "" {
		var existing Example
		err = s.db.WithContext(ctx).Where("owner_id = ? AND word_id = ? AND sentence = ?", owner, word.ID, sentence).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ex := Example{ID: uuid.NewString(), OwnerID: owner, WordID: word.ID, Sentence: sentence, Translation: strings.TrimSpace(req.Translation), ContentOrigin: "document", CreatedAt: now, UpdatedAt: now}
			fields := map[string]string{"LazyMindID": ex.ID, "VocabularyID": word.ID, "TargetWord": word.Term, "Sentence": ex.Sentence, "Translation": ex.Translation, "ContentOrigin": ex.ContentOrigin, "SourceRefsJSON": "[]", "SchemaVersion": "1"}
			ex.ProviderNoteID, err = client.addNote(ctx, deckName, sentenceModel, fields, sanitizeTags(req.ExampleTags, "sentence"))
			if err != nil {
				return AddWordResult{}, err
			}
			if err = s.db.WithContext(ctx).Create(&ex).Error; err != nil {
				return AddWordResult{}, err
			}
			example = &ex
		} else if err == nil {
			example = &existing
		} else {
			return AddWordResult{}, err
		}
	}
	if req.DocumentID != "" {
		bbox, _ := json.Marshal(req.BBox)
		ref := SourceRef{ID: uuid.NewString(), OwnerID: owner, WordID: word.ID, DatasetID: req.DatasetID, DocumentID: req.DocumentID, SegmentID: req.SegmentID, Page: req.Page, BBoxJSON: string(bbox), SelectedText: req.SelectedText, ContextSentence: sentence, CreatedAt: now}
		if example != nil {
			ref.ExampleID = example.ID
		}
		if err = s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&ref).Error; err != nil {
			return AddWordResult{}, err
		}
	}
	return AddWordResult{Word: word, Example: example}, nil
}

func (s *Service) DocumentWords(ctx context.Context, owner, documentID string) ([]DocumentWord, error) {
	var refs []SourceRef
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND document_id = ?", owner, documentID).Order("created_at DESC").Find(&refs).Error; err != nil {
		return nil, err
	}
	result := make([]DocumentWord, 0, len(refs))
	for _, ref := range refs {
		var word Word
		if err := s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, ref.WordID).First(&word).Error; err != nil {
			continue
		}
		item := DocumentWord{Word: word, Source: ref}
		var sourceCount int64
		s.db.WithContext(ctx).Model(&SourceRef{}).Where("owner_id = ? AND word_id = ?", owner, word.ID).Distinct("document_id").Count(&sourceCount)
		var otherSources int64
		s.db.WithContext(ctx).Model(&SourceRef{}).Where("owner_id = ? AND word_id = ? AND document_id <> ?", owner, word.ID, documentID).Count(&otherSources)
		item.SourceCount = sourceCount
		item.CanDelete = otherSources == 0 && word.OriginType != "user"
		if ref.ExampleID != "" {
			var ex Example
			if s.db.WithContext(ctx).Where("owner_id = ? AND id = ?", owner, ref.ExampleID).First(&ex).Error == nil {
				item.Example = &ex
			}
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Service) flushPending(ctx context.Context, owner string) error {
	var operations []providerOperation
	if err := s.db.WithContext(ctx).Where("owner_id = ? AND provider = ? AND status = ?", owner, ProviderAnki, "pending").Order("created_at ASC").Find(&operations).Error; err != nil {
		return err
	}
	for _, operation := range operations {
		if operation.OperationType != "add_word" {
			var mutation queuedMutation
			if err := json.Unmarshal([]byte(operation.PayloadJSON), &mutation); err != nil {
				_ = s.db.WithContext(ctx).Model(&operation).Updates(map[string]any{"status": "failed", "last_error": err.Error()}).Error
				continue
			}
			setting, err := s.Setting(ctx, owner)
			if err != nil {
				return err
			}
			client, err := newAnkiClient(setting.AnkiEndpoint)
			if err != nil {
				return err
			}
			switch operation.OperationType {
			case "update_word":
				err = client.updateNote(ctx, mutation.NoteID, mutation.Fields)
				if err == nil && len(mutation.Tags) > 0 {
					err = client.updateTags(ctx, mutation.NoteID, mutation.Tags)
				}
			case "delete_word":
				err = client.deleteNote(ctx, mutation.NoteID)
			case "master_word":
				err = client.suspendNote(ctx, mutation.NoteID)
			default:
				err = errors.New("unsupported queued Anki operation: " + operation.OperationType)
			}
			if err != nil {
				_ = s.db.WithContext(ctx).Model(&operation).Updates(map[string]any{"last_error": err.Error(), "retry_count": operation.RetryCount + 1}).Error
				return err
			}
			if err = s.db.WithContext(ctx).Model(&operation).Updates(map[string]any{"status": "completed", "last_error": ""}).Error; err != nil {
				return err
			}
			continue
		}
		var request AddWordRequest
		if err := json.Unmarshal([]byte(operation.PayloadJSON), &request); err != nil {
			s.db.WithContext(ctx).Model(&operation).Updates(map[string]any{"status": "failed", "last_error": err.Error(), "retry_count": operation.RetryCount + 1})
			continue
		}
		result, err := s.AddWord(ctx, owner, request)
		if err != nil || result.Queued {
			message := "AnkiConnect became unavailable"
			if err != nil {
				message = err.Error()
			}
			_ = s.db.WithContext(ctx).Model(&operation).Updates(map[string]any{"last_error": message, "retry_count": operation.RetryCount + 1}).Error
			return fmt.Errorf("AnkiConnect queue flush failed: %s", message)
		}
		if err := s.db.WithContext(ctx).Model(&operation).Updates(map[string]any{"status": "completed", "last_error": ""}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Sync(ctx context.Context, owner string) error {
	setting, err := s.Setting(ctx, owner)
	if err != nil {
		return err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return err
	}
	if err := s.flushPending(ctx, owner); err != nil {
		return err
	}
	err = client.sync(ctx)
	updates := map[string]any{"anki_last_sync_error": ""}
	if err != nil {
		updates["anki_last_sync_error"] = err.Error()
	} else {
		updates["anki_last_sync_at"] = time.Now().UTC()
	}
	_ = s.db.WithContext(ctx).Model(&ProviderSetting{}).Where("owner_id = ?", owner).Updates(updates).Error
	return err
}
