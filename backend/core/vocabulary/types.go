package vocabulary

import "time"

const (
	ProviderAnki     = "anki"
	defaultAnkiURL   = "http://127.0.0.1:8765"
	defaultAnkiDeck  = "LazyMind Vocabulary"
	vocabularyModel  = "LazyMind Vocabulary"
	sentenceModel    = "LazyMind Sentence"
	ankiModelVersion = 1
)

type ProviderSetting struct {
	OwnerID                string     `json:"-" gorm:"column:owner_id;primaryKey"`
	SelectedProvider       string     `json:"selected_provider" gorm:"column:selected_provider"`
	AnkiEndpoint           string     `json:"anki_endpoint" gorm:"column:anki_endpoint"`
	AnkiDeckName           string     `json:"anki_deck_name" gorm:"column:anki_deck_name"`
	AnkiModelVersion       int        `json:"anki_model_version" gorm:"column:anki_model_version"`
	LocalDefaultWordbookID string     `json:"local_default_wordbook_id" gorm:"column:local_default_wordbook_id"`
	AnkiLastSyncAt         *time.Time `json:"anki_last_sync_at,omitempty" gorm:"column:anki_last_sync_at"`
	AnkiLastSyncError      string     `json:"anki_last_sync_error" gorm:"column:anki_last_sync_error"`
	CreatedAt              time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt              time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (ProviderSetting) TableName() string { return "vocabulary_provider_settings" }

type Word struct {
	ID             string     `json:"id" gorm:"column:id;primaryKey"`
	OwnerID        string     `json:"-" gorm:"column:owner_id"`
	Provider       string     `json:"provider" gorm:"column:provider"`
	ProviderNoteID string     `json:"provider_note_id" gorm:"column:provider_note_id"`
	NormalizedTerm string     `json:"-" gorm:"column:normalized_term"`
	Term           string     `json:"term" gorm:"column:term"`
	Language       string     `json:"language" gorm:"column:language"`
	Phonetic       string     `json:"phonetic" gorm:"column:phonetic"`
	PartOfSpeech   string     `json:"part_of_speech" gorm:"column:part_of_speech"`
	Meaning        string     `json:"meaning" gorm:"column:meaning"`
	Definition     string     `json:"definition" gorm:"column:definition"`
	UserNote       string     `json:"user_note" gorm:"column:user_note"`
	CreatedAt      time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"column:updated_at"`
	MasteredAt     *time.Time `json:"mastered_at,omitempty" gorm:"column:mastered_at"`
	OriginType     string     `json:"origin_type" gorm:"column:origin_type"`
	SourceName     string     `json:"source_name" gorm:"column:source_name"`
	SourceVersion  string     `json:"source_version" gorm:"column:source_version"`
	LicenseID      string     `json:"license_id" gorm:"column:license_id"`
	SourceLocator  string     `json:"source_locator" gorm:"column:source_locator"`
	ArchivedAt     *time.Time `json:"archived_at,omitempty" gorm:"column:archived_at"`
}

func (Word) TableName() string { return "vocabulary_words" }

type Example struct {
	ID             string    `json:"id" gorm:"column:id;primaryKey"`
	OwnerID        string    `json:"-" gorm:"column:owner_id"`
	WordID         string    `json:"word_id" gorm:"column:word_id"`
	ProviderNoteID string    `json:"provider_note_id" gorm:"column:provider_note_id"`
	Sentence       string    `json:"sentence" gorm:"column:sentence"`
	Translation    string    `json:"translation" gorm:"column:translation"`
	ContentOrigin  string    `json:"content_origin" gorm:"column:content_origin"`
	CreatedAt      time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"column:updated_at"`
	SourceName     string    `json:"source_name" gorm:"column:source_name"`
	SourceVersion  string    `json:"source_version" gorm:"column:source_version"`
	LicenseID      string    `json:"license_id" gorm:"column:license_id"`
	SourceLocator  string    `json:"source_locator" gorm:"column:source_locator"`
}

func (Example) TableName() string { return "vocabulary_examples" }

type SourceRef struct {
	ID               string    `json:"id" gorm:"column:id;primaryKey"`
	OwnerID          string    `json:"-" gorm:"column:owner_id"`
	WordID           string    `json:"word_id" gorm:"column:word_id"`
	ExampleID        string    `json:"example_id" gorm:"column:example_id"`
	DatasetID        string    `json:"dataset_id" gorm:"column:dataset_id"`
	DocumentID       string    `json:"document_id" gorm:"column:document_id"`
	SegmentID        string    `json:"segment_id" gorm:"column:segment_id"`
	Page             *int      `json:"page,omitempty" gorm:"column:page"`
	BBoxJSON         string    `json:"bbox_json,omitempty" gorm:"column:bbox_json"`
	SelectedText     string    `json:"selected_text" gorm:"column:selected_text"`
	ContextSentence  string    `json:"context_sentence" gorm:"column:context_sentence"`
	CreatedAt        time.Time `json:"created_at" gorm:"column:created_at"`
	DocumentRevision string    `json:"document_revision" gorm:"column:document_revision"`
}

func (SourceRef) TableName() string { return "vocabulary_source_refs" }

type DocumentWord struct {
	Word
	Example     *Example  `json:"example,omitempty"`
	Source      SourceRef `json:"source"`
	SourceCount int64     `json:"source_count"`
	CanDelete   bool      `json:"can_delete"`
}

type AddWordRequest struct {
	Provider          string    `json:"provider,omitempty"`
	Term              string    `json:"term"`
	Language          string    `json:"language,omitempty"`
	Phonetic          string    `json:"phonetic,omitempty"`
	PartOfSpeech      string    `json:"part_of_speech,omitempty"`
	Meaning           string    `json:"meaning,omitempty"`
	Definition        string    `json:"definition,omitempty"`
	UserNote          string    `json:"user_note,omitempty"`
	Tags              []string  `json:"tags,omitempty"`
	Sentence          string    `json:"sentence,omitempty"`
	Translation       string    `json:"translation,omitempty"`
	ExampleTags       []string  `json:"example_tags,omitempty"`
	DatasetID         string    `json:"dataset_id,omitempty"`
	DocumentID        string    `json:"document_id,omitempty"`
	SegmentID         string    `json:"segment_id,omitempty"`
	Page              *int      `json:"page,omitempty"`
	BBox              []float64 `json:"bbox,omitempty"`
	SelectedText      string    `json:"selected_text,omitempty"`
	ContextSentence   string    `json:"context_sentence,omitempty"`
	WordbookIDs       []string  `json:"wordbook_ids,omitempty"`
	OriginType        string    `json:"origin_type,omitempty"`
	DictionaryEntryID string    `json:"dictionary_entry_id,omitempty"`
	DocumentRevision  string    `json:"document_revision,omitempty"`
}

type AddWordResult struct {
	Word    Word     `json:"word"`
	Example *Example `json:"example,omitempty"`
	Queued  bool     `json:"queued"`
}

type ProviderStatus struct {
	Provider          string     `json:"provider"`
	Connected         bool       `json:"connected"`
	Version           int        `json:"version,omitempty"`
	Initialized       bool       `json:"initialized"`
	DeckName          string     `json:"deck_name"`
	PendingOperations int64      `json:"pending_operations"`
	Message           string     `json:"message,omitempty"`
	Permission        string     `json:"permission,omitempty"`
	ReviewCapability  string     `json:"review_capability"`
	LastSyncAt        *time.Time `json:"last_sync_at,omitempty"`
	LastSyncError     string     `json:"last_sync_error,omitempty"`
}

type ReviewCard struct {
	ID                string     `json:"id" gorm:"column:id;primaryKey"`
	OwnerID           string     `json:"-" gorm:"column:owner_id"`
	WordID            string     `json:"word_id" gorm:"column:word_id"`
	ExampleID         string     `json:"example_id" gorm:"column:example_id"`
	CardType          string     `json:"card_type" gorm:"column:card_type"`
	SchedulerVersion  string     `json:"scheduler_version" gorm:"column:scheduler_version"`
	ParametersVersion string     `json:"parameters_version" gorm:"column:parameters_version"`
	FSRSCardJSON      string     `json:"-" gorm:"column:fsrs_card_json"`
	RowVersion        int64      `json:"row_version" gorm:"column:row_version"`
	SuspendedAt       *time.Time `json:"suspended_at,omitempty" gorm:"column:suspended_at"`
	CreatedAt         time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt         time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (ReviewCard) TableName() string { return "vocabulary_review_cards" }

type VocabularyItem struct {
	Word      Word       `json:"word"`
	Example   *Example   `json:"example,omitempty"`
	State     string     `json:"state"`
	DueAt     *time.Time `json:"due_at,omitempty"`
	CardType  string     `json:"card_type,omitempty"`
	Tags      []string   `json:"tags,omitempty"`
	Wordbooks []Wordbook `json:"wordbooks,omitempty"`
	Reps      uint64     `json:"reps"`
	Lapses    uint64     `json:"lapses"`
}
type ReviewQuestion struct {
	VocabularyItem
	Options     map[string]time.Time `json:"options"`
	Interaction string               `json:"interaction"`
	Choices     []ReviewChoice       `json:"choices,omitempty"`
	RowVersion  int64                `json:"row_version"`
	CardID      string               `json:"card_id"`
	Prompt      string               `json:"prompt"`
	Answer      string               `json:"answer"`
	Remaining   int64                `json:"remaining"`
	PreviewedAt time.Time            `json:"previewed_at"`
}
type ReviewChoice struct {
	Value        string `json:"value"`
	Label        string `json:"label"`
	PartOfSpeech string `json:"part_of_speech,omitempty"`
}
type ReviewRequest struct {
	Rating         string    `json:"rating"`
	Response       string    `json:"response,omitempty"`
	RowVersion     int64     `json:"row_version"`
	IdempotencyKey string    `json:"idempotency_key"`
	CardID         string    `json:"card_id"`
	PreviewedAt    time.Time `json:"previewed_at"`
}
