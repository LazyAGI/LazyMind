import { resolveRuntimeMode, type RuntimeEnv, type RuntimeMode } from "./mode";

export interface RuntimeFeatureEnv extends RuntimeEnv {
  VITE_HIDE_EVO?: string;
}

export interface RuntimeFeatures {
  hideEvo: boolean;
  hideRegister: boolean;
  hideCloudAdmin: boolean;
  localAutoLogin: boolean;
  allowFolderPicker: boolean;
  allowOpenLogDir: boolean;
  useLocalGateway: boolean;
}

function readRuntimeFeatureEnv(): RuntimeFeatureEnv {
  return (
    (typeof import.meta !== "undefined" &&
      ((import.meta as unknown as { env?: RuntimeFeatureEnv }).env || {})) ||
    {}
  );
}

function parseBooleanFlag(value?: string): boolean | undefined {
  const normalized = String(value ?? "")
    .trim()
    .toLowerCase();

  if (["1", "true", "yes", "on"].includes(normalized)) {
    return true;
  }
  if (["0", "false", "no", "off"].includes(normalized)) {
    return false;
  }
  return undefined;
}

function isLocalLikeMode(mode: RuntimeMode): boolean {
  return mode === "local" || mode === "desktop";
}

export function resolveRuntimeFeatures(
  env: RuntimeFeatureEnv = readRuntimeFeatureEnv(),
): RuntimeFeatures {
  const mode = resolveRuntimeMode(env);
  const isLocalLike = isLocalLikeMode(mode);
  const explicitHideEvo = parseBooleanFlag(env.VITE_HIDE_EVO);

  return {
    hideEvo: explicitHideEvo ?? isLocalLike,
    hideRegister: isLocalLike,
    hideCloudAdmin: isLocalLike,
    localAutoLogin: isLocalLike,
    allowFolderPicker: mode === "desktop",
    allowOpenLogDir: mode === "desktop",
    useLocalGateway: isLocalLike,
  };
}

export const runtimeFeatures = resolveRuntimeFeatures();
