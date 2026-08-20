package workflowmcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"lazymind/agentconnector/internal/coreapi"
)

const contractVersion = "workflow.v1"

type StartOrigin struct {
	ConversationID string
	ExternalRef    string
}

type Client struct {
	api    *coreapi.Client
	origin StartOrigin
}

type Projection struct {
	SessionID    string `json:"session_id"`
	StateVersion int64  `json:"state_version"`
	Projection   struct {
		Past       []string         `json:"past"`
		Current    []string         `json:"current"`
		Reachable  []string         `json:"reachable"`
		Ready      []string         `json:"ready"`
		Retryable  []string         `json:"retryable"`
		Rewindable []string         `json:"rewindable"`
		Blocked    []string         `json:"blocked"`
		Stale      []string         `json:"stale"`
		Pruned     []string         `json:"pruned"`
		Bypassed   []string         `json:"bypassed"`
		EndReached bool             `json:"end_reached"`
		Completed  bool             `json:"completed"`
		Nodes      map[string]any   `json:"nodes,omitempty"`
		Edges      []map[string]any `json:"edges,omitempty"`
	} `json:"projection"`
	Graph          map[string]any `json:"graph,omitempty"`
	AttemptHistory map[string]any `json:"attempt_history,omitempty"`
	InputWitnesses map[string]any `json:"input_witnesses,omitempty"`
}

type StartInput struct {
	WorkflowID     string         `json:"workflow_id" jsonschema:"required,LazyMind Workflow identifier"`
	RequestContext string         `json:"request_context,omitempty" jsonschema:"The user's task or desired outcome"`
	InputBindings  map[string]any `json:"input_bindings,omitempty" jsonschema:"Prepared LazyMind input-resource bindings keyed by material ID"`
	IdempotencyKey string         `json:"idempotency_key,omitempty" jsonschema:"Stable retry key; generated when omitted"`
	SessionID      string         `json:"session_id,omitempty" jsonschema:"Optional stable session identifier"`
}

type InputResource struct {
	ResourceID    string `json:"resource_id"`
	Name          string `json:"name"`
	MIMEType      string `json:"mime_type"`
	Size          int64  `json:"size"`
	ContentHash   string `json:"content_hash"`
	Revision      int64  `json:"revision"`
	ContentBase64 string `json:"content_base64,omitempty"`
}

type InputImportResult struct {
	Resource InputResource  `json:"resource"`
	Binding  map[string]any `json:"binding"`
}

type StartResult struct {
	PreparationID string     `json:"preparation_id"`
	SessionID     string     `json:"session_id"`
	State         Projection `json:"state"`
}

