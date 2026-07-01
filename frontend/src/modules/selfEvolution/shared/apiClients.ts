import { AgentApi as CoreAgentApi, Configuration as CoreConfiguration, DefaultApi as CoreDefaultApi, type Dataset } from "@/api/generated/core-client";
import { BASE_URL, axiosInstance } from "@/components/request";
import { t } from "./i18n";

export function getSelfEvolutionWorkflowImageSrc(language?: string) {
  return language?.startsWith("en") ? "/Lazy-e.png" : "/Lazy-c.png";
}

export function createCoreAgentApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new CoreDefaultApi(
    new CoreConfiguration({
      basePath: baseUrl,
      baseOptions: {
        headers: { "Content-Type": "application/json" },
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

export function createCoreAgentGeneratedApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new CoreAgentApi(
    new CoreConfiguration({
      basePath: baseUrl,
      baseOptions: {
        headers: { "Content-Type": "application/json" },
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

export const getKnowledgeBaseName = (dataset: Dataset) =>
  dataset.display_name || dataset.name || dataset.dataset_id || t("selfEvolutionRun.unnamedKnowledgeBase");

export const isCanceledRequest = (error: unknown) => {
  const normalizedError = error as {
    code?: string;
    name?: string;
    config?: { signal?: AbortSignal };
    message?: string;
  };
  const messageText = normalizedError?.message?.toLowerCase() || "";

  return (
    normalizedError?.code === "ERR_CANCELED" ||
    normalizedError?.name === "CanceledError" ||
    normalizedError?.config?.signal?.aborted ||
    messageText.includes("canceled") ||
    messageText.includes("cancelled") ||
    messageText.includes("aborted")
  );
};
