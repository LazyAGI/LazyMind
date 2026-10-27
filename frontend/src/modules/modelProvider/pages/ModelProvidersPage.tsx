import {
  useSettingsEditor,
  useSettingsFormDraft,
} from "@/modules/settings/SettingsNavigationGuard";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  AutoComplete,
  Button,
  Form,
  Input,
  Modal,
  Select,
  message,
} from "antd";
import { useTranslation } from "react-i18next";
import {
  getLocalizedErrorMessage,
  localizeErrorCode,
} from "@/components/request";
import {
  beginCloudLogin,
  getCloudSession,
  isCloudBusinessAvailable,
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT,
} from "@/runtime/cloud/session";
import {
  closeCloudLoginPopup,
  openCloudLogin,
  openCloudTokenPlan,
  reserveCloudLoginPopup,
} from "@/runtime/desktopBridge";
import { RightOutlined, SearchOutlined } from "@ant-design/icons";
import {
  cancelCredentialRestore,
  addGroupModels,
  getCredentialBackupStatus,
  getCredentialRestoreDiscovery,
  getCredentialRestoreOperation,
  modelProvidersApi,
  modelProvidersDefaultApi,
  setCredentialBackupEnabled,
  startCredentialRestore,
  listRemoteGroupModels,
  unwrapModelProviderData,
  updateGroupModel,
  withModelProviderJsonOptions,
  type CredentialRestoreRecord,
  type RemoteGroupModel,
} from "../api";
import { CredentialBackupPanel } from "../components/CredentialBackupPanel";
import { CredentialRestorePanel } from "../components/CredentialRestorePanel";
import CloudSystemProviderCard, {
  type CloudSystemProviderModel,
} from "../components/CloudSystemProviderCard";
import type { CredentialBackupStatus } from "../credentialBackupModel";
import type {
  CredentialRestoreMode,
  CredentialRestoreStatus,
} from "../credentialRestoreModel";
import { getProviderLogoUrl } from "../providerBranding";
import {
  LLM_MAX_INPUT_TOKENS_MAX_LENGTH,
  parseLlmMaxInputTokens,
} from "../maxInputTokens";
import { ProviderWorkspace } from "../components/ProviderWorkspace";
import { ProviderModelCatalogDrawer, type CatalogModelChoice } from "../components/ProviderModelCatalogDrawer";
import { sortCustomModelsFirst } from "../modelOrdering";
import { getModelTaskType } from "../modelTaskTypes";
import { ModelTypeIcon } from "../components/ModelTypeBadge";
import { getSelectableCapabilities, isModelTypeSupportedByProtocol, normalizeRuntimeModelType, supportsModelContext, supportsStoredModelContext } from "../modelCapabilities";
import { resolveRemoteModelMetadata } from "../remoteModelMetadata";
import { canDiscoverWithoutApiKey } from "../providerAuthentication";
import { getProviderConnectionFailureMessage } from "../connectionFailure";
import { getProviderDisplayName } from "../providerDisplayNames";
import { ModelEditorModal, type ModelEditorValues } from "../components/ModelEditorModal";
import { ModelFeaturesEditor } from "../components/ModelFeaturesEditor";
import { EmbeddingOptionsEditor } from "../components/EmbeddingOptionsEditor";
import { editableModelSettings, isModelFeatureEnabled, type EmbeddingEditorValues, type ModelSettings } from "../modelSettings";
import "../index.scss";

export type ModelCapability =
  | "LLM_CHAT"
  | "EMBEDDING"
  | "VLM"
  | "RERANK"
  | "ASR"
  | "TTS"
  | "TEXT_TO_IMAGE"
  | "TEXT_TO_VIDEO"
  | "MULTIMODAL_EMBEDDING"
  | "IMAGE_EDITING"
  | "LLM_SELF_EVOLUTION"
  | "UNKNOWN";

export function getSelectableModelCapabilities(
  capabilities: readonly ModelCapability[],
  protocol?: string,
): ModelCapability[] {
  return getSelectableCapabilities(capabilities, protocol);
}

export interface ProviderModel {
  modelSettings?: ModelSettings;
  vision?: boolean;
  id: string;
  name: string;
  capability: ModelCapability;
  builtIn: boolean;
  enabled: boolean;
  maxInputTokens?: string;
  inputTokenLimit?: string;
  maxOutputTokens?: string;
}

export interface ProviderOption {
  id: string;
  name: string;
  logoDataUrl?: string;
  brandKey?: string;
  serviceKey?: string;
  isCustom?: boolean;
  protocolName?: string;
  brand: string;
  logoUrl?: string;
  headline: string;
  backendDescription?: string;
  source: string;
  baseUrl: string;
  capabilities: ModelCapability[];
  models: ProviderModel[];
}

export interface ProviderConnectionGroup {
  id: string;
  name: string;
  source: string;
  baseUrl: string;
  apiKeyConfigured: boolean;
  verified: boolean;
  models: ProviderModel[];
  modelCount?: number;
  keys: Array<{ id: string; masked: string; enabled?: boolean }>;
}

export interface AddedProvider extends ProviderOption {
  groups: ProviderConnectionGroup[];
}

interface CustomModelModalState {
  provider: AddedProvider;
  group: ProviderConnectionGroup;
}

interface EditModelWindowModalState {
  provider: AddedProvider;
  group: ProviderConnectionGroup;
  model: ProviderModel;
}

interface CustomModelFormValues extends EmbeddingEditorValues {
  modelSettings?: ModelSettings;
  vision?: boolean;
  providerId: string;
  groupId: string;
  name: string;
  capability: ModelCapability;
  maxInputTokens?: string;
}

const capabilityLabelKeys: Record<ModelCapability, string> = {
  UNKNOWN: "modelProvider.taskType.unknown",
  LLM_CHAT: "modelProvider.capability.llmChat",
  EMBEDDING: "modelProvider.capability.embedding",
  VLM: "modelProvider.capability.vlm",
  RERANK: "modelProvider.capability.rerank",
  ASR: "modelProvider.capability.asr",
  TTS: "modelProvider.capability.tts",
  TEXT_TO_IMAGE: "modelProvider.capability.textToImage",
  TEXT_TO_VIDEO: "modelProvider.capability.textToVideo",
  MULTIMODAL_EMBEDDING: "modelProvider.capability.multimodalEmbedding",
  IMAGE_EDITING: "modelProvider.capability.imageEditing",
  LLM_SELF_EVOLUTION: "modelProvider.capability.selfEvolution",
};

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
    modelCount: overrides.modelCount ?? overrides.models?.length ?? provider.models.length,
    keys: overrides.keys || [],
  };
}

enum ModelProviderModelType {
  VLM = "vlm",
  LLM = "llm",
  Embedding = "embed",
  MultimodalEmbedding = "multimodal_embedding",
  TextToImage = "text2image",
  TextToVideo = "text2video",
  TTS = "tts",
  STT = "stt",
  Rerank = "rerank",
  ImageEditing = "image_editing",
}

const modelTypeByCapability: Record<ModelCapability, string> = {
  UNKNOWN: "",
  EMBEDDING: ModelProviderModelType.Embedding,
  VLM: ModelProviderModelType.VLM,
  RERANK: ModelProviderModelType.Rerank,
  ASR: ModelProviderModelType.STT,
  TTS: ModelProviderModelType.TTS,
  TEXT_TO_IMAGE: ModelProviderModelType.TextToImage,
  TEXT_TO_VIDEO: ModelProviderModelType.TextToVideo,
  MULTIMODAL_EMBEDDING: ModelProviderModelType.MultimodalEmbedding,
  IMAGE_EDITING: ModelProviderModelType.ImageEditing,
  LLM_CHAT: ModelProviderModelType.LLM,
  LLM_SELF_EVOLUTION: ModelProviderModelType.LLM,
};

export function getModelTypeForCapability(capability: ModelCapability): string {
  return modelTypeByCapability[capability];
}

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

