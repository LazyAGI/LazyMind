package vocabulary

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db           *gorm.DB
	remoteClient func(string, string) (cloudRemote, error)
}

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
	return ProviderSetting{OwnerID: owner, SelectedProvider: "local", LegacySelectedProvider: "local", AnkiSyncAvailable: ankiSyncAvailable(), AnkiEndpoint: defaultAnkiURL, AnkiDeckName: defaultAnkiDeck, AnkiModelVersion: ankiModelVersion}
}

func (s *Service) Setting(ctx context.Context, owner string) (ProviderSetting, error) {
	setting := defaults(owner)
	err := s.db.WithContext(ctx).Where("owner_id = ?", owner).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	// The historical column stores the user's Anki opt-in. The vocabulary
	// provider itself is always LazyMind, including for existing Anki users.
	setting.AnkiSyncEnabled = setting.LegacySelectedProvider == ProviderAnki
	return setting, err
}

func ankiSyncAvailable() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LAZYMIND_VOCABULARY_ANKI_SYNC_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type providerSettingsPatch struct {
	SelectedProvider       string  `json:"selected_provider"`
	AnkiSyncEnabled        *bool   `json:"anki_sync_enabled"`
	AnkiEndpoint           *string `json:"anki_endpoint"`
	AnkiDeckName           *string `json:"anki_deck_name"`
	LocalDefaultWordbookID *string `json:"local_default_wordbook_id"`
}

func (s *Service) SaveSetting(ctx context.Context, owner string, input ProviderSetting) (ProviderSetting, error) {
	return s.patchSetting(ctx, owner, providerSettingsPatch{
		SelectedProvider: input.SelectedProvider, AnkiEndpoint: &input.AnkiEndpoint,
		AnkiDeckName: &input.AnkiDeckName, LocalDefaultWordbookID: &input.LocalDefaultWordbookID,
		AnkiSyncEnabled: &input.AnkiSyncEnabled,
	})
}

func (s *Service) patchSetting(ctx context.Context, owner string, input providerSettingsPatch) (ProviderSetting, error) {
	if input.SelectedProvider != "" {
		if err := validateProvider(input.SelectedProvider); err != nil {
			return ProviderSetting{}, err
		}
	}
	setting := defaults(owner)
	columns := []string{"updated_at"}
	if input.AnkiSyncEnabled != nil {
		if *input.AnkiSyncEnabled {
			setting.LegacySelectedProvider = ProviderAnki
		}
		columns = append(columns, "selected_provider")
	}
	if input.AnkiEndpoint != nil {
		if _, err := newAnkiClient(*input.AnkiEndpoint); err != nil {
			return ProviderSetting{}, err
		}
		setting.AnkiEndpoint = strings.TrimSpace(*input.AnkiEndpoint)
		columns = append(columns, "anki_endpoint")
	}
	if input.AnkiDeckName != nil {
		setting.AnkiDeckName = strings.TrimSpace(*input.AnkiDeckName)
		if setting.AnkiDeckName == "" {
			return ProviderSetting{}, errors.New("Anki deck name is required")
		}
		columns = append(columns, "anki_deck_name")
	}
	if input.LocalDefaultWordbookID != nil {
		setting.LocalDefaultWordbookID = strings.TrimSpace(*input.LocalDefaultWordbookID)
		columns = append(columns, "local_default_wordbook_id")
	}
	db := s.db.WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		if input.AnkiEndpoint != nil {
			if err := checkAnkiEndpointChange(ctx, tx, owner, setting.AnkiEndpoint); err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "owner_id"}}, DoUpdates: clause.AssignmentColumns(columns),
		}).Create(&setting).Error
	})
	if err != nil {
		return ProviderSetting{}, err
	}
	return s.Setting(ctx, owner)
}

func (s *Service) ankiSetting(ctx context.Context, owner string) (ProviderSetting, error) {
	if !ankiSyncAvailable() {
		return ProviderSetting{}, errors.New("Anki sync is unavailable on this deployment")
	}
	setting, err := s.Setting(ctx, owner)
	if err == nil && !setting.AnkiSyncEnabled {
		err = errors.New("Anki sync is disabled")
	}
	return setting, err
}

