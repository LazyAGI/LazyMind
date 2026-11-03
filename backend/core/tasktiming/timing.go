// Package tasktiming persists lifecycle facts; reads never write or advance a clock.
package tasktiming

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/taskdisplay"
)

type requestTimeKey struct{}

func WithRequestTime(ctx context.Context, now time.Time) context.Context {
	return context.WithValue(ctx, requestTimeKey{}, now)
}
func RequestedAt(ctx context.Context) time.Time {
	if at, ok := ctx.Value(requestTimeKey{}).(time.Time); ok {
		return at
	}
	return time.Now().UTC()
}

// Older databases have no reliable task-level timing. Leave their values unknown.
func Available(db *gorm.DB) bool {
	return db != nil && db.Migrator().HasTable(&orm.TaskExecutionTiming{}) && db.Migrator().HasTable(&orm.TaskAttemptTiming{})
}
func terminal(status string) bool {
	switch status {
	case "completed", "succeeded", "failed", "canceled", "cancelled", "interrupted", "stopped", "waiting_approval":
		return true
	}
	return false
}

func workflowWait(status string) bool {
	return status == "stopped" || status == "waiting_approval"
}

func Begin(ctx context.Context, db *gorm.DB, executionID, runID, conversationID, owner string, at time.Time) error {
	if !Available(db) {
		return nil
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&orm.TaskExecutionTiming{ID: executionID, RunID: runID, HistoryID: runID, ConversationID: conversationID, OwnerUserID: owner, Status: "running", Revision: 1, RequestedAt: at}).Error
}
func SetStatus(ctx context.Context, db *gorm.DB, executionID, status string, at time.Time) error {
	if !Available(db) {
		return nil
	}
	updates := map[string]any{"status": status, "revision": gorm.Expr("revision + 1")}
	if terminal(status) {
		updates["finished_at"] = at
	}
	return db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("id = ? AND finished_at IS NULL AND status <> ?", executionID, status).Updates(updates).Error
}

// RecordAttempt is called inside the same transaction/lock as the lifecycle
// transition. IDs fence duplicate and delayed events from other generations.
func RecordAttempt(ctx context.Context, db *gorm.DB, executionID, attemptID string, started time.Time, finished *time.Time) error {
	if !Available(db) || started.IsZero() {
		return nil
	}
	return transaction(ctx, db, func(tx *gorm.DB) error { return recordAttempt(ctx, tx, executionID, attemptID, started, finished) })
}
func recordAttempt(ctx context.Context, db *gorm.DB, executionID, attemptID string, started time.Time, finished *time.Time) error {
	if !Available(db) || started.IsZero() {
		return nil
	}
	// Serialize facts with terminalization so the final snapshot is immutable.
	if err := db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("id = ?", executionID).UpdateColumn("revision", gorm.Expr("revision")).Error; err != nil {
		return err
	}
	var root orm.TaskExecutionTiming
	if err := db.WithContext(ctx).Where("id = ?", executionID).First(&root).Error; err != nil {
		return err
	}
	if root.FinishedAt != nil {
		return nil
	}
	row := orm.TaskAttemptTiming{ID: attemptID, ExecutionID: executionID, StartedAt: started, FinishedAt: finished}
	created := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if created.Error != nil {
		return created.Error
	}
	changed := created.RowsAffected > 0
	if !changed && finished != nil {
		result := db.WithContext(ctx).Model(&orm.TaskAttemptTiming{}).Where("id = ? AND execution_id = ? AND finished_at IS NULL", attemptID, executionID).Update("finished_at", *finished)
		if result.Error != nil {
			return result.Error
		}
		changed = result.RowsAffected > 0
	}
	if !changed {
		return nil
	}
	return db.WithContext(ctx).Model(&orm.TaskExecutionTiming{}).Where("id = ?", executionID).UpdateColumn("revision", gorm.Expr("revision + 1")).Error
}

