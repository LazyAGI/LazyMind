package knowledge

import (
	"time"

	"lazymind/core/compat/contract"
)

type ListInput struct {
	Keyword string               // Metadata keyword filter.
	Tags    []string             // Required tags; all must match.
	Page    contract.PageRequest // Pagination input.
}

type Summary struct {
	ID                string    // Stable dataset ID.
	Name              string    // Display name.
	Description       string    // Dataset description.
	Tags              []string  // Dataset tags.
	UpdatedAt         time.Time // Last update time.
	DocumentSizeBytes int64     // Sum of non-folder document sizes.
	DocumentCount     int64     // Count of non-folder documents.
}

type ListResult struct {
	Items []Summary           // Page items.
	Page  contract.PageResult // Pagination result.
}

type GetInput struct {
	KnowledgeID string // Dataset ID to fetch.
}

type GetResult struct {
	Knowledge Summary // Knowledge catalog summary.
}

type GetDocumentInput struct {
	KnowledgeID    string               // Dataset ID that owns the document.
	DocumentID     string               // Stable Core document ID.
	IncludeContent bool                 // Read safe text content when true.
	IncludeChunks  bool                 // Read one chunk page when true.
	ChunksPage     contract.PageRequest // Chunk pagination input.
}

type GetDocumentResult struct {
	Document DocumentDetail
}

type DocumentDetail struct {
	ID           string
	KnowledgeID  string
	Name         string
	Source       string
	Tags         []string
	ParseStatus  string
	MIMEType     string
	SizeBytes    int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	CreatedBy    string
	OriginalFile *FileRef
	Content      *DocumentContent
	Chunks       []DocumentChunk
	ChunksPage   *contract.PageResult
}

type FileRef struct {
	FileName    string
	DownloadURL string
}

type DocumentContent struct {
	MIMEType  string
	Text      string
	Truncated bool
}

type DocumentChunk struct {
	ID     string
	Text   string
	Number int32
}

type SearchInput struct {
	Query        string
	KnowledgeIDs []string
	TopK         int
}

type SearchResult struct {
	Hits []SearchHit
}

type SearchHit struct {
	KnowledgeID string
	DocumentID  string
	ChunkID     string
	Text        string
	Score       float64
	SourceURL   string
	Title       string
}
