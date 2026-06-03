package source

import "time"

func timePtr(t time.Time) *time.Time {
	return &t
}

func syncCheckpointLockAvailable(checkpoint SyncCheckpoint, now time.Time) bool {
	return checkpoint.LockOwner == "" || checkpoint.LockUntil == nil || !checkpoint.LockUntil.After(now)
}

func applySyncRunFinish(run *SyncRun, finish SyncRunFinish) {
	run.Status = finish.Status
	run.Coverage = finish.Coverage
	run.SeenCount = finish.SeenCount
	run.NewCount = finish.NewCount
	run.ModifiedCount = finish.ModifiedCount
	run.DeletedCount = finish.DeletedCount
	run.UnchangedCount = finish.UnchangedCount
	run.ErrorCode = finish.ErrorCode
	run.ErrorMessage = finish.ErrorMessage
	run.FinishedAt = &finish.FinishedAt
}

func applyCheckpointFinish(checkpoint *SyncCheckpoint, finish SyncRunFinish) {
	checkpoint.NextSyncAt = finish.NextSyncAt
	checkpoint.LockOwner = ""
	checkpoint.LockUntil = nil
	checkpoint.UpdatedAt = finish.FinishedAt
	if finish.Status == SyncRunStatusSucceeded {
		checkpoint.Cursor = finish.Cursor
		checkpoint.RetryCount = 0
		checkpoint.LastSuccessAt = &finish.FinishedAt
		checkpoint.LastError = JSON{}
		return
	}
	checkpoint.RetryCount++
	checkpoint.LastError = JSON{"code": finish.ErrorCode, "message": finish.ErrorMessage}
}
