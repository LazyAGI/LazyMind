package currentmemory

type CurrentMemoryOperation struct {
	Op    string  `json:"op"`
	Path  string  `json:"path"`
	Value *string `json:"value,omitempty"`
}

type CurrentMemoryOperationsRequest struct {
	Operations []CurrentMemoryOperation `json:"operations"`
}

type CurrentMemorySoulData struct {
	Document        SoulDocument       `json:"document"`
	TemplateVersion int                `json:"template_version"`
	Presentation    MemoryPresentation `json:"presentation"`
	UpdatedAt       int64              `json:"updated_at"`
}

type CurrentMemoryProfileData struct {
	Document        ProfileDocument    `json:"document"`
	TemplateVersion int                `json:"template_version"`
	Presentation    MemoryPresentation `json:"presentation"`
	UpdatedAt       int64              `json:"updated_at"`
}

type CurrentMemoryPreferenceItem struct {
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type CurrentMemoryPreferenceListData struct {
	Items           []CurrentMemoryPreferenceItem `json:"items"`
	TotalSize       int64                         `json:"total_size"`
	ETag            string                        `json:"etag"`
	UpdatedAt       int64                         `json:"updated_at"`
	ProjectionState PreferenceProjectionState     `json:"projection_state"`
}

type CurrentMemoryPreferenceDetailData struct {
	Item            CurrentMemoryPreferenceItem `json:"item"`
	ReferenceStatus string                      `json:"reference_status" enum:"available,missing"`
	Reference       *ReferenceDocument          `json:"reference" nullable:"true"`
}

type CurrentMemoryPreferenceOrderRequest struct {
	OrderedNames []string `json:"ordered_names"`
	ExpectedETag string   `json:"expected_etag"`
}
