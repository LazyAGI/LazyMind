package doc

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/readingpolicy"
	"time"
)

// A user action may promote a quota-deferred automatic job, but never steals an
// actual running/queued parse. The same identity keeps concurrent clicks deduped.
func enqueuePDFIR(ctx context.Context, db *gorm.DB, req asyncjob.EnqueueRequest, automatic bool) (*orm.AsyncJob, error) {
	job, err := asyncjob.Enqueue(ctx, db, req)
	if err != nil || automatic || job.Status != "pending" || job.ErrorCode != readingpolicy.QuotaErrorCode {
		return job, err
	}
	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return nil, err
	}
	err = db.WithContext(ctx).Model(&orm.AsyncJob{}).Where("id=? AND status=? AND error_code=?", job.ID, "pending", readingpolicy.QuotaErrorCode).Updates(map[string]any{
		"payload_json": json.RawMessage(payload), "create_user_id": req.CreateUserID, "next_run_at": time.Now().UTC(), "error_code": "", "error_message": "", "updated_at": time.Now().UTC(),
	}).Error
	if err != nil {
		return nil, err
	}
	var current orm.AsyncJob
	err = db.WithContext(ctx).Where("id=?", job.ID).Take(&current).Error
	return &current, err
}
