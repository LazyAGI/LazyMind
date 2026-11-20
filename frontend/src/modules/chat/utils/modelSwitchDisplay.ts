import { chatProviderDisplayName } from "./providerDisplay";
import type { ChatModelSwitchIdentity } from "@/api/generated/core-client";

export function formatModelSwitchLabel(
  identity: ChatModelSwitchIdentity,
  maxInputTokens: number | null | undefined,
  unknownWindow: string,
  language = "en-US",
) {
  const label = [chatProviderDisplayName(identity, language), identity.model_name || identity.model_id].filter(Boolean).join(" · ");
  const window = maxInputTokens == null ? unknownWindow
    : maxInputTokens % 1024 === 0 ? `${maxInputTokens / 1024}K` : String(maxInputTokens);
  return `${label} (${window})`;
}
