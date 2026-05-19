import { useCallback, useEffect, useMemo, useState } from "react";
import { Modal, Select, Switch, Tag, Tooltip, message } from "antd";
import { DownOutlined, QuestionCircleOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import { BASE_URL, axiosInstance, getLocalizedErrorMessage } from "@/components/request";

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
}

interface ApiModel {
  id: string;
  name: string;
  model_type?: string;
  is_default?: boolean;
}

interface SelectedModelApiItem {
  base_url?: string;
  group_name: string;
  model_id: string;
  model_type: string;
  name: string;
  provider_name: string;
  user_model_provider_group_id: string;
  user_model_provider_id: string;
}

type SelectedModels = Partial<Record<ModelCapability, string>>;

type ModelOptionItem = {
  provider: ProviderOption;
  group: ProviderConnectionGroup;
  model: ProviderModel;
  value: string;
};

const moduleConfigs: ModuleConfig[] = [
  {
    key: "LLM_CHAT",
    titleKey: "modelProvider.module.llmChatTitle",
    subtitleKey: "modelProvider.module.llmChatSubtitle",
    required: true,
  },
  {
    key: "EMBEDDING",
    titleKey: "modelProvider.module.embeddingTitle",
    subtitleKey: "modelProvider.module.embeddingSubtitle",
    required: true,
    restricted: true,
  },
  {
    key: "VLM",
    titleKey: "modelProvider.module.vlmTitle",
    subtitleKey: "modelProvider.module.vlmSubtitle",
  },
  {
    key: "RERANK",
    titleKey: "modelProvider.module.rerankTitle",
    subtitleKey: "modelProvider.module.rerankSubtitle",
  },
  {
    key: "ASR",
    titleKey: "modelProvider.module.asrTitle",
    subtitleKey: "modelProvider.module.asrSubtitle",
  },
  {
    key: "TTS",
    titleKey: "modelProvider.module.ttsTitle",
    subtitleKey: "modelProvider.module.ttsSubtitle",
  },
  {
    key: "TEXT_TO_IMAGE",
    titleKey: "modelProvider.module.textToImageTitle",
    subtitleKey: "modelProvider.module.textToImageSubtitle",
  },
  {
    key: "LLM_SELF_EVOLUTION",
    titleKey: "modelProvider.module.selfEvolutionTitle",
    subtitleKey: "modelProvider.module.selfEvolutionSubtitle",
  },
];

const visibleModuleConfigs = moduleConfigs.filter((module) => module.key !== "EMBEDDING");

enum ModelProviderModelType {
  VLM = "VLM",
  LLM = "llm",
  LLMChat = "llm-chat",
  LLMEvolution = "llm-evo",
  Embedding = "embedding",
  MultimodalEmbedding = "multimodal_embedding",
  TextToImage = "text2image",
  TTS = "tts",
  STT = "stt",
  Rerank = "rerank",
  ImageEditing = "image_editing",
}

const modelTypeByCapability: Record<ModelCapability, ModelProviderModelType> = {
  LLM_CHAT: ModelProviderModelType.LLM,
  EMBEDDING: ModelProviderModelType.Embedding,
  VLM: ModelProviderModelType.VLM,
  RERANK: ModelProviderModelType.Rerank,
  ASR: ModelProviderModelType.STT,
  TTS: ModelProviderModelType.TTS,
  TEXT_TO_IMAGE: ModelProviderModelType.TextToImage,
  MULTIMODAL_EMBEDDING: ModelProviderModelType.MultimodalEmbedding,
  IMAGE_EDITING: ModelProviderModelType.ImageEditing,
  LLM_SELF_EVOLUTION: ModelProviderModelType.LLM,
};

const selectedModelTypeByCapability: Record<ModelCapability, ModelProviderModelType> = {
  ...modelTypeByCapability,
  LLM_CHAT: ModelProviderModelType.LLMChat,
  LLM_SELF_EVOLUTION: ModelProviderModelType.LLMEvolution,
};

