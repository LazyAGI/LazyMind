import axios from "axios";
import type { AxiosInstance, AxiosError, InternalAxiosRequestConfig } from "axios";
import { message } from "antd";
import { AgentAppsAuth } from "@/components/auth";
import i18n from "@/i18n";
import { getApiBaseUrl } from "@/runtime/apiBase";
import {
  ensureLocalSession,
  isLocalSessionEnabled,
  localSessionInitialized,
  restoreLocalSessionAndGetToken,
} from "@/runtime/localSession";

export const BASE_URL = getApiBaseUrl();

const axiosInstance: AxiosInstance = axios.create({
  timeout: 30000,
});

let isRefreshing = false;
let refreshQueue: Array<(token: string) => void> = [];

function processQueue(newToken: string) {
  refreshQueue.forEach((cb) => cb(newToken));
  refreshQueue = [];
}

function applyOptionalAuthHeader(config: any) {
  const authHeaders = AgentAppsAuth.getAuthHeaders();
  config.headers = config.headers ?? {};
  config.headers["Accept-Language"] =
    i18n.resolvedLanguage || i18n.language || "zh-CN";

  if (authHeaders.authorization) {
    if (!config.headers.Authorization && !config.headers.authorization) {
      config.headers.authorization = authHeaders.authorization;
    }
  }

  if (authHeaders["X-User-Id"]) {
    if (
      !config.headers["X-User-Id"] &&
      !config.headers["X-User-ID"] &&
      !config.headers["x-user-id"]
    ) {
      config.headers["X-User-Id"] = authHeaders["X-User-Id"];
    }
  }

  if (authHeaders["X-Tenant-ID"]) {
    if (
      !config.headers["X-Tenant-ID"] &&
      !config.headers["X-Tenant-Id"] &&
      !config.headers["x-tenant-id"]
    ) {
      config.headers["X-Tenant-ID"] = authHeaders["X-Tenant-ID"];
    }
  }

  if (config.headers.Authorization === "Bearer undefined") {
    delete config.headers.Authorization;
  }
  if (config.headers.authorization === "Bearer undefined") {
    delete config.headers.authorization;
  }
  return config;
}

function isCanceledError(error: any): boolean {
  if (error?.code === "ERR_CANCELED" || error?.name === "CanceledError")
    return true;
  if (error?.config?.signal?.aborted) return true;
  const msg = (error?.message || "").toLowerCase();
  return (
    msg.includes("canceled") ||
    msg.includes("cancelled") ||
    msg.includes("aborted")
  );
}

function getErrorPayload(error: any): any {
  return error?.response?.data ?? error;
}

function hasApiResponse(error: any): boolean {
  return error?.response !== undefined;
}

const HTTP_STATUS_ERROR_CODE_MAP: Record<number, string> = {
  400: "2000103",
  401: "2000104",
  403: "2000102",
  404: "2000106",
  409: "2000107",
  429: "2000108",
  502: "2000110",
};

const GENERIC_REQUEST_ERROR_CODE = "2000509";
const API_ERROR_MESSAGE_KEY = "api-request-error";

const RAW_ERROR_MESSAGE_CODE_MAP: Record<string, string> = {
  "dataset name already exists": "2001102",
};

export function extractErrorCode(error: any): string | undefined {
  const responseData = getErrorPayload(error);
  const candidates = [
    responseData?.code,
    responseData?.error_code,
    responseData?.errorCode,
    responseData?.err_code,
    responseData?.err_msg,
    responseData?.error?.code,
    responseData?.data?.code,
    responseData?.data?.error_code,
    responseData?.data?.errorCode,
    responseData?.data?.err_code,
    responseData?.data?.err_msg,
    responseData?.data?.error?.code,
  ];

  for (const candidate of candidates) {
    if (candidate !== undefined && candidate !== null) {
      const normalized = String(candidate).trim();
      if (normalized) {
        return normalized;
      }
    }
  }

  return undefined;
}

function extractRawErrorMessage(error: any): string | undefined {
  const responseData = getErrorPayload(error);
  const detail = responseData?.detail;

  if (Array.isArray(detail)) {
    const messages = detail
      .map((item: any) =>
        typeof item === "string" ? item : item?.msg || item?.message,
      )
      .filter(Boolean);

    if (messages.length > 0) {
      return messages.join("；");
    }
  }

  if (typeof detail === "string" && detail.trim()) {
    return detail;
  }

  if (
    typeof responseData?.message === "string" &&
    responseData.message.trim()
  ) {
    return responseData.message;
  }

  if (typeof responseData?.err_msg === "string" && responseData.err_msg.trim()) {
    return responseData.err_msg;
  }

  if (
    typeof responseData?.error?.message === "string" &&
    responseData.error.message.trim()
  ) {
    return responseData.error.message;
  }

  if (
    typeof error?.response?.message === "string" &&
    error.response.message.trim()
  ) {
    return error.response.message;
  }

  if (typeof error?.message === "string" && error.message.trim()) {
    return error.message;
  }

  return undefined;
}

