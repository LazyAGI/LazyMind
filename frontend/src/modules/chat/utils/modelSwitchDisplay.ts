import type { ChatModelSwitchIdentity } from "@/api/generated/core-client";

export function formatModelSwitchLabel(
  identity: ChatModelSwitchIdentity,
  maxInputTokens: number | null | undefined,
  unknownWindow: string,
) {
  const label = [identity.provider_name, identity.model_name || identity.model_id].filter(Boolean).join(" · ");
  const window = maxInputTokens == null ? unknownWindow
    : maxInputTokens % 1024 === 0 ? `${maxInputTokens / 1024}K` : String(maxInputTokens);
  return `${label} (${window})`;
}
