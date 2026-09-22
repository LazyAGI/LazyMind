package userprefs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/chat"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/readonlyorm"
	"lazymind/core/mcp"
	"lazymind/core/scheduler"
	"lazymind/core/settingsactivity"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

type SettingsChangeRequest struct {
	Key              string   `json:"key"`
	Enabled          *bool    `json:"enabled" required:"true"`
	ConfirmedTaskIDs []string `json:"confirmed_task_ids,omitempty"`
}

type SettingsAffectedTask struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	ConversationID string `json:"conversation_id,omitempty"`
}

type SettingsChangeImpact struct {
	Key     string                 `json:"key"`
	Enabled bool                   `json:"enabled"`
	Tasks   []SettingsAffectedTask `json:"tasks" required:"true"`
}

type SettingsChangeResult struct {
	Applied     bool                                 `json:"applied"`
	Impact      SettingsChangeImpact                 `json:"impact"`
	Preferences *uiPreferencesResponse               `json:"preferences,omitempty"`
	MCP         *mcp.BulkUpdateServerEnabledResponse `json:"mcp,omitempty"`
}

func changePatch(key string, enabled *bool) (uiPreferencesPatchRequest, bool) {
	req := uiPreferencesPatchRequest{}
	switch key {
	case "developer_mode_active":
		req.DeveloperModeActive = enabled
	case "task_center_enabled":
		req.TaskCenterEnabled = enabled
	case "schedules_enabled":
		req.SchedulesEnabled = enabled
	case "skills_enabled":
		req.SkillsEnabled = enabled
	case "workflows_enabled":
		req.WorkflowsEnabled = enabled
	case "mcp_enabled":
		req.MCPEnabled = enabled
	case "document_parsing_enabled":
		req.DocumentParsingEnabled = enabled
	default:
		return req, false
	}
	return req, true
}

func readSettingsChange(w http.ResponseWriter, r *http.Request) (SettingsChangeRequest, string, bool) {
	var req SettingsChangeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	_, valid := changePatch(req.Key, req.Enabled)
	if err != nil || !valid || req.Enabled == nil || len(req.ConfirmedTaskIDs) > 2000 || decoder.Decode(new(any)) != io.EOF {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return req, "", false
	}
	user := strings.TrimSpace(store.UserID(r))
	if user == "" {
		common.ReplyErr(w, "forbidden", http.StatusForbidden)
		return req, "", false
	}
	if store.DB() == nil {
		common.ReplyErr(w, "unable to query conversation status", http.StatusServiceUnavailable)
		return req, "", false
	}
	return req, user, true
}

