export interface ApiBaseEnv {
  VITE_API_BASE_URL?: string;
}

function readApiBaseEnv(): ApiBaseEnv {
  return (
    (typeof import.meta !== "undefined" &&
      ((import.meta as unknown as { env?: ApiBaseEnv }).env || {})) ||
    {}
  );
}

function getWindowOrigin(): string {
  return (typeof window !== "undefined" && window.location.origin) || "";
}

function trimTrailingSlashes(value: string): string {
  return value.replace(/\/+$/, "");
}

function ensureLeadingSlash(path: string): string {
  return path.startsWith("/") ? path : `/${path}`;
}

export function resolveApiBaseUrl(
  env: ApiBaseEnv = readApiBaseEnv(),
  origin = getWindowOrigin(),
): string {
  return trimTrailingSlashes(String(env.VITE_API_BASE_URL || origin || ""));
}

export function getApiBaseUrl(): string {
  return resolveApiBaseUrl();
}

export function resolveApiUrl(
  path: string,
  env: ApiBaseEnv = readApiBaseEnv(),
  origin = getWindowOrigin(),
): string {
  const baseUrl = resolveApiBaseUrl(env, origin);
  const normalizedPath = ensureLeadingSlash(path);
  return baseUrl ? `${baseUrl}${normalizedPath}` : normalizedPath;
}

export function resolveCoreApiUrl(
  path: string,
  env: ApiBaseEnv = readApiBaseEnv(),
  origin = getWindowOrigin(),
): string {
  return resolveApiUrl(`/api/core/${path.replace(/^\/+/, "")}`, env, origin);
}

export function resolveAuthServiceApiUrl(
  path: string,
  env: ApiBaseEnv = readApiBaseEnv(),
  origin = getWindowOrigin(),
): string {
  return resolveApiUrl(`/api/authservice/${path.replace(/^\/+/, "")}`, env, origin);
}

export function apiUrl(path: string): string {
  return resolveApiUrl(path);
}

export function coreApiUrl(path: string): string {
  return resolveCoreApiUrl(path);
}

export function authServiceApiUrl(path: string): string {
  return resolveAuthServiceApiUrl(path);
}