func (s *Service) Status(ctx context.Context, owner string) ProviderStatus {
	setting, err := s.Setting(ctx, owner)
	status := ProviderStatus{
		Provider: ProviderAnki, DeckName: setting.AnkiDeckName,
		Available: setting.AnkiSyncAvailable, Enabled: setting.AnkiSyncEnabled,
		LastSyncAt: setting.AnkiLastSyncAt, LastSyncError: setting.AnkiLastSyncError,
	}
	if err != nil {
		status.Message = err.Error()
		return status
	}
	_ = s.db.WithContext(ctx).Model(&providerOperation{}).Where("owner_id = ? AND provider = ? AND status = ?", owner, ProviderAnki, "pending").Count(&status.PendingOperations).Error
	if !status.Available || !status.Enabled {
		return status
	}
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
	names, err := client.modelNames(ctx)
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
	hasVocabulary, hasSentence := false, false
	for _, name := range names {
		hasVocabulary = hasVocabulary || name == vocabularyModel
		hasSentence = hasSentence || name == sentenceModel
	}
	status.Initialized = hasVocabulary && hasSentence
	return status
}

func (s *Service) ListAnkiDecks(ctx context.Context, owner string) ([]AnkiDeck, error) {
	setting, err := s.ankiSetting(ctx, owner)
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
	setting, err := s.ankiSetting(ctx, owner)
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
	setting, err := s.ankiSetting(ctx, owner)
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
	books, err := s.ListWordbooks(ctx, owner)
	items := make([]ChatWordbook, 0, len(books))
	for _, book := range books {
		items = append(items, ChatWordbook{ID: book.ID, Name: book.Name, Provider: "local"})
	}
	return "local", items, err
}

func (s *Service) ChatWords(ctx context.Context, owner, wordbookID, search string) (string, []ChatWord, error) {
	rows, err := s.SearchWords(ctx, owner, WordQuery{WordbookID: wordbookID, Search: search})
	items := make([]ChatWord, 0, len(rows))
	for _, row := range rows {
		items = append(items, ChatWord{ID: row.Word.ID, Term: row.Word.Term, Meaning: row.Word.Meaning, PartOfSpeech: row.Word.PartOfSpeech, State: row.State, Reps: row.Reps, Lapses: row.Lapses, Tags: row.Tags})
	}
	return "local", items, err
}

func (s *Service) Initialize(ctx context.Context, owner string) error {
	setting, err := s.ankiSetting(ctx, owner)
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
	setting, err := s.ankiSetting(ctx, owner)
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

type queuedMutation struct {
	NoteID string            `json:"note_id,omitempty"`
	WordID string            `json:"word_id,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
	Tags   []string          `json:"tags,omitempty"`
}

func (s *Service) AddWord(ctx context.Context, owner string, req AddWordRequest) (AddWordResult, error) {
	req.Term = strings.TrimSpace(req.Term)
	if req.Term == "" {
		return AddWordResult{}, errors.New("term is required")
	}
	req.Language = defaultString(req.Language, "en")
	if req.Provider != "" {
		if err := validateProvider(req.Provider); err != nil {
			return AddWordResult{}, err
		}
	}
	if req.Provider == ProviderAnki {
		// Older clients put Anki deck names here. Local wordbooks use IDs,
		// and the Anki destination is now selected in sync settings.
		req.WordbookIDs = nil
	}
	return s.addLocalWord(ctx, owner, req, true)
}

func (s *Service) DocumentWords(ctx context.Context, owner, documentID string) ([]DocumentWord, error) {
	if err := s.recoverLegacyAnkiAdds(ctx, owner); err != nil {
		return nil, err
	}
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

func (s *Service) Sync(ctx context.Context, owner string) error {
	if err := s.flushPending(ctx, owner); err != nil {
		s.recordAnkiSync(ctx, owner, err)
		return err
	}
	setting, err := s.ankiSetting(ctx, owner)
	if err != nil {
		return err
	}
	client, err := newAnkiClient(setting.AnkiEndpoint)
	if err != nil {
		return err
	}
	err = client.sync(ctx)
	s.recordAnkiSync(ctx, owner, err)
	return err
}

func (s *Service) recordAnkiSync(ctx context.Context, owner string, err error) {
	updates := map[string]any{"anki_last_sync_error": ""}
	if err != nil {
		updates["anki_last_sync_error"] = err.Error()
	} else {
		updates["anki_last_sync_at"] = time.Now().UTC()
	}
	_ = s.db.WithContext(ctx).Model(&ProviderSetting{}).Where("owner_id = ?", owner).Updates(updates).Error
}
