package externalcapability

import (
	"encoding/json"
	"fmt"
	"strings"

	"lazymind/core/capability"
)

// The internal Chat gateway publishes bounded, sanitized errors. Decode only
// that contract; arbitrary response bodies and exception causes stay private.
func builtinToolError(status int, raw []byte) error {
	var envelope struct {
		Detail struct {
			Code      capability.ErrorCode      `json:"code"`
			Message   string                    `json:"message"`
			Retryable bool                      `json:"retryable"`
			Upstream  *capability.UpstreamError `json:"upstream"`
		} `json:"detail"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		detail := envelope.Detail
		switch detail.Code {
		case capability.InvalidArgument, capability.Unavailable, capability.DeadlineExceeded,
			capability.ToolExecutionFailed, capability.ProviderRejected, capability.RateLimited:
			message := strings.TrimSpace(detail.Message)
			if message != "" && len(message) <= 8192 {
				public := capability.NewError(detail.Code, "tool.call", message, detail.Retryable, nil)
				if upstream := detail.Upstream; upstream != nil && upstream.HTTPStatus >= 400 && upstream.HTTPStatus <= 599 &&
					len(upstream.Code) <= 512 && len(upstream.RequestID) <= 512 {
					public.Upstream = upstream
				}
				return public
			}
		}
	}
	return capability.NewError(capability.Unavailable, "tool.call",
		fmt.Sprintf("The internal tool service returned HTTP %d without public error details", status),
		status == 502 || status == 503, nil)
}
