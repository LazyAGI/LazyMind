package taskguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/state"
)

type SkillOperation string

const (
	TriggerSkillReview   SkillOperation = "trigger_skill_review"
	TriggerSkillOrganize SkillOperation = "trigger_skill_organize"
	StartUserEdit        SkillOperation = "start_user_edit"
	WriteSkillDraft      SkillOperation = "write_skill_draft"
	AutoUpdateSkill      SkillOperation = "auto_update_skill"
)

const (
	ReasonMaintenanceTaskRunning = "skill_maintenance_task_running"
	ReasonOrganizeDraftConflict  = "skill_organize_draft_conflict"
	ReasonDraftOwnedByOtherTask  = "skill_draft_owned_by_another_task"
	ReasonDraftStillEditing      = "skill_draft_still_editing"
	ReasonTaskStatusUnavailable  = "skill_task_status_unavailable"
	ReasonTaskNotRunning         = "skill_task_not_running"
	ReasonUserManagedDraft       = "skill_user_managed_draft"

	DispositionReject = "reject"
	DispositionDefer  = "defer"
)

const (
	conversationIdleTTLKeyPrefix = "lazymind:conversation_idle:ttl:"
	defaultDeferredRetryAfter    = time.Minute
	triggerSourceScheduled       = "scheduled"
	skillReviewTaskIDPrefix      = "review_"
	skillOrganizeTaskIDPrefix    = "org_"
)

type SkillOperationRequest struct {
	UserID        string
	SkillID       string
	SkillIDs      []string
	TaskID        string
	Operation     SkillOperation
	TriggerSource string
}

