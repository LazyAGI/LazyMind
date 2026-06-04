package worker

import (
	"context"
	"errors"
	"io"
	"time"

	statepkg "github.com/lazymind/scan_control_plane/internal/sourceengine/state"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	TaskStatusPending    = store.ParseTaskStatusPending
	TaskStatusRunning    = store.ParseTaskStatusRunning
	TaskStatusSubmitted  = store.ParseTaskStatusSubmitted
	TaskStatusSucceeded  = store.ParseTaskStatusSucceeded
	TaskStatusFailed     = store.ParseTaskStatusFailed
	TaskStatusSuperseded = store.ParseTaskStatusSuperseded
)

var ErrNoTask = errors.New("no due parse task")

type Store interface {
	ClaimDueTask(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (store.ParseTask, bool, error)
	GetSource(ctx context.Context, sourceID string) (store.Source, error)
	GetBinding(ctx context.Context, sourceID, bindingID string) (store.Binding, error)
	GetDocumentState(ctx context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error)
	GetDocument(ctx context.Context, sourceID, bindingID, objectKey string) (store.Document, error)
	GetObject(ctx context.Context, sourceID, bindingID, objectKey string) (store.SourceObject, error)
	UpsertObjects(ctx context.Context, objects []store.SourceObject) error
	SaveParseTask(ctx context.Context, task store.ParseTask) error
	SupersedeTask(ctx context.Context, taskID string, reason string) error
	FailTask(ctx context.Context, taskID string, reason string) error
	ReleaseTaskLease(ctx context.Context, taskID, workerID string, nextRunAt time.Time) (store.ParseTask, bool, error)
	RetryOrDeadLetterTask(ctx context.Context, taskID, reason string, now time.Time, maxRetries int64, backoff time.Duration) (store.ParseTask, bool, error)
}

type CoreResultStore interface {
	ClaimSubmittedTask(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (store.ParseTask, bool, error)
	GetSource(ctx context.Context, sourceID string) (store.Source, error)
	GetBinding(ctx context.Context, sourceID, bindingID string) (store.Binding, error)
	GetDocumentState(ctx context.Context, sourceID, bindingID, objectKey string) (store.DocumentState, error)
	GetDocument(ctx context.Context, sourceID, bindingID, objectKey string) (store.Document, error)
	ReleaseTaskLease(ctx context.Context, taskID, workerID string, nextRunAt time.Time) (store.ParseTask, bool, error)
	SaveParseTask(ctx context.Context, task store.ParseTask) error
	FailTask(ctx context.Context, taskID string, reason string) error
	SupersedeTask(ctx context.Context, taskID string, reason string) error
}

type LeaseStore interface {
	ClaimDueTask(ctx context.Context, workerID string, now time.Time, ttl time.Duration) (store.ParseTask, bool, error)
	HeartbeatTaskLease(ctx context.Context, taskID, workerID string, now time.Time, ttl time.Duration) (store.ParseTask, bool, error)
	ReleaseTaskLease(ctx context.Context, taskID, workerID string, nextRunAt time.Time) (store.ParseTask, bool, error)
	RetryOrDeadLetterTask(ctx context.Context, taskID, reason string, now time.Time, maxRetries int64, backoff time.Duration) (store.ParseTask, bool, error)
}

type StateReducer interface {
	ApplyTaskSuccess(ctx context.Context, input statepkg.TaskSuccessInput) error
	ApplyTaskFailure(ctx context.Context, input statepkg.TaskFailureInput) error
}

type TempObjectStore interface {
	Put(ctx context.Context, input TempObjectInput) (TempObject, error)
	Open(ctx context.Context, uri string) (io.ReadCloser, error)
	Cleanup(ctx context.Context, token string) error
}

type TempObjectCleaner interface {
	CleanupExpired(ctx context.Context, ttl time.Duration) (int, error)
}

type TempObjectInput struct {
	Reader io.Reader
}

type TempObject struct {
	URI          string
	CleanupToken string
	SizeBytes    int64
	CreatedAt    time.Time
}
