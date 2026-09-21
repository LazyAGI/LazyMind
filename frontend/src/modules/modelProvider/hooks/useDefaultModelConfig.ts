import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Modal, message } from "antd";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import { useModelFeatures } from "@/hooks/useModelFeatures";
import {
  getCloudSession,
  isCloudBusinessAvailable,
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT,
} from "@/runtime/cloud/session";
import {
  modelProvidersApi,
  modelProvidersDefaultApi,
  unwrapModelProviderData,
  withModelProviderJsonOptions,
} from "../api";
import { getProviderLogoUrl } from "../providerBranding";

export type SetupAvailabilityState = "loading" | "ready" | "empty" | "error";

export interface UseDefaultModelConfigOptions {
  cloudServiceSetupStates: Record<CloudServiceSlotKey, SetupAvailabilityState>;
  modelProviderSetupState: SetupAvailabilityState;
  onModelSelectionChanged: () => void | Promise<void>;
  highlightTarget?: ModelCapability;
  onHighlightResolved?: () => void;
}

export type ModelCapability =
  | "llm"
  | "embed_main"
  | "vlm"
  | "reranker"
  | "speech_to_text"
  | "tts"
  | "image_generator"
  | "video_generator"
  | "embed_image"
  | "image_editor"
  | "evo_llm";

interface ProviderModel {
  id: string;
  name: string;
  capability: ModelCapability;
  builtIn: boolean;
  enabled: boolean;
  maxInputTokens?: string;
  availability?: "available" | "degraded" | "unavailable";
  lifecycle?: "active" | "deprecated" | "retired";
  readOnly?: boolean;
}

export interface ProviderOption {
  id: string;
  name: string;
  brand: string;
  logoUrl?: string;
  headline: string;
  backendDescription?: string;
  source: string;
  baseUrl: string;
  capabilities: ModelCapability[];
  models: ProviderModel[];
}

interface ProviderConnectionGroup {
  id: string;
  name: string;
  source: string;
  baseUrl: string;
  apiKeyConfigured: boolean;
  verified: boolean;
  models: ProviderModel[];
}

interface ModuleConfig {
  key: ModelCapability;
  titleKey: string;
  subtitleKey: string;
  required?: boolean;
  restricted?: boolean;
}

interface ApiProvider {
  id: string;
  name: string;
  description?: string;
  base_url?: string;
}

interface ApiModel {
  id: string;
  is_editable?: boolean;
  name: string;
  model_type?: string;
  is_default?: boolean;
  max_input_tokens?: string;
  source?: "own" | "cloud";
  provider_id?: string;
  provider_group_id?: string;
  user_model_provider_id?: string;
  user_model_provider_group_id?: string;
  provider_name?: string;
  group_name?: string;
  base_url?: string;
  availability?: "available" | "degraded" | "unavailable";
  lifecycle?: "active" | "deprecated" | "retired";
  read_only?: boolean;
  capabilities?: string[];
}

interface SelectedModelApiItem {
  base_url?: string;
  group_name: string;
  is_default?: boolean;
  is_editable?: boolean;
  model_id: string;
  model_key: string;
  name: string;
  provider_name: string;
  share?: boolean;
  user_model_provider_group_id?: string;
  user_model_provider_id?: string;
  source?: "own" | "cloud";
  provider_id?: string;
  provider_group_id?: string;
  availability?: "available" | "degraded" | "unavailable";
  unavailable_reason?: string;
  read_only?: boolean;
  max_input_tokens?: string;
}

type SelectedModels = Partial<Record<ModelCapability, string>>;
type SelectedModelMaxInputTokens = Partial<
  Record<ModelCapability, string>
>;

export type CloudServiceSlotKey = "cloudParsing" | "searchEngine";
export type CloudServiceCategory = "ocr" | "search";

type SelectedCloudServices = Partial<Record<CloudServiceSlotKey, string>>;
type CloudServiceCategoryBySlot = Record<
  CloudServiceSlotKey,
  CloudServiceCategory
>;

type ModelOptionItem = {
  provider: ProviderOption;
  group: ProviderConnectionGroup;
  model: ProviderModel;
  value: string;
  source: "own" | "cloud";
  /** True when the option comes from an image_editing catalog model. */
  isEditable?: boolean;
};

interface CloudServiceConfig {
  setupActionKey: string;
  setupDescriptionKey: string;
  setupEmptyKey: string;
  key: CloudServiceSlotKey;
  titleKey: string;
  subtitleKey: string;
  category: CloudServiceCategory;
}

interface CloudServiceOption {
  baseUrl: string;
  groupId: string;
  groupName: string;
  providerName: string;
}

interface VerifiedCloudServiceGroup {
  base_url: string;
  category: string;
  group_id: string;
  group_name: string;
  provider_name: string;
  source?: string;
  user_model_provider_id: string;
}

export interface VerifiedCloudServiceResponse {
  groups?: VerifiedCloudServiceGroup[];
  ready: boolean;
  source?: string;
  shared_by_name?: string;
  shared_by_id?: string;
  provider_name?: string;
  group_name?: string;
}

interface CloudServiceGroupListResponse {
  groups?: VerifiedCloudServiceGroup[];
}

interface SelectedCloudServiceApiItem {
  base_url?: string;
  category: CloudServiceCategory;
  group_id: string;
  group_name: string;
  provider_name: string;
  share?: boolean;
  user_model_provider_id: string;
}

