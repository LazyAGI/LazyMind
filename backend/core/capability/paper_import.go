package capability

const (
	PaperSourcesTool = "knowledge.paper.sources"
	PaperImportTool  = "knowledge.paper.import"
)

type PaperSourcesInput struct {
	AnswerTurn int `json:"answer_turn,omitempty" jsonschema:"Earlier answer turn to inspect. Omit to find the latest earlier answer with sources in this chat."`
}

type PaperSourcesResult struct {
	AnswerID   string        `json:"answer_id"`
	AnswerTurn int           `json:"answer_turn"`
	Items      []PaperSource `json:"items"`
}

// PaperSelection describes the requested identity, without asserting a verified original.
type PaperSelection struct {
	WorkID       string   `json:"work_id,omitempty"`
	DOI          string   `json:"doi,omitempty"`
	ArxivID      string   `json:"arxiv_id,omitempty"`
	Title        string   `json:"title,omitempty"`
	Authors      []string `json:"authors,omitempty"`
	Year         int      `json:"year,omitempty"`
	Venue        string   `json:"venue,omitempty"`
	SourceID     string   `json:"source_id,omitempty"`
	ExactVersion bool     `json:"exact_version,omitempty" jsonschema:"Require the supplied publication DOI or versioned arXiv ID. Use only when the user requests that edition."`
}

type PaperSource struct {
	SourceID string `json:"source_id"`
	Title    string `json:"title"`
	URL      string `json:"url,omitempty"`
	PaperKey string `json:"paper_key,omitempty"`
	Cited    bool   `json:"cited,omitempty"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
}

// PaperSourceSnapshot is filled by the trusted Chat runtime from current tool
// results. Model-supplied snapshots are rejected before dispatch.
type PaperSourceSnapshot struct {
	SourceID string `json:"source_id"`
	Title    string `json:"title,omitempty"`
	URL      string `json:"url,omitempty"`
	DOI      string `json:"doi,omitempty"`
}

type PaperImportInput struct {
	KnowledgeBaseID string                `json:"knowledge_base_id" jsonschema:"Existing writable academic_papers library ID; use knowledge.create first if the user requests a new collection."`
	AnswerID        string                `json:"answer_id,omitempty" jsonschema:"Earlier answer ID. When using source_ids, omit to select current-turn sources."`
	SourceIDs       []string              `json:"source_ids,omitempty" jsonschema:"Select saved sources from answer_id or current-turn citation indexes. Supply either source_ids or papers."`
	Papers          []PaperSelection      `json:"papers,omitempty" jsonschema:"Papers the user explicitly asked to import, identified by work ID, DOI, arXiv ID or title and known authors/year. Supply either papers or source_ids."`
	CurrentSources  []PaperSourceSnapshot `json:"current_sources,omitempty" jsonschema:"Reserved for the runtime; omit."`
}

type PaperImportResult struct {
	BatchID         string        `json:"batch_id,omitempty"`
	KnowledgeBaseID string        `json:"knowledge_base_id"`
	QueuedCount     int           `json:"queued_count"`
	Items           []PaperSource `json:"items"`
}