export function getLocalizedErrorMessage(error: any): string {
  const errorCode = extractErrorCode(error);

  if (errorCode && i18n.exists(`errors.${errorCode}`)) {
    return i18n.t(`errors.${errorCode}`);
  }

  const rawMessage = extractRawErrorMessage(error);
  const rawMessageCode = rawMessage
    ? RAW_ERROR_MESSAGE_CODE_MAP[rawMessage.trim().toLowerCase()]
    : undefined;

  if (rawMessageCode && i18n.exists(`errors.${rawMessageCode}`)) {
    return i18n.t(`errors.${rawMessageCode}`);
  }

  // Backend response text is diagnostic data, not a user-facing translation.
  // API failures may only use the shared error catalog, never page copy.
  if (hasApiResponse(error)) {
    const statusCode = Number(error?.response?.status);
    return localizeErrorCode(
      HTTP_STATUS_ERROR_CODE_MAP[statusCode] || GENERIC_REQUEST_ERROR_CODE,
    );
  }

  // Some APIs return a business-error envelope with HTTP 200. Once a code is
  // present, keep the same catalog-only rule even without an Axios response.
  if (errorCode) {
    return localizeErrorCode(GENERIC_REQUEST_ERROR_CODE);
  }

  if (
    error?.isAxiosError ||
    error?.request ||
    ["ERR_NETWORK", "ECONNABORTED", "ETIMEDOUT"].includes(error?.code)
  ) {
    return localizeErrorCode(GENERIC_REQUEST_ERROR_CODE);
  }

  // This helper is reserved for request failures. Unknown errors must not leak
  // browser, transport, or backend text through page-level fallbacks.
  return localizeErrorCode(GENERIC_REQUEST_ERROR_CODE);
}

/** Resolve a core error code (e.g. err_msg "2000725") via errors.{code} i18n. */
export function localizeErrorCode(code?: string, fallback = ""): string {
  const normalized = String(code ?? "").trim();
  if (normalized && i18n.exists(`errors.${normalized}`)) {
    return i18n.t(`errors.${normalized}`);
  }
  return fallback;
}

function isRefreshEndpoint(url?: string): boolean {
  if (!url) return false;
  return url.includes("/auth/refresh") || url.includes("/auth/login") || url.includes("/auth/logout");
}

function extractBusinessEnvelopeErrorCode(responseData: any): string | undefined {
  if (!responseData || typeof responseData !== "object") return undefined;
  const candidate =
    responseData.code ??
    responseData.error_code ??
    responseData.errorCode ??
    responseData.err_code ??
    responseData.err_msg;
  if (candidate === undefined || candidate === null) return undefined;
  const code = String(candidate).trim();
  if (!code || code === "0" || code === "200") return undefined;

  const looksLikeEnvelope =
    Object.prototype.hasOwnProperty.call(responseData, "message") &&
    (Object.prototype.hasOwnProperty.call(responseData, "data") ||
      /^\d{7}$/.test(code));
  if (i18n.exists(`errors.${code}`) || looksLikeEnvelope) {
    return code;
  }
  return undefined;
}

async function restoreLocalSessionAndRetry(
  originalRequest?: InternalAxiosRequestConfig & { _localSessionRetry?: boolean },
) {
  if (!isLocalSessionEnabled()) {
    return null;
  }
  if (!originalRequest) {
    return null;
  }
  const token = await restoreLocalSessionAndGetToken();
  originalRequest._localSessionRetry = true;
  originalRequest.headers = originalRequest.headers ?? {};
  originalRequest.headers.authorization = `Bearer ${token}`;
  return axiosInstance(originalRequest);
}

