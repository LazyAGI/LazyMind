package vocabulary

import "time"

type Wordbook struct {
	ID          string     `json:"id" gorm:"column:id;primaryKey"`
	OwnerID     string     `json:"-" gorm:"column:owner_id;uniqueIndex:uk_vocabulary_wordbook_owner_name"`
	Name        string     `json:"name" gorm:"column:name;uniqueIndex:uk_vocabulary_wordbook_owner_name"`
	Description string     `json:"description" gorm:"column:description"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty" gorm:"column:archived_at"`
	CreatedAt   time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (Wordbook) TableName() string { return "vocabulary_wordbooks" }

type WordbookEntry struct {
	OwnerID    string    `gorm:"column:owner_id;primaryKey"`
	WordbookID string    `gorm:"column:wordbook_id;primaryKey"`
	WordID     string    `gorm:"column:word_id;primaryKey"`
	CreatedAt  time.Time `gorm:"column:created_at"`
}

func (WordbookEntry) TableName() string { return "vocabulary_wordbook_entries" }

type Tag struct {
	ID        string    `json:"id" gorm:"column:id;primaryKey"`
	OwnerID   string    `json:"-" gorm:"column:owner_id;uniqueIndex:uk_vocabulary_tag_owner_name"`
	Name      string    `json:"name" gorm:"column:name;uniqueIndex:uk_vocabulary_tag_owner_name"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at"`
}

func (Tag) TableName() string { return "vocabulary_tags" }

type WordTag struct {
	OwnerID   string    `gorm:"column:owner_id;primaryKey"`
	WordID    string    `gorm:"column:word_id;primaryKey"`
	TagID     string    `gorm:"column:tag_id;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (WordTag) TableName() string { return "vocabulary_word_tags" }

type ExampleTag struct {
	OwnerID   string    `gorm:"column:owner_id;primaryKey"`
	ExampleID string    `gorm:"column:example_id;primaryKey"`
	TagID     string    `gorm:"column:tag_id;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (ExampleTag) TableName() string { return "vocabulary_example_tags" }

type DictionaryEntry struct {
	ID             string              `json:"id" gorm:"column:id;primaryKey"`
	Language       string              `json:"language"`
	NormalizedTerm string              `json:"-" gorm:"column:normalized_term"`
	Term           string              `json:"term"`
	Phonetic       string              `json:"phonetic"`
	SourceName     string              `json:"source_name"`
	SourceVersion  string              `json:"source_version"`
	LicenseID      string              `json:"license_id"`
	SourceLocator  string              `json:"source_locator"`
	Priority       int                 `json:"priority"`
	Senses         []DictionarySense   `json:"senses" gorm:"-"`
	Examples       []DictionaryExample `json:"examples" gorm:"-"`
}

func (DictionaryEntry) TableName() string { return "vocabulary_dictionary_entries" }

type DictionarySense struct {
	ID           string `json:"id" gorm:"column:id;primaryKey"`
	EntryID      string `json:"entry_id"`
	PartOfSpeech string `json:"part_of_speech"`
	Definition   string `json:"definition"`
	Translation  string `json:"translation"`
	SenseOrder   int    `json:"sense_order"`
}

func (DictionarySense) TableName() string { return "vocabulary_dictionary_senses" }

type DictionaryExample struct {
	ID            string `json:"id" gorm:"column:id;primaryKey"`
	EntryID       string `json:"entry_id"`
	SenseID       string `json:"sense_id"`
	Sentence      string `json:"sentence"`
	Translation   string `json:"translation"`
	SourceLocator string `json:"source_locator"`
	ExampleOrder  int    `json:"example_order"`
}

func (DictionaryExample) TableName() string { return "vocabulary_dictionary_examples" }

type DictionaryImport struct {
	ID            string    `json:"id" gorm:"column:id;primaryKey"`
	SourceName    string    `json:"source_name"`
	SourceVersion string    `json:"source_version"`
	LicenseID     string    `json:"license_id"`
	SourceURL     string    `json:"source_url"`
	Checksum      string    `json:"checksum"`
	ImportedAt    time.Time `json:"imported_at"`
}

func (DictionaryImport) TableName() string { return "vocabulary_dictionary_imports" }

type FSRSProfile struct {
	ID                  string    `json:"id" gorm:"column:id;primaryKey"`
	OwnerID             string    `json:"-"`
	Name                string    `json:"name"`
	WeightsJSON         string    `json:"weights_json"`
	DesiredRetention    float64   `json:"desired_retention"`
	MaximumIntervalDays int       `json:"maximum_interval_days"`
	SchedulerVersion    string    `json:"scheduler_version"`
	Source              string    `json:"source"`
	Active              bool      `json:"active"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (FSRSProfile) TableName() string { return "vocabulary_fsrs_profiles" }

type ReviewLogRow struct {
	ID             string    `gorm:"column:id;primaryKey"`
	OwnerID        string    `gorm:"column:owner_id"`
	CardID         string    `gorm:"column:card_id"`
	Rating         int       `gorm:"column:rating"`
	FSRSLogJSON    string    `gorm:"column:fsrs_log_json"`
	ReviewedAt     time.Time `gorm:"column:reviewed_at"`
	IdempotencyKey string    `gorm:"column:idempotency_key"`
	CreatedAt      time.Time `gorm:"column:created_at"`
}

func (ReviewLogRow) TableName() string { return "vocabulary_review_logs" }

type ReviewSession struct {
	ID           string     `json:"id" gorm:"column:id;primaryKey"`
	OwnerID      string     `json:"-" gorm:"column:owner_id"`
	Provider     string     `json:"provider" gorm:"column:provider"`
	WordbookID   string     `json:"wordbook_id" gorm:"column:wordbook_id"`
	WordbookName string     `json:"wordbook_name" gorm:"column:wordbook_name"`
	Status       string     `json:"status" gorm:"column:status"`
	ExpiresAt    time.Time  `json:"expires_at" gorm:"column:expires_at"`
	StartedAt    time.Time  `json:"started_at" gorm:"column:started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty" gorm:"column:completed_at"`
	CreatedAt    time.Time  `json:"created_at" gorm:"column:created_at"`
}

func (ReviewSession) TableName() string { return "vocabulary_review_sessions" }

type ReviewSessionItem struct {
	ID          string     `json:"id" gorm:"column:id;primaryKey"`
	SessionID   string     `json:"session_id" gorm:"column:session_id;uniqueIndex:idx_review_session_card"`
	OwnerID     string     `json:"-" gorm:"column:owner_id"`
	CardID      string     `json:"card_id" gorm:"column:card_id;uniqueIndex:idx_review_session_card"`
	WordID      string     `json:"word_id" gorm:"column:word_id"`
	Term        string     `json:"term" gorm:"column:term"`
	Meaning     string     `json:"meaning" gorm:"column:meaning"`
	Prompt      string     `json:"prompt" gorm:"column:prompt"`
	Expected    string     `json:"-" gorm:"column:expected_answer"`
	CardType    string     `json:"card_type" gorm:"column:card_type"`
	Status      string     `json:"status" gorm:"column:status"`
	Sequence    int        `json:"sequence" gorm:"column:sequence"`
	RowVersion  int64      `json:"row_version" gorm:"column:row_version"`
	PreviewedAt time.Time  `json:"previewed_at" gorm:"column:previewed_at"`
	IssuedAt    *time.Time `json:"issued_at,omitempty" gorm:"column:issued_at"`
	AnsweredAt  *time.Time `json:"answered_at,omitempty" gorm:"column:answered_at"`
	CreatedAt   time.Time  `json:"created_at" gorm:"column:created_at"`
}

func (ReviewSessionItem) TableName() string { return "vocabulary_review_session_items" }

type ReviewSessionAnswer struct {
	ID             string    `json:"id" gorm:"column:id;primaryKey"`
	SessionID      string    `json:"session_id" gorm:"column:session_id"`
	OwnerID        string    `json:"-" gorm:"column:owner_id"`
	CardID         string    `json:"card_id" gorm:"column:card_id"`
	WordID         string    `json:"word_id" gorm:"column:word_id"`
	Term           string    `json:"term" gorm:"column:term"`
	Rating         int       `json:"rating" gorm:"column:rating"`
	IntervalBefore int64     `json:"interval_before_days" gorm:"column:interval_before_days"`
	IntervalAfter  int64     `json:"interval_after_days" gorm:"column:interval_after_days"`
	AnsweredAt     time.Time `json:"answered_at" gorm:"column:answered_at"`
	CreatedAt      time.Time `json:"created_at" gorm:"column:created_at"`
}

func (ReviewSessionAnswer) TableName() string { return "vocabulary_review_session_answers" }

type WordbookInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Archived    bool   `json:"archived"`
}
type WordUpdate struct {
	Term         *string  `json:"term"`
	Phonetic     *string  `json:"phonetic"`
	PartOfSpeech *string  `json:"part_of_speech"`
	Meaning      *string  `json:"meaning"`
	Definition   *string  `json:"definition"`
	UserNote     *string  `json:"user_note"`
	WordbookIDs  []string `json:"wordbook_ids"`
	Tags         []string `json:"tags"`
}
type ExampleInput struct {
	Sentence      string   `json:"sentence"`
	Translation   string   `json:"translation"`
	ContentOrigin string   `json:"content_origin"`
	Tags          []string `json:"tags"`
}
type WordQuery struct {
	Provider   string
	Search     string
	State      string
	WordbookID string
	DocumentID string
	TagsAll    []string
	TagsAny    []string
	TagsNot    []string
}
type SelectionResolveRequest struct {
	Term     string `json:"term"`
	Language string `json:"language"`
	Context  string `json:"context"`
	Provider string `json:"provider"`
}
type SelectionResolveResult struct {
	Term       string            `json:"term"`
	Sentence   string            `json:"sentence"`
	Provider   string            `json:"provider"`
	Existing   *Word             `json:"existing,omitempty"`
	Dictionary []DictionaryEntry `json:"dictionary"`
	Wordbooks  []Wordbook        `json:"wordbooks"`
}
type ReviewStats struct {
	Total         int64 `json:"total"`
	Due           int64 `json:"due"`
	New           int64 `json:"new"`
	Learning      int64 `json:"learning"`
	Review        int64 `json:"review"`
	Relearning    int64 `json:"relearning"`
	Mastered      int64 `json:"mastered"`
	Weak          int64 `json:"weak"`
	ReviewedToday int64 `json:"reviewed_today"`
}
