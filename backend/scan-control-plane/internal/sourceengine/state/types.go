package state

import (
	"errors"
	"time"

	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	SourceStateNew        = "NEW"
	SourceStateModified   = "MODIFIED"
	SourceStateUnchanged  = "UNCHANGED"
	SourceStateDeleted    = "DELETED"
	SourceStateOutOfScope = "OUT_OF_SCOPE"

	PendingActionCreate  = "CREATE"
	PendingActionReparse = "REPARSE"
	PendingActionDelete  = "DELETE"

	SyncStateIdle = "IDLE"

	ParseQueueStateNone   = "NONE"
	ParseQueueStateQueued = "QUEUED"
	ParseQueueStateFailed = "FAILED"
)

var ErrSuperseded = errors.New("superseded")

type TaskSuccessInput struct {
	Task           store.ParseTask
	CoreDocumentID string
	CoreVersionID  string
	CompletedAt    time.Time
}

type TaskFailureInput struct {
	Task      store.ParseTask
	ErrorCode string
	Message   string
	Phase     string
	FailedAt  time.Time
}
