import { SettingOutlined, StopOutlined, SwapOutlined } from "@ant-design/icons";
import { Alert, Button, Space } from "antd";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import {
  CHAT_OPEN_MODEL_SELECTOR_EVENT,
  getChatConversationPath,
} from "@/modules/chat/constants/chat";
import { MODEL_FAILURE_CODES } from "@/modules/chat/utils/chatStreamError";

import "./index.scss";

const KNOWN_CODES = new Set([
  ...MODEL_FAILURE_CODES,
  "length",
  "content_filter",
  "insufficient_system_resource",
  "unknown",
]);

const MODEL_SETTINGS_CODES = new Set([
  "authentication_failed",
  "permission_denied",
  "not_found",
  "usage_limit_exceeded",
  "quota_exhausted",
  "balance_exhausted",
  "organization_spend_limit_exceeded",
  "project_spend_limit_exceeded",
]);

const API_CREDENTIAL_CODES = new Set([
  "authentication_failed",
  "permission_denied",
]);

export interface RunTerminalView {
  status: "completed" | "interrupted" | "failed" | "cancelled";
  reason: string;
  code?: string;
  partial_output: boolean;
}

function isUserCancelledTerminal(terminal: RunTerminalView): boolean {
  return terminal.status === "cancelled";
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
    return "chat.runStatus.failed";
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
  conversationId,
  providerId,
  providerName,
  modelName,
  onRetry,
  retryDisabled = false,
}: {
  terminal?: RunTerminalView;
  conversationId?: string;
  providerId?: string;
  providerName?: string;
  modelName?: string;
  onRetry?: () => void;
  retryDisabled?: boolean;
}) {
  const { t } = useTranslation();
  if (!terminal || terminal.status === "completed") {
    return null;
  }
  const isCredentialFailure = API_CREDENTIAL_CODES.has(terminal.code || "");
  const isModelUnavailable = terminal.code === "not_found";
  const description = isCredentialFailure
    ? [
        t("chat.apiKeyUnavailableDescription", {
          provider: providerName || t("chat.modelServiceFallback"),
        }),
        terminal.partial_output
          ? t("chat.runStatus.partialOutput")
          : t("chat.runStatus.noOutput"),
      ].join(" ")
    : isModelUnavailable
      ? [
          t("chat.modelUnavailableDescription", {
            provider: providerName || t("chat.modelServiceFallback"),
            model: modelName || t("chat.currentModelFallback"),
          }),
          terminal.partial_output
            ? t("chat.runStatus.partialOutput")
            : t("chat.runStatus.noOutput"),
        ].join(" ")
      : runStatusDescription(terminal, t);
  const isCancelled = isUserCancelledTerminal(terminal);
  const className = isCancelled
    ? "chat-run-status-card chat-run-status-card--cancelled"
    : "chat-run-status-card";
  const isModelFailure = terminal.reason === "model_failure";
  const shouldCheckSettings = MODEL_SETTINGS_CODES.has(terminal.code || "");
  const modelSettingsUrl = (() => {
    const params = new URLSearchParams({ section: "models", view: "providers" });
    if (conversationId) {
      params.set("return_to", getChatConversationPath(conversationId));
    }
    if (providerId) {
      params.set("provider_id", providerId);
    }
    return `/settings?${params.toString()}`;
  })();

  const actions = !isCancelled ? (
    <Space className="chat-run-status-card__actions" size={6} wrap>
      {shouldCheckSettings ? (
        <Button
          size="small"
          aria-label={t("chat.checkModelSettings")}
          icon={<SettingOutlined />}
          href={modelSettingsUrl}
        >
          {t("chat.checkModelSettings")}
        </Button>
      ) : null}
      {onRetry ? (
        <Button size="small" disabled={retryDisabled} onClick={onRetry}>
          {t(shouldCheckSettings ? "chat.continueAfterConfiguration" : "chat.tryAgain")}
        </Button>
      ) : null}
      {isModelFailure ? (
        <Button
          size="small"
          aria-label={t("chat.changeModel")}
          icon={<SwapOutlined />}
          onClick={() => {
            window.dispatchEvent(
              new CustomEvent(CHAT_OPEN_MODEL_SELECTOR_EVENT, {
                detail: { conversationId },
              }),
            );
          }}
        >
          {t("chat.changeModel")}
        </Button>
      ) : null}
    </Space>
  ) : undefined;
  return (
    <Alert
      className={className}
      type={isCancelled ? "warning" : "error"}
      showIcon
      icon={isCancelled ? <StopOutlined aria-hidden="true" /> : undefined}
      message={t(isCredentialFailure
        ? "chat.apiKeyUnavailableTitle"
        : isModelUnavailable
          ? "chat.modelUnavailableTitle"
        : runStatusTitleKey(terminal))}
      description={description}
      action={actions}
    />
  );
}
