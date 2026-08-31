import { StopOutlined } from "@ant-design/icons";
import { Alert } from "antd";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import "./index.scss";

const KNOWN_CODES = new Set([
  "invalid_request",
  "authentication_failed",
  "permission_denied",
  "not_found",
  "rate_limited",
  "usage_limit_exceeded",
  "concurrency_limited",
  "quota_exhausted",
  "balance_exhausted",
  "organization_spend_limit_exceeded",
  "project_spend_limit_exceeded",
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

function isUserCancelledTerminal(terminal: RunTerminalView): boolean {
  return terminal.status === "cancelled" || terminal.reason === "user_cancelled";
}

export function runStatusDescription(
  terminal: RunTerminalView,
  t: TFunction,
): string {
  const parts: string[] = [];
  if (!isUserCancelledTerminal(terminal)) {
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

export function runStatusTitleKey(terminal: RunTerminalView): string {
  if (isUserCancelledTerminal(terminal)) {
    return "chat.runStatus.cancelled";
  }
  if (terminal.reason === "model_failure") {
    return "chat.runStatus.modelFailed";
  }
  if (terminal.reason === "model_incomplete") {
    return "chat.runStatus.interrupted";
  }
  if (terminal.reason === "runtime_failure") {
    return "chat.runStatus.runtimeFailed";
  }
  return `chat.runStatus.${terminal.status}`;
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
  const isCancelled = isUserCancelledTerminal(terminal);
  const className = isCancelled
    ? "chat-run-status-card chat-run-status-card--cancelled"
    : "chat-run-status-card";
  return (
    <Alert
      className={className}
      type={isCancelled ? "warning" : "error"}
      showIcon
      icon={isCancelled ? <StopOutlined aria-hidden="true" /> : undefined}
      message={t(runStatusTitleKey(terminal))}
      description={description}
    />
  );
}
