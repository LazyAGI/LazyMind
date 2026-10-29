package vocabulary

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"time"
)

type cloudRemote interface {
	ListWordbooks(context.Context) ([]CloudWordbook, error)
	CreateWordbook(context.Context, string) (CloudWordbook, error)
	AddWord(context.Context, string, string, bool) error
}

func (s *Service) makeCloudClient(provider, token string) (cloudRemote, error) {
	if s.remoteClient != nil {
		return s.remoteClient(provider, token)
	}
	return newCloudClient(provider, token)
}
func RegisterCloudSync(db *gorm.DB) {
	s := New(db)
	asyncjob.Register(CloudSyncJobType, func(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
		return s.runCloudOperation(ctx, job)
	})
}
func (s *Service) runCloudOperation(ctx context.Context, job asyncjob.Job) (asyncjob.Result, error) {
	var payload struct {
		OperationID string `json:"operation_id"`
	}
	if json.Unmarshal(job.PayloadJSON, &payload) != nil || payload.OperationID == "" {
		return asyncjob.Result{Permanent: true}, errors.New("invalid vocabulary cloud operation")
	}
	var op CloudOperation
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", payload.OperationID, job.CreateUserID).First(&op).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return asyncjob.Result{}, nil
		}
		return asyncjob.Result{}, errors.New("vocabulary cloud storage failed")
	}
	if op.JobID != job.ID || op.Status == "succeeded" || op.Status == "canceled" {
		return asyncjob.Result{}, nil
	}
	var c CloudConnection
	err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", op.ConnectionID, op.OwnerID).First(&c).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return asyncjob.Result{}, errors.New("vocabulary cloud storage failed")
	}
	var word Word
	wordErr := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", op.WordID, op.OwnerID).First(&word).Error
	if wordErr != nil && !errors.Is(wordErr, gorm.ErrRecordNotFound) {
		return asyncjob.Result{}, errors.New("vocabulary cloud storage failed")
	}
	if err != nil || !c.Enabled || c.TargetID != op.TargetID || c.AddToStudy != op.AddToStudy || wordErr != nil || word.ArchivedAt != nil {
		return asyncjob.Result{}, s.finishCloudOperation(ctx, op, "canceled", "")
	}
	// Bound whole attempts, including throttle waits. The durable job survives shutdown.
	attemptCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	token, err := decryptCloudToken(attemptCtx, c)
	if err == nil {
		var client cloudRemote
		client, err = s.makeCloudClient(c.Provider, token)
		if err == nil {
			err = client.AddWord(attemptCtx, op.TargetID, op.Term, op.AddToStudy)
		}
	}
	if err == nil {
		return asyncjob.Result{}, s.finishCloudOperation(ctx, op, "succeeded", "")
	}
	// Never persist arbitrary error strings from transports or credential providers.
	safe := "vocabulary cloud sync failed"
	sanitizedErr := errors.New("vocabulary cloud sync failed")
	permanent := false
	var ce *CloudClientError
	if errors.As(err, &ce) {
		safe = ce.Error()
		sanitizedErr = ce
		permanent = !ce.Retryable
	}
	status := "pending"
	if permanent || job.AttemptCount >= job.MaxAttempts {
		status = "failed"
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	if ce != nil && ce.Code == "rate_limited" && ce.RetryAfter > 0 {
		status = "pending"
	}
	if e := s.finishCloudOperation(finishCtx, op, status, safe); e != nil {
		return asyncjob.Result{}, e
	}
	if ce != nil && ce.Code == "rate_limited" && ce.RetryAfter > 0 {
		return asyncjob.Result{RetryAt: time.Now().UTC().Add(ce.RetryAfter), DeferAttempt: true}, sanitizedErr
	}
	return asyncjob.Result{Permanent: permanent}, sanitizedErr
}
func (s *Service) finishCloudOperation(ctx context.Context, op CloudOperation, status, message string) error {
	// Disconnect/target changes may have canceled this while a request was in flight.
	return s.db.WithContext(ctx).Model(&CloudOperation{}).Where("id = ? AND owner_id = ? AND job_id = ? AND status <> ?", op.ID, op.OwnerID, op.JobID, "canceled").Updates(map[string]any{"status": status, "last_error": message, "updated_at": time.Now().UTC()}).Error
}
