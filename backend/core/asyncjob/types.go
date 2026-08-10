package asyncjob

import (
	"context"
	"encoding/json"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

const (
	ErrorCodeHandlerNotFound = "handler_not_found"
	ErrorCodeHandlerFailed   = "handler_failed"
	ErrorCodeLockExpired     = "lock_expired"
)

type Handler func(ctx context.Context, job Job, reporter Reporter) (Result, error)

type Job struct {
	ID             string
	JobType        string
	ResourceType   string
	ResourceID     string
	PayloadJSON    json.RawMessage
	AttemptCount   int
	CreateUserID   string
	CreateUserName string
}

type Result struct {
	ResultJSON       json.RawMessage
	ErrorCode        string
	ErrorDetailsJSON json.RawMessage
}

type Reporter interface {
	SetProgress(ctx context.Context, current, total int64) error
	Heartbeat(ctx context.Context) error
}

type EnqueueRequest struct {
	JobType        string
	ResourceType   string
	ResourceID     string
	IdempotencyKey string
	Payload        any
	MaxAttempts    int
	RunAt          time.Time
	CreateUserID   string
	CreateUserName string
}

type Options struct {
	WorkerID     string
	Concurrency  int
	PollInterval time.Duration
	LockTTL      time.Duration
}
