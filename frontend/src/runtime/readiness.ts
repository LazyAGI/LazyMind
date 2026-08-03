import {
  runtimeStatus,
  type DesktopRuntimeStatus,
  type DesktopRuntimeStatusResult,
} from "./desktopBridge";
import { getRuntimeMode, type RuntimeMode } from "./mode";

export type RuntimeCapability = "configuration" | "chat" | "parser";
export type RuntimeCapabilityState = "starting" | "ready" | "failed";

const READY_SERVICE_STATES = new Set(["running", "ready"]);
const FAILED_SERVICE_STATES = new Set(["failed"]);

export const RUNTIME_CAPABILITY_SERVICES: Record<
  RuntimeCapability,
  readonly string[]
> = {
  configuration: ["local-proxy", "auth-service", "core", "frontend"],
  chat: ["local-proxy", "auth-service", "core", "frontend", "chat"],
  parser: [
    "core",
    "lazyllm-doc-server",
    "lazyllm-parse-server",
    "lazyllm-parse-worker",
    "lazyllm-algo",
  ],
};

export class RuntimeReadinessError extends Error {
  constructor(
    public readonly code: "failed" | "timeout",
    message: string,
    options?: { cause?: unknown },
  ) {
    super(message);
    this.name = "RuntimeReadinessError";
    if (options && "cause" in options) {
      (this as Error & { cause?: unknown }).cause = options.cause;
    }
  }
}

export interface WaitForCapabilityOptions {
  signal?: AbortSignal;
  timeoutMs?: number;
  pollIntervalMs?: number;
  onWaiting?: () => void;
  failFast?: boolean;
}

type RuntimeStatusReader = () => Promise<DesktopRuntimeStatusResult>;

export function resolveRuntimeCapabilityState(
  status: DesktopRuntimeStatus,
  capability: RuntimeCapability,
): RuntimeCapabilityState {
  const requiredServices = RUNTIME_CAPABILITY_SERVICES[capability];
  const services = status.services || {};

  if (
    requiredServices.some((name) =>
      FAILED_SERVICE_STATES.has(services[name]?.status || ""),
    )
  ) {
    return "failed";
  }

  return requiredServices.every((name) =>
    READY_SERVICE_STATES.has(services[name]?.status || ""),
  )
    ? "ready"
    : "starting";
}

function createAbortError() {
  const error = new Error("Runtime readiness wait was cancelled");
  error.name = "AbortError";
  return error;
}

function wait(delayMs: number, signal?: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(createAbortError());
      return;
    }

    const handleAbort = () => {
      clearTimeout(timer);
      reject(createAbortError());
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", handleAbort);
      resolve();
    }, delayMs);
    signal?.addEventListener("abort", handleAbort, { once: true });
  });
}

export async function waitForCapability(
  capability: RuntimeCapability,
  readStatus: RuntimeStatusReader,
  options: WaitForCapabilityOptions = {},
) {
  const timeoutMs = options.timeoutMs ?? 60_000;
  const pollIntervalMs = options.pollIntervalMs ?? 750;
  const deadline = Date.now() + timeoutMs;
  let waitingNotified = false;
  let lastError: unknown;

  while (true) {
    if (options.signal?.aborted) {
      throw createAbortError();
    }

    const result = await readStatus();
    if (result.ok) {
      const state = resolveRuntimeCapabilityState(result.data, capability);
      if (state === "ready") {
        return;
      }
      if (state === "failed") {
        lastError = new RuntimeReadinessError(
          "failed",
          `Runtime capability "${capability}" failed to start`,
        );
        if (options.failFast) {
          throw lastError;
        }
      }
    } else {
      lastError = result.error;
    }

    if (!waitingNotified) {
      waitingNotified = true;
      options.onWaiting?.();
    }

    const remainingMs = deadline - Date.now();
    if (remainingMs <= 0) {
      throw new RuntimeReadinessError(
        "timeout",
        `Timed out waiting for runtime capability "${capability}"`,
        { cause: lastError },
      );
    }

    await wait(Math.min(pollIntervalMs, remainingMs), options.signal);
  }
}

export function waitForRuntimeCapability(
  capability: RuntimeCapability,
  options: WaitForCapabilityOptions = {},
) {
  if (!shouldWaitForRuntimeCapability(getRuntimeMode())) {
    return Promise.resolve();
  }
  return waitForCapability(capability, runtimeStatus, options);
}

export function shouldWaitForRuntimeCapability(mode: RuntimeMode): boolean {
  return mode === "desktop";
}
