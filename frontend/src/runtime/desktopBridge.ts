import { isDesktopRuntime } from "./mode";

export type DesktopBridgeUnavailableReason = "unavailable" | "failed";

export type DesktopBridgeResult =
  | { ok: true }
  | { ok: false; reason: DesktopBridgeUnavailableReason; error?: unknown };

interface LazyMindDesktopBridge {
  openLogsDir?: () => Promise<void> | void;
  openDataDir?: () => Promise<void> | void;
}

function getDesktopBridge(): LazyMindDesktopBridge | undefined {
  if (!isDesktopRuntime() || typeof window === "undefined") {
    return undefined;
  }

  return (window as Window & { lazymindDesktop?: LazyMindDesktopBridge })
    .lazymindDesktop;
}

async function callDesktopBridge(
  method: keyof LazyMindDesktopBridge,
): Promise<DesktopBridgeResult> {
  const bridge = getDesktopBridge();
  const handler = bridge?.[method];

  if (!handler) {
    return { ok: false, reason: "unavailable" };
  }

  try {
    await handler.call(bridge);
    return { ok: true };
  } catch (error) {
    return { ok: false, reason: "failed", error };
  }
}

export function openLogsDir(): Promise<DesktopBridgeResult> {
  return callDesktopBridge("openLogsDir");
}

export function openDataDir(): Promise<DesktopBridgeResult> {
  return callDesktopBridge("openDataDir");
}