type SessionSummary struct {
	SessionID          string `json:"session_id"`
	WorkflowID         string `json:"workflow_id"`
	WorkflowRef        string `json:"workflow_ref,omitempty"`
	WorkflowRevisionID string `json:"workflow_revision_id,omitempty"`
	WorkflowRevisionNo int64  `json:"workflow_revision_no,omitempty"`
	Status             string `json:"status"`
	CurrentStepID      string `json:"current_step_id,omitempty"`
	StateVersion       int64  `json:"state_version"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

type SessionPage struct {
	Sessions      []SessionSummary `json:"sessions"`
	NextPageToken string           `json:"next_page_token,omitempty"`
}

type SessionLifecycleResult struct {
	SessionID    string `json:"session_id"`
	Status       string `json:"status"`
	StateVersion int64  `json:"state_version"`
	CommandID    string `json:"command_id"`
}

type BeginInput struct {
	SessionID          string `json:"session_id" jsonschema:"required,Workflow session returned by workflow.start"`
	StepID             string `json:"step_id" jsonschema:"required,One currently ready or retryable step"`
	CommandID          string `json:"command_id,omitempty" jsonschema:"Stable retry key; generated when omitted"`
	Objective          string `json:"objective,omitempty" jsonschema:"Optional runtime refinement which cannot replace the Workflow prompt"`
	RuntimeInstruction string `json:"runtime_instruction,omitempty" jsonschema:"Optional retry or execution note"`
}

type StepContract struct {
	ContractVersion   string            `json:"contract_version"`
	SessionID         string            `json:"session_id"`
	AttemptID         string            `json:"attempt_id"`
	StepID            string            `json:"step_id"`
	AttemptNo         int               `json:"attempt_no"`
	Operation         string            `json:"operation"`
	Objective         string            `json:"objective,omitempty"`
	Prompt            string            `json:"prompt,omitempty"`
	Acceptance        []string          `json:"acceptance_criteria,omitempty"`
	Instruction       string            `json:"instruction,omitempty"`
	PartialSelector   map[string][]int  `json:"partial_selector,omitempty"`
	WorkflowRevision  string            `json:"workflow_revision"`
	Inputs            map[string]any    `json:"inputs,omitempty"`
	DeclaredOutputs   []string          `json:"declared_outputs,omitempty"`
	RequiredOutputs   []string          `json:"required_outputs,omitempty"`
	OutputCardinality map[string]string `json:"output_cardinality,omitempty"`
	Capabilities      []string          `json:"capabilities,omitempty"`
	LegacyTools       []string          `json:"legacy_tools,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

type Execution struct {
	ExecutionID  string       `json:"execution_id"`
	LeaseExpires string       `json:"lease_expires_at"`
	StepContract StepContract `json:"step_contract"`
}

type BeginResult struct {
	Execution Execution  `json:"execution"`
	State     Projection `json:"state"`
}

type ResumeInput struct {
	SessionID   string `json:"session_id" jsonschema:"required,Workflow session identifier"`
	ExecutionID string `json:"execution_id" jsonschema:"required,Execution identifier returned by workflow.step.begin"`
}

type Output struct {
	Slot        string `json:"slot" jsonschema:"required,Declared Workflow output slot"`
	ContentType string `json:"content_type,omitempty" jsonschema:"MIME type or application/json"`
	Value       any    `json:"value,omitempty" jsonschema:"Inline JSON or text value"`
	LocalPath   string `json:"local_path,omitempty" jsonschema:"Local artifact file inside the current workspace; mutually exclusive with value"`
	Caption     string `json:"caption,omitempty"`
	Seq         int    `json:"seq,omitempty" jsonschema:"One-based idempotency sequence for list items or repeated revisions of one slot"`
}

type SubmitInput struct {
	SessionID   string   `json:"session_id" jsonschema:"required,Workflow session identifier"`
	ExecutionID string   `json:"execution_id" jsonschema:"required,Execution identifier returned by workflow.step.begin"`
	Outcome     string   `json:"outcome" jsonschema:"required,One of succeeded failed or cancelled"`
	Summary     string   `json:"summary,omitempty"`
	ErrorCode   string   `json:"error_code,omitempty"`
	ExecutorRef string   `json:"executor_ref,omitempty" jsonschema:"Optional external Agent task or trace reference"`
	Outputs     []Output `json:"outputs,omitempty" jsonschema:"Artifacts mapped to declared Workflow output slots"`
}

type SubmitResult struct {
	ExecutionID     string     `json:"execution_id"`
	AttemptStatus   string     `json:"attempt_status"`
	AlreadyTerminal bool       `json:"already_terminal,omitempty"`
	State           Projection `json:"state"`
}

type Artifact struct {
	ID                string `json:"artifact_id"`
	SessionID         string `json:"session_id"`
	SlotID            string `json:"slot_id"`
	Slot              string `json:"slot"`
	StepID            string `json:"step_id"`
	Attempt           int    `json:"attempt"`
	ProducerAttemptID string `json:"producer_attempt_id,omitempty"`
	Revision          int    `json:"revision"`
	ListIndex         *int   `json:"list_index,omitempty"`
	Selected          bool   `json:"selected"`
	Validity          string `json:"validity"`
	ChangeSource      string `json:"change_source"`
	ContentType       string `json:"content_type"`
	Value             any    `json:"value,omitempty"`
	Caption           string `json:"caption,omitempty"`
	Deleted           bool   `json:"deleted"`
	CreatedAt         string `json:"created_at"`
}

func NewClient(api *coreapi.Client, origin StartOrigin) (*Client, error) {
	if api == nil {
		return nil, errors.New("Workflow MCP requires a LazyMind API client")
	}
	origin.ConversationID = strings.TrimSpace(origin.ConversationID)
	origin.ExternalRef = strings.TrimSpace(origin.ExternalRef)
	return &Client{api: api, origin: origin}, nil
}

func (c *Client) List(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	err := c.api.DoJSON(ctx, http.MethodGet, "/workflow-runtime/v1/workflows", nil, &result)
	return result, err
}

func (c *Client) Get(ctx context.Context, workflowID, revisionID string) (map[string]any, error) {
	if strings.TrimSpace(workflowID) == "" {
		return nil, errors.New("workflow_id is required")
	}
	path := "/workflow-runtime/v1/workflows/" + url.PathEscape(workflowID)
	if strings.TrimSpace(revisionID) != "" {
		path += "?revision_id=" + url.QueryEscape(revisionID)
	}
	var result map[string]any
	err := c.api.DoJSON(ctx, http.MethodGet, path, nil, &result)
	return result, err
}

func (c *Client) State(ctx context.Context, sessionID string) (Projection, error) {
	if strings.TrimSpace(sessionID) == "" {
		return Projection{}, errors.New("session_id is required")
	}
	var state Projection
	err := c.api.DoJSON(ctx, http.MethodGet, "/workflow-sessions/"+url.PathEscape(sessionID)+"/projection", nil, &state)
	return state, err
}

func (c *Client) ListSessions(ctx context.Context, status string, pageSize int, pageToken string) (SessionPage, error) {
	if pageSize < 0 || pageSize > 100 {
		return SessionPage{}, errors.New("page_size must be between 1 and 100")
	}
	query := url.Values{}
	if strings.TrimSpace(status) != "" {
		query.Set("status", strings.TrimSpace(status))
	}
	if pageSize > 0 {
		query.Set("page_size", fmt.Sprintf("%d", pageSize))
	}
	if strings.TrimSpace(pageToken) != "" {
		query.Set("page_token", strings.TrimSpace(pageToken))
	}
	path := "/workflow-sessions"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var page SessionPage
	err := c.api.DoJSON(ctx, http.MethodGet, path, nil, &page)
	if page.Sessions == nil {
		page.Sessions = []SessionSummary{}
	}
	return page, err
}

func (c *Client) StopSession(ctx context.Context, sessionID, commandID string) (SessionLifecycleResult, error) {
	return c.setSessionStopped(ctx, sessionID, commandID, true)
}

func (c *Client) ResumeSession(ctx context.Context, sessionID, commandID string) (SessionLifecycleResult, error) {
	return c.setSessionStopped(ctx, sessionID, commandID, false)
}

func (c *Client) setSessionStopped(ctx context.Context, sessionID, commandID string, stopped bool) (SessionLifecycleResult, error) {
	sessionID, commandID = strings.TrimSpace(sessionID), strings.TrimSpace(commandID)
	if sessionID == "" {
		return SessionLifecycleResult{}, errors.New("session_id is required")
	}
	if commandID == "" {
		var err error
		commandID, err = newID("mcp-session-")
		if err != nil {
			return SessionLifecycleResult{}, err
		}
	}
	action := ":resume"
	if stopped {
		action = ":stop"
	}
	var result SessionLifecycleResult
	err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(sessionID)+action,
		map[string]any{"command_id": commandID}, &result)
	return result, err
}

