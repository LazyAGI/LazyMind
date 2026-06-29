// Package scheduler manages recurring user-defined chat triggers (UserSchedule).
// On each cron tick, it creates a fresh conversation (is_task_conv=true), a TaskCenterTask
// (task_type=scheduled), and posts a chat request to the internal chat service URL.
package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
		s.ID = "sched_" + common.GenerateID()
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
		s.NextRunAt = next
	}
	return db.WithContext(ctx).Create(s).Error
}

// ListSchedules returns all active schedules for a user.
func ListSchedules(ctx context.Context, db *gorm.DB, userID string) ([]orm.UserSchedule, error) {
	var rows []orm.UserSchedule
	if err := db.WithContext(ctx).
		Where("user_id = ? AND enabled = true", userID).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
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

// nextCronTime parses a cron expression and returns the next fire time.
// Only standard 5-field cron is supported ("minute hour dom month dow").
// Returns an error if the expression is invalid.
func nextCronTime(expr, tz string) (time.Time, error) {
	// Lightweight 5-field cron parser.  Supports */N, ranges, and lists.
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("cron expression must have 5 fields (minute hour dom month dow)")
	}
	// Use a simple tick-forward: start from now + 1 minute, advance up to 1 year.
	now := time.Now().In(loc)
	t := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), 0, 0, loc).Add(time.Minute)
	for i := 0; i < 525600; i++ { // max 1 year of minutes
		if matchCron(t, fields) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron expression produces no future times within 1 year")
}

func matchCron(t time.Time, fields []string) bool {
	return matchField(fields[0], t.Minute(), 0, 59) &&
		matchField(fields[1], t.Hour(), 0, 23) &&
		matchField(fields[2], t.Day(), 1, 31) &&
		matchField(fields[3], int(t.Month()), 1, 12) &&
		matchField(fields[4], int(t.Weekday()), 0, 6)
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

// ── Scheduler loop ────────────────────────────────────────────────────────────

// RunScheduler starts a goroutine that fires due schedules every minute.
// Call once at application startup. The goroutine stops when ctx is cancelled.
func RunScheduler(ctx context.Context, db *gorm.DB, chatBaseURL string) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fireSchedules(ctx, db, chatBaseURL)
			}
		}
	}()
}

// fireSchedules queries all enabled schedules whose next_run_at <= now and fires them.
func fireSchedules(ctx context.Context, db *gorm.DB, _ string) {
	now := time.Now().UTC()
	var due []orm.UserSchedule
	if err := db.WithContext(ctx).
		Where("enabled = true AND next_run_at <= ?", now).
		Find(&due).Error; err != nil {
		return
	}
	for _, s := range due {
		s := s
		go func() {
			fireOne(ctx, db, s, now)
		}()
	}
}

