import { Alert } from "antd";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

const KNOWN_CODES = new Set([
  "invalid_request",
  "authentication_failed",
  "permission_denied",
  "not_found",
  "rate_limited",
  "too_many_requests",
  "quota_exhausted",
  "balance_exhausted",
  "organization_spend_limit_exceeded",
  "project_spend_limit_exceeded",
  "organization_usage_limit_exceeded",
  "input_filtered",
  "output_filtered",
  "token_limit",
  "request_timeout",
  "provider_overloaded",
  "service_unavailable",
  "provider_internal_error",
  "provider_rejected",
  "conflict",
  "unprocessable_entity",
  "protocol_error",
  "transport_error",
  "length",
  "content_filter",
  "insufficient_system_resource",
  "unknown",
]);

export interface RunTerminalView {
  status: "completed" | "interrupted" | "failed" | "cancelled";
  reason: string;
  code?: string;
  partial_output: boolean;
}

export function runStatusDescription(
  terminal: RunTerminalView,
  t: TFunction,
): string {
  const parts: string[] = [];
  if (terminal.status !== "cancelled") {
    const reasonKey = terminal.code && KNOWN_CODES.has(terminal.code)
      ? `chat.runStatus.codes.${terminal.code}`
      : terminal.reason === "model_incomplete"
        ? "chat.runStatus.incompleteUnknown"
        : terminal.reason === "runtime_failure"
          ? "chat.runStatus.runtimeError"
          : "chat.runStatus.providerError";
    parts.push(t(reasonKey));
  }
  parts.push(
    terminal.partial_output
      ? t("chat.runStatus.partialOutput")
      : t("chat.runStatus.noOutput"),
  );
  return parts.join(" ");
}

export default function RunStatusCard({
  terminal,
}: {
  terminal?: RunTerminalView;
}) {
  const { t } = useTranslation();
  if (!terminal || terminal.status === "completed") {
    return null;
  }
  const description = runStatusDescription(terminal, t);
  return (
    <Alert
      className="chat-run-status-card"
      type={terminal.status === "cancelled" ? "warning" : "error"}
      showIcon
      message={t(`chat.runStatus.${terminal.status}`)}
      description={description}
    />
  );
}
