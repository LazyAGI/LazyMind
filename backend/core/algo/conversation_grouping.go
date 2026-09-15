package algo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"lazymind/core/common"
)

const conversationGroupingPath = "/api/conversation/grouping:run"
const conversationGroupingExecutionsPath = "/api/conversation/grouping-executions/"

// ConversationGroupingRequest contains one frozen batch, never a generic LLM task.
type ConversationGroupingRequest struct {
	Input     map[string]any `json:"input"`
	LLMConfig map[string]any `json:"llm_config"`
	Options   map[string]any `json:"options"`
}

type ConversationGroupingResult struct {
	Status    string          `json:"status"`
	Output    json.RawMessage `json:"output"`
	ErrorCode string          `json:"error_code"`
	Retryable bool            `json:"retryable"`
	Usage     json.RawMessage `json:"usage"`
}

type ConversationGroupingProgress struct {
	FirstResponseAt string `json:"first_response_at"`
	LastActivityAt  string `json:"last_activity_at"`
	State           string `json:"state"`
	ReceivedChars   int64  `json:"received_chars"`
	ElapsedSeconds  int64  `json:"elapsed_seconds"`
	IdleSeconds     int64  `json:"idle_seconds"`
}

func RunConversationGrouping(ctx context.Context, request ConversationGroupingRequest) (ConversationGroupingResult, error) {
	var result ConversationGroupingResult
	err := common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(), conversationGroupingPath),
		request, nil, &result, 10*time.Minute)
	return result, err
}

func CancelConversationGrouping(ctx context.Context, executionID string) (bool, error) {
	var result struct {
		Settled bool `json:"settled"`
	}
	err := common.ApiPost(ctx, common.JoinURL(common.ChatServiceEndpoint(),
		conversationGroupingExecutionsPath+executionID+":cancel"), map[string]any{}, nil, &result, 10*time.Second)
	return result.Settled, err
}

// StreamConversationGrouping owns transport only. Core's caller owns checkpoints and settlement.
func StreamConversationGrouping(ctx context.Context, executionID string, request ConversationGroupingRequest,
	progress func(ConversationGroupingProgress) error) (ConversationGroupingResult, error) {
	var result ConversationGroupingResult
	payload, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodPost,
		common.JoinURL(common.ChatServiceEndpoint(), conversationGroupingExecutionsPath+executionID+":stream"),
		bytes.NewReader(payload))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Progress heartbeats prove transport liveness; model deadlines belong to Algorithm.
	watchdog := time.AfterFunc(30*time.Second, cancel)
	defer watchdog.Stop()
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("conversation grouping stream returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(response.Body)
	for {
		var event struct {
			ConversationGroupingProgress
			Type   string                     `json:"type"`
			Result ConversationGroupingResult `json:"result"`
		}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return result, err
		}
		watchdog.Reset(30 * time.Second)
		switch event.Type {
		case "result":
			return event.Result, nil
		case "progress":
			if progress != nil {
				if err := progress(event.ConversationGroupingProgress); err != nil {
					return result, err
				}
			}
		}
	}
}