func fireOne(ctx context.Context, db *gorm.DB, s orm.UserSchedule, firedAt time.Time) {
	// Create a fresh task-conversation for every scheduled trigger.
	convID := createTaskConversation(ctx, db, s.UserID, s.PromptTemplate)

	taskTitle := s.Name
	if taskTitle == "" {
		taskTitle = "Scheduled: " + s.PromptTemplate
	}
	if len(taskTitle) > 120 {
		taskTitle = taskTitle[:120] + "..."
	}
	task := &orm.TaskCenterTask{
		UserID:         s.UserID,
		ConversationID: convID,
		TaskType:       "scheduled",
		Title:          &taskTitle,
		Status:         "running",
		ScheduleID:     &s.ID,
	}
	_ = taskcenter.CreateTask(ctx, db, task)

	// Compute next run time and update the schedule with an optimistic lock (CAS).
	// If another instance already updated next_run_at, RowsAffected == 0 and we skip.
	next, err := nextCronTime(s.CronExpr, s.Timezone)
	if err != nil {
		next = firedAt.Add(24 * time.Hour)
	}
	result := db.WithContext(ctx).Model(&orm.UserSchedule{}).
		Where("id = ? AND next_run_at = ?", s.ID, s.NextRunAt).
		Updates(map[string]any{
			"last_run_at": firedAt,
			"next_run_at": next,
			"run_count":   gorm.Expr("run_count + 1"),
		})
	if result.RowsAffected == 0 {
		// Another instance already fired this schedule; skip to avoid duplicate execution.
		return
	}

	if convID == "" {
		return
	}

	// Build chat request with kb_ids and file_ids from the schedule definition.
	query := renderPromptTemplate(s.PromptTemplate, firedAt)
	reqBody := map[string]any{
		"query":           query,
		"conversation_id": convID,
		"stream":          true,
		"mode":            "auto",
		"input":           []map[string]any{{"input_type": "text", "text": query}},
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
// Plugin and subagent are explicitly enabled so scheduled tasks always run regardless
// of the user's global chat settings.
// Returns the new conversation ID, or "" on failure.
func createTaskConversation(ctx context.Context, db *gorm.DB, userID, promptTemplate string) string {
	displayName := promptTemplate
	if len(displayName) > 80 {
		displayName = displayName[:80] + "..."
	}
	now := time.Now().UTC()
	enablePlugin := true
	pluginMode := "auto"
	enableSubagent := true
	conv := orm.Conversation{
		ID:             "conv_" + common.GenerateID(),
		DisplayName:    displayName,
		ChannelID:      "default",
		IsTaskConv:     true,
		EnablePlugin:   &enablePlugin,
		PluginMode:     &pluginMode,
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

// sendScheduledChatRequest posts the scheduled trigger to Go core and updates TaskCenter status.
func sendScheduledChatRequest(userID, convID, taskID string, db *gorm.DB, reqBody map[string]any) {
	coreURL := common.CoreSelfEndpoint() + "/conversations:chat"
	body, _ := json.Marshal(reqBody)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, coreURL, bytes.NewReader(body))
	if err != nil {
		_ = taskcenter.UpdateTaskStatus(ctx, db, taskID, "failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-User-Id", userID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = taskcenter.UpdateTaskStatus(ctx, db, taskID, "failed")
		return
	}
	defer resp.Body.Close()
	// Drain the response so the upstream can finish writing events to Redis.
	buf := make([]byte, 4096)
	for {
		if _, err := resp.Body.Read(buf); err != nil {
			break
		}
	}
	if resp.StatusCode >= 400 {
		_ = taskcenter.UpdateTaskStatus(ctx, db, taskID, "failed")
	} else {
		_ = taskcenter.UpdateTaskStatus(ctx, db, taskID, "completed")
	}
}

// ── API handlers ──────────────────────────────────────────────────────────────

type scheduleResponse struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	Name           string     `json:"name"`
	Remark         string     `json:"remark"`
	CronExpr       string     `json:"cron_expr"`
	Timezone       string     `json:"timezone"`
	PromptTemplate string     `json:"prompt_template"`
	KbIDs          []string   `json:"kb_ids"`
	FileIDs        []string   `json:"file_ids"`
	Enabled        bool       `json:"enabled"`
	RunCount       int        `json:"run_count"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	NextRunAt      time.Time  `json:"next_run_at"`
	CreatedAt      time.Time  `json:"created_at"`
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
		Enabled:        s.Enabled,
		RunCount:       s.RunCount,
		LastRunAt:      s.LastRunAt,
		NextRunAt:      s.NextRunAt,
		CreatedAt:      s.CreatedAt,
	}
}

// ListSchedulesHandler handles GET /schedules
func ListSchedulesHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	db := store.DB()
	rows, err := ListSchedules(r.Context(), db, userID)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]scheduleResponse, 0, len(rows))
	for _, s := range rows {
		items = append(items, toScheduleResponse(s))
	}
	common.ReplyJSON(w, map[string]any{"items": items, "total": len(items)})
}

// CreateScheduleHandler handles POST /schedules
func CreateScheduleHandler(w http.ResponseWriter, r *http.Request) {
	userID := store.UserID(r)
	var body struct {
		Name           string   `json:"name"`
		Remark         string   `json:"remark"`
		CronExpr       string   `json:"cron_expr"`
		Timezone       string   `json:"timezone"`
		PromptTemplate string   `json:"prompt_template"`
		KbIDs          []string `json:"kb_ids"`
		FileIDs        []string `json:"file_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		common.ReplyErr(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if body.CronExpr == "" || body.PromptTemplate == "" {
		common.ReplyErr(w, "cron_expr and prompt_template are required", http.StatusBadRequest)
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
		Enabled:        true,
	}
	db := store.DB()
	if err := CreateSchedule(r.Context(), db, s); err != nil {
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
