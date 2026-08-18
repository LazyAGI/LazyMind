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
  "input_filtered",
  "output_filtered",
  "token_limit",
  "request_timeout",
  "provider_overloaded",
  "service_unavailable",
  "provider_internal_error",
  "provider_rejected",
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
  provider_http_status?: number;
  retry_after_ms?: number;
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
  if (
    Number.isInteger(terminal.provider_http_status) &&
    Number(terminal.provider_http_status) > 0
  ) {
    parts.push(t("chat.runStatus.httpStatus", { status: terminal.provider_http_status }));
  }
  if (
    Number.isFinite(terminal.retry_after_ms) &&
    Number(terminal.retry_after_ms) > 0
  ) {
    parts.push(t("chat.runStatus.retryAfterSeconds", {
      seconds: Math.ceil(Number(terminal.retry_after_ms) / 1000),
    }));
  }
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
