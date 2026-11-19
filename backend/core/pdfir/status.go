package pdfir

// StructureStatus is the shared PDF parsing status, independent of retrieval indexing.
type StructureStatus struct {
	ErrorCode    string `json:"error_code,omitempty"`
	Status       string `json:"status"`
	Available    bool   `json:"available"`
	ErrorMessage string `json:"error_message,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	Model        string `json:"model,omitempty"`
}