export interface ModelReadyResponse {
  ready: boolean;
  source?: string;
	fallback_from?: string;
  reason?: string;
  shared_by_name?: string;
  shared_by_id?: string;
  provider_name?: string;
  model_name?: string;
}

type CapabilityKey = ModelCapability | CloudServiceSlotKey;
type CapabilityReadyStates = Partial<Record<CapabilityKey, "loading" | "ready" | "error">>;

type ModelReadyStatus = Partial<Record<ModelCapability, ModelReadyResponse>>;
type CloudServiceReadyStatus = Partial<
  Record<CloudServiceSlotKey, VerifiedCloudServiceResponse>
>;

const moduleConfigs: ModuleConfig[] = [
  {
    key: "llm",
    titleKey: "modelProvider.module.llmChatTitle",
    subtitleKey: "modelProvider.module.llmChatSubtitle",
    required: true,
  },
  {
    key: "embed_main",
    titleKey: "modelProvider.module.embeddingTitle",
    subtitleKey: "modelProvider.module.embeddingSubtitle",
    required: true,
    restricted: true,
  },
  {
    key: "embed_image",
    titleKey: "modelProvider.module.multimodalEmbeddingTitle",
    subtitleKey: "modelProvider.module.multimodalEmbeddingSubtitle",
    restricted: true,
  },
  {
    key: "vlm",
    titleKey: "modelProvider.module.vlmTitle",
    subtitleKey: "modelProvider.module.vlmSubtitle",
  },
  {
    key: "reranker",
    titleKey: "modelProvider.module.rerankTitle",
    subtitleKey: "modelProvider.module.rerankSubtitle",
  },
  {
    key: "speech_to_text",
    titleKey: "modelProvider.module.asrTitle",
    subtitleKey: "modelProvider.module.asrSubtitle",
  },
  {
    key: "tts",
    titleKey: "modelProvider.module.ttsTitle",
    subtitleKey: "modelProvider.module.ttsSubtitle",
  },
  {
    key: "image_generator",
    titleKey: "modelProvider.module.textToImageTitle",
    subtitleKey: "modelProvider.module.textToImageSubtitle",
  },
  {
    key: "video_generator",
    titleKey: "modelProvider.module.textToVideoTitle",
    subtitleKey: "modelProvider.module.textToVideoSubtitle",
  },
  {
    key: "evo_llm",
    titleKey: "modelProvider.module.selfEvolutionTitle",
    subtitleKey: "modelProvider.module.selfEvolutionSubtitle",
    required: true,
  },
];

export const cloudServiceConfigs: CloudServiceConfig[] = [
  {
    key: "cloudParsing",
    setupActionKey: "modelProvider.cloudParsingSetupAction",
    setupDescriptionKey: "modelProvider.cloudParsingSetupDescription",
    setupEmptyKey: "modelProvider.cloudParsingSetupEmpty",
    titleKey: "modelProvider.module.cloudParsingServiceTitle",
    subtitleKey: "modelProvider.module.cloudParsingServiceSubtitle",
    category: "ocr",
  },
  {
    key: "searchEngine",
    setupActionKey: "modelProvider.searchEngineSetupAction",
    setupDescriptionKey: "modelProvider.searchEngineSetupDescription",
    setupEmptyKey: "modelProvider.searchEngineSetupEmpty",
    titleKey: "modelProvider.module.searchEngineServiceTitle",
    subtitleKey: "modelProvider.module.searchEngineServiceSubtitle",
    category: "search",
  },
];

const cloudServiceCategoryBySlot = cloudServiceConfigs.reduce(
  (acc, service) => {
    acc[service.key] = service.category;
    return acc;
  },
  {} as CloudServiceCategoryBySlot,
);

const selectedCapabilityByModelType: Record<string, ModelCapability> = {
  evo_llm: "evo_llm",
  stt: "speech_to_text",
  text2image: "image_generator",
  text2video: "video_generator",
  image_editing: "image_editor",
};

export function normalizeProviderKey(value: string) {
  return (
    value
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-") || "provider"
  );
}