export const handleError = async (error: AxiosError): Promise<any> => {
  if (isCanceledError(error)) return Promise.reject(error);
  
  const originalRequest = error.config as InternalAxiosRequestConfig & { _retry?: boolean; _localSessionRetry?: boolean; silentError?: boolean };
  const silentError = Boolean(originalRequest?.silentError);
  
  if (error.response) {
    if (error.response.status === 403) {
      const errMsg = getLocalizedErrorMessage(error);
      const errorCode = extractErrorCode(error);
      if (
        errorCode === "1000106" ||
        extractRawErrorMessage(error) === "User is disabled"
      ) {
        message.error({
          key: API_ERROR_MESSAGE_KEY,
          content: errMsg || localizeErrorCode(GENERIC_REQUEST_ERROR_CODE),
        });
        void AgentAppsAuth.logout(
          `${BASE_URL || window.location.origin}${window.BASENAME || ""}/agent/chat`,
        );
        return Promise.reject(error);
      }
      if (!silentError) {
        message.error({
          key: API_ERROR_MESSAGE_KEY,
          content: errMsg || localizeErrorCode(GENERIC_REQUEST_ERROR_CODE),
        });
      }
    } else if (error.response.status === 401) {
      if (isRefreshEndpoint(originalRequest?.url)) {
        if (isLocalSessionEnabled()) {
          try {
            await restoreLocalSessionAndGetToken();
          } catch (localSessionError) {
            console.error("Local admin session restore failed:", localSessionError);
          }
          return Promise.reject(error);
        }
        if (AgentAppsAuth.isLoggedIn()) {
          message.warning({
            key: API_ERROR_MESSAGE_KEY,
            content:
              getLocalizedErrorMessage(error) ||
              localizeErrorCode(GENERIC_REQUEST_ERROR_CODE),
          });
        }
        void AgentAppsAuth.logout();
        return Promise.reject(error);
      }

      if (!originalRequest || originalRequest._retry) {
        if (isLocalSessionEnabled() && originalRequest && !originalRequest._localSessionRetry) {
          try {
            const localSessionRetryResponse = await restoreLocalSessionAndRetry(originalRequest);
            if (localSessionRetryResponse) {
              return localSessionRetryResponse;
            }
          } catch (localSessionError) {
            console.error("Local admin session restore failed:", localSessionError);
          }
        }
        if (AgentAppsAuth.isLoggedIn()) {
          message.warning({
            key: API_ERROR_MESSAGE_KEY,
            content:
              getLocalizedErrorMessage(error) ||
              localizeErrorCode(GENERIC_REQUEST_ERROR_CODE),
          });
        }
        void AgentAppsAuth.logout();
        return Promise.reject(error);
      }

      originalRequest._retry = true;

      if (isRefreshing) {
        return new Promise((resolve, reject) => {
          refreshQueue.push((newToken: string) => {
            if (originalRequest.headers) {
              originalRequest.headers.authorization = `Bearer ${newToken}`;
            }
            axiosInstance(originalRequest).then(resolve).catch(reject);
          });
        });
      }

      isRefreshing = true;

      try {
        const newAccessToken = await AgentAppsAuth.refreshAccessToken();
        
        processQueue(newAccessToken);

        if (originalRequest.headers) {
          originalRequest.headers.authorization = `Bearer ${newAccessToken}`;
        }

        return await axiosInstance(originalRequest);
      } catch (refreshError) {
        console.error("Token refresh failed:", refreshError);

        if (isLocalSessionEnabled()) {
          try {
            const token = await restoreLocalSessionAndGetToken();
            processQueue(token);
            originalRequest.headers = originalRequest.headers ?? {};
            originalRequest.headers.authorization = `Bearer ${token}`;
            originalRequest._localSessionRetry = true;
            return await axiosInstance(originalRequest);
          } catch (localSessionError) {
            console.error("Local admin session restore failed:", localSessionError);
          }
        }
        
        refreshQueue.forEach((cb) => {
          cb("");
        });
        refreshQueue = [];
        
        message.warning({
          key: API_ERROR_MESSAGE_KEY,
          content:
            getLocalizedErrorMessage(refreshError) ||
            localizeErrorCode(GENERIC_REQUEST_ERROR_CODE),
        });
        void AgentAppsAuth.logout();
        return Promise.reject(refreshError);
      } finally {
        isRefreshing = false;
      }
    } else {
      if (!silentError) {
        message.error({
          key: API_ERROR_MESSAGE_KEY,
          content: getLocalizedErrorMessage(error),
        });
      }
    }
  } else if (error.request) {
    if (!silentError) {
      message.error({
        key: API_ERROR_MESSAGE_KEY,
        content: localizeErrorCode(GENERIC_REQUEST_ERROR_CODE),
      });
    }
  } else {
    if (!silentError) {
      message.error({
        key: API_ERROR_MESSAGE_KEY,
        content: getLocalizedErrorMessage(error),
      });
    }
  }
  return Promise.reject(error);
};

axiosInstance.interceptors.request.use(
  (config) => {
    if (
      isLocalSessionEnabled() &&
      (!AgentAppsAuth.getUserInfo()?.token || !localSessionInitialized)
    ) {
      return ensureLocalSession().then(() => applyOptionalAuthHeader(config));
    }
    return applyOptionalAuthHeader(config);
  },
  handleError,
);
axiosInstance.interceptors.response.use((response) => {
  const businessErrorCode = extractBusinessEnvelopeErrorCode(response.data);
  if (!businessErrorCode) return response;

  const error = new axios.AxiosError(
    "Business request failed",
    "ERR_BAD_RESPONSE",
    response.config,
    response.request,
    response,
  );
  return handleError(error);
}, handleError);

export { axiosInstance };
