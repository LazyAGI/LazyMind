import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button, Empty, Form, Input, Modal, Popconfirm, Select, Switch, Tag, Tooltip, message } from "antd";
import { useTranslation } from "react-i18next";
import {
  CheckCircleFilled,
  CheckCircleOutlined,
  DeleteOutlined,
  DownOutlined,
  EditOutlined,
  KeyOutlined,
  LoadingOutlined,
  MinusCircleOutlined,
  PlusCircleOutlined,
  QuestionCircleOutlined,
  SearchOutlined,
  UpOutlined,
} from "@ant-design/icons";
import { AgentAppsAuth } from "@/components/auth";
import { BASE_URL, axiosInstance, getLocalizedErrorMessage } from "@/components/request";
import type { RawAxiosRequestConfig } from "axios";
import { notifyModelFeaturesChanged, useModelFeatures } from "@/hooks/useModelFeatures";
import "./index.scss";

type ModelCapability =
  | "llm"
  | "embed_main"
  | "vlm"
  | "reranker"
  | "stt"
  | "tts"
  | "text2image"
  | "embed_image"
  | "image_editing"
  | "evo_llm";

interface ProviderModel {
  id: string;
  name: string;
  capability: ModelCapability;
  builtIn: boolean;
  enabled: boolean;
}

interface ProviderOption {
  id: string;
  name: string;
  brand: string;
  logoUrl?: string;
  headline: string;
  backendDescription?: string;
  source: string;
  baseUrl: string;
  capabilities: ModelCapability[];
}

interface ProviderConnectionGroup {
  id: string;
  name: string;
  source: string;
  baseUrl: string;
  apiKey?: string;
  apiKeyConfigured: boolean;
  verified: boolean;
  models: ProviderModel[];
}

interface AddedProvider extends ProviderOption {
  groups: ProviderConnectionGroup[];
}

interface ProviderConfigModalState {
  provider: ProviderOption | AddedProvider;
  group?: ProviderConnectionGroup;
}

interface AlgorithmProviderConfig {
  source: string;
  baseUrl: string;
  apiKey?: string;
}

interface ModuleConfig {
  key: ModelCapability;
  titleKey: string;
  subtitleKey: string;
  required?: boolean;
  restricted?: boolean;
}

interface ProviderConfigFormValues {
  name?: string;
  apiKey?: string;
  baseUrl?: string;
}

interface CustomModelModalState {
  provider: AddedProvider;
  group: ProviderConnectionGroup;
}

interface CustomModelFormValues {
  providerId: string;
  groupId: string;
  name: string;
  capability: ModelCapability;
}

interface SelectedModelApiItem {
  group_name: string;
  model_id: string;
  model_type: string;
  name: string;
  provider_name: string;
  user_model_provider_group_id: string;
  user_model_provider_id: string;
  share?: boolean;
}

const capabilityLabelKeys: Record<ModelCapability, string> = {
  llm: "modelProvider.capability.llmChat",
  embed_main: "modelProvider.capability.embedding",
  vlm: "modelProvider.capability.vlm",
  reranker: "modelProvider.capability.rerank",
  stt: "modelProvider.capability.asr",
  tts: "modelProvider.capability.tts",
  text2image: "modelProvider.capability.textToImage",
  embed_image: "modelProvider.capability.multimodalEmbedding",
  image_editing: "modelProvider.capability.imageEditing",
  evo_llm: "modelProvider.capability.selfEvolution",
};

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
    key: "evo_llm",
    titleKey: "modelProvider.module.selfEvolutionTitle",
    subtitleKey: "modelProvider.module.selfEvolutionSubtitle",
  },
];


const builtInProviders: ProviderOption[] = [
  {
    id: "tongyi",
    name: "Tongyi-Qianwen",
    brand: "通义",
    headline: "覆盖文本、向量、多模态、语音与重排序能力，适合作为默认全能供应商。",
    source: "tongyi",
    baseUrl: "https://dashscope.aliyuncs.com/",
    capabilities: ["llm", "embed_main", "vlm", "reranker", "stt", "tts", "text2image"],
  },
  {
    id: "openai",
    name: "OpenAI",
    brand: "◎",
    headline: "通用模型生态完整，适合接入对话、向量、语音与多模态任务。",
    source: "openai",
    baseUrl: "https://api.openai.com/v1/",
    capabilities: ["llm", "embed_main", "vlm", "tts", "stt"],
  },
  {
    id: "anthropic",
    name: "Anthropic",
    brand: "AI",
    headline: "长文本和稳健推理体验突出，适合高质量文本对话场景。",
    source: "anthropic",
    baseUrl: "https://api.anthropic.com/v1/",
    capabilities: ["llm", "vlm"],
  },
  {
    id: "gemini",
    name: "Gemini",
    brand: "✦",
    headline: "视觉、搜索增强与跨模态协作能力均衡。",
    source: "gemini",
    baseUrl: "https://generativelanguage.googleapis.com/v1beta",
    capabilities: ["llm", "embed_main", "vlm"],
  },
  {
    id: "deepseek",
    name: "DeepSeek",
    brand: "DS",
    headline: "推理模型性价比高，适合默认问答主模型或自进化任务。",
    source: "deepseek",
    baseUrl: "https://api.deepseek.com",
    capabilities: ["llm", "evo_llm"],
  },
];

type SelectedModels = Partial<Record<ModelCapability, string>>;

type ModelReadyInfo = {
  ready: boolean;
  source?: 'own' | 'shared';
  sharedByName?: string;
  sharedByID?: string;
  providerName?: string;
  modelName?: string;
} | null;

type ModelOptionItem = {
  provider: ProviderOption;
  group: ProviderConnectionGroup;
  model: ProviderModel;
  algorithmConfig: AlgorithmProviderConfig;
  value: string;
};

function createConnectionGroup(provider: ProviderOption, overrides: Partial<ProviderConnectionGroup> = {}): ProviderConnectionGroup {
  return {
    id: overrides.id || `${provider.id}-default`,
    name: overrides.name || provider.name,
    source: provider.source,
    baseUrl: overrides.baseUrl || provider.baseUrl,
    apiKey: overrides.apiKey,
    apiKeyConfigured: overrides.apiKeyConfigured ?? false,
    verified: overrides.verified ?? false,
    models: overrides.models || [],
  };
}

const getModelValue = (providerId: string, groupId: string, modelId: string) => `${providerId}:${groupId}:${modelId}`;

const parseModelValue = (value?: string) => {
  const [providerId, groupId, ...modelIdParts] = String(value || "").split(":");
  return {
    providerId,
    groupId,
    modelId: modelIdParts.join(":"),
  };
};

const getAlgorithmProviderConfig = (
  provider: AddedProvider,
  group: ProviderConnectionGroup
): AlgorithmProviderConfig => ({
  source: provider.source,
  baseUrl: group.baseUrl,
  apiKey: group.apiKeyConfigured ? "********" : undefined,
});

const selectedCapabilityByModelType: Record<string, ModelCapability> = {
  evo_llm: "evo_llm",
};

