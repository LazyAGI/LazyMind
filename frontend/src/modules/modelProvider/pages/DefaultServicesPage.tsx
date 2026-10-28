import { useCallback, useEffect, useRef, useState } from "react";
import { Modal, Switch } from "antd";
import { useTranslation } from "react-i18next";
import { modelProvidersApi, unwrapModelProviderData } from "../api";
import DefaultModelConfigPanel, {
  type CloudServiceSlotKey,
  type ModelCapability,
  type SetupAvailabilityState,
} from "../components/DefaultModelConfigPanel";
import ExternalServicesPage from "./ExternalServicesPage";
import "./DefaultServicesPage.scss";

type ServiceCategory = "parsing" | "search" | "academic" | "translation";

interface DefaultServicesPageProps {
  onConfigureCloudService: (service: CloudServiceSlotKey) => void;
  onConfigureProviders: () => void;
  onModelSelectionChanged: () => void | Promise<void>;
  highlightTarget?: ModelCapability;
  onHighlightResolved?: () => void;
  focusServiceCategory?: ServiceCategory;
  documentParsingEnabled: boolean;
  documentParsingSaving: boolean;
  controlsDisabled: boolean;
  onDocumentParsingChange: (enabled: boolean) => void;
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
  highlightTarget,
  onHighlightResolved,
  focusServiceCategory,
  documentParsingEnabled,
  documentParsingSaving,
  controlsDisabled,
  onDocumentParsingChange,
}: DefaultServicesPageProps) {
  const { t } = useTranslation();
  const [setupAvailability, setSetupAvailability] = useState<SetupAvailability>(loadingSetupAvailability);
  const [selectionVersion, setSelectionVersion] = useState(0);
  const [activeFocusCategory, setActiveFocusCategory] = useState(focusServiceCategory);
  const [toolServiceSummaries, setToolServiceSummaries] = useState<Partial<Record<ServiceCategory, string>>>({});
  const latestRequest = useRef(0);

  const checkSetupAvailability = useCallback(async () => {
    const requestId = ++latestRequest.current;
    setSetupAvailability(loadingSetupAvailability);
    const [modelResult, parsingResult, searchResult, academicResult, translationResult] = await Promise.allSettled([
      listAvailableModels(),
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet(
        { category: "ocr" },
        silentSetupCheckOptions,
      ),
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet(
        { category: "search" },
        silentSetupCheckOptions,
      ),
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet({ category: "datasource" }, silentSetupCheckOptions),
      modelProvidersApi.apiCoreModelProvidersProviderGroupsGet({ category: "translation" }, silentSetupCheckOptions),
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
    const summaries: Partial<Record<ServiceCategory, string>> = {};
    for (const [category, result] of [["parsing", parsingResult], ["search", searchResult], ["academic", academicResult], ["translation", translationResult]] as const) {
      if (result.status !== "fulfilled") continue;
      const groups = unwrapModelProviderData<{ groups?: { name?: string; provider_name?: string }[] }>(result.value.data).groups || [];
      if (groups.length) summaries[category] = groups.map((group) => group.name || group.provider_name).filter(Boolean).join("、") || `已配置 ${groups.length} 个服务`;
    }
    setToolServiceSummaries(summaries);
  }, []);

  useEffect(() => {
    void checkSetupAvailability();
    return () => {
      latestRequest.current += 1;
    };
  }, [checkSetupAvailability]);

  useEffect(() => {
    setActiveFocusCategory(focusServiceCategory);
  }, [focusServiceCategory]);

  const handleConfigureCloudService = (service: CloudServiceSlotKey) => {
    onConfigureCloudService(service);
    setActiveFocusCategory(service === "cloudParsing" ? "parsing" : "search");
  };

  const handleServiceConfigurationChanged = () => {
    setSelectionVersion((current) => current + 1);
    void checkSetupAvailability();
    void onModelSelectionChanged();
  };

  const parsingControl = (
    <div className="default-services-parsing-control">
      <div className="default-services-parsing-copy">
        <strong>{t("settingsPage.knowledge.documentParsing")}</strong>
        <p>{t("settingsPage.knowledge.documentParsingGroupDesc")}</p>
      </div>
      <div className="default-services-parsing-action">
        <span>{t(documentParsingEnabled ? "settingsPage.enabled" : "settingsPage.disabled")}</span>
        <Switch
          aria-label={t("settingsPage.knowledge.documentParsingAria")}
          checked={documentParsingEnabled}
          disabled={controlsDisabled}
          loading={documentParsingSaving}
          onChange={onDocumentParsingChange}
        />
      </div>
    </div>
  );

  return (
    <div className="model-provider-service-page default-services-page">
      <DefaultModelConfigPanel
        key={selectionVersion}
        cloudServiceSetupStates={{
          cloudParsing: setupAvailability.cloudParsing,
          searchEngine: setupAvailability.searchEngine,
        }}
        modelProviderSetupState={setupAvailability.modelProvider}
        onConfigureCloudService={handleConfigureCloudService}
        onConfigureToolService={setActiveFocusCategory}
        toolServiceSummaries={toolServiceSummaries}
        onConfigureProviders={onConfigureProviders}
        onModelSelectionChanged={onModelSelectionChanged}
        onRetrySetup={() => void checkSetupAvailability()}
        highlightTarget={highlightTarget}
        onHighlightResolved={onHighlightResolved}
      />
      <Modal
        open={Boolean(activeFocusCategory)}
        title={activeFocusCategory ? ({ search: "搜索引擎", academic: "学术搜索引擎", parsing: "文档解析（OCR）", translation: "机器翻译" }[activeFocusCategory]) : ""}
        width={1000}
        footer={null}
        destroyOnHidden
        onCancel={() => setActiveFocusCategory(undefined)}
        className="default-services-modal"
      >
        {activeFocusCategory ? <ExternalServicesPage
          key={activeFocusCategory}
          variant="default-models"
          includeMcp={false}
          includeDependencies={false}
          includeBuiltinTools={false}
          visibleCategories={[activeFocusCategory]}
          focusCategory={activeFocusCategory}
          parsingControl={activeFocusCategory === "parsing" ? parsingControl : undefined}
          onConfigurationChanged={handleServiceConfigurationChanged}
        /> : null}
      </Modal>
    </div>
  );
}
