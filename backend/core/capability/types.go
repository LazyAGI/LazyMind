package capability

import "time"

const RequiredPermission = "qa.read"

type PermissionSet map[string]struct{}

func NewPermissionSet(values ...string) PermissionSet {
	set := make(PermissionSet, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func (p PermissionSet) Has(value string) bool {
	_, ok := p[value]
	return ok
}

type Principal struct {
	UserID      string
	TenantID    string
	Permissions PermissionSet
}

type InvocationContext struct {
	Principal Principal
}

type ListVocabularyWordbooksInput struct{}
type VocabularyWordbook struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}
type ListVocabularyWordbooksResult struct {
	Provider string               `json:"provider"`
	Items    []VocabularyWordbook `json:"items"`
	Total    int                  `json:"total"`
}
type ListVocabularyWordsInput struct {
	WordbookID string `json:"wordbook_id,omitempty" jsonschema:"wordbook ID or Anki deck name; omit to use the current wordbook"`
	Search     string `json:"search,omitempty" jsonschema:"optional word or meaning keyword"`
	DueOnly    bool   `json:"due_only,omitempty" jsonschema:"return only words that the scheduler says are due now"`
}
type VocabularyWord struct {
	ID           string   `json:"id"`
	Term         string   `json:"term"`
	Meaning      string   `json:"meaning"`
	PartOfSpeech string   `json:"part_of_speech,omitempty"`
	State        string   `json:"state,omitempty"`
	ReviewCount  uint64   `json:"review_count,omitempty"`
	Lapses       uint64   `json:"lapses,omitempty"`
	Tags         []string `json:"tags,omitempty"`
}
type ListVocabularyWordsResult struct {
	Provider string           `json:"provider"`
	Items    []VocabularyWord `json:"items"`
	Total    int              `json:"total"`
}
type NextVocabularyReviewInput struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"review session ID returned by vocabulary.review.start; required for session-driven review"`
	Count     int    `json:"count,omitempty" jsonschema:"maximum questions to issue in this batch; defaults to 5 and is capped at 5"`
}
type NextVocabularyReviewResult struct {
	Available    bool                       `json:"available"`
	Completed    bool                       `json:"completed"`
	ReviewItemID string                     `json:"review_item_id,omitempty"`
	Provider     string                     `json:"provider"`
	CardID       string                     `json:"card_id,omitempty"`
	WordID       string                     `json:"word_id,omitempty"`
	Prompt       string                     `json:"prompt,omitempty"`
	Term         string                     `json:"term,omitempty"`
	Meaning      string                     `json:"meaning,omitempty"`
	CardType     string                     `json:"card_type,omitempty"`
	Answer       string                     `json:"answer,omitempty"`
	Interaction  string                     `json:"interaction,omitempty"`
	Choices      []string                   `json:"choices,omitempty"`
	Remaining    int64                      `json:"remaining"`
	RowVersion   int64                      `json:"row_version,omitempty"`
	PreviewedAt  time.Time                  `json:"previewed_at,omitempty"`
	Questions    []VocabularyReviewQuestion `json:"questions,omitempty"`
}
type VocabularyReviewQuestion struct {
	ReviewItemID string    `json:"review_item_id"`
	CardID       string    `json:"card_id"`
	WordID       string    `json:"word_id,omitempty"`
	Prompt       string    `json:"prompt"`
	Term         string    `json:"term"`
	Meaning      string    `json:"meaning"`
	CardType     string    `json:"card_type"`
	Interaction  string    `json:"interaction"`
	Choices      []string  `json:"choices"`
	RowVersion   int64     `json:"row_version,omitempty"`
	PreviewedAt  time.Time `json:"previewed_at,omitempty"`
}
type StartVocabularyReviewInput struct {
	Count int `json:"count,omitempty" jsonschema:"number of questions from 1 to 20; defaults to 5"`
}
type StartVocabularyReviewResult struct {
	SessionID    string                       `json:"session_id"`
	Provider     string                       `json:"provider"`
	WordbookName string                       `json:"wordbook_name"`
	Questions    []NextVocabularyReviewResult `json:"questions"`
	Total        int                          `json:"total"`
	Remaining    int                          `json:"remaining"`
}
type AnswerVocabularyReviewInput struct {
	SessionID    string    `json:"session_id,omitempty" jsonschema:"review session ID returned by vocabulary.review.start"`
	ReviewItemID string    `json:"review_item_id,omitempty" jsonschema:"opaque review item ID returned by vocabulary.review.next"`
	Provider     string    `json:"provider,omitempty" jsonschema:"provider returned by vocabulary.review.start or vocabulary.review.next"`
	CardID       string    `json:"card_id,omitempty" jsonschema:"legacy card ID returned by vocabulary.review.next"`
	WordID       string    `json:"word_id,omitempty" jsonschema:"word ID returned by vocabulary.review.next; empty for Anki"`
	Rating       string    `json:"rating,omitempty" jsonschema:"legacy self-assessment rating; do not use for objective session questions"`
	RowVersion   int64     `json:"row_version,omitempty"`
	PreviewedAt  time.Time `json:"previewed_at,omitempty"`
	Term         string    `json:"term,omitempty" jsonschema:"term associated with the question"`
	Response     string    `json:"response,omitempty" jsonschema:"user's raw answer; the backend determines correctness for objective questions"`
}
type AnswerVocabularyReviewResult struct {
	Accepted  bool  `json:"accepted"`
	Correct   bool  `json:"correct"`
	Remaining int64 `json:"remaining"`
}
type VocabularyReviewReportInput struct {
	SessionID string `json:"session_id"`
}
type VocabularyReviewReportResult struct {
	SessionID             string         `json:"session_id"`
	Provider              string         `json:"provider"`
	WordbookName          string         `json:"wordbook_name"`
	Total                 int            `json:"total"`
	Correct               int            `json:"correct"`
	Incorrect             int            `json:"incorrect"`
	Accuracy              float64        `json:"accuracy"`
	RatingCounts          map[string]int `json:"rating_counts"`
	AverageIntervalBefore float64        `json:"average_interval_before_days"`
	AverageIntervalAfter  float64        `json:"average_interval_after_days"`
	DifficultWords        []string       `json:"difficult_words"`
}

type PageRequest struct {
	PageSize  int    `json:"page_size,omitempty" jsonschema:"page size, from 1 to 100"`
	PageToken string `json:"page_token,omitempty" jsonschema:"opaque continuation token returned by the previous call"`
}

type PageInfo struct {
	NextPageToken string `json:"next_page_token,omitempty"`
	Total         int64  `json:"total"`
}

type CursorPageInfo struct {
	NextPageToken  string `json:"next_page_token,omitempty"`
	Total          *int64 `json:"total,omitempty"`
	ProviderCursor string `json:"-"`
}

type ListSkillsInput struct {
	Keyword  string      `json:"keyword,omitempty" jsonschema:"optional keyword matched against published skill metadata and content"`
	Category string      `json:"category,omitempty" jsonschema:"optional exact skill category"`
	Tags     []string    `json:"tags,omitempty" jsonschema:"optional tags; every tag must match"`
	Page     PageRequest `json:"page,omitempty"`
}

type SkillSummary struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Category       string   `json:"category,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	HeadRevisionID string   `json:"head_revision_id"`
}

