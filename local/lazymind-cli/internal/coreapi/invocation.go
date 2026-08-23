package coreapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type invocationContextKey struct{}

type InvocationMetadata struct {
	ID                  string
	ClientName          string
	ConnectorInstanceID string
	ConversationID      string
	ExternalRef         string
}

type InvocationSource struct {
	Provider     string `json:"provider"`
	HostID       string `json:"host_id"`
	ThreadID     string `json:"thread_id"`
	TurnID       string `json:"turn_id,omitempty"`
	ThreadSource string `json:"thread_source,omitempty"`
	ProjectKey   string `json:"project_key,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	Message      string `json:"message,omitempty"`
}

type InvocationSourceLink struct {
	ConversationID string `json:"conversation_id"`
	ExternalRef    string `json:"external_ref"`
	HistoryID      string `json:"history_id"`
}

type InvocationStart struct {
	ClientName          string            `json:"client_name"`
	ClientVersion       string            `json:"client_version,omitempty"`
	ConnectorName       string            `json:"connector_name"`
	ConnectorVersion    string            `json:"connector_version,omitempty"`
	ConnectorInstanceID string            `json:"connector_instance_id"`
	ProtocolVersion     string            `json:"protocol_version,omitempty"`
	Transport           string            `json:"transport"`
	ToolName            string            `json:"tool_name"`
	ReadOnly            bool              `json:"read_only"`
	RequestHash         string            `json:"request_hash"`
	RequestSummary      json.RawMessage   `json:"request_summary,omitempty"`
	Source              *InvocationSource `json:"source,omitempty"`
}

type InvocationStartResult struct {
	Created bool                  `json:"created"`
	Source  *InvocationSourceLink `json:"source,omitempty"`
}

type InvocationFinish struct {
	Status        string          `json:"status"`
	ResultSummary json.RawMessage `json:"result_summary,omitempty"`
	ErrorCode     string          `json:"error_code,omitempty"`
	Retryable     bool            `json:"retryable,omitempty"`
	WorkflowID    string          `json:"workflow_id,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	StepID        string          `json:"step_id,omitempty"`
	AttemptID     string          `json:"attempt_id,omitempty"`
	ResourceID    string          `json:"resource_id,omitempty"`
	ArtifactID    string          `json:"artifact_id,omitempty"`
	CommandID     string          `json:"command_id,omitempty"`
	ExternalRef   string          `json:"external_ref,omitempty"`
}

func WithInvocation(ctx context.Context, metadata InvocationMetadata) context.Context {
	return context.WithValue(ctx, invocationContextKey{}, metadata)
}

func InvocationFromContext(ctx context.Context) (InvocationMetadata, bool) {
	metadata, ok := ctx.Value(invocationContextKey{}).(InvocationMetadata)
	return metadata, ok && strings.TrimSpace(metadata.ID) != ""
}

func (c *Client) StartInvocation(ctx context.Context, id string, input InvocationStart) (InvocationStartResult, error) {
	var result InvocationStartResult
	err := c.DoJSON(ctx, http.MethodPost, "/agent-invocations/"+url.PathEscape(id)+":start", input, &result)
	return result, err
}

func (c *Client) FinishInvocation(ctx context.Context, id string, input InvocationFinish) error {
	return c.DoJSON(ctx, http.MethodPost, "/agent-invocations/"+url.PathEscape(id)+":finish", input, nil)
}
