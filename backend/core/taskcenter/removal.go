package taskcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/conversationgroup"
	"lazymind/core/store"
	"lazymind/core/tasktiming"
)

const removalCancellationJob = "task_center.removal_cancel"
const RemovalScopeChangedCode = 2004811

var errRemovalScopeChanged = errors.New("task removal scope changed")
var errRemovalBlocked = errors.New("task removal unavailable")

// OnRemovalCancelHook stops only executions started no later than the deletion.
// It runs in the existing durable worker, after the removal transaction commits.
var OnRemovalCancelHook func(context.Context, string, string, time.Time, []string) error

func init() { asyncjob.Register(removalCancellationJob, runRemovalCancellation) }
func runRemovalCancellation(ctx context.Context, job asyncjob.Job, _ asyncjob.Reporter) (asyncjob.Result, error) {
	var payload struct {
		ConversationID   string    `json:"conversation_id"`
		Before           time.Time `json:"before"`
		ChatExecutionIDs []string  `json:"chat_execution_ids"`
	}
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil || payload.ConversationID == "" || payload.Before.IsZero() {
		return asyncjob.Result{Permanent: true}, errors.New("invalid removal cancellation")
	}
	if OnRemovalCancelHook == nil {
		return asyncjob.Result{}, errors.New("task cancellation is not configured")
	}
	if payload.ChatExecutionIDs == nil {
		return asyncjob.Result{Permanent: true}, errors.New("removal cancellation has no execution snapshot")
	}
	return asyncjob.Result{}, OnRemovalCancelHook(ctx, job.CreateUserID, payload.ConversationID, payload.Before, payload.ChatExecutionIDs)
}

type TaskRemovalPreview struct {
	TaskID            string `json:"task_id"`
	TaskCount         int    `json:"task_count"`
	ConversationCount int    `json:"conversation_count"`
	ActiveTaskCount   int    `json:"active_task_count"`
	ImpactVersion     string `json:"impact_version"`
}
type TaskRemovalResult struct {
	RemovedTaskIDs         []string `json:"removed_task_ids"`
	TrashedConversationIDs []string `json:"trashed_conversation_ids"`
	CancellationPending    bool     `json:"cancellation_pending"`
}
type removalScope struct {
	target        orm.TaskCenterTask
	tasks         []orm.TaskCenterTask
	conversations []orm.Conversation
}