func (c *Client) ImportInput(ctx context.Context, name, mimeType, hash, contentBase64 string, size int64) (InputImportResult, error) {
	request := map[string]any{
		"contract_version": contractVersion, "name": name, "mime_type": mimeType,
		"size": size, "content_hash": hash, "content_base64": contentBase64,
	}
	var resource InputResource
	if err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-input-resources", request, &resource); err != nil {
		return InputImportResult{}, err
	}
	return InputImportResult{Resource: resource, Binding: map[string]any{
		"resource_id": resource.ResourceID, "revision": resource.Revision, "content_hash": resource.ContentHash,
	}}, nil
}

func (c *Client) GetInput(ctx context.Context, resourceID string) (InputResource, error) {
	if strings.TrimSpace(resourceID) == "" {
		return InputResource{}, errors.New("resource_id is required")
	}
	var resource InputResource
	err := c.api.DoJSON(ctx, http.MethodGet, "/workflow-input-resources/"+url.PathEscape(resourceID), nil, &resource)
	return resource, err
}

func (c *Client) Start(ctx context.Context, input StartInput) (StartResult, error) {
	input.WorkflowID = strings.TrimSpace(input.WorkflowID)
	if input.WorkflowID == "" {
		return StartResult{}, errors.New("workflow_id is required")
	}
	if input.IdempotencyKey == "" {
		var err error
		input.IdempotencyKey, err = newID("mcp-start-")
		if err != nil {
			return StartResult{}, err
		}
	}
	if input.SessionID == "" {
		input.SessionID = sessionIDForKey(input.IdempotencyKey)
	}
	request := map[string]any{
		"idempotency_key": input.IdempotencyKey, "workflow_id": input.WorkflowID,
		"input_bindings": input.InputBindings, "origin_host": "external-agent", "origin_ref": "mcp",
		"controller_host": "external-agent", "request_context": input.RequestContext,
	}
	origin := c.origin
	if invocation, ok := coreapi.InvocationFromContext(ctx); ok {
		if strings.TrimSpace(origin.ExternalRef) == "" {
			origin.ExternalRef = strings.TrimSpace(invocation.ExternalRef)
		}
		if strings.TrimSpace(origin.ConversationID) == "" {
			origin.ConversationID = strings.TrimSpace(invocation.ConversationID)
		}
	}
	if origin.ExternalRef != "" {
		request["origin_ref"] = origin.ExternalRef
	}
	if origin.ConversationID != "" {
		request["conversation_id"] = origin.ConversationID
	}
	var prepared struct {
		PreparationID string `json:"preparation_id"`
	}
	if err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-preparations", request, &prepared); err != nil {
		return StartResult{}, err
	}
	var consumed struct {
		SessionID string `json:"session_id"`
	}
	if err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-preparations/"+url.PathEscape(prepared.PreparationID)+":consume",
		map[string]any{"session_id": input.SessionID}, &consumed); err != nil {
		return StartResult{}, err
	}
	state, err := c.State(ctx, consumed.SessionID)
	if err != nil {
		return StartResult{}, err
	}
	return StartResult{PreparationID: prepared.PreparationID, SessionID: consumed.SessionID, State: state}, nil
}

