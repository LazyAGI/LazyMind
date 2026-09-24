package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// Only scope v2 is a public outline of this subtask. It carries no execution
// status and must never be promoted into process_steps.
func publicPlanSteps(raw json.RawMessage) []string {
	var plan struct {
		ScopeVersion int      `json:"scope_version"`
		Steps        []string `json:"steps"`
	}
	if json.Unmarshal(raw, &plan) != nil || plan.ScopeVersion != 2 || len(plan.Steps) < 3 || len(plan.Steps) > 5 {
		return nil
	}
	for i, step := range plan.Steps {
		step = strings.TrimSpace(step)
		if step == "" || utf8.RuneCountInString(step) > 100 {
			return nil
		}
		plan.Steps[i] = step
	}
	return plan.Steps
}

func ordinaryPlan(ctx context.Context, db *gorm.DB, task *orm.SubAgentTask) ([]string, error) {
	query := db.WithContext(ctx).Where("task_id = ? AND role = ?", task.ID, "plan")
	if task.ExecutionID != "" && task.StartedAt != nil {
		// Before plan events were scoped, AppendRemoteStep stored no execution ID.
		// Recover only records inside the current execution, never earlier retries.
		query = query.Where("execution_id = ? OR (execution_id = '' AND created_at >= ?)", task.ExecutionID, task.StartedAt)
	} else {
		query = query.Where("execution_id = ?", task.ExecutionID)
	}
	var rows []orm.SubAgentStep
	if err := query.Select("content").Order("seq DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		if plan := publicPlanSteps(row.Content); len(plan) > 0 {
			return plan, nil
		}
	}
	return nil, nil
}
