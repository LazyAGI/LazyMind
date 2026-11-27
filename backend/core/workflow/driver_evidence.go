package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
)

// Read only deterministic checks declared by the pinned revision, from the
// current effective attempt. A previous attempt's success is not current evidence.
func buildWorkflowPostStepChecks(ctx context.Context, db *gorm.DB, session *orm.WorkflowSession, stepID string) (string, error) {
	graph, err := loadSessionGraph(ctx, db, session)
	if err != nil {
		return "", err
	}
	names := map[string]string{}
	for _, check := range graph.Runtime.PostStepChecks {
		if check.StepID == stepID {
			names[fmt.Sprintf("post-step-check-%d", len(names)+1)] = check.Tool
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	step, err := GetLatestStep(ctx, db, session.ID, stepID)
	if err != nil {
		return "", err
	}
	if step == nil || step.Validity != "effective" {
		return "", nil
	}
	var events []orm.SubAgentStep
	if err := db.WithContext(ctx).Where("task_id = ? AND role = ?", step.TaskID, "tool").Order("seq").Find(&events).Error; err != nil {
		return "", err
	}
	results := map[string]json.RawMessage{}
	for _, event := range events {
		var content struct {
			Results []struct {
				ID     string          `json:"tool_call_id"`
				Name   string          `json:"name"`
				Result json.RawMessage `json:"result"`
			} `json:"tool_results"`
		}
		if err := json.Unmarshal(event.Content, &content); err != nil {
			return "", fmt.Errorf("decode post-step check evidence: %w", err)
		}
		for _, result := range content.Results {
			if name, ok := names[result.ID]; ok && name == result.Name {
				results[result.ID] = result.Result
			}
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\nDeterministic post-step checks for current attempt %d (runtime evidence):\n", step.Attempt)
	for i := 1; i <= len(names); i++ {
		id := fmt.Sprintf("post-step-check-%d", i)
		if result, ok := results[id]; ok {
			fmt.Fprintf(&out, "- %s: %s\n", names[id], result)
		} else {
			fmt.Fprintf(&out, "- %s: no recorded result for this attempt\n", names[id])
		}
	}
	return out.String(), nil
}
