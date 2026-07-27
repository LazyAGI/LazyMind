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

type CurrentMemoryPreferenceResidentIndexUsage struct {
	UsedItems int64 `json:"used_items"`
	MaxItems  int64 `json:"max_items"`
	OverLimit bool  `json:"over_limit"`
}

type CurrentMemoryPreferenceListData struct {
	Items              []CurrentMemoryPreferenceItem             `json:"items"`
	TotalSize          int64                                     `json:"total_size"`
	ResidentIndexUsage CurrentMemoryPreferenceResidentIndexUsage `json:"resident_index_usage"`
	ETag               string                                    `json:"etag"`
	UpdatedAt          int64                                     `json:"updated_at"`
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
