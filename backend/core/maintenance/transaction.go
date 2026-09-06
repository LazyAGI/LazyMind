// Package maintenance coordinates short database mutations with maintenance leases.
package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

var ErrLeaseLost = errors.New("task_lease_lost: maintenance execution is no longer valid; mutation=none")

type PreferenceOrganizingError struct{ TaskID string }

func (e *PreferenceOrganizingError) Error() string {
	return "preference_organizing: Preference Organizer is running; mutation=none"
}

type Identity struct{ TaskID, RunID string }
type identityKey struct{}

func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}
func Execution(ctx context.Context) Identity { id, _ := ctx.Value(identityKey{}).(Identity); return id }
func RequestContext(r *http.Request) context.Context {
	taskID := r.Header.Get("X-LazyMind-Task-Id")
	if taskID == "" {
		taskID = r.URL.Query().Get("task_id")
	}
	return WithIdentity(r.Context(), Identity{TaskID: taskID, RunID: r.Header.Get("X-LazyMind-Run-Id")})
}

// UserTransaction always acquires the user lock before task/data row locks.
// The SQLite connection is pinned and BEGIN IMMEDIATE is limited to this helper.
// Callbacks must not perform network IO or open nested transactions.
func UserTransaction(ctx context.Context, db *gorm.DB, userID string, fn func(*gorm.DB) error) error {
	if db.Dialector.Name() == "sqlite" {
		return common.ImmediateTransactionWithSQLiteBusyRetry(ctx, db, fn)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sum := sha256.Sum256([]byte("memory-maintenance:" + strings.TrimSpace(userID)))
		key := int64(binary.BigEndian.Uint64(sum[:8]))
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", key).Error; err != nil {
			return err
		}
		return fn(tx)
	})
}

func taskIdentity(id Identity) (string, string, bool) {
	for prefix, kind := range map[string]string{"preference_organizer_": orm.ResourceUpdateTaskTypeOrganizePreference, "memory_review_": orm.ResourceUpdateTaskTypeGenerateReview} {
		if strings.HasPrefix(id.TaskID, prefix) {
			return strings.TrimPrefix(id.TaskID, prefix), kind, true
		}
	}
	return "", "", false
}

// Authorize must run inside UserTransaction, immediately before mutation.
// Even non-preference writes by maintenance executions need a live lease.
func Authorize(ctx context.Context, tx *gorm.DB, userID string, freezePreference bool) error {
	id := Execution(ctx)
	taskID, kind, required := taskIdentity(id)
	if required {
		if id.RunID == "" {
			return ErrLeaseLost
		}
		var task orm.ResourceUpdateTask
		q := tx.Where("id = ? AND user_id = ? AND task_type = ? AND run_id = ? AND status = ? AND locked_until > ?", taskID, userID, kind, id.RunID, orm.ResourceUpdateTaskStatusRunning, time.Now().UTC())
		if tx.Dialector.Name() != "sqlite" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := q.Take(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLeaseLost
			}
			return err
		}
		if kind == orm.ResourceUpdateTaskTypeGenerateReview && task.ResourceType != orm.ResourceUpdateResourceTypeMemory {
			return ErrLeaseLost
		}
	}
	if !freezePreference {
		return nil
	}
	// Small isolated repositories used by callers may not include task storage.
	if !required && !tx.Migrator().HasTable(&orm.ResourceUpdateTask{}) {
		return nil
	}
	var organizer orm.ResourceUpdateTask
	err := tx.Where("user_id = ? AND task_type = ? AND status = ?", userID, orm.ResourceUpdateTaskTypeOrganizePreference, orm.ResourceUpdateTaskStatusRunning).Take(&organizer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if required && kind == orm.ResourceUpdateTaskTypeOrganizePreference && taskID == organizer.ID {
		return nil
	}
	return &PreferenceOrganizingError{TaskID: organizer.ID}
}
