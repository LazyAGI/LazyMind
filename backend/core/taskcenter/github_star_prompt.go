package taskcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/maintenance"
	"lazymind/core/store"
)

type GitHubStarPromptConfig struct {
	MinSuccessfulTasks int
	Cooldown           time.Duration
	MaxPrompts         int
}

type GitHubStarPromptDecision struct {
	Show               bool   `json:"show"`
	Reason             string `json:"reason"`
	TaskID             string `json:"task_id"`
	MinSuccessfulTasks int    `json:"min_successful_tasks"`
	CooldownDays       int    `json:"cooldown_days"`
	MaxPrompts         int    `json:"max_prompts"`
}

type GitHubStarPromptService struct {
	DB     *gorm.DB
	Now    func() time.Time
	Config GitHubStarPromptConfig
}

func GitHubStarPromptConfigFromEnv() (GitHubStarPromptConfig, error) {
	values := []int{3, 7, 3}
	for i, key := range []string{"LAZYMIND_GITHUB_STAR_MIN_SUCCESSFUL_TASKS", "LAZYMIND_GITHUB_STAR_COOLDOWN_DAYS", "LAZYMIND_GITHUB_STAR_MAX_PROMPTS"} {
		if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				return GitHubStarPromptConfig{}, fmt.Errorf("invalid %s", key)
			}
			values[i] = value
		}
	}
	if int64(values[1]) > math.MaxInt64/int64(24*time.Hour) {
		return GitHubStarPromptConfig{}, fmt.Errorf("invalid %s", "LAZYMIND_GITHUB_STAR_COOLDOWN_DAYS")
	}
	return GitHubStarPromptConfig{MinSuccessfulTasks: values[0], Cooldown: time.Duration(values[1]) * 24 * time.Hour, MaxPrompts: values[2]}, nil
}

func githubStarRoot(ctx context.Context, db *gorm.DB, task orm.TaskCenterTask) (string, bool, error) {
	if task.ArchivedAt != nil || (task.TaskType != "workflow_run" && task.TaskType != "background_chat" && task.TaskType != "scheduled") {
		return "", false, nil
	}
	var conversation orm.Conversation
	if err := db.WithContext(ctx).Where("id=? AND create_user_id=? AND deleted_at IS NULL AND archived_at IS NULL", task.ConversationID, task.UserID).Take(&conversation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	resolved, err := resolveTaskForResponse(ctx, db, task)
	if err != nil || resolved.Status != "succeeded" {
		return "", false, err
	}
	key := "task:" + task.ID
	if resolved.WorkflowSessionID != nil && *resolved.WorkflowSessionID != "" {
		key = "workflow:" + *resolved.WorkflowSessionID
	}
	var output orm.TaskRunOutput
	err = db.WithContext(ctx).Where("task_id=? AND output_status=?", task.ID, "ready").Take(&output).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, err
	}
	if err == nil {
		var artifacts []json.RawMessage
		_ = json.Unmarshal(output.ArtifactManifestJSON, &artifacts)
		if strings.TrimSpace(output.FinalAnswerText) != "" || len(artifacts) > 0 {
			return key, true, nil
		}
	}
	if resolved.WorkflowSessionID != nil && *resolved.WorkflowSessionID != "" {
		var revisions []orm.WorkflowSlotRevision
		if err := db.WithContext(ctx).Where("session_id=? AND selected=true AND validity <> ?", *resolved.WorkflowSessionID, "stale").Find(&revisions).Error; err != nil {
			return "", false, err
		}
		for _, revision := range revisions {
			content := strings.TrimSpace(string(revision.ContentSnapshot))
			if revision.ArtifactSeq != nil || revision.HumanArtifactID != nil || (content != "" && content != "null" && content != "{}" && content != "[]") {
				return key, true, nil
			}
		}
	}
	// Plain background Work stores its result in the exact bound history,
	// without the scheduled-task output snapshot or a Workflow revision.
	if task.TaskType == "background_chat" && (resolved.WorkflowSessionID == nil || *resolved.WorkflowSessionID == "") {
		execution, scoped := readBackgroundExecution(task.ProgressJSON)
		status, _ := backgroundAggregate(execution)
		if !scoped || status != "succeeded" {
			return "", false, nil
		}
		var historyModel any = &orm.ChatHistory{}
		if len(execution.Parts) == 2 {
			historyModel = &orm.MultiAnswersChatHistory{}
		}
		hasResult := false
		for _, part := range execution.Parts {
			if part.CompletedAt.Before(task.CreatedAt) {
				return "", false, nil
			}
			var history struct{ Result, RunStatus string }
			err := db.WithContext(ctx).Model(historyModel).Select("result", "run_status").
				Where("id=? AND conversation_id=? AND run_id=?", part.HistoryID, task.ConversationID, part.RunID).Take(&history).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", false, nil
			}
			if err != nil {
				return "", false, err
			}
			if ordinaryTerminalStatus(history.RunStatus) != "succeeded" {
				return "", false, nil
			}
			if TaskOutputBody(history.Result) != "" {
				hasResult = true
			}
			if !hasResult {
				var artifact orm.ConversationArtifact
				err := db.WithContext(ctx).Select("id").Where("conversation_id=? AND history_id=? AND create_user_id=?", task.ConversationID, part.HistoryID, task.UserID).
					Where("created_at >= ? AND created_at <= ?", task.CreatedAt, *part.CompletedAt).Take(&artifact).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return "", false, err
				}
				hasResult = err == nil
			}
		}
		if hasResult {
			return key, true, nil
		}
	}
	return "", false, nil
}