func loadRemovalScope(ctx context.Context, tx *gorm.DB, owner, id string) (removalScope, error) {
	var s removalScope
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, owner).Take(&s.target).Error; err != nil {
		return s, err
	}
	if s.target.ArchivedAt != nil && s.target.ArchivedReason != ArchivedReasonConversationTrash && s.target.ArchivedReason != ArchivedReasonTaskRemove {
		return s, gorm.ErrRecordNotFound
	}
	if s.target.ArchivedAt != nil && s.target.ArchivedReason == ArchivedReasonTaskRemove {
		s.tasks = []orm.TaskCenterTask{s.target}
		return s, nil
	}
	if s.target.ConversationID != "" {
		var c orm.Conversation
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", s.target.ConversationID).Take(&c).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return s, err
		}
		if err == nil {
			if c.CreateUserID != owner {
				return s, gorm.ErrRecordNotFound
			}
			if c.ParentConversationID != nil {
				return s, errRemovalBlocked
			}
			s.conversations = append(s.conversations, c)
			var children []orm.Conversation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("parent_conversation_id=? AND create_user_id=? AND relation_type=?", c.ID, owner, "sidechat").Order("id").Find(&children).Error; err != nil {
				return s, err
			}
			s.conversations = append(s.conversations, children...)
		}
	}
	if len(s.conversations) == 0 {
		s.tasks = []orm.TaskCenterTask{s.target}
	} else {
		ids := make([]string, 0, len(s.conversations))
		for _, c := range s.conversations {
			ids = append(ids, c.ID)
		}
		if err := conversationgroup.RequireOrganizerUnlocked(ctx, tx, owner, ids, ""); err != nil {
			return s, errRemovalBlocked
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND conversation_id IN ? AND (archived_at IS NULL OR archived_reason IN ?)", owner, ids, []string{ArchivedReasonConversationTrash, ArchivedReasonConversationArchive}).Order("id").Find(&s.tasks).Error; err != nil {
			return s, err
		}
	}
	return s, nil
}
func (s removalScope) preview() TaskRemovalPreview {
	p := TaskRemovalPreview{TaskID: s.target.ID, TaskCount: len(s.tasks), ConversationCount: len(s.conversations)}
	for _, task := range s.tasks {
		if task.ArchivedAt == nil && !isTerminal(task.Status) {
			p.ActiveTaskCount++
		}
	}
	// Bind confirmation to membership and conversation revisions, not progress
	// updates: streaming task progress must not invalidate every confirmation.
	type convRevision struct {
		ID        string
		UpdatedAt time.Time
		DeletedAt *time.Time
	}
	ids := []string{}
	conversations := []convRevision{}
	for _, task := range s.tasks {
		ids = append(ids, task.ID)
	}
	for _, c := range s.conversations {
		conversations = append(conversations, convRevision{c.ID, c.UpdatedAt, c.DeletedAt})
	}
	raw, _ := json.Marshal(struct {
		IDs           []string
		Conversations []convRevision
	}{ids, conversations})
	sum := sha256.Sum256(raw)
	p.ImpactVersion = hex.EncodeToString(sum[:])
	return p
}
func previewTaskRemoval(ctx context.Context, db *gorm.DB, owner, id string) (TaskRemovalPreview, error) {
	var p TaskRemovalPreview
	err := conversationgroup.UserTransaction(ctx, db, owner, func(tx *gorm.DB) error {
		s, err := loadRemovalScope(ctx, tx, owner, id)
		if err != nil {
			return err
		}
		p = s.preview()
		return nil
	})
	return p, err
}
func removeTaskWithImpact(ctx context.Context, db *gorm.DB, owner, id, version string) (TaskRemovalResult, error) {
	result := TaskRemovalResult{RemovedTaskIDs: []string{}, TrashedConversationIDs: []string{}}
	err := conversationgroup.UserTransaction(ctx, db, owner, func(tx *gorm.DB) error {
		return removeTaskInTransaction(ctx, tx, owner, id, version, &result)
	})
	sort.Strings(result.RemovedTaskIDs)
	sort.Strings(result.TrashedConversationIDs)
	return result, err
}

func removalHasActiveRun(ctx context.Context, db *gorm.DB, owner, conversation string, before time.Time) (bool, error) {
	var count int64
	if db.Migrator().HasTable(&orm.WorkflowSession{}) {
		if err := db.WithContext(ctx).Model(&orm.WorkflowSession{}).Where("create_user_id=? AND conversation_id=? AND created_at<=? AND status=?", owner, conversation, before, "active").Count(&count).Error; err != nil || count > 0 {
			return count > 0, err
		}
	}
	if db.Migrator().HasTable(&orm.ExternalChatRun{}) {
		if err := db.WithContext(ctx).Model(&orm.ExternalChatRun{}).Where("actor_user_id=? AND conversation_id=? AND created_at<=? AND status IN ?", owner, conversation, before, []string{"pending", "running"}).Count(&count).Error; err != nil {
			return false, err
		}
	}
	return count > 0, nil
}