func CheckSettingsChange(w http.ResponseWriter, r *http.Request) {
	req, user, ok := readSettingsChange(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	impact, err := settingsImpact(ctx, store.DB(), store.LazyLLMDB(), store.State(), user, req.Key, *req.Enabled)
	if err != nil {
		common.ReplyErr(w, "unable to query conversation status", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, impact)
}

func ApplySettingsChange(w http.ResponseWriter, r *http.Request) {
	req, user, ok := readSettingsChange(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	result, err := applySettingsChange(ctx, store.DB(), store.LazyLLMDB(), store.State(), user, req)
	if err != nil {
		common.ReplyErr(w, "update user ui preferences failed", http.StatusServiceUnavailable)
		return
	}
	common.ReplyOK(w, result)
}

func applySettingsChange(ctx context.Context, db, lazyDB *gorm.DB, cache state.Store, user string, req SettingsChangeRequest) (SettingsChangeResult, error) {
	result := SettingsChangeResult{}
	err := settingsactivity.WithLock(ctx, cache, user, func() error {
		impact, err := settingsImpact(ctx, db, lazyDB, cache, user, req.Key, *req.Enabled)
		if err != nil {
			return err
		}
		result.Impact = impact
		confirmed := map[string]bool{}
		for _, id := range req.ConfirmedTaskIDs {
			confirmed[id] = true
		}
		for _, task := range impact.Tasks {
			if !confirmed[task.ID] {
				return nil
			}
		}
		// Resolve derived data before committing, so a later read failure cannot turn
		// a successfully persisted setting into an apparent failed save.
		configured, err := LoadUserPreferenceConfigured(ctx, db, user)
		if err != nil {
			return err
		}
		var row orm.UserUIPreferences
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if req.Key == "mcp_enabled" {
				result.MCP, err = mcp.SetOwnedServersEnabled(ctx, tx, user, *req.Enabled)
				if err != nil {
					return err
				}
				row, err = LoadUserUIPreferences(ctx, tx, user)
				return err
			}
			patch, _ := changePatch(req.Key, req.Enabled)
			row, err = UpsertUserUIPreferences(ctx, tx, user, patch)
			if err != nil {
				return err
			}
			if patch.SkillsEnabled != nil {
				return setAllSkillsEnabled(ctx, tx, user, *patch.SkillsEnabled)
			}
			if patch.WorkflowsEnabled != nil {
				return setAllWorkflowsEnabled(ctx, tx, user, *patch.WorkflowsEnabled)
			}
			if patch.SchedulesEnabled != nil && *patch.SchedulesEnabled {
				return scheduler.RecomputeEnabledSchedules(ctx, tx, user, time.Now().UTC())
			}
			return nil
		})
		if err != nil {
			return err
		}
		preferences := buildUIPreferencesResponse(row, configured)
		result.Preferences = &preferences
		result.Applied = true
		return nil
	})
	return result, err
}

func settingsImpact(ctx context.Context, db, lazyDB *gorm.DB, cache state.Store, user, key string, enabled bool) (SettingsChangeImpact, error) {
	result := SettingsChangeImpact{Key: key, Enabled: enabled, Tasks: []SettingsAffectedTask{}}
	if enabled {
		return result, nil
	}
	seen := map[string]bool{}
	add := func(id, title, status, conversation string) {
		if seen[id] {
			return
		}
		seen[id] = true
		result.Tasks = append(result.Tasks, SettingsAffectedTask{id, title, status, conversation})
	}
	switch key {
	case "developer_mode_active":
		rows, err := chat.RunningSettingsConversations(ctx, db, cache, user)
		if err != nil {
			return result, err
		}
		for _, row := range rows {
			add("chat:"+row.ID, row.DisplayName, "running", row.ID)
		}
	case "task_center_enabled":
		var rows []orm.SubAgentTask
		err := db.WithContext(ctx).Where("create_user_id = ? AND agent_type <> ? AND status IN ?", user, "workflow_step", []string{"pending", "running"}).Where("NOT EXISTS (SELECT 1 FROM plugin_session_steps step WHERE step.task_id = sub_agent_tasks.id)").Find(&rows).Error
		if err != nil {
			return result, err
		}
		for _, row := range rows {
			add("subtask:"+row.ID, row.Title, row.Status, row.ConversationID)
		}
	case "schedules_enabled":
		var rows []orm.TaskCenterTask
		err := db.WithContext(ctx).Where("user_id = ? AND task_type = ? AND status IN ? AND finished_at IS NULL AND (scheduled_fire_at IS NULL OR scheduled_fire_at <= ?)", user, "scheduled", []string{"pending", "running"}, time.Now()).Find(&rows).Error
		if err != nil {
			return result, err
		}
		for _, row := range rows {
			title := ""
			if row.Title != nil {
				title = *row.Title
			}
			add("schedule:"+row.ID, title, row.Status, row.ConversationID)
		}
	case "workflows_enabled":
		var sessions []orm.WorkflowSession
		err := db.WithContext(ctx).Where("create_user_id = ? AND dismissed = ? AND status NOT IN ?", user, false, []string{"completed", "stopped", "canceled", "cancelled"}).Find(&sessions).Error
		if err != nil {
			return result, err
		}
		for _, session := range sessions {
			var count int64
			err = db.WithContext(ctx).Table("plugin_session_steps step").Where("step.session_id = ? AND (step.validity = ? OR step.validity = '') AND step.status IN ?", session.ID, "effective", []string{"pending", "queued", "claimed", "running"}).
				Where("NOT EXISTS (SELECT 1 FROM plugin_session_steps newer WHERE newer.session_id = step.session_id AND newer.step_id = step.step_id AND newer.attempt > step.attempt AND (newer.validity = 'effective' OR newer.validity = ''))").
				Where("NOT EXISTS (SELECT 1 FROM sub_agent_tasks task WHERE task.id = step.task_id AND task.status IN ?)", []string{"succeeded", "failed", "interrupted", "canceled", "cancelled"}).Count(&count).Error
			if err != nil {
				return result, err
			}
			if cache == nil {
				return result, errors.New("workflow state unavailable")
			}
			drivers, err := cache.HGetAll(ctx, workflow.DriverActivityKey(session.ConversationID))
			if err != nil {
				return result, err
			}
			for _, raw := range drivers {
				var driver workflow.DriverActivity
				if json.Unmarshal([]byte(raw), &driver) != nil {
					return result, errors.New("invalid workflow state")
				}
				if driver.SessionID == session.ID && driver.ExpiresAt > time.Now().Unix() {
					count++
				}
			}
			if count > 0 {
				add("workflow:"+session.ID, session.WorkflowRef, "running", session.ConversationID)
			}
		}
	case "skills_enabled", "mcp_enabled":
		activities, err := settingsactivity.Read(ctx, cache, user)
		if err != nil {
			return result, err
		}
		for _, activity := range activities {
			if activity.Capability != key {
				continue
			}
			var conversation orm.Conversation
			err := db.WithContext(ctx).Select("id, display_name").Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", activity.ConversationID, user).Take(&conversation).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return result, err
			}
			if activity.UpdatedAt < time.Now().Add(-60*time.Second).Unix() {
				running, err := chat.RunningSettingsConversations(ctx, db, cache, user)
				if err != nil {
					return result, err
				}
				live := false
				for _, row := range running {
					if row.ID == activity.ConversationID {
						live = true
					}
				}
				if live {
					return result, errors.New("activity check unavailable")
				}
				continue
			}
			runID := activity.RunID
			if runID == "" {
				runID = activity.ID
			}
			add("activity:"+conversation.ID+":"+runID, conversation.DisplayName, "running", conversation.ID)
		}
	case "document_parsing_enabled":
		var tasks []orm.Task
		if err := db.WithContext(ctx).Where("create_user_id = ? AND deleted_at IS NULL AND task_type IN ?", user, []string{"TASK_TYPE_PARSE", "TASK_TYPE_REPARSE", "TASK_TYPE_PARSE_UPLOADED"}).Find(&tasks).Error; err != nil {
			return result, err
		}
		ids := []string{}
		for _, task := range tasks {
			if task.LazyllmTaskID != "" {
				ids = append(ids, task.LazyllmTaskID)
			}
		}
		states := map[string]string{}
		if len(ids) > 0 {
			if lazyDB == nil {
				return result, errors.New("parsing state unavailable")
			}
			var rows []readonlyorm.LazyLLMDocServiceTaskRow
			if err := lazyDB.WithContext(ctx).Where("task_id IN ?", ids).Find(&rows).Error; err != nil {
				return result, err
			}
			for _, row := range rows {
				states[row.TaskID] = row.Status
			}
		}
		for _, task := range tasks {
			status := states[task.LazyllmTaskID]
			if task.LazyllmTaskID != "" && status == "" {
				return result, errors.New("parsing state unavailable")
			}
			if task.LazyllmTaskID == "" {
				var ext struct {
					State string `json:"task_state"`
				}
				if len(task.Ext) > 0 && json.Unmarshal(task.Ext, &ext) != nil {
					return result, errors.New("invalid parsing state")
				}
				status = ext.State
			}
			switch strings.ToUpper(status) {
			case "RUNNING", "STARTED", "SUBMITTED", "PROCESSING", "WAITING", "WORKING":
				add("parsing:"+task.ID, task.DisplayName, "running", "")
			}
		}
	default:
		return result, errors.New("invalid setting")
	}
	sort.Slice(result.Tasks, func(i, j int) bool { return result.Tasks[i].ID < result.Tasks[j].ID })
	return result, nil
}