func (c *Client) Begin(ctx context.Context, input BeginInput) (BeginResult, error) {
	state, err := c.State(ctx, input.SessionID)
	if err != nil {
		return BeginResult{}, err
	}
	if !contains(state.Projection.Ready, input.StepID) && !contains(state.Projection.Retryable, input.StepID) && !contains(state.Projection.Rewindable, input.StepID) {
		return BeginResult{}, fmt.Errorf("step %q is not ready; ready=%v retryable=%v rewindable=%v", input.StepID,
			state.Projection.Ready, state.Projection.Retryable, state.Projection.Rewindable)
	}
	if input.CommandID == "" {
		input.CommandID, err = newID("mcp-step-")
		if err != nil {
			return BeginResult{}, err
		}
	}
	command := map[string]any{
		"contract_version": contractVersion, "command_id": input.CommandID,
		"tool": "advance_step_and_hand_off", "session_id": input.SessionID,
		"expected_state_version": state.StateVersion, "retry_origin": "automatic",
		"steps": []map[string]any{{"step_id": input.StepID, "objective": input.Objective, "runtime_instruction": input.RuntimeInstruction}},
	}
	var advanced struct {
		TaskID string `json:"task_id"`
		Tasks  []struct {
			StepID string `json:"step_id"`
			TaskID string `json:"task_id"`
		} `json:"tasks"`
	}
	if err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(input.SessionID)+":advance-step-and-hand-off", command, &advanced); err != nil {
		return BeginResult{}, err
	}
	executionID := advanced.TaskID
	for _, task := range advanced.Tasks {
		if task.StepID == input.StepID {
			executionID = task.TaskID
			break
		}
	}
	if executionID == "" {
		return BeginResult{}, errors.New("LazyMind accepted the step but returned no execution_id")
	}
	execution, err := c.begin(ctx, input.SessionID, executionID, false)
	if err != nil {
		return BeginResult{}, err
	}
	state, err = c.State(ctx, input.SessionID)
	return BeginResult{Execution: execution, State: state}, err
}

