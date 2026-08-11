import { useCallback, useEffect, useRef, useState } from "react";
import { message } from "antd";
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from "@/components/auth";
import {
  axiosInstance,
  BASE_URL,
  localizeErrorCode,
} from "@/components/request";
import { fetchCurrentUser } from "@/modules/signin/utils/request";
import {
  fetchModelFeatures,
  isImageEmbedRequired,
  MODEL_FEATURES_CHANGED_EVENT,
} from "@/hooks/useModelFeatures";
import { isDesktopRuntime } from "@/runtime/mode";
import { waitForRuntimeCapability } from "@/runtime/readiness";

type ApiEnvelope<T> = {
  data?: T;
};

interface ModelReadyResponse {
  ready: boolean;
  source?: string;
}

export type ChatModelProviderStatus =
  | "idle"
  | "initializing"
  | "loading"
  | "ready"
  | "missing"
  | "error";

interface ChatModelProviderSnapshot {
  status: ChatModelProviderStatus;
  requiresModelProviderConfig: boolean | null;
  embeddingReady: boolean | null;
  multimodalEmbeddingReady: boolean | null;
  rerankReady: boolean | null;
  vlmReady: boolean | null;
}

let cachedSnapshotUserKey: string | null = null;
let cachedSnapshot: ChatModelProviderSnapshot | null = null;

function getCurrentUserCacheKey() {
  const userInfo = AgentAppsAuth.getUserInfo();
  return userInfo?.userId || userInfo?.username || userInfo?.token || null;
}

function getCachedSnapshot() {
  const userKey = getCurrentUserCacheKey();
  if (!userKey || userKey !== cachedSnapshotUserKey) {
    return null;
  }
  return cachedSnapshot;
}

function setCachedSnapshot(snapshot: ChatModelProviderSnapshot) {
  const userKey = getCurrentUserCacheKey();
  if (!userKey) {
    return;
  }
  cachedSnapshotUserKey = userKey;
  cachedSnapshot = snapshot;
}

function unwrapResponse<T>(payload: ApiEnvelope<T> | T): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

function createAbortError() {
  const error = new Error("Configuration readiness check was cancelled");
  error.name = "AbortError";
  return error;
}

function waitBeforeRetry(signal: AbortSignal, delayMs = 750) {
  return new Promise<void>((resolve, reject) => {
    if (signal.aborted) {
      reject(createAbortError());
      return;
    }

    const handleAbort = () => {
      clearTimeout(timer);
      reject(createAbortError());
    };
    const timer = window.setTimeout(() => {
      signal.removeEventListener("abort", handleAbort);
      resolve();
    }, delayMs);
    signal.addEventListener("abort", handleAbort, { once: true });
  });
}

async function requestWithStartupRetry<T>(
  request: () => Promise<T>,
  retry: boolean,
  signal: AbortSignal,
  isStale: () => boolean,
) {
  while (true) {
    if (signal.aborted || isStale()) {
      throw createAbortError();
    }

    try {
      return await request();
    } catch (error) {
      if (!retry) {
        throw error;
      }
      await waitBeforeRetry(signal);
    }
  }
}

