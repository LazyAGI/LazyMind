import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Button, Empty, Form, Input, Modal, Popconfirm, Select, Tag, Tooltip, message } from "antd";
import { useTranslation } from "react-i18next";
import {
  CheckCircleFilled,
  DeleteOutlined,
  DownOutlined,
  EditOutlined,
  KeyOutlined,
  LoadingOutlined,
  PlusCircleOutlined,
  SearchOutlined,
  UpOutlined,
} from "@ant-design/icons";
import { getLocalizedErrorMessage } from "@/components/request";
import { modelProvidersApi, unwrapModelProviderData } from "../api";
import "../index.scss";

type ModelCapability =
  | "LLM_CHAT"
  | "EMBEDDING"
  | "VLM"
  | "RERANK"
  | "ASR"
  | "TTS"
  | "TEXT_TO_IMAGE"
  | "MULTIMODAL_EMBEDDING"
  | "IMAGE_EDITING"
  | "LLM_SELF_EVOLUTION";

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
  models: ProviderModel[];
}

interface ProviderConnectionGroup {
  id: string;
  name: string;
  source: string;
  baseUrl: string;
  apiKeyPreview?: string;
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

interface ProviderConfigFormValues {
  name?: string;
  apiKey?: string;
  baseUrl?: string;
}

interface VerifyGroupModalState {
  provider: AddedProvider;
  group: ProviderConnectionGroup;
}

interface VerifyGroupFormValues {
  apiKey?: string;
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

const capabilityLabelKeys: Record<ModelCapability, string> = {
  LLM_CHAT: "modelProvider.capability.llmChat",
  EMBEDDING: "modelProvider.capability.embedding",
  VLM: "modelProvider.capability.vlm",
  RERANK: "modelProvider.capability.rerank",
  ASR: "modelProvider.capability.asr",
  TTS: "modelProvider.capability.tts",
  TEXT_TO_IMAGE: "modelProvider.capability.textToImage",
  MULTIMODAL_EMBEDDING: "modelProvider.capability.multimodalEmbedding",
  IMAGE_EDITING: "modelProvider.capability.imageEditing",
  LLM_SELF_EVOLUTION: "modelProvider.capability.selfEvolution",
};

const builtInProviders: ProviderOption[] = [
  {
    id: "tongyi",
    name: "Tongyi-Qianwen",
    brand: "通义",
    headline: "覆盖文本、向量、多模态、语音与重排序能力，适合作为默认全能供应商。",
    source: "tongyi",
    baseUrl: "https://dashscope.aliyuncs.com/",
    capabilities: ["LLM_CHAT", "EMBEDDING", "VLM", "RERANK", "ASR", "TTS", "TEXT_TO_IMAGE"],
    models: [
      { id: "qwen-plus", name: "qwen-plus", capability: "LLM_CHAT", builtIn: true, enabled: true },
      { id: "deepseek-r1", name: "deepseek-r1", capability: "LLM_CHAT", builtIn: true, enabled: true },
      { id: "text-embedding-v2", name: "text-embedding-v2", capability: "EMBEDDING", builtIn: true, enabled: true },
      { id: "qwen-vl-max", name: "qwen-vl-max", capability: "VLM", builtIn: true, enabled: true },
      { id: "gte-rerank", name: "gte-rerank", capability: "RERANK", builtIn: true, enabled: true },
      { id: "qwen3-asr-flash", name: "qwen3-asr-flash", capability: "ASR", builtIn: true, enabled: true },
      { id: "sambert-zhide-v1", name: "sambert-zhide-v1", capability: "TTS", builtIn: true, enabled: true },
      { id: "wanx2-1-t2i-turbo", name: "wanx2.1-t2i-turbo", capability: "TEXT_TO_IMAGE", builtIn: true, enabled: true },
    ],
  },
  {
    id: "openai",
    name: "OpenAI",
    brand: "◎",
    headline: "通用模型生态完整，适合接入对话、向量、语音与多模态任务。",
    source: "openai",
    baseUrl: "https://api.openai.com/v1/",
    capabilities: ["LLM_CHAT", "EMBEDDING", "VLM", "TTS", "ASR"],
    models: [
      { id: "gpt-4-1", name: "gpt-4.1", capability: "LLM_CHAT", builtIn: true, enabled: true },
      { id: "gpt-4o", name: "gpt-4o", capability: "VLM", builtIn: true, enabled: true },
      { id: "text-embedding-3-large", name: "text-embedding-3-large", capability: "EMBEDDING", builtIn: true, enabled: true },
      { id: "whisper-1", name: "whisper-1", capability: "ASR", builtIn: true, enabled: true },
      { id: "gpt-4o-mini-tts", name: "gpt-4o-mini-tts", capability: "TTS", builtIn: true, enabled: true },
    ],
  },
  {
    id: "anthropic",
    name: "Anthropic",
    brand: "AI",
    headline: "长文本和稳健推理体验突出，适合高质量文本对话场景。",
    source: "anthropic",
    baseUrl: "https://api.anthropic.com/v1/",
    capabilities: ["LLM_CHAT", "VLM"],
    models: [
      { id: "claude-sonnet-4-5", name: "claude-sonnet-4.5", capability: "LLM_CHAT", builtIn: true, enabled: true },
      { id: "claude-opus-4-1", name: "claude-opus-4.1", capability: "LLM_CHAT", builtIn: true, enabled: true },
    ],
  },
  {
    id: "deepseek",
    name: "DeepSeek",
    brand: "DS",
    headline: "推理模型性价比高，适合默认问答主模型或自进化任务。",
    source: "deepseek",
    baseUrl: "https://api.deepseek.com",
    capabilities: ["LLM_CHAT", "LLM_SELF_EVOLUTION"],
    models: [
      { id: "deepseek-chat", name: "deepseek-chat", capability: "LLM_CHAT", builtIn: true, enabled: true },
      { id: "deepseek-reasoner", name: "deepseek-reasoner", capability: "LLM_SELF_EVOLUTION", builtIn: true, enabled: true },
    ],
  },
];

function createConnectionGroup(provider: ProviderOption, overrides: Partial<ProviderConnectionGroup> = {}): ProviderConnectionGroup {
  return {
    id: overrides.id || `${provider.id}-default`,
    name: overrides.name || provider.name,
    source: provider.source,
    baseUrl: overrides.baseUrl || provider.baseUrl,
    apiKeyPreview: overrides.apiKeyPreview,
    apiKeyConfigured: overrides.apiKeyConfigured ?? false,
    verified: overrides.verified ?? false,
    models: overrides.models || provider.models.map((model) => ({ ...model })),
  };
}

enum ModelProviderModelType {
  VLM = "VLM",
  LLM = "llm",
  Embedding = "embedding",
  MultimodalEmbedding = "multimodal_embedding",
  TextToImage = "text2image",
  TTS = "tts",
  STT = "stt",
  Rerank = "rerank",
  ImageEditing = "image_editing",
}

const modelTypeByCapability: Record<ModelCapability, ModelProviderModelType> = {
  EMBEDDING: ModelProviderModelType.Embedding,
  VLM: ModelProviderModelType.VLM,
  RERANK: ModelProviderModelType.Rerank,
  ASR: ModelProviderModelType.STT,
  TTS: ModelProviderModelType.TTS,
  TEXT_TO_IMAGE: ModelProviderModelType.TextToImage,
  MULTIMODAL_EMBEDDING: ModelProviderModelType.MultimodalEmbedding,
  IMAGE_EDITING: ModelProviderModelType.ImageEditing,
  LLM_CHAT: ModelProviderModelType.LLM,
  LLM_SELF_EVOLUTION: ModelProviderModelType.LLM,
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

function mapModelTypeToCapability(modelType?: string): ModelCapability {
  const normalized = (modelType || "").toLowerCase();
  if (normalized === ModelProviderModelType.MultimodalEmbedding) return "MULTIMODAL_EMBEDDING";
  if (normalized.includes("embedding")) return "EMBEDDING";
  if (normalized.includes("rerank")) return "RERANK";
  if (normalized === ModelProviderModelType.STT || normalized === "asr") return "ASR";
  if (normalized === ModelProviderModelType.TTS) return "TTS";
  if (normalized === ModelProviderModelType.ImageEditing) return "IMAGE_EDITING";
  if (normalized === ModelProviderModelType.TextToImage) return "TEXT_TO_IMAGE";
  if (normalized === ModelProviderModelType.VLM.toLowerCase() || normalized.includes("vision")) return "VLM";
  return "LLM_CHAT";
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

interface ApiProvider {
  id: string;
  name: string;
  description?: string;
  base_url?: string;
}

interface ApiGroup {
  id: string;
  name: string;
  base_url?: string;
  api_key?: string;
  api_key_configured?: boolean;
  api_key_preview?: string;
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
    capabilities: [
      "LLM_CHAT",
      "EMBEDDING",
      "MULTIMODAL_EMBEDDING",
      "VLM",
      "RERANK",
      "ASR",
      "TTS",
      "TEXT_TO_IMAGE",
      "IMAGE_EDITING",
    ],
    models: [],
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
    apiKeyPreview: isApiGroup
      ? getSafeApiKeyPreview((group as ApiGroup).api_key_preview || (group as ApiGroup).api_key)
      : (group as ProviderConnectionGroup).apiKeyPreview,
    apiKeyConfigured: isApiGroup
      ? Boolean((group as ApiGroup).api_key_configured || (group as ApiGroup).api_key)
      : (group as ProviderConnectionGroup).apiKeyConfigured,
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

function maskApiKey(value?: string) {
  const normalized = normalizeFormText(value);
  if (!normalized) {
    return "";
  }
  if (normalized.length <= 8) {
    return "********";
  }
  return `${normalized.slice(0, 4)}...${normalized.slice(-4)}`;
}

function getSafeApiKeyPreview(value?: string) {
  return maskApiKey(value) || "********";
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

export default function ModelProviderPage() {
  const { t, i18n } = useTranslation();
  const [providerConfigForm] = Form.useForm<ProviderConfigFormValues>();
  const [customModelForm] = Form.useForm<CustomModelFormValues>();
  const [verifyGroupForm] = Form.useForm<VerifyGroupFormValues>();

  const [providerOptions, setProviderOptions] = useState<ProviderOption[]>(builtInProviders);
  const [addedProviderList, setAddedProviderList] = useState<AddedProvider[]>([]);
  const [configModal, setConfigModal] = useState<ProviderConfigModalState | null>(null);
  const [customModelModal, setCustomModelModal] = useState<CustomModelModalState | null>(null);
  const [verifyGroupModal, setVerifyGroupModal] = useState<VerifyGroupModalState | null>(null);
  const [expandedProviderIds, setExpandedProviderIds] = useState<Record<string, boolean>>({});
  const [keyword, setKeyword] = useState("");
  const [loading, setLoading] = useState(false);
  const [providerSearchLoading, setProviderSearchLoading] = useState(false);
  const [providerConfigSaving, setProviderConfigSaving] = useState(false);
  const [verifyingGroupIds, setVerifyingGroupIds] = useState<Record<string, boolean>>({});
  const [expandedGroupIds, setExpandedGroupIds] = useState<Record<string, boolean>>({});
  const [loadingGroupModelIds, setLoadingGroupModelIds] = useState<Record<string, boolean>>({});
  const watchedProviderBaseUrl = Form.useWatch("baseUrl", providerConfigForm);
  const providerSearchRequestIdRef = useRef(0);
  const initialProvidersLoadedRef = useRef(false);
  const localizedFallbacks = useMemo(() => createModelProviderFallbacks(t), [i18n.language, t]);
  const getCapabilityLabel = useCallback((capability: ModelCapability) => t(capabilityLabelKeys[capability]), [t]);
  const configProvider = configModal?.provider || null;
  const activeVerifyKey = verifyGroupModal
    ? `${verifyGroupModal.provider.id}:${verifyGroupModal.group.id}`
    : "";
  const verifyGroupBusy = activeVerifyKey ? Boolean(verifyingGroupIds[activeVerifyKey]) : false;
  const baseUrlChanged = configProvider
    ? !isDefaultProviderBaseUrl(
        configProvider,
        watchedProviderBaseUrl ?? providerConfigForm.getFieldValue("baseUrl") ?? configProvider.baseUrl
      )
    : false;
  const apiKeyRequired = !!configProvider && !baseUrlChanged;

  const fetchProviderOptions = useCallback(async (searchKeyword = "") => {
    const providerResponse = await modelProvidersApi.apiCoreModelProvidersGet({
      keyword: searchKeyword.trim() || undefined,
    });
    const providerData = unwrapModelProviderData<{ providers?: ApiProvider[] }>(providerResponse.data);
    return (providerData.providers || []).map((provider) => mapApiProvider(provider, localizedFallbacks));
  }, [localizedFallbacks]);

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

      const withGroupsResponse = await modelProvidersApi.apiCoreModelProvidersWithGroupsGet();
      const withGroupsData = unwrapModelProviderData<{ providers?: ApiProvider[] }>(withGroupsResponse.data);
      const addedIds = new Set((withGroupsData.providers || []).map((provider) => provider.id));
      const addedProviders = await Promise.all(
        providers
          .filter((provider) => addedIds.has(provider.id))
          .map(async (provider): Promise<AddedProvider> => {
            const groupResponse = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet({
              modelProviderId: provider.id,
            });
            const groupData = unwrapModelProviderData<{ groups?: ApiGroup[] }>(groupResponse.data);
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

  const openProviderConfig = (provider: AddedProvider | ProviderOption, group?: ProviderConnectionGroup) => {
    const configuredProvider = addedProviderList.find((item) => item.id === provider.id);
    const providerDraft = configuredProvider || provider;
    const groupDraft = group || createConnectionGroup(providerDraft);

    setConfigModal({ provider: providerDraft, group });
    providerConfigForm.setFieldsValue({
      name: groupDraft.name,
      apiKey: "",
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
        verify: false,
        ...(apiKey ? { api_key: apiKey } : {}),
      };
      const savedGroup = activeConfigModal.group
        ? unwrapModelProviderData<ApiGroup>((await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdPatch({
            modelProviderId: configProvider.id,
            groupId: activeConfigModal.group.id,
            updateModelProviderGroupOpenAPIRequest: payload,
          })).data)
        : unwrapModelProviderData<ApiGroup>((await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsPost({
            modelProviderId: configProvider.id,
            createModelProviderGroupOpenAPIRequest: payload,
          })).data);
      const nextGroup = mapApiGroup(
        configProvider,
        {
          ...savedGroup,
          api_key_configured: Boolean(apiKey || existingGroup?.apiKeyConfigured || savedGroup.api_key_configured || savedGroup.api_key),
          api_key_preview: apiKey ? maskApiKey(apiKey) : existingGroup?.apiKeyPreview || savedGroup.api_key_preview,
        },
        existingGroup?.models || []
      );

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
      setExpandedProviderIds((current) => ({ ...current, [configProvider.id]: true }));
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

  const verifyProviderGroup = async (providerId: string, groupId: string, apiKey?: string) => {
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
      if (!apiKey && !group.apiKeyConfigured) {
        message.warning(t("modelProvider.message.fillApiKeyBeforeVerify"));
        return;
      }

      const payload = {
        provider_name: provider.name,
        base_url: group.baseUrl,
        api_key: apiKey || "",
        dry_run: false,
      };
      const checkResponse = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdCheckPost(
        {
          modelProviderId: provider.id,
          groupId: group.id,
          checkModelProviderOpenAPIRequest: payload,
        },
        { timeout: 3 * 60 * 1000 },
      );
      const checkResult = unwrapModelProviderData<CheckModelProviderResult>(checkResponse.data);
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

  const openVerifyGroupModal = (provider: AddedProvider, group: ProviderConnectionGroup) => {
    setVerifyGroupModal({ provider, group });
    verifyGroupForm.resetFields();
  };

  const closeVerifyGroupModal = () => {
    if (!verifyGroupModal) {
      return;
    }
    if (verifyGroupBusy) {
      return;
    }
    setVerifyGroupModal(null);
    verifyGroupForm.resetFields();
  };

  const submitVerifyGroup = async (values: VerifyGroupFormValues) => {
    if (!verifyGroupModal) {
      return;
    }
    await verifyProviderGroup(
      verifyGroupModal.provider.id,
      verifyGroupModal.group.id,
      normalizeFormText(values.apiKey)
    );
    setVerifyGroupModal(null);
    verifyGroupForm.resetFields();
  };

  const deleteProviderGroup = async (providerId: string, group: ProviderConnectionGroup) => {
    const provider = addedProviderList.find((item) => item.id === providerId);
    if (!provider) {
      return;
    }

    try {
      await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdDelete({
        modelProviderId: providerId,
        groupId: group.id,
      });
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
      setExpandedGroupIds((current) => {
        const next = { ...current };
        delete next[`${providerId}:${group.id}`];
        return next;
      });
      message.success(t("modelProvider.message.groupRemoved", { name: group.name }));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.deleteGroupFailed")));
    }
  };

  const deleteProvider = async (provider: AddedProvider) => {
    try {
      await Promise.all(
        provider.groups.map((group) =>
          modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdDelete({
            modelProviderId: provider.id,
            groupId: group.id,
          })
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
      const modelResponse = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet({
        modelProviderId: provider.id,
        groupId: group.id,
      });
      const modelData = unwrapModelProviderData<{ models?: ApiModel[] }>(modelResponse.data);
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
      capability: provider.capabilities[0] || "LLM_CHAT",
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
      const createdModel = unwrapModelProviderData<ApiModel>((await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsPost({
        modelProviderId: provider.id,
        groupId: group.id,
        addModelProviderGroupModelOpenAPIRequest: {
          name: values.name.trim(),
          model_type: modelTypeByCapability[values.capability],
        },
      })).data);
      const nextModel: ProviderModel = {
        id: createdModel.id,
        name: createdModel.name,
        capability: mapModelTypeToCapability(createdModel.model_type || modelTypeByCapability[values.capability]),
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
      message.success(t("modelProvider.message.modelAdded"));
      closeCustomModelModal();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.addModelFailed")));
    }
  };

  const deleteCustomModel = async (providerId: string, groupId: string, model: ProviderModel) => {
    try {
      await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdDelete({
        modelProviderId: providerId,
        groupId,
        modelId: model.id,
      });
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
      message.success(t("modelProvider.message.modelDeleted"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.deleteModelFailed")));
    }
  };

  return (
    <div className="model-provider-page-content">
      <section className="model-provider-shell">
        <div className="model-provider-main-panel">
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
                                        onClick={() => openVerifyGroupModal(provider, group)}
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
            <Input.Password
              autoComplete="off"
              maxLength={512}
              placeholder={apiKeyRequired ? t("modelProvider.apiKeyPlaceholder") : t("modelProvider.apiKeyOptionalPlaceholder")}
              visibilityToggle={false}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        centered
        confirmLoading={verifyGroupBusy}
        destroyOnHidden
        maskClosable={!verifyGroupBusy}
        okText={t("modelProvider.verify")}
        open={!!verifyGroupModal}
        title={t("modelProvider.verifyGroupTitle", { name: verifyGroupModal?.group.name || "" })}
        width={520}
        onCancel={closeVerifyGroupModal}
        onOk={() => verifyGroupForm.submit()}
      >
        <Form<VerifyGroupFormValues>
          className="model-provider-form"
          form={verifyGroupForm}
          layout="vertical"
          onFinish={submitVerifyGroup}
        >
          <Form.Item label="Base URL">
            <Input value={verifyGroupModal?.group.baseUrl || ""} readOnly />
          </Form.Item>
          {verifyGroupModal?.group.apiKeyConfigured ? (
            <div className="model-provider-key-status" role="status">
              <KeyOutlined />
              <span>
                {t("modelProvider.keyConfiguredStatus", {
                  preview: getSafeApiKeyPreview(verifyGroupModal.group.apiKeyPreview),
                })}
              </span>
            </div>
          ) : null}
          <Form.Item
            extra={
              verifyGroupModal?.group.apiKeyConfigured
                ? t("modelProvider.verifyConfiguredApiKeyExtra")
                : t("modelProvider.verifyApiKeyExtra")
            }
            label="API Key"
            name="apiKey"
            normalize={(value: string | undefined) => value?.trim()}
            rules={[
              {
                required: !verifyGroupModal?.group.apiKeyConfigured,
                message: t("modelProvider.validation.apiKeyRequired"),
              },
              { max: 512, message: t("modelProvider.validation.apiKeyMax") },
              {
                validator: (_, value?: string) =>
                  /\s/.test(normalizeFormText(value))
                    ? Promise.reject(new Error(t("modelProvider.validation.apiKeyNoSpaces")))
                    : Promise.resolve(),
              },
            ]}
          >
            <Input.Password
              autoComplete="off"
              maxLength={512}
              placeholder={t("modelProvider.verifyApiKeyPlaceholder")}
              visibilityToggle={false}
            />
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
    </div>
  );
}