function getProviderBrand(name: string) {
  const trimmed = name.trim();
  if (!trimmed) return "AI";
  if (/openai/i.test(trimmed)) return "◎";
  return trimmed
    .split(/[\s-]+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

function createConnectionGroup(
  provider: ProviderOption,
  overrides: Partial<ProviderConnectionGroup> = {},
): ProviderConnectionGroup {
  return {
    id: overrides.id || `${provider.id}-default`,
    name: overrides.name || provider.name,
    source: provider.source,
    baseUrl: overrides.baseUrl || provider.baseUrl,
    apiKeyConfigured: overrides.apiKeyConfigured ?? false,
    verified: overrides.verified ?? false,
    models: overrides.models || provider.models.map((model) => ({ ...model })),
  };
}

function getModelValue(
  source: "own" | "cloud",
  providerId: string,
  groupId: string,
  modelId: string,
) {
  return `${source}:${providerId}:${groupId}:${modelId}`;
}

function parseModelValue(value?: string) {
  const [source, providerId, groupId, ...modelIdParts] = String(value || "").split(":");
  return {
    source: source === "cloud" ? "cloud" as const : "own" as const,
    providerId,
    groupId,
    modelId: modelIdParts.join(":"),
  };
}

function getCapabilityByModelType(
  modelType?: string,
): ModelCapability | undefined {
  const normalized = (modelType || "").toLowerCase();
  const selectedCapability = selectedCapabilityByModelType[normalized];
  if (selectedCapability) {
    return selectedCapability;
  }
  return moduleConfigs.find((module) => module.key === normalized)?.key;
}

function getModelTypeByCapability(capability: ModelCapability): string {
  const entry = Object.entries(selectedCapabilityByModelType).find(
    ([, cap]) => cap === capability,
  );
  return entry ? entry[0] : capability;
}

const createModelProviderFallbacks = (
  t: ReturnType<typeof useTranslation>["t"],
) => ({
  providerDescription: t("modelProvider.providerDescriptionFallback"),
  providerDescriptions: {
    claude: t("modelProvider.providerDescriptions.claude", {
      defaultValue: "",
    }),
    deepseek: t("modelProvider.providerDescriptions.deepseek", {
      defaultValue: "",
    }),
    doubao: t("modelProvider.providerDescriptions.doubao", {
      defaultValue: "",
    }),
    glm: t("modelProvider.providerDescriptions.glm", { defaultValue: "" }),
    kimi: t("modelProvider.providerDescriptions.kimi", { defaultValue: "" }),
    minimax: t("modelProvider.providerDescriptions.minimax", {
      defaultValue: "",
    }),
    openai: t("modelProvider.providerDescriptions.openai", {
      defaultValue: "",
    }),
    openrouter: t("modelProvider.providerDescriptions.openrouter", {
      defaultValue: "",
    }),
    qwen: t("modelProvider.providerDescriptions.qwen", { defaultValue: "" }),
    sensenova: t("modelProvider.providerDescriptions.sensenova", {
      defaultValue: "",
    }),
    siliconflow: t("modelProvider.providerDescriptions.siliconflow", {
      defaultValue: "",
    }),
  } as Record<string, string>,
});

type ModelProviderFallbacks = ReturnType<typeof createModelProviderFallbacks>;

function getLocalizedProviderDescription(
  name: string,
  fallbackDescription: string | undefined,
  fallbacks: ModelProviderFallbacks,
) {
  const providerKey = normalizeProviderKey(name).replace(/-/g, "");
  const translatedDescription = fallbacks.providerDescriptions[providerKey];
  return (
    fallbackDescription ||
    translatedDescription ||
    fallbacks.providerDescription
  );
}

function mapApiProvider(
  provider: ApiProvider,
  fallbacks: ModelProviderFallbacks,
): ProviderOption {
  const backendDescription = provider.description;

  return {
    id: provider.id,
    name: provider.name,
    brand: getProviderBrand(provider.name),
    logoUrl: getProviderLogoUrl(provider.name),
    headline: getLocalizedProviderDescription(
      provider.name,
      backendDescription,
      fallbacks,
    ),
    backendDescription,
    source: provider.name,
    baseUrl: provider.base_url || "",
    capabilities: [],
    models: [],
  };
}

function mapVerifiedCloudServiceGroup(
  group: VerifiedCloudServiceGroup,
): CloudServiceOption {
  return {
    baseUrl: group.base_url,
    groupId: group.group_id,
    groupName: group.group_name,
    providerName: group.provider_name,
  };
}

function mergeCloudServiceOptions(
  options: CloudServiceOption[],
  nextOption: CloudServiceOption,
) {
  if (options.some((option) => option.groupId === nextOption.groupId)) {
    return options;
  }
  return [nextOption, ...options];
}

async function fetchCapabilityReadiness(capability: CapabilityKey) {
  const options = { silentError: true };
  if (capability === "cloudParsing" || capability === "searchEngine") {
    const response = await modelProvidersApi.apiCoreModelProvidersVerifiedGet({
      category: cloudServiceCategoryBySlot[capability],
    }, options as never);
    return unwrapModelProviderData<VerifiedCloudServiceResponse>(response.data);
  }
  const response = await modelProvidersDefaultApi.apiCoreModelProvidersModelsReadyGet(
    withModelProviderJsonOptions({ ...options, params: { model_type: getModelTypeByCapability(capability) } }),
  );
  return unwrapModelProviderData<ModelReadyResponse>(response.data as unknown);
}

export function useDefaultModelConfig({
  cloudServiceSetupStates,
  modelProviderSetupState,
  onModelSelectionChanged,
  highlightTarget,
  onHighlightResolved,
}: UseDefaultModelConfigOptions) {
  const { t, i18n } = useTranslation();
  const currentLanguage = i18n.resolvedLanguage || i18n.language || "zh-CN";
  const [providerOptions, setProviderOptions] = useState<ProviderOption[]>([]);
  const [selectedModels, setSelectedModels] = useState<SelectedModels>({});
  const [selectedModelMaxInputTokens, setSelectedModelMaxInputTokens] =
    useState<SelectedModelMaxInputTokens>({});
  const [selectedCloudServices, setSelectedCloudServices] =
    useState<SelectedCloudServices>({});
  const [cloudServiceShareStatus, setCloudServiceShareStatus] = useState<
    Partial<Record<CloudServiceSlotKey, boolean>>
  >({});
  const [cloudServiceOptions, setCloudServiceOptions] = useState<
    Partial<Record<CloudServiceSlotKey, CloudServiceOption[]>>
  >({});
  const [cloudServiceOptionStates, setCloudServiceOptionStates] = useState<
    Partial<Record<CloudServiceSlotKey, SetupAvailabilityState>>
  >({});
  const [cloudServiceReadyStatus, setCloudServiceReadyStatus] =
    useState<CloudServiceReadyStatus>({});
  const [moduleModelOptions, setModuleModelOptions] = useState<
    Partial<Record<ModelCapability, ModelOptionItem[]>>
  >({});
  const [moduleModelOptionStates, setModuleModelOptionStates] = useState<
    Partial<Record<ModelCapability, SetupAvailabilityState>>
  >({});
  const [shareStatus, setShareStatus] = useState<
    Partial<Record<ModelCapability, boolean>>
  >({});
  const [modelReadyStatus, setModelReadyStatus] = useState<ModelReadyStatus>(
    {},
  );
  const [lazyMindCloudAvailable, setLazyMindCloudAvailable] = useState(false);
  const [defaultLoadState, setDefaultLoadState] = useState<"loading" | "ready" | "error">("loading");
  const [savingCapabilities, setSavingCapabilities] = useState<Set<ModelCapability | CloudServiceSlotKey>>(new Set());
  const savingCapabilitiesRef = useRef(new Set<ModelCapability | CloudServiceSlotKey>());
  const [capabilityReadyStates, setCapabilityReadyStates] = useState<CapabilityReadyStates>({});
  const readinessRequests = useRef(new Map<CapabilityKey, symbol>());
  const loadRevision = useRef(0);
  const optionRevision = useRef(0);
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === "system-admin";
  const modelFeaturesState = useModelFeatures();
  const imageEmbedEnabled =
    modelFeaturesState.status !== "ready" ||
    modelFeaturesState.features.image_embed_enabled;
  const visibleModuleConfigs = useMemo(
    () =>
      moduleConfigs.filter(
        (module) => module.key !== "embed_image" || imageEmbedEnabled,
      ),
    [imageEmbedEnabled],
  );

  const localizedFallbacks = useMemo(
    () => createModelProviderFallbacks(t),
    [currentLanguage, t],
  );

  useEffect(() => {
    let cancelled = false;
    const refreshSession = () => {
      void getCloudSession()
        .then((session) => {
		  if (!cancelled) setLazyMindCloudAvailable(isCloudBusinessAvailable(session));
        })
        .catch(() => {
          if (!cancelled) setLazyMindCloudAvailable(false);
        });
    };
    const refreshWhenVisible = () => {
      if (document.visibilityState === "visible") refreshSession();
    };
	const refreshCloudSession = () => {
	  setLazyMindCloudAvailable(false);
	  refreshSession();
	};
    refreshSession();
    window.addEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, refreshCloudSession);
    window.addEventListener("focus", refreshSession);
    document.addEventListener("visibilitychange", refreshWhenVisible);
    return () => {
      cancelled = true;
      window.removeEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, refreshCloudSession);
      window.removeEventListener("focus", refreshSession);
      document.removeEventListener("visibilitychange", refreshWhenVisible);
    };
  }, []);

  const loadDefaultModelState = useCallback(async () => {
    if (savingCapabilitiesRef.current.size) return;
    const revision = ++loadRevision.current;
    readinessRequests.current.clear();
    try {
      const providerResponse = await modelProvidersApi.apiCoreModelProvidersGet();
      const providerData = unwrapModelProviderData<{ providers?: ApiProvider[] }>(providerResponse.data);
      const providers = (providerData.providers || []).map((provider) =>
        mapApiProvider(provider, localizedFallbacks),
      );

      const selectedResponse = await modelProvidersApi.apiCoreModelProvidersSelectedModelsGet();
      const selectedData = unwrapModelProviderData<{ selections?: SelectedModelApiItem[] }>(selectedResponse.data);
      const nextSelectedModels: SelectedModels = {};
      const nextSelectedModelMaxInputTokens: SelectedModelMaxInputTokens = {};
      const selectedOptions: Partial<
        Record<ModelCapability, ModelOptionItem[]>
      > = {};

      (selectedData.selections || []).forEach((selection) => {
        const rawCapability = getCapabilityByModelType(selection.model_key);
        if (!rawCapability) {
          return;
        }
        // Unify image_editing into the 文生图 slot; prefer editable when both exist.
        const capability: ModelCapability =
          rawCapability === "image_editor" ? "image_generator" : rawCapability;
        const isEditable = !!selection.is_editable;
        if (
          capability === "image_generator" &&
          !isEditable &&
          nextSelectedModels.image_generator &&
          selectedOptions.image_generator?.some((item) => item.isEditable)
        ) {
          return;
        }
        const source = selection.source === "cloud" ? "cloud" : "own";
        const providerId =
          selection.provider_id ||
          selection.user_model_provider_id ||
          (source === "cloud" ? "lazymind-cloud" : "");
        const groupId =
          selection.provider_group_id ||
          selection.user_model_provider_group_id ||
          (source === "cloud" ? "cloud-system" : "");
        const provider =
          providers.find(
            (item) => item.id === providerId,
          ) ||
          mapApiProvider(
            {
              id: providerId,
              name: selection.provider_name,
              base_url: selection.base_url,
            },
            localizedFallbacks,
          );
        const group = createConnectionGroup(provider, {
          id: groupId,
          name: selection.group_name || (source === "cloud" ? "" : provider.name),
          baseUrl: selection.base_url || provider.baseUrl,
          apiKeyConfigured: true,
          verified: true,
        });
        const model: ProviderModel = {
          id: selection.model_id,
          name: selection.name,
          capability,
          builtIn: Boolean(selection.is_default),
          enabled: true,
          maxInputTokens: selection.max_input_tokens,
          availability: selection.availability,
          readOnly: selection.read_only,
        };
        const option: ModelOptionItem = {
          provider,
          group,
          model,
          source,
          value: getModelValue(source, provider.id, group.id, model.id),
          isEditable,
        };
        nextSelectedModels[capability] = option.value;
        if (selection.max_input_tokens?.trim()) {
          nextSelectedModelMaxInputTokens[capability] =
            selection.max_input_tokens;
        }
        selectedOptions[capability] = [
          option,
          ...(selectedOptions[capability] || []).filter(
            (item) => item.value !== option.value,
          ),
        ];
      });

      const nextShareStatus: Partial<Record<ModelCapability, boolean>> = {};
      (selectedData.selections || []).forEach((selection) => {
        const rawCapability = getCapabilityByModelType(selection.model_key);
        if (!rawCapability) {
          return;
        }
        const capability: ModelCapability =
          rawCapability === "image_editor" ? "image_generator" : rawCapability;
        // Prefer share status from image_editing when both image roles are set.
        if (
          capability === "image_generator" &&
          selection.model_key === "text2image" &&
          nextShareStatus.image_generator !== undefined
        ) {
          return;
        }
        nextShareStatus[capability] =
          selection.source === "cloud" ? false : !!selection.share;
      });

      const selectedProviderResponse = await modelProvidersApi.apiCoreModelProvidersSelectedProvidersGet();
      const selectedProviderData = unwrapModelProviderData<{ selections?: SelectedCloudServiceApiItem[] }>(
        selectedProviderResponse.data as unknown,
      );
      const nextSelectedCloudServices: SelectedCloudServices = {};
      const nextCloudShareStatus: Partial<
        Record<CloudServiceSlotKey, boolean>
      > = {};
      const selectedCloudOptions: Partial<
        Record<CloudServiceSlotKey, CloudServiceOption[]>
      > = {};
      (selectedProviderData.selections || []).forEach((selection) => {
        const service = cloudServiceConfigs.find(
          (item) => item.category === selection.category,
        );
        if (!service) {
          return;
        }
        nextSelectedCloudServices[service.key] = selection.group_id;
        nextCloudShareStatus[service.key] = !!selection.share;
        selectedCloudOptions[service.key] = mergeCloudServiceOptions(
          selectedCloudOptions[service.key] || [],
          {
            baseUrl: selection.base_url || "",
            groupId: selection.group_id,
            groupName: selection.group_name,
            providerName: selection.provider_name,
          },
        );
      });
      const nextReadyStatus: ModelReadyStatus = {};
      const nextCloudReadyStatus: CloudServiceReadyStatus = {};
      const nextReadyStates: CapabilityReadyStates = {};
      if (!isAdmin) {
        const capabilities = [...visibleModuleConfigs, ...cloudServiceConfigs].map((item) => item.key);
        const results = await Promise.allSettled(capabilities.map(fetchCapabilityReadiness));
        results.forEach((result, index) => {
          const capability = capabilities[index];
          nextReadyStates[capability] = result.status === "fulfilled" ? "ready" : "error";
          if (result.status === "fulfilled") {
            if (capability === "cloudParsing" || capability === "searchEngine") {
              nextCloudReadyStatus[capability] = result.value;
            } else {
              nextReadyStatus[capability] = result.value;
            }
          }
        });
      }
      if (revision !== loadRevision.current) return;
      setProviderOptions(providers);
      setSelectedModels(nextSelectedModels);
      setSelectedModelMaxInputTokens(nextSelectedModelMaxInputTokens);
      optionRevision.current += 1;
      setModuleModelOptions(selectedOptions);
      setModuleModelOptionStates({});
      setShareStatus(nextShareStatus);
      setSelectedCloudServices(nextSelectedCloudServices);
      setCloudServiceShareStatus(nextCloudShareStatus);
      setCloudServiceOptions(selectedCloudOptions);
      setCloudServiceOptionStates({});
      // Keep the last known shared configuration on a failed check, but show
      // its error state instead of presenting stale readiness as current.
      setModelReadyStatus((current) => isAdmin ? {} : { ...current, ...nextReadyStatus });
      setCloudServiceReadyStatus((current) => isAdmin ? {} : { ...current, ...nextCloudReadyStatus });
      setCapabilityReadyStates(nextReadyStates);
      setDefaultLoadState("ready");
    } catch {
      if (revision === loadRevision.current) setDefaultLoadState("error");
    }
  }, [currentLanguage, isAdmin, localizedFallbacks, t, visibleModuleConfigs]);

  useEffect(() => {
    void loadDefaultModelState();
    return () => { loadRevision.current += 1; optionRevision.current += 1; readinessRequests.current.clear(); };
  }, [loadDefaultModelState]);

  useEffect(() => {
    const refreshModels = () => void loadDefaultModelState();
    const refreshWhenVisible = () => {
      if (document.visibilityState === "visible") refreshModels();
    };
    window.addEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, refreshModels);
    window.addEventListener("focus", refreshModels);
    document.addEventListener("visibilitychange", refreshWhenVisible);
    return () => {
      window.removeEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, refreshModels);
      window.removeEventListener("focus", refreshModels);
      document.removeEventListener("visibilitychange", refreshWhenVisible);
    };
  }, [loadDefaultModelState]);

  const loadModuleModels = async (
    capability: ModelCapability,
    force = false,
  ) => {
    if (!force && moduleModelOptionStates[capability] === "ready") {
      return;
    }
    if (moduleModelOptionStates[capability] === "loading") {
      return;
    }

    const revision = optionRevision.current;
    setModuleModelOptionStates((current) => ({ ...current, [capability]: "loading" }));
    try {
      const modelTypes =
        capability === "image_generator"
          ? ["text2image", "image_editing"]
          : [getModelTypeByCapability(capability)];

      const fetchedLists = await Promise.all(
        modelTypes.map(async (modelType) => {
          const response = await modelProvidersApi.apiCoreModelProvidersModelsGet({
            modelType,
          });
          const data = unwrapModelProviderData<{ models?: ApiModel[] }>(
            response.data,
          );
          return data.models || [];
        }),
      );

      const fetchedOptions: ModelOptionItem[] = [];
      const seenValues = new Set<string>();
      fetchedLists.forEach((models) => {
        models
          .forEach((model) => {
            const source = model.source === "cloud" ? "cloud" : "own";
            const providerId =
              model.provider_id ||
              model.user_model_provider_id ||
              (source === "cloud" ? "lazymind-cloud" : "");
            const groupId =
              model.provider_group_id ||
              model.user_model_provider_group_id ||
              (source === "cloud" ? "cloud-system" : "");
            const provider =
              providerOptions.find(
                (item) => item.id === providerId,
              ) ||
              mapApiProvider(
                {
                  id: providerId,
                  name: model.provider_name || "LazyMind Cloud",
                  base_url: model.base_url,
                },
                localizedFallbacks,
              );
            const group = createConnectionGroup(provider, {
              id: groupId,
              name: model.group_name || (source === "cloud" ? "" : provider.name),
              baseUrl: model.base_url || provider.baseUrl,
              verified: true,
            });
            const providerModel: ProviderModel = {
              id: model.id,
              name: model.name,
              capability,
              builtIn: Boolean(model.is_default),
              enabled: true,
              maxInputTokens: model.max_input_tokens,
              availability: model.availability,
              lifecycle: model.lifecycle,
              readOnly: model.read_only,
            };
            const value = getModelValue(
              source,
              provider.id,
              group.id,
              providerModel.id,
            );
            if (seenValues.has(value)) {
              // Prefer the editable entry when the same model id appears twice.
              if (model.is_editable) {
                const index = fetchedOptions.findIndex(
                  (item) => item.value === value,
                );
                if (index >= 0) {
                  fetchedOptions[index] = {
                    ...fetchedOptions[index],
                    isEditable: true,
                  };
                }
              }
              return;
            }
            seenValues.add(value);
            fetchedOptions.push({
              provider,
              group,
              model: providerModel,
              value,
              source,
              isEditable: !!model.is_editable,
            });
          });
      });

      const selectedValue = selectedModels[capability];
      const selectedOption =
        selectedValue &&
        (moduleModelOptions[capability] || []).find(
          (option) => option.value === selectedValue,
        );
      const options =
        selectedOption &&
        !fetchedOptions.some((option) => option.value === selectedOption.value)
          ? [selectedOption, ...fetchedOptions]
          : fetchedOptions;

      if (revision !== optionRevision.current) return;
      setModuleModelOptions((current) => ({ ...current, [capability]: options }));
      setModuleModelOptionStates((current) => ({ ...current, [capability]: "ready" }));
    } catch {
      if (revision === optionRevision.current) {
        setModuleModelOptionStates((current) => ({ ...current, [capability]: "error" }));
      }
    }
  };

  const saveSelectedModel = async (
    capability: ModelCapability,
    value?: string,
  ) => {
    const parsed = value ? parseModelValue(value) : undefined;
    const modelId = parsed?.modelId || "";
    const source = parsed?.source || "own";
    const selectionItem = (modelKey: string, id: string) => ({
      model_key: modelKey,
      model_id: id,
      ...(id ? { source } : {}),
    });
    const selections =
      capability === "image_generator"
        ? (() => {
            const selectedOption = value
              ? moduleModelOptions.image_generator?.find(
                  (option) => option.value === value,
                )
              : undefined;
            const isEditable = !!selectedOption?.isEditable;
            if (!value) {
              return [
                selectionItem("text2image", ""),
                selectionItem("image_editing", ""),
              ];
            }
            if (isEditable) {
              if (source === "cloud") {
                return [
                  selectionItem("text2image", ""),
                  selectionItem("image_editing", modelId),
                ];
              }
              return [
                selectionItem("text2image", modelId),
                selectionItem("image_editing", modelId),
              ];
            }
            return [
              selectionItem("text2image", modelId),
              selectionItem("image_editing", ""),
            ];
          })()
        : [
            selectionItem(getModelTypeByCapability(capability), modelId),
          ];

    const response = await modelProvidersApi.apiCoreModelProvidersSelectedModelsPut({
      setSelectedModelsOpenAPIRequest: {
        selections,
      },
    });
    return unwrapModelProviderData<{ selections?: SelectedModelApiItem[] }>(response.data);
  };

  const toggleShareModel = async (
    capability: ModelCapability,
    share: boolean,
  ) => {
    const value = selectedModels[capability];
    if (!value) {
      if (!share) {
        setShareStatus((current) => ({ ...current, [capability]: false }));
        return;
      }
      message.warning(t("modelProvider.noModelSelectedForShare"));
      return;
    }
    if (parseModelValue(value).source === "cloud") {
      message.warning(t("modelProvider.cloudSystemCannotShare"));
      return;
    }

    try {
      await modelProvidersDefaultApi.apiCoreModelProvidersSelectedModelsSharePut(
        withModelProviderJsonOptions({
          data: {
            model_id: parseModelValue(value).modelId,
            model_key: getModelTypeByCapability(capability),
            share,
          },
        }),
      );
      setShareStatus((current) => ({ ...current, [capability]: share }));
      message.success(
        share
          ? t("modelProvider.shareEnabled")
          : t("modelProvider.shareDisabled"),
      );
    } catch {
    }
  };

  const setCapabilitySaving = (capability: ModelCapability | CloudServiceSlotKey, saving: boolean) => {
    loadRevision.current += 1;
    if (saving) savingCapabilitiesRef.current.add(capability);
    else savingCapabilitiesRef.current.delete(capability);
    setSavingCapabilities(new Set(savingCapabilitiesRef.current));
  };

  const applyModelSelection = (capability: ModelCapability, value?: string) => {
    if (savingCapabilitiesRef.current.has(capability)) return;
    const maxInputTokens = value
      ? moduleModelOptions[capability]?.find(
          (option) => option.value === value,
        )?.model.maxInputTokens
      : undefined;
    setCapabilitySaving(capability, true);
    void saveSelectedModel(capability, value)
      .then((response) => {
        const savedSelection = value ? response.selections?.find((selection) => {
          const rawCapability = getCapabilityByModelType(selection.model_key);
          const selectedCapability = rawCapability === "image_editor" ? "image_generator" : rawCapability;
          return selectedCapability === capability && selection.model_id === parseModelValue(value).modelId;
        }) : undefined;
        const savedValue = savedSelection && savedSelection.availability !== "unavailable" ? value : undefined;
        setSelectedModels((current) => ({ ...current, [capability]: savedValue }));
        setSelectedModelMaxInputTokens((current) => ({
          ...current,
          [capability]: savedValue ? (savedSelection?.max_input_tokens || maxInputTokens)?.trim() : undefined,
        }));
        setShareStatus((current) => ({
          ...current,
          [capability]: Boolean(savedValue && savedSelection?.source !== "cloud" && savedSelection?.share),
        }));
        if (savedValue && capability === highlightTarget) onHighlightResolved?.();
        void onModelSelectionChanged();
        if (!isAdmin) return retryCapabilityReadiness(capability, true);
      })
      .catch(() => {})
      .finally(() => setCapabilitySaving(capability, false));
  };

  const handleModelSelection = (
    capability: ModelCapability,
    value?: string,
  ) => {
    const previousValue = selectedModels[capability];
    if (
      capability === "embed_main" &&
      previousValue &&
      previousValue !== value &&
      shareStatus.embed_main === true
    ) {
      Modal.confirm({
        title: t("modelProvider.embeddingChangeTitle"),
        content: t("modelProvider.embeddingChangeContent"),
        okText: t("modelProvider.confirmSwitch"),
        cancelText: t("modelProvider.cancelSwitch"),
        okButtonProps: { danger: true },
        onOk: () => {
          applyModelSelection(capability, value);
        },
      });
      return;
    }

    applyModelSelection(capability, value);
  };

  const saveSelectedCloudService = async (
    service: CloudServiceSlotKey,
    value?: string,
  ) => {
    const response = await modelProvidersApi.apiCoreModelProvidersSelectedProvidersPut({
      setSelectedProviderOpenAPIRequest: {
        selections: [
          {
            category: cloudServiceCategoryBySlot[service],
            group_id: value || "",
          },
        ],
      },
    });
    return unwrapModelProviderData<{ selections?: SelectedCloudServiceApiItem[] }>(
      response.data as unknown,
    );
  };

  const loadVerifiedCloudService = async (
    service: CloudServiceConfig,
  ) => {
    if (cloudServiceOptionStates[service.key] === "loading") {
      return;
    }

    const revision = optionRevision.current;
    setCloudServiceOptionStates((current) => ({ ...current, [service.key]: "loading" }));
    try {
      const response = await modelProvidersApi.apiCoreModelProvidersProviderGroupsGet({
        category: service.category,
      });
      const data = unwrapModelProviderData<CloudServiceGroupListResponse>(response.data);
      const groups = data.groups || [];
      const fetchedOptions = groups.map((group) =>
        mapVerifiedCloudServiceGroup(group),
      );
      const currentSelectedGroupId = selectedCloudServices[service.key];
      const selectedOption =
        currentSelectedGroupId &&
        (cloudServiceOptions[service.key] || []).find(
          (option) => option.groupId === currentSelectedGroupId,
        );
      const options =
        selectedOption &&
        !fetchedOptions.some(
          (option) => option.groupId === selectedOption.groupId,
        )
          ? [selectedOption, ...fetchedOptions]
          : fetchedOptions;
      if (revision !== optionRevision.current) return;
      setCloudServiceOptions((current) => ({ ...current, [service.key]: options }));
      setCloudServiceOptionStates((current) => ({ ...current, [service.key]: "ready" }));
    } catch {
      if (revision === optionRevision.current) {
        setCloudServiceOptionStates((current) => ({ ...current, [service.key]: "error" }));
      }
    }
  };

  const handleCloudServiceSelection = (
    service: CloudServiceSlotKey,
    value?: string,
  ) => {
    if (savingCapabilitiesRef.current.has(service)) return;
    setCapabilitySaving(service, true);
    void saveSelectedCloudService(service, value)
      .then((response) => {
        const selection = response.selections?.find((item) => item.category === cloudServiceCategoryBySlot[service]);
        setSelectedCloudServices((current) => ({ ...current, [service]: selection?.group_id }));
        setCloudServiceShareStatus((current) => ({ ...current, [service]: !!selection?.share }));
        void onModelSelectionChanged();
        if (!isAdmin) return retryCapabilityReadiness(service, true);
      })
      .catch(() => {})
      .finally(() => setCapabilitySaving(service, false));
  };

  const toggleShareCloudService = (
    service: CloudServiceSlotKey,
    share: boolean,
  ) => {
    if (!selectedCloudServices[service]) {
      message.warning(t("modelProvider.noCloudServiceSelectedForShare"));
      return;
    }

    void modelProvidersApi
      .apiCoreModelProvidersSelectedProvidersSharePut({
        setSharedProviderOpenAPIRequest: {
          group_id: selectedCloudServices[service],
          share,
        },
      },
      )
      .then(() => {
        setCloudServiceShareStatus((current) => ({
          ...current,
          [service]: share,
        }));
        message.success(
          share
            ? t("modelProvider.shareEnabled")
            : t("modelProvider.shareDisabled"),
        );
      })
      .catch(() => {});
  };

  const isModelConfigured = (capability: ModelCapability) => {
    const selected = moduleModelOptions[capability]?.find((option) => option.value === selectedModels[capability]);
    const selectedAvailable = selected &&
      (selected.source !== "cloud" || lazyMindCloudAvailable) &&
      selected.model.availability !== "unavailable" &&
      selected.model.lifecycle !== "deprecated" && selected.model.lifecycle !== "retired";
    return Boolean(selectedAvailable || (!isAdmin && modelReadyStatus[capability]?.ready));
  };
  const isCloudServiceConfigured = (service: CloudServiceSlotKey) => Boolean(
    selectedCloudServices[service] || (!isAdmin && cloudServiceReadyStatus[service]?.ready),
  );
  useEffect(() => {
    if (defaultLoadState !== "ready") return;
    if (modelProviderSetupState === "ready") {
      visibleModuleConfigs.forEach((module) => {
        if (!isModelConfigured(module.key) && !moduleModelOptionStates[module.key]) {
          void loadModuleModels(module.key);
        }
      });
    }
    cloudServiceConfigs.forEach((service) => {
      if (cloudServiceSetupStates[service.key] === "ready" &&
          !isCloudServiceConfigured(service.key) && !cloudServiceOptionStates[service.key]) {
        void loadVerifiedCloudService(service);
      }
    });
  }, [defaultLoadState, modelProviderSetupState, cloudServiceSetupStates, visibleModuleConfigs,
    selectedModels, selectedCloudServices, moduleModelOptionStates, cloudServiceOptionStates, lazyMindCloudAvailable]);

  const retryCapabilityReadiness = async (capability: CapabilityKey, invalidate = false) => {
    if (invalidate) {
      // A saved selection invalidates both cached readiness and any pre-save retry.
      readinessRequests.current.delete(capability);
      if (capability === "cloudParsing" || capability === "searchEngine") {
        setCloudServiceReadyStatus((current) => ({ ...current, [capability]: undefined }));
      } else {
        setModelReadyStatus((current) => ({ ...current, [capability]: undefined }));
      }
    }
    if (readinessRequests.current.has(capability)) return;
    // A manual retry takes precedence over an older background refresh.
    loadRevision.current += 1;
    const request = Symbol();
    readinessRequests.current.set(capability, request);
    setCapabilityReadyStates((current) => ({ ...current, [capability]: "loading" }));
    try {
      const response = await fetchCapabilityReadiness(capability);
      if (readinessRequests.current.get(capability) !== request) return;
      if (capability === "cloudParsing" || capability === "searchEngine") {
        setCloudServiceReadyStatus((current) => ({ ...current, [capability]: response }));
      } else {
        setModelReadyStatus((current) => ({ ...current, [capability]: response }));
      }
      setCapabilityReadyStates((current) => ({ ...current, [capability]: "ready" }));
    } catch {
      if (readinessRequests.current.get(capability) === request) {
        setCapabilityReadyStates((current) => ({ ...current, [capability]: "error" }));
      }
    } finally {
      if (readinessRequests.current.get(capability) === request) readinessRequests.current.delete(capability);
    }
  };

  const retryDefaultModelState = () => {
    setDefaultLoadState("loading");
    void loadDefaultModelState();
  };

  return {
    visibleModuleConfigs,
    isAdmin,
    lazyMindCloudAvailable,
    defaultLoadState,
    savingCapabilities,
    selectedModels,
    selectedModelMaxInputTokens,
    moduleModelOptions,
    moduleModelOptionStates,
    modelReadyStatus,
    shareStatus,
    selectedCloudServices,
    cloudServiceOptions,
    cloudServiceOptionStates,
    cloudServiceReadyStatus,
    cloudServiceShareStatus,
    isModelConfigured,
    isCloudServiceConfigured,
    loadModuleModels,
    loadVerifiedCloudService,
    handleModelSelection,
    handleCloudServiceSelection,
    toggleShareModel,
    toggleShareCloudService,
    retryDefaultModelState,
    capabilityReadyStates,
    retryCapabilityReadiness,
  };
}
