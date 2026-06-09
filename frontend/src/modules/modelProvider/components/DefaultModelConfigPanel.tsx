import { useCallback, useEffect, useMemo, useState } from "react";
import { Modal, Select, Switch, Tag, Tooltip, message } from "antd";
import {
  CheckCircleOutlined,
  CloudServerOutlined,
  CompassOutlined,
  DownOutlined,
  FilePdfOutlined,
  GoogleOutlined,
  MinusCircleOutlined,
  QuestionCircleOutlined,
  ScanOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { AgentAppsAuth } from "@/components/auth";
import { getLocalizedErrorMessage } from "@/components/request";
import { useModelFeatures } from "@/hooks/useModelFeatures";
import {
  modelProvidersApi,
  modelProvidersDefaultApi,
  unwrapModelProviderData,
  withModelProviderJsonOptions,
} from "../api";

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
  name: string;
  model_type?: string;
  is_default?: boolean;
}

interface SelectedModelApiItem {
  base_url?: string;
  group_name: string;
  model_id: string;
  model_key: string;
  name: string;
  provider_name: string;
  share?: boolean;
  user_model_provider_group_id: string;
  user_model_provider_id: string;
}

type SelectedModels = Partial<Record<ModelCapability, string>>;

type CloudServiceSlotKey = "cloudParsing" | "searchEngine";
type CloudServiceCategory = "ocr" | "search";

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
};

interface CloudServiceConfig {
  key: CloudServiceSlotKey;
  titleKey: string;
  subtitleKey: string;
  category: CloudServiceCategory;
}

interface CloudServiceOption {
  baseUrl: string;
  groupId: string;
  groupName: string;
  icon: JSX.Element;
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

interface VerifiedCloudServiceResponse {
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

interface ModelReadyResponse {
  ready: boolean;
  source?: string;
  shared_by_name?: string;
  shared_by_id?: string;
  provider_name?: string;
  model_name?: string;
}

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
    key: "evo_llm",
    titleKey: "modelProvider.module.selfEvolutionTitle",
    subtitleKey: "modelProvider.module.selfEvolutionSubtitle",
  },
];