type ListSkillsResult struct {
	Items []SkillSummary `json:"items"`
	Page  PageInfo       `json:"page"`
}

type GetSkillInput struct {
	SkillID        string `json:"skill_id" jsonschema:"stable LazyMind skill ID"`
	IncludeContent bool   `json:"include_content,omitempty" jsonschema:"include committed SKILL.md content"`
}

type SkillContent struct {
	RevisionID string `json:"revision_id"`
	Text       string `json:"text"`
}

type GetSkillResult struct {
	Skill   SkillSummary  `json:"skill"`
	Content *SkillContent `json:"content,omitempty"`
}

type ListKnowledgeInput struct {
	Keyword string      `json:"keyword,omitempty" jsonschema:"optional knowledge base metadata keyword"`
	Tags    []string    `json:"tags,omitempty" jsonschema:"optional tags; every tag must match"`
	Page    PageRequest `json:"page,omitempty"`
}

type KnowledgeSummary struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description,omitempty"`
	Tags              []string  `json:"tags,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
	DocumentSizeBytes int64     `json:"document_size_bytes"`
	DocumentCount     int64     `json:"document_count"`
}

type ListKnowledgeResult struct {
	Items []KnowledgeSummary `json:"items"`
	Page  PageInfo           `json:"page"`
}

type ListKnowledgeDocumentsInput struct {
	KnowledgeID string      `json:"knowledge_id" jsonschema:"stable LazyMind knowledge base ID"`
	Page        PageRequest `json:"page,omitempty"`
}

type KnowledgeDocumentSummary struct {
	ID          string    `json:"id"`
	KnowledgeID string    `json:"knowledge_id"`
	Name        string    `json:"name"`
	Tags        []string  `json:"tags,omitempty"`
	ParseStatus string    `json:"parse_status,omitempty"`
	MIMEType    string    `json:"mime_type,omitempty"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CreatedBy   string    `json:"created_by,omitempty"`
}