export function useChatModelProviderGuard() {
  const desktopRuntime = isDesktopRuntime();
  const initialSnapshot = desktopRuntime ? null : getCachedSnapshot();
  const [status, setStatus] = useState<ChatModelProviderStatus>(
    () =>
      initialSnapshot?.status ??
      (desktopRuntime ? "initializing" : "loading"),
  );
  const [requiresModelProviderConfig, setRequiresModelProviderConfig] =
    useState<boolean | null>(() => {
      const dynamic = AgentAppsAuth.getUserInfo()?.dynamic;
      return typeof dynamic === "boolean"
        ? dynamic
        : initialSnapshot?.requiresModelProviderConfig ?? null;
    });
  const [embeddingReady, setEmbeddingReady] = useState<boolean | null>(
    () => initialSnapshot?.embeddingReady ?? null,
  );
  const [multimodalEmbeddingReady, setMultimodalEmbeddingReady] =
    useState<boolean | null>(
      () => initialSnapshot?.multimodalEmbeddingReady ?? null,
    );
  const [rerankReady, setRerankReady] = useState<boolean | null>(
    () => initialSnapshot?.rerankReady ?? null,
  );
  const [vlmReady, setVlmReady] = useState<boolean | null>(
    () => initialSnapshot?.vlmReady ?? null,
  );
  const [configurationRuntimeReady, setConfigurationRuntimeReady] =
    useState(!desktopRuntime);
  const [chatRuntimeReady, setChatRuntimeReady] = useState(!desktopRuntime);
  const requestIdRef = useRef(0);
  const configurationRuntimeReadyRef = useRef(!desktopRuntime);
  const runtimeWaitAbortRef = useRef<AbortController | null>(null);

  const runCheck = useCallback(async () => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    const isStale = () => requestIdRef.current !== requestId;
    const controller = new AbortController();
    runtimeWaitAbortRef.current?.abort();
    runtimeWaitAbortRef.current = controller;

    if (desktopRuntime && !configurationRuntimeReadyRef.current) {
      setStatus("initializing");
    } else if (!getCachedSnapshot()) {
      setStatus("loading");
    }

    try {
      await waitForRuntimeCapability("configuration", {
        signal: controller.signal,
      });
    } catch (error) {
      if ((error as Error)?.name === "AbortError" || isStale()) {
        return false;
      }
      setStatus(desktopRuntime ? "initializing" : "error");
      return false;
    }

    if (isStale()) {
      return false;
    }
    if (desktopRuntime) {
      configurationRuntimeReadyRef.current = true;
      setConfigurationRuntimeReady(true);
    }
    if (!getCachedSnapshot()) {
      setStatus("loading");
    }

    let shouldCheckModelProvider = false;

    try {
      const currentUser = await requestWithStartupRetry(
        fetchCurrentUser,
        desktopRuntime,
        controller.signal,
        isStale,
      );
      if (isStale()) {
        return false;
      }
      shouldCheckModelProvider = currentUser.dynamic === true;
      setRequiresModelProviderConfig(shouldCheckModelProvider);
    } catch (error) {
      if ((error as Error)?.name === "AbortError" || isStale()) {
        return false;
      }
      if (!isStale()) {
        setStatus(desktopRuntime ? "loading" : "error");
      }
      return false;
    }

    if (!shouldCheckModelProvider) {
      if (!isStale()) {
        setEmbeddingReady(null);
        setMultimodalEmbeddingReady(null);
        setRerankReady(null);
        setVlmReady(null);
        setStatus("ready");
        setCachedSnapshot({
          status: "ready",
          requiresModelProviderConfig: shouldCheckModelProvider,
          embeddingReady: null,
          multimodalEmbeddingReady: null,
          rerankReady: null,
          vlmReady: null,
        });
      }
      return true;
    }

    try {
      const features = await requestWithStartupRetry(
        () => fetchModelFeatures(true),
        desktopRuntime,
        controller.signal,
        isStale,
      );
      const imageEmbedRequired = isImageEmbedRequired(features);
      const silentRequestOptions = { silentError: true } as never;

      const [chatReadyResp, embeddingResp, multimodalEmbeddingResp, rerankResp, vlmResp] = await Promise.all([
        requestWithStartupRetry(
          () =>
            axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
              `${BASE_URL}/api/core/model_providers/models/ready?model_type=llm`,
              silentRequestOptions,
            ),
          desktopRuntime,
          controller.signal,
          isStale,
        ).catch(() => null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_main`,
          silentRequestOptions,
        ).catch(() => null),
        imageEmbedRequired
          ? axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
              `${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_image`,
              silentRequestOptions,
            ).catch(() => null)
          : Promise.resolve(null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=reranker`,
          silentRequestOptions,
        ).catch(() => null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=vlm`,
          silentRequestOptions,
        ).catch(() => null),
      ]);

      if (isStale()) {
        return false;
      }

      if (!chatReadyResp) {
        setStatus(desktopRuntime ? "loading" : "error");
        setEmbeddingReady(null);
        setMultimodalEmbeddingReady(null);
        setRerankReady(null);
        setVlmReady(null);
        setCachedSnapshot({
          status: "error",
          requiresModelProviderConfig: shouldCheckModelProvider,
          embeddingReady: null,
          multimodalEmbeddingReady: null,
          rerankReady: null,
          vlmReady: null,
        });
        if (!desktopRuntime) {
          message.error({
            key: "api-request-error",
            content: localizeErrorCode("2000509"),
          });
        }
        return false;
      }

      const ready = unwrapResponse<ModelReadyResponse>(chatReadyResp.data).ready === true;
      const nextStatus: ChatModelProviderStatus = ready ? "ready" : "missing";
      setStatus(nextStatus);

      const getReady = (resp: typeof embeddingResp): boolean | null => {
        if (!resp) return null;
        return unwrapResponse<ModelReadyResponse>(resp.data).ready ?? null;
      };
      const nextEmbeddingReady = getReady(embeddingResp);
      const nextMultimodalEmbeddingReady = imageEmbedRequired
        ? getReady(multimodalEmbeddingResp)
        : null;
      const nextRerankReady = getReady(rerankResp);
      const nextVlmReady = getReady(vlmResp);

      setEmbeddingReady(nextEmbeddingReady);
      // null means "not applicable" (image embed not configured) — does not trigger disabled state.
      setMultimodalEmbeddingReady(nextMultimodalEmbeddingReady);
      setRerankReady(nextRerankReady);
      setVlmReady(nextVlmReady);
      setCachedSnapshot({
        status: nextStatus,
        requiresModelProviderConfig: shouldCheckModelProvider,
        embeddingReady: nextEmbeddingReady,
        multimodalEmbeddingReady: nextMultimodalEmbeddingReady,
        rerankReady: nextRerankReady,
        vlmReady: nextVlmReady,
      });

      return ready;
    } catch (error) {
      if ((error as Error)?.name === "AbortError" || isStale()) {
        return false;
      }
      if (!isStale()) {
        setStatus(desktopRuntime ? "loading" : "error");
      }
      return false;
    }
  }, [desktopRuntime]);

  const refresh = useCallback(() => {
    void runCheck();
  }, [runCheck]);

  useEffect(() => {
    const updateDynamicUserState = () => {
      const dynamic = AgentAppsAuth.getUserInfo()?.dynamic;
      setRequiresModelProviderConfig(
        typeof dynamic === "boolean" ? dynamic : null,
      );
    };

    updateDynamicUserState();
    window.addEventListener(AUTH_USER_CHANGE_EVENT, updateDynamicUserState);
    window.addEventListener("storage", updateDynamicUserState);

    return () => {
      window.removeEventListener(AUTH_USER_CHANGE_EVENT, updateDynamicUserState);
      window.removeEventListener("storage", updateDynamicUserState);
    };
  }, []);

  useEffect(() => {
    if (!desktopRuntime) {
      setChatRuntimeReady(true);
      return;
    }

    const controller = new AbortController();
    setChatRuntimeReady(false);
    void waitForRuntimeCapability("chat", { signal: controller.signal })
      .then(() => {
        setChatRuntimeReady(true);
      })
      .catch((error) => {
        if ((error as Error)?.name !== "AbortError") {
          setChatRuntimeReady(false);
        }
      });

    return () => {
      controller.abort();
    };
  }, [desktopRuntime]);

  useEffect(() => {
    void runCheck();

    const onFeaturesChanged = () => {
      void runCheck();
    };
    window.addEventListener(MODEL_FEATURES_CHANGED_EVENT, onFeaturesChanged);
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        void runCheck();
      }
    };
    document.addEventListener("visibilitychange", onVisibilityChange);

    return () => {
      runtimeWaitAbortRef.current?.abort();
      window.removeEventListener(MODEL_FEATURES_CHANGED_EVENT, onFeaturesChanged);
      document.removeEventListener("visibilitychange", onVisibilityChange);
      // Invalidate in-flight work from a previous mount (e.g. React Strict Mode).
      requestIdRef.current += 1;
    };
  }, [desktopRuntime, runCheck]);

  const isRuntimeInitializing = desktopRuntime && !chatRuntimeReady;

  return {
    canChat: status === "ready" && chatRuntimeReady,
    isChecking: status === "loading" || status === "initializing",
    isRuntimeInitializing,
    isConfigurationReady: configurationRuntimeReady,
    needsModelProviderConfig: status === "missing",
    requiresModelProviderConfig: requiresModelProviderConfig === true,
    embeddingReady,
    multimodalEmbeddingReady,
    rerankReady,
    vlmReady,
    refresh,
    status,
  };
}
