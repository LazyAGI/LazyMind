import { useModelSelectionStore } from "@/modules/chat/store/modelSelection";
import { CHAT_MODEL_SELECTION_CHANGED_EVENT } from "@/modules/chat/constants/chat";
import { createElement, createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { Button, Modal, message, type ModalFuncProps } from "antd";
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
  previewEmbeddingSwitch,
  clearEmbeddingChunks,
  unwrapModelProviderData,
  withModelProviderJsonOptions,
} from "../api";
import { getProviderLogoUrl } from "../providerBranding";
import { isModelTypeSupportedByProtocol } from "../modelCapabilities";
import type { ModelSettings } from "../modelSettings";

export type SetupAvailabilityState = "loading" | "ready" | "empty" | "error";

export const DefaultModelConfigRefreshContext = createContext(0);

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
  logoDataUrl?: string;
  name: string;
  modelType?: string;
  vision?: boolean;
  modelSettings?: ModelSettings;
  capability: ModelCapability;
  builtIn: boolean;
  enabled: boolean;
  maxInputTokens?: string;
  availability?: "available" | "degraded" | "unavailable";
  unavailableReason?: string;
  lifecycle?: "active" | "deprecated" | "retired";
  readOnly?: boolean;
}

export interface ProviderOption {
  id: string;
  name: string;
  brandKey?: string;
  serviceKey?: string;
  isCustom?: boolean;
  protocolName?: string;
  runtimeIdentity?: string;
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
  logo_data_url?: string;
  id: string;
  name: string;
  brand_key?: string;
  service_key?: string;
  is_custom?: boolean;
  protocol_name?: string;
  runtime_identity?: string;
  description?: string;
  base_url?: string;
}

interface ApiModel {
  logo_data_url?: string;
  brand_key?: string;
  service_key?: string;
  is_custom?: boolean;
  id: string;
  is_editable?: boolean;
  name: string;
  model_type?: string;
  vision?: boolean;
  model_settings?: ModelSettings;
  is_default?: boolean;
  enabled?: boolean;
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
  unavailable_reason?: string;
  lifecycle?: "active" | "deprecated" | "retired";
  read_only?: boolean;
  capabilities?: string[];
}

interface SelectedModelApiItem {
  logo_data_url?: string;
  brand_key?: string;
  service_key?: string;
  is_custom?: boolean;
  base_url?: string;
  group_name: string;
  is_default?: boolean;
  is_editable?: boolean;
  model_id: string;
  model_key: string;
  model_type?: string;
  vision?: boolean;
  model_settings?: ModelSettings;
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
  enabled?: boolean;
  max_input_tokens?: string;
}

type SelectedModels = Partial<Record<ModelCapability, string>>;
type SelectedModelMaxInputTokens = Partial<
  Record<ModelCapability, string>
>;
type SelectedModelRows = Partial<Record<ModelCapability, SelectedModelApiItem>>;
type ModelCatalog = Partial<Record<ModelCapability, ApiModel[]>>;
type DefaultConfigPart = "providers" | "models" | "services";
type DefaultConfigLoadStates = Record<DefaultConfigPart, "loading" | "ready" | "error">;

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

type ModuleModelOptions = Partial<Record<ModelCapability, ModelOptionItem[]>>;

export function isSelectableModelOption(option: ModelOptionItem, cloudAvailable: boolean) {
  const { model, source } = option;
  return (source !== "cloud" || cloudAvailable) && model.enabled &&
    model.availability !== "unavailable" && model.lifecycle !== "deprecated" && model.lifecycle !== "retired";
}

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
  logo_data_url?: string;
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
    brandKey: provider.brand_key,
    serviceKey: provider.service_key,
    isCustom: provider.is_custom === true,
    protocolName: provider.protocol_name || provider.name,
    runtimeIdentity: provider.runtime_identity,
    brand: provider.is_custom ? "API" : getProviderBrand(provider.brand_key || provider.name || provider.protocol_name || ""),
    logoUrl: provider.logo_data_url || getProviderLogoUrl(provider.brand_key || provider.name || provider.protocol_name || "", provider.is_custom),
    headline: getLocalizedProviderDescription(
      provider.name,
      backendDescription,
      fallbacks,
    ),
    backendDescription,
    source: provider.protocol_name || provider.name,
    baseUrl: provider.base_url || "",
    capabilities: [],
    models: [],
  };
}

