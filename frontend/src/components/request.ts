import axios from "axios";
import type { AxiosInstance, AxiosError, InternalAxiosRequestConfig } from "axios";
import { message } from "antd";
import { AgentAppsAuth } from "@/components/auth";
import i18n from "@/i18n";
import { getApiBaseUrl } from "@/runtime/apiBase";

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

const RAW_ERROR_MESSAGE_CODE_MAP: Record<string, string> = {
  "dataset name already exists": "2001102",
};

export function extractErrorCode(error: any): string | undefined {
  const responseData = getErrorPayload(error);
  const candidates = [
    responseData?.code,
    responseData?.error_code,
    responseData?.errorCode,
    responseData?.data?.code,
    responseData?.data?.error_code,
    responseData?.data?.errorCode,
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

export function getLocalizedErrorMessage(
  error: any,
  fallback?: string,
): string | undefined {
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

  return rawMessage || fallback;
}

/** Resolve a core error code (e.g. err_msg "2000725") via errors.{code} i18n. */
export function localizeErrorCode(code?: string, fallback = ""): string {
  const normalized = String(code ?? "").trim();
  if (normalized && i18n.exists(`errors.${normalized}`)) {
    return i18n.t(`errors.${normalized}`);
  }
  return normalized || fallback;
}

function isRefreshEndpoint(url?: string): boolean {
  if (!url) return false;
  return url.includes("/auth/refresh") || url.includes("/auth/login") || url.includes("/auth/logout");
}

export const handleError = async (error: AxiosError) => {
  if (isCanceledError(error)) return Promise.reject(error);
  
  const originalRequest = error.config as InternalAxiosRequestConfig & { _retry?: boolean };
  
  if (error.response) {
    if (error.response.status === 403) {
      const errMsg = getLocalizedErrorMessage(
        error,
        i18n.t("common.accessDenied"),
      );
      const errorCode = extractErrorCode(error);
      if (
        errorCode === "1000106" ||
        extractRawErrorMessage(error) === "User is disabled"
      ) {
        message.error(errMsg || i18n.t("auth.userDisabled"));
        void AgentAppsAuth.logout(
          `${BASE_URL || window.location.origin}${window.BASENAME || ""}/agent/chat`,
        );
        return Promise.reject(error);
      }
      message.error(errMsg || i18n.t("common.accessDenied"));
    } else if (error.response.status === 401) {
      if (isRefreshEndpoint(originalRequest?.url)) {
        if (AgentAppsAuth.isLoggedIn()) {
          message.warning(i18n.t("auth.sessionExpired"));
        }
        void AgentAppsAuth.logout();
        return Promise.reject(error);
      }

      if (!originalRequest || originalRequest._retry) {
        if (AgentAppsAuth.isLoggedIn()) {
          message.warning(i18n.t("auth.authFailedRelogin"));
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
        
        refreshQueue.forEach((cb) => {
          cb("");
        });
        refreshQueue = [];
        
        message.warning(i18n.t("auth.sessionExpired"));
        void AgentAppsAuth.logout();
        return Promise.reject(refreshError);
      } finally {
        isRefreshing = false;
      }
    } else {
      message.error(
        getLocalizedErrorMessage(error, i18n.t("common.requestFailed")) ||
          i18n.t("common.requestFailed"),
      );
    }
  } else if (error.request) {
    message.error(i18n.t("common.serverNoResponse"));
  } else {
    message.error(
      getLocalizedErrorMessage(error, i18n.t("common.requestError")) ||
        i18n.t("common.requestError"),
    );
  }
  return Promise.reject(error);
};

axiosInstance.interceptors.request.use(
  (config) => applyOptionalAuthHeader(config),
  handleError,
);
axiosInstance.interceptors.response.use((response) => response, handleError);

export { axiosInstance };
