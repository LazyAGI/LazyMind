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

/** Compare distinct token limits without losing precision on long numeric input. */
export function tokenLimitCount(value?: string | null): bigint | null {
  const normalized = value?.trim().toUpperCase() || "";
  if (!MAX_INPUT_TOKENS_PATTERN.test(normalized) || normalized.length > LLM_MAX_INPUT_TOKENS_MAX_LENGTH) return null;
  const suffix = normalized.slice(-1);
  const amount = BigInt(suffix === "K" || suffix === "M" ? normalized.slice(0, -1) : normalized);
  return amount * BigInt(suffix === "K" ? 1024 : suffix === "M" ? 1024 * 1024 : 1);
}
