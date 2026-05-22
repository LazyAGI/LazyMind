import { useCallback, useEffect, useRef, useState } from "react";
import { AgentAppsAuth, AUTH_USER_CHANGE_EVENT } from "@/components/auth";
import { axiosInstance, BASE_URL } from "@/components/request";
import { fetchCurrentUser } from "@/modules/signin/utils/request";

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
  const [status, setStatus] = useState<ChatModelProviderStatus>("idle");
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
  const mountedRef = useRef(true);

  const refresh = useCallback(async () => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    setStatus("loading");

    let shouldCheckModelProvider = false;

    try {
      const currentUser = await fetchCurrentUser();
      if (!mountedRef.current || requestIdRef.current !== requestId) {
        return false;
      }
      shouldCheckModelProvider = currentUser.dynamic === true;
      setRequiresModelProviderConfig(shouldCheckModelProvider);
    } catch {
      if (mountedRef.current && requestIdRef.current === requestId) {
        setStatus("error");
      }
      return false;
    }

    if (!shouldCheckModelProvider) {
      setStatus("ready");
      return true;
    }

    try {
      const [chatReadyResp, embeddingResp, multimodalEmbeddingResp, rerankResp, vlmResp] = await Promise.all([
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=llm-chat`
        ).catch(() => null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=embedding`
        ).catch(() => null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=multimodal_embedding`
        ).catch(() => null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=rerank`
        ).catch(() => null),
        axiosInstance.get<ApiEnvelope<ModelReadyResponse> | ModelReadyResponse>(
          `${BASE_URL}/api/core/model_providers/models/ready?model_type=VLM`
        ).catch(() => null),
      ]);

      if (!mountedRef.current || requestIdRef.current !== requestId) {
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
      setMultimodalEmbeddingReady(getReady(multimodalEmbeddingResp));
      setRerankReady(getReady(rerankResp));
      setVlmReady(getReady(vlmResp));

      return ready;
    } catch {
      if (mountedRef.current && requestIdRef.current === requestId) {
        setStatus("error");
      }
      return false;
    }
  }, []);

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
    mountedRef.current = true;
    void refresh();

    return () => {
      mountedRef.current = false;
    };
  }, [refresh]);

  return {
    canChat: status === "ready",
    isChecking: status === "idle" || status === "loading",
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
