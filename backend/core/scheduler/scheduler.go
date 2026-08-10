// Package scheduler manages recurring user-defined chat triggers (UserSchedule).
// On each cron tick, it creates a fresh conversation (is_task_conv=true), a TaskCenterTask
// (task_type=scheduled), and posts a chat request to the internal chat service URL.
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // embed IANA zones for packaged desktop runtimes

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
)

// ── DB helpers ───────────────────────────────────────────────────────────────

// CreateSchedule inserts a new UserSchedule and computes the first next_run_at.
func CreateSchedule(ctx context.Context, db *gorm.DB, s *orm.UserSchedule) error {
	if s.ID == "" {
		s.ID = common.GeneratePrefixedID("sched_", 36)
	}
	s.CreatedAt = time.Now().UTC()
	if s.KbIDs == "" {
		s.KbIDs = "[]"
	}
	if s.FileIDs == "" {
		s.FileIDs = "[]"
	}
	if s.NextRunAt.IsZero() {
		next, err := nextCronTime(s.CronExpr, s.Timezone)
		if err != nil {
			return err
		}
		s.NextRunAt = next.UTC()
	}
	return db.WithContext(ctx).Create(s).Error
}

// ListSchedules returns schedules for a user. When includeDisabled is true, both
// enabled and disabled schedules are returned; otherwise only enabled ones.
func ListSchedules(ctx context.Context, db *gorm.DB, userID string, includeDisabled bool) ([]orm.UserSchedule, error) {
	var rows []orm.UserSchedule
	q := db.WithContext(ctx).Where("user_id = ?", userID)
	if !includeDisabled {
		q = q.Where("enabled = true")
	}
	if err := q.Order("created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// CancelSchedule disables a schedule owned by userID.
func CancelSchedule(ctx context.Context, db *gorm.DB, userID, id string) error {
	return db.WithContext(ctx).Model(&orm.UserSchedule{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{"enabled": false}).Error
}

// DeleteSchedule permanently removes a schedule rule and its dependency edges.
// Historical task-center runs are intentionally kept; they are independent
// execution records and can still be removed from the task center separately.
func DeleteSchedule(ctx context.Context, db *gorm.DB, userID, id string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var schedule orm.UserSchedule
		if err := tx.Where("id = ? AND user_id = ?", id, userID).First(&schedule).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND (source_schedule_id = ? OR target_schedule_id = ?)", userID, id, id).
			Delete(&orm.ScheduleDependency{}).Error; err != nil {
			return err
		}
		result := tx.Where("id = ? AND user_id = ?", id, userID).Delete(&orm.UserSchedule{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

// nextCronTime parses a cron expression and returns the next fire time.
// Only standard 5-field cron is supported ("minute hour dom month dow").
// Returns an error if the expression is invalid.
func nextCronTime(expr, tz string) (time.Time, error) {
	return nextCronTimeAfter(expr, tz, time.Now())
}

func nextCronTimeAfter(expr, tz string, after time.Time) (time.Time, error) {
	// Lightweight 5-field cron parser.  Supports */N, ranges, and lists.
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression: unsupported timezone %q: %w", tz, err)
	}
	interval, cadenceUnit, cronExpr, err := parseCadenceExpr(expr)
	if err != nil {
		return time.Time{}, err
	}
	fields := strings.Fields(cronExpr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have 5 fields (minute hour dom month dow)")
	}
	// Use a simple tick-forward: start from now + 1 minute, advance up to 1 year.
	now := after.In(loc)
	t := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), 0, 0, loc).Add(time.Minute)
	for i := 0; i < 5*525600; i++ { // cover long month cadences up to roughly four years
		if matchCron(t, fields) && matchCadence(t, interval, cadenceUnit) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron expression produces no future times within 5 years")
}

func previousCronTime(expr, tz string, before time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid cron expression: unsupported timezone %q: %w", tz, err)
	}
	interval, cadenceUnit, cronExpr, err := parseCadenceExpr(expr)
	if err != nil {
		return time.Time{}, err
	}
	fields := strings.Fields(cronExpr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have 5 fields (minute hour dom month dow)")
	}
	t := before.In(loc).Truncate(time.Minute).Add(-time.Minute)
	for i := 0; i < 5*525600; i++ {
		if matchCron(t, fields) && matchCadence(t, interval, cadenceUnit) {
			return t, nil
		}
		t = t.Add(-time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron expression has no previous time within 5 years")
}

func parseCadenceExpr(expr string) (int, string, string, error) {
	if !strings.HasPrefix(expr, "@every:") {
		return 1, "", expr, nil
	}
	parts := strings.SplitN(strings.TrimPrefix(expr, "@every:"), ";", 2)
	if len(parts) != 2 {
		return 0, "", "", fmt.Errorf("invalid cadence expression")
	}
	meta := strings.Split(parts[0], ":")
	if len(meta) != 2 || (meta[1] != "week" && meta[1] != "month") {
		return 0, "", "", fmt.Errorf("invalid cadence metadata")
	}
	interval, err := strconv.Atoi(meta[0])
	if err != nil || interval < 1 || interval > 52 {
		return 0, "", "", fmt.Errorf("cadence interval must be between 1 and 52")
	}
	return interval, meta[1], parts[1], nil
}

func matchCadence(t time.Time, interval int, unit string) bool {
	if interval <= 1 {
		return true
	}
	if unit == "month" {
		return (t.Year()*12+int(t.Month())-1)%interval == 0
	}
	year, week := t.ISOWeek()
	return (year*53+week-1)%interval == 0
}

func matchCron(t time.Time, fields []string) bool {
	return matchField(fields[0], t.Minute(), 0, 59) &&
		matchField(fields[1], t.Hour(), 0, 23) &&
		matchDayOfMonth(fields[2], t) &&
		matchField(fields[3], int(t.Month()), 1, 12) &&
		matchField(fields[4], int(t.Weekday()), 0, 6)
}

// matchDayOfMonth additionally accepts -1 through -4 for the last through
// fourth-to-last day of the month. This keeps month-end schedules stable across
// months with different lengths without expanding them into multiple schedules.
func matchDayOfMonth(field string, t time.Time) bool {
	if matchField(field, t.Day(), 1, 31) {
		return true
	}
	lastDay := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
	for _, part := range strings.Split(field, ",") {
		offset, err := strconv.Atoi(part)
		if err == nil && offset >= -4 && offset <= -1 && t.Day() == lastDay+offset+1 {
			return true
		}
	}
	return false
}

func matchField(field string, val, min, max int) bool {
	if field == "*" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if strings.Contains(part, "/") {
			sub := strings.SplitN(part, "/", 2)
			step, err := strconv.Atoi(sub[1])
			if err != nil || step <= 0 {
				continue
			}
			base := min
			if sub[0] != "*" {
				base, _ = strconv.Atoi(sub[0])
			}
			for v := base; v <= max; v += step {
				if v == val {
					return true
				}
			}
		} else if strings.Contains(part, "-") {
			sub := strings.SplitN(part, "-", 2)
			lo, _ := strconv.Atoi(sub[0])
			hi, _ := strconv.Atoi(sub[1])
			if val >= lo && val <= hi {
				return true
			}
		} else {
			n, err := strconv.Atoi(part)
			if err == nil && n == val {
				return true
			}
		}
	}
	return false
}

// truncateRunes truncates s to at most maxRunes Unicode code points,
// appending suffix if truncation occurred. This avoids splitting multi-byte
// characters (e.g. CJK) that would produce invalid UTF-8 sequences in the DB.
func truncateRunes(s string, maxRunes int, suffix string) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + suffix
}

// ── Scheduler loop ────────────────────────────────────────────────────────────

// RunScheduler starts a goroutine that fires due schedules every 30 seconds.
// Call once at application startup. The goroutine stops when ctx is cancelled.
// Task status is now derived on read via resolveTaskStatus (chat_histories presence),
// so no periodic reconciler is needed here.
func RunScheduler(ctx context.Context, db *gorm.DB, chatBaseURL string) {
	go func() {
		repairFutureScheduleNextRunsAt(ctx, db, time.Now().UTC())
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fireSchedules(ctx, db, chatBaseURL)
				resumeWaitingTasks(ctx, db)
			}
		}
	}()
}

// repairFutureScheduleNextRunsAt corrects future timestamps produced when a
// packaged runtime could not load a schedule's IANA timezone and fell back to
// UTC. Overdue timestamps are preserved so the regular scheduler can catch up.
func repairFutureScheduleNextRunsAt(ctx context.Context, db *gorm.DB, now time.Time) {
	var schedules []orm.UserSchedule
	if err := db.WithContext(ctx).
		Where("enabled = true AND next_run_at > ?", now.UTC()).
		Find(&schedules).Error; err != nil {
		fmt.Printf("[Scheduler] repair next run query failed: %v\n", err)
		return
	}
	for _, schedule := range schedules {
		next, err := nextCronTimeAfter(schedule.CronExpr, schedule.Timezone, now)
		if err != nil {
			fmt.Printf("[Scheduler] repair next run skipped schedule %s: %v\n", schedule.ID, err)
			continue
		}
		next = next.UTC()
		if schedule.NextRunAt.Equal(next) {
			continue
		}
		if err := db.WithContext(ctx).Model(&orm.UserSchedule{}).
			Where("id = ? AND next_run_at = ?", schedule.ID, schedule.NextRunAt).
			Update("next_run_at", next).Error; err != nil {
			fmt.Printf("[Scheduler] repair next run failed for schedule %s: %v\n", schedule.ID, err)
		}
	}
}

// maxConcurrentFires is the maximum number of schedules fired concurrently in one tick.
const maxConcurrentFires = 50

// fireSchedules queries all enabled schedules whose next_run_at <= now and fires them.
// At most maxConcurrentFires goroutines run simultaneously to protect downstream services.
func fireSchedules(ctx context.Context, db *gorm.DB, _ string) {
	now := time.Now().UTC()
	var due []orm.UserSchedule
	if err := db.WithContext(ctx).
		Where("enabled = true AND next_run_at <= ?", now).
		Find(&due).Error; err != nil {
		return
	}
	sem := make(chan struct{}, maxConcurrentFires)
	var wg sync.WaitGroup
	for _, s := range due {
		s := s
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			fireOne(ctx, db, s, now)
		}()
	}
	// Do not resolve dependencies until every due schedule has materialized its
	// actual TaskCenterTask. This is the same-tick barrier for dependency chains.
	wg.Wait()
}

