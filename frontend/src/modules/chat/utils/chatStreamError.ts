import type { RunTerminal } from "./StreamManager";

type ModelFailureCode = NonNullable<RunTerminal["code"]>;

export interface MappedChatStreamError {
  appCode: number | string;
  httpStatus: number;
  semanticCode: ModelFailureCode;
}

const MODEL_FAILURE_CODES = new Set<ModelFailureCode>([
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
]);

const CORE_MODEL_ERROR_CODE_MAP = new Map<string, ModelFailureCode>([
  ["2000856", "not_found"],
  ["2000959", "not_found"],
  ["2001312", "authentication_failed"],
  ["2001313", "authentication_failed"],
  ["2001314", "authentication_failed"],
  ["2001383", "authentication_failed"],
  ["2001421", "not_found"],
  ["2001597", "not_found"],
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
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

function mapMessageToModelFailure(message: string): ModelFailureCode | undefined {
  const normalized = message.trim().toLowerCase();
  if (normalized.includes("model config unavailable")) {
    return "not_found";
  }
  if (
    normalized.includes("api_key") ||
    normalized.includes("api key")
  ) {
    return "authentication_failed";
  }
  if (normalized.includes("model") && normalized.includes("not found")) {
    return "not_found";
  }
  return undefined;
}

/**
 * Converts a Core `{ code, message }` HTTP error into the public model-failure
 * vocabulary. Provider text is used only for classification and is never
 * returned, so callers cannot accidentally render credentials or raw errors.
 */
export function parseCoreChatStreamError(
  data: unknown,
  eventStatus: unknown,
): MappedChatStreamError | undefined {
  const httpStatus = Number(eventStatus);
  // A status of zero means the browser did not receive an HTTP response. Keep
  // treating it as a recoverable transport interruption.
  if (!Number.isInteger(httpStatus) || httpStatus < 100 || httpStatus > 599) {
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
  if (appCode === undefined || !message.trim() || String(appCode) === "0") {
    return undefined;
  }

  const directCode = String(appCode) as ModelFailureCode;
  const semanticCode = MODEL_FAILURE_CODES.has(directCode)
    ? directCode
    : CORE_MODEL_ERROR_CODE_MAP.get(String(appCode)) ??
      mapMessageToModelFailure(message);

  // Generic Core validation, authorization, and runtime envelopes are not
  // model-provider failures. Leave those to the normal request/recovery path
  // so the UI never suggests changing a model for an unrelated error.
  if (!semanticCode) {
    return undefined;
  }

  return { appCode, httpStatus, semanticCode };
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
  semanticCode: ModelFailureCode,
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
    run_status: "failed",
    run_terminal: {
      status: "failed",
      reason: "model_failure",
      code: semanticCode,
      partial_output: hasPartialAssistantOutput(assistant),
    },
  };
  const next = [...messages];
  if (assistantIndex >= 0) {
    next[assistantIndex] = failedAssistant;
  } else {
    next.push(failedAssistant);
  }
  return next;
}
