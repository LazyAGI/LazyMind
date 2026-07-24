package algo

import (
	"context"
	"encoding/json"
	"time"

	"lazymind/core/common"
)

const channelIntentPath = "/api/chat/channel-intent-classify"
const channelIntentTimeout = 65 * time.Second

// ChannelCommandDescription describes one opaque command to the classifier.
// Core does not interpret command names or parameter schemas.
type ChannelCommandDescription struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ChannelCommandRegistry is supplied by the channel gateway. OutputSchema is the
// complete JSON Schema for the returned ChannelCommandEnvelope.
type ChannelCommandRegistry struct {
	SchemaVersion  string                      `json:"schema_version"`
	Commands       []ChannelCommandDescription `json:"commands"`
	SelectionRules []string                    `json:"selection_rules"`
	OutputSchema   json.RawMessage             `json:"output_schema"`
}

type ChannelIntentRequest struct {
	Provider        string                 `json:"provider"`
	Message         string                 `json:"message"`
	State           json.RawMessage        `json:"state"`
	CommandRegistry ChannelCommandRegistry `json:"command_registry"`
	LLMConfig       map[string]any         `json:"llm_config"`
}

// ChannelCommandEnvelope remains opaque to Core after structural decoding.
type ChannelCommandEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	Command       string          `json:"command"`
	Parameters    json.RawMessage `json:"parameters"`
}

func ClassifyChannelIntent(
	ctx context.Context,
	req ChannelIntentRequest,
) (ChannelCommandEnvelope, error) {
	var response ChannelCommandEnvelope
	err := common.ApiPost(
		ctx,
		common.ChatServiceEndpoint()+channelIntentPath,
		req,
		nil,
		&response,
		channelIntentTimeout,
	)
	return response, err
}
