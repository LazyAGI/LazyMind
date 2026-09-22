package learning

import "time"

type KnowledgeBaseCapability struct {
	ID                string    `json:"id" gorm:"column:id;primaryKey"`
	OwnerID           string    `json:"-" gorm:"column:owner_id;index:idx_learning_kb_cap_owner_dataset"`
	DatasetID         string    `json:"dataset_id" gorm:"column:dataset_id;uniqueIndex:uk_learning_kb_cap"`
	CapabilityKey     string    `json:"capability_key" gorm:"column:capability_key;uniqueIndex:uk_learning_kb_cap"`
	CapabilityVersion int       `json:"capability_version" gorm:"column:capability_version"`
	Enabled           bool      `json:"enabled" gorm:"column:enabled"`
	DisplayOrder      int       `json:"display_order" gorm:"column:display_order"`
	SettingsJSON      string    `json:"settings_json" gorm:"column:settings_json"`
	CreatedAt         time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt         time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (KnowledgeBaseCapability) TableName() string { return "learning_knowledge_base_capabilities" }

type CapabilityProfile struct {
	ID                 string    `json:"id" gorm:"column:id;primaryKey"`
	OwnerID            string    `json:"owner_id,omitempty" gorm:"column:owner_id;index"`
	ProfileKey         string    `json:"profile_key" gorm:"column:profile_key"`
	CustomName         string    `json:"custom_name,omitempty" gorm:"column:custom_name"`
	Description        string    `json:"description,omitempty" gorm:"column:description"`
	CapabilityRefsJSON string    `json:"capability_refs_json" gorm:"column:capability_refs_json"`
	Builtin            bool      `json:"builtin" gorm:"column:builtin"`
	CreatedAt          time.Time `json:"created_at" gorm:"column:created_at"`
	UpdatedAt          time.Time `json:"updated_at" gorm:"column:updated_at"`
}

func (CapabilityProfile) TableName() string { return "learning_capability_profiles" }

type Subject struct {
	ID             string    `gorm:"column:id;primaryKey"`
	OwnerID        string    `gorm:"column:owner_id;uniqueIndex:uk_learning_subject"`
	SubjectKind    string    `gorm:"column:subject_kind;uniqueIndex:uk_learning_subject"`
	NormalizedText string    `gorm:"column:normalized_text;uniqueIndex:uk_learning_subject"`
	DisplayText    string    `gorm:"column:display_text"`
	Language       string    `gorm:"column:language;uniqueIndex:uk_learning_subject"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (Subject) TableName() string { return "learning_subjects" }

type Occurrence struct {
	ID               string    `gorm:"column:id;primaryKey"`
	OwnerID          string    `gorm:"column:owner_id;uniqueIndex:uk_learning_occurrence"`
	SubjectID        string    `gorm:"column:subject_id;uniqueIndex:uk_learning_occurrence"`
	DatasetID        string    `gorm:"column:dataset_id"`
	DocumentID       string    `gorm:"column:document_id;uniqueIndex:uk_learning_occurrence"`
	SegmentID        string    `gorm:"column:segment_id"`
	Page             *int      `gorm:"column:page"`
	BBoxJSON         string    `gorm:"column:bbox_json"`
	SelectedText     string    `gorm:"column:selected_text"`
	ContextText      string    `gorm:"column:context_text"`
	StartOffset      int       `gorm:"column:start_offset;uniqueIndex:uk_learning_occurrence"`
	EndOffset        int       `gorm:"column:end_offset;uniqueIndex:uk_learning_occurrence"`
	DocumentRevision string    `gorm:"column:document_revision;uniqueIndex:uk_learning_occurrence"`
	CreatedAt        time.Time `gorm:"column:created_at"`
}

func (Occurrence) TableName() string { return "learning_occurrences" }

type Content struct {
	ID                string    `gorm:"column:id;primaryKey"`
	OwnerID           string    `gorm:"column:owner_id;uniqueIndex:uk_learning_content"`
	SubjectID         string    `gorm:"column:subject_id;uniqueIndex:uk_learning_content"`
	OccurrenceID      string    `gorm:"column:occurrence_id;uniqueIndex:uk_learning_content"`
	CapabilityKey     string    `gorm:"column:capability_key;uniqueIndex:uk_learning_content"`
	CapabilityVersion int       `gorm:"column:capability_version"`
	SchemaVersion     int       `gorm:"column:schema_version;uniqueIndex:uk_learning_content"`
	ContentJSON       string    `gorm:"column:content_json"`
	Origin            string    `gorm:"column:origin"`
	Status            string    `gorm:"column:status"`
	ProviderTraceID   string    `gorm:"column:provider_trace_id"`
	ModelConfigID     string    `gorm:"column:model_config_id"`
	GeneratorVersion  string    `gorm:"column:generator_version"`
	UserEdited        bool      `gorm:"column:user_edited"`
	CreatedAt         time.Time `gorm:"column:created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at"`
}

func (Content) TableName() string { return "learning_contents" }

type Book struct {
	ID, OwnerID, Name, Description, CapabilityKey string
	CapabilityVersion, SchemaVersion              int
	QuestionTypesJSON, GenerationPolicyJSON       string
	ArchivedAt                                    *time.Time
	CreatedAt, UpdatedAt                          time.Time
}

func (Book) TableName() string { return "learning_books" }

type BookEntry struct {
	ID        string    `gorm:"column:id;primaryKey"`
	OwnerID   string    `gorm:"column:owner_id"`
	BookID    string    `gorm:"column:book_id;uniqueIndex:uk_learning_book_entry"`
	ContentID string    `gorm:"column:content_id;uniqueIndex:uk_learning_book_entry"`
	Status    string    `gorm:"column:status"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (BookEntry) TableName() string { return "learning_book_entries" }

type Preset struct {
	ID, OwnerID, ScopeType, ScopeID, DocumentRevision, CapabilityKey, NormalizedKey, ValueJSON, Origin, Status string
	CapabilityVersion, SchemaVersion, Priority                                                                 int
	UserEdited                                                                                                 bool
	CreatedAt, UpdatedAt                                                                                       time.Time
}

func (Preset) TableName() string { return "learning_presets" }

type DictionaryEntry struct {
	ID, ProviderKey, Language, NormalizedHeadword, DisplayHeadword, PayloadJSON, SourceName, SourceVersion, LicenseID, SourceLocator string
	Priority                                                                                                                         int
}

func (DictionaryEntry) TableName() string { return "learning_dictionary_entries" }

type DictionaryImport struct {
	ID, ProviderKey, SourceName, SourceVersion, LicenseID, SourceURL, Checksum string
	ImportedAt                                                                 time.Time
}

func (DictionaryImport) TableName() string { return "learning_dictionary_imports" }

type QuestionInstance struct {
	ID, OwnerID, CardID, SessionID, QuestionType, PayloadJSON, AnswerSpecJSON, ExplanationJSON, Locale, GeneratorType, GeneratorVersion, ModelConfigID, ContentVersion, Status string
	QuestionTypeVersion                                                                                                                                                        int
	CreatedAt                                                                                                                                                                  time.Time
}

func (QuestionInstance) TableName() string { return "learning_question_instances" }

type Card struct {
	ID                   string `gorm:"column:id;primaryKey"`
	OwnerID              string `gorm:"column:owner_id;uniqueIndex:uk_learning_card"`
	BookID               string `gorm:"column:book_id"`
	BookEntryID          string `gorm:"column:book_entry_id;uniqueIndex:uk_learning_card"`
	ContentID            string `gorm:"column:content_id"`
	QuestionType         string `gorm:"column:question_type;uniqueIndex:uk_learning_card"`
	FSRSCardJSON         string `gorm:"column:fsrs_card_json"`
	SchedulerVersion     string `gorm:"column:scheduler_version"`
	RowVersion           int    `gorm:"column:row_version"`
	Suspended            bool   `gorm:"column:suspended"`
	CreatedAt, UpdatedAt time.Time
}

func (Card) TableName() string { return "learning_cards" }

type ReviewSession struct {
	ID, OwnerID, BookID, Locale, Status string
	Total, Answered, Correct            int
	CreatedAt, CompletedAt              time.Time
}

func (ReviewSession) TableName() string { return "learning_review_sessions" }

type ReviewSessionItem struct {
	ID                 string `gorm:"column:id;primaryKey"`
	OwnerID            string `gorm:"column:owner_id"`
	SessionID          string `gorm:"column:session_id;uniqueIndex:uk_learning_session_card"`
	CardID             string `gorm:"column:card_id;uniqueIndex:uk_learning_session_card"`
	QuestionInstanceID string `gorm:"column:question_instance_id"`
	Position           int
	AnsweredAt         *time.Time
	CreatedAt          time.Time
}

func (ReviewSessionItem) TableName() string { return "learning_review_session_items" }

type ReviewAnswer struct {
	ID                                                              string `gorm:"column:id;primaryKey"`
	OwnerID                                                         string `gorm:"column:owner_id;uniqueIndex:uk_learning_answer_idempotency"`
	SessionID, QuestionInstanceID, AnswerJSON, FeedbackJSON, Rating string
	IdempotencyKey                                                  string `gorm:"column:idempotency_key;uniqueIndex:uk_learning_answer_idempotency"`
	Score                                                           float64
	Correct                                                         bool
	AnsweredAt                                                      time.Time
}

func (ReviewAnswer) TableName() string { return "learning_review_answers" }

type ReviewLog struct {
	ID                          string `gorm:"column:id;primaryKey"`
	OwnerID                     string `gorm:"column:owner_id;uniqueIndex:uk_learning_log_idempotency"`
	CardID, Rating, FSRSLogJSON string
	IdempotencyKey              string `gorm:"column:idempotency_key;uniqueIndex:uk_learning_log_idempotency"`
	ReviewedAt                  time.Time
}

func (ReviewLog) TableName() string { return "learning_review_logs" }

type PreanalysisTask struct {
	ID, OwnerID, DatasetID, DocumentID, DocumentRevision, Status, CapabilityKeysJSON, RequestJSON, ResultJSON, ErrorMessage string
	Total, Completed, Failed                                                                                                int
	CreatedAt, StartedAt, CompletedAt, UpdatedAt                                                                            time.Time
}

func (PreanalysisTask) TableName() string { return "learning_preanalysis_tasks" }
