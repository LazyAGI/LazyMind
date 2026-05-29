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

function unwrapResponse<T>(payload: ApiEnvelope<T> | T): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

export function useChatModelProviderGuard() {
  const [status, setStatus] = useState<ChatModelProviderStatus>("loading");
  const [requiresModelProviderConfig, setRequiresModelProviderConfig] =
    useState<boolean | null>(() => {
      const dynamic = AgentAppsAuth.getUserInfo()?.dynamic;
      return typeof dynamic === "boolean" ? dynamic : null;
    });
  const [embeddingReady, setEmbeddingReady] = useState<boolean | null>(null);
  const [multimodalEmbeddingReady, setMultimodalEmbeddingReady] = useState<boolean | null>(null);
  const [rerankReady, setRerankReady] = useState<boolean | null>(null);
  const [vlmReady, setVlmReady] = useState<boolean | null>(null);
  const requestIdRef = useRef(0);

  const runCheck = useCallback(async () => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    setStatus("loading");

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
        setStatus("ready");
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
      setStatus(ready ? "ready" : "missing");

      const getReady = (resp: typeof embeddingResp): boolean | null => {
        if (!resp) return null;
        return unwrapResponse<ModelReadyResponse>(resp.data).ready ?? null;
      };
      setEmbeddingReady(getReady(embeddingResp));
      // null means "not applicable" (image embed not configured) — does not trigger disabled state.
      setMultimodalEmbeddingReady(imageEmbedRequired ? getReady(multimodalEmbeddingResp) : null);
      setRerankReady(getReady(rerankResp));
      setVlmReady(getReady(vlmResp));

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