function getSelectedCapability(selection: SelectedModelApiItem) {
  const capability = getCapabilityByModelType(selection.model_key);
  return capability === "image_editor" ? "image_generator" : capability;
}

function mapModelOption(
  model: ApiModel,
  capability: ModelCapability,
  providers: ProviderOption[],
  fallbacks: ModelProviderFallbacks,
): ModelOptionItem {
  const source = model.source === "cloud" ? "cloud" : "own";
  const providerId = model.provider_id || model.user_model_provider_id || (source === "cloud" ? "lazymind-cloud" : "");
  const groupId = model.provider_group_id || model.user_model_provider_group_id || (source === "cloud" ? "cloud-system" : "");
  const provider = (source === "own" && providers.find((item) => item.id === providerId)) || mapApiProvider({
    id: providerId,
    brand_key: model.brand_key,
    service_key: model.service_key,
    name: source === "cloud" ? model.provider_name || "LazyMind Cloud" : model.group_name || model.provider_name || "",
    protocol_name: model.provider_name,
    is_custom: source === "own" && (model.is_custom ?? (!model.brand_key && !!model.group_name && model.group_name !== model.provider_name)),
    base_url: model.base_url,
  }, fallbacks);
  const group = createConnectionGroup(provider, {
    id: groupId,
    name: model.group_name || (source === "cloud" ? "" : provider.name),
    baseUrl: model.base_url || provider.baseUrl,
    verified: true,
  });
  // Cloud availability comes from the gateway, independently of the local adapter registry.
  const supported = source === "cloud" || isModelTypeSupportedByProtocol(
    model.model_type || getModelTypeByCapability(capability),
    provider.protocolName || provider.source,
    provider.runtimeIdentity || provider.name,
  );
  return {
    provider,
    group,
    source,
    value: getModelValue(source, provider.id, group.id, model.id),
    isEditable: !!model.is_editable,
    model: {
      id: model.id,
      name: model.name || model.id,
      logoDataUrl: model.logo_data_url,
      modelType: model.model_type,
      vision: model.vision,
      modelSettings: model.model_settings,
      capability,
      builtIn: Boolean(model.is_default),
      enabled: model.enabled !== false,
      maxInputTokens: model.max_input_tokens,
      availability: supported ? model.availability : "unavailable",
      unavailableReason: model.unavailable_reason,
      lifecycle: model.lifecycle,
      readOnly: model.read_only,
    },
  };
}

function mapCloudServiceGroup(
  group: VerifiedCloudServiceGroup | SelectedCloudServiceApiItem,
): CloudServiceOption {
  return {
    baseUrl: group.base_url || "",
    groupId: group.group_id,
    groupName: group.group_name,
    providerName: group.provider_name,
  };
}

