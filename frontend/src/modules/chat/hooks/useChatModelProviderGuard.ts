import { useCallback, useEffect, useRef, useState } from "react";
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from "@/components/auth";
import { axiosInstance, BASE_URL } from "@/components/request";
import { fetchCurrentUser } from "@/modules/signin/utils/request";
import {
  fetchModelFeatures,
  isImageEmbedRequired,
  MODEL_FEATURES_CHANGED_EVENT,
} from "@/hooks/useModelFeatures";

type ApiEnvelope<T> = {
  data?: T;
};

interface ModelReadyResponse {
  ready: boolean;
  source?: string;
}

export type ChatModelProviderStatus =
  | "idle"
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

export function useChatModelProviderGuard() {
  const initialSnapshot = getCachedSnapshot();
  const [status, setStatus] = useState<ChatModelProviderStatus>(
    () => initialSnapshot?.status ?? "loading",
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
  const requestIdRef = useRef(0);

  const runCheck = useCallback(async () => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    if (!getCachedSnapshot()) {
      setStatus("loading");
    }

    const isStale = () => requestIdRef.current !== requestId;

    let shouldCheckModelProvider = false;

    try {
      const currentUser = await fetchCurrentUser();
      if (isStale()) {
        return false;
      }
      shouldCheckModelProvider = currentUser.dynamic === true;
      setRequiresModelProviderConfig(shouldCheckModelProvider);
    } catch {
      if (!isStale()) {
        setStatus("error");
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
      const features = await fetchModelFeatures(true);
      const imageEmbedRequired = isImageEmbedRequired(features);

      const [chatReadyResp, embeddingResp, multimodalEmbeddingResp, rerankResp, vlmResp] = await Promise.all([
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=llm`
        ).catch(() => null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_main`
        ).catch(() => null),
        imageEmbedRequired
          ? axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
              `${BASE_URL}/api/core/model_providers/models/ready?model_type=embed_image`
            ).catch(() => null)
          : Promise.resolve(null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=reranker`
        ).catch(() => null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=vlm`
        ).catch(() => null),
      ]);

      if (isStale()) {
        return false;
      }

      const ready = chatReadyResp
        ? unwrapResponse<ModelReadyResponse>(chatReadyResp.data).ready === true
        : false;
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
    } catch {
      if (!isStale()) {
        setStatus("error");
      }
      return false;
    }
  }, []);

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
      window.removeEventListener(MODEL_FEATURES_CHANGED_EVENT, onFeaturesChanged);
      document.removeEventListener("visibilitychange", onVisibilityChange);
      // Invalidate in-flight work from a previous mount (e.g. React Strict Mode).
      requestIdRef.current += 1;
    };
  }, [runCheck]);

  return {
    canChat: status === "ready",
    isChecking: status === "loading",
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