type ListKnowledgeDocumentsResult struct {
	Items []KnowledgeDocumentSummary `json:"items"`
	Page  PageInfo                   `json:"page"`
}

type GetKnowledgeDocumentInput struct {
	KnowledgeID    string      `json:"knowledge_id" jsonschema:"stable LazyMind knowledge base ID"`
	DocumentID     string      `json:"document_id" jsonschema:"stable LazyMind document ID"`
	IncludeContent bool        `json:"include_content,omitempty" jsonschema:"include safe textual document content"`
	IncludeChunks  bool        `json:"include_chunks,omitempty" jsonschema:"include one parsed chunk page"`
	ChunksPage     PageRequest `json:"chunks_page,omitempty"`
}

type DocumentFileRef struct {
	FileName    string `json:"file_name"`
	DownloadURL string `json:"download_url,omitempty"`
}

type KnowledgeDocumentContent struct {
	Text      string `json:"text"`
	MIMEType  string `json:"mime_type,omitempty"`
	Truncated bool   `json:"truncated"`
}

type KnowledgeDocumentChunk struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Number int32  `json:"number"`
}

type KnowledgeDocumentDetail struct {
	KnowledgeDocumentSummary
	Source       string                    `json:"source,omitempty"`
	OriginalFile *DocumentFileRef          `json:"original_file,omitempty"`
	Content      *KnowledgeDocumentContent `json:"content,omitempty"`
	Chunks       []KnowledgeDocumentChunk  `json:"chunks,omitempty"`
	ChunksPage   *PageInfo                 `json:"chunks_page,omitempty"`
}

type GetKnowledgeDocumentResult struct {
	Document KnowledgeDocumentDetail `json:"document"`
}

type SearchKnowledgeInput struct {
	Query        string   `json:"query" jsonschema:"search query to retrieve from LazyMind knowledge bases"`
	KnowledgeIDs []string `json:"knowledge_ids" jsonschema:"one to twenty accessible LazyMind knowledge base IDs"`
	TopK         int      `json:"top_k,omitempty" jsonschema:"maximum number of retrieval hits, from 1 to 50"`
}

type KnowledgeSearchHit struct {
	KnowledgeID string  `json:"knowledge_id"`
	DocumentID  string  `json:"document_id"`
	ChunkID     string  `json:"chunk_id,omitempty"`
	Text        string  `json:"text"`
	Score       float64 `json:"score"`
	Title       string  `json:"title,omitempty"`
}