export function mapModelTypeToCapability(modelType?: string): ModelCapability {
  const normalized = normalizeRuntimeModelType(modelType || "");
  if (
    normalized === ModelProviderModelType.MultimodalEmbedding ||
    normalized === "cross_modal_embed" ||
    normalized === "embed_image"
  )
    return "MULTIMODAL_EMBEDDING";
  if (
    normalized === ModelProviderModelType.Embedding ||
    normalized === "embed_main" ||
    normalized.includes("embedding")
  )
    return "EMBEDDING";
  if (normalized.includes("rerank")) return "RERANK";
  if (normalized === ModelProviderModelType.STT || normalized === "asr")
    return "ASR";
  if (normalized === ModelProviderModelType.TTS) return "TTS";
  if (normalized === ModelProviderModelType.ImageEditing)
    return "IMAGE_EDITING";
  if (normalized === ModelProviderModelType.TextToImage) return "TEXT_TO_IMAGE";
  if (normalized === ModelProviderModelType.TextToVideo) return "TEXT_TO_VIDEO";
  if (
    normalized === ModelProviderModelType.VLM.toLowerCase() ||
    normalized.includes("vision")
  )
    return "VLM";
  return normalized === "llm" ? "LLM_CHAT" : "UNKNOWN";
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

interface ApiProvider {
  id: string;
  name: string;
  logo_data_url?: string;
  brand_key?: string;
  service_key?: string;
  is_custom?: boolean;
  protocol_name?: string;
  description?: string;
  base_url?: string;
}

interface ApiGroup {
  id: string;
  name: string;
  base_url?: string;
  has_api_key?: boolean;
  model_count?: number;
  is_verified?: boolean;
  user_model_provider_id: string;
  keys?: Array<{ id: string; masked: string; enabled?: boolean }>;
}

interface CheckModelProviderResult {
  success: boolean;
  message?: string;
  reason?: string;
}

export function resolveSavedProviderGroupVerified(group: {
  is_verified?: boolean;
  check?: CheckModelProviderResult;
}): boolean {
  if (typeof group.is_verified === "boolean") {
    return group.is_verified;
  }
  return group.check?.success === true;
}

interface ApiModel {
  model_settings?: ModelSettings;
  vision?: boolean;
  id: string;
  name: string;
  model_type?: string;
  is_default?: boolean;
  max_input_tokens?: string;
  input_token_limit?: string | null;
  max_output_tokens?: string | null;
  enabled?: boolean;
}

function mapApiProvider(
  provider: ApiProvider,
  fallbacks: ModelProviderFallbacks,
): ProviderOption {
  const backendDescription = provider.description;

  return {
    id: provider.id,
    name: provider.name,
    logoDataUrl: provider.logo_data_url || undefined,
    brandKey: provider.brand_key,
    serviceKey: provider.service_key,
    isCustom: provider.is_custom === true,
    protocolName: provider.protocol_name || provider.name,
    brand: provider.is_custom ? "API" : getProviderBrand(provider.brand_key || provider.name || provider.protocol_name || ""),
    logoUrl: getProviderLogoUrl(provider.brand_key || provider.name || provider.protocol_name || "", provider.is_custom),
    headline: getLocalizedProviderDescription(
      provider.name,
      backendDescription,
      fallbacks,
    ),
    backendDescription,
    source: provider.protocol_name || provider.name,
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
      "TEXT_TO_VIDEO",
      "IMAGE_EDITING",
    ],
    models: [],
  };
}

function mapApiGroup(
  provider: ProviderOption,
  group: ApiGroup | ProviderConnectionGroup,
  models: ApiModel[],
): ProviderConnectionGroup {
  const isApiGroup =
    "base_url" in group || "has_api_key" in group || "is_verified" in group;

  return createConnectionGroup(provider, {
    id: group.id,
    name: group.name,
    baseUrl: isApiGroup
      ? (group as ApiGroup).base_url || provider.baseUrl
      : (group as ProviderConnectionGroup).baseUrl || provider.baseUrl,
    keys: group.keys || [],
    modelCount: isApiGroup ? (group as ApiGroup).model_count : (group as ProviderConnectionGroup).modelCount,
    apiKeyConfigured: isApiGroup
      ? Boolean((group as ApiGroup).has_api_key)
      : (group as ProviderConnectionGroup).apiKeyConfigured,
    verified: isApiGroup
      ? Boolean((group as ApiGroup).is_verified)
      : (group as ProviderConnectionGroup).verified,
    models: sortCustomModelsFirst(models.map((model) => ({
      id: model.id,
      name: model.name,
      capability: mapModelTypeToCapability(model.model_type),
      vision: model.vision,
      modelSettings: model.model_settings,
      builtIn: Boolean(model.is_default),
      enabled: model.enabled !== false,
      maxInputTokens: model.max_input_tokens,
      inputTokenLimit: model.input_token_limit || undefined,
      maxOutputTokens: model.max_output_tokens || undefined,
    }))),
  });
}

function normalizeModelName(value: string) {
  return value.trim().toLowerCase();
}

function normalizeFormText(value?: string) {
  return value?.trim() || "";
}

function getCredentialRestoreFailureCode(error: unknown) {
  if (!error || typeof error !== "object" || !("response" in error))
    return undefined;
  const response = (error as { response?: { data?: unknown } }).response;
  const payload = response?.data;
  if (!payload || typeof payload !== "object") return undefined;
  const data =
    "data" in payload ? (payload as { data?: unknown }).data : undefined;
  if (!data || typeof data !== "object" || !("reason_code" in data))
    return undefined;
  return String((data as { reason_code?: unknown }).reason_code || "");
}

interface ModelProviderPageProps {
  onConfigurationChanged?: () => void | Promise<void>;
  highlightProviderId?: string;
  onBack?: () => void;
}

type CloudSystemProviderState =
  "loading" | "ready" | "signed_out" | "plan_required" | "error";