func fireOne(ctx context.Context, db *gorm.DB, s orm.UserSchedule, firedAt time.Time) {
	scheduledAt := s.NextRunAt.UTC()
	// Compute next run time first so we can CAS before creating any records.
	next, err := nextCronTime(s.CronExpr, s.Timezone)
	if err != nil {
		next = firedAt.Add(24 * time.Hour)
	}

	// Use an optimistic lock (CAS on next_run_at) to ensure only one instance fires
	// this schedule tick. Do this BEFORE creating conversation/task so we never create
	// orphaned records when two instances race.
	result := db.WithContext(ctx).Model(&orm.UserSchedule{}).
		Where("id = ? AND next_run_at = ?", s.ID, s.NextRunAt).
		Updates(map[string]any{
			"last_run_at": scheduledAt,
			"next_run_at": next.UTC(),
		})
	if result.RowsAffected == 0 {
		// Another instance already fired this schedule tick; skip entirely.
		return
	}
	var depCount int64
	db.WithContext(ctx).Model(&orm.ScheduleDependency{}).Where("target_schedule_id = ? AND enabled = true", s.ID).Count(&depCount)
	if depCount > 0 {
		start := dependencyWindowStart(db, s, scheduledAt)
		createWaitingScheduledTask(ctx, db, s, start, scheduledAt, "scheduled")
		return
	}

	// CAS won — now create conversation and task. Only increment run_count after
	// the task record is successfully persisted so the counter stays in sync.
	convID := createTaskConversation(ctx, db, s.UserID, s.PromptTemplate)
	if convID == "" {
		return
	}

	taskTitle := s.Name
	if taskTitle == "" {
		taskTitle = "Scheduled: " + s.PromptTemplate
	}
	taskTitle = truncateRunes(taskTitle, 40, "...")
	task := &orm.TaskCenterTask{
		UserID:            s.UserID,
		ConversationID:    convID,
		TaskType:          "scheduled",
		Title:             &taskTitle,
		Status:            "running",
		ScheduleID:        &s.ID,
		GroupID:           s.GroupID,
		ScheduledFireAt:   &scheduledAt,
		LogicalSlotKey:    s.ID + ":" + scheduledAt.Format(time.RFC3339Nano),
		TriggerType:       "scheduled",
		DefinitionVersion: s.DefinitionVersion,
	}
	if err := taskcenter.CreateTask(ctx, db, task); err != nil {
		fmt.Printf("[Scheduler] CreateTask failed for schedule %s: %v\n", s.ID, err)
		return
	}
	// Task persisted — now it's safe to increment run_count.
	db.WithContext(ctx).Model(&orm.UserSchedule{}).
		Where("id = ?", s.ID).
		Update("run_count", gorm.Expr("run_count + 1"))

	// Build chat request with kb_ids and file_ids from the schedule definition.
	query := renderPromptTemplate(s.PromptTemplate, firedAt)
	reqBody := map[string]any{
		"query":                 query,
		"conversation_id":       convID,
		"stream":                true,
		"mode":                  "auto",
		"thinking_depth":        "high",
		"input":                 []map[string]any{{"input_type": "text", "text": query}},
		"skip_sensitive_filter": true,
		"disabled_tools":        []string{"ask_user"},
	}
	// Attach knowledge base IDs if configured.
	var kbIDs []string
	if json.Unmarshal([]byte(s.KbIDs), &kbIDs) == nil && len(kbIDs) > 0 {
		reqBody["kb_ids"] = kbIDs
	}
	// Attach pre-uploaded file IDs if configured.
	var fileIDs []string
	if json.Unmarshal([]byte(s.FileIDs), &fileIDs) == nil && len(fileIDs) > 0 {
		reqBody["file_ids"] = fileIDs
	}
	go sendScheduledChatRequest(s.UserID, convID, task.ID, db, reqBody)
}

