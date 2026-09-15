export const DEFAULT_LLM_MAX_INPUT_TOKENS = "128K";
export const DEFAULT_LLM_MAX_OUTPUT_TOKENS = "32K";
export const LLM_MAX_INPUT_TOKENS_MAX_LENGTH = 16;

const MAX_INPUT_TOKENS_PATTERN = /^[1-9]\d*([KkMm])?$/;

export function isLlmChatCapability(capability?: string) {
  return capability === "LLM_CHAT" || capability === "llm";
}

export function resolveLlmMaxInputTokens(value?: string | null) {
  const trimmed = value?.trim();
  return trimmed ? trimmed.toUpperCase() : DEFAULT_LLM_MAX_INPUT_TOKENS;
}

export function parseLlmMaxInputTokens(value?: string | null) {
  const normalized = resolveLlmMaxInputTokens(value);
  if (normalized.length > LLM_MAX_INPUT_TOKENS_MAX_LENGTH) {
    return null;
  }
  return MAX_INPUT_TOKENS_PATTERN.test(normalized) ? normalized : null;
}

export function isDefaultLlmMaxInputTokens(value?: string | null) {
  return parseLlmMaxInputTokens(value) === DEFAULT_LLM_MAX_INPUT_TOKENS;
}
