package modelprovider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// FailureCodeForRemoteModels classifies a provider failure without forwarding
// its message, body, URL or credentials. HTTP 429 may represent quota exhaustion.
func FailureCodeForRemoteModels(status int, body []byte, err error) string {
	if err != nil {
		var timeout interface{ Timeout() bool }
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
			return "timeout"
		}
		return "connection_error"
	}
	var payload struct {
		Error struct {
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		for _, raw := range []any{payload.Error.Code, payload.Error.Type} {
			value, ok := raw.(string)
			if !ok {
				continue
			}
			value = strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(value))
			switch value {
			case "insufficientquota", "quotaexceeded", "balanceexhausted", "insufficientbalance", "balancenotenough", "creditbalanceexhausted", "accountquotaexceeded":
				return "quota_exceeded"
			}
		}
	}
	switch status {
	case 400, 422:
		return "invalid_request"
	case 401:
		return "authentication_failed"
	case 402:
		return "quota_exceeded"
	case 403:
		return "permission_denied"
	case 404:
		return "model_list_unavailable"
	case 408, 504:
		return "timeout"
	case 429:
		return "rate_limited"
	}
	if status >= 200 && status < 300 {
		return "invalid_response"
	}
	return "upstream_error"
}

func safeConnectionReason(reason string) string {
	switch reason {
	case "authentication_failed", "permission_denied", "model_not_found", "model_list_unavailable", "quota_exceeded", "rate_limited", "timeout", "connection_error", "invalid_request", "invalid_response", "empty_response", "unsupported_type", "unsupported_provider", "unknown_type", "upstream_error":
		return reason
	}
	return "upstream_error"
}
