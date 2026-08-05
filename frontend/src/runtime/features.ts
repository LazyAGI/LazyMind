import {
  isLocalLikeRuntimeMode,
  resolveRuntimeMode,
  type RuntimeEnv,
} from "./mode";

export interface RuntimeFeatureEnv extends RuntimeEnv {
  VITE_HIDE_EVO?: string;
}

export interface RuntimeFeatures {
  hideEvo: boolean;
  hideRegister: boolean;
  hideCloudAdmin: boolean;
  localLikeAutoLogin: boolean;
  hideLocalUserControls: boolean;
  hideUserGroupSurfaces: boolean;
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

export function resolveRuntimeFeatures(
  env: RuntimeFeatureEnv = readRuntimeFeatureEnv(),
): RuntimeFeatures {
  const mode = resolveRuntimeMode(env);
  const isLocalLike = isLocalLikeRuntimeMode(mode);
  const isDesktop = mode === "desktop";
  const explicitHideEvo = parseBooleanFlag(env.VITE_HIDE_EVO);

  return {
    hideEvo: explicitHideEvo ?? isLocalLike,
    hideRegister: isLocalLike,
    hideCloudAdmin: isLocalLike,
    localLikeAutoLogin: isLocalLike,
    hideLocalUserControls: isLocalLike,
    hideUserGroupSurfaces: isLocalLike,
    allowFolderPicker: isDesktop,
    allowOpenLogDir: isDesktop,
    useLocalGateway: isLocalLike,
  };
}

export const runtimeFeatures = resolveRuntimeFeatures();
