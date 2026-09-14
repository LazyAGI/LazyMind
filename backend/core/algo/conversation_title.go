package algo

import (
	"context"
	"encoding/json"
	"lazymind/core/common"
	"time"
)

const (
	conversationTitlePath  = "/api/conversation/title:generate"
	conversationTitlesPath = "/api/conversation/titles:generate"
)

type ConversationTitle struct {
	Title          string   `json:"title"`
	Summary        string   `json:"initial_intent_summary"`
	IntentStatus   string   `json:"intent_status"`
	MissingContext []string `json:"missing_context"`
}

type ConversationTitleResult struct {
	Status    string            `json:"status"`
	Output    ConversationTitle `json:"output"`
	ErrorCode string            `json:"error_code"`
	Retryable bool              `json:"retryable"`
	Usage     json.RawMessage   `json:"usage"`
}

type ConversationTitleBatchInput struct {
	ID    string          `json:"id"`
	Input json.RawMessage `json:"input"`
}

type ConversationTitleBatchItem struct {
	ID string `json:"id"`
	ConversationTitle
}

type ConversationTitleBatchResult struct {
	Status string `json:"status"`
	Output struct {
		Items []ConversationTitleBatchItem `json:"items"`
	} `json:"output"`
	ErrorCode string          `json:"error_code"`
	Usage     json.RawMessage `json:"usage"`
}

func GenerateConversationTitles(ctx context.Context, inputs []ConversationTitleBatchInput, llmConfig map[string]any, timeoutSeconds int) (ConversationTitleBatchResult, error) {
	request := map[string]any{
		"items":      inputs,
		"llm_config": llmConfig, "options": map[string]any{"timeout_seconds": timeoutSeconds, "max_retries": 1},
	}
	var result ConversationTitleBatchResult
	err := common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), conversationTitlesPath), request, nil, &result, time.Duration(timeoutSeconds+5)*time.Second)
	return result, err
}

func GenerateConversationTitle(ctx context.Context, input json.RawMessage, llmConfig map[string]any, timeoutSeconds int) (ConversationTitleResult, error) {
	request := map[string]any{
		"input":      input,
		"llm_config": llmConfig, "options": map[string]any{"timeout_seconds": timeoutSeconds, "max_retries": 1},
	}
	var result ConversationTitleResult
	err := common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), conversationTitlePath), request, nil, &result, time.Duration(timeoutSeconds+5)*time.Second)
	return result, err
}
