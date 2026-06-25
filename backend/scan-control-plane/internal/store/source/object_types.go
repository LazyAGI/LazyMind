package source

import "time"

type SourceObject struct {
	SourceID        string
	BindingID       string
	TreeKey         string
	ObjectKey       string
	ParentKey       string
	DisplayName     string
	SearchName      string
	ObjectType      string
	IsDocument      bool
	IsContainer     bool
	HasChildren     bool
	SourceVersion   string
	SizeBytes       int64
	MimeType        string
	FileExtension   string
	ModifiedAt      *time.Time
	DeletedAtSource *time.Time
	Depth           int64
	ProviderMeta    JSON
	LastSeenRunID   string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type DocumentState struct {
	SourceID            string
	BindingID           string
	BindingGeneration   int64
	ObjectKey           string
	SourceVersion       string
	BaselineVersion     string
	DeletedAtSource     *time.Time
	SourceState         string
	SyncState           string
	PendingAction       string
	DocumentListVisible bool
	Selectable          bool
	ParseQueueState     string
	DocumentID          string
	ActiveTaskID        string
	LastDetectedAt      *time.Time
	LastSyncedAt        *time.Time
	LastError           JSON
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Document struct {
	DocumentID       string
	TenantID         string
	SourceID         string
	BindingID        string
	ObjectKey        string
	CoreDocumentID   string
	CurrentVersionID string
	DesiredVersionID string
	SourceVersion    string
	DisplayName      string
	MimeType         string
	FileExtension    string
	ParseStatus      string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type ObjectListRequest struct {
	SourceID          string
	BindingID         string
	TreeKey           string
	ParentKey         string
	IncludeDocuments  bool
	IncludeContainers bool
	StateFilter       []string
	PageSize          int
	Cursor            string
}

type ObjectSearchRequest struct {
	SourceID          string
	BindingID         string
	TreeKey           string
	Keyword           string
	IncludeDocuments  bool
	IncludeContainers bool
	StateFilter       []string
	PageSize          int
	Cursor            string
}

type ObjectWithState struct {
	Object SourceObject
	State  *DocumentState
}

type SourceDocumentListRequest struct {
	SourceID      string
	BindingID     string
	Keyword       string
	StateFilter   []string
	ParseStatuses []string
	Page          int
	PageSize      int
}

type DocumentWithState struct {
	Object   SourceObject
	State    DocumentState
	Document *Document
}

type SourceSummaryRequest struct {
	SourceID  string
	BindingID string
}

type SourceSummary struct {
	SourceID            string
	BindingID           string
	TotalObjects        int64
	DocumentObjects     int64
	ContainerObjects    int64
	NewCount            int64
	ModifiedCount       int64
	DeletedCount        int64
	UnchangedCount      int64
	PendingTaskCount    int64
	RunningTaskCount    int64
	SubmittedTaskCount  int64
	FailedTaskCount     int64
	SucceededTaskCount  int64
	SupersededTaskCount int64
	ParsedDocumentCount int64
	StorageBytes        int64
	LastSuccessAt       *time.Time
	LastError           JSON
	Bindings            []SourceSummary
}

func (s *SourceSummary) Add(item SourceSummary) {
	s.TotalObjects += item.TotalObjects
	s.DocumentObjects += item.DocumentObjects
	s.ContainerObjects += item.ContainerObjects
	s.NewCount += item.NewCount
	s.ModifiedCount += item.ModifiedCount
	s.DeletedCount += item.DeletedCount
	s.UnchangedCount += item.UnchangedCount
	s.PendingTaskCount += item.PendingTaskCount
	s.RunningTaskCount += item.RunningTaskCount
	s.SubmittedTaskCount += item.SubmittedTaskCount
	s.FailedTaskCount += item.FailedTaskCount
	s.SucceededTaskCount += item.SucceededTaskCount
	s.SupersededTaskCount += item.SupersededTaskCount
	s.ParsedDocumentCount += item.ParsedDocumentCount
	s.StorageBytes += item.StorageBytes
	if item.LastSuccessAt != nil && (s.LastSuccessAt == nil || item.LastSuccessAt.After(*s.LastSuccessAt)) {
		s.LastSuccessAt = item.LastSuccessAt
	}
	if len(s.LastError) == 0 && len(item.LastError) > 0 {
		s.LastError = CloneJSON(item.LastError)
	}
}

func AddSourceStateCount(summary *SourceSummary, status string, count int64) {
	switch status {
	case "NEW":
		summary.NewCount += count
	case "MODIFIED":
		summary.ModifiedCount += count
	case "DELETED":
		summary.DeletedCount += count
	case "UNCHANGED":
		summary.UnchangedCount += count
	}
}

func AddTaskStatusCount(summary *SourceSummary, status string, count int64) {
	switch status {
	case "PENDING":
		summary.PendingTaskCount += count
	case "RUNNING":
		summary.RunningTaskCount += count
	case "SUBMITTED":
		summary.SubmittedTaskCount += count
	case "FAILED":
		summary.FailedTaskCount += count
	case "SUCCEEDED":
		summary.SucceededTaskCount += count
	case "SUPERSEDED":
		summary.SupersededTaskCount += count
	}
}
