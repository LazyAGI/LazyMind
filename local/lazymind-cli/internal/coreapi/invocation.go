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
}

type InvocationStart struct {
	ClientName          string          `json:"client_name"`
	ClientVersion       string          `json:"client_version,omitempty"`
	ConnectorName       string          `json:"connector_name"`
	ConnectorVersion    string          `json:"connector_version,omitempty"`
	ConnectorInstanceID string          `json:"connector_instance_id"`
	ProtocolVersion     string          `json:"protocol_version,omitempty"`
	Transport           string          `json:"transport"`
	ToolName            string          `json:"tool_name"`
	ReadOnly            bool            `json:"read_only"`
	RequestHash         string          `json:"request_hash"`
	RequestSummary      json.RawMessage `json:"request_summary,omitempty"`
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

func (c *Client) StartInvocation(ctx context.Context, id string, input InvocationStart) error {
	return c.DoJSON(ctx, http.MethodPost, "/agent-invocations/"+url.PathEscape(id)+":start", input, nil)
}

func (c *Client) FinishInvocation(ctx context.Context, id string, input InvocationFinish) error {
	return c.DoJSON(ctx, http.MethodPost, "/agent-invocations/"+url.PathEscape(id)+":finish", input, nil)
}
