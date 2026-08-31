export type ModelNavigationStatus = "ready" | "pending";

export function resolveModelNavigationStatus(
  effectiveEnabled: boolean | undefined,
): ModelNavigationStatus | undefined {
  if (effectiveEnabled === undefined) return undefined;
  return effectiveEnabled ? "ready" : "pending";
}
