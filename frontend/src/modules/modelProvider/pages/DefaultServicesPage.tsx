import { useCallback, useEffect, useRef, useState } from "react";
import { modelProvidersApi, unwrapModelProviderData } from "../api";
import DefaultModelConfigPanel, {
  type CloudServiceSlotKey,
  type SetupAvailabilityState,
} from "../components/DefaultModelConfigPanel";

interface DefaultServicesPageProps {
  onConfigureCloudService: (service: CloudServiceSlotKey) => void;
  onConfigureProviders: () => void;
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

export default function DefaultServicesPage({
  onConfigureCloudService,
  onConfigureProviders,
}: DefaultServicesPageProps) {
  const [setupAvailability, setSetupAvailability] = useState<SetupAvailability>(loadingSetupAvailability);
  const latestRequest = useRef(0);

  const checkSetupAvailability = useCallback(async () => {
    const requestId = ++latestRequest.current;
    setSetupAvailability(loadingSetupAvailability);
    const [modelResult, parsingResult, searchResult] = await Promise.allSettled([
      modelProvidersApi.apiCoreModelProvidersModelsGet({ modelType: "llm" }),
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet({ category: "ocr" }),
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet({ category: "search" }),
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
        onRetrySetup={() => void checkSetupAvailability()}
      />
    </div>
  );
}
