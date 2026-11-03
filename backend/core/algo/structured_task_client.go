package algo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"lazymind/core/common"
)

const (
	FormulaExplanationTask        = "paper.formula.explain"
	FormulaExplanationTaskVersion = "formula-explanation-v7-low-latency"
	VisualExplanationTask         = "paper.visual.explain"
	VisualExplanationTaskVersion  = "paper-reading-v2-grounded-findings"
)

type StructuredTaskRequest struct {
	Task      string
	Version   string
	Input     map[string]any
	LLMConfig map[string]any
}

type StructuredTaskUsage struct {
	Task       string `json:"task_type"`
	Version    string `json:"task_version"`
	ModelCalls int    `json:"model_calls"`
	ElapsedMS  int64  `json:"elapsed_ms"`
}

type StructuredTaskResult struct {
	Output json.RawMessage     `json:"output"`
	Usage  StructuredTaskUsage `json:"usage"`
}

type StructuredTaskError struct {
	Code         string              `json:"error_code"`
	Retryable    bool                `json:"retryable"`
	DiagnosticID string              `json:"diagnostic_id"`
	Usage        StructuredTaskUsage `json:"usage"`
}

func (e *StructuredTaskError) Error() string { return "structured task: " + e.Code }

// RunStructuredTask performs one transport call to a registered, versioned task.
// The caller owns retries and business validation; never fall back to rewrite.
func RunStructuredTask(ctx context.Context, req StructuredTaskRequest) (StructuredTaskResult, error) {
	var result StructuredTaskResult
	if req.Task == "" || req.Version == "" {
		return result, &StructuredTaskError{Code: "invalid_task_config"}
	}
	payload := llmTaskRequest{Mode: "llm", TaskType: req.Task, Input: llmTaskInput{Data: req.Input}, LLMConfig: ensureLLMConfig(req.LLMConfig), Options: map[string]any{"task_version": req.Version}}
	var response struct {
		Status string              `json:"status"`
		Output json.RawMessage     `json:"output"`
		Usage  StructuredTaskUsage `json:"usage"`
	}
	if err := common.ApiPost(ctx, generateURL(llmTaskRunPath), payload, nil, &response, 2*time.Minute); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		failure := &StructuredTaskError{Code: "transport_error", Retryable: true, Usage: StructuredTaskUsage{ModelCalls: -1}}
		var httpErr *common.HTTPError
		if errors.As(err, &httpErr) {
			failure.Code = "upstream_error"
			var body struct {
				Detail StructuredTaskError `json:"detail"`
			}
			body.Detail.Usage.ModelCalls = -1
			if json.Unmarshal(httpErr.Body, &body) == nil && body.Detail.Code != "" {
				failure = &body.Detail
				switch body.Detail.Code {
				case "invalid_task_input", "invalid_task_config", "unsupported_task_type", "unsupported_task_version", "invalid_model_output", "model_configuration", "authentication_failed", "rate_limited", "request_timeout", "transport_error", "model_call_failed",
					"invalid_request", "permission_denied", "not_found", "usage_limit_exceeded", "concurrency_limited", "quota_exhausted", "balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded", "input_filtered", "output_filtered", "token_limit", "provider_overloaded", "service_unavailable", "provider_internal_error", "provider_rejected", "conflict", "unprocessable_entity", "protocol_error":
					// Public provider/task classification; never expose raw upstream messages.
				default:
					failure.Code = "upstream_error"
				}
			}
		}
		log.Printf("structured_task task=%s version=%s failed=%s model_calls=%d elapsed_ms=%d", req.Task, req.Version, failure.Code, failure.Usage.ModelCalls, failure.Usage.ElapsedMS)
		return result, failure
	}
	output := bytes.TrimSpace(response.Output)
	if response.Status != "succeeded" || len(output) == 0 || output[0] != '{' || len(output) > 1024*1024 || !json.Valid(output) {
		return result, &StructuredTaskError{Code: "invalid_model_output", Usage: response.Usage}
	}
	if response.Usage.Task != req.Task || response.Usage.Version != req.Version || response.Usage.ModelCalls != 1 {
		return result, fmt.Errorf("structured task response contract mismatch")
	}
	log.Printf("structured_task task=%s version=%s model_calls=%d elapsed_ms=%d", req.Task, req.Version, response.Usage.ModelCalls, response.Usage.ElapsedMS)
	return StructuredTaskResult{Output: output, Usage: response.Usage}, nil
}