export default function ModelProviderPage({
  onConfigurationChanged,
  highlightProviderId,
  onBack,
}: ModelProviderPageProps) {
  const { t, i18n } = useTranslation();
  const currentLanguage = i18n.resolvedLanguage || i18n.language || "zh-CN";
  const [customModelForm] = Form.useForm<CustomModelFormValues>();

  const [providerOptions, setProviderOptions] = useState<ProviderOption[]>([]);
  const [addedProviderList, setAddedProviderList] = useState<AddedProvider[]>(
    [],
  );
  const [customModelModal, setCustomModelModal] =
    useState<CustomModelModalState | null>(null);
  const [editModelWindowModal, setEditModelWindowModal] =
    useState<EditModelWindowModalState | null>(null);
  const [customModelSaving, setCustomModelSaving] = useState(false);
  const customModelSavePending = useRef(false);
  const remoteModelRequest = useRef(0);
  const groupModelVersions = useRef<Record<string, number>>({});
  const modelDeleteRequests = useRef(new Map<string, Promise<boolean>>());
  const [modelsError, setModelsError] = useState<Record<string, string>>({});
  const [remoteModels, setRemoteModels] = useState<RemoteGroupModel[]>([]);
  const [remoteModelsLoading, setRemoteModelsLoading] = useState(false);
  const [catalogTarget, setCatalogTarget] = useState<{ providerId: string; groupId: string; remoteModels?: RemoteGroupModel[] } | null>(null);
  const keyDiscoveryRequests = useRef(new Set<string>());
  const [discoveringGroupIds, setDiscoveringGroupIds] = useState<Record<string, boolean>>({});
  const [contextWindowMode, setContextWindowMode] = useState<"auto" | "manual">(
    "auto",
  );
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const [verifyingGroupIds, setVerifyingGroupIds] = useState<
    Record<string, boolean>
  >({});
  const [loadingGroupModelIds, setLoadingGroupModelIds] = useState<
    Record<string, boolean>
  >({});
  const [credentialBackupStatus, setCredentialBackupStatus] =
    useState<CredentialBackupStatus>({
      enabled: false,
      backedUp: 0,
      pending: 0,
      failed: 0,
    });
  const [credentialBackupAvailable, setCredentialBackupAvailable] =
    useState(false);
  const [credentialBackupLoading, setCredentialBackupLoading] = useState(false);
  const [credentialRestoreRecords, setCredentialRestoreRecords] = useState<
    CredentialRestoreRecord[]
  >([]);
  const [credentialRestoreLoading, setCredentialRestoreLoading] =
    useState(false);
  const [credentialRestoreStatus, setCredentialRestoreStatus] =
    useState<CredentialRestoreStatus>({
      available: false,
      requiresExplicitAction: true,
      backupCount: 0,
      status: "idle",
    });
  const [cloudSystemState, setCloudSystemState] =
    useState<CloudSystemProviderState>("loading");
  const [cloudSystemModels, setCloudSystemModels] = useState<
    CloudSystemProviderModel[]
  >([]);
  const [cloudRuntimeAvailable, setCloudRuntimeAvailable] = useState(false);
  const [cloudPlanURL, setCloudPlanURL] = useState("");
  const [contextWindowExpanded, setContextWindowExpanded] = useState(false);
  const watchedCustomCapability = Form.useWatch("capability", customModelForm);
  const cloudCatalogRequestIdRef = useRef(0);
  const providerLoadRequestIdRef = useRef(0);
  const initialProvidersLoadedRef = useRef(false);
  const addedProviderListRef = useRef<AddedProvider[]>([]);
  addedProviderListRef.current = addedProviderList;
  const localizedFallbacks = useMemo(
    () => createModelProviderFallbacks(t),
    [i18n.language, t],
  );
  const getCapabilityLabel = useCallback(
    (capability: ModelCapability) => t(capabilityLabelKeys[capability]),
    [t],
  );
  const legacyProviderLocation = useSettingsEditor("provider");
  const customModelDraft = useSettingsFormDraft(
    customModelForm,
    Boolean(customModelModal),
    customModelSaving,
  );
  const loadCloudSystemProvider = useCallback(async () => {
    const requestId = ++cloudCatalogRequestIdRef.current;
    setCloudSystemState("loading");
    try {
      const session = await getCloudSession();
      if (requestId !== cloudCatalogRequestIdRef.current) return;
      const available = isCloudBusinessAvailable(session);
      setCloudRuntimeAvailable(available);
      if (!available) {
        setCloudSystemModels([]);
        setCloudSystemState(
          session.configured === true && session.reachability === "unreachable"
            ? "error"
            : "signed_out",
        );
        return;
      }
      const response = await modelProvidersApi.apiCoreModelProvidersModelsGet(
        {},
      );
      if (requestId !== cloudCatalogRequestIdRef.current) return;
      const data = unwrapModelProviderData<{
        models?: Array<{
          id: string;
          name: string;
          model_type: string;
          source?: string;
          availability?: string;
          lifecycle?: string;
        }>;
      }>(response.data);
      const models = (data.models || [])
        .filter((model) => model.source === "cloud")
        .map((model): CloudSystemProviderModel => ({
          id: model.id,
          name: model.name,
          modelType: model.model_type,
          availability:
            model.availability === "degraded" ||
            model.availability === "unavailable"
              ? model.availability
              : "available",
          lifecycle:
            model.lifecycle === "deprecated" || model.lifecycle === "retired"
              ? model.lifecycle
              : "active",
        }));
      setCloudSystemModels(models);
      if (models.length) {
        setCloudSystemState("ready");
        return;
      }
      const readiness =
        await modelProvidersDefaultApi.apiCoreModelProvidersModelsReadyGet(
          withModelProviderJsonOptions({ params: { model_type: "llm" } }),
        );
      if (requestId !== cloudCatalogRequestIdRef.current) return;
      const ready = unwrapModelProviderData<{
        reason?: string;
        cloud_plan_url?: string;
      }>(readiness.data as unknown);
      setCloudPlanURL(ready.cloud_plan_url || "");
      setCloudSystemState(
        ready.reason === "cloud_plan_required" ? "plan_required" : "error",
      );
    } catch {
      if (requestId === cloudCatalogRequestIdRef.current) {
        setCloudRuntimeAvailable(false);
        setCloudSystemModels([]);
        setCloudSystemState("error");
      }
    }
  }, []);

  useEffect(() => {
    void loadCloudSystemProvider();
    const refresh = () => void loadCloudSystemProvider();
    const refreshCloudSession = () => {
      setCloudRuntimeAvailable(false);
      setCloudSystemModels([]);
      refresh();
    };
    const refreshWhenVisible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    window.addEventListener(
      LAZYMIND_CLOUD_SESSION_CHANGED_EVENT,
      refreshCloudSession,
    );
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refreshWhenVisible);
    return () => {
      cloudCatalogRequestIdRef.current += 1;
      window.removeEventListener(
        LAZYMIND_CLOUD_SESSION_CHANGED_EVENT,
        refreshCloudSession,
      );
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", refreshWhenVisible);
    };
  }, [loadCloudSystemProvider]);

  const beginSystemCloudLogin = useCallback(async () => {
    const popup = reserveCloudLoginPopup();
    if (popup === null) {
      message.error(t("layout.cloudOpenFailed"));
      return;
    }
    setCloudSystemState("loading");
    try {
      const login = await beginCloudLogin();
      const result = await openCloudLogin(login.authorization_url, popup);
      if (!result.ok) throw result.error || new Error(result.reason);
      setCloudSystemState("loading");
    } catch {
      closeCloudLoginPopup(popup);
      message.error(t("layout.cloudLoginFailed"));
      void loadCloudSystemProvider();
    }
  }, [loadCloudSystemProvider, t]);

  const openSystemCloudPlan = useCallback(async () => {
    const result = await openCloudTokenPlan(cloudPlanURL);
    if (!result.ok) message.error(t("layout.cloudOpenFailed"));
  }, [cloudPlanURL, t]);

  const fetchProviderOptions = useCallback(
    async (searchKeyword = "") => {
      const providerResponse = await modelProvidersApi.apiCoreModelProvidersGet(
        {
          keyword: searchKeyword.trim() || undefined,
        },
      );
      const providerData = unwrapModelProviderData<{
        providers?: ApiProvider[];
      }>(providerResponse.data);
      return (providerData.providers || []).map((provider) =>
        mapApiProvider(provider, localizedFallbacks),
      );
    },
    [localizedFallbacks],
  );

  const loadModelProviders = useCallback(async () => {
    const requestId = ++providerLoadRequestIdRef.current;
    const isCurrent = () => requestId === providerLoadRequestIdRef.current;
    const isFirstLoad = !initialProvidersLoadedRef.current;
    if (isFirstLoad) {
      setLoading(true);
    }
    try {
      const providers = await fetchProviderOptions();
      // A newer refresh owns the result; superseded requests are not failures.
      if (!isCurrent()) return true;
      setProviderOptions(providers);

      const withGroupsResponse =
        await modelProvidersApi.apiCoreModelProvidersWithGroupsGet();
      if (!isCurrent()) return true;
      const withGroupsData = unwrapModelProviderData<{
        providers?: ApiProvider[];
      }>(withGroupsResponse.data);
      const addedIds = new Set(
        (withGroupsData.providers || []).map((provider) => provider.id),
      );
      const addedProviders = await Promise.all(
        providers
          .filter((provider) => addedIds.has(provider.id))
          .map(async (provider): Promise<AddedProvider> => {
            const groupResponse =
              await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet(
                {
                  modelProviderId: provider.id,
                },
              );
            const groupData = unwrapModelProviderData<{ groups?: ApiGroup[] }>(
              groupResponse.data,
            );
            const groups = (groupData.groups || []).map((group) =>
              mapApiGroup(provider, group, []),
            );
            return { ...provider, groups };
          }),
      );

      if (!isCurrent()) return true;
      setAddedProviderList((current) => {
        if (!isCurrent()) return current;
        const cachedModels = new Map(
          current.flatMap((item) =>
            item.groups.map((group) => [group.id, group.models] as const),
          ),
        );
        return addedProviders.map((item) => ({
          ...item,
          groups: item.groups.map((group) => ({
            ...group,
            models: cachedModels.get(group.id) || [],
          })),
        }));
      });
      setLoadError(false);
      return true;
    } catch (error) {
      if (!isCurrent()) return true;
      setLoadError(true);
      return false;
    } finally {
      if (isCurrent()) {
        initialProvidersLoadedRef.current = true;
        setLoading(false);
      }
    }
  }, [fetchProviderOptions]);

  useEffect(() => {
    void loadModelProviders();
    // Language refreshes share the metadata gate and retain cached group models.
    return () => { providerLoadRequestIdRef.current += 1; };
  }, [currentLanguage, loadModelProviders]);

  const loadCredentialBackup = useCallback(async (showLoading = true) => {
    if (showLoading) setCredentialBackupLoading(true);
    try {
      const status = await getCredentialBackupStatus();
      setCredentialBackupAvailable(status.available);
      setCredentialBackupStatus(status);
    } catch {
      setCredentialBackupAvailable(false);
    } finally {
      if (showLoading) setCredentialBackupLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!cloudRuntimeAvailable) {
      setCredentialBackupAvailable(false);
      setCredentialBackupStatus({
        enabled: false,
        backedUp: 0,
        pending: 0,
        failed: 0,
      });
      setCredentialBackupLoading(false);
      return;
    }
    void loadCredentialBackup();
  }, [cloudRuntimeAvailable, loadCredentialBackup]);

  useEffect(() => {
    if (!credentialBackupStatus.enabled) return;
    const timer = window.setInterval(
      () => void loadCredentialBackup(false),
      15_000,
    );
    return () => window.clearInterval(timer);
  }, [credentialBackupStatus.enabled, loadCredentialBackup]);

  const toggleCredentialBackup = useCallback(
    async (enabled: boolean) => {
      setCredentialBackupLoading(true);
      try {
        const status = await setCredentialBackupEnabled(enabled);
        setCredentialBackupAvailable(status.available);
        setCredentialBackupStatus(status);
        message.success(
          t(
            enabled
              ? "modelProvider.credentialBackup.enabledSuccess"
              : "modelProvider.credentialBackup.disabledSuccess",
          ),
        );
      } catch {
        message.error(t("modelProvider.credentialBackup.updateFailed"));
      } finally {
        setCredentialBackupLoading(false);
      }
    },
    [t],
  );

  const loadCredentialRestore = useCallback(async (showLoading = true) => {
    if (showLoading) setCredentialRestoreLoading(true);
    try {
      const discovery = await getCredentialRestoreDiscovery();
      setCredentialRestoreRecords(discovery.records);
      setCredentialRestoreStatus((current) => ({
        ...current,
        available: discovery.available,
        requiresExplicitAction: discovery.requiresExplicitAction,
        backupCount: discovery.records.length,
        status:
          discovery.activeOperation?.status ||
          (current.status === "pending" || current.status === "running"
            ? current.status
            : "idle"),
        completedRecords: discovery.activeOperation?.completedRecords,
        totalRecords: discovery.activeOperation?.totalRecords,
        temporaryExpiresAt: discovery.activeOperation?.temporaryExpiresAt,
        operationId: discovery.activeOperation?.operationId,
        failureCode: discovery.activeOperation?.failureCode,
      }));
    } catch {
      setCredentialRestoreStatus((current) => ({
        ...current,
        available: false,
        status: "idle",
      }));
    } finally {
      if (showLoading) setCredentialRestoreLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!cloudRuntimeAvailable) {
      setCredentialRestoreRecords([]);
      setCredentialRestoreStatus((current) => ({
        ...current,
        available: false,
        backupCount: 0,
        status: "idle",
      }));
      setCredentialRestoreLoading(false);
      return;
    }
    void loadCredentialRestore();
  }, [cloudRuntimeAvailable, loadCredentialRestore]);

  const startRestore = useCallback(
    async (
      mode: CredentialRestoreMode,
      resolution: "fail" | "replace_local" | "save_copy" = "fail",
    ) => {
      if (!credentialRestoreRecords.length) return;
      setCredentialRestoreLoading(true);
      try {
        const operation = await startCredentialRestore(
          mode,
          credentialRestoreRecords,
          resolution,
        );
        setCredentialRestoreStatus({
          available: true,
          requiresExplicitAction: true,
          backupCount: credentialRestoreRecords.length,
          status: operation.status,
          completedRecords: operation.completedRecords,
          totalRecords: operation.totalRecords,
          failureCode: operation.failureCode,
          temporaryExpiresAt: operation.temporaryExpiresAt,
          operationId: operation.operationId,
        });
      } catch (error) {
        const failureCode = getCredentialRestoreFailureCode(error);
        setCredentialRestoreStatus((current) => ({
          ...current,
          status: failureCode === "local_conflict" ? "conflict" : "failed",
          failureCode,
        }));
      } finally {
        setCredentialRestoreLoading(false);
      }
    },
    [credentialRestoreRecords],
  );

  const cancelRestore = useCallback(async () => {
    const operationId = credentialRestoreStatus.operationId;
    if (!operationId) return;
    setCredentialRestoreLoading(true);
    try {
      await cancelCredentialRestore(operationId);
      await loadCredentialRestore(false);
      setCredentialRestoreStatus((current) => ({
        ...current,
        status: "idle",
        operationId: undefined,
      }));
    } catch (error) {
      setCredentialRestoreStatus((current) => ({
        ...current,
        status: "failed",
        failureCode: getCredentialRestoreFailureCode(error),
      }));
    } finally {
      setCredentialRestoreLoading(false);
    }
  }, [credentialRestoreStatus, loadCredentialRestore]);

  useEffect(() => {
    if (
      credentialRestoreStatus.status !== "pending" &&
      credentialRestoreStatus.status !== "running"
    )
      return;
    const operationId = credentialRestoreStatus.operationId;
    if (!operationId) return;
    let disposed = false;
    let inFlight = false;
    const poll = async () => {
      if (disposed || inFlight) return;
      inFlight = true;
      try {
        const operation = await getCredentialRestoreOperation(operationId);
        if (disposed) return;
        setCredentialRestoreStatus((current) => ({
          ...current,
          status: operation.status,
          completedRecords: operation.completedRecords,
          totalRecords: operation.totalRecords,
          failureCode: operation.failureCode,
          temporaryExpiresAt: operation.temporaryExpiresAt,
          operationId: operation.operationId,
        }));
        if (
          operation.status === "succeeded" &&
          operation.mode === "trusted_device"
        ) {
          await loadModelProviders();
        }
      } catch (error) {
        if (!disposed) {
          setCredentialRestoreStatus((current) => ({
            ...current,
            status: "failed",
            failureCode: getCredentialRestoreFailureCode(error),
          }));
        }
      } finally {
        inFlight = false;
      }
    };
    const timer = window.setInterval(() => void poll(), 1500);
    void poll();
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [credentialRestoreStatus.status, loadModelProviders]);

  const [verificationEvent, setVerificationEvent] = useState<{ providerId: string; account: string | null; sequence: number }>();

  const verifyProviderGroup = async (
    provider: AddedProvider,
    group: ProviderConnectionGroup,
    apiKey: string,
    isCurrent: () => boolean = () => true,
  ) => {
    const providerId = provider.id;
    const verificationAccount = localStorage.getItem("lazymind:user");
    const groupId = group.id;
    const requestApiKey = normalizeFormText(apiKey);
    const verifyKey = `${providerId}:${groupId}`;
    if (verifyingGroupIds[verifyKey]) {
      return false;
    }

    setVerifyingGroupIds((current) => ({ ...current, [verifyKey]: true }));
    try {
      const payload: Record<string, unknown> = {
        provider_name:
          provider.protocolName || provider.source || provider.name,
        base_url: group.baseUrl,
        ...(requestApiKey ? { api_key: requestApiKey } : {}),
        dry_run: false,
      };
      const checkResponse =
        await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdCheckPost(
          {
            modelProviderId: provider.id,
            groupId: group.id,
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            checkModelProviderOpenAPIRequest: payload as any,
          },
          withModelProviderJsonOptions({ timeout: 3 * 60 * 1000, silentError: true }),
        );
      const checkResult = unwrapModelProviderData<CheckModelProviderResult>(
        checkResponse.data,
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
                    : group,
                ),
              }
            : provider,
        ),
      );
      if (isVerified) {
        setVerificationEvent(current => ({ providerId, account: verificationAccount, sequence: (current?.sequence || 0) + 1 }));
        await loadModelProviders();
        if (isCurrent()) message.success(t("modelProvider.message.groupVerified"));
        void onConfigurationChanged?.();
        return true;
      }
      if (isCurrent()) message.error(getProviderConnectionFailureMessage(checkResult, t, localizeErrorCode("2000509")));
      void onConfigurationChanged?.();
    } catch (error) {
      // A failed upstream check may still have persisted an unverified state.
      await loadModelProviders();
      void onConfigurationChanged?.();
      if (isCurrent()) message.error(getProviderConnectionFailureMessage(error, t));
    } finally {
      setVerifyingGroupIds((current) => {
        const next = { ...current };
        delete next[verifyKey];
        return next;
      });
    }
  };

  const loadGroupModels = async (providerId: string, groupId: string) => {
    const provider = addedProviderList.find((item) => item.id === providerId);
    const group = provider?.groups.find((item) => item.id === groupId);
    const groupKey = `${providerId}:${groupId}`;
    if (!provider || !group || loadingGroupModelIds[groupKey]) {
      return;
    }

    const version = groupModelVersions.current[groupKey] || 0;
    setModelsError((current) => { const next = { ...current }; delete next[groupKey]; return next; });
    setLoadingGroupModelIds((current) => ({ ...current, [groupKey]: true }));
    try {
      const modelResponse =
        await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet(
          {
            modelProviderId: provider.id,
            groupId: group.id,
          },
        );
      const modelData = unwrapModelProviderData<{ models?: ApiModel[] }>(
        modelResponse.data,
      );
      if ((groupModelVersions.current[groupKey] || 0) !== version) return;
      const nextGroup = mapApiGroup(provider, group, modelData.models || []);
      setAddedProviderList((current) =>
        current.map((item) =>
          item.id === providerId
            ? {
                ...item,
                groups: item.groups.map((candidate) =>
                  candidate.id === groupId
                    ? { ...candidate, models: nextGroup.models, modelCount: nextGroup.models.length }
                    : candidate,
                ),
              }
            : item,
        ),
      );
    } catch (error) {
      if ((groupModelVersions.current[groupKey] || 0) === version) {
        setModelsError((current) => ({ ...current, [groupKey]: getLocalizedErrorMessage(error) }));
      }
    } finally {
      setLoadingGroupModelIds((current) => {
        const next = { ...current };
        delete next[groupKey];
        return next;
      });
    }
  };

  const toggleContextWindow = () => {
    setContextWindowExpanded((current) => !current);
  };

  const markContextWindowManual = () => {
    setContextWindowMode("manual");
  };

  const loadRemoteModelNames = async () => {
    const provider = customModelModal?.provider;
    const group = customModelModal?.group;
    if (!provider || !group || remoteModelsLoading) {
      return;
    }
    const request = ++remoteModelRequest.current;
    setRemoteModelsLoading(true);
    try {
      const data = await listRemoteGroupModels(provider.id, group.id);
      if (remoteModelRequest.current === request) setRemoteModels(data.models || []);
    } catch (error) {
      if (remoteModelRequest.current === request) {
        setRemoteModels([]);
        message.error(getProviderConnectionFailureMessage(error, t));
      }
    } finally {
      if (remoteModelRequest.current === request) setRemoteModelsLoading(false);
    }
  };

  const appendGroupModels = (
    providerId: string,
    groupId: string,
    nextModels: ProviderModel[],
    totalCount?: number,
  ) => {
    if (!nextModels.length) return;
    const key = `${providerId}:${groupId}`;
    groupModelVersions.current[key] = (groupModelVersions.current[key] || 0) + 1;
    setAddedProviderList((current) =>
      current.map((item) =>
        item.id === providerId
          ? {
              ...item,
                groups: item.groups.map((candidate) => {
                  if (candidate.id !== groupId) return candidate;
                  const models = new Map(candidate.models.map((model) => [model.id, model]));
                  const before = models.size;
                  for (const model of nextModels) models.set(model.id, model);
                  return { ...candidate, models: [...models.values()], modelCount: totalCount ?? Math.max(candidate.modelCount || 0, candidate.models.length) + models.size - before };
                }),
            }
          : item,
      ),
    );
  };

  const appendGroupModel = (providerId: string, groupId: string, model: ProviderModel) => appendGroupModels(providerId, groupId, [model]);

  const openCustomModelModal = (
    provider: AddedProvider,
    group: ProviderConnectionGroup,
  ) => {
    remoteModelRequest.current += 1;
    setRemoteModelsLoading(false);
    setContextWindowExpanded(false);
    setContextWindowMode("auto");
    setRemoteModels([]);
    setCustomModelModal({ provider, group });
    customModelForm.setFieldsValue({
      providerId: provider.id,
      groupId: group.id,
      capability: getSelectableModelCapabilities(provider.capabilities, provider.protocolName || provider.source)[0] || "LLM_CHAT",
      name: "",
      maxInputTokens: undefined,
    });
    customModelDraft.captureSavedValues();
  };

  const addCatalogModel = async (providerId: string, groupId: string, choice: CatalogModelChoice) => {
    const currentProvider = addedProviderListRef.current.find((provider) => provider.id === providerId);
    const currentGroup = currentProvider?.groups.find((group) => group.id === groupId);
    if (!currentGroup) throw new Error(t("modelProvider.workspace.savedRefreshFailed"));
    if (choice.model_type && !isModelTypeSupportedByProtocol(choice.model_type, currentProvider?.protocolName || currentProvider?.source))
      throw new Error(t("modelProvider.catalog.unsupportedType"));
    if (currentGroup.models.some((model) => normalizeModelName(model.name) === normalizeModelName(choice.name))) return;
    const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsPost({
      modelProviderId: providerId,
      groupId,
      addModelProviderGroupModelOpenAPIRequest: {
        name: choice.name.trim(),
        model_type: choice.model_type,
        vision: choice.model_type === "llm" && choice.vision === true,
        ...(choice.model_settings ? { model_settings: choice.model_settings } : {}),
        ...(choice.max_input_tokens && supportsStoredModelContext(choice.model_type) ? { max_input_tokens: choice.max_input_tokens } : {}),
      },
    });
    const created = unwrapModelProviderData<ApiModel>(response.data);
    appendGroupModel(providerId, groupId, {
      id: created.id,
      name: created.name,
      capability: mapModelTypeToCapability(created.model_type || choice.model_type),
      builtIn: Boolean(created.is_default),
      vision: created.vision,
      modelSettings: created.model_settings,
      enabled: true,
      maxInputTokens: created.max_input_tokens || choice.max_input_tokens,
    });
  };

  const addCatalogModels = async (providerId: string, groupId: string, choices: CatalogModelChoice[]) => {
    let succeeded = 0;
    let failed = 0;
    for (let offset = 0; offset < choices.length; offset += 200) {
      const batch = choices.slice(offset, offset + 200);
      try {
        const result = await addGroupModels(providerId, groupId, batch.map((choice) => ({
          name: choice.name.trim(),
          model_type: choice.model_type,
          vision: choice.model_type === "llm" && choice.vision === true,
          ...(choice.model_settings ? { model_settings: choice.model_settings } : {}),
          ...(choice.max_input_tokens && supportsStoredModelContext(choice.model_type) ? { max_input_tokens: choice.max_input_tokens } : {}),
        })));
        const saved = result.models || [];
        appendGroupModels(providerId, groupId, saved.map((model) => ({
          id: model.id,
          name: model.name,
          capability: mapModelTypeToCapability(model.model_type),
          builtIn: model.is_default,
          vision: model.vision,
          modelSettings: model.model_settings,
          enabled: true,
          maxInputTokens: model.max_input_tokens || undefined,
          inputTokenLimit: model.input_token_limit || undefined,
          maxOutputTokens: model.max_output_tokens || undefined,
        })), result.total_count);
        succeeded += saved.length;
        failed += result.failures?.length || 0;
      } catch {
        failed += batch.length;
      }
    }
    return { succeeded, failed };
  };

  const discoverModelsAfterKeySaved = async (
    provider: AddedProvider,
    groupId: string,
    isCurrent: () => boolean,
  ) => {
    const key = `${provider.id}:${groupId}`;
    if (!isCurrent() || keyDiscoveryRequests.current.has(key)) return;
    keyDiscoveryRequests.current.add(key);
    let discovered: RemoteGroupModel[] | undefined;
    let added = false;
    const openCatalog = () => {
      if (isCurrent()) setCatalogTarget({ providerId: provider.id, groupId, remoteModels: discovered });
    };
    try {
      // Read the saved selection: adding a second key must not enable it or
      // bypass a deliberately disabled supplier's current key.
      const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet({ modelProviderId: provider.id });
      let savedGroup = unwrapModelProviderData<{ groups?: ApiGroup[] }>(response.data).groups?.find((group) => group.id === groupId);
      const hasEnabledSavedKey = (candidate?: ApiGroup) => Boolean(candidate && (candidate.keys?.length ? candidate.keys.some((key) => key.enabled) : candidate.has_api_key));
      if (!isCurrent() || !savedGroup || !hasEnabledSavedKey(savedGroup)) return;
      if (!savedGroup.is_verified) {
        const currentModels = addedProviderListRef.current.find((item) => item.id === provider.id)?.groups.find((group) => group.id === groupId)?.models || [];
        const canonical = mapApiGroup(provider, savedGroup, []);
        const verified = await verifyProviderGroup(provider, { ...canonical, models: currentModels }, "", isCurrent);
        if (!isCurrent() || !verified) return;
        const refreshed = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet({ modelProviderId: provider.id });
        savedGroup = unwrapModelProviderData<{ groups?: ApiGroup[] }>(refreshed.data).groups?.find((group) => group.id === groupId);
        if (!isCurrent() || !savedGroup?.is_verified || !hasEnabledSavedKey(savedGroup)) return;
      }
      setDiscoveringGroupIds((current) => ({ ...current, [key]: true }));
      const version = groupModelVersions.current[key] || 0;
      const savedResponse = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet({ modelProviderId: provider.id, groupId });
      if (!isCurrent() || version !== (groupModelVersions.current[key] || 0)) return;
      const snapshot = mapApiGroup(provider, savedGroup, unwrapModelProviderData<{ models?: ApiModel[] }>(savedResponse.data).models || []);
      const mergeProvider = (item: AddedProvider): AddedProvider => ({
        ...item,
        groups: item.groups.some((group) => group.id === groupId)
          ? item.groups.map((group) => group.id === groupId ? snapshot : group)
          : [...item.groups, snapshot],
      });
      const mergeSnapshot = (current: AddedProvider[]) => current.some((item) => item.id === provider.id)
        ? current.map((item) => item.id === provider.id ? mergeProvider(item) : item)
        : [...current, mergeProvider(provider)];
      addedProviderListRef.current = mergeSnapshot(addedProviderListRef.current);
      setAddedProviderList(mergeSnapshot);

      const remote = await listRemoteGroupModels(provider.id, groupId);
      if (!isCurrent()) return;
      const unique = new Map<string, RemoteGroupModel>();
      for (const model of remote.models || []) {
        const name = normalizeModelName(model.name);
        if (name && !unique.has(name)) unique.set(name, model);
      }
      discovered = [...unique.values()];
      const currentSelection = () => {
        const current = addedProviderListRef.current.find((item) => item.id === provider.id)?.groups.find((group) => group.id === groupId);
        return isCurrent() && current?.verified && current.baseUrl === snapshot.baseUrl &&
          current.keys.find((key) => key.enabled)?.id === snapshot.keys.find((key) => key.enabled)?.id;
      };
      if (!currentSelection()) return;
      if (discovered.length >= 5) {
        openCatalog();
        return;
      }
      const protocol = provider.protocolName || provider.source;
      for (const model of discovered) {
        const current = addedProviderListRef.current.find((item) => item.id === provider.id)?.groups.find((group) => group.id === groupId);
        if (!current || !currentSelection()) return;
        if (current.models.some((saved) => normalizeModelName(saved.name) === normalizeModelName(model.name))) continue;
        const metadata = resolveRemoteModelMetadata(protocol, model);
        const modelType = metadata.modelType;
        if (!isModelTypeSupportedByProtocol(modelType, protocol)) {
          openCatalog();
          continue;
        }
        await addCatalogModel(provider.id, groupId, {
          name: model.name,
          model_type: modelType,
          max_input_tokens: model.max_input_tokens,
          vision: metadata.vision,
          model_settings: metadata.modelSettings,
        });
        added = true;
      }
    } catch (error) {
      if (isCurrent()) {
        if (discovered) openCatalog();
        message.error(getProviderConnectionFailureMessage(error, t));
      }
    } finally {
      if (added) void onConfigurationChanged?.();
      keyDiscoveryRequests.current.delete(key);
      setDiscoveringGroupIds((current) => { const next = { ...current }; delete next[key]; return next; });
    }
  };

  const closeCustomModelModal = () => {
    remoteModelRequest.current += 1;
    setRemoteModelsLoading(false);
    setContextWindowExpanded(false);
    setContextWindowMode("auto");
    setRemoteModels([]);
    setCustomModelModal(null);
    customModelForm.resetFields();
  };

  const openEditModelWindowModal = (
    provider: AddedProvider,
    group: ProviderConnectionGroup,
    model: ProviderModel,
  ) => setEditModelWindowModal({ provider, group, model });

  const saveEditedModel = async (values: ModelEditorValues) => {
    const target = editModelWindowModal;
    if (!target) return;
    if (!(values.capability === "UNKNOWN" && target.model.capability === "UNKNOWN") && !isModelTypeSupportedByProtocol(getModelTypeForCapability(values.capability), target.provider.protocolName || target.provider.source))
      throw new Error(t(values.capability === "UNKNOWN" ? "modelProvider.catalog.unclassifiedType" : "modelProvider.catalog.unsupportedType"));
    const updated = await updateGroupModel(target.provider.id, target.group.id, target.model.id, {
      name: values.name.trim(),
      model_type: getModelTypeForCapability(values.capability),
      vision: values.capability === "LLM_CHAT" && values.vision === true,
      ...(values.modelSettings !== undefined ? { model_settings: values.modelSettings } : {}),
      ...(supportsStoredModelContext(values.capability) ? { max_input_tokens: values.maxInputTokens, input_token_limit: values.inputTokenLimit, max_output_tokens: values.maxOutputTokens } : {}),
      ...(target.model.builtIn ? { customize: true } : {}),
    });
    const key = `${target.provider.id}:${target.group.id}`;
    groupModelVersions.current[key] = (groupModelVersions.current[key] || 0) + 1;
    setAddedProviderList((current) => current.map((provider) => provider.id === target.provider.id ? {
      ...provider,
      groups: provider.groups.map((group) => group.id === target.group.id ? {
        ...group,
        models: group.models.map((model) => model.id === target.model.id ? {
          ...model,
          name: updated.name,
          capability: mapModelTypeToCapability(updated.model_type),
          vision: updated.vision,
          modelSettings: updated.model_settings,
          maxInputTokens: updated.max_input_tokens,
          inputTokenLimit: updated.input_token_limit || undefined,
          maxOutputTokens: updated.max_output_tokens || undefined,
          builtIn: Boolean(updated.is_default),
        } : model),
      } : group),
    } : provider));
    message.success(t("modelProvider.message.modelUpdated"));
    void onConfigurationChanged?.();
  };

  const addCustomModel = async (values: CustomModelFormValues) => {
    if (customModelSavePending.current) return;
    const provider = addedProviderList.find(
      (item) => item.id === values.providerId,
    );
    const group = provider?.groups.find((item) => item.id === values.groupId);
    if (!provider || !group) {
      return;
    }

    if (!isModelTypeSupportedByProtocol(getModelTypeForCapability(values.capability), provider.protocolName || provider.source)) {
      customModelForm.setFields([{ name: "capability", errors: [t(values.capability === "UNKNOWN" ? "modelProvider.catalog.unclassifiedType" : "modelProvider.catalog.unsupportedType")] }]);
      return;
    }

    const normalizedName = normalizeModelName(values.name);
    const duplicated = group.models.some(
      (model) => normalizeModelName(model.name) === normalizedName,
    );

    if (duplicated) {
      customModelForm.setFields([
        {
          name: "name",
          errors: [t("modelProvider.validation.duplicateModelName")],
        },
      ]);
      return;
    }

    customModelSavePending.current = true;
    setCustomModelSaving(true);
    try {
      let maxInputTokens: string | null | undefined;
      if (
        supportsModelContext(values.capability) &&
        contextWindowMode === "manual"
      ) {
        maxInputTokens = parseLlmMaxInputTokens(values.maxInputTokens);
        if (!maxInputTokens) {
          customModelForm.setFields([
            {
              name: "maxInputTokens",
              errors: [t("modelProvider.validation.maxInputTokensInvalid")],
            },
          ]);
          return;
        }
      } else if (supportsModelContext(values.capability)) {
        const discoveredContext = remoteModels.find((model) => model.name.trim() === values.name.trim())?.max_input_tokens;
        if (discoveredContext?.trim()) maxInputTokens = parseLlmMaxInputTokens(discoveredContext) || undefined;
      }
      const createdModel = unwrapModelProviderData<ApiModel>(
        (
          await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsPost(
            {
              modelProviderId: provider.id,
              groupId: group.id,
              addModelProviderGroupModelOpenAPIRequest: {
                name: values.name.trim(),
                model_type: getModelTypeForCapability(values.capability),
                vision:
                  values.capability === "LLM_CHAT" && isModelFeatureEnabled(values.modelSettings, "vision", values.vision === true),
                ...(editableModelSettings(values.capability, values.modelSettings, values) !== undefined ? { model_settings: editableModelSettings(values.capability, values.modelSettings, values) } : {}),
                ...(maxInputTokens ? { max_input_tokens: maxInputTokens } : {}),
              },
            },
          )
        ).data,
      );
      const nextModel: ProviderModel = {
        id: createdModel.id,
        name: createdModel.name,
        capability: mapModelTypeToCapability(
          createdModel.model_type ||
            getModelTypeForCapability(values.capability),
        ),
        builtIn: Boolean(createdModel.is_default),
        vision: createdModel.vision,
        modelSettings: createdModel.model_settings,
        enabled: true,
        maxInputTokens:
          createdModel.max_input_tokens || maxInputTokens || undefined,
      };
      appendGroupModel(provider.id, group.id, nextModel);
      message.success(t("modelProvider.message.modelAdded"));
      void onConfigurationChanged?.();
      closeCustomModelModal();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      customModelSavePending.current = false;
      setCustomModelSaving(false);
    }
  };

  const deleteCustomModel = (
    providerId: string,
    groupId: string,
    model: ProviderModel,
    notify = true,
    confirmIndexedDowngrade = false,
  ) => {
    const operationKey = `${providerId}:${groupId}:${model.id}`;
    const existing = modelDeleteRequests.current.get(operationKey);
    if (existing) return existing;
    const operation = Promise.resolve().then(async () => {
      try {
        await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsModelIdDelete(
          {
            modelProviderId: providerId,
            groupId,
            modelId: model.id,
          },
          model.capability === "EMBEDDING" && confirmIndexedDowngrade
            ? { params: { confirm_indexed_downgrade: true } }
            : undefined,
        );
        const key = `${providerId}:${groupId}`;
        groupModelVersions.current[key] = (groupModelVersions.current[key] || 0) + 1;
        setAddedProviderList((current) =>
          current.map((provider) =>
            provider.id === providerId
              ? {
                  ...provider,
                  groups: provider.groups.map((group) =>
                    group.id === groupId
                      ? {
                          ...group,
                          models: group.models.filter(
                            (item) => item.id !== model.id,
                          ),
                          modelCount: Math.max(0, (group.modelCount ?? group.models.length) - 1),
                        }
                      : group,
                  ),
                }
              : provider,
          ),
        );
        if (notify) {
          message.success(t("modelProvider.message.modelDeleted"));
          void onConfigurationChanged?.();
        }
        return true;
      } catch (error) {
        const response = (error as { response?: { status?: number; data?: { detail?: unknown; message?: unknown } } })?.response;
        const detail = String(response?.data?.detail || response?.data?.message || "");
        const needsEmbeddingConfirmation = model.capability === "EMBEDDING" &&
          response?.status === 409 && detail.includes("requires confirmation to downgrade");
        if (needsEmbeddingConfirmation) {
          Modal.confirm({
            centered: true,
            title: t("modelProvider.confirmDeleteModel", { name: model.name }),
            content: t("modelProvider.confirmDeleteEmbeddingDesc"),
            okText: t("common.delete"),
            cancelText: t("common.cancel"),
            okButtonProps: { danger: true },
            onOk: () => deleteCustomModel(providerId, groupId, model, notify, true),
          });
        } else if (notify) message.error(getLocalizedErrorMessage(error));
        return false;
      } finally {
        modelDeleteRequests.current.delete(operationKey);
      }
    });
    modelDeleteRequests.current.set(operationKey, operation);
    return operation;
  };

  const catalogProvider = addedProviderList.find((provider) => provider.id === catalogTarget?.providerId);
  const catalogGroup = catalogProvider?.groups.find((group) => group.id === catalogTarget?.groupId);

  return (
    <div className="model-provider-page-content">
      <ProviderWorkspace
        verificationEvent={verificationEvent}
        providers={providerOptions}
        configuredProviders={addedProviderList}
        loading={loading}
        loadError={loadError}
        onRetry={() => void loadModelProviders()}
        highlightProviderId={
          highlightProviderId || legacyProviderLocation.item || undefined
        }
        onBack={onBack}
        capabilityLabel={getCapabilityLabel}
        onLoadModels={(provider, group) =>
          void loadGroupModels(provider.id, group.id)
        }
        modelsLoading={{ ...loadingGroupModelIds, ...discoveringGroupIds }}
        modelsError={modelsError}
        onAddModel={openCustomModelModal}
        onSyncModels={(provider, group) =>
          {
            setCatalogTarget({ providerId: provider.id, groupId: group.id });
            void loadGroupModels(provider.id, group.id);
          }
        }
        onEditModel={openEditModelWindowModal}
        onDeleteModel={(provider, group, model) =>
          deleteCustomModel(provider.id, group.id, model)
        }
        onCheck={(provider, group) =>
          void verifyProviderGroup(provider, group, "")
        }
        checking={verifyingGroupIds}
        onKeySaved={discoverModelsAfterKeySaved}
        onReload={async () => {
          const refreshed = await loadModelProviders();
          void onConfigurationChanged?.();
          if (!refreshed)
            throw new Error(t("modelProvider.workspace.savedRefreshFailed"));
        }}
        cloudContent={
          <>
            <CloudSystemProviderCard
              state={cloudSystemState}
              models={cloudSystemModels}
              onLogin={() => void beginSystemCloudLogin()}
              onOpenPlan={() => void openSystemCloudPlan()}
              onRetry={() => void loadCloudSystemProvider()}
            />
            <CredentialBackupPanel
              available={credentialBackupAvailable}
              loading={credentialBackupLoading}
              status={credentialBackupStatus}
              onRetry={() => void loadCredentialBackup()}
              onToggle={(enabled) => void toggleCredentialBackup(enabled)}
            />
            <CredentialRestorePanel
              loading={credentialRestoreLoading}
              status={credentialRestoreStatus}
              onCancel={() => void cancelRestore()}
              onRefresh={() => void loadCredentialRestore()}
              onStart={(mode, resolution) =>
                void startRestore(mode, resolution)
              }
            />
          </>
        }
      />

      {catalogProvider && catalogGroup ? <ProviderModelCatalogDrawer
        open={!!catalogTarget}
        providerId={catalogProvider.id}
        providerName={getProviderDisplayName(catalogProvider, currentLanguage)}
        providerLogoDataUrl={catalogProvider.logoDataUrl}
        protocolName={catalogProvider.protocolName || catalogProvider.source}
        groupId={catalogGroup.id}
        models={catalogGroup.models}
        initialRemoteModels={catalogTarget?.remoteModels}
        modelsLoading={loadingGroupModelIds[`${catalogProvider.id}:${catalogGroup.id}`] || discoveringGroupIds[`${catalogProvider.id}:${catalogGroup.id}`]}
        requiresVerification={!catalogGroup.verified && !canDiscoverWithoutApiKey(catalogProvider, catalogGroup)}
        onCheckConnection={() => {
          setCatalogTarget(null);
          void verifyProviderGroup(catalogProvider, catalogGroup, "");
        }}
        onClose={() => setCatalogTarget(null)}
        onRefreshModels={() => void loadGroupModels(catalogProvider.id, catalogGroup.id)}
        onAdd={(model) => addCatalogModel(catalogProvider.id, catalogGroup.id, model)}
        onAddMany={(models) => addCatalogModels(catalogProvider.id, catalogGroup.id, models)}
        onRemove={(model) => deleteCustomModel(catalogProvider.id, catalogGroup.id, model, false)}
        onChanged={() => void onConfigurationChanged?.()}
      /> : null}

      <ModelEditorModal
        model={editModelWindowModal?.model || null}
        providerName={editModelWindowModal ? getProviderDisplayName(editModelWindowModal.provider, currentLanguage) : ""}
        protocolName={editModelWindowModal?.provider.protocolName || editModelWindowModal?.provider.source}
        capabilities={editModelWindowModal ? getSelectableModelCapabilities(editModelWindowModal.provider.capabilities, editModelWindowModal.provider.protocolName || editModelWindowModal.provider.source) : []}
        models={editModelWindowModal?.group.models || []}
        capabilityLabel={getCapabilityLabel}
        onSave={saveEditedModel}
        onClose={() => setEditModelWindowModal(null)}
      />

      <Modal
        centered
        destroyOnHidden
        okText={t("modelProvider.add")}
        open={!!customModelModal}
        title={t("modelProvider.addCustomModelTitle", {
          name: customModelModal ? getProviderDisplayName(customModelModal.provider, currentLanguage) : "",
        })}
        width={520}
        confirmLoading={customModelSaving}
        closable={!customModelSaving}
        maskClosable={!customModelSaving}
        keyboard={!customModelSaving}
        onCancel={() => { if (!customModelSaving) customModelDraft.confirmClose(closeCustomModelModal); }}
        onOk={() => customModelForm.submit()}
      >
        <Form<CustomModelFormValues>
          autoComplete="off"
          className="model-provider-form"
          disabled={customModelSaving}
          form={customModelForm}
          layout="vertical"
          onFinish={addCustomModel}
        >
          <Form.Item
            label={t("modelProvider.provider")}
            name="providerId"
            rules={[
              {
                required: true,
                message: t("modelProvider.validation.providerRequired"),
              },
            ]}
          >
            <Select disabled>
              {customModelModal ? (
                <Select.Option value={customModelModal.provider.id}>
                  {getProviderDisplayName(customModelModal.provider, currentLanguage)}
                </Select.Option>
              ) : null}
            </Select>
          </Form.Item>

          <Form.Item name="groupId" hidden>
            <Input />
          </Form.Item>
          <Form.Item
            extra={t("modelProvider.modelNameExtra")}
            label={t("modelProvider.modelName")}
            name="name"
            normalize={(value: string | undefined) => value?.trim()}
            rules={[
              {
                required: true,
                message: t("modelProvider.validation.modelNameRequired"),
              },
              { validator: (_, value?: string) => !value || new TextEncoder().encode(value.trim()).length <= 512
                ? Promise.resolve() : Promise.reject(new Error(t("modelProvider.editor.nameBytes"))) },
            ]}
          >
            <AutoComplete
              options={remoteModels.map((item) => ({ value: item.name }))}
              filterOption={(input, option) =>
                String(option?.value || "")
                  .toLowerCase()
                  .includes(input.trim().toLowerCase())
              }
            >
              <Input
                allowClear
                autoComplete="off"
                autoCorrect="off"
                maxLength={512}
                placeholder={t("modelProvider.modelNamePlaceholder")}
                spellCheck={false}
                addonAfter={
                  <Button
                    aria-label={t("modelProvider.fetchAvailableModels")}
                    loading={remoteModelsLoading}
                    disabled={!customModelModal || (!customModelModal.group.verified && !canDiscoverWithoutApiKey(customModelModal.provider, customModelModal.group))}
                    title={!customModelModal || (!customModelModal.group.verified && !canDiscoverWithoutApiKey(customModelModal.provider, customModelModal.group)) ? t("modelProvider.catalog.verifyBeforeSync") : undefined}
                    size="small"
                    type="text"
                    icon={<SearchOutlined />}
                    onClick={() => void loadRemoteModelNames()}
                  />
                }
              />
            </AutoComplete>
          </Form.Item>

          <Form.Item
            label={t("modelProvider.modelType")}
            name="capability"
            rules={[
              {
                required: true,
                message: t("modelProvider.validation.modelTypeRequired"),
              },
            ]}
          >
            <Select
              onChange={(value) => {
                if (!supportsModelContext(value)) {
                  setContextWindowExpanded(false);
                  setContextWindowMode("auto");
                  customModelForm.setFieldValue("maxInputTokens", undefined);
                }
              }}
            >
              {getSelectableModelCapabilities(customModelModal?.provider.capabilities || [], customModelModal?.provider.protocolName || customModelModal?.provider.source).map((capability) => (
                <Select.Option key={capability} value={capability}>
                  <span className="provider-model-task-option">
                    <ModelTypeIcon type={getModelTaskType(capability)} size={16} />
                    {getCapabilityLabel(capability)}
                  </span>
                </Select.Option>
              ))}
            </Select>
          </Form.Item>

          {supportsModelContext(watchedCustomCapability) ? <ModelFeaturesEditor disabled={customModelSaving} visualModel={watchedCustomCapability === "VLM"} /> : null}
          {watchedCustomCapability === "EMBEDDING" ? <EmbeddingOptionsEditor disabled={customModelSaving} /> : null}

          {supportsModelContext(watchedCustomCapability) ? (
            <div className="model-provider-context-window">
              <button
                aria-expanded={contextWindowExpanded}
                aria-label={
                  contextWindowExpanded
                    ? t("modelProvider.maxInputTokensCollapse")
                    : t("modelProvider.maxInputTokensExpand")
                }
                className="model-provider-context-window-toggle"
                type="button"
                onClick={toggleContextWindow}
              >
                <span>{t("modelProvider.maxInputTokensLabel")}</span>
                <RightOutlined
                  className={contextWindowExpanded ? "is-expanded" : undefined}
                />
              </button>
              <Form.Item
                hidden={!contextWindowExpanded}
                name="maxInputTokens"
                normalize={(value: string | undefined) => value?.trim()}
                rules={
                  contextWindowMode === "manual"
                    ? [
                        {
                          required: true,
                          message: t(
                            "modelProvider.validation.maxInputTokensRequired",
                          ),
                        },
                        {
                          validator: (_, value?: string) =>
                            parseLlmMaxInputTokens(value)
                              ? Promise.resolve()
                              : Promise.reject(
                                  new Error(
                                    t(
                                      "modelProvider.validation.maxInputTokensInvalid",
                                    ),
                                  ),
                                ),
                        },
                      ]
                    : []
                }
              >
                <Input
                  autoComplete="off"
                  className="model-provider-context-window-input"
                  maxLength={LLM_MAX_INPUT_TOKENS_MAX_LENGTH}
                  placeholder={t("modelProvider.maxInputTokensAutoPlaceholder")}
                  onChange={markContextWindowManual}
                />
              </Form.Item>
            </div>
          ) : null}
        </Form>
      </Modal>
    </div>
  );
}