// PendingRemovalCancellation prevents restoring a conversation while an old
// broad cancellation signal is still queued. Failed jobs remain fenced for retry.
func PendingRemovalCancellation(ctx context.Context, db *gorm.DB, owner, conversationID string) (bool, error) {
	var jobs []orm.AsyncJob
	err := db.WithContext(ctx).Where("job_type=? AND create_user_id=? AND resource_id=? AND status IN ?", removalCancellationJob, owner, conversationID, []string{"pending", "running", "failed"}).Find(&jobs).Error
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.Status == "failed" {
			if _, err := asyncjob.EnqueueInTransaction(ctx, db, asyncjob.EnqueueRequest{JobType: job.JobType, ResourceType: job.ResourceType, ResourceID: job.ResourceID, CreateUserID: owner, IdempotencyKey: job.IdempotencyKey, Payload: job.PayloadJSON, MaxAttempts: 8}); err != nil {
				return true, err
			}
		}
	}
	return len(jobs) > 0, nil
}

func removalRequestID(r *http.Request) string {
	if id := common.PathVar(r, "task_id"); id != "" {
		return id
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/core")
	path = strings.TrimPrefix(path, "/task-center/tasks/")
	return strings.Split(strings.Split(path, "/")[0], ":")[0]
}
func replyRemovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		common.ReplyErr(w, "task not found", 404)
	case errors.Is(err, errRemovalScopeChanged):
		common.ReplyAppErr(w, common.NewAppError(409, RemovalScopeChangedCode, "Task removal scope changed; review the affected items again"))
	case errors.Is(err, errRemovalBlocked):
		common.ReplyAppErr(w, common.NewAppError(409, 2004812, "This task cannot be removed while its related conversation is locked"))
	default:
		common.ReplyAppErr(w, common.NewAppError(500, 2004813, "Unable to move the task to Trash. Please retry"))
	}
}
func TaskRemovalPreviewHandler(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(store.UserID(r))
	if owner == "" {
		common.ReplyErr(w, "unauthorized", 401)
		return
	}
	if store.DB() == nil {
		replyRemovalError(w, errors.New("database unavailable"))
		return
	}
	p, err := previewTaskRemoval(r.Context(), store.DB(), owner, removalRequestID(r))
	if err != nil {
		replyRemovalError(w, err)
		return
	}
	common.ReplyJSON(w, p)
}
func RemoveTaskHandler(w http.ResponseWriter, r *http.Request) {
	owner := strings.TrimSpace(store.UserID(r))
	if owner == "" {
		common.ReplyErr(w, "unauthorized", 401)
		return
	}
	var input struct {
		ImpactVersion string `json:"impact_version"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&input)
	if err != nil && !errors.Is(err, io.EOF) || len(input.ImpactVersion) > 64 {
		common.ReplyErr(w, "invalid body", 400)
		return
	}
	if store.DB() == nil {
		replyRemovalError(w, errors.New("database unavailable"))
		return
	}
	result, err := removeTaskWithImpact(r.Context(), store.DB(), owner, removalRequestID(r), input.ImpactVersion)
	if err != nil {
		replyRemovalError(w, err)
		return
	}
	for _, id := range result.TrashedConversationIDs {
		if OnConversationTrashHook != nil {
			OnConversationTrashHook(id)
		}
	}
	common.ReplyJSON(w, result)
}

func removeTaskInTransaction(ctx context.Context, tx *gorm.DB, owner, id, version string, result *TaskRemovalResult) error {
	s, err := loadRemovalScope(ctx, tx, owner, id)
	if err != nil {
		return err
	}
	if s.target.ArchivedAt != nil {
		for _, task := range s.tasks {
			if task.ArchivedAt != nil && task.ArchivedAt.Equal(*s.target.ArchivedAt) {
				result.RemovedTaskIDs = append(result.RemovedTaskIDs, task.ID)
			}
		}
		for _, c := range s.conversations {
			if c.DeletedAt != nil {
				result.TrashedConversationIDs = append(result.TrashedConversationIDs, c.ID)
			}
		}
		return nil
	}
	// Successful retries do not renew retention or enqueue cancellation twice.
	if version != "" && version != s.preview().ImpactVersion {
		return errRemovalScopeChanged
	}
	now := time.Now().UTC()
	schedules := []string{}
	cancelConversations := map[string]bool{}
	executions := map[string][]string{}
	for _, task := range s.tasks {
		result.RemovedTaskIDs = append(result.RemovedTaskIDs, task.ID)
		if task.ScheduleID != nil {
			schedules = append(schedules, *task.ScheduleID)
		}
		if task.ArchivedAt == nil && !isTerminal(task.Status) && len(s.conversations) > 0 {
			cancelConversations[task.ConversationID] = true
		}
	}
	for _, c := range s.conversations {
		ids, err := removalChatExecutionIDs(ctx, c.ID, now)
		if err != nil {
			return err
		}
		executions[c.ID] = ids
		active, err := removalHasActiveRun(ctx, tx, owner, c.ID, now)
		if err != nil {
			return err
		}
		if len(ids) > 0 || active {
			cancelConversations[c.ID] = true
		}
		if err := tasktiming.CancelConversation(ctx, tx, owner, c.ID, now); err != nil {
			return err
		}
		result.TrashedConversationIDs = append(result.TrashedConversationIDs, c.ID)
		if c.DeletedAt == nil {
			if err := tx.Model(&orm.Conversation{}).Where("id=? AND create_user_id=?", c.ID, owner).Updates(map[string]any{"deleted_at": now, "trash_expires_at": now.Add(30 * 24 * time.Hour), "archived_at": nil, "archive_folder_id": nil, "updated_at": now}).Error; err != nil {
				return err
			}
		}
	}
	reason := ArchivedReasonConversationTrash
	if len(s.conversations) == 0 {
		reason = ArchivedReasonTaskRemove
	}
	for _, task := range s.tasks {
		if task.ArchivedAt != nil && task.ArchivedReason != ArchivedReasonConversationArchive {
			continue
		}
		changes := map[string]any{"archived_at": now, "archived_reason": reason, "updated_at": now}
		if !isTerminal(task.Status) {
			changes["status"] = "canceled"
			changes["dependency_status"] = "canceled"
			changes["finished_at"] = now
		}
		if err := tx.Model(&orm.TaskCenterTask{}).Where("id=? AND user_id=?", task.ID, owner).Updates(changes).Error; err != nil {
			return err
		}
	}
	if err := recountScheduleRuns(ctx, tx, owner, schedules); err != nil {
		return err
	}
	for conv := range cancelConversations {
		_, err = asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: removalCancellationJob, ResourceType: "conversation", ResourceID: conv, CreateUserID: owner, IdempotencyKey: id + ":" + conv + ":" + now.Format(time.RFC3339Nano), Payload: map[string]any{"conversation_id": conv, "before": now, "chat_execution_ids": executions[conv]}, MaxAttempts: 8})
		if err != nil {
			return err
		}
		result.CancellationPending = true
	}
	return nil
}

// RemoveAcademicConversation participates in the caller's user/record transaction.
// It reuses task removal's execution snapshot and durable cancellation.
func RemoveAcademicConversation(ctx context.Context, tx *gorm.DB, owner, conversation string) error {
	var task orm.TaskCenterTask
	err := tx.Where("user_id=? AND conversation_id=?", owner, conversation).Order("created_at DESC").Take(&task).Error
	if err == nil {
		result := TaskRemovalResult{}
		return removeTaskInTransaction(ctx, tx, owner, task.ID, "", &result)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var row orm.Conversation
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND create_user_id=?", conversation, owner).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.DeletedAt != nil {
		return nil
	}
	if err = conversationgroup.RequireOrganizerUnlocked(ctx, tx, owner, []string{conversation}, ""); err != nil {
		return err
	}
	now := time.Now().UTC()
	return tx.Model(&row).Updates(map[string]any{"deleted_at": now, "trash_expires_at": now.Add(30 * 24 * time.Hour), "updated_at": now}).Error
}
