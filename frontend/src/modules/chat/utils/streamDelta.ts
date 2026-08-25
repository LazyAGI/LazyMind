export type ChatStreamDeltaMode = "append" | "replace";

export function mergeChatStreamDelta(
  previous: string,
  incoming: string,
  mode?: ChatStreamDeltaMode,
) {
  return mode === "replace" ? incoming : previous + incoming;
}
