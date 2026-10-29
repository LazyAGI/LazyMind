import { useCallback, useEffect, useRef, useState } from "react";
import { getTranslationServiceStatus, translateSelectionText, type DocumentTranslationProvider, type TranslationServiceStatus } from "@/modules/knowledge/api/translation";
import { translateTextWithLLM } from "@/modules/knowledge/api/pdfArtifacts";

const unavailableStatus: TranslationServiceStatus = {
  configured: false,
  provider: "",
  document_supported: false,
};

export function resolveDocumentTranslationProvider(
  providers: DocumentTranslationProvider[],
  selectedGroupId?: string,
): DocumentTranslationProvider | undefined {
  return providers.find((item) => item.group_id === selectedGroupId);
}

export function useDocumentTranslationService() {
  const [status, setStatus] = useState<TranslationServiceStatus>(unavailableStatus);
  const [documentProviders, setDocumentProviders] = useState<DocumentTranslationProvider[]>([]);
  const [providerGroupId, setProviderGroupId] = useState<string>();
  const [mode, setMode] = useState<"api" | "llm">("api");
  const [modalOpen, setModalOpen] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const requestGeneration = useRef(0);

  const refreshStatus = useCallback(async () => {
    const generation = ++requestGeneration.current;
    setRefreshing(true);
    let nextStatus = unavailableStatus;
    try {
      nextStatus = await getTranslationServiceStatus();
    } catch {
      // The API choice stays unavailable until a later successful refresh.
    }
    if (requestGeneration.current === generation) {
      setStatus(nextStatus);
      const nextProviders = nextStatus.document_providers || [];
      setDocumentProviders(nextProviders);
      setProviderGroupId((current) => nextProviders.find((item) => item.group_id === current)?.group_id
        || nextProviders.find((item) => item.group_id === nextStatus.selected_group_id)?.group_id
        || nextProviders[0]?.group_id);
      if (!nextProviders.length) setMode("llm");
      setRefreshing(false);
    }
    return nextStatus;
  }, []);

  useEffect(() => {
    void refreshStatus();
    return () => { requestGeneration.current += 1; };
  }, [refreshStatus]);

  const openModal = useCallback(() => {
    setModalOpen(true);
    void refreshStatus();
  }, [refreshStatus]);

  return {
    provider: documentProviders.find((item) => item.group_id === providerGroupId)?.provider_name || status.provider,
    providerGroupId,
    setProviderGroupId,
    documentProviders,
    documentSupported: documentProviders.length > 0,
    configured: status.configured,
    mode,
    setMode,
    modalOpen,
    refreshing,
    setModalOpen,
    openModal,
    refreshStatus,
  };
}

export function getTranslationProgressLabel(
  job: { provider_type?: string; provider?: string } | undefined,
  preparing: boolean,
  mode: "api" | "llm",
  provider: string,
) {
  const jobMode = !preparing && job?.provider_type ? job.provider_type : mode;
  if (jobMode === "llm") return "LazyMind 大模型";
  if (!preparing && job) return job.provider || "翻译 API";
  return provider || "翻译 API";
}

export function usePdfSelectionTranslation(
  refreshStatus: () => Promise<TranslationServiceStatus>,
  onError: (error: unknown) => void,
) {
  const [result, setResult] = useState("");
  const [loading, setLoading] = useState(false);
  const requestGeneration = useRef(0);

  const performTranslation = useCallback(async (text: string, generation: number) => {
    try {
      const response = await translateSelectionText(text, undefined);
      if (requestGeneration.current === generation) setResult(response.translated_text);
    } catch (error) {
      if (requestGeneration.current === generation) onError(error);
    } finally {
      if (requestGeneration.current === generation) setLoading(false);
    }
  }, [onError]);

  const translate = useCallback(async (text: string) => {
    const generation = ++requestGeneration.current;
    setResult("");
    setLoading(true);
    try {
      await refreshStatus();
      if (requestGeneration.current !== generation) return;
      await performTranslation(text, generation);
    } catch (error) {
      if (requestGeneration.current === generation) {
        onError(error);
        setLoading(false);
      }
    }
  }, [onError, performTranslation, refreshStatus]);

  const translateWithLLM = useCallback(async (text: string) => {
    const source = text.trim();
    if (!source) return;
    const generation = ++requestGeneration.current;
    setResult("");
    setLoading(true);
    try {
      const targetLanguage = /[\u3400-\u9fff]/u.test(source) ? "en" : "zh";
      const translated = await translateTextWithLLM(source, targetLanguage);
      if (requestGeneration.current === generation) setResult(translated);
    } catch (error) {
      if (requestGeneration.current === generation) onError(error);
    } finally {
      if (requestGeneration.current === generation) setLoading(false);
    }
  }, [onError]);

  const clear = useCallback(() => {
    requestGeneration.current += 1;
    setResult("");
    setLoading(false);
  }, []);

  return {
    result,
    loading,
    translate,
    translateWithLLM,
    clear,
  };
}
