package algo

import (
	"context"
	"encoding/json"
	"lazymind/core/common"
	"time"
)

type OpeningDescription struct {
	Title          string   `json:"title"`
	Summary        string   `json:"initial_intent_summary"`
	IntentStatus   string   `json:"intent_status"`
	MissingContext []string `json:"missing_context"`
}

type OpeningTaskResult struct {
	Status    string             `json:"status"`
	Output    OpeningDescription `json:"output"`
	ErrorCode string             `json:"error_code"`
	Retryable bool               `json:"retryable"`
	Usage     json.RawMessage    `json:"usage"`
}

func DescribeConversationOpening(ctx context.Context, input json.RawMessage, llmConfig map[string]any, timeoutSeconds int) (OpeningTaskResult, error) {
	request := map[string]any{
		"mode": "llm", "task_type": "conversation.describe_opening", "input": input,
		"llm_config": llmConfig, "options": map[string]any{"timeout_seconds": timeoutSeconds, "max_retries": 1},
	}
	var result OpeningTaskResult
	err := common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), llmTaskRunPath), request, nil, &result, time.Duration(timeoutSeconds+5)*time.Second)
	return result, err
}