function mergeCloudServiceOptions(
  options: CloudServiceOption[],
  selected: CloudServiceOption,
) {
  const index = options.findIndex((option) => option.groupId === selected.groupId);
  if (index === -1) return [selected, ...options];

  const catalogOption = options[index];
  const merged = [...options];
  merged[index] = {
    groupId: selected.groupId,
    groupName: selected.groupName || catalogOption.groupName,
    providerName: selected.providerName || catalogOption.providerName,
    baseUrl: selected.baseUrl || catalogOption.baseUrl,
  };
  return merged;
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
  const refreshRevision = useContext(DefaultModelConfigRefreshContext);
  const appliedRefreshRevision = useRef(refreshRevision);
  const [refreshRetryRevision, setRefreshRetryRevision] = useState(0);
  const currentLanguage = i18n.resolvedLanguage || i18n.language || "zh-CN";
  const [providerOptions, setProviderOptions] = useState<ProviderOption[]>([]);
  const [selectedModelRows, setSelectedModelRows] = useState<SelectedModelRows>();
  const [selectedCloudServices, setSelectedCloudServices] =
    useState<SelectedCloudServices>({});
  const [cloudServiceShareStatus, setCloudServiceShareStatus] = useState<
    Partial<Record<CloudServiceSlotKey, boolean>>
  >({});
  const [cloudServiceCatalogs, setCloudServiceCatalogs] = useState<
    Partial<Record<CloudServiceSlotKey, CloudServiceOption[]>>
  >({});
  const [selectedCloudServiceOptions, setSelectedCloudServiceOptions] = useState<
    Partial<Record<CloudServiceSlotKey, CloudServiceOption>>
  >({});
  const [cloudServiceOptionStates, setCloudServiceOptionStates] = useState<
    Partial<Record<CloudServiceSlotKey, SetupAvailabilityState>>
  >({});
  const [cloudServiceReadyStatus, setCloudServiceReadyStatus] =
    useState<CloudServiceReadyStatus>({});
  const [modelCatalog, setModelCatalog] = useState<ModelCatalog>({});
  const [moduleModelOptionStates, setModuleModelOptionStates] = useState<
    Partial<Record<ModelCapability, SetupAvailabilityState>>
  >({});
  const [shareStatus, setShareStatus] = useState<
    Partial<Record<ModelCapability, boolean>>
  >({});
  const [modelReadyStatus, setModelReadyStatus] = useState<ModelReadyStatus>(
    {},
  );
  const [cloudSessionAvailable, setCloudSessionAvailable] = useState<boolean>();
  const lazyMindCloudAvailable = cloudSessionAvailable === true;
  const [loadStates, setLoadStates] = useState<DefaultConfigLoadStates>({
    providers: "loading",
    models: "loading",
    services: "loading",
  });
  const [savingCapabilities, setSavingCapabilities] = useState<Set<ModelCapability | CloudServiceSlotKey>>(new Set());
  const [previewingEmbedding, setPreviewingEmbedding] = useState(false);
  const savingCapabilitiesRef = useRef(new Set<ModelCapability | CloudServiceSlotKey>());
  const embeddingSwitchRevision = useRef(0);
  const [capabilityReadyStates, setCapabilityReadyStates] = useState<CapabilityReadyStates>({});
  const readinessRequests = useRef(new Map<CapabilityKey, symbol>());
  const manualReadinessRevisions = useRef(new Map<CapabilityKey, number>());
  const loadRevision = useRef(0);
  const selectionRevisions = useRef({ providers: 0, models: 0, services: 0 });
  const optionRevisions = useRef({ models: 0, services: 0 });
  const requestedModelCatalogs = useRef(new Set<ModelCapability>());
  const requestedCloudServiceCatalogs = useRef(new Set<CloudServiceSlotKey>());
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

  const cloudServiceOptions = useMemo(() => {
    const options = { ...cloudServiceCatalogs };
    for (const { key } of cloudServiceConfigs) {
      const selected = selectedCloudServiceOptions[key];
      if (selected) options[key] = mergeCloudServiceOptions(options[key] || [], selected);
    }
    return options;
  }, [cloudServiceCatalogs, selectedCloudServiceOptions]);

  const { selectedModels, selectedModelMaxInputTokens, moduleModelOptions } = useMemo(() => {
    const values: SelectedModels = {};
    const tokens: SelectedModelMaxInputTokens = {};
    const options: ModuleModelOptions = {};
    for (const { key } of moduleConfigs) {
      const catalog = modelCatalog[key];
      const selection = selectedModelRows?.[key];
      if (!catalog && !selection) continue;
      const candidates = new Map<string, ModelOptionItem>();
      for (const model of catalog || []) {
        const option = mapModelOption(model, key, providerOptions, localizedFallbacks);
        const existing = candidates.get(option.value);
        if (!existing) candidates.set(option.value, option);
        else if (option.isEditable) existing.isEditable = true;
      }
      if (selection) {
        const saved = mapModelOption({
          ...selection,
          id: selection.model_id,
          // The Cloud selection projection has no local enabled flag.
          enabled: selection.source === "cloud" ? true : selection.enabled,
        }, key, providerOptions, localizedFallbacks);
        // The API evaluated the saved model even when its provider metadata is still pending.
        saved.model.availability = selection.availability ?? saved.model.availability;
        const candidate = [...candidates.values()].find((option) =>
          option.source === saved.source && option.model.id === saved.model.id,
        );
        if (candidate) {
          saved.model.lifecycle = candidate.model.lifecycle;
          saved.model.enabled = saved.model.enabled && candidate.model.enabled;
          if (candidate.model.availability === "unavailable") saved.model.availability = "unavailable";
          saved.isEditable ||= candidate.isEditable;
          candidates.delete(candidate.value);
        }
        values[key] = saved.value;
        tokens[key] = selection.max_input_tokens?.trim();
        options[key] = [saved, ...candidates.values()];
      } else {
        options[key] = [...candidates.values()];
      }
    }
    return { selectedModels: values, selectedModelMaxInputTokens: tokens, moduleModelOptions: options };
  }, [modelCatalog, selectedModelRows, providerOptions, localizedFallbacks]);

  useEffect(() => {
    let revision = 0;
    const refreshSession = () => {
      const request = ++revision;
      void getCloudSession()
        .then((session) => {
          if (request === revision) setCloudSessionAvailable(isCloudBusinessAvailable(session));
        })
        .catch(() => {
          if (request === revision) setCloudSessionAvailable(false);
        });
    };
    const refreshWhenVisible = () => {
      if (document.visibilityState === "visible") refreshSession();
    };
    const refreshCloudSession = () => {
      setCloudSessionAvailable(undefined);
      refreshSession();
    };
    refreshSession();
    window.addEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, refreshCloudSession);
    window.addEventListener("focus", refreshSession);
    document.addEventListener("visibilitychange", refreshWhenVisible);
    return () => {
      revision += 1;
      window.removeEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, refreshCloudSession);
      window.removeEventListener("focus", refreshSession);
      document.removeEventListener("visibilitychange", refreshWhenVisible);
    };
  }, []);

  const loadCapabilityReadiness = useCallback(async (capability: CapabilityKey) => {
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
  }, []);

  const loadDefaultModelState = useCallback((retainCatalog = false, refreshWhileSaving = false) => {
    if (savingCapabilitiesRef.current.size && !refreshWhileSaving) return false;
    const revision = ++loadRevision.current;
    const modelCapabilities = visibleModuleConfigs.map((module) => module.key);
    const serviceCapabilities = cloudServiceConfigs.map((service) => service.key);
    const refreshReadiness = (capabilities: CapabilityKey[]) => {
      if (isAdmin) return;
      for (const capability of capabilities) {
        // A manual check that started during this refresh is newer than its snapshot.
        if ((manualReadinessRevisions.current.get(capability) ?? 0) < revision) {
          void loadCapabilityReadiness(capability);
        }
      }
    };
    if (retainCatalog) {
      // Start new catalog and readiness checks while the previous display data stays visible.
      optionRevisions.current.models += 1;
      optionRevisions.current.services += 1;
      setModuleModelOptionStates({});
      setCloudServiceOptionStates({});
      refreshReadiness([...modelCapabilities, ...serviceCapabilities]);
    }
    const loadPart = async <T,>(part: DefaultConfigPart, request: () => Promise<T>, apply: (response: T) => void) => {
      const selectionRevision = selectionRevisions.current[part];
      const isCurrent = () => revision === loadRevision.current && selectionRevision === selectionRevisions.current[part];
      try {
        const response = await request();
        if (!isCurrent()) return false;
        apply(response);
        setLoadStates((current) => ({ ...current, [part]: "ready" }));
        return true;
      } catch {
        if (!isCurrent()) return false;
        setLoadStates((current) => ({ ...current, [part]: "error" }));
        return !refreshWhileSaving;
      }
    };

    const providersLoad = loadPart("providers", () => modelProvidersApi.apiCoreModelProvidersGet(), (response) => {
      const data = unwrapModelProviderData<{ providers?: ApiProvider[] }>(response.data);
      setProviderOptions((data.providers || []).map((provider) => mapApiProvider(provider, localizedFallbacks)));
    });

    const modelsLoad = loadPart("models", () => modelProvidersApi.apiCoreModelProvidersSelectedModelsGet(), (response) => {
      const data = unwrapModelProviderData<{ selections?: SelectedModelApiItem[] }>(response.data);
      const rows: SelectedModelRows = {};
      const shares: typeof shareStatus = {};
      for (const selection of data.selections || []) {
        const capability = getSelectedCapability(selection);
        if (!capability || !selection.model_id) continue;
        // Both image roles share one control. Prefer the editable selection.
        if (capability === "image_generator" && rows.image_generator?.is_editable && !selection.is_editable) continue;
        rows[capability] = selection;
        shares[capability] = selection.source !== "cloud" && !!selection.share;
      }
      if (!retainCatalog) {
        optionRevisions.current.models += 1;
        setModelCatalog({});
        setModuleModelOptionStates({});
      }
      setSelectedModelRows(rows);
      setShareStatus(shares);
      if (isAdmin) setModelReadyStatus({});
      if (!retainCatalog) refreshReadiness(modelCapabilities);
    });

    const servicesLoad = loadPart("services", () => modelProvidersApi.apiCoreModelProvidersSelectedProvidersGet(), (response) => {
      const data = unwrapModelProviderData<{ selections?: SelectedCloudServiceApiItem[] }>(response.data);
      const selections: SelectedCloudServices = {};
      const shares: typeof cloudServiceShareStatus = {};
      const options: typeof selectedCloudServiceOptions = {};
      for (const selection of data.selections || []) {
        const service = cloudServiceConfigs.find((item) => item.category === selection.category);
        if (!service) continue;
        selections[service.key] = selection.group_id;
        shares[service.key] = !!selection.share;
        options[service.key] = mapCloudServiceGroup(selection);
      }
      setSelectedCloudServices(selections);
      setCloudServiceShareStatus(shares);
      setSelectedCloudServiceOptions(options);
      if (isAdmin) setCloudServiceReadyStatus({});
      if (!retainCatalog) {
        optionRevisions.current.services += 1;
        setCloudServiceCatalogs({});
        setCloudServiceOptionStates({});
        refreshReadiness(serviceCapabilities);
      }
    });
    return Promise.all([providersLoad, modelsLoad, servicesLoad])
      .then((accepted) => accepted.every(Boolean));
  }, [isAdmin, loadCapabilityReadiness, localizedFallbacks, visibleModuleConfigs]);

  useEffect(() => {
    void loadDefaultModelState();
    return () => {
      loadRevision.current += 1;
      optionRevisions.current.models += 1;
      optionRevisions.current.services += 1;
      readinessRequests.current.clear();
    };
  }, [loadDefaultModelState]);

  useEffect(() => {
    if (refreshRevision === appliedRefreshRevision.current || savingCapabilities.size) return;
    const targetRevision = refreshRevision;
    const refresh = loadDefaultModelState(true);
    if (!refresh) return;
    let active = true;
    void refresh.then((accepted) => {
      if (!active) return;
      if (accepted) {
        appliedRefreshRevision.current = targetRevision;
      } else if (!savingCapabilitiesRef.current.size) {
        // Another load invalidated this request without changing the external
        // revision. Queue one current replacement instead of consuming it.
        setRefreshRetryRevision((current) => current + 1);
      }
    });
    return () => { active = false; };
  }, [loadDefaultModelState, refreshRevision, refreshRetryRevision, savingCapabilities]);

  useEffect(() => () => { embeddingSwitchRevision.current += 1; }, []);

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

    const revision = optionRevisions.current.models;
    requestedModelCatalogs.current.add(capability);
    setModuleModelOptionStates((current) => ({ ...current, [capability]: "loading" }));
    try {
      const modelTypes = capability === "image_generator"
        ? ["text2image", "image_editing"]
        : [getModelTypeByCapability(capability)];
      const fetchedLists = await Promise.all(modelTypes.map(async (modelType) => {
        const response = await modelProvidersApi.apiCoreModelProvidersModelsGet({ modelType });
        const data = unwrapModelProviderData<{ models?: ApiModel[] }>(response.data);
        return data.models || [];
      }));
      if (revision !== optionRevisions.current.models) return;
      setModelCatalog((current) => ({ ...current, [capability]: fetchedLists.flat() }));
      setModuleModelOptionStates((current) => ({ ...current, [capability]: "ready" }));
    } catch {
      if (revision === optionRevisions.current.models) {
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
    const part = capability === "cloudParsing" || capability === "searchEngine" ? "services" : "models";
    selectionRevisions.current[part] += 1;
    if (saving) savingCapabilitiesRef.current.add(capability);
    else savingCapabilitiesRef.current.delete(capability);
    setSavingCapabilities(new Set(savingCapabilitiesRef.current));
  };

  const applyModelSelection = (capability: ModelCapability, value?: string) => {
    if (savingCapabilitiesRef.current.has(capability)) return;
    const chosenOption = moduleModelOptions[capability]?.find((option) => option.value === value);
    const identity = value ? parseModelValue(value) : undefined;
    setCapabilitySaving(capability, true);
    return saveSelectedModel(capability, value)
      .then((response) => {
        const matches = identity ? response.selections?.filter((selection) =>
          getSelectedCapability(selection) === capability && selection.model_id === identity.modelId &&
          (selection.source || "own") === identity.source,
        ) : undefined;
        const savedSelection = matches?.find((selection) => selection.is_editable) || matches?.[0];
        const row = savedSelection && identity ? {
          ...savedSelection,
          provider_id: savedSelection.provider_id || savedSelection.user_model_provider_id || identity.providerId,
          provider_group_id: savedSelection.provider_group_id || savedSelection.user_model_provider_group_id || identity.groupId,
          provider_name: savedSelection.provider_name || chosenOption?.provider.protocolName || "",
          group_name: savedSelection.group_name || chosenOption?.group.name || "",
          name: savedSelection.name || chosenOption?.model.name || savedSelection.model_id,
          max_input_tokens: savedSelection.max_input_tokens || chosenOption?.model.maxInputTokens,
        } : undefined;
        setSelectedModelRows((current) => ({ ...current, [capability]: row }));
        setShareStatus((current) => ({
          ...current,
          [capability]: Boolean(row && row.source !== "cloud" && row.share),
        }));
        if (row && capability === highlightTarget) onHighlightResolved?.();
        if (capability === "llm") {
          useModelSelectionStore.getState().resetForNewChat();
          window.dispatchEvent(new CustomEvent(CHAT_MODEL_SELECTION_CHANGED_EVENT));
        }
        void onModelSelectionChanged();
        if (!isAdmin) void retryCapabilityReadiness(capability, true);
      })
      .catch(() => {})
      .finally(() => setCapabilitySaving(capability, false));
  };

  const handleModelSelection = (
    capability: ModelCapability,
    value?: string,
  ) => {
    if (capability !== "embed_main") {
      applyModelSelection(capability, value);
      return;
    }
    if (value === selectedModels.embed_main || previewingEmbedding || savingCapabilitiesRef.current.has(capability)) return;
    const request = ++embeddingSwitchRevision.current;
    const candidate = parseModelValue(value);
    const indexManagementDialog: Pick<ModalFuncProps, "width" | "footer"> = {
      width: 600,
      footer: (buttons) => createElement("div", {
        style: { display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, flexWrap: "wrap" },
      }, createElement(Button, {
        href: "/lib/knowledge/list?index_management=open",
        onClick: (event: React.MouseEvent<HTMLElement>) => {
          if (savingCapabilitiesRef.current.has(capability)) event.preventDefault();
        },
      }, "前往索引管理"), createElement("div", {
        style: { display: "flex", alignItems: "center", gap: 8, marginLeft: "auto" },
      }, buttons)),
    };
    setPreviewingEmbedding(true);
    void previewEmbeddingSwitch({
      model_id: candidate.modelId,
      source: candidate.source,
    }).then((preview) => {
      if (request !== embeddingSwitchRevision.current) return;
      if (preview.can_switch && (preview.outcome === "empty" || preview.outcome === "unchanged")) {
        setPreviewingEmbedding(false);
        applyModelSelection(capability, value);
        return;
      }
      if (preview.can_switch && preview.outcome === "same") {
        Modal.confirm({
          title: t("modelProvider.embeddingSignatureMatchedTitle"),
          ...indexManagementDialog,
          content: t("modelProvider.embeddingSignatureMatched"),
          okText: t("modelProvider.confirmSwitch"),
          cancelText: t("modelProvider.cancelSwitch"),
          onOk: () => {
            if (request !== embeddingSwitchRevision.current) return;
            setPreviewingEmbedding(false);
            applyModelSelection(capability, value);
          },
          onCancel: () => {
            if (request === embeddingSwitchRevision.current) setPreviewingEmbedding(false);
          },
        });
        return;
      }
      if (preview.outcome === "clear_required" && candidate.modelId && candidate.source === "own") {
        let completed = false;
        const dialog = Modal.confirm({
          title: t("modelProvider.embeddingIndexResetTitle"),
          ...indexManagementDialog,
          content: t("modelProvider.embeddingIndexResetRequired"),
          okText: t("modelProvider.embeddingClearAndSwitch"),
          okButtonProps: { danger: true },
          cancelText: t("modelProvider.cancelSwitch"),
          onOk: async () => {
            if (request !== embeddingSwitchRevision.current || completed || savingCapabilitiesRef.current.has(capability)) return;
            setCapabilitySaving(capability, true);
            dialog.update({ cancelButtonProps: { disabled: true } });
            try {
              let activated = false;
              try {
                const result = await clearEmbeddingChunks({
                  model_id: candidate.modelId,
                  source: candidate.source,
                  generation: preview.generation,
                  confirm_clear_chunks: true,
                });
                activated = result.cleared === true && result.activated === true;
              } catch {
                // A lost response may still have activated the target on the server.
              }
              if (request !== embeddingSwitchRevision.current) return;
              // The server owns both cleanup and activation. Reconcile while the
              // save lock is held; never send a separate selection PUT here.
              const refreshed = await loadDefaultModelState(true, true);
              if (request !== embeddingSwitchRevision.current) return;
              void onModelSelectionChanged();
              if (!activated || !refreshed) {
                message.error(t("modelProvider.embeddingCleanupFailed"));
                // Reject to keep the dialog open so retry reuses this confirmation
                // and generation, including after partially completed activation.
                throw new Error("embedding activation incomplete");
              }
              completed = true;
              setPreviewingEmbedding(false);
              if (capability === highlightTarget) onHighlightResolved?.();
              message.success(t("modelProvider.embeddingSwitchManualParse"));
            } finally {
              if (request === embeddingSwitchRevision.current) {
                setCapabilitySaving(capability, false);
                if (!completed) dialog.update({ cancelButtonProps: { disabled: false } });
              }
            }
          },
          onCancel: () => {
            if (request !== embeddingSwitchRevision.current || savingCapabilitiesRef.current.has(capability)) return;
            embeddingSwitchRevision.current += 1;
            setPreviewingEmbedding(false);
          },
        });
        return;
      }
      setPreviewingEmbedding(false);
      Modal.warning({
        title: t(preview.outcome === "clear_required"
          ? "modelProvider.embeddingIndexResetTitle"
          : "modelProvider.embeddingSignatureCheckFailedTitle"),
        ...indexManagementDialog,
        content: t(preview.outcome === "clear_required"
          ? "modelProvider.embeddingIndexResetRequired"
          : "modelProvider.embeddingSignatureCheckFailed"),
        okText: t("common.confirm"),
      });
    }).catch(() => {
      if (request !== embeddingSwitchRevision.current) return;
      setPreviewingEmbedding(false);
      Modal.warning({
        title: t("modelProvider.embeddingSignatureCheckFailedTitle"),
        ...indexManagementDialog,
        content: t("modelProvider.embeddingSignatureCheckFailed"),
        okText: t("common.confirm"),
      });
    });
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

    const revision = optionRevisions.current.services;
    requestedCloudServiceCatalogs.current.add(service.key);
    setCloudServiceOptionStates((current) => ({ ...current, [service.key]: "loading" }));
    try {
      const response = await modelProvidersApi.apiCoreModelProvidersProviderGroupsGet({
        category: service.category,
      });
      const data = unwrapModelProviderData<CloudServiceGroupListResponse>(response.data);
      if (revision !== optionRevisions.current.services) return;
      const options = (data.groups || []).map(mapCloudServiceGroup);
      setCloudServiceCatalogs((current) => ({ ...current, [service.key]: options }));
      setCloudServiceOptionStates((current) => ({ ...current, [service.key]: "ready" }));
    } catch {
      if (revision === optionRevisions.current.services) {
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
        setSelectedCloudServiceOptions((current) => ({
          ...current,
          [service]: selection?.group_id ? mapCloudServiceGroup(selection) : undefined,
        }));
        setCloudServiceShareStatus((current) => ({ ...current, [service]: !!selection?.share }));
        void onModelSelectionChanged();
        if (!isAdmin) void retryCapabilityReadiness(service, true);
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

  const hasPersistedSelection = (capability: ModelCapability) => Boolean(selectedModels[capability]);
  const isUsable = (capability: ModelCapability) => {
    const selected = moduleModelOptions[capability]?.find((option) => option.value === selectedModels[capability]);
    return Boolean(selected && isSelectableModelOption(selected, lazyMindCloudAvailable));
  };
  const isCloudServiceConfigured = (service: CloudServiceSlotKey) => Boolean(
    selectedCloudServices[service] || (!isAdmin && cloudServiceReadyStatus[service]?.ready),
  );
  useEffect(() => {
    if (loadStates.models === "ready" && modelProviderSetupState === "ready") {
      for (const { key } of visibleModuleConfigs) {
        const hasRuntimeFallback = !hasPersistedSelection(key) && !isAdmin && modelReadyStatus[key]?.ready;
        const needsOptions = requestedModelCatalogs.current.has(key) || (!isUsable(key) && !hasRuntimeFallback);
        if (needsOptions && !moduleModelOptionStates[key]) {
          void loadModuleModels(key);
        }
      }
    }
    for (const service of cloudServiceConfigs) {
      const needsOptions = requestedCloudServiceCatalogs.current.has(service.key) ||
        (loadStates.services === "ready" && !isCloudServiceConfigured(service.key));
      if (cloudServiceSetupStates[service.key] === "ready" && needsOptions && !cloudServiceOptionStates[service.key]) {
        void loadVerifiedCloudService(service);
      }
    }
  }, [loadStates, modelProviderSetupState, cloudServiceSetupStates, visibleModuleConfigs,
    selectedModels, selectedCloudServices, moduleModelOptionStates, cloudServiceOptionStates,
    lazyMindCloudAvailable, moduleModelOptions, modelReadyStatus, cloudServiceReadyStatus]);

  const retryCapabilityReadiness = (capability: CapabilityKey, invalidate = false) => {
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
    manualReadinessRevisions.current.set(capability, loadRevision.current);
    return loadCapabilityReadiness(capability);
  };

  const retryDefaultModelState = () => {
    if (savingCapabilitiesRef.current.size) return;
    setLoadStates((current) => ({
      providers: current.providers === "error" ? "loading" : current.providers,
      models: current.models === "error" ? "loading" : current.models,
      services: current.services === "error" ? "loading" : current.services,
    }));
    loadDefaultModelState();
  };

  return {
    visibleModuleConfigs,
    isAdmin,
    lazyMindCloudAvailable,
    cloudSessionKnown: cloudSessionAvailable !== undefined,
    defaultLoadState: loadStates.models,
    hasLoadedModelSelections: selectedModelRows !== undefined,
    defaultLoadFailed: Object.values(loadStates).includes("error"),
    selectedServicesLoadState: loadStates.services,
    savingCapabilities,
    previewingEmbedding,
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
    hasPersistedSelection,
    isUsable,
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
