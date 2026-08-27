import { useCallback, useEffect, useRef, useState } from "react";
import { modelProvidersApi, unwrapModelProviderData } from "../api";
import DefaultModelConfigPanel, {
  type CloudServiceSlotKey,
  type SetupAvailabilityState,
} from "../components/DefaultModelConfigPanel";

interface DefaultServicesPageProps {
  onConfigureCloudService: (service: CloudServiceSlotKey) => void;
  onConfigureProviders: () => void;
  onModelSelectionChanged: () => void | Promise<void>;
}

interface SetupAvailability {
  cloudParsing: SetupAvailabilityState;
  modelProvider: SetupAvailabilityState;
  searchEngine: SetupAvailabilityState;
}

const loadingSetupAvailability: SetupAvailability = {
  cloudParsing: "loading",
  modelProvider: "loading",
  searchEngine: "loading",
};

const configurableModelTypes = [
  "llm",
  "embed_main",
  "cross_modal_embed",
  "vlm",
  "reranker",
  "speech_to_text",
  "text2image",
  "image_editing",
  "text2video",
  "evo_llm",
];

const silentSetupCheckOptions = { silentError: true } as never;

async function listAvailableModels() {
  try {
    return await modelProvidersApi.apiCoreModelProvidersModelsGet(
      {},
      silentSetupCheckOptions,
    );
  } catch (error) {
    const results = await Promise.allSettled(
      configurableModelTypes.map((modelType) =>
        modelProvidersApi.apiCoreModelProvidersModelsGet(
          { modelType },
          silentSetupCheckOptions,
        ),
      ),
    );
    const fulfilled = results.filter((result) => result.status === "fulfilled");
    if (fulfilled.length === 0) throw error;
    return {
      data: {
        models: fulfilled.flatMap((result) =>
          unwrapModelProviderData<{ models?: unknown[] }>(result.value.data).models ?? [],
        ),
      },
    };
  }
}

export default function DefaultServicesPage({
  onConfigureCloudService,
  onConfigureProviders,
  onModelSelectionChanged,
}: DefaultServicesPageProps) {
  const [setupAvailability, setSetupAvailability] = useState<SetupAvailability>(loadingSetupAvailability);
  const latestRequest = useRef(0);

  const checkSetupAvailability = useCallback(async () => {
    const requestId = ++latestRequest.current;
    setSetupAvailability(loadingSetupAvailability);
    const [modelResult, parsingResult, searchResult] = await Promise.allSettled([
      listAvailableModels(),
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet(
        { category: "ocr" },
        silentSetupCheckOptions,
      ),
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet(
        { category: "search" },
        silentSetupCheckOptions,
      ),
    ]);
    if (requestId !== latestRequest.current) return;

    const modelProvider = modelResult.status === "fulfilled"
      ? (unwrapModelProviderData<{ models?: unknown[] }>(modelResult.value.data).models ?? []).length > 0 ? "ready" : "empty"
      : "error";
    const cloudParsing = parsingResult.status === "fulfilled"
      ? (unwrapModelProviderData<{ groups?: unknown[] }>(parsingResult.value.data).groups ?? []).length > 0 ? "ready" : "empty"
      : "error";
    const searchEngine = searchResult.status === "fulfilled"
      ? (unwrapModelProviderData<{ groups?: unknown[] }>(searchResult.value.data).groups ?? []).length > 0 ? "ready" : "empty"
      : "error";

    setSetupAvailability({ cloudParsing, modelProvider, searchEngine });
  }, []);

  useEffect(() => {
    void checkSetupAvailability();
    return () => {
      latestRequest.current += 1;
    };
  }, [checkSetupAvailability]);

  return (
    <div className="model-provider-service-page">
      <DefaultModelConfigPanel
        cloudServiceSetupStates={{
          cloudParsing: setupAvailability.cloudParsing,
          searchEngine: setupAvailability.searchEngine,
        }}
        modelProviderSetupState={setupAvailability.modelProvider}
        onConfigureCloudService={onConfigureCloudService}
        onConfigureProviders={onConfigureProviders}
        onModelSelectionChanged={onModelSelectionChanged}
        onRetrySetup={() => void checkSetupAvailability()}
      />
    </div>
  );
}