func (c *Client) Resume(ctx context.Context, input ResumeInput) (BeginResult, error) {
	execution, err := c.begin(ctx, input.SessionID, input.ExecutionID, true)
	if err != nil {
		return BeginResult{}, err
	}
	state, err := c.State(ctx, input.SessionID)
	return BeginResult{Execution: execution, State: state}, err
}

func (c *Client) begin(ctx context.Context, sessionID, executionID string, resume bool) (Execution, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(executionID) == "" {
		return Execution{}, errors.New("session_id and execution_id are required")
	}
	action := ":begin"
	if resume {
		action = ":resume"
	}
	var execution Execution
	err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(sessionID)+
		"/hosted-attempts/"+url.PathEscape(executionID)+action, map[string]any{}, &execution)
	return execution, err
}

func (c *Client) Submit(ctx context.Context, input SubmitInput, artifacts []map[string]any) (SubmitResult, error) {
	if strings.TrimSpace(input.SessionID) == "" || strings.TrimSpace(input.ExecutionID) == "" {
		return SubmitResult{}, errors.New("session_id and execution_id are required")
	}
	payload := map[string]any{
		"outcome": input.Outcome, "summary": input.Summary, "error_code": input.ErrorCode,
		"executor_ref": input.ExecutorRef, "artifacts": artifacts,
	}
	var result SubmitResult
	err := c.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(input.SessionID)+
		"/hosted-attempts/"+url.PathEscape(input.ExecutionID)+":submit", payload, &result)
	if err != nil {
		return SubmitResult{}, err
	}
	result.State, err = c.State(ctx, input.SessionID)
	return result, err
}

func (c *Client) ListArtifacts(ctx context.Context, sessionID string) ([]Artifact, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("session_id is required")
	}
	var result struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	err := c.api.DoJSON(ctx, http.MethodGet, "/workflow-sessions/"+url.PathEscape(sessionID)+"/artifacts", nil, &result)
	return result.Artifacts, err
}

func (c *Client) GetArtifact(ctx context.Context, artifactID string) (Artifact, error) {
	if strings.TrimSpace(artifactID) == "" {
		return Artifact{}, errors.New("artifact_id is required")
	}
	var result Artifact
	err := c.api.DoJSON(ctx, http.MethodGet, "/workflow-artifacts/"+url.PathEscape(artifactID), nil, &result)
	return result, err
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func newID(prefix string) (string, error) {
	const maxLength = 36
	if len(prefix) >= maxLength {
		return "", errors.New("generated ID prefix must be shorter than 36 characters")
	}
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	encoded := hex.EncodeToString(buffer)
	if remaining := maxLength - len(prefix); len(encoded) > remaining {
		encoded = encoded[:remaining]
	}
	return prefix + encoded, nil
}

func sessionIDForKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "mcp-" + hex.EncodeToString(sum[:16])
}