// createTaskConversation creates a new conversation flagged as is_task_conv=true.
// Workflow and subagent are explicitly enabled so scheduled tasks always run regardless
// of the user's global chat settings.
// Returns the new conversation ID, or "" on failure.
func createTaskConversation(ctx context.Context, db *gorm.DB, userID, promptTemplate string) string {
	displayName := truncateRunes(promptTemplate, 40, "...")
	now := time.Now().UTC()
	enableWorkflow := true
	workflowMode := "auto"
	enableSubagent := true
	conv := orm.Conversation{
		ID:             common.GeneratePrefixedID("conv_", 36),
		DisplayName:    displayName,
		ChannelID:      "default",
		IsTaskConv:     true,
		EnableWorkflow: &enableWorkflow,
		WorkflowMode:   &workflowMode,
		EnableSubagent: &enableSubagent,
		BaseModel: orm.BaseModel{
			CreateUserID: userID,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
	if err := db.WithContext(ctx).Create(&conv).Error; err != nil {
		fmt.Printf("[Scheduler] createTaskConversation: %v\n", err)
		return ""
	}
	return conv.ID
}

// renderPromptTemplate substitutes basic placeholders in the prompt template.
func renderPromptTemplate(tpl string, t time.Time) string {
	r := strings.NewReplacer(
		"{{date}}", t.Format("2006-01-02"),
		"{{time}}", t.Format("15:04"),
		"{{datetime}}", t.Format("2006-01-02 15:04:05"),
	)
	return r.Replace(tpl)
}

// sendScheduledChatRequest fires a chat request for a scheduled task in a background
// goroutine and persists either the finalized output or a concrete failure reason.
func sendScheduledChatRequest(userID, convID, taskID string, db *gorm.DB, reqBody map[string]any) {
	coreURL := common.CoreSelfEndpoint() + "/conversations:chat"
	body, err := json.Marshal(reqBody)
	if err != nil {
		failScheduledTask(db, taskID, "创建任务请求失败："+err.Error())
		return
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, coreURL, bytes.NewReader(body))
	if err != nil {
		fmt.Printf("[Scheduler] sendScheduledChatRequest: build request failed for task %s: %v\n", taskID, err)
		failScheduledTask(db, taskID, "创建任务请求失败："+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-User-Id", userID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("[Scheduler] sendScheduledChatRequest: HTTP error for task %s: %v\n", taskID, err)
		failScheduledTask(db, taskID, scheduledRequestFailureReason(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		failScheduledTask(db, taskID, fmt.Sprintf("任务请求失败：服务返回 HTTP %d", resp.StatusCode))
		return
	}
	// Drain the response body so the upstream goroutines can finish writing to
	// Redis and DB before the task output is finalized.
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		fmt.Printf("[Scheduler] sendScheduledChatRequest: response stream failed for task %s: %v\n", taskID, err)
		failScheduledTask(db, taskID, scheduledRequestFailureReason(err))
		return
	}
	finalizeTaskOutput(context.Background(), db, taskID, convID)
}

func scheduledRequestFailureReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "任务执行超时（超过2小时）"
	case errors.Is(err, context.Canceled):
		return "任务执行被中断"
	default:
		return "任务请求失败：" + err.Error()
	}
}

func failScheduledTask(db *gorm.DB, taskID, reason string) {
	if err := taskcenter.UpdateTaskFailure(context.Background(), db, taskID, reason); err != nil {
		fmt.Printf("[Scheduler] failed to persist failure reason for task %s: %v\n", taskID, err)
	}
}

// ── API handlers ──────────────────────────────────────────────────────────────

type scheduleResponse struct {
	ID             string               `json:"id"`
	UserID         string               `json:"user_id"`
	Name           string               `json:"name"`
	Remark         string               `json:"remark"`
	CronExpr       string               `json:"cron_expr"`
	Timezone       string               `json:"timezone"`
	PromptTemplate string               `json:"prompt_template"`
	KbIDs          []string             `json:"kb_ids"`
	FileIDs        []string             `json:"file_ids"`
	GroupID        *string              `json:"group_id,omitempty"`
	GroupPosition  int                  `json:"group_position"`
	Dependencies   []dependencyResponse `json:"dependencies"`
	Enabled        bool                 `json:"enabled"`
	RunCount       int                  `json:"run_count"`
	LastRunAt      *time.Time           `json:"last_run_at,omitempty"`
	NextRunAt      time.Time            `json:"next_run_at"`
	CreatedAt      time.Time            `json:"created_at"`
}

func toScheduleResponse(s orm.UserSchedule) scheduleResponse {
	var kbIDs []string
	_ = json.Unmarshal([]byte(s.KbIDs), &kbIDs)
	if kbIDs == nil {
		kbIDs = []string{}
	}
	var fileIDs []string
	_ = json.Unmarshal([]byte(s.FileIDs), &fileIDs)
	if fileIDs == nil {
		fileIDs = []string{}
	}
	return scheduleResponse{
		ID:             s.ID,
		UserID:         s.UserID,
		Name:           s.Name,
		Remark:         s.Remark,
		CronExpr:       s.CronExpr,
		Timezone:       s.Timezone,
		PromptTemplate: s.PromptTemplate,
		KbIDs:          kbIDs,
		FileIDs:        fileIDs,
		GroupID:        s.GroupID,
		GroupPosition:  s.GroupPosition,
		Enabled:        s.Enabled,
		RunCount:       s.RunCount,
		LastRunAt:      s.LastRunAt,
		NextRunAt:      s.NextRunAt,
		CreatedAt:      s.CreatedAt,
	}
}

// ListSchedulesHandler handles GET /schedules
// Query params: include_disabled=true to include disabled schedules.
func ListSchedulesHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	includeDisabled := r.URL.Query().Get("include_disabled") == "true"
	db := store.DB()
	rows, err := ListSchedules(r.Context(), db, userID, includeDisabled)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ids := make([]string, 0, len(rows))
	for _, s := range rows {
		ids = append(ids, s.ID)
	}
	deps := loadDependencies(db, userID, ids)
	items := make([]scheduleResponse, 0, len(rows))
	for _, s := range rows {
		item := toScheduleResponse(s)
		item.Dependencies = deps[s.ID]
		if item.Dependencies == nil {
			item.Dependencies = []dependencyResponse{}
		}
		items = append(items, item)
	}
	common.ReplyJSON(w, map[string]any{"items": items, "total": len(items)})
}

// CreateScheduleHandler handles POST /schedules
func CreateScheduleHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	var body struct {
		Name           string            `json:"name"`
		Remark         string            `json:"remark"`
		CronExpr       string            `json:"cron_expr"`
		Timezone       string            `json:"timezone"`
		PromptTemplate string            `json:"prompt_template"`
		KbIDs          []string          `json:"kb_ids"`
		FileIDs        []string          `json:"file_ids"`
		GroupID        *string           `json:"group_id"`
		Dependencies   []dependencyInput `json:"dependencies"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.CronExpr == "" || body.PromptTemplate == "" {
		common.ReplyErr(w, "cron_expr and prompt_template are required", http.StatusBadRequest)
		return
	}
	if err := validateScheduleDescription(r.Context(), body.PromptTemplate); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	tz := body.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	kbIDsJSON := "[]"
	if len(body.KbIDs) > 0 {
		if b, err := json.Marshal(body.KbIDs); err == nil {
			kbIDsJSON = string(b)
		}
	}
	fileIDsJSON := "[]"
	if len(body.FileIDs) > 0 {
		if b, err := json.Marshal(body.FileIDs); err == nil {
			fileIDsJSON = string(b)
		}
	}
	s := &orm.UserSchedule{
		UserID:         userID,
		Name:           body.Name,
		Remark:         body.Remark,
		CronExpr:       body.CronExpr,
		Timezone:       tz,
		PromptTemplate: body.PromptTemplate,
		KbIDs:          kbIDsJSON,
		FileIDs:        fileIDsJSON,
		GroupID:        body.GroupID,
		Enabled:        true,
	}
	db := store.DB()
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := CreateSchedule(r.Context(), tx, s); err != nil {
			return err
		}
		return replaceDependencies(tx, userID, s.ID, body.Dependencies)
	}); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	common.ReplyJSON(w, toScheduleResponse(*s))
}

// CancelScheduleHandler handles POST /schedules/{schedule_id}:cancel
func CancelScheduleHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	path := strings.TrimPrefix(r.URL.Path, "/schedules/")
	id := strings.TrimSuffix(path, ":cancel")

	db := store.DB()
	if err := CancelSchedule(r.Context(), db, userID, id); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}

// DeleteScheduleHandler handles DELETE /schedules/{schedule_id}.
func DeleteScheduleHandler(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "user not found", http.StatusUnauthorized)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/schedules/")
	if id == "" {
		common.ReplyErr(w, "schedule_id required", http.StatusBadRequest)
		return
	}

	err := DeleteSchedule(r.Context(), store.DB(), userID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "schedule not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, nil)
}

// EnableScheduleHandler handles POST /schedules/{schedule_id}:enable
func EnableScheduleHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	path := strings.TrimPrefix(r.URL.Path, "/schedules/")
	id := strings.TrimSuffix(path, ":enable")
	db := store.DB()
	// Recompute next_run_at from now so the schedule fires at the correct future time.
	var s orm.UserSchedule
	if err := db.WithContext(r.Context()).
		Where("id = ? AND user_id = ?", id, userID).First(&s).Error; err != nil {
		common.ReplyErr(w, "schedule not found", http.StatusNotFound)
		return
	}
	next, err := nextCronTime(s.CronExpr, s.Timezone)
	if err != nil {
		common.ReplyErr(w, "invalid cron expression: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := db.WithContext(r.Context()).Model(&orm.UserSchedule{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]any{"enabled": true, "next_run_at": next.UTC()}).Error; err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Enabled = true
	s.NextRunAt = next
	common.ReplyJSON(w, toScheduleResponse(s))
}

// UpdateScheduleHandler handles PUT /schedules/{schedule_id}
func UpdateScheduleHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	id := strings.TrimPrefix(r.URL.Path, "/schedules/")
	var body struct {
		Name           string            `json:"name"`
		Remark         string            `json:"remark"`
		CronExpr       string            `json:"cron_expr"`
		Timezone       string            `json:"timezone"`
		PromptTemplate string            `json:"prompt_template"`
		KbIDs          []string          `json:"kb_ids"`
		FileIDs        []string          `json:"file_ids"`
		GroupID        *string           `json:"group_id"`
		Dependencies   []dependencyInput `json:"dependencies"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	db := store.DB()
	var s orm.UserSchedule
	if err := db.WithContext(r.Context()).
		Where("id = ? AND user_id = ?", id, userID).First(&s).Error; err != nil {
		common.ReplyErr(w, "schedule not found", http.StatusNotFound)
		return
	}
	updates := map[string]any{}
	if body.Name != "" {
		updates["name"] = body.Name
		s.Name = body.Name
	}
	updates["remark"] = body.Remark
	s.Remark = body.Remark
	if body.PromptTemplate != "" {
		if err := validateScheduleDescription(r.Context(), body.PromptTemplate); err != nil {
			common.ReplyErr(w, err.Error(), http.StatusBadRequest)
			return
		}
		updates["prompt_template"] = body.PromptTemplate
		s.PromptTemplate = body.PromptTemplate
	}
	if body.CronExpr != "" {
		tz := body.Timezone
		if tz == "" {
			tz = s.Timezone
		}
		next, err := nextCronTime(body.CronExpr, tz)
		if err != nil {
			common.ReplyErr(w, "invalid cron_expr: "+err.Error(), http.StatusBadRequest)
			return
		}
		updates["cron_expr"] = body.CronExpr
		updates["timezone"] = tz
		updates["next_run_at"] = next.UTC()
		s.CronExpr = body.CronExpr
		s.Timezone = tz
		s.NextRunAt = next.UTC()
	}
	if body.KbIDs != nil {
		if b, err := json.Marshal(body.KbIDs); err == nil {
			updates["kb_ids"] = string(b)
			s.KbIDs = string(b)
		}
	}
	if body.FileIDs != nil {
		if b, err := json.Marshal(body.FileIDs); err == nil {
			updates["file_ids"] = string(b)
			s.FileIDs = string(b)
		}
	}
	if body.GroupID != nil {
		updates["group_id"] = body.GroupID
		s.GroupID = body.GroupID
	}
	if len(updates) == 0 && body.Dependencies == nil {
		common.ReplyJSON(w, toScheduleResponse(s))
		return
	}
	if err := db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&orm.UserSchedule{}).Where("id = ? AND user_id = ?", id, userID).Updates(updates).Error; err != nil {
				return err
			}
		}
		if body.Dependencies != nil {
			return replaceDependencies(tx, userID, id, body.Dependencies)
		}
		return nil
	}); err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyJSON(w, toScheduleResponse(s))
}

