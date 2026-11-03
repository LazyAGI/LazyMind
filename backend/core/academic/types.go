package academic

import "time"

type WorkInput struct {
	CitationCount   *int64         `json:"citation_count,omitempty"`
	TrustedFulltext bool           `json:"-"`
	WorkID          string         `json:"work_id,omitempty"`
	Title           string         `json:"title,omitempty"`
	Authors         []string       `json:"authors,omitempty"`
	Year            int            `json:"year,omitempty"`
	Venue           string         `json:"venue,omitempty"`
	Abstract        string         `json:"abstract,omitempty"`
	DOI             string         `json:"doi,omitempty"`
	ArxivID         string         `json:"arxiv_id,omitempty"`
	Provider        string         `json:"provider,omitempty"`
	ProviderWorkID  string         `json:"provider_work_id,omitempty"`
	Provenance      map[string]any `json:"provenance,omitempty"`
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
	DatasetID      string `json:"dataset_id"`
	DatasetName    string `json:"dataset_name"`
	DocumentID     string `json:"document_id"`
	DocumentName   string `json:"document_name"`
	SourceProvider string `json:"source_provider,omitempty"`
	SourceVersion  string `json:"source_version,omitempty"`
	ContentSHA256  string `json:"content_sha256,omitempty"`
}

type PresenceResult struct {
	MetadataOnly     bool               `json:"metadata_only"`
	WorkID           string             `json:"work_id"`
	Status           string             `json:"status"`
	CurrentDataset   []PresenceDocument `json:"current_dataset"`
	OtherDatasets    []PresenceDocument `json:"other_datasets"`
	Importing        bool               `json:"importing"`
	CoverageComplete bool               `json:"coverage_complete"`
}

type PreviewItem struct {
	ManualDownloadURLs []string            `json:"manual_download_urls,omitempty"`
	Warnings           []string            `json:"warnings,omitempty"`
	LibraryPaperID     string              `json:"library_paper_id,omitempty"`
	Paper              PaperRef            `json:"paper"`
	WorkID             string              `json:"work_id"`
	ReferenceIDs       []string            `json:"reference_ids,omitempty"`
	Title              string              `json:"title"`
	DOI                string              `json:"doi,omitempty"`
	ArxivID            string              `json:"arxiv_id,omitempty"`
	Presence           PresenceResult      `json:"presence"`
	Candidates         []FulltextCandidate `json:"fulltext_candidates"`
	Disposition        string              `json:"disposition"`
}

type ImportQueueItem struct {
	TargetDatasetID  string               `json:"target_dataset_id"`
	SourceProvider   string               `json:"source_provider,omitempty"`
	SourceVersion    string               `json:"source_version,omitempty"`
	BatchStatus      string               `json:"batch_status"`
	BatchFailedItems int                  `json:"batch_failed_items"`
	CurrentDocument  *ImportQueueDocument `json:"current_document,omitempty" gorm:"-"`
	ItemID           string               `json:"item_id"`
	BatchID          string               `json:"batch_id"`
	WorkID           string               `json:"work_id"`
	Title            string               `json:"title"`
	Status           string               `json:"status"`
	Stage            string               `json:"stage"`
	DocumentID       string               `json:"document_id,omitempty"`
	ErrorCode        string               `json:"error_code,omitempty"`
	ErrorMessage     string               `json:"error_message,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

type ImportQueueDocument struct {
	DocumentID       string `json:"document_id"`
	ProcessingStatus string `json:"processing_status"`
	ProcessingError  string `json:"processing_error,omitempty"`
}
