package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
)

const conversationStatusBatchLimit = 100

type conversationRunningStatus struct {
	ConversationID string `json:"conversation_id"`
	Status         string `json:"status"`
}

// BatchConversationStatus is a content-free snapshot, independent of chat resume.
func BatchConversationStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationIDs []string `json:"conversation_ids"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		common.ReplyErr(w, "invalid conversation status request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) || len(body.ConversationIDs) == 0 || len(body.ConversationIDs) > conversationStatusBatchLimit {
		common.ReplyErr(w, "provide between 1 and 100 conversation ids", http.StatusBadRequest)
		return
	}
	ids := make([]string, 0, len(body.ConversationIDs))
	seen := map[string]bool{}
	for _, id := range body.ConversationIDs {
		if id == "" || len(id) > 64 || strings.TrimSpace(id) != id || strings.ContainsAny(id, "\r\n\x00") {
			common.ReplyErr(w, "invalid conversation id", http.StatusBadRequest)
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if store.DB() == nil {
		common.ReplyErr(w, "unable to query conversation status", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	statuses, err := batchConversationRunningStatus(ctx, store.DB(), store.State(), recoveryUserID(r), ids, time.Now())
	if err != nil {
		common.ReplyErr(w, "unable to query conversation status", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeConversationJSON(w, http.StatusOK, map[string]any{"statuses": statuses})
}

func batchConversationRunningStatus(ctx context.Context, db *gorm.DB, cache state.Store, owner string, requested []string, now time.Time) ([]conversationRunningStatus, error) {
	var allowed []string
	if err := db.WithContext(ctx).Model(&orm.Conversation{}).
		Where("id IN ? AND create_user_id = ? AND archived_at IS NULL AND deleted_at IS NULL AND is_ephemeral = ?", requested, owner, false).
		Pluck("id", &allowed).Error; err != nil {
		return nil, err
	}
	result := make([]conversationRunningStatus, 0, len(allowed))
	if len(allowed) == 0 {
		return result, nil
	}
	statuses := make(map[string]string, len(allowed))
	for _, id := range allowed {
		statuses[id] = "idle"
	}
	mark := func(id, status string) {
		if current, ok := statuses[id]; ok && current != "running" && (status == "running" || status == "unknown") {
			statuses[id] = status
		}
	}
	queryRunning := func(query *gorm.DB) {
		var ids []string
		if err := query.Pluck("conversation_id", &ids).Error; err != nil {
			for _, id := range allowed {
				mark(id, "unknown")
			}
			return
		}
		for _, id := range ids {
			mark(id, "running")
		}
	}
	queryRunning(db.WithContext(ctx).Model(&orm.ExternalChatRun{}).
		Where("conversation_id IN ? AND actor_user_id = ? AND status IN ?", allowed, owner, []string{"pending", "running"}))
	queryRunning(db.WithContext(ctx).Model(&orm.SubAgentTask{}).
		Where("conversation_id IN ? AND agent_type <> ? AND status IN ?", allowed, "workflow_step", []string{"pending", "running"}).
		Where("NOT EXISTS (SELECT 1 FROM plugin_session_steps step WHERE step.task_id = sub_agent_tasks.id)"))
	// Attempts are runtime records, unlike unscheduled nodes in the workflow graph.
	// Ignore superseded attempts and orphan adapters whose task has already ended.
	queryRunning(db.WithContext(ctx).Table("plugin_sessions session").Select("session.conversation_id").
		Joins("JOIN plugin_session_steps step ON step.session_id = session.id").
		Where("session.conversation_id IN ? AND session.dismissed = ? AND session.status NOT IN ?", allowed, false, []string{"completed", "stopped", "canceled", "cancelled"}).
		Where("(step.validity = ? OR step.validity = '') AND step.status IN ?", "effective", []string{"pending", "queued", "claimed", "running"}).
		Where("NOT EXISTS (SELECT 1 FROM plugin_session_steps newer WHERE newer.session_id = step.session_id AND newer.step_id = step.step_id AND newer.attempt > step.attempt AND (newer.validity = 'effective' OR newer.validity = ''))").
		Where("NOT EXISTS (SELECT 1 FROM sub_agent_tasks task WHERE task.id = step.task_id AND task.status IN ?)", []string{"succeeded", "failed", "interrupted", "canceled", "cancelled"}))
	// Workflow-backed tasks use the attempt state above. Historical unlinked
	// workflow tasks are matched within the execution's lifetime, as in TaskCenter.
	queryRunning(db.WithContext(ctx).Model(&orm.TaskCenterTask{}).
		Where("conversation_id IN ? AND user_id = ? AND archived_at IS NULL AND finished_at IS NULL", allowed, owner).
		Where("status IN ? AND (scheduled_fire_at IS NULL OR scheduled_fire_at <= ?)", []string{"pending", "running"}, now).
		Where("(plugin_session_id IS NULL OR plugin_session_id = '') AND task_type IN ?", []string{"background_chat", "scheduled"}). // workflow-naming: persistence
		Where("status = 'pending' OR created_at >= ?", now.Add(-2*time.Hour)).
		Where("id NOT IN (?)", taskcenter.HistoricalWorkflowMatches(db.WithContext(ctx)).
			Where("task_center_tasks.conversation_id IN ? AND task_center_tasks.user_id = ?", allowed, owner).
			Select("task_center_tasks.id")))

	type cachedChatRun struct {
		Status     string `json:"status"`
		RunID      string `json:"run_id"`
		LastUpdate int64  `json:"last_update"`
	}
	type cacheResult struct {
		values    map[string]*cachedChatRun
		err       error
		drivers   map[string]string
		driverErr error
	}
	cached := make([]cacheResult, len(allowed))
	var workers sync.WaitGroup
	jobs := make(chan int)
	for worker := 0; worker < min(8, len(allowed)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if cache == nil {
					cached[index].err = errors.New("state unavailable")
					continue
				}
				values, err := cache.HGetAll(ctx, chatStatusKey(allowed[index]))
				cached[index].err = err
				cached[index].values = make(map[string]*cachedChatRun, len(values))
				for id, raw := range values {
					value := &cachedChatRun{}
					if json.Unmarshal([]byte(raw), value) != nil {
						value = nil
					}
					cached[index].values[id] = value
				}
				cached[index].drivers, cached[index].driverErr = cache.HGetAll(ctx, workflow.DriverActivityKey(allowed[index]))
			}
		}()
	}
	for index := range allowed {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	drivingSessions := map[string]string{}
	for index, item := range cached {
		if item.driverErr != nil {
			mark(allowed[index], "unknown")
		}
		for _, raw := range item.drivers {
			var activity workflow.DriverActivity
			if json.Unmarshal([]byte(raw), &activity) != nil {
				mark(allowed[index], "unknown")
			} else if activity.ExpiresAt > now.Unix() {
				drivingSessions[activity.SessionID] = allowed[index]
			}
		}
	}
	if len(drivingSessions) > 0 {
		ids := make([]string, 0, len(drivingSessions))
		for id := range drivingSessions {
			ids = append(ids, id)
		}
		var sessions []orm.WorkflowSession
		if err := db.WithContext(ctx).Select("id, conversation_id").Where("id IN ? AND conversation_id IN ? AND dismissed = ? AND status NOT IN ?", ids, allowed, false, []string{"completed", "stopped", "canceled", "cancelled"}).Find(&sessions).Error; err != nil {
			for _, id := range drivingSessions {
				mark(id, "unknown")
			}
		} else {
			for _, session := range sessions {
				if drivingSessions[session.ID] == session.ConversationID {
					mark(session.ConversationID, "running")
				}
			}
		}
	}

	// Read only run metadata, never messages, logs, or generated content.
	type historyRun struct{ ID, ConversationID, RunID, RunStatus string }
	historyIDs := []string{}
	for _, item := range cached {
		for id := range item.values {
			historyIDs = append(historyIDs, id)
		}
	}
	durable := map[string]map[string]historyRun{}
	for _, model := range []any{&orm.ChatHistory{}, &orm.MultiAnswersChatHistory{}} {
		var rows []historyRun
		if err := db.WithContext(ctx).Model(model).Select("id, conversation_id, run_id, run_status").
			Where("conversation_id IN ? AND (run_status = ? OR id IN ?)", allowed, "generating", historyIDs).Find(&rows).Error; err != nil {
			for _, id := range allowed {
				mark(id, "unknown")
			}
			continue
		}
		for _, row := range rows {
			if durable[row.ConversationID] == nil {
				durable[row.ConversationID] = map[string]historyRun{}
			}
			durable[row.ConversationID][row.ID] = row
		}
	}
	for index, id := range allowed {
		item := cached[index]
		if item.err != nil {
			mark(id, "unknown")
			continue
		}
		runs := durable[id]
		for historyID, value := range item.values {
			if value == nil {
				mark(id, "unknown")
				continue
			}
			if value.Status != "generating" {
				continue
			}
			row, ok := runs[historyID]
			if ok && row.RunID == value.RunID && row.RunStatus != "" && row.RunStatus != "generating" {
				continue
			}
			if value.LastUpdate > 0 && now.Unix()-value.LastUpdate >= int64(chatCacheExpireTime/time.Second) {
				mark(id, "unknown")
			} else {
				mark(id, "running")
			}
		}
		for historyID, row := range runs {
			if row.RunStatus == "generating" {
				if value := item.values[historyID]; value == nil || value.RunID != row.RunID {
					mark(id, "unknown")
				}
			}
		}
	}
	for _, id := range requested {
		if status, ok := statuses[id]; ok {
			result = append(result, conversationRunningStatus{ConversationID: id, Status: status})
		}
	}
	return result, nil
}