// RunNowHandler handles POST /schedules/{schedule_id}:run-now.
// It immediately fires the schedule once without modifying next_run_at,
// and increments run_count so the execution appears in the history.
func RunNowHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	path := strings.TrimPrefix(r.URL.Path, "/schedules/")
	id := strings.TrimSuffix(path, ":run-now")
	db := store.DB()
	var s orm.UserSchedule
	if err := db.WithContext(r.Context()).
		Where("id = ? AND user_id = ?", id, userID).First(&s).Error; err != nil {
		common.ReplyErr(w, "schedule not found", http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	var depCount int64
	db.Model(&orm.ScheduleDependency{}).Where("target_schedule_id = ? AND enabled = true", s.ID).Count(&depCount)
	if depCount > 0 {
		// A manual run uses the click time as its right boundary. Its left boundary
		// is the most recent successful, non-deleted run. On the first effective
		// run, derive one full cycle from the next planned trigger.
		start := dependencyWindowStart(db, s, s.NextRunAt)
		taskID := createWaitingScheduledTask(r.Context(), db, s, start, now, "manual")
		if taskID == "" {
			common.ReplyErr(w, "failed to create waiting task", http.StatusInternalServerError)
			return
		}
		go resumeWaitingTasks(context.Background(), db)
		common.ReplyJSON(w, map[string]any{"task_id": taskID, "conversation_id": "", "status": "waiting_inputs"})
		return
	}
	convID := createTaskConversation(r.Context(), db, s.UserID, s.PromptTemplate)
	if convID == "" {
		common.ReplyErr(w, "failed to create task conversation", http.StatusInternalServerError)
		return
	}
	taskTitle := s.Name
	if taskTitle == "" {
		taskTitle = "Scheduled: " + s.PromptTemplate
	}
	taskTitle = truncateRunes(taskTitle, 40, "...")
	task := &orm.TaskCenterTask{
		UserID:          s.UserID,
		ConversationID:  convID,
		TaskType:        "scheduled",
		Title:           &taskTitle,
		Status:          "running",
		ScheduleID:      &s.ID,
		GroupID:         s.GroupID,
		ScheduledFireAt: &now,
		LogicalSlotKey:  s.ID + ":manual:" + now.Format(time.RFC3339Nano),
		TriggerType:     "manual",
	}
	if err := taskcenter.CreateTask(r.Context(), db, task); err != nil {
		common.ReplyErr(w, "failed to create task: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Increment run_count and record last_run_at without touching next_run_at.
	db.WithContext(r.Context()).Model(&orm.UserSchedule{}).
		Where("id = ?", s.ID).
		Updates(map[string]any{
			"last_run_at": now,
			"run_count":   gorm.Expr("run_count + 1"),
		})
	query := renderPromptTemplate(s.PromptTemplate, now)
	reqBody := map[string]any{
		"query":           query,
		"conversation_id": convID,
		"stream":          true,
		"mode":            "auto",
		"thinking_depth":  "high",
		"input":           []map[string]any{{"input_type": "text", "text": query}},
		"disabled_tools":  []string{"ask_user"},
	}
	var kbIDs []string
	if json.Unmarshal([]byte(s.KbIDs), &kbIDs) == nil && len(kbIDs) > 0 {
		reqBody["kb_ids"] = kbIDs
	}
	var fileIDs []string
	if json.Unmarshal([]byte(s.FileIDs), &fileIDs) == nil && len(fileIDs) > 0 {
		reqBody["file_ids"] = fileIDs
	}
	go sendScheduledChatRequest(s.UserID, convID, task.ID, db, reqBody)
	common.ReplyJSON(w, map[string]any{"task_id": task.ID, "conversation_id": convID})
}