function normalizeProviderKey(value: string) {
  return value.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-") || "provider";
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

function getProviderLogoUrl(name: string) {
  const normalized = name.trim().toLowerCase();
  const domainMap: Array<[RegExp, string]> = [
    [/claude|anthropic/, "anthropic.com"],
    [/deepseek/, "deepseek.com"],
    [/doubao|volc|ark/, "volcengine.com"],
    [/glm|bigmodel|zhipu/, "bigmodel.cn"],
    [/kimi|moonshot/, "moonshot.cn"],
    [/minimax/, "minimaxi.com"],
    [/openai/, "openai.com"],
    [/qwen|tongyi|通义/, "qwen.ai"],
    [/sensenova|sensecore|商汤|日日新/, "platform.sensenova.cn"],
    [/siliconflow/, "siliconflow.cn"],
  ];
  const match = domainMap.find(([pattern]) => pattern.test(normalized));
  if (!match) return undefined;
  return `https://www.google.com/s2/favicons?domain=${encodeURIComponent(match[1])}&sz=96`;
}

// Maps a raw model_type string from the backend to a ModelCapability.
// Since ModelCapability values are now aligned with backend model_type keys,
// this is mostly a passthrough with a fallback for legacy "vision" aliases.
function mapModelTypeToCapability(modelType?: string): ModelCapability {
  const normalized = (modelType || "").toLowerCase();
  if (normalized.includes("vision") && normalized !== "vlm") return "vlm";
  const known = moduleConfigs.find((m) => m.key === normalized);
  return known ? normalized as ModelCapability : "llm";
}

function getCapabilityByModelType(modelType?: string): ModelCapability | undefined {
  const normalized = (modelType || "").toLowerCase();
  const explicit = selectedCapabilityByModelType[normalized];
  if (explicit) return explicit;
  return moduleConfigs.find((m) => m.key === normalized)?.key;
}

const createModelProviderFallbacks = (t: ReturnType<typeof useTranslation>["t"]) => ({
  providerDescription: t("modelProvider.providerDescriptionFallback"),
  providerDescriptions: {
    claude: t("modelProvider.providerDescriptions.claude", { defaultValue: "" }),
    deepseek: t("modelProvider.providerDescriptions.deepseek", { defaultValue: "" }),
    doubao: t("modelProvider.providerDescriptions.doubao", { defaultValue: "" }),
    glm: t("modelProvider.providerDescriptions.glm", { defaultValue: "" }),
    kimi: t("modelProvider.providerDescriptions.kimi", { defaultValue: "" }),
    minimax: t("modelProvider.providerDescriptions.minimax", { defaultValue: "" }),
    openai: t("modelProvider.providerDescriptions.openai", { defaultValue: "" }),
    qwen: t("modelProvider.providerDescriptions.qwen", { defaultValue: "" }),
    sensenova: t("modelProvider.providerDescriptions.sensenova", { defaultValue: "" }),
    siliconflow: t("modelProvider.providerDescriptions.siliconflow", { defaultValue: "" }),
  } as Record<string, string>,
});

type ModelProviderFallbacks = ReturnType<typeof createModelProviderFallbacks>;

function getLocalizedProviderDescription(
  name: string,
  fallbackDescription: string | undefined,
  fallbacks: ModelProviderFallbacks
) {
  const providerKey = normalizeProviderKey(name).replace(/-/g, "");
  const translatedDescription = fallbacks.providerDescriptions[providerKey];
  return translatedDescription || fallbackDescription || fallbacks.providerDescription;
}

interface ApiEnvelope<T> {
  code?: number;
  message?: string;
  data?: T;
}

interface ApiProvider {
  id: string;
  name: string;
  description?: string;
  base_url?: string;
  model_types?: string[];
}

interface ApiGroup {
  id: string;
  name: string;
  base_url?: string;
  api_key?: string;
  is_verified?: boolean;
  user_model_provider_id: string;
}

interface CheckModelProviderResult {
  success: boolean;
  message?: string;
}

interface ApiModel {
  id: string;
  name: string;
  model_type?: string;
  is_default?: boolean;
}

function getApiBaseUrl() {
  return `${BASE_URL || window.location.origin}/api/core`;
}

function getRequestHeaders() {
  return {
    "Content-Type": "application/json",
    ...AgentAppsAuth.getAuthHeaders(),
  };
}

function unwrapResponse<T>(payload: ApiEnvelope<T> | T): T {
  if (payload && typeof payload === "object" && "data" in payload) {
    return (payload as ApiEnvelope<T>).data as T;
  }
  return payload as T;
}

async function modelProviderRequest<T>(
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE",
  path: string,
  data?: unknown,
  options?: RawAxiosRequestConfig
) {
  const response = await axiosInstance.request<ApiEnvelope<T> | T>({
    method,
    url: `${getApiBaseUrl()}${path}`,
    data,
    headers: getRequestHeaders(),
    ...options,
  });
  return unwrapResponse<T>(response.data);
}

function mapApiProvider(provider: ApiProvider, fallbacks: ModelProviderFallbacks): ProviderOption {
  const backendDescription = provider.description;

  return {
    id: provider.id,
    name: provider.name,
    brand: getProviderBrand(provider.name),
    logoUrl: getProviderLogoUrl(provider.name),
    headline: getLocalizedProviderDescription(provider.name, backendDescription, fallbacks),
    backendDescription,
    source: provider.name,
    baseUrl: provider.base_url || "",
    capabilities: (provider.model_types || []).filter(
      (t): t is ModelCapability => moduleConfigs.some((m) => m.key === t)
    ),
  };
}

function mapApiGroup(
  provider: ProviderOption,
  group: ApiGroup | ProviderConnectionGroup,
  models: ApiModel[]
): ProviderConnectionGroup {
  const isApiGroup = "base_url" in group || "api_key" in group || "is_verified" in group;

  return createConnectionGroup(provider, {
    id: group.id,
    name: group.name,
    baseUrl: isApiGroup ? (group as ApiGroup).base_url || provider.baseUrl : (group as ProviderConnectionGroup).baseUrl || provider.baseUrl,
    apiKey: isApiGroup ? (group as ApiGroup).api_key : (group as ProviderConnectionGroup).apiKey,
    apiKeyConfigured: isApiGroup ? Boolean((group as ApiGroup).api_key) : (group as ProviderConnectionGroup).apiKeyConfigured,
    verified: isApiGroup ? Boolean((group as ApiGroup).is_verified) : (group as ProviderConnectionGroup).verified,
    models: models.map((model) => ({
      id: model.id,
      name: model.name,
      capability: mapModelTypeToCapability(model.model_type),
      builtIn: Boolean(model.is_default),
      enabled: true,
    })),
  });
}

function getCheckFailureMessage(checkResult?: CheckModelProviderResult): string | undefined {
  if (!checkResult || typeof checkResult !== "object") {
    return undefined;
  }

  if (typeof checkResult.message === "string" && checkResult.message.trim()) {
    return checkResult.message.trim();
  }

  return undefined;
}

function ProviderLogo({ provider, compact = false }: { provider: ProviderOption; compact?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={`model-provider-logo is-${normalizeProviderKey(provider.name)}${compact ? " is-compact" : ""}`}
    >
      <span className="model-provider-logo-fallback">{provider.brand}</span>
      {provider.logoUrl ? (
        <img
          alt=""
          loading="lazy"
          src={provider.logoUrl}
          onError={(event) => {
            event.currentTarget.style.display = "none";
          }}
        />
      ) : null}
    </span>
  );
}

function CapabilityTag({ label, active = false }: { label: string; active?: boolean }) {
  return (
    <Tag className={`model-provider-capability${active ? " is-active" : ""}`}>
      {label}
    </Tag>
  );
}

function normalizeModelName(value: string) {
  return value.trim().toLowerCase();
}

function normalizeFormText(value?: string) {
  return value?.trim() || "";
}

function renderDescriptionWithLinks(description: string) {
  const parts = description.split(/(https?:\/\/[^\s，。；、）)]+)/g);

  return parts.map((part, index) => {
    if (/^https?:\/\//.test(part)) {
      return (
        <a
          href={part}
          key={`${part}-${index}`}
          rel="noreferrer"
          target="_blank"
          onClick={(event) => event.stopPropagation()}
        >
          {part}
        </a>
      );
    }

    return <span key={`${part}-${index}`}>{part}</span>;
  });
}

function isDefaultProviderBaseUrl(provider: Pick<ProviderOption, "baseUrl">, baseUrl?: string) {
  return normalizeFormText(baseUrl) === normalizeFormText(provider.baseUrl);
}

function getModelProvidersPath(keyword?: string) {
  const normalizedKeyword = keyword?.trim();
  if (!normalizedKeyword) {
    return "/model_providers";
  }
  return `/model_providers?${new URLSearchParams({ keyword: normalizedKeyword }).toString()}`;
}

