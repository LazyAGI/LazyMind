export type RuntimeMode = "cloud" | "local" | "desktop";

export interface RuntimeEnv {
  VITE_LAZYMIND_MODE?: string;
}

const RUNTIME_MODES = new Set<RuntimeMode>(["cloud", "local", "desktop"]);

function readRuntimeEnv(): RuntimeEnv {
  return (
    (typeof import.meta !== "undefined" &&
      ((import.meta as unknown as { env?: RuntimeEnv }).env || {})) ||
    {}
  );
}

export function resolveRuntimeMode(env: RuntimeEnv = readRuntimeEnv()): RuntimeMode {
  const rawMode = String(env.VITE_LAZYMIND_MODE || "")
    .trim()
    .toLowerCase();

  if (RUNTIME_MODES.has(rawMode as RuntimeMode)) {
    return rawMode as RuntimeMode;
  }

  return "cloud";
}

export function getRuntimeMode(): RuntimeMode {
  return resolveRuntimeMode();
}

export function isLocalRuntime(): boolean {
  return getRuntimeMode() === "local";
}

export function isDesktopRuntime(): boolean {
  return getRuntimeMode() === "desktop";
}
