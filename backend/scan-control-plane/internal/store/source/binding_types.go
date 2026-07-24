package source

import "time"

type Binding struct {
	BindingID              string
	SourceID               string
	BindingType            string
	ConnectorType          string
	TargetType             string
	TargetRef              string
	TargetFingerprint      string
	AgentID                string
	AuthConnectionID       string
	ProviderOptions        JSON
	TreeKey                string
	BindingGeneration      int64
	CoreParentDocumentID   string
	CoreParentDocumentName string
	SyncMode               string
	SchedulePolicy         JSON
	NextSyncAt             *time.Time
	IncludeExtensions      JSON
	ExcludeExtensions      JSON
	Status                 string
	LastError              JSON
	DeletedAt              *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type CleanupIntent struct {
	Kind      string
	TaskID    string
	Reason    string
	CreatedAt time.Time
}

type BindingUpdateCleanup struct {
	OldCoreParentDocumentID string
	ClearIndexedState       bool
	CancelPendingScheduled  bool
	OldBindingGeneration    int64
	Reason                  string
}

type CleanupResult struct {
	CancelledSyncRunCount   int64
	CancelledParseTaskCount int64
	ClearedObjectCount      int64
	ClearedStateCount       int64
	TombstonedDocumentCount int64
	CleanupIntents          []CleanupIntent
}

func (r *CleanupResult) Add(item CleanupResult) {
	r.CancelledSyncRunCount += item.CancelledSyncRunCount
	r.CancelledParseTaskCount += item.CancelledParseTaskCount
	r.ClearedObjectCount += item.ClearedObjectCount
	r.ClearedStateCount += item.ClearedStateCount
	r.TombstonedDocumentCount += item.TombstonedDocumentCount
	r.CleanupIntents = append(r.CleanupIntents, item.CleanupIntents...)
}

type BindingCreateMutation struct {
	Binding    Binding
	Checkpoint SyncCheckpoint
}

type BindingUpdateMutation struct {
	Binding    Binding
	Checkpoint SyncCheckpoint
	Cleanup    BindingUpdateCleanup
}

type BindingDeleteMutation struct {
	SourceID  string
	BindingID string
	DeletedAt time.Time
}

type BindingDeleteResult struct {
	Binding Binding
	Cleanup CleanupResult
}
