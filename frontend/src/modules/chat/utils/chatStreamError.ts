import type { RunTerminal } from "./StreamManager";
import { ModelFailureCode as ModelFailureCodeEnum } from "@/api/generated/core-client";

type ModelFailureCode = NonNullable<RunTerminal["code"]>;
type ChatStreamFailureCode = ModelFailureCode | "request_rejected";

export interface MappedChatStreamError {
  appCode: number | string;
  httpStatus: number;
  terminal: RunTerminal;
  legacy?: true;
  historyId?: string;
}

export const MODEL_FAILURE_CODES: ReadonlySet<string> = new Set(Object.values(ModelFailureCodeEnum));

// Compatibility with Core versions that did not return run_terminal. New
// classifications belong to the server terminal contract, not this table.
const CORE_MODEL_ERROR_CODE_MAP = new Map<string, ModelFailureCode>([
  ["2000856", "not_found"],
  ["2000959", "model_configuration"],
  ["2001312", "model_configuration"],
  ["2001313", "model_configuration"],
  ["2001314", "model_configuration"],
  ["2001383", "model_configuration"],
  ["2001421", "not_found"],
  ["2001597", "model_configuration"],
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// These errors reject creation before Core has persisted a conversation.
// Return only known translation keys, never diagnostic response text.
export function parseWorkspaceCreationError(data: unknown, status: unknown): string | undefined {
  if (![400, 403, 404, 409].includes(Number(status))) return undefined;
  let payload = data;
  if (typeof payload === "string") {
    try { payload = JSON.parse(payload); } catch { return undefined; }
  }
  if (!isRecord(payload)) return undefined;
  if (String(payload.code) === "2002813") return "errors.2002813";
  const body = isRecord(payload.data) ? payload.data : payload;
  const detail = isRecord(body.detail) ? body.detail : undefined;
  const reason = detail?.reason;
  if (typeof reason === "string" && [
    "path_unavailable", "revoked", "workspace_not_found", "path_invalid",
    "invalid_selection", "binding_conflict", "binding_locked", "mode_forbidden",
  ].includes(reason)) return `chat.workspace.reason.${reason}`;
  return undefined;
}

function normalizeAppCode(value: unknown): number | string | undefined {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim()) {
    return value.trim();
  }
  return undefined;
}

function parseHTTPRunTerminal(value: unknown): RunTerminal | undefined {
  if (!isRecord(value) || typeof value.partial_output !== "boolean" || typeof value.reason !== "string") return undefined;
  const hasCode = Object.prototype.hasOwnProperty.call(value, "code");
  if (hasCode && (typeof value.code !== "string" || !value.code.trim())) return undefined;
  const valid = value.status === "failed"
    ? ["model_failure", "runtime_failure"].includes(String(value.reason)) && hasCode
    : value.status === "interrupted"
      ? ["model_failure", "model_incomplete"].includes(String(value.reason)) && hasCode
      : value.status === "cancelled" && value.reason === "user_cancelled" && !hasCode;
  if (!valid) return undefined;
  for (const field of ["model_call_id", "diagnostic_id"] as const) {
    if (field in value && typeof value[field] !== "string") return undefined;
  }
  if ("model_invoked" in value && typeof value.model_invoked !== "boolean") return undefined;
  const diagnostic = value.transport_diagnostic;
  if (diagnostic !== undefined && (!isRecord(diagnostic) ||
    typeof diagnostic.code !== "string" ||
    !["upstream_stream_failed", "upstream_stream_timeout"].includes(String(diagnostic.code)))) return undefined;
  // Only retain public terminal fields, never arbitrary provider response data.
  return {
    status: value.status as RunTerminal["status"], reason: value.reason as RunTerminal["reason"],
    partial_output: value.partial_output,
    ...(hasCode ? { code: value.code as string } : {}),
    ...(typeof value.model_invoked === "boolean" ? { model_invoked: value.model_invoked } : {}),
    ...(typeof value.model_call_id === "string" ? { model_call_id: value.model_call_id } : {}),
    ...(typeof value.diagnostic_id === "string" ? { diagnostic_id: value.diagnostic_id } : {}),
    ...(isRecord(diagnostic) ? { transport_diagnostic: { code: diagnostic.code as "upstream_stream_failed" | "upstream_stream_timeout" } } : {}),
  };
}

/**
 * Converts a rejected Core HTTP request into a terminal failure, not a stream
 * interruption. Classification uses known codes, never provider message text.
 */
export function parseCoreChatStreamError(
  data: unknown,
  eventStatus: unknown,
): MappedChatStreamError | undefined {
  const httpStatus = Number(eventStatus);
  // A status of zero means the browser did not receive an HTTP response. Keep
  // treating it as a recoverable transport interruption.
  if (!Number.isInteger(httpStatus) || httpStatus < 400 || httpStatus > 599) {
    return undefined;
  }

  let payload: unknown = data;
  if (typeof data === "string") {
    try {
      payload = JSON.parse(data);
    } catch {
      return undefined;
    }
  }
  if (!isRecord(payload)) {
    return undefined;
  }

  const appCode = normalizeAppCode(payload.code);
  const message = typeof payload.message === "string" ? payload.message : "";
  if (appCode === undefined || String(appCode) === "0") {
    return undefined;
  }

  const body = isRecord(payload.data) ? payload.data : payload;
  const detail = isRecord(body.detail) ? body.detail : undefined;
  const historyId = typeof detail?.history_id === "string" && detail.history_id.trim()
    ? detail.history_id.trim() : undefined;
  if (Object.prototype.hasOwnProperty.call(body, "run_terminal")) {
    // A malformed terminal is still an HTTP rejection, not a lost connection.
    // Do not guess from the numeric code or resume an older conversation turn.
    const terminal: RunTerminal = parseHTTPRunTerminal(body.run_terminal) ?? {
      status: "failed", reason: "runtime_failure", code: "request_rejected", partial_output: false,
    };
    return { appCode, httpStatus, terminal, ...(historyId ? { historyId } : {}) };
  }
  if (!message.trim()) return undefined;
  let reason: "model_failure" | "runtime_failure" = "runtime_failure";
  let semanticCode: ChatStreamFailureCode = "request_rejected";
  if (detail?.reason === "user_env_invalid_name") {
    semanticCode = "user_env_invalid_name";
  } else if (detail?.reason === "user_env_unavailable" ||
      message === "Unable to access user environment variables; check the credential key configuration") {
    semanticCode = "user_env_unavailable";
  } else {
    const directCode = String(appCode);
    const modelCode = MODEL_FAILURE_CODES.has(directCode)
      ? directCode : CORE_MODEL_ERROR_CODE_MAP.get(directCode);
    if (modelCode) { semanticCode = modelCode; reason = "model_failure"; }
  }

  // Core rejected this request before opening SSE. Resuming by conversation id
  // would replay an older turn; runtime failures must not suggest model changes.
  return {
    appCode, httpStatus,
    terminal: { status: "failed", reason, code: semanticCode, partial_output: false },
    legacy: true,
    ...(historyId ? { historyId } : {}),
  };
}

function hasPartialAssistantOutput(message: Record<string, unknown>): boolean {
  if (
    String(message.delta || message.raw_delta || "").trim() ||
    String(message.reasoning_content || "").trim()
  ) {
    return true;
  }
  return Array.isArray(message.answers) &&
    message.answers.some(
      (answer) =>
        isRecord(answer) &&
        (String(answer.content || "").trim() ||
          String(answer.reasoning_content || "").trim()),
    );
}

export function applyChatStreamFailure(
  messages: any[],
  assistantRole: string,
  terminalOrCode: RunTerminal | ChatStreamFailureCode,
  reason: "model_failure" | "runtime_failure" = "model_failure",
): any[] {
  const assistantIndex = messages.findLastIndex(
    (item) => item?.role === assistantRole,
  );
  const hasUserTurn = messages.some((item) => item?.role !== assistantRole);
  if (assistantIndex < 0 && !hasUserTurn) {
    return messages;
  }

  const assistant = assistantIndex >= 0
    ? messages[assistantIndex]
    : { role: assistantRole, delta: "", answers: [] };
  const failedAssistant = {
    ...assistant,
    model_retry: undefined,
    run_status: typeof terminalOrCode === "string" ? "failed" : terminalOrCode.status,
    run_terminal: typeof terminalOrCode === "string" ? {
      status: "failed", reason, code: terminalOrCode,
      partial_output: hasPartialAssistantOutput(assistant),
    } : terminalOrCode,
  };
  const next = [...messages];
  if (assistantIndex >= 0) {
    next[assistantIndex] = failedAssistant;
  } else {
    next.push(failedAssistant);
  }
  return next;
}
