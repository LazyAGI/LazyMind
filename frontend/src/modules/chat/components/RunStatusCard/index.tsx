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

const MODEL_FALLBACK_CODES = new Set(["model_fallback_exhausted", "model_context_not_fit"]);

const RUNTIME_FAILURE_CODES = new Set([
  "request_rejected",
  "user_env_unavailable",
  "user_env_invalid_name",
]);

const KNOWN_CODES = new Set([
  ...MODEL_FAILURE_CODES,
  ...MODEL_FALLBACK_CODES,
  ...RUNTIME_FAILURE_CODES,
  "length",
  "content_filter",
  "insufficient_system_resource",
  "unknown",
]);

const MODEL_SETTINGS_CODES = new Set([
  "authentication_failed",
  "model_configuration",
  "permission_denied",
  "not_found",
  "usage_limit_exceeded",
  "quota_exhausted",
  "balance_exhausted",
  "organization_spend_limit_exceeded",
  "project_spend_limit_exceeded",
]);

const CODE_TITLE_KEYS: Record<string, string> = {
  authentication_failed: "chat.apiKeyUnavailableTitle",
  permission_denied: "chat.runStatus.titles.permission",
  model_configuration: "chat.runStatus.titles.configuration",
  not_found: "chat.modelUnavailableTitle",
  request_timeout: "chat.runStatus.titles.timeout",
  rate_limited: "chat.runStatus.titles.rateLimit",
  concurrency_limited: "chat.runStatus.titles.concurrencyLimit",
  usage_limit_exceeded: "chat.runStatus.titles.usageLimit",
  quota_exhausted: "chat.runStatus.titles.quota",
  balance_exhausted: "chat.runStatus.titles.quota",
  organization_spend_limit_exceeded: "chat.runStatus.titles.quota",
  project_spend_limit_exceeded: "chat.runStatus.titles.quota",
};

export interface RunTerminalView {
  status: "completed" | "interrupted" | "failed" | "cancelled";
  reason: string;
  code?: string;
  partial_output: boolean;
  diagnostic_id?: string;
  model_invoked?: boolean;
}

function isUserCancelledTerminal(terminal: RunTerminalView): boolean {
  return terminal.status === "cancelled";
}

function isModelTerminal(terminal: RunTerminalView): boolean {
  return terminal.reason === "model_failure" || terminal.reason === "model_incomplete";
}

function canChangeModel(terminal: RunTerminalView): boolean {
  return terminal.model_invoked !== false && (
    isModelTerminal(terminal) ||
    terminal.code === "upstream_request_failed" ||
    terminal.code === "upstream_stream_failed"
  );
}

export function runStatusDescription(
  terminal: RunTerminalView,
  t: TFunction,
): string {
  const parts: string[] = [];
  if (!isUserCancelledTerminal(terminal)) {
    const reasonKey = terminal.code && KNOWN_CODES.has(terminal.code) && (
      isModelTerminal(terminal) || RUNTIME_FAILURE_CODES.has(terminal.code) || MODEL_FALLBACK_CODES.has(terminal.code)
    )
      ? `chat.runStatus.codes.${terminal.code}`
      : terminal.reason === "model_incomplete"
        ? "chat.runStatus.incompleteUnknown"
        : terminal.reason === "runtime_failure" && !canChangeModel(terminal)
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
  const codeTitle = isModelTerminal(terminal) && terminal.code && Object.prototype.hasOwnProperty.call(CODE_TITLE_KEYS, terminal.code)
    ? CODE_TITLE_KEYS[terminal.code]
    : undefined;
  if (codeTitle) {
    return codeTitle;
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
  const isCancelled = isUserCancelledTerminal(terminal);
  const isCredentialFailure = !isCancelled && isModelTerminal(terminal) && terminal.code === "authentication_failed";
  const isModelUnavailable = !isCancelled && isModelTerminal(terminal) && terminal.code === "not_found";
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
  const className = isCancelled
    ? "chat-run-status-card chat-run-status-card--cancelled"
    : "chat-run-status-card";
  const isFallbackFailure = MODEL_FALLBACK_CODES.has(terminal.code || "");
  const shouldChangeModel = canChangeModel(terminal) || isFallbackFailure;
  const shouldCheckSettings = MODEL_SETTINGS_CODES.has(terminal.code || "") || isFallbackFailure;
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
          {t(shouldCheckSettings ? "chat.runStatus.retryAfterSettings" : "chat.tryAgain")}
        </Button>
      ) : null}
      {shouldChangeModel ? (
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
      message={t(runStatusTitleKey(terminal))}
      description={description}
      action={actions}
    />
  );
}
