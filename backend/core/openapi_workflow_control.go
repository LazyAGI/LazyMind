package main

import (
	"lazymind/core/common/orm"
	"lazymind/core/workflow"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/hosted"
)

type workflowControlPath struct {
	SessionID string `json:"session_id"`
}
type workflowHostActionPath struct {
	ActionID string `json:"action_id"`
}
type workflowHostedPath struct {
	SessionID string `json:"session_id"`
	AttemptID string `json:"attempt_id"`
}
type workflowControlReply struct {
	Code    int                            `json:"code"`
	Message string                         `json:"message"`
	Data    workflow.WorkflowControlResult `json:"data"`
}
type workflowControlSnapshotData struct {
	Control    *controlstore.Snapshot `json:"control"`
	Session    map[string]any         `json:"session,omitempty"`
	Projection map[string]any         `json:"projection,omitempty"`
}
type workflowControlSnapshotReply struct {
	Code    int                         `json:"code"`
	Message string                      `json:"message"`
	Data    workflowControlSnapshotData `json:"data"`
}
type workflowControlError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type workflowControlErrorReply struct {
	OK    bool                 `json:"ok"`
	Error workflowControlError `json:"error"`
}
type workflowCapabilitiesData struct {
	Protocol    string `json:"protocol"`
	SchemaReady bool   `json:"schema_ready"`
}
type workflowCapabilitiesReply struct {
	Code    int                      `json:"code"`
	Message string                   `json:"message"`
	Data    workflowCapabilitiesData `json:"data"`
}
type workflowHostPageReply struct {
	Code    int                             `json:"code"`
	Message string                          `json:"message"`
	Data    workflow.WorkflowHostActionPage `json:"data"`
}
type workflowHostClaimReply struct {
	Code    int                        `json:"code"`
	Message string                     `json:"message"`
	Data    workflow.WorkflowHostClaim `json:"data"`
}
type workflowHostReceiptReply struct {
	Code    int                    `json:"code"`
	Message string                 `json:"message"`
	Data    orm.WorkflowHostAction `json:"data"`
}
type workflowHostedExecutionReply struct {
	ContractVersion string           `json:"contract_version"`
	OK              bool             `json:"ok"`
	Result          hosted.Execution `json:"result"`
}
type workflowHostedSubmissionReply struct {
	ContractVersion string                  `json:"contract_version"`
	OK              bool                    `json:"ok"`
	Result          hosted.SubmissionResult `json:"result"`
}
type workflowExecutionBeginBody struct {
	CommandID            string `json:"command_id"`
	StepID               string `json:"step_id"`
	ExpectedStateVersion int64  `json:"expected_state_version"`
	Objective            string `json:"objective,omitempty"`
	RuntimeInstruction   string `json:"runtime_instruction,omitempty"`
}
type workflowExecutionStopBody struct {
	CommandID string `json:"command_id"`
}

func workflowControlOperations() []openAPIOperation {
	body := func(v any) *openAPIBody {
		return &openAPIBody{Required: true, ContentType: "application/json", Schema: schemaSource{Type: v}}
	}
	response := func(v any) map[int]openAPIResponse {
		return map[int]openAPIResponse{200: {Description: "Workflow control result", ContentType: "application/json", Schema: schemaSource{Type: v}}, 409: {Description: "Stale state, invalid admission, or conflicting command", ContentType: "application/json", Schema: schemaSource{Type: workflowControlErrorReply{}}}}
	}
	operation := func(method, path, summary string, params any, request any, reply any) openAPIOperation {
		value := openAPIOperation{Method: method, Path: path, Summary: summary, Tags: []string{"workflow-control"}, PathParams: params, Responses: response(reply)}
		if request != nil {
			value.RequestBody = body(request)
		}
		return value
	}
	result := []openAPIOperation{
		operation("GET", "/workflow-control/capabilities", "Inspect installed workflow control protocol and schema readiness", nil, nil, workflowCapabilitiesReply{}),
		operation("GET", "/workflow-sessions/{session_id}/control", "Read one consistent workflow control and workbench snapshot", workflowControlPath{}, nil, workflowControlSnapshotReply{}),
		operation("POST", "/workflow-sessions/{session_id}/control", "Apply an authenticated user review or lifecycle decision", workflowControlPath{}, workflow.WorkflowControlCommand{}, workflowControlReply{}),
		operation("POST", "/workflow-sessions/{session_id}/executions:begin", "Create one execution grant subject to authoritative admission", workflowControlPath{}, workflowExecutionBeginBody{}, workflowControlReply{}),
		operation("POST", "/workflow-sessions/{session_id}/executions:stop", "Fence workflow executions and request host cancellation", workflowControlPath{}, workflowExecutionStopBody{}, workflowControlReply{}),
		operation("POST", "/workflow-sessions/{session_id}/host-binding", "Pair the workflow with its original host driver", workflowControlPath{}, workflow.WorkflowHostBindingRequest{}, workflowControlSnapshotReply{}),
		operation("GET", "/workflow-host-actions", "Page unsettled actions for a paired connector", nil, nil, workflowHostPageReply{}),
		operation("GET", "/workflow-host-actions/{action_id}", "Inspect a host action and its current control state", workflowHostActionPath{}, nil, workflowHostClaimReply{}),
		operation("POST", "/workflow-host-actions/{action_id}:claim", "Acquire an exclusive delivery lease without resending unknown outcomes", workflowHostActionPath{}, workflow.WorkflowHostIdentity{}, workflowHostClaimReply{}),
		operation("POST", "/workflow-host-actions/{action_id}:settle", "Record host acceptance or reconcile a durable native event", workflowHostActionPath{}, workflow.WorkflowHostReceipt{}, workflowHostReceiptReply{}),
		operation("POST", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}:begin", "Claim an existing queued execution and receive its fenced handle", workflowHostedPath{}, nil, workflowHostedExecutionReply{}),
		operation("POST", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}:resume", "Rotate the existing execution handle for explicit recovery", workflowHostedPath{}, nil, workflowHostedExecutionReply{}),
		operation("POST", "/workflow-sessions/{session_id}/hosted-attempts/{attempt_id}:submit", "Atomically submit artifacts, terminal outcome, and required review", workflowHostedPath{}, hosted.Submission{}, workflowHostedSubmissionReply{}),
	}
	result[1].QueryParams = struct {
		View string `json:"view,omitempty"`
	}{}
	result[2].Headers = struct {
		Origin string `json:"Origin"`
	}{}
	result[2].Description = "Review commands are not exposed as MCP tools. Reuse the identical command_id and body after an uncertain response. A fixed receipt and fresh control are returned separately. Confirmation requires the exact displayed review version and manifest hash."
	for _, i := range []int{6, 7} {
		result[i].QueryParams = struct {
			ConnectorID string `json:"connector_id"`
			After       string `json:"after,omitempty"`
		}{}
		result[i].Headers = struct {
			Credential string `json:"X-Workflow-Host-Credential"`
		}{}
	}
	result[12].Description = "workflow.control.v1 runs require execution_handle. Repeating the identical terminal submission returns its receipt and current control; conflicting content is rejected."
	return result
}
