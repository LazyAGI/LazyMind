package source

import "time"

const (
	SyncRunStatusPending   = "PENDING"
	SyncRunStatusRunning   = "RUNNING"
	SyncRunStatusSucceeded = "SUCCEEDED"
	SyncRunStatusFailed    = "FAILED"
	SyncRunStatusCanceled  = "CANCELED"
)

type SyncCheckpoint struct {
	SourceID          string
	BindingID         string
	BindingGeneration int64
	Cursor            string
	NextSyncAt        *time.Time
	LastSyncAt        *time.Time
	LastSuccessAt     *time.Time
	LockOwner         string
	LockUntil         *time.Time
	RetryCount        int64
	LastError         JSON
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type SyncRun struct {
	RunID             string
	SourceID          string
	BindingID         string
	BindingGeneration int64
	TriggerType       string
	ScheduledFireAt   *time.Time
	ScopeType         string
	ScopeRef          JSON
	Coverage          JSON
	Status            string
	SeenCount         int64
	NewCount          int64
	ModifiedCount     int64
	DeletedCount      int64
	UnchangedCount    int64
	ErrorCode         string
	ErrorMessage      string
	StartedAt         time.Time
	FinishedAt        *time.Time
}

type SyncRunFinish struct {
	Status         string
	Cursor         string
	NextSyncAt     *time.Time
	Coverage       JSON
	SeenCount      int64
	NewCount       int64
	ModifiedCount  int64
	DeletedCount   int64
	UnchangedCount int64
	ErrorCode      string
	ErrorMessage   string
	FinishedAt     time.Time
}
