import { AgentAppsAuth } from "@/components/auth";
import { Method, SSE } from "./sse";

export function createChatStream(
  url: string,
  payload: Record<string, unknown>,
  callbacks: Record<string, (event: CustomEvent) => void>,
) {
  return new SSE(url, {
    method: Method.POST,
    headers: {
      "Content-Type": "application/json",
      Accept: "text/event-stream",
      ...AgentAppsAuth.getAuthHeaders(),
    },
    timeout: 1_800_000,
    payload: JSON.stringify(payload),
    callbacks,
  });
}
