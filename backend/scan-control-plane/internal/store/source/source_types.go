package source

import "time"

type Source struct {
	SourceID          string
	TenantID          string
	CreatedBy         string
	Name              string
	DatasetID         string
	Status            string
	SourceOptions     JSON
	IncludeExtensions JSON
	ExcludeExtensions JSON
	ConfigVersion     int64
	DeletedAt         *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type SourceCreateRecord struct {
	Source      Source
	Bindings    []Binding
	Checkpoints []SyncCheckpoint
	Operation   CreateOperation
}

type SourceListRequest struct {
	CallerID  string
	TenantID  string
	SourceIDs []string
	Keyword   string
	Status    string
	Page      int
	PageSize  int
}

type SourceListRecord struct {
	Source       Source
	BindingCount int
	Summary      map[string]any
}

type SourceUpdateMutation struct {
	Source         Source
	CreateBindings []BindingCreateMutation
	UpdateBindings []BindingUpdateMutation
	DeleteBindings []BindingDeleteMutation
	Now            time.Time
}

type SourceUpdateResult struct {
	Cleanup CleanupResult
}

type SourceDeleteResult struct {
	Source   Source
	Bindings []Binding
	Cleanup  CleanupResult
}
