package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/log"
	"lazymind/core/store"
)

const (
	userActiveThreadStatusCreating = "creating"
	userActiveThreadStatusActive   = "active"
	userActiveThreadStatusFinished = "finished"

	userActiveThreadCreateLease = 2 * time.Minute

	userActiveThreadExistsType    = "USER_ACTIVE_THREAD_EXISTS"
	userActiveThreadExistsMessage = "当前已有任务正在运行，请先等待完成、暂停或取消后再继续该历史任务。"
)

type userActiveThreadCreationGuard struct {
	userID      string
	createToken string
	committed   bool
}

type userActiveThreadError struct {
	message    string
	statusCode int
	data       map[string]any
}

func (e *userActiveThreadError) Error() string {
	return e.message
}

func reserveUserActiveThreadCreation(ctx context.Context, db *gorm.DB, r *http.Request) (*userActiveThreadCreationGuard, error) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		return nil, &userActiveThreadError{
			message:    "missing X-User-Id",
			statusCode: http.StatusBadRequest,
		}
	}

	for attempt := 0; attempt < 3; attempt++ {
		guard, retry, err := tryReserveUserActiveThreadCreation(ctx, db, r, userID)
		if err != nil || !retry {
			return guard, err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, &userActiveThreadError{
		message:    "thread creation is busy, please retry",
		statusCode: http.StatusConflict,
	}
}

func ensureUserCanActivateThread(ctx context.Context, db *gorm.DB, r *http.Request, targetThreadID string) error {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		return &userActiveThreadError{
			message:    "missing X-User-Id",
			statusCode: http.StatusBadRequest,
		}
	}
	targetThreadID = strings.TrimSpace(targetThreadID)
	if targetThreadID == "" {
		return &userActiveThreadError{
			message:    "thread_id required",
			statusCode: http.StatusBadRequest,
		}
	}

	for attempt := 0; attempt < 3; attempt++ {
		retry, err := tryEnsureUserCanActivateThread(ctx, db, r, userID, targetThreadID)
		if err != nil || !retry {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return &userActiveThreadError{
		message:    "thread activation is busy, please retry",
		statusCode: http.StatusConflict,
	}
}

func tryEnsureUserCanActivateThread(ctx context.Context, db *gorm.DB, r *http.Request, userID, targetThreadID string) (bool, error) {
	now := time.Now().UTC()
	var active orm.AgentUserActiveThread
	err := db.Where("user_id = ?", userID).First(&active).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		created, err := createUserActiveThreadActivation(db, userID, targetThreadID, now)
		if err != nil || created {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}

	status := strings.TrimSpace(active.Status)
	if strings.EqualFold(status, userActiveThreadStatusCreating) {
		if active.LeaseUntil.After(now) {
			return false, &userActiveThreadError{
				message:    "thread creation already in progress",
				statusCode: http.StatusConflict,
				data: map[string]any{
					"lease_until": active.LeaseUntil,
				},
			}
		}
		if err := deleteExpiredCreatingActiveThread(db, userID, now); err != nil {
			return false, err
		}
		return true, nil
	}

	activeThreadID := strings.TrimSpace(active.ThreadID)
	if isTerminalUserActiveThreadStatus(status) || activeThreadID == "" {
		if err := activateUserThread(db, userID, targetThreadID, now); err != nil {
			return false, err
		}
		return false, nil
	}
	if activeThreadID == targetThreadID {
		return false, nil
	}

	flowStatus, flowErr := fetchThreadFlowStatus(ctx, r, activeThreadID)
	if flowErr != nil {
		if isThreadFlowNotFound(flowErr) {
			if err := activateUserThread(db, userID, targetThreadID, now); err != nil {
				return false, err
			}
			return false, nil
		}
		return false, &userActiveThreadError{
			message:    "active thread status unknown",
			statusCode: http.StatusConflict,
			data: map[string]any{
				"thread_id": activeThreadID,
				"detail":    flowErr.Error(),
			},
		}
	}
	if isThreadFlowRunning(flowStatus) {
		return false, &userActiveThreadError{
			message:    userActiveThreadExistsMessage,
			statusCode: http.StatusConflict,
			data: map[string]any{
				"type":        userActiveThreadExistsType,
				"thread_id":   activeThreadID,
				"flow_status": flowStatus,
			},
		}
	}

	if err := activateUserThread(db, userID, targetThreadID, now); err != nil {
		return false, err
	}
	return false, nil
}

func tryReserveUserActiveThreadCreation(ctx context.Context, db *gorm.DB, r *http.Request, userID string) (*userActiveThreadCreationGuard, bool, error) {
	now := time.Now().UTC()
	var active orm.AgentUserActiveThread
	err := db.Where("user_id = ?", userID).First(&active).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		guard, created, err := createUserActiveThreadReservation(db, userID, now)
		if err != nil || created {
			return guard, false, err
		}
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}

	status := strings.TrimSpace(active.Status)
	if strings.EqualFold(status, userActiveThreadStatusCreating) {
		if active.LeaseUntil.After(now) {
			return nil, false, &userActiveThreadError{
				message:    "thread creation already in progress",
				statusCode: http.StatusConflict,
				data: map[string]any{
					"lease_until": active.LeaseUntil,
				},
			}
		}
		if err := deleteExpiredCreatingActiveThread(db, userID, now); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	if isTerminalUserActiveThreadStatus(status) {
		if err := deleteUserActiveThread(db, userID, active.ThreadID); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	threadID := strings.TrimSpace(active.ThreadID)
	if threadID == "" {
		if err := deleteUserActiveThread(db, userID, ""); err != nil {
			return nil, false, err
		}
		return nil, true, nil
	}

	flowStatus, flowErr := fetchThreadFlowStatus(ctx, r, threadID)
	if flowErr != nil {
		if isThreadFlowNotFound(flowErr) {
			if err := markUserActiveThreadFinished(db, threadID); err != nil {
				return nil, false, err
			}
			return nil, true, nil
		}
		return nil, false, &userActiveThreadError{
			message:    "active thread status unknown",
			statusCode: http.StatusConflict,
			data: map[string]any{
				"thread_id": threadID,
				"detail":    flowErr.Error(),
			},
		}
	}
	if isThreadFlowRunning(flowStatus) {
		return nil, false, &userActiveThreadError{
			message:    userActiveThreadExistsMessage,
			statusCode: http.StatusConflict,
			data: map[string]any{
				"type":        userActiveThreadExistsType,
				"thread_id":   threadID,
				"flow_status": flowStatus,
			},
		}
	}

	if err := markUserActiveThreadFinished(db, threadID); err != nil {
		return nil, false, err
	}
	return nil, true, nil
}

func createUserActiveThreadReservation(db *gorm.DB, userID string, now time.Time) (*userActiveThreadCreationGuard, bool, error) {
	token := sha256Hex(userID + ":" + newStreamRecordID())
	row := orm.AgentUserActiveThread{
		UserID:      userID,
		Status:      userActiveThreadStatusCreating,
		CreateToken: token,
		LeaseUntil:  now.Add(userActiveThreadCreateLease),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, false, nil
	}
	return &userActiveThreadCreationGuard{
		userID:      userID,
		createToken: token,
	}, true, nil
}

func createUserActiveThreadActivation(db *gorm.DB, userID, threadID string, now time.Time) (bool, error) {
	row := orm.AgentUserActiveThread{
		UserID:      userID,
		ThreadID:    threadID,
		Status:      userActiveThreadStatusActive,
		CreateToken: "",
		LeaseUntil:  now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func activateUserThread(db *gorm.DB, userID, threadID string, now time.Time) error {
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"thread_id":    threadID,
			"status":       userActiveThreadStatusActive,
			"create_token": "",
			"lease_until":  now,
			"updated_at":   now,
		}),
	}).Create(&orm.AgentUserActiveThread{
		UserID:      userID,
		ThreadID:    threadID,
		Status:      userActiveThreadStatusActive,
		CreateToken: "",
		LeaseUntil:  now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error
}

func deleteExpiredCreatingActiveThread(db *gorm.DB, userID string, now time.Time) error {
	return db.Where(
		"user_id = ? AND status = ? AND lease_until <= ?",
		userID,
		userActiveThreadStatusCreating,
		now,
	).Delete(&orm.AgentUserActiveThread{}).Error
}

func deleteUserActiveThread(db *gorm.DB, userID, threadID string) error {
	query := db.Where("user_id = ?", userID)
	if threadID != "" {
		query = query.Where("thread_id = ?", threadID)
	}
	return query.Delete(&orm.AgentUserActiveThread{}).Error
}

func (g *userActiveThreadCreationGuard) Commit(db *gorm.DB, threadID string) error {
	if g == nil || g.committed {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return fmt.Errorf("thread_id required")
	}
	now := time.Now().UTC()
	result := db.Model(&orm.AgentUserActiveThread{}).
		Where("user_id = ? AND create_token = ? AND status = ?", g.userID, g.createToken, userActiveThreadStatusCreating).
		Updates(map[string]any{
			"thread_id":    threadID,
			"status":       userActiveThreadStatusActive,
			"create_token": "",
			"lease_until":  now,
			"updated_at":   now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("active thread reservation no longer owns user %s", g.userID)
	}
	g.committed = true
	return nil
}

func (g *userActiveThreadCreationGuard) Abort(db *gorm.DB) {
	if g == nil || g.committed {
		return
	}
	if err := db.Where(
		"user_id = ? AND create_token = ? AND status = ?",
		g.userID,
		g.createToken,
		userActiveThreadStatusCreating,
	).Delete(&orm.AgentUserActiveThread{}).Error; err != nil {
		log.Logger.Warn().Err(err).Str("user_id", g.userID).Msg("abort agent active thread reservation failed")
	}
}

func markUserActiveThreadFinished(db *gorm.DB, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if db == nil || threadID == "" {
		return nil
	}
	now := time.Now().UTC()
	return db.Model(&orm.AgentUserActiveThread{}).
		Where("thread_id = ?", threadID).
		Updates(map[string]any{
			"status":      userActiveThreadStatusFinished,
			"lease_until": now,
			"updated_at":  now,
		}).Error
}

func isTerminalUserActiveThreadStatus(status string) bool {
	return strings.EqualFold(status, userActiveThreadStatusFinished) ||
		strings.EqualFold(status, "failed") ||
		strings.EqualFold(status, "cancelled")
}

func isThreadFlowRunning(flowStatus *threadFlowStatusResponse) bool {
	if flowStatus == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(flowStatus.Status)) {
	case "running", "pending", "waiting_checkpoint", "paused":
		return true
	}
	return len(flowStatus.ActiveTaskIDs) > 0
}

func isThreadFlowNotFound(err error) bool {
	var httpErr *common.HTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound
}

func replyUserActiveThreadError(w http.ResponseWriter, err error) {
	var activeErr *userActiveThreadError
	if errors.As(err, &activeErr) {
		if len(activeErr.data) > 0 {
			common.ReplyErrWithData(w, activeErr.message, activeErr.data, activeErr.statusCode)
			return
		}
		common.ReplyErr(w, activeErr.message, activeErr.statusCode)
		return
	}
	common.ReplyErr(w, fmt.Sprintf("%s: %v", "reserve active thread failed", err), http.StatusInternalServerError)
}
