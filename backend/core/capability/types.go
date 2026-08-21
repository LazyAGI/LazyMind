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

type PageRequest struct {
	PageSize  int    `json:"page_size,omitempty" jsonschema:"page size, from 1 to 100"`
	PageToken string `json:"page_token,omitempty" jsonschema:"opaque continuation token returned by the previous call"`
}

type PageInfo struct {
	NextPageToken string `json:"next_page_token,omitempty"`
	Total         int64  `json:"total"`
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