func measure(row orm.TaskExecutionTiming, attempts []orm.TaskAttemptTiming, now time.Time) *taskdisplay.OrdinaryRunView {
	cutoff := now
	if row.FinishedAt != nil {
		cutoff = *row.FinishedAt
	}
	elapsed := max(int64(0), cutoff.Sub(row.RequestedAt).Milliseconds())
	cumulative := int64(0)
	running := 0
	for _, attempt := range attempts {
		end := cutoff
		if attempt.FinishedAt != nil && attempt.FinishedAt.Before(end) {
			end = *attempt.FinishedAt
		}
		cumulative += max(int64(0), end.Sub(attempt.StartedAt).Milliseconds())
		if row.FinishedAt == nil && attempt.FinishedAt == nil {
			running++
		}
	}
	return &taskdisplay.OrdinaryRunView{RunID: row.RunID, ExecutionID: row.ID, Revision: row.Revision, Status: row.Status, FinalOutputRefs: []string{}, FinalArtifacts: []taskdisplay.PublicArtifact{}, Timing: &taskdisplay.RunTiming{RequestedAt: row.RequestedAt, FinishedAt: row.FinishedAt, MeasuredAt: now, ElapsedMS: &elapsed, CumulativeExecutionMS: &cumulative, RunningAttemptCount: running}}
}
func Snapshot(ctx context.Context, db *gorm.DB, owner, conversationID, runID string, now time.Time) (out *taskdisplay.OrdinaryRunView, err error) {
	if !Available(db) {
		return nil, nil
	}
	err = readTransaction(ctx, db, func(tx *gorm.DB) error {
		var inner error
		out, inner = snapshotRun(ctx, tx, owner, conversationID, runID, now)
		return inner
	})
	return
}
func snapshotRun(ctx context.Context, db *gorm.DB, owner, conversationID, runID string, now time.Time) (*taskdisplay.OrdinaryRunView, error) {
	if !Available(db) {
		return nil, nil
	}
	var rows []orm.TaskExecutionTiming
	if err := db.WithContext(ctx).Where("owner_user_id = ? AND conversation_id = ? AND run_id = ?", owner, conversationID, runID).Order("requested_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	runs, err := measureRuns(ctx, db, rows, now)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}
func List(ctx context.Context, db *gorm.DB, owner, conversationID string, now time.Time) (out []taskdisplay.OrdinaryRunView, err error) {
	if !Available(db) {
		return []taskdisplay.OrdinaryRunView{}, nil
	}
	err = readTransaction(ctx, db, func(tx *gorm.DB) error {
		var inner error
		out, inner = listRuns(ctx, tx, owner, conversationID, now)
		return inner
	})
	return
}
func listRuns(ctx context.Context, db *gorm.DB, owner, conversationID string, now time.Time) ([]taskdisplay.OrdinaryRunView, error) {
	out := []taskdisplay.OrdinaryRunView{}
	if !Available(db) {
		return out, nil
	}
	var rows []orm.TaskExecutionTiming
	if err := db.WithContext(ctx).Where("owner_user_id = ? AND conversation_id = ?", owner, conversationID).Order("requested_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	return measureRuns(ctx, db, rows, now)
}

// Rows are newest first. A Workflow's pause, approval and failed-step retry
// intervals belong to the same unfinished execution. Completion ends that
// accumulation; ordinary Chat rounds remain independent.
func measureRuns(ctx context.Context, db *gorm.DB, rows []orm.TaskExecutionTiming, now time.Time) ([]taskdisplay.OrdinaryRunView, error) {
	out := []taskdisplay.OrdinaryRunView{}
	groups := map[string][]orm.TaskExecutionTiming{}
	order := []string{}
	closed := map[string]bool{}
	ids := []string{}
	for _, row := range rows {
		group := groups[row.RunID]
		if len(group) == 0 {
			order = append(order, row.RunID)
		} else if closed[row.RunID] || row.WorkflowSessionID == "" || !(workflowWait(row.Status) || row.Status == "failed") {
			closed[row.RunID] = true
			continue
		}
		groups[row.RunID] = append(group, row)
		ids = append(ids, row.ID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	var attempts []orm.TaskAttemptTiming
	if err := db.WithContext(ctx).Where("execution_id IN ?", ids).Find(&attempts).Error; err != nil {
		return nil, err
	}
	byExecution := map[string][]orm.TaskAttemptTiming{}
	for _, attempt := range attempts {
		byExecution[attempt.ExecutionID] = append(byExecution[attempt.ExecutionID], attempt)
	}
	for _, runID := range order {
		group := groups[runID]
		view := measure(group[0], byExecution[group[0].ID], now)
		for _, interval := range group[1:] {
			previous := measure(interval, byExecution[interval.ID], now)
			*view.Timing.ElapsedMS += *previous.Timing.ElapsedMS
			*view.Timing.CumulativeExecutionMS += *previous.Timing.CumulativeExecutionMS
		}
		out = append(out, *view)
	}

	return out, nil
}

func transaction(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok || db.SkipDefaultTransaction {
		return fn(db.WithContext(ctx))
	}
	return common.ImmediateTransactionWithSQLiteBusyRetry(ctx, db, fn)
}
func readTransaction(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok || db.SkipDefaultTransaction {
		return fn(db.WithContext(ctx))
	}
	return db.WithContext(ctx).Transaction(fn, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
}
