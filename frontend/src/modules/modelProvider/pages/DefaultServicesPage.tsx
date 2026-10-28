import { useCallback, useEffect, useRef, useState } from "react";
import { Switch } from "antd";
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
  const latestRequest = useRef(0);
  const serviceArea = useRef<HTMLElement>(null);
  const focusObserver = useRef<MutationObserver | null>(null);
  const focusTimer = useRef<number | null>(null);

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

  const scrollToServiceCategory = useCallback((category: ServiceCategory) => {
    focusObserver.current?.disconnect();
    const container = serviceArea.current;
    if (!container) return;
    const scrollIfPresent = () => {
      const section = container.querySelector<HTMLElement>(`[data-service-category="${category}"]`);
      if (!section) return false;
      const reducedMotion = window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
      section.scrollIntoView?.({ behavior: reducedMotion ? "auto" : "smooth", block: "start" });
      return true;
    };
    if (scrollIfPresent() || typeof MutationObserver === "undefined") return;
    const observer = new MutationObserver(() => {
      if (scrollIfPresent()) observer.disconnect();
    });
    observer.observe(container, { childList: true, subtree: true });
    focusObserver.current = observer;
  }, []);

  useEffect(() => {
    setActiveFocusCategory(focusServiceCategory);
    if (focusServiceCategory) scrollToServiceCategory(focusServiceCategory);
  }, [focusServiceCategory, scrollToServiceCategory]);

  useEffect(() => () => {
    focusObserver.current?.disconnect();
    if (focusTimer.current !== null) window.clearTimeout(focusTimer.current);
  }, []);

  const handleConfigureCloudService = (service: CloudServiceSlotKey) => {
    const category = service === "cloudParsing" ? "parsing" : "search";
    onConfigureCloudService(service);
    if (focusTimer.current !== null) {
      window.clearTimeout(focusTimer.current);
      focusTimer.current = null;
    }
    if (activeFocusCategory === category) {
      // A second click on the same link should expand and focus the group again.
      setActiveFocusCategory(undefined);
      focusTimer.current = window.setTimeout(() => {
        setActiveFocusCategory(category);
        focusTimer.current = null;
      }, 0);
    } else {
      setActiveFocusCategory(category);
    }
    scrollToServiceCategory(category);
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
        onConfigureProviders={onConfigureProviders}
        onModelSelectionChanged={onModelSelectionChanged}
        onRetrySetup={() => void checkSetupAvailability()}
        highlightTarget={highlightTarget}
        onHighlightResolved={onHighlightResolved}
      />
      <section className="default-services-configuration" ref={serviceArea}>
        <ExternalServicesPage
          variant="default-models"
          visibleCategories={["parsing", "search", "academic", "translation"]}
          focusCategory={activeFocusCategory}
          parsingControl={parsingControl}
          onConfigurationChanged={handleServiceConfigurationChanged}
        />
      </section>
    </div>
  );
}