type RunningSkillTask struct {
	ID        string `json:"id"`
	RequestID string `json:"request_id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
}

type SkillOperationDecision struct {
	Allowed        bool
	ReasonCode     string
	Message        string
	Disposition    string
	RetryAfter     time.Duration
	BlockingTask   *RunningSkillTask
	BlockingSkills []string
	DraftTaskID    string
	DraftVersion   int64
}

type draftState struct {
	SkillID      string `gorm:"column:skill_id"`
	RelativeRoot string `gorm:"column:relative_root"`
	TaskID       string `gorm:"column:task_id"`
	Version      int64  `gorm:"column:version"`
	EntryCount   int64  `gorm:"column:entry_count"`
}

// EvaluateSkillOperation is the single policy entry point for Skill task and
// draft admission. Callers provide facts only and must not duplicate its rules.
func EvaluateSkillOperation(ctx context.Context, db *gorm.DB, stateStore state.Store, req SkillOperationRequest) (SkillOperationDecision, error) {
	req.UserID = strings.TrimSpace(req.UserID)
	req.SkillID = strings.TrimSpace(req.SkillID)
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.TriggerSource = strings.TrimSpace(req.TriggerSource)
	if req.UserID == "" {
		return SkillOperationDecision{}, fmt.Errorf("user_id is required")
	}
	if db == nil {
		return SkillOperationDecision{}, fmt.Errorf("task guard db is nil")
	}

	running, err := findRunningSkillMaintenanceTask(ctx, db, req.UserID)
	if err != nil {
		return unavailableDecision(req, "无法确认当前 Skill 任务状态"), err
	}

	switch req.Operation {
	case TriggerSkillReview:
		if running != nil {
			if strings.EqualFold(req.TriggerSource, triggerSourceScheduled) && taskMatchesRunning(req.TaskID, running) {
				return allowedDecision(), nil
			}
			return maintenanceBlockedDecision(req, running), nil
		}
		return allowedDecision(), nil

	case TriggerSkillOrganize:
		if running != nil {
			return maintenanceBlockedDecision(req, running), nil
		}
		drafts, err := loadDraftStates(ctx, db, req.UserID, normalizedSkillIDs(req))
		if err != nil {
			return SkillOperationDecision{}, err
		}
		blocking := draftPaths(drafts)
		if len(blocking) > 0 {
			return blockedDecision(req, ReasonOrganizeDraftConflict, "部分 Skill 存在未完成草稿，整理任务未启动", blocking, nil), nil
		}
		return allowedDecision(), nil

	case StartUserEdit:
		if running != nil {
			return maintenanceBlockedDecision(req, running), nil
		}
		draft, err := loadSingleDraftState(ctx, db, req.UserID, req.SkillID)
		if err != nil {
			return SkillOperationDecision{}, err
		}
		if draft == nil || draft.EntryCount == 0 || strings.TrimSpace(draft.TaskID) == "" {
			return allowedDecision(), nil
		}
		if isMaintenanceTaskID(draft.TaskID) {
			return allowedDecision(), nil
		}
		active, err := isConversationActiveByTaskID(ctx, stateStore, draft.TaskID)
		if err != nil {
			return unavailableDecision(req, "无法确认 Skill Editor 是否仍在编辑"), err
		}
		if active {
			return blockedDecision(req, ReasonDraftStillEditing, "Skill Editor 正在编辑该草稿", nil, nil), nil
		}
		return allowedDecision(), nil

	case WriteSkillDraft:
		if req.SkillID == "" || req.TaskID == "" {
			return SkillOperationDecision{}, fmt.Errorf("skill_id and task_id are required")
		}
		draft, err := loadSingleDraftState(ctx, db, req.UserID, req.SkillID)
		if err != nil {
			return SkillOperationDecision{}, err
		}
		switch taskKind(req.TaskID) {
		case "review", "organize":
			if running == nil || !taskMatchesRunning(req.TaskID, running) {
				if running != nil {
					return maintenanceBlockedDecision(req, running), nil
				}
				return blockedDecision(req, ReasonTaskNotRunning, "当前 Skill 任务已不在运行中", nil, nil), nil
			}
			if taskKind(req.TaskID) == "review" {
				return allowedDecision(), nil
			}
			if draft == nil || draft.EntryCount == 0 || draft.TaskID == req.TaskID {
				return allowedDecision(), nil
			}
			return blockedDecision(req, ReasonDraftOwnedByOtherTask, "草稿属于其他 Skill 编辑任务", []string{skillPath(draft.RelativeRoot)}, running), nil
		default:
			if running != nil {
				return maintenanceBlockedDecision(req, running), nil
			}
			if draft == nil || draft.EntryCount == 0 || draft.TaskID == req.TaskID {
				return allowedDecision(), nil
			}
			return blockedDecision(req, ReasonDraftOwnedByOtherTask, "草稿属于其他 Skill 编辑任务", []string{skillPath(draft.RelativeRoot)}, nil), nil
		}

	case AutoUpdateSkill:
		draft, err := loadSingleDraftState(ctx, db, req.UserID, req.SkillID)
		if err != nil {
			return SkillOperationDecision{}, err
		}
		if running != nil {
			decision := maintenanceBlockedDecision(req, running)
			if draft != nil {
				decision.DraftTaskID = draft.TaskID
				decision.DraftVersion = draft.Version
			}
			return decision, nil
		}
		if draft == nil || draft.EntryCount == 0 {
			return allowedDecision(), nil
		}
		if strings.TrimSpace(draft.TaskID) == "" {
			decision := blockedDecision(req, ReasonUserManagedDraft, "用户管理的草稿需要手动提交", nil, nil)
			decision.DraftVersion = draft.Version
			return decision, nil
		}
		if isMaintenanceTaskID(draft.TaskID) {
			return allowedDraftDecision(draft), nil
		}
		active, err := isConversationActiveByTaskID(ctx, stateStore, draft.TaskID)
		if err != nil {
			return unavailableDecision(req, "无法确认 Skill Editor 是否已结束"), err
		}
		if active {
			decision := blockedDecision(req, ReasonDraftStillEditing, "Skill Editor 仍在编辑该草稿", nil, nil)
			decision.DraftTaskID = draft.TaskID
			decision.DraftVersion = draft.Version
			return decision, nil
		}
		return allowedDraftDecision(draft), nil

	default:
		return SkillOperationDecision{}, fmt.Errorf("unsupported skill operation %q", req.Operation)
	}
}

func findRunningSkillMaintenanceTask(ctx context.Context, db *gorm.DB, userID string) (*RunningSkillTask, error) {
	var row struct {
		ID        string `gorm:"column:id"`
		RequestID string `gorm:"column:requestid"`
		Status    string `gorm:"column:status"`
		StartedAt string `gorm:"column:started_at"`
	}
	err := db.WithContext(ctx).
		Table("skill_review_stats").
		Select("id, requestid, status, started_at").
		Where("userid = ?", userID).
		Scopes(orm.SkillReviewStatsActiveScope).
		Order("started_at DESC, id DESC").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return findCoreSkillMaintenanceReservation(ctx, db, userID)
	}
	if err != nil {
		return nil, err
	}
	taskID := firstNonEmpty(row.RequestID, row.ID)
	return &RunningSkillTask{
		ID:        strings.TrimSpace(row.ID),
		RequestID: strings.TrimSpace(row.RequestID),
		Type:      maintenanceTaskType(taskID),
		Status:    strings.TrimSpace(row.Status),
		StartedAt: strings.TrimSpace(row.StartedAt),
	}, nil
}

func findCoreSkillMaintenanceReservation(ctx context.Context, db *gorm.DB, userID string) (*RunningSkillTask, error) {
	if !db.Migrator().HasTable(&orm.ResourceUpdateTask{}) {
		return nil, nil
	}
	var task orm.ResourceUpdateTask
	err := db.WithContext(ctx).
		Where("user_id = ? AND resource_type = ? AND task_type IN ? AND status IN ?",
			userID,
			orm.ResourceUpdateResourceTypeSkill,
			[]string{orm.ResourceUpdateTaskTypeGenerateReview, orm.ResourceUpdateTaskTypeOrganizeSkill},
			[]string{orm.ResourceUpdateTaskStatusPending, orm.ResourceUpdateTaskStatusRunning}).
		Order("created_at DESC").
		Take(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	requestID := ""
	if len(task.RequestJSON) > 0 {
		var request struct {
			RequestID string `json:"requestid"`
		}
		if json.Unmarshal(task.RequestJSON, &request) == nil {
			requestID = strings.TrimSpace(request.RequestID)
		}
	}
	startedAt := task.CreatedAt.UTC()
	if task.StartedAt != nil {
		startedAt = task.StartedAt.UTC()
	}
	taskType := "skill_review"
	if task.TaskType == orm.ResourceUpdateTaskTypeOrganizeSkill {
		taskType = "skill_organize"
	}
	return &RunningSkillTask{
		ID:        task.ID,
		RequestID: requestID,
		Type:      taskType,
		Status:    task.Status,
		StartedAt: startedAt.Format(time.RFC3339Nano),
	}, nil
}

func loadSingleDraftState(ctx context.Context, db *gorm.DB, userID, skillID string) (*draftState, error) {
	if strings.TrimSpace(skillID) == "" {
		return nil, fmt.Errorf("skill_id is required")
	}
	rows, err := loadDraftStates(ctx, db, userID, []string{skillID})
	if err != nil {
		return nil, err
	}
	return &rows[0], nil
}

func loadDraftStates(ctx context.Context, db *gorm.DB, userID string, skillIDs []string) ([]draftState, error) {
	if len(skillIDs) == 0 {
		return nil, fmt.Errorf("skill_ids is required")
	}
	var rows []draftState
	err := db.WithContext(ctx).
		Table("skills AS s").
		Select("s.id AS skill_id, s.relative_root, COALESCE(d.task_id, '') AS task_id, COALESCE(d.version, 0) AS version, COUNT(e.skill_id) AS entry_count").
		Joins("LEFT JOIN skill_drafts AS d ON d.skill_id = s.id").
		Joins("LEFT JOIN skill_draft_entries AS e ON e.skill_id = s.id").
		Where("s.owner_user_id = ? AND s.deleted_at IS NULL AND s.id IN ?", userID, skillIDs).
		Group("s.id, s.relative_root, d.task_id, d.version").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) != len(skillIDs) {
		return nil, gorm.ErrRecordNotFound
	}
	byID := make(map[string]draftState, len(rows))
	for _, row := range rows {
		byID[row.SkillID] = row
	}
	ordered := make([]draftState, 0, len(skillIDs))
	for _, skillID := range skillIDs {
		ordered = append(ordered, byID[skillID])
	}
	return ordered, nil
}

func isConversationActiveByTaskID(ctx context.Context, stateStore state.Store, taskID string) (bool, error) {
	if stateStore == nil {
		return false, fmt.Errorf("state store is nil")
	}
	return stateStore.Exists(ctx, conversationIdleTTLKeyPrefix+strings.TrimSpace(taskID))
}

func normalizedSkillIDs(req SkillOperationRequest) []string {
	values := append([]string(nil), req.SkillIDs...)
	if req.SkillID != "" {
		values = append(values, req.SkillID)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func draftPaths(drafts []draftState) []string {
	result := make([]string, 0, len(drafts))
	for _, draft := range drafts {
		if draft.EntryCount > 0 {
			result = append(result, skillPath(draft.RelativeRoot))
		}
	}
	sort.Strings(result)
	return result
}

func skillPath(relativeRoot string) string {
	return path.Join("skills", strings.Trim(strings.TrimSpace(relativeRoot), "/"))
}

func taskMatchesRunning(taskID string, running *RunningSkillTask) bool {
	if running == nil {
		return false
	}
	taskID = strings.TrimSpace(taskID)
	return taskID != "" && (taskID == strings.TrimSpace(running.ID) || taskID == strings.TrimSpace(running.RequestID))
}

func isMaintenanceTaskID(taskID string) bool {
	return taskKind(taskID) != "editor"
}

func taskKind(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	switch {
	case strings.HasPrefix(taskID, skillReviewTaskIDPrefix):
		return "review"
	case strings.HasPrefix(taskID, skillOrganizeTaskIDPrefix):
		return "organize"
	default:
		return "editor"
	}
}

func maintenanceTaskType(taskID string) string {
	switch taskKind(taskID) {
	case "organize":
		return "skill_organize"
	default:
		return "skill_review"
	}
}

func allowedDecision() SkillOperationDecision {
	return SkillOperationDecision{Allowed: true}
}

func allowedDraftDecision(draft *draftState) SkillOperationDecision {
	decision := allowedDecision()
	if draft != nil {
		decision.DraftTaskID = strings.TrimSpace(draft.TaskID)
		decision.DraftVersion = draft.Version
	}
	return decision
}

func maintenanceBlockedDecision(req SkillOperationRequest, task *RunningSkillTask) SkillOperationDecision {
	message := "当前正在执行 Skill Review 任务"
	if task != nil && task.Type == "skill_organize" {
		message = "当前正在执行 Skill 整理任务"
	}
	return blockedDecision(req, ReasonMaintenanceTaskRunning, message, nil, task)
}

func unavailableDecision(req SkillOperationRequest, message string) SkillOperationDecision {
	return blockedDecision(req, ReasonTaskStatusUnavailable, message, nil, nil)
}

func blockedDecision(req SkillOperationRequest, code, message string, skills []string, task *RunningSkillTask) SkillOperationDecision {
	disposition := DispositionReject
	retryAfter := time.Duration(0)
	if req.Operation == AutoUpdateSkill || strings.EqualFold(req.TriggerSource, triggerSourceScheduled) {
		disposition = DispositionDefer
		retryAfter = defaultDeferredRetryAfter
	}
	return SkillOperationDecision{
		Allowed:        false,
		ReasonCode:     code,
		Message:        message,
		Disposition:    disposition,
		RetryAfter:     retryAfter,
		BlockingTask:   task,
		BlockingSkills: append([]string(nil), skills...),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
