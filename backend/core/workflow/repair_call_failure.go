package workflow

import (
	"errors"

	"lazymind/core/algo"
)

func workflowRepairCallDiagnostic(err error) repairDiagnostic {
	d := repairDiagnostic{Code: "E_REPAIR_MODEL_FAILED", Severity: "error", Message: "AI 调用失败，本次没有应用修改。请稍后重新修复。"}
	var failure *algo.WorkflowTaskError
	if !errors.As(err, &failure) {
		return d
	}
	d.Details = map[string]any{"error_code": failure.Code, "retryable": failure.Retryable, "diagnostic_id": failure.DiagnosticID}
	switch failure.Code {
	case "request_timeout":
		d.Code, d.Message = "E_REPAIR_MODEL_TIMEOUT", "AI 修复调用超时，本次没有应用修改。请稍后重试或检查模型服务。"
	case "transport_error", "service_unavailable", "provider_overloaded", "provider_internal_error":
		d.Code, d.Message = "E_REPAIR_MODEL_TRANSPORT", "模型服务连接异常，本次没有应用修改。请检查连接后重新修复。"
	case "authentication_failed", "permission_denied", "model_configuration", "invalid_task_config":
		d.Code, d.Message = "E_REPAIR_MODEL_CONFIG", "模型认证或配置异常，本次没有应用修改。请检查模型配置后重试。"
	case "rate_limited", "concurrency_limited", "quota_exhausted", "balance_exhausted", "usage_limit_exceeded", "organization_spend_limit_exceeded", "project_spend_limit_exceeded":
		d.Code, d.Message = "E_REPAIR_MODEL_LIMIT", "模型请求受限或额度不足，本次没有应用修改。请检查额度或稍后重试。"
	case "invalid_model_output", "protocol_error":
		d.Code, d.Message = "E_REPAIR_MODEL_OUTPUT", "模型返回的修复结果格式无效，本次没有应用修改。请重新修复。"
	}
	return d
}