const selectedCapabilityByModelType: Record<string, ModelCapability> = {
  [ModelProviderModelType.LLMChat]: "LLM_CHAT",
  [ModelProviderModelType.LLMEvolution]: "LLM_SELF_EVOLUTION",
  llm: "LLM_CHAT",
  llm2: "LLM_SELF_EVOLUTION",
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

function createConnectionGroup(provider: ProviderOption, overrides: Partial<ProviderConnectionGroup> = {}): ProviderConnectionGroup {
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

function getModelValue(providerId: string, groupId: string, modelId: string) {
  return `${providerId}:${groupId}:${modelId}`;
}

function parseModelValue(value?: string) {
  const [providerId, groupId, ...modelIdParts] = String(value || "").split(":");
  return {
    providerId,
    groupId,
    modelId: modelIdParts.join(":"),
  };
}

function getCapabilityByModelType(modelType?: string): ModelCapability | undefined {
  const normalized = (modelType || "").toLowerCase();
  const selectedCapability = selectedCapabilityByModelType[normalized];
  if (selectedCapability) {
    return selectedCapability;
  }
  return visibleModuleConfigs.find((module) => selectedModelTypeByCapability[module.key].toLowerCase() === normalized)?.key;
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

async function modelProviderRequest<T>(method: "GET" | "PUT", path: string, data?: unknown) {
  const response = await axiosInstance.request<ApiEnvelope<T> | T>({
    method,
    url: `${getApiBaseUrl()}${path}`,
    data,
    headers: getRequestHeaders(),
  });
  return unwrapResponse<T>(response.data);
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
    capabilities: [],
    models: [],
  };
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

export default function DefaultModelConfigPanel() {
  const { t, i18n } = useTranslation();
  const [providerOptions, setProviderOptions] = useState<ProviderOption[]>([]);
  const [selectedModels, setSelectedModels] = useState<SelectedModels>({});
  const [moduleModelOptions, setModuleModelOptions] = useState<Partial<Record<ModelCapability, ModelOptionItem[]>>>({});
  const [moduleModelLoading, setModuleModelLoading] = useState<Partial<Record<ModelCapability, boolean>>>({});
  const [shareDefaultConfig, setShareDefaultConfig] = useState(true);
  const localizedFallbacks = useMemo(() => createModelProviderFallbacks(t), [i18n.language, t]);

  const loadDefaultModelState = useCallback(async () => {
    try {
      const providerData = await modelProviderRequest<{ providers?: ApiProvider[] }>("GET", "/model_providers");
      const providers = (providerData.providers || []).map((provider) => mapApiProvider(provider, localizedFallbacks));
      setProviderOptions(providers);

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
            base_url: selection.base_url,
          }, localizedFallbacks);
        const group = createConnectionGroup(provider, {
          id: selection.user_model_provider_group_id,
          name: selection.group_name,
          baseUrl: selection.base_url || provider.baseUrl,
          apiKeyConfigured: true,
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
          value: getModelValue(provider.id, group.id, model.id),
        };
        nextSelectedModels[capability] = option.value;
        selectedOptions[capability] = [option, ...(selectedOptions[capability] || [])];
      });

      setSelectedModels(nextSelectedModels);
      setModuleModelOptions((current) => ({ ...selectedOptions, ...current }));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.loadProvidersFailed")));
    }
  }, [localizedFallbacks, t]);

  useEffect(() => {
    void loadDefaultModelState();
  }, [loadDefaultModelState]);

  const loadModuleModels = async (capability: ModelCapability, force = false) => {
    if (!force && moduleModelOptions[capability]) {
      return;
    }
    if (moduleModelLoading[capability]) {
      return;
    }

    setModuleModelLoading((current) => ({ ...current, [capability]: true }));
    try {
      const modelType = modelTypeByCapability[capability];
      const data = await modelProviderRequest<{ models?: Array<ApiModel & {
        user_model_provider_id: string;
        user_model_provider_group_id: string;
        provider_name: string;
        group_name: string;
        base_url?: string;
      }> }>("GET", `/model_providers/models?model_type=${encodeURIComponent(modelType)}`);
      const options = (data.models || []).map((model) => {
        const provider =
          providerOptions.find((item) => item.id === model.user_model_provider_id) ||
          mapApiProvider({
            id: model.user_model_provider_id,
            name: model.provider_name,
            base_url: model.base_url,
          }, localizedFallbacks);
        const group = createConnectionGroup(provider, {
          id: model.user_model_provider_group_id,
          name: model.group_name,
          baseUrl: model.base_url || provider.baseUrl,
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

  useEffect(() => {
    Object.entries(selectedModels).forEach(([capability, value]) => {
      if (value && !moduleModelOptions[capability as ModelCapability]) {
        void loadModuleModels(capability as ModelCapability);
      }
    });
  }, [selectedModels, moduleModelOptions]);

  const saveSelectedModel = async (capability: ModelCapability, value?: string) => {
    const modelType = selectedModelTypeByCapability[capability];
    const selections = [
      {
        model_type: modelType,
        model_id: value ? parseModelValue(value).modelId : "",
      },
    ];

    await modelProviderRequest<{ selections?: SelectedModelApiItem[] }>("PUT", "/model_providers/selected_models", {
      selections,
    });
  };

  const applyModelSelection = (capability: ModelCapability, value?: string) => {
    setSelectedModels((current) => ({
      ...current,
      [capability]: value,
    }));
    void saveSelectedModel(capability, value).catch((error) => {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.error.saveDefaultModelFailed")));
    });
  };

  const handleModelSelection = (capability: ModelCapability, value?: string) => {
    const previousValue = selectedModels[capability];
    if (capability === "EMBEDDING" && previousValue && previousValue !== value) {
      Modal.confirm({
        title: t("modelProvider.embeddingChangeTitle"),
        content: t("modelProvider.embeddingChangeContent"),
        okText: t("modelProvider.confirmSwitch"),
        cancelText: t("modelProvider.cancelSwitch"),
        onOk: () => {
          applyModelSelection(capability, value);
        },
      });
      return;
    }

    applyModelSelection(capability, value);
  };

  return (
    <section className="model-provider-config-panel" aria-label={t("modelProvider.defaultConfigAria")}>
      <div className="model-provider-panel-title-row">
        <div>
          <h2 className="model-provider-section-title">{t("modelProvider.defaultTitle")}</h2>
          <p className="model-provider-section-subtitle">{t("modelProvider.defaultSubtitle")}</p>
        </div>
        <div className="model-provider-share-control">
          <span>{t("modelProvider.shareConfigTitle")}</span>
          <Switch
            checked={shareDefaultConfig}
            onChange={setShareDefaultConfig}
            aria-label={t("modelProvider.shareConfigTitle")}
          />
        </div>
      </div>

      <div className="model-provider-default-list">
        {visibleModuleConfigs.map((module) => {
          const options = moduleModelOptions[module.key] || [];
          const optionLoading = Boolean(moduleModelLoading[module.key]);
          const moduleTitle = t(module.titleKey);
          const moduleSubtitle = t(module.subtitleKey);

          return (
            <div className="model-provider-default-row" key={module.key}>
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
                {module.restricted ? <Tag className="model-provider-limited-tag">{t("modelProvider.limited")}</Tag> : null}
              </div>

              <Select
                allowClear={!module.required}
                className="model-provider-model-select"
                id={`model-provider-${module.key.toLowerCase()}`}
                listHeight={340}
                optionLabelProp="label"
                placeholder={module.required ? t("modelProvider.requiredModelPlaceholder") : t("modelProvider.optionalModelPlaceholder")}
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
  );
}
