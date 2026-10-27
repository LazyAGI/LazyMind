import { useCallback, useEffect, useRef, useState } from "react";
import { getTranslationServiceStatus, translateSelectionText, type TranslationServiceStatus } from "@/modules/knowledge/api/translation";

const unavailableStatus: TranslationServiceStatus = {
  configured: false,
  provider: "",
  document_supported: false,
};

export function useDocumentTranslationService() {
  const [status, setStatus] = useState<TranslationServiceStatus>(unavailableStatus);
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
      if (!nextStatus.document_supported) setMode("llm");
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
    provider: status.provider,
    documentSupported: status.document_supported,
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
  const [sourceText, setSourceText] = useState("");
  const [sourceLanguage, setSourceLanguage] = useState<"en" | "zh">("en");
  const [provider, setProvider] = useState("");
  const [loading, setLoading] = useState(false);
  const requestGeneration = useRef(0);

  const performTranslation = useCallback(async (text: string, selectedProvider: string, language: "en" | "zh", generation: number) => {
    try {
      const response = await translateSelectionText(text, selectedProvider === "MyMemory" ? language : undefined);
      if (requestGeneration.current === generation) setResult(response.translated_text);
    } catch (error) {
      if (requestGeneration.current === generation) onError(error);
    } finally {
      if (requestGeneration.current === generation) setLoading(false);
    }
  }, [onError]);

  const translate = useCallback(async (text: string) => {
    const generation = ++requestGeneration.current;
    const language = /\p{Script=Han}/u.test(text) ? "zh" : "en";
    setSourceText(text);
    setSourceLanguage(language);
    setProvider("");
    setResult("");
    setLoading(true);
    try {
      const status = await refreshStatus();
      if (requestGeneration.current !== generation) return;
      setProvider(status.provider);
      await performTranslation(text, status.provider, language, generation);
    } catch (error) {
      if (requestGeneration.current === generation) {
        onError(error);
        setLoading(false);
      }
    }
  }, [onError, performTranslation, refreshStatus]);

  const changeSourceLanguage = useCallback(async (language: "en" | "zh") => {
    if (!sourceText || provider !== "MyMemory") return;
    const generation = ++requestGeneration.current;
    setSourceLanguage(language);
    setResult("");
    setLoading(true);
    await performTranslation(sourceText, provider, language, generation);
  }, [performTranslation, provider, sourceText]);

  const clear = useCallback(() => {
    requestGeneration.current += 1;
    setResult("");
    setSourceText("");
    setProvider("");
    setLoading(false);
  }, []);

  return {
    result,
    loading,
    canChooseSource: provider === "MyMemory",
    sourceLanguage,
    translate,
    changeSourceLanguage,
    clear,
  };
}