type SearchKnowledgeResult struct {
	Hits []KnowledgeSearchHit `json:"hits"`
}

// CloudDocumentSource is an authorized cloud account available to LazyMind
// conversations. Provider credentials never cross the capability boundary.
type CloudDocumentSource struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Provider  string     `json:"provider"`
	Status    string     `json:"status,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type ListCloudDocumentsInput struct {
	Keyword string      `json:"keyword,omitempty" jsonschema:"optional connected cloud account name keyword"`
	Status  string      `json:"status,omitempty" jsonschema:"optional cloud account authorization status"`
	Page    PageRequest `json:"page,omitempty"`
}
type ListCloudDocumentsResult struct {
	Items []CloudDocumentSource `json:"items"`
	Page  PageInfo              `json:"page"`
}

type CloudDocumentMetadata struct {
	ID          string `json:"id"`
	SourceID    string `json:"source_id"`
	NodeRef     string `json:"node_ref,omitempty"`
	TargetType  string `json:"target_type,omitempty"`
	TargetRef   string `json:"target_ref,omitempty"`
	ObjectKey   string `json:"object_key,omitempty"`
	ParentKey   string `json:"parent_key,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	FileType    string `json:"file_type,omitempty"`
	IsDocument  bool   `json:"is_document"`
	IsContainer bool   `json:"is_container"`
	HasChildren bool   `json:"has_children"`
	Selectable  bool   `json:"selectable"`
}
type GetCloudDocumentInput struct {
	SourceID         string      `json:"source_id" jsonschema:"stable LazyMind cloud account ID"`
	NodeRef          string      `json:"node_ref,omitempty" jsonschema:"opaque provider node reference returned by a previous list or search"`
	TargetType       string      `json:"target_type,omitempty" jsonschema:"provider target type returned with node_ref"`
	TargetRef        string      `json:"target_ref,omitempty" jsonschema:"provider target reference returned with node_ref"`
	IncludeDocuments bool        `json:"include_documents,omitempty" jsonschema:"include one online page of documents and folders"`
	DocumentsPage    PageRequest `json:"documents_page,omitempty"`
	ProviderCursor   string      `json:"-"`
}
type GetCloudDocumentResult struct {
	Source        CloudDocumentSource     `json:"source"`
	Documents     []CloudDocumentMetadata `json:"documents,omitempty"`
	DocumentsPage *CursorPageInfo         `json:"documents_page,omitempty"`
}

type SearchCloudDocumentsInput struct {
	SourceID          string      `json:"source_id" jsonschema:"stable LazyMind cloud account ID"`
	Query             string      `json:"query" jsonschema:"online cloud document title query"`
	NodeRef           string      `json:"node_ref,omitempty" jsonschema:"optional provider node scope returned by a previous call"`
	TargetType        string      `json:"target_type,omitempty"`
	TargetRef         string      `json:"target_ref,omitempty"`
	Page              PageRequest `json:"page,omitempty"`
	IncludeDocuments  bool        `json:"include_documents,omitempty"`
	IncludeContainers bool        `json:"include_containers,omitempty"`
	ProviderCursor    string      `json:"-"`
}
type CloudDocumentSearchHit struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name,omitempty"`
	SearchName  string `json:"search_name,omitempty"`
	SourceID    string `json:"source_id"`
	NodeRef     string `json:"node_ref,omitempty"`
	TargetType  string `json:"target_type,omitempty"`
	TargetRef   string `json:"target_ref,omitempty"`
	ObjectKey   string `json:"object_key,omitempty"`
	ParentKey   string `json:"parent_key,omitempty"`
	IsDocument  bool   `json:"is_document"`
	IsContainer bool   `json:"is_container"`
	HasChildren bool   `json:"has_children"`
	Selectable  bool   `json:"selectable"`
}
type SearchCloudDocumentsResult struct {
	Hits []CloudDocumentSearchHit `json:"hits"`
	Page CursorPageInfo           `json:"page"`
}