func (s GitHubStarPromptService) Claim(ctx context.Context, userID, taskID string) (GitHubStarPromptDecision, error) {
	decision := GitHubStarPromptDecision{TaskID: taskID, Reason: "ineligible", MinSuccessfulTasks: s.Config.MinSuccessfulTasks,
		CooldownDays: int(s.Config.Cooldown / (24 * time.Hour)), MaxPrompts: s.Config.MaxPrompts}
	if userID == "" || s.Config.MinSuccessfulTasks < 1 || s.Config.Cooldown <= 0 || s.Config.MaxPrompts < 1 {
		return decision, errors.New("invalid GitHub Star invitation configuration")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	err := maintenance.UserTransaction(ctx, s.DB, userID, func(tx *gorm.DB) error {
		var task orm.TaskCenterTask
		if err := tx.Where("id=? AND user_id=?", taskID, userID).Take(&task).Error; err != nil {
			return err
		}
		key, ready, err := githubStarRoot(ctx, tx, task)
		if err != nil || !ready {
			return err
		}
		row := orm.UserGitHubStarPrompt{UserID: userID, PromptedTaskIDs: "[]", CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id=?", userID).Take(&row).Error; err != nil {
			return err
		}
		var shown []string
		if err := json.Unmarshal([]byte(row.PromptedTaskIDs), &shown); err != nil {
			return err
		}
		if row.Supported || len(shown) >= s.Config.MaxPrompts {
			return nil
		}
		for _, id := range shown {
			if id == key {
				return nil
			}
		}
		if row.LastPromptAt != nil && now.Sub(*row.LastPromptAt) < s.Config.Cooldown {
			decision.Reason = "cooldown"
			return nil
		}
		var tasks []orm.TaskCenterTask
		if err := tx.Where("user_id=? AND archived_at IS NULL AND task_type IN ?", userID, []string{"workflow_run", "background_chat", "scheduled"}).Order("created_at DESC").Find(&tasks).Error; err != nil {
			return err
		}
		completed := map[string]bool{}
		for _, candidate := range tasks {
			root, ok, err := githubStarRoot(ctx, tx, candidate)
			if err != nil {
				return err
			}
			if ok {
				completed[root] = true
			}
			if len(completed) >= s.Config.MinSuccessfulTasks {
				break
			}
		}
		if len(completed) < s.Config.MinSuccessfulTasks {
			return nil
		}
		encoded, err := json.Marshal(append(shown, key))
		if err != nil {
			return err
		}
		if err := tx.Model(&row).Updates(map[string]any{"prompted_task_ids": string(encoded), "last_prompt_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		decision.Show, decision.Reason = true, "eligible"
		return nil
	})
	return decision, err
}

func (s GitHubStarPromptService) MarkSupported(ctx context.Context, userID string) error {
	if userID == "" {
		return errors.New("missing user id")
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	return maintenance.UserTransaction(ctx, s.DB, userID, func(tx *gorm.DB) error {
		row := orm.UserGitHubStarPrompt{UserID: userID, PromptedTaskIDs: "[]", Supported: true, CreatedAt: now, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.Assignments(map[string]any{"supported": true, "updated_at": now})}).Create(&row).Error
	})
}

func decodeGitHubStarBody(w http.ResponseWriter, r *http.Request, body any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return false
	}
	return true
}

func ClaimGitHubStarPrompt(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		TaskID string `json:"task_id"`
	}
	if !decodeGitHubStarBody(w, r, &body) {
		return
	}
	body.TaskID = strings.TrimSpace(body.TaskID)
	if body.TaskID == "" || len(body.TaskID) > 64 {
		common.ReplyErr(w, "invalid task_id", http.StatusBadRequest)
		return
	}
	config, err := GitHubStarPromptConfigFromEnv()
	if err != nil {
		common.ReplyErr(w, "GitHub Star invitation configuration is unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := (GitHubStarPromptService{DB: store.DB(), Config: config}).Claim(r.Context(), owner, body.TaskID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "task not found", http.StatusNotFound)
		return
	}
	if err != nil {
		common.ReplyErr(w, "GitHub Star invitation is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.ReplyJSON(w, result)
}

func SupportGitHubStarPrompt(w http.ResponseWriter, r *http.Request) {
	owner := store.UserID(r)
	if owner == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		Supported *bool `json:"supported"`
	}
	if !decodeGitHubStarBody(w, r, &body) {
		return
	}
	if body.Supported == nil || !*body.Supported {
		common.ReplyAppErr(w, common.NewAppError(http.StatusBadRequest, common.ErrCodeInvalidParams, "supported must be true"))
		return
	}
	if err := (GitHubStarPromptService{DB: store.DB()}).MarkSupported(r.Context(), owner); err != nil {
		common.ReplyErr(w, "GitHub Star preference is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	common.ReplyOK(w, map[string]bool{"supported": true})
}
