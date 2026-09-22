package academic

import "time"

type WorkInput struct {
	WorkID         string         `json:"work_id,omitempty"`
	Title          string         `json:"title,omitempty"`
	Authors        []string       `json:"authors,omitempty"`
	Year           int            `json:"year,omitempty"`
	Venue          string         `json:"venue,omitempty"`
	Abstract       string         `json:"abstract,omitempty"`
	DOI            string         `json:"doi,omitempty"`
	ArxivID        string         `json:"arxiv_id,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	ProviderWorkID string         `json:"provider_work_id,omitempty"`
	Provenance     map[string]any `json:"provenance,omitempty"`
}

type FulltextCandidate struct {
	URL            string `json:"url"`
	SourceType     string `json:"source_type"`
	SourceProvider string `json:"source_provider,omitempty"`
	Version        string `json:"version,omitempty"`
	License        string `json:"license,omitempty"`
	ExpectedMIME   string `json:"expected_mime,omitempty"`
}

type PresenceDocument struct {
	DatasetID     string `json:"dataset_id"`
	DatasetName   string `json:"dataset_name"`
	DocumentID    string `json:"document_id"`
	DocumentName  string `json:"document_name"`
	SourceVersion string `json:"source_version,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
}

type PresenceResult struct {
	WorkID           string             `json:"work_id"`
	Status           string             `json:"status"`
	CurrentDataset   []PresenceDocument `json:"current_dataset"`
	OtherDatasets    []PresenceDocument `json:"other_datasets"`
	Importing        bool               `json:"importing"`
	CoverageComplete bool               `json:"coverage_complete"`
}

type PreviewItem struct {
	WorkID       string              `json:"work_id"`
	ReferenceIDs []string            `json:"reference_ids,omitempty"`
	Title        string              `json:"title"`
	DOI          string              `json:"doi,omitempty"`
	ArxivID      string              `json:"arxiv_id,omitempty"`
	Presence     PresenceResult      `json:"presence"`
	Candidates   []FulltextCandidate `json:"fulltext_candidates"`
	Disposition  string              `json:"disposition"`
}

type ImportQueueItem struct {
	ItemID       string    `json:"item_id"`
	BatchID      string    `json:"batch_id"`
	WorkID       string    `json:"work_id"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	Stage        string    `json:"stage"`
	DocumentID   string    `json:"document_id,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
