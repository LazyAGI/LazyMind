export const DEFAULT_LLM_MAX_INPUT_TOKENS = "128K";

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
  return MAX_INPUT_TOKENS_PATTERN.test(normalized) ? normalized : null;
}
