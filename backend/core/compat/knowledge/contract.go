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