export default function ModelProviderPage() {
  const { t, i18n } = useTranslation();
  const [providerConfigForm] = Form.useForm<ProviderConfigFormValues>();
  const [customModelForm] = Form.useForm<CustomModelFormValues>();

  const [providerOptions, setProviderOptions] = useState<ProviderOption[]>(builtInProviders);
  const [addedProviderList, setAddedProviderList] = useState<AddedProvider[]>([]);
  const [configModal, setConfigModal] = useState<ProviderConfigModalState | null>(null);
  const [customModelModal, setCustomModelModal] = useState<CustomModelModalState | null>(null);
  const [expandedProviderIds, setExpandedProviderIds] = useState<Record<string, boolean>>({});
  const [keyword, setKeyword] = useState("");
  const [loading, setLoading] = useState(false);
  const [providerSearchLoading, setProviderSearchLoading] = useState(false);
  const [providerConfigSaving, setProviderConfigSaving] = useState(false);
  const [verifyingGroupIds, setVerifyingGroupIds] = useState<Record<string, boolean>>({});
  const [expandedGroupIds, setExpandedGroupIds] = useState<Record<string, boolean>>({});
  const [loadingGroupModelIds, setLoadingGroupModelIds] = useState<Record<string, boolean>>({});
  const [selectedModels, setSelectedModels] = useState<SelectedModels>({});
  const [moduleModelOptions, setModuleModelOptions] = useState<Partial<Record<ModelCapability, ModelOptionItem[]>>>({});
  const [moduleModelLoading, setModuleModelLoading] = useState<Partial<Record<ModelCapability, boolean>>>({});
  const [shareStatus, setShareStatus] = useState<Partial<Record<ModelCapability, boolean>>>({});
  const [modelReadyStatus, setModelReadyStatus] = useState<Partial<Record<ModelCapability, ModelReadyInfo>>>({});
  const isAdmin = AgentAppsAuth.getUserInfo()?.role === 'system-admin';
  const modelFeaturesState = useModelFeatures();
  const imageEmbedEnabled =
    modelFeaturesState.status !== 'ready' || modelFeaturesState.features.image_embed_enabled;
  // Hide MULTIMODAL_EMBEDDING slot when image embed is not configured in runtime_models.yaml.
  const visibleModuleConfigs = useMemo(
    () => moduleConfigs.filter((m) => m.key !== 'MULTIMODAL_EMBEDDING' || imageEmbedEnabled),
    [imageEmbedEnabled],
  );
  const watchedProviderBaseUrl = Form.useWatch("baseUrl", providerConfigForm);
  const providerSearchRequestIdRef = useRef(0);
  const initialProvidersLoadedRef = useRef(false);
  const localizedFallbacks = useMemo(() => createModelProviderFallbacks(t), [i18n.language, t]);
  const getCapabilityLabel = useCallback((capability: ModelCapability) => t(capabilityLabelKeys[capability]), [t]);
  const configProvider = configModal?.provider || null;
  const baseUrlChanged = configProvider
    ? !isDefaultProviderBaseUrl(
        configProvider,
        watchedProviderBaseUrl ?? providerConfigForm.getFieldValue("baseUrl") ?? configProvider.baseUrl
      )
    : false;
  const apiKeyRequired = !!configProvider && !baseUrlChanged;

  const fetchProviderOptions = useCallback(async (searchKeyword = "") => {
    const providerData = await modelProviderRequest<{ providers?: ApiProvider[] }>(
      "GET",
      getModelProvidersPath(searchKeyword)
    );
    return (providerData.providers || []).map((provider) => mapApiProvider(provider, localizedFallbacks));
  }, [localizedFallbacks]);

  const refreshSelectedModelsState = useCallback(async () => {
    const selectedData = await modelProviderRequest<{ selections?: SelectedModelApiItem[] }>(
      "GET",
      "/model_providers/selected_models"
    );
    const nextSelectedModels: SelectedModels = {};
    const nextShareStatus: Partial<Record<ModelCapability, boolean>> = {};
    (selectedData.selections || []).forEach((selection) => {
      const capability = getCapabilityByModelType(selection.model_type);
      if (!capability) {
        return;
      }
      nextSelectedModels[capability] = getModelValue(
        selection.user_model_provider_id,
        selection.user_model_provider_group_id,
        selection.model_id,
      );
      if (selection.share) {
        nextShareStatus[capability] = true;
      }
    });
    setSelectedModels(nextSelectedModels);
    setShareStatus(nextShareStatus);
  }, []);

  const searchProviderOptions = useCallback(
    async (searchKeyword: string) => {
      const requestId = providerSearchRequestIdRef.current + 1;
      providerSearchRequestIdRef.current = requestId;
      setProviderSearchLoading(true);

      try {
        const providers = await fetchProviderOptions(searchKeyword);
        if (providerSearchRequestIdRef.current === requestId) {
          setProviderOptions(providers);
        }
      } catch (error) {
        if (providerSearchRequestIdRef.current === requestId) {
          message.error(getLocalizedErrorMessage(error, t("modelProvider.error.searchFailed")));
        }
      } finally {
        if (providerSearchRequestIdRef.current === requestId) {
          setProviderSearchLoading(false);
        }
      }
    },
    [fetchProviderOptions]
  );

  const loadModelProviders = async () => {
    setLoading(true);
    try {
      const providers = await fetchProviderOptions();
      setProviderOptions(providers);

      const withGroupsData = await modelProviderRequest<{ providers?: ApiProvider[] }>("GET", "/model_providers:with_groups");
      const addedIds = new Set((withGroupsData.providers || []).map((provider) => provider.id));
      const addedProviders = await Promise.all(
        providers
          .filter((provider) => addedIds.has(provider.id))
          .map(async (provider): Promise<AddedProvider> => {
            const groupData = await modelProviderRequest<{ groups?: ApiGroup[] }>(
              "GET",
              `/model_providers/${encodeURIComponent(provider.id)}/groups`
            );
            const groups = (groupData.groups || []).map((group) => mapApiGroup(provider, group, []));
            return { ...provider, groups };
          })
      );

      setAddedProviderList(addedProviders);
      setExpandedProviderIds((current) => {
        const next = { ...current };
        addedProviders.forEach((provider, index) => {
          if (next[provider.id] === undefined) {
            next[provider.id] = index === 0;
          }
        });
        return next;
      });
      const selectedData = await modelProviderRequest<{ selections?: SelectedModelApiItem[] }>(
        "GET",
        "/model_providers/selected_models"
      );
      const nextSelectedModels: SelectedModels = {};
      const selectedOptions: Partial<Record<ModelCapability, ModelOptionItem[]>> = {};
      (selectedData.selections || []).forEach((selection) => {
        const capability = getCapabilityByModelType(selection.model_type);
        if (!capability) {
          return;
        }
        const provider =
          providers.find((item) => item.id === selection.user_model_provider_id) ||
          mapApiProvider({
            id: selection.user_model_provider_id,
            name: selection.provider_name,
          }, localizedFallbacks);
        const group = createConnectionGroup(provider, {
          id: selection.user_model_provider_group_id,
          name: selection.group_name,
          baseUrl: provider.baseUrl,
          verified: true,
        });
        const model: ProviderModel = {
          id: selection.model_id,
          name: selection.name,
          capability,
          builtIn: true,
          enabled: true,
        };
        const option = {
          provider,
          group,
          model,
          algorithmConfig: getAlgorithmProviderConfig({ ...provider, groups: [group] }, group),
          value: getModelValue(provider.id, group.id, model.id),
        };
        nextSelectedModels[capability] = option.value;
        selectedOptions[capability] = [option, ...(selectedOptions[capability] || [])];
      });
      setSelectedModels(nextSelectedModels);
      setModuleModelOptions((current) => ({ ...selectedOptions, ...current }));

      // Extract share status from selections (admin view).
      const nextShareStatus: Partial<Record<ModelCapability, boolean>> = {};
      (selectedData.selections || []).forEach((selection) => {
        const capability = getCapabilityByModelType(selection.model_type);
        if (capability && selection.share) {
          nextShareStatus[capability] = true;
        }
      });
      setShareStatus(nextShareStatus);

      // For non-admin users, fetch ready status for restricted capabilities.
      if (!isAdmin) {
        const readyResults = await Promise.allSettled(
          moduleConfigs.map(async (m) => {
            const dbModelType = m.key;
            const resp = await modelProviderRequest<{
              ready: boolean;
              source?: string;
              shared_by_name?: string;
              shared_by_id?: string;
              provider_name?: string;
              model_name?: string;
            }>(
              "GET",
              `/model_providers/models/ready?model_type=${encodeURIComponent(dbModelType)}`
            );
            return {
              capability: m.key,
              info: {
                ready: resp.ready,
                source: resp.source as 'own' | 'shared' | undefined,
                sharedByName: resp.shared_by_name,
                sharedByID: resp.shared_by_id,
                providerName: resp.provider_name,
                modelName: resp.model_name,
              } as ModelReadyInfo,
            };
          })
        );
        const nextReadyStatus: Partial<Record<ModelCapability, ModelReadyInfo>> = {};
        readyResults.forEach((result) => {
          if (result.status === 'fulfilled') {
            nextReadyStatus[result.value.capability] = result.value.info;
          }
        });
        setModelReadyStatus(nextReadyStatus);
      }
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.loadProvidersFailed")));
    } finally {
      initialProvidersLoadedRef.current = true;
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadModelProviders();
  }, []);

  useEffect(() => {
    if (!initialProvidersLoadedRef.current) {
      return;
    }

    const debounceTimer = window.setTimeout(() => {
      void searchProviderOptions(keyword);
    }, 300);

    return () => window.clearTimeout(debounceTimer);
  }, [keyword, searchProviderOptions]);

  const addedProviderIds = useMemo(
    () => new Set(addedProviderList.map((provider) => provider.id)),
    [addedProviderList]
  );

  const visibleProviders = providerOptions;

  const loadModuleModels = async (capability: ModelCapability, force = false) => {
    if (!force && moduleModelOptions[capability]) {
      return;
    }
    if (moduleModelLoading[capability]) {
      return;
    }

    setModuleModelLoading((current) => ({ ...current, [capability]: true }));
    try {
      const modelType = capability;
      const data = await modelProviderRequest<{ models?: Array<ApiModel & {
        user_model_provider_id: string;
        user_model_provider_group_id: string;
        provider_name: string;
        group_name: string;
      }> }>("GET", `/model_providers/models?model_type=${encodeURIComponent(modelType)}`);
      const options = (data.models || []).map((model) => {
        const provider =
          providerOptions.find((item) => item.id === model.user_model_provider_id) ||
          mapApiProvider({
            id: model.user_model_provider_id,
            name: model.provider_name,
          }, localizedFallbacks);
        const configuredProvider = addedProviderList.find((item) => item.id === provider.id);
        const group =
          configuredProvider?.groups.find((item) => item.id === model.user_model_provider_group_id) ||
          createConnectionGroup(provider, {
            id: model.user_model_provider_group_id,
            name: model.group_name,
            baseUrl: provider.baseUrl,
            verified: true,
          });
        const providerModel: ProviderModel = {
          id: model.id,
          name: model.name,
          capability,
          builtIn: Boolean(model.is_default),
          enabled: true,
        };

        return {
          provider,
          group,
          model: providerModel,
          algorithmConfig: getAlgorithmProviderConfig({ ...provider, groups: [group] }, group),
          value: getModelValue(provider.id, group.id, providerModel.id),
        };
      });

      setModuleModelOptions((current) => ({ ...current, [capability]: options }));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.loadModelsFailed")));
    } finally {
      setModuleModelLoading((current) => ({ ...current, [capability]: false }));
    }
  };

  const clearModuleModelCache = (capability?: ModelCapability) => {
    setModuleModelOptions((current) => {
      if (!capability) {
        return {};
      }
      const next = { ...current };
      delete next[capability];
      return next;
    });
  };

  useEffect(() => {
    Object.entries(selectedModels).forEach(([capability, value]) => {
      if (value && !moduleModelOptions[capability as ModelCapability]) {
        void loadModuleModels(capability as ModelCapability);
      }
    });
  }, [selectedModels, moduleModelOptions]);

  const openProviderConfig = (provider: AddedProvider | ProviderOption, group?: ProviderConnectionGroup) => {
    const configuredProvider = addedProviderList.find((item) => item.id === provider.id);
    const providerDraft = configuredProvider || provider;
    const groupDraft = group || createConnectionGroup(providerDraft);

    setConfigModal({ provider: providerDraft, group });
    providerConfigForm.setFieldsValue({
      name: groupDraft.name,
      apiKey: groupDraft.apiKey || "",
      baseUrl: groupDraft.baseUrl || providerDraft.baseUrl,
    });
  };

  const closeProviderConfig = () => {
    if (providerConfigSaving) {
      return;
    }
    setConfigModal(null);
    providerConfigForm.resetFields();
  };

  const saveProviderConfig = async (values: ProviderConfigFormValues) => {
    const activeConfigModal = configModal;

    if (!configProvider || !activeConfigModal || providerConfigSaving) {
      return;
    }

    const groupName = normalizeFormText(values.name);
    const baseUrl = normalizeFormText(values.baseUrl);
    const apiKey = normalizeFormText(values.apiKey);
    const isCustomBaseUrl = !isDefaultProviderBaseUrl(configProvider, baseUrl);
    const existingProvider = addedProviderList.find((provider) => provider.id === configProvider.id);
    const existingGroup = activeConfigModal.group
      ? existingProvider?.groups.find((group) => group.id === activeConfigModal.group?.id)
      : undefined;

    if (!isCustomBaseUrl && !apiKey && !existingGroup?.apiKeyConfigured) {
      providerConfigForm.setFields([{ name: "apiKey", errors: [t("modelProvider.validation.apiKeyRequired")] }]);
      return;
    }

    setProviderConfigSaving(true);
    try {
      const payload = {
        name: groupName || configProvider.name,
        base_url: baseUrl,
        ...(apiKey ? { api_key: apiKey } : {}),
      };
      const savedGroup = activeConfigModal.group
        ? await modelProviderRequest<ApiGroup>(
            "PATCH",
            `/model_providers/${encodeURIComponent(configProvider.id)}/groups/${encodeURIComponent(activeConfigModal.group.id)}`,
            payload
          )
        : await modelProviderRequest<ApiGroup>(
            "POST",
            `/model_providers/${encodeURIComponent(configProvider.id)}/groups`,
            payload
          );
      const nextGroup = mapApiGroup(configProvider, { ...savedGroup, api_key: apiKey || existingGroup?.apiKey }, existingGroup?.models || []);

      setAddedProviderList((current) =>
        current.some((provider) => provider.id === configProvider.id)
          ? current.map((provider) =>
              provider.id === configProvider.id
                ? {
                    ...provider,
                    groups: existingGroup
                      ? provider.groups.map((group) => (group.id === nextGroup.id ? nextGroup : group))
                      : [...provider.groups, nextGroup],
                  }
                : provider
            )
          : [
              ...current,
              {
                ...configProvider,
                groups: [nextGroup],
              },
            ]
      );
      if (!nextGroup.verified) {
        setSelectedModels((current) => {
          const next = { ...current };
          Object.entries(next).forEach(([capability, value]) => {
            const parsed = parseModelValue(value);
            if (parsed.providerId === configProvider.id && parsed.groupId === nextGroup.id) {
              delete next[capability as ModelCapability];
            }
          });
          return next;
        });
      }
      setExpandedProviderIds((current) => ({ ...current, [configProvider.id]: true }));
      clearModuleModelCache();
      message.success(t("modelProvider.message.groupSaved", { name: nextGroup.name }));
      setConfigModal(null);
      providerConfigForm.resetFields();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.saveFailed")));
    } finally {
      setProviderConfigSaving(false);
    }
  };

  const addProvider = (provider: ProviderOption) => {
    openProviderConfig(provider);
  };

  const verifyProviderGroup = async (providerId: string, groupId: string) => {
    const verifyKey = `${providerId}:${groupId}`;
    if (verifyingGroupIds[verifyKey]) {
      return;
    }

    setVerifyingGroupIds((current) => ({ ...current, [verifyKey]: true }));
    try {
      const provider = addedProviderList.find((item) => item.id === providerId);
      const group = provider?.groups.find((item) => item.id === groupId);
      if (!provider || !group) {
        return;
      }
      if (!group.apiKey) {
        message.warning(t("modelProvider.message.fillApiKeyBeforeVerify"));
        return;
      }

      const checkResult = await modelProviderRequest<CheckModelProviderResult>(
        "POST",
        `/model_providers/${encodeURIComponent(provider.id)}/groups/${encodeURIComponent(group.id)}:check`,
        {
          provider_name: provider.name,
          base_url: group.baseUrl,
          api_key: group.apiKey || "",
        },
        { timeout: 3 * 60 * 1000 }
      );
      const isVerified = checkResult?.success === true;
      setAddedProviderList((current) =>
        current.map((provider) =>
          provider.id === providerId
            ? {
                ...provider,
                groups: provider.groups.map((group) =>
                  group.id === groupId
                    ? {
                        ...group,
                        verified: isVerified,
                      }
                    : group
                ),
              }
            : provider
        )
      );
      if (isVerified) {
        message.success(t("modelProvider.message.groupVerified"));
        return;
      }
      setSelectedModels((current) => {
        const next = { ...current };
        Object.entries(next).forEach(([capability, value]) => {
          const parsed = parseModelValue(value);
          if (parsed.providerId === providerId && parsed.groupId === groupId) {
            delete next[capability as ModelCapability];
          }
        });
        return next;
      });
      message.error(getCheckFailureMessage(checkResult) || t("modelProvider.message.groupVerifyFailed"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.verifyFailed")));
    } finally {
      setVerifyingGroupIds((current) => {
        const next = { ...current };
        delete next[verifyKey];
        return next;
      });
    }
  };

  const deleteProviderGroup = async (providerId: string, group: ProviderConnectionGroup) => {
    const provider = addedProviderList.find((item) => item.id === providerId);
    if (!provider) {
      return;
    }

    try {
      await modelProviderRequest("DELETE", `/model_providers/${encodeURIComponent(providerId)}/groups/${encodeURIComponent(group.id)}`);
      setAddedProviderList((current) =>
        current
          .map((item) =>
            item.id === providerId
              ? {
                  ...item,
                  groups: item.groups.filter((candidate) => candidate.id !== group.id),
                }
              : item
          )
          .filter((item) => item.groups.length > 0)
      );
      setSelectedModels((current) => {
        const next = { ...current };
        Object.entries(next).forEach(([capability, value]) => {
          const parsed = parseModelValue(value);
          if (parsed.providerId === providerId && parsed.groupId === group.id) {
            delete next[capability as ModelCapability];
          }
        });
        return next;
      });
      setExpandedGroupIds((current) => {
        const next = { ...current };
        delete next[`${providerId}:${group.id}`];
        return next;
      });
      clearModuleModelCache();
      message.success(t("modelProvider.message.groupRemoved", { name: group.name }));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.deleteGroupFailed")));
    }
  };

  const deleteProvider = async (provider: AddedProvider) => {
    try {
      await Promise.all(
        provider.groups.map((group) =>
          modelProviderRequest("DELETE", `/model_providers/${encodeURIComponent(provider.id)}/groups/${encodeURIComponent(group.id)}`)
        )
      );
      setAddedProviderList((current) => current.filter((item) => item.id !== provider.id));
      setExpandedProviderIds((current) => {
        const next = { ...current };
        delete next[provider.id];
        return next;
      });
      setExpandedGroupIds((current) => {
        const next = { ...current };
        provider.groups.forEach((group) => {
          delete next[`${provider.id}:${group.id}`];
        });
        return next;
      });
      setSelectedModels((current) => {
        const next = { ...current };
        Object.entries(next).forEach(([capability, value]) => {
          if (parseModelValue(value).providerId === provider.id) {
            delete next[capability as ModelCapability];
          }
        });
        return next;
      });
      clearModuleModelCache();
      message.success(t("modelProvider.message.providerRemoved", { name: provider.name }));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.removeProviderFailed")));
    }
  };

  const loadGroupModels = async (providerId: string, groupId: string) => {
    const provider = addedProviderList.find((item) => item.id === providerId);
    const group = provider?.groups.find((item) => item.id === groupId);
    const groupKey = `${providerId}:${groupId}`;
    if (!provider || !group || loadingGroupModelIds[groupKey]) {
      return;
    }

    setLoadingGroupModelIds((current) => ({ ...current, [groupKey]: true }));
    try {
      const modelData = await modelProviderRequest<{ models?: ApiModel[] }>(
        "GET",
        `/model_providers/${encodeURIComponent(provider.id)}/groups/${encodeURIComponent(group.id)}/models`
      );
      const nextGroup = mapApiGroup(provider, group, modelData.models || []);
      setAddedProviderList((current) =>
        current.map((item) =>
          item.id === providerId
            ? {
                ...item,
                groups: item.groups.map((candidate) => (candidate.id === groupId ? nextGroup : candidate)),
              }
            : item
        )
      );
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.loadModelsFailed")));
    } finally {
      setLoadingGroupModelIds((current) => {
        const next = { ...current };
        delete next[groupKey];
        return next;
      });
    }
  };

  const toggleGroupModels = async (providerId: string, groupId: string) => {
    const groupKey = `${providerId}:${groupId}`;
    const willExpand = !expandedGroupIds[groupKey];
    if (willExpand) {
      await loadGroupModels(providerId, groupId);
    }
    setExpandedGroupIds((current) => ({ ...current, [groupKey]: willExpand }));
  };

  const toggleProviderModels = async (providerId: string) => {
    const willExpand = !expandedProviderIds[providerId];
    setExpandedProviderIds((current) => ({
      ...current,
      [providerId]: willExpand,
    }));
  };

  const openCustomModelModal = (provider: AddedProvider, group: ProviderConnectionGroup) => {
    setCustomModelModal({ provider, group });
    customModelForm.setFieldsValue({
      providerId: provider.id,
      groupId: group.id,
      capability: provider.capabilities[0] || "llm",
      name: "",
    });
  };

  const closeCustomModelModal = () => {
    setCustomModelModal(null);
    customModelForm.resetFields();
  };

  const addCustomModel = async (values: CustomModelFormValues) => {
    const provider = addedProviderList.find((item) => item.id === values.providerId);
    const group = provider?.groups.find((item) => item.id === values.groupId);
    if (!provider || !group) {
      return;
    }

    const normalizedName = normalizeModelName(values.name);
    const duplicated = group.models.some((model) => normalizeModelName(model.name) === normalizedName);

    if (duplicated) {
      customModelForm.setFields([{ name: "name", errors: [t("modelProvider.validation.duplicateModelName")] }]);
      return;
    }

    try {
      const createdModel = await modelProviderRequest<ApiModel>(
        "POST",
        `/model_providers/${encodeURIComponent(provider.id)}/groups/${encodeURIComponent(group.id)}/models`,
        {
          name: values.name.trim(),
          model_type: values.capability,
        }
      );
      const nextModel: ProviderModel = {
        id: createdModel.id,
        name: createdModel.name,
        capability: mapModelTypeToCapability(createdModel.model_type || values.capability),
        builtIn: Boolean(createdModel.is_default),
        enabled: true,
      };
      setAddedProviderList((current) =>
        current.map((item) =>
          item.id === provider.id
            ? {
                ...item,
                groups: item.groups.map((candidate) =>
                  candidate.id === group.id
                    ? {
                        ...candidate,
                        models: [...candidate.models, nextModel],
                      }
                    : candidate
                ),
              }
            : item
        )
      );
      clearModuleModelCache(values.capability);
      message.success(t("modelProvider.message.modelAdded"));
      closeCustomModelModal();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.addModelFailed")));
    }
  };

  const deleteCustomModel = async (providerId: string, groupId: string, model: ProviderModel) => {
    try {
      await modelProviderRequest(
        "DELETE",
        `/model_providers/${encodeURIComponent(providerId)}/groups/${encodeURIComponent(groupId)}/models/${encodeURIComponent(model.id)}`
      );
      setAddedProviderList((current) =>
        current.map((provider) =>
          provider.id === providerId
            ? {
                ...provider,
                groups: provider.groups.map((group) =>
                  group.id === groupId
                    ? {
                        ...group,
                        models: group.models.filter((item) => item.id !== model.id),
                      }
                    : group
                ),
              }
            : provider
        )
      );
      await refreshSelectedModelsState();
      clearModuleModelCache(model.capability);
      if (model.capability === "embed_main" || model.capability === "embed_image") {
        notifyModelFeaturesChanged();
      }
      message.success(t("modelProvider.message.modelDeleted"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.deleteModelFailed")));
    }
  };

  const saveSelectedModel = async (capability: ModelCapability, value?: string) => {
    const selections = [
      {
        model_type: capability,
        model_id: value ? parseModelValue(value).modelId : "",
      },
    ];

    const resp = await modelProviderRequest<{ selections?: SelectedModelApiItem[] }>("PUT", "/model_providers/selected_models", {
      selections,
    });
    return resp;
  };

  const toggleShareModel = async (capability: ModelCapability, share: boolean) => {
    const value = selectedModels[capability];
    if (!value) {
      message.warning(t("modelProvider.noModelSelectedForShare"));
      return;
    }
    const modelId = parseModelValue(value).modelId;
    try {
      await modelProviderRequest("PUT", "/model_providers/selected_models/share", {
        model_id: modelId,
        share,
      });
      setShareStatus((current) => ({ ...current, [capability]: share }));
      message.success(share ? t("modelProvider.shareEnabled") : t("modelProvider.shareDisabled"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.shareUpdateFailed")));
    }
  };

  const applyModelSelection = (capability: ModelCapability, value?: string) => {
    setSelectedModels((current) => ({
      ...current,
      [capability]: value,
    }));
    void saveSelectedModel(capability, value).then(async () => {
      await refreshSelectedModelsState();
      if (capability === "embed_main" || capability === "embed_image") {
        notifyModelFeaturesChanged();
      }
    }).catch((error) => {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.saveDefaultModelFailed")));
    });
  };

  const handleModelSelection = (capability: ModelCapability, value?: string) => {
    const previousValue = selectedModels[capability];
    // Only warn when switching embedding that is already live (share=true means it's been configured and shared).
    if (capability === "embed_main" && previousValue && previousValue !== value && shareStatus["embed_main"] === true) {
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

  return (
    <main className="model-provider-page">
      <section className="model-provider-shell">
        <div className="model-provider-main-panel">
          <section className="model-provider-config-panel" aria-label={t("modelProvider.defaultConfigAria")}>
            <div className="model-provider-panel-title-row">
              <div>
                <h2 className="model-provider-section-title">{t("modelProvider.defaultTitle")}</h2>
                <p className="model-provider-section-subtitle">{t("modelProvider.defaultSubtitle")}</p>
              </div>
            </div>

            <div className="model-provider-default-list">
              {visibleModuleConfigs.map((module) => {
                const options = moduleModelOptions[module.key] || [];
                const optionLoading = Boolean(moduleModelLoading[module.key]);
                const moduleTitle = t(module.titleKey);
                const moduleSubtitle = t(module.subtitleKey);

                return (
                  <div className={`model-provider-default-row${module.restricted && !isAdmin ? " is-restricted" : ""}`} key={module.key}>
                    <div className="model-provider-default-meta">
                      <label
                        className="model-provider-default-title"
                        htmlFor={`model-provider-${module.key.toLowerCase()}`}
                      >
                        {module.required ? <span className="is-required">*</span> : null}
                        <span>{moduleTitle}</span>
                      </label>
                      <Tooltip placement="top" title={moduleSubtitle}>
                        <button
                          aria-label={t("modelProvider.moduleHelpAria", { title: moduleTitle })}
                          className="model-provider-default-help"
                          type="button"
                        >
                          <QuestionCircleOutlined />
                        </button>
                      </Tooltip>
                      {module.restricted ? (
                        <Tooltip placement="top" title={t("modelProvider.restrictedAdminOnly")}>
                          <span className="model-provider-limited-tag-wrap">
                            <Tag className="model-provider-limited-tag">{t("modelProvider.limited")}</Tag>
                          </span>
                        </Tooltip>
                      ) : null}
                      {isAdmin ? (
                        <Tooltip title={t("modelProvider.shareAdminTip")}>
                          <Switch
                            aria-label={t("modelProvider.shareToggleAria", { title: moduleTitle })}
                            checked={!!shareStatus[module.key]}
                            checkedChildren={t("modelProvider.shared")}
                            className="model-provider-share-switch"
                            size="small"
                            unCheckedChildren={t("modelProvider.unshared")}
                            onChange={(checked) => void toggleShareModel(module.key, checked)}
                          />
                        </Tooltip>
                      ) : null}
                      {!isAdmin ? (
                        <Tooltip
                          title={(() => {
                            const info = modelReadyStatus[module.key];
                            if (info == null) return undefined;
                            if (!info.ready) return t("modelProvider.modelNotReadyTip");
                            if (info.source === 'shared' && info.sharedByName) {
                              return t("modelProvider.modelReadySharedTip", {
                                name: info.sharedByName,
                                provider: info.providerName || '',
                                model: info.modelName || '',
                              });
                            }
                            return t("modelProvider.modelReadyTip");
                          })()}
                        >
                          <span style={{ pointerEvents: "auto" }} className="model-provider-ready-indicator" aria-label={t("modelProvider.readyStatusAria", { title: moduleTitle })}>
                            {modelReadyStatus[module.key]?.ready === true ? (
                              <CheckCircleOutlined className="model-provider-ready-icon is-ready" />
                            ) : modelReadyStatus[module.key]?.ready === false ? (
                              <MinusCircleOutlined className={`model-provider-ready-icon is-not-ready${module.required ? ' is-required' : ''}`} />
                            ) : null}
                          </span>
                        </Tooltip>
                      ) : null}
                    </div>

                    <Select
                      allowClear={!module.required}
                      className="model-provider-model-select"
                      disabled={module.restricted && !isAdmin}
                      id={`model-provider-${module.key.toLowerCase()}`}
                      listHeight={340}
                      optionLabelProp="label"
                      placeholder={
                        module.restricted && !isAdmin
                          ? t("modelProvider.restrictedPlaceholder")
                          : module.required
                            ? t("modelProvider.requiredModelPlaceholder")
                            : t("modelProvider.optionalModelPlaceholder")
                      }
                      popupClassName="model-provider-select-dropdown"
                      suffixIcon={<DownOutlined className="model-provider-select-caret" />}
                      value={selectedModels[module.key]}
                      onChange={(value) => handleModelSelection(module.key, value)}
                      onDropdownVisibleChange={(open) => {
                        if (open) {
                          void loadModuleModels(module.key, true);
                        }
                      }}
                      loading={optionLoading}
                      notFoundContent={optionLoading ? t("common.loading") : t("modelProvider.noModelOptions")}
                    >
                      {options.map(({ provider, group, model, value }) => (
                        <Select.Option
                          key={value}
                          label={
                            <span className="model-provider-select-value">
                              <ProviderLogo provider={provider} compact />
                              <span className="model-provider-select-value-text">
                                {model.name} · {group.name}
                              </span>
                            </span>
                          }
                          value={value}
                        >
                          <span className="model-provider-select-option">
                            <ProviderLogo provider={provider} compact />
                            <span className="model-provider-select-copy">
                              <strong>{model.name}</strong>
                              <small>
                                {provider.name} / {group.name}
                                {model.builtIn ? t("modelProvider.builtInModelSuffix") : t("modelProvider.customModelSuffix")}
                              </small>
                            </span>
                          </span>
                        </Select.Option>
                      ))}
                    </Select>
                  </div>
                );
              })}
            </div>
          </section>

          <section className="model-provider-added-section">
            <div className="model-provider-panel-heading">
              <h2 className="model-provider-section-title">{t("modelProvider.myGroupsTitle")}</h2>
              <p className="model-provider-section-subtitle">{t("modelProvider.myGroupsSubtitle")}</p>
            </div>

            <div className="model-provider-added-list">
              {addedProviderList.length ? (
                addedProviderList.map((provider) => {
                  const isExpanded = !!expandedProviderIds[provider.id];
                  const modelListId = `model-provider-${provider.id}-models`;

                  return (
                    <article
                      className={`model-provider-added-card${isExpanded ? " is-expanded" : ""}`}
                      key={provider.id}
                    >
                      <div className="model-provider-added-summary">
                        <div className="model-provider-added-brand">
                          <ProviderLogo provider={provider} />
                          <div>
                            <strong>{provider.name}</strong>
                            <span>
                              {t("modelProvider.providerGroupCount", { source: provider.source, count: provider.groups.length })}
                            </span>
                          </div>
                        </div>

                        <div className="model-provider-added-actions">
                          <span className="model-provider-connection-badge">
                            <CheckCircleFilled />
                            {t("modelProvider.availableGroupCount", { count: provider.groups.filter((group) => group.verified).length })}
                          </span>
                          <Button icon={<PlusCircleOutlined />} onClick={() => openProviderConfig(provider)}>
                            {t("modelProvider.addGroup")}
                          </Button>
                          <Button
                            aria-controls={modelListId}
                            aria-expanded={isExpanded}
                            className="model-provider-expand-button"
                            onClick={() => void toggleProviderModels(provider.id)}
                          >
                            {isExpanded ? t("modelProvider.collapseGroups") : t("modelProvider.expandGroups")}
                            {isExpanded ? <UpOutlined /> : <DownOutlined />}
                          </Button>
                          <Popconfirm
                            cancelText={t("common.cancel")}
                            okButtonProps={{ danger: true }}
                            okText={t("modelProvider.remove")}
                            title={t("modelProvider.confirmRemoveProvider", { name: provider.name })}
                            description={t("modelProvider.confirmRemoveProviderDesc")}
                            onConfirm={() => deleteProvider(provider)}
                          >
                            <Button aria-label={t("modelProvider.removeProviderAria", { name: provider.name })} danger icon={<DeleteOutlined />} />
                          </Popconfirm>
                        </div>
                      </div>

                      {isExpanded ? (
                        <div
                          aria-label={t("modelProvider.providerModelListAria", { name: provider.name })}
                          className="model-provider-added-models"
                          id={modelListId}
                        >
                          <div className="model-provider-added-tags" aria-label={t("modelProvider.providerCapabilitiesAria", { name: provider.name })}>
                            {provider.capabilities.map((capability) => (
                              <CapabilityTag label={getCapabilityLabel(capability)} key={capability} />
                            ))}
                          </div>

                          <div className="model-provider-group-rows" aria-label={t("modelProvider.providerGroupsAria", { name: provider.name })}>
                            {provider.groups.map((group) => {
                              const verifyKey = `${provider.id}:${group.id}`;

                              return (
                                <div className="model-provider-group-row" key={group.id}>
                                  <div className="model-provider-group-header">
                                    <div className="model-provider-group-meta">
                                      <div className="model-provider-group-title-row">
                                        <strong>{group.name}</strong>
                                        <Tag className="model-provider-source-tag">source: {group.source}</Tag>
                                        <Tag className={group.verified ? "model-provider-verified-tag" : "model-provider-pending-tag"}>
                                          {group.verified ? t("modelProvider.verified") : t("modelProvider.pendingVerify")}
                                        </Tag>
                                      </div>
                                      <span>{group.baseUrl}</span>
                                    </div>

                                    <div className="model-provider-group-actions">
                                      <Button
                                        className="model-provider-group-toggle"
                                        loading={!!loadingGroupModelIds[`${provider.id}:${group.id}`]}
                                        onClick={() => void toggleGroupModels(provider.id, group.id)}
                                      >
                                        {expandedGroupIds[`${provider.id}:${group.id}`] ? t("modelProvider.collapseModels") : t("modelProvider.expandModels")}
                                        {expandedGroupIds[`${provider.id}:${group.id}`] ? <UpOutlined /> : <DownOutlined />}
                                      </Button>
                                      <Button icon={<PlusCircleOutlined />} onClick={() => openCustomModelModal(provider, group)}>
                                        {t("modelProvider.addModel")}
                                      </Button>
                                      <Button icon={<EditOutlined />} onClick={() => openProviderConfig(provider, group)}>
                                        {t("common.edit")}
                                      </Button>
                                      <Button
                                        icon={<KeyOutlined />}
                                        loading={!!verifyingGroupIds[verifyKey]}
                                        type={group.verified ? "default" : "primary"}
                                        onClick={() => verifyProviderGroup(provider.id, group.id)}
                                      >
                                        {group.verified ? t("modelProvider.reverify") : t("modelProvider.verify")}
                                      </Button>
                                      <Popconfirm
                                        cancelText={t("common.cancel")}
                                        okButtonProps={{ danger: true }}
                                        okText={t("common.delete")}
                                        title={t("modelProvider.confirmDeleteGroup", { name: group.name })}
                                        description={t("modelProvider.confirmDeleteGroupDesc")}
                                        onConfirm={() => deleteProviderGroup(provider.id, group)}
                                      >
                                        <Button aria-label={t("modelProvider.deleteGroupAria", { name: group.name })} danger icon={<DeleteOutlined />} />
                                      </Popconfirm>
                                    </div>
                                  </div>

                                  {expandedGroupIds[`${provider.id}:${group.id}`] ? (
                                    <div className="model-provider-branch-model-rows" aria-label={t("modelProvider.groupModelListAria", { name: group.name })}>
                                      {group.models.length ? (
                                        group.models.map((model) => (
                                          <div className="model-provider-model-row" key={model.id}>
                                            <div className="model-provider-model-meta">
                                              <strong>{model.name}</strong>
                                              <CapabilityTag label={getCapabilityLabel(model.capability)} />
                                              {model.builtIn ? null : <Tag className="model-provider-custom-tag">{t("modelProvider.custom")}</Tag>}
                                            </div>

                                            <div className="model-provider-model-actions">
                                              {model.builtIn ? (
                                                <span>{t("modelProvider.cannotDelete")}</span>
                                              ) : (
                                                <Popconfirm
                                                  cancelText={t("common.cancel")}
                                                  okButtonProps={{ danger: true }}
                                                  okText={t("common.delete")}
                                                  title={t("modelProvider.confirmDeleteModel", { name: model.name })}
                                                  description={t("modelProvider.confirmDeleteModelDesc")}
                                                  onConfirm={() => deleteCustomModel(provider.id, group.id, model)}
                                                >
                                                  <Button aria-label={t("modelProvider.deleteModelAria", { name: model.name })} icon={<DeleteOutlined />} />
                                                </Popconfirm>
                                              )}
                                            </div>
                                          </div>
                                        ))
                                      ) : (
                                        <div className="model-provider-model-empty">{t("modelProvider.noModels")}</div>
                                      )}
                                    </div>
                                  ) : null}
                                </div>
                              );
                            })}
                          </div>
                        </div>
                      ) : null}
                    </article>
                  );
                })
              ) : (
                <div className="model-provider-empty-state" role="status">
                  <Empty description={t("modelProvider.emptyAddedProviders")} image={Empty.PRESENTED_IMAGE_SIMPLE} />
                </div>
              )}
            </div>
          </section>
        </div>

        <aside className="model-provider-side-panel" aria-label={t("modelProvider.builtInProvidersAria")}>
          <div className="model-provider-side-header">
            <h2 className="model-provider-section-title">{t("modelProvider.builtInProvidersTitle")}</h2>
            <p className="model-provider-section-subtitle">{t("modelProvider.builtInProvidersSubtitle")}</p>
          </div>

          <Input
            allowClear
            aria-label={t("modelProvider.searchAria")}
            disabled={loading}
            placeholder={t("modelProvider.searchPlaceholder")}
            size="large"
            suffix={providerSearchLoading ? <LoadingOutlined /> : <SearchOutlined />}
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
          />

          <div className="model-provider-list">
            {visibleProviders.length ? (
              visibleProviders.map((provider) => {
                const isAdded = addedProviderIds.has(provider.id);
                const providerDescription = getLocalizedProviderDescription(
                  provider.name,
                  provider.backendDescription || provider.headline,
                  localizedFallbacks
                );

                return (
                  <article className={`model-provider-card${isAdded ? " is-added" : ""}`} key={provider.id}>
                    <div className="model-provider-card-header">
                      <div className="model-provider-card-brand">
                        <ProviderLogo provider={provider} />
                        <div>
                          <div className="model-provider-card-title-row">
                            <strong>{provider.name}</strong>
                            {isAdded ? <Tag className="model-provider-added-tag">{t("modelProvider.added")}</Tag> : null}
                          </div>
                          <Tooltip
                            overlayClassName="model-provider-description-tooltip"
                            placement="left"
                            title={renderDescriptionWithLinks(providerDescription)}
                          >
                            <p className="model-provider-card-description">{providerDescription}</p>
                          </Tooltip>
                        </div>
                      </div>
                    </div>

                    <div className="model-provider-card-foot">
                      <Button
                        className="model-provider-add-button"
                        icon={<PlusCircleOutlined />}
                        type="primary"
                        onClick={() => addProvider(provider)}
                      >
                        {isAdded ? t("modelProvider.addGroup") : t("modelProvider.configureAndAdd")}
                      </Button>
                    </div>
                  </article>
                );
              })
            ) : (
              <div className="model-provider-empty-state" role="status">
                <Empty description={t("modelProvider.noMatchedProviders")} image={Empty.PRESENTED_IMAGE_SIMPLE} />
              </div>
            )}
          </div>
        </aside>
      </section>

      <Modal
        centered
        confirmLoading={providerConfigSaving}
        destroyOnHidden
        maskClosable={!providerConfigSaving}
        okText={t("modelProvider.saveConfig")}
        open={!!configModal}
        title={t("modelProvider.groupConfigTitle", { name: configProvider?.name || "" })}
        width={520}
        onCancel={closeProviderConfig}
        onOk={() => providerConfigForm.submit()}
      >
        <Form<ProviderConfigFormValues>
          className="model-provider-form"
          form={providerConfigForm}
          layout="vertical"
          onFinish={saveProviderConfig}
        >
          <Form.Item
            extra={t("modelProvider.groupNameExtra")}
            label={t("modelProvider.groupName")}
            name="name"
            normalize={(value: string | undefined) => value?.trim()}
            rules={[
              { required: true, message: t("modelProvider.validation.groupNameRequired") },
              { max: 80, message: t("modelProvider.validation.groupNameMax") },
            ]}
          >
            <Input maxLength={80} placeholder={configProvider?.name || t("modelProvider.groupNamePlaceholder")} />
          </Form.Item>

          <Form.Item
            extra={baseUrlChanged ? t("modelProvider.baseUrlCustomExtra") : t("modelProvider.baseUrlDefaultExtra")}
            label="Base URL"
            name="baseUrl"
            normalize={(value: string | undefined) => value?.trim()}
            rules={[
              { required: true, message: t("modelProvider.validation.baseUrlRequired") },
              { type: "url", message: t("modelProvider.validation.baseUrlInvalid") },
              { max: 512, message: t("modelProvider.validation.baseUrlMax") },
            ]}
          >
            <Input maxLength={512} placeholder="https://api.example.com/v1" />
          </Form.Item>

          <Form.Item
            dependencies={["baseUrl"]}
            extra={baseUrlChanged ? t("modelProvider.apiKeyCustomExtra") : t("modelProvider.apiKeyDefaultExtra")}
            label="API Key"
            name="apiKey"
            normalize={(value: string | undefined) => value?.trim()}
            required={apiKeyRequired}
            rules={[
              {
                validator: (_, value?: string) => {
                  const apiKey = normalizeFormText(value);

                  if (apiKeyRequired && !apiKey) {
                    return Promise.reject(new Error(t("modelProvider.validation.apiKeyRequired")));
                  }

                  if (apiKey.length > 512) {
                    return Promise.reject(new Error(t("modelProvider.validation.apiKeyMax")));
                  }

                  if (/\s/.test(apiKey)) {
                    return Promise.reject(new Error(t("modelProvider.validation.apiKeyNoSpaces")));
                  }

                  return Promise.resolve();
                },
              },
            ]}
          >
            <Input.Password autoComplete="off" maxLength={512} placeholder={apiKeyRequired ? t("modelProvider.apiKeyPlaceholder") : t("modelProvider.apiKeyOptionalPlaceholder")} visibilityToggle />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        centered
        destroyOnHidden
        okText={t("modelProvider.add")}
        open={!!customModelModal}
        title={t("modelProvider.addCustomModelTitle", { name: customModelModal?.group.name || "" })}
        width={520}
        onCancel={closeCustomModelModal}
        onOk={() => customModelForm.submit()}
      >
        <Form<CustomModelFormValues>
          className="model-provider-form"
          form={customModelForm}
          layout="vertical"
          onFinish={addCustomModel}
        >
          <Form.Item label={t("modelProvider.provider")} name="providerId" rules={[{ required: true, message: t("modelProvider.validation.providerRequired") }]}>
            <Select disabled>
              {customModelModal ? (
                <Select.Option value={customModelModal.provider.id}>{customModelModal.provider.name}</Select.Option>
              ) : null}
            </Select>
          </Form.Item>

          <Form.Item label={t("modelProvider.group")} name="groupId" rules={[{ required: true, message: t("modelProvider.validation.groupRequired") }]}>
            <Select disabled>
              {customModelModal ? (
                <Select.Option value={customModelModal.group.id}>{customModelModal.group.name}</Select.Option>
              ) : null}
            </Select>
          </Form.Item>

          <Form.Item
            extra={t("modelProvider.modelNameExtra")}
            label={t("modelProvider.modelName")}
            name="name"
            normalize={(value: string | undefined) => value?.trim()}
            rules={[
              { required: true, message: t("modelProvider.validation.modelNameRequired") },
              { max: 120, message: t("modelProvider.validation.modelNameMax") },
              { pattern: /^[\w.-]+$/, message: t("modelProvider.validation.modelNamePattern") },
            ]}
          >
            <Input maxLength={120} placeholder={t("modelProvider.modelNamePlaceholder")} />
          </Form.Item>

          <Form.Item label={t("modelProvider.modelType")} name="capability" rules={[{ required: true, message: t("modelProvider.validation.modelTypeRequired") }]}>
            <Select>
              {customModelModal?.provider.capabilities.map((capability) => (
                <Select.Option key={capability} value={capability}>
                  {getCapabilityLabel(capability)}
                </Select.Option>
              ))}
            </Select>
          </Form.Item>
        </Form>
      </Modal>
    </main>
  );
}