const cloudServiceConfigs: CloudServiceConfig[] = [
  {
    key: "cloudParsing",
    titleKey: "modelProvider.module.cloudParsingServiceTitle",
    subtitleKey: "modelProvider.module.cloudParsingServiceSubtitle",
    category: "ocr",
  },
  {
    key: "searchEngine",
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
};

function normalizeProviderKey(value: string) {
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
    translatedDescription ||
    fallbackDescription ||
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

function getCloudServiceIcon(
  providerName: string,
  category: CloudServiceCategory,
) {
  const normalizedName = normalizeProviderKey(providerName).replace(/-/g, "");
  if (normalizedName.includes("mineru")) {
    return <FilePdfOutlined />;
  }
  if (normalizedName.includes("paddle") || normalizedName.includes("ocr")) {
    return <ScanOutlined />;
  }
  if (normalizedName.includes("bing")) {
    return <SearchOutlined />;
  }
  if (normalizedName.includes("google")) {
    return <GoogleOutlined />;
  }
  if (normalizedName.includes("tavily")) {
    return <CompassOutlined />;
  }
  return category === "ocr" ? <ScanOutlined /> : <SearchOutlined />;
}

function mapVerifiedCloudServiceGroup(
  group: VerifiedCloudServiceGroup,
  category: CloudServiceCategory,
): CloudServiceOption {
  return {
    baseUrl: group.base_url,
    groupId: group.group_id,
    groupName: group.group_name,
    icon: getCloudServiceIcon(group.provider_name, category),
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

function getModelReadyTooltip(
  t: ReturnType<typeof useTranslation>["t"],
  readyStatus?: ModelReadyResponse,
) {
  if (!readyStatus) {
    return undefined;
  }
  if (!readyStatus.ready) {
    return t("modelProvider.modelNotReadyTip");
  }
  if (
    readyStatus.source === "shared" &&
    readyStatus.shared_by_name &&
    readyStatus.provider_name &&
    readyStatus.model_name
  ) {
    return t("modelProvider.modelReadySharedTip", {
      user: readyStatus.shared_by_name,
      provider: readyStatus.provider_name,
      model: readyStatus.model_name,
    });
  }
  return t("modelProvider.modelReadyTip");
}

function getCloudServiceReadyTooltip(
  t: ReturnType<typeof useTranslation>["t"],
  readyStatus?: VerifiedCloudServiceResponse,
) {
  if (!readyStatus) {
    return undefined;
  }
  if (!readyStatus.ready) {
    return t("modelProvider.cloudServiceNotReadyTip");
  }
  if (
    readyStatus.source === "shared" &&
    readyStatus.shared_by_name &&
    readyStatus.provider_name &&
    readyStatus.group_name
  ) {
    return t("modelProvider.cloudServiceReadySharedTip", {
      user: readyStatus.shared_by_name,
      provider: readyStatus.provider_name,
      group: readyStatus.group_name,
    });
  }
  return t("modelProvider.cloudServiceReadyTip");
}

function ProviderLogo({
  provider,
  compact = false,
}: {
  provider: ProviderOption;
  compact?: boolean;
}) {
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
  const [selectedCloudServices, setSelectedCloudServices] =
    useState<SelectedCloudServices>({});
  const [cloudServiceShareStatus, setCloudServiceShareStatus] = useState<
    Partial<Record<CloudServiceSlotKey, boolean>>
  >({});
  const [cloudServiceOptions, setCloudServiceOptions] = useState<
    Partial<Record<CloudServiceSlotKey, CloudServiceOption[]>>
  >({});
  const [cloudServiceLoading, setCloudServiceLoading] = useState<
    Partial<Record<CloudServiceSlotKey, boolean>>
  >({});
  const [cloudServiceSearchKeywords, setCloudServiceSearchKeywords] = useState<
    Partial<Record<CloudServiceSlotKey, string>>
  >({});
  const [cloudServiceReadyStatus, setCloudServiceReadyStatus] =
    useState<CloudServiceReadyStatus>({});
  const [moduleModelOptions, setModuleModelOptions] = useState<
    Partial<Record<ModelCapability, ModelOptionItem[]>>
  >({});
  const [moduleModelLoading, setModuleModelLoading] = useState<
    Partial<Record<ModelCapability, boolean>>
  >({});
  const [moduleModelSearchKeywords, setModuleModelSearchKeywords] = useState<
    Partial<Record<ModelCapability, string>>
  >({});
  const [shareStatus, setShareStatus] = useState<
    Partial<Record<ModelCapability, boolean>>
  >({});
  const [modelReadyStatus, setModelReadyStatus] = useState<ModelReadyStatus>(
    {},
  );
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
    [i18n.language, t],
  );

  const loadDefaultModelState = useCallback(async () => {
    try {
      const providerResponse = await modelProvidersApi.apiCoreModelProvidersGet();
      const providerData = unwrapModelProviderData<{ providers?: ApiProvider[] }>(providerResponse.data);
      const providers = (providerData.providers || []).map((provider) =>
        mapApiProvider(provider, localizedFallbacks),
      );
      setProviderOptions(providers);

      const selectedResponse = await modelProvidersApi.apiCoreModelProvidersSelectedModelsGet();
      const selectedData = unwrapModelProviderData<{ selections?: SelectedModelApiItem[] }>(selectedResponse.data);
      const nextSelectedModels: SelectedModels = {};
      const selectedOptions: Partial<
        Record<ModelCapability, ModelOptionItem[]>
      > = {};

      (selectedData.selections || []).forEach((selection) => {
        const capability = getCapabilityByModelType(selection.model_key);
        if (!capability) {
          return;
        }
        const provider =
          providers.find(
            (item) => item.id === selection.user_model_provider_id,
          ) ||
          mapApiProvider(
            {
              id: selection.user_model_provider_id,
              name: selection.provider_name,
              base_url: selection.base_url,
            },
            localizedFallbacks,
          );
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
        selectedOptions[capability] = [
          option,
          ...(selectedOptions[capability] || []),
        ];
      });

      setSelectedModels(nextSelectedModels);
      setModuleModelOptions((current) => ({ ...selectedOptions, ...current }));

      const nextShareStatus: Partial<Record<ModelCapability, boolean>> = {};
      (selectedData.selections || []).forEach((selection) => {
        const capability = getCapabilityByModelType(selection.model_key);
        if (capability) {
          nextShareStatus[capability] = !!selection.share;
        }
      });
      setShareStatus(nextShareStatus);

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
            icon: getCloudServiceIcon(
              selection.provider_name,
              service.category,
            ),
            providerName: selection.provider_name,
          },
        );
      });
      setSelectedCloudServices(nextSelectedCloudServices);
      setCloudServiceShareStatus(nextCloudShareStatus);
      setCloudServiceOptions((current) => ({
        ...selectedCloudOptions,
        ...current,
      }));

      if (!isAdmin) {
        const [modelReadyResults, cloudReadyResults] = await Promise.all([
          Promise.allSettled(
            moduleConfigs.map(async (module) => {
              const response = await modelProvidersDefaultApi.apiCoreModelProvidersModelsReadyGet(
                withModelProviderJsonOptions({
                  params: { model_type: module.key },
                }),
              );
              return {
                capability: module.key,
                response: unwrapModelProviderData<ModelReadyResponse>(response.data as unknown),
              };
            }),
          ),
          Promise.allSettled(
            cloudServiceConfigs.map(async (service) => {
              const response =
                await modelProvidersApi.apiCoreModelProvidersVerifiedGet({
                  category: service.category,
                });
              return {
                service: service.key,
                response: unwrapModelProviderData<VerifiedCloudServiceResponse>(response.data),
              };
            }),
          ),
        ]);
        const nextReadyStatus: ModelReadyStatus = {};
        modelReadyResults.forEach((result) => {
          if (result.status === "fulfilled") {
            nextReadyStatus[result.value.capability] = result.value.response;
          }
        });
        setModelReadyStatus(nextReadyStatus);

        const nextCloudReadyStatus: CloudServiceReadyStatus = {};
        cloudReadyResults.forEach((result) => {
          if (result.status === "fulfilled") {
            nextCloudReadyStatus[result.value.service] = result.value.response;
          }
        });
        setCloudServiceReadyStatus(nextCloudReadyStatus);
      }
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(
          error,
          t("modelProvider.error.loadProvidersFailed"),
        ),
      );
    }
  }, [isAdmin, localizedFallbacks, t]);

  useEffect(() => {
    void loadDefaultModelState();
  }, [loadDefaultModelState]);

  const loadModuleModels = async (
    capability: ModelCapability,
    force = false,
    keyword = "",
  ) => {
    const trimmedKeyword = keyword.trim();
    if (!force && trimmedKeyword === "" && moduleModelOptions[capability]) {
      return;
    }
    if (moduleModelLoading[capability]) {
      return;
    }

    setModuleModelLoading((current) => ({ ...current, [capability]: true }));
    try {
      const response = await modelProvidersApi.apiCoreModelProvidersModelsGet({
        modelType: capability,
      });
      const data = unwrapModelProviderData<{
        models?: Array<
          ApiModel & {
            user_model_provider_id: string;
            user_model_provider_group_id: string;
            provider_name: string;
            group_name: string;
            base_url?: string;
          }
        >;
      }>(response.data);
      const fetchedOptions = (data.models || [])
        .filter((model) =>
          trimmedKeyword
            ? `${model.name} ${model.provider_name} ${model.group_name}`
                .toLowerCase()
                .includes(trimmedKeyword.toLowerCase())
            : true,
        )
        .map((model) => {
        const provider =
          providerOptions.find(
            (item) => item.id === model.user_model_provider_id,
          ) ||
          mapApiProvider(
            {
              id: model.user_model_provider_id,
              name: model.provider_name,
              base_url: model.base_url,
            },
            localizedFallbacks,
          );
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

      setModuleModelOptions((current) => ({
        ...current,
        [capability]: options,
      }));
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(
          error,
          t("modelProvider.error.loadModelsFailed"),
        ),
      );
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

  const saveSelectedModel = async (
    capability: ModelCapability,
    value?: string,
  ) => {
    const selections = [
      {
        model_key: capability,
        model_id: value ? parseModelValue(value).modelId : "",
      },
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
      message.warning(t("modelProvider.noModelSelectedForShare"));
      return;
    }

    try {
      await modelProvidersDefaultApi.apiCoreModelProvidersSelectedModelsSharePut(
        withModelProviderJsonOptions({
          data: {
            model_id: parseModelValue(value).modelId,
            model_key: capability,
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
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(
          error,
          t("modelProvider.error.shareUpdateFailed"),
        ),
      );
    }
  };

  const applyModelSelection = (capability: ModelCapability, value?: string) => {
    setSelectedModels((current) => ({
      ...current,
      [capability]: value,
    }));
    void saveSelectedModel(capability, value)
      .then((response) => {
        (response.selections || []).forEach((selection) => {
          const selectedCapability = getCapabilityByModelType(
            selection.model_key,
          );
          if (selectedCapability) {
            setShareStatus((current) => ({
              ...current,
              [selectedCapability]: !!selection.share,
            }));
          }
        });
      })
      .catch((error) => {
        message.error(
          getLocalizedErrorMessage(
            error,
            t("modelProvider.error.saveDefaultModelFailed"),
          ),
        );
      });
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
    keyword = "",
  ) => {
    if (cloudServiceLoading[service.key]) {
      return;
    }

    setCloudServiceLoading((current) => ({ ...current, [service.key]: true }));
    try {
      const trimmedKeyword = keyword.trim();
      const response = await modelProvidersApi.apiCoreModelProvidersProviderGroupsGet({
        category: service.category,
      });
      const data = unwrapModelProviderData<CloudServiceGroupListResponse>(response.data);
      const groups = (data.groups || []).filter((group) =>
        trimmedKeyword
          ? `${group.provider_name} ${group.group_name} ${group.base_url}`
              .toLowerCase()
              .includes(trimmedKeyword.toLowerCase())
          : true,
      );
      const fetchedOptions = groups.map((group) =>
        mapVerifiedCloudServiceGroup(group, service.category),
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
      const selectedGroupId =
        currentSelectedGroupId &&
        options.some((option) => option.groupId === currentSelectedGroupId)
          ? currentSelectedGroupId
          : undefined;
      setCloudServiceOptions((current) => ({
        ...current,
        [service.key]: options,
      }));
      setSelectedCloudServices((current) => ({
        ...current,
        [service.key]: selectedGroupId,
      }));
      if (!selectedGroupId) {
        setCloudServiceShareStatus((current) => ({
          ...current,
          [service.key]: false,
        }));
      }
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(
          error,
          t("modelProvider.error.loadProvidersFailed"),
        ),
      );
    } finally {
      setCloudServiceLoading((current) => ({
        ...current,
        [service.key]: false,
      }));
    }
  };

  const handleCloudServiceSelection = (
    service: CloudServiceSlotKey,
    value?: string,
  ) => {
    setSelectedCloudServices((current) => ({
      ...current,
      [service]: value,
    }));
    if (!value) {
      setCloudServiceShareStatus((current) => ({
        ...current,
        [service]: false,
      }));
    }

    void saveSelectedCloudService(service, value)
      .then((response) => {
        (response.selections || []).forEach((selection) => {
          const slot = cloudServiceConfigs.find(
            (item) => item.category === selection.category,
          )?.key;
          if (slot) {
            setCloudServiceShareStatus((current) => ({
              ...current,
              [slot]: !!selection.share,
            }));
          }
        });
      })
      .catch((error) => {
        message.error(
          getLocalizedErrorMessage(
            error,
            t("modelProvider.error.saveDefaultModelFailed"),
          ),
        );
        const config = cloudServiceConfigs.find((item) => item.key === service);
        if (config) {
          void loadVerifiedCloudService(config);
        }
      });
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
      .catch((error) => {
        message.error(
          getLocalizedErrorMessage(
            error,
            t("modelProvider.error.shareUpdateFailed"),
          ),
        );
      });
  };

  return (
    <section
      className="model-provider-config-panel"
      aria-label={t("modelProvider.defaultConfigAria")}
    >
      <div className="model-provider-panel-title-row">
        <div>
          <h2 className="model-provider-section-title">
            {t("modelProvider.defaultTitle")}
          </h2>
          <p className="model-provider-section-subtitle">
            {t("modelProvider.defaultSubtitle")}
          </p>
        </div>
      </div>

      <div className="model-provider-default-list">
        {visibleModuleConfigs.map((module) => {
          const options = moduleModelOptions[module.key] || [];
          const optionLoading = Boolean(moduleModelLoading[module.key]);
          const moduleTitle = t(module.titleKey);
          const moduleSubtitle = t(module.subtitleKey);

          return (
            <div
              className={`model-provider-default-row${module.restricted && !isAdmin ? " is-restricted" : ""}`}
              key={module.key}
            >
              <div className="model-provider-default-meta">
                <label
                  className="model-provider-default-title"
                  htmlFor={`model-provider-${module.key.toLowerCase()}`}
                >
                  {module.required ? (
                    <span className="is-required">*</span>
                  ) : null}
                  <span>{moduleTitle}</span>
                </label>
                <Tooltip placement="top" title={moduleSubtitle}>
                  <button
                    aria-label={t("modelProvider.moduleHelpAria", {
                      title: moduleTitle,
                    })}
                    className="model-provider-default-help"
                    type="button"
                  >
                    <QuestionCircleOutlined />
                  </button>
                </Tooltip>
                {module.restricted ? (
                  <Tooltip
                    placement="top"
                    title={
                      !isAdmin
                        ? t("modelProvider.restrictedAdminOnly")
                        : undefined
                    }
                  >
                    <span className="model-provider-limited-tag-wrap">
                      <Tag className="model-provider-limited-tag">
                        {t("modelProvider.limited")}
                      </Tag>
                    </span>
                  </Tooltip>
                ) : null}
                {isAdmin ? (
                  <Tooltip
                    title={
                      shareStatus[module.key]
                        ? t("modelProvider.shareOn")
                        : t("modelProvider.shareOff")
                    }
                  >
                    <Switch
                      aria-label={t("modelProvider.shareToggleAria", {
                        title: moduleTitle,
                      })}
                      checked={!!shareStatus[module.key]}
                      checkedChildren={t("modelProvider.shared")}
                      className="model-provider-share-switch"
                      size="small"
                      unCheckedChildren={t("modelProvider.unshared")}
                      onChange={(checked) =>
                        void toggleShareModel(module.key, checked)
                      }
                    />
                  </Tooltip>
                ) : null}
                {!isAdmin ? (
                  <Tooltip
                    title={getModelReadyTooltip(t, modelReadyStatus[module.key])}
                  >
                    <span
                      aria-label={t("modelProvider.readyStatusAria", {
                        title: moduleTitle,
                      })}
                      className="model-provider-ready-indicator"
                    >
                      {modelReadyStatus[module.key]?.ready ? (
                        <CheckCircleOutlined className="model-provider-ready-icon is-ready" />
                      ) : modelReadyStatus[module.key]?.ready === false ? (
                        <MinusCircleOutlined className="model-provider-ready-icon is-not-ready" />
                      ) : null}
                    </span>
                  </Tooltip>
                ) : null}
              </div>

              <Select
                allowClear={!module.required}
                className="model-provider-model-select"
                disabled={module.restricted && !isAdmin}
                filterOption={false}
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
                showSearch
                suffixIcon={
                  <DownOutlined className="model-provider-select-caret" />
                }
                value={selectedModels[module.key]}
                onChange={(value) => handleModelSelection(module.key, value)}
                onSearch={(value) => {
                  setModuleModelSearchKeywords((current) => ({
                    ...current,
                    [module.key]: value,
                  }));
                  void loadModuleModels(module.key, true, value);
                }}
                onDropdownVisibleChange={(open) => {
                  if (open) {
                    void loadModuleModels(
                      module.key,
                      true,
                      moduleModelSearchKeywords[module.key] || "",
                    );
                  }
                }}
                loading={optionLoading}
                notFoundContent={
                  optionLoading
                    ? t("common.loading")
                    : t("modelProvider.noModelOptions")
                }
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
                          {model.builtIn
                            ? t("modelProvider.builtInModelSuffix")
                            : t("modelProvider.customModelSuffix")}
                        </small>
                      </span>
                    </span>
                  </Select.Option>
                ))}
              </Select>
            </div>
          );
        })}

        {cloudServiceConfigs.map((service) => {
          const serviceTitle = t(service.titleKey);
          const serviceSubtitle = t(service.subtitleKey);
          const options = cloudServiceOptions[service.key] || [];
          const optionLoading = Boolean(cloudServiceLoading[service.key]);
          const cloudReady = cloudServiceReadyStatus[service.key];

          return (
            <div
              className="model-provider-default-row model-provider-cloud-service-row"
              key={service.key}
            >
              <div className="model-provider-default-meta">
                <label
                  className="model-provider-default-title"
                  htmlFor={`model-provider-cloud-${service.key}`}
                >
                  <span>{serviceTitle}</span>
                </label>
                <Tooltip placement="top" title={serviceSubtitle}>
                  <button
                    aria-label={t("modelProvider.moduleHelpAria", {
                      title: serviceTitle,
                    })}
                    className="model-provider-default-help"
                    type="button"
                  >
                    <QuestionCircleOutlined />
                  </button>
                </Tooltip>
                {isAdmin ? (
                  <Tooltip
                    title={
                      cloudServiceShareStatus[service.key]
                        ? t("modelProvider.shareOn")
                        : t("modelProvider.shareOff")
                    }
                  >
                    <Switch
                      aria-label={t("modelProvider.shareToggleAria", {
                        title: serviceTitle,
                      })}
                      checked={!!cloudServiceShareStatus[service.key]}
                      checkedChildren={t("modelProvider.shared")}
                      className="model-provider-share-switch"
                      size="small"
                      unCheckedChildren={t("modelProvider.unshared")}
                      onChange={(checked) =>
                        toggleShareCloudService(service.key, checked)
                      }
                    />
                  </Tooltip>
                ) : null}
                {!isAdmin ? (
                  <Tooltip
                    title={getCloudServiceReadyTooltip(t, cloudReady)}
                  >
                    <span
                      aria-label={t("modelProvider.readyStatusAria", {
                        title: serviceTitle,
                      })}
                      className="model-provider-ready-indicator"
                    >
                      {cloudReady?.ready ? (
                        <CheckCircleOutlined className="model-provider-ready-icon is-ready" />
                      ) : cloudReady?.ready === false ? (
                        <MinusCircleOutlined className="model-provider-ready-icon is-not-ready" />
                      ) : null}
                    </span>
                  </Tooltip>
                ) : null}
              </div>

              <Select
                allowClear
                className="model-provider-model-select"
                filterOption={false}
                id={`model-provider-cloud-${service.key}`}
                optionLabelProp="label"
                placeholder={t("modelProvider.cloudServicePlaceholder")}
                popupClassName="model-provider-select-dropdown"
                showSearch
                suffixIcon={
                  <DownOutlined className="model-provider-select-caret" />
                }
                value={selectedCloudServices[service.key]}
                onChange={(value) =>
                  handleCloudServiceSelection(service.key, value)
                }
                onSearch={(value) => {
                  setCloudServiceSearchKeywords((current) => ({
                    ...current,
                    [service.key]: value,
                  }));
                  void loadVerifiedCloudService(service, value);
                }}
                onDropdownVisibleChange={(open) => {
                  if (open) {
                    void loadVerifiedCloudService(
                      service,
                      cloudServiceSearchKeywords[service.key] || "",
                    );
                  }
                }}
                loading={optionLoading}
                notFoundContent={
                  optionLoading
                    ? t("common.loading")
                    : t("modelProvider.noCloudServiceOptions")
                }
              >
                {options.map((option) => (
                  <Select.Option
                    key={option.groupId}
                    label={
                      <span className="model-provider-select-value">
                        <span className="model-provider-cloud-service-icon">
                          {option.icon}
                        </span>
                        <span className="model-provider-select-value-text">
                          {option.providerName} · {option.groupName}
                        </span>
                      </span>
                    }
                    value={option.groupId}
                  >
                    <span className="model-provider-select-option">
                      <span className="model-provider-cloud-service-icon">
                        {option.icon}
                      </span>
                      <span className="model-provider-select-copy">
                        <strong>{option.providerName}</strong>
                        <small>
                          <CloudServerOutlined />
                          {option.groupName}
                          {option.baseUrl ? ` · ${option.baseUrl}` : ""}
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
