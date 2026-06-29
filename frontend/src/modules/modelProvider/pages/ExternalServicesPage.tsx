import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, AutoComplete, Button, Empty, Form, Input, Modal, Space, Spin, Tag, Tooltip, message } from "antd";
import {
  CloudServerOutlined,
  CompassOutlined,
  CopyOutlined,
  DeleteOutlined,
  EyeInvisibleOutlined,
  EyeOutlined,
  FilePdfOutlined,
  GoogleOutlined,
  InfoCircleFilled,
  PlusOutlined,
  ReadOutlined,
  RightOutlined,
  ScanOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  modelProvidersApi,
  modelProvidersDefaultApi,
  unwrapModelProviderData,
  withModelProviderJsonOptions,
} from "../api";
import ToolManagementSection from "../components/ToolManagementSection";

type ServiceCategoryKey = "parsing" | "search" | "academic";
type ServiceProviderCategory = "ocr" | "search" | "datasource";
type ServiceTone = "blue" | "cyan" | "green" | "red" | "violet";

interface ExternalServiceConfig {
  key: string;
  name: string;
  description: string;
  summary: string;
  category: ServiceCategoryKey;
  fields: Array<keyof ExternalServiceFormValues>;
  logo: JSX.Element;
  logoUrl: string;
  tone: ServiceTone;
  status: "configured" | "missing" | "tbd";
  baseUrl?: string;
  baseUrlPresets?: BaseUrlPreset[];
}

interface ExternalServiceFormValues {
  baseUrl?: string;
  apiKey?: string;
  searchEngineId?: string;
}

interface BaseUrlPreset {
  key?: string;
  labelKey?: string;
  descKey?: string;
  value: string;
}

interface ExternalServicesPageProps {
  section?: "parsing" | "tools";
}

interface ApiExternalProvider {
  base_url?: string;
  base_url_presets?: Array<{
    key?: string;
    value?: string;
  }>;
  capabilities?: string[];
  category?: string;
  description?: string;
  id: string;
  is_configured?: boolean;
  name: string;
}

interface ApiExternalGroup {
  base_url?: string;
  id: string;
  is_verified?: boolean;
  name?: string;
  user_model_provider_id?: string;
}

interface CheckExternalServiceResult {
  success: boolean;
  message?: string;
}

interface SaveExternalGroupResponse extends ApiExternalGroup {
  check?: CheckExternalServiceResult;
}

const serviceCategories: Array<{
  key: ServiceCategoryKey;
  titleKey: string;
  descKey: string;
  icon: JSX.Element;
}> = [
  {
    key: "parsing",
    titleKey: "modelProvider.external.parsingCategoryTitle",
    descKey: "modelProvider.external.parsingCategoryDesc",
    icon: <CloudServerOutlined />,
  },
  {
    key: "search",
    titleKey: "modelProvider.external.toolsCategoryTitle",
    descKey: "modelProvider.external.toolsCategoryDesc",
    icon: <SearchOutlined />,
  },
  {
    key: "academic",
    titleKey: "modelProvider.external.academicCategoryTitle",
    descKey: "modelProvider.external.academicCategoryDesc",
    icon: <ReadOutlined />,
  },
];

const externalServiceConfigs: ExternalServiceConfig[] = [
  {
    key: "mineru",
    name: "MinerU",
    description: "",
    summary: "",
    category: "parsing",
    fields: ["baseUrl", "apiKey"],
    logo: <FilePdfOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=mineru.net&sz=96",
    tone: "blue",
    status: "configured",
  },
  {
    key: "paddleocr",
    name: "PaddleOCR",
    description: "",
    summary: "",
    category: "parsing",
    fields: ["baseUrl", "apiKey"],
    logo: <ScanOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=paddleocr.ai&sz=96",
    tone: "cyan",
    status: "tbd",
  },
  {
    key: "bingSearch",
    name: "Bing Search",
    description: "",
    summary: "",
    category: "search",
    fields: ["apiKey"],
    logo: <SearchOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=bing.com&sz=96",
    tone: "green",
    status: "missing",
  },
  {
    key: "googleSearch",
    name: "Google Custom Search",
    description: "",
    summary: "",
    category: "search",
    fields: ["apiKey", "searchEngineId"],
    logo: <GoogleOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=google.com&sz=96",
    tone: "red",
    status: "configured",
  },
  {
    key: "bocha",
    name: "Bocha",
    description: "",
    summary: "",
    category: "search",
    fields: ["apiKey"],
    logo: <SearchOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=bochaai.com&sz=96",
    tone: "green",
    status: "missing",
  },
  {
    key: "tavily",
    name: "Tavily",
    description: "",
    summary: "",
    category: "search",
    fields: ["apiKey"],
    logo: <CompassOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=tavily.com&sz=96",
    tone: "violet",
    status: "missing",
  },
  {
    key: "sciverse",
    name: "Sciverse",
    description: "",
    summary: "",
    category: "academic",
    fields: ["apiKey"],
    logo: <SearchOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=sciverse.space&sz=96",
    tone: "violet",
    status: "missing",
  },
];

const fallbackServiceByName = new Map<string, ExternalServiceConfig>(
  externalServiceConfigs.map((service) => [normalizeProviderName(service.name), service])
);

const serviceToneByCategory: Record<ServiceCategoryKey, ServiceTone> = {
  parsing: "blue",
  search: "green",
  academic: "violet",
};

function normalizeProviderName(value: string) {
  return value.trim().toLowerCase().replace(/[^a-z0-9]+/g, "");
}

function normalizeBaseUrlForCompare(value?: string) {
  return (value || "").trim().replace(/\/+$/, "");
}

function validateHttpBaseUrl(value?: string) {
  const normalizedValue = (value || "").trim();
  if (!normalizedValue) {
    return false;
  }
  try {
    const parsedUrl = new URL(normalizedValue);
    return parsedUrl.protocol === "http:" || parsedUrl.protocol === "https:";
  } catch {
    return false;
  }
}

function isFormValidationError(error: unknown) {
  return (
    !!error &&
    typeof error === "object" &&
    Array.isArray((error as { errorFields?: unknown[] }).errorFields)
  );
}

function getCheckFailureMessage(checkResult?: CheckExternalServiceResult): string | undefined {
  if (!checkResult || typeof checkResult !== "object") {
    return undefined;
  }

  if (typeof checkResult.message === "string" && checkResult.message.trim()) {
    return checkResult.message.trim();
  }

  return undefined;
}

function isGoogleCustomSearch(service?: ExternalServiceConfig | null) {
  return normalizeProviderName(service?.name || "") === "googlecustomsearch";
}

function getServiceProviderCategory(service: ExternalServiceConfig): ServiceProviderCategory {
  if (service.category === "parsing") {
    return "ocr";
  }
  if (service.category === "academic") {
    return "datasource";
  }
  return "search";
}

function isCustomServiceBaseUrl(service: ExternalServiceConfig, baseUrl?: string) {
  if (!service.fields.includes("baseUrl")) {
    return false;
  }
  return normalizeBaseUrlForCompare(baseUrl) !== normalizeBaseUrlForCompare(service.baseUrl);
}

function mapProviderCategory(category?: string): ServiceCategoryKey {
  const normalizedCategory = category?.trim().toLowerCase();
  if (normalizedCategory === "ocr" || normalizedCategory === "parse" || normalizedCategory === "parsing") {
    return "parsing";
  }
  if (normalizedCategory === "datasource" || normalizedCategory === "academic") {
    return "academic";
  }
  return "search";
}

function getProviderLogoUrl(name: string) {
  const normalizedName = normalizeProviderName(name);
  if (!normalizedName) {
    return "";
  }
  return `https://www.google.com/s2/favicons?domain=${encodeURIComponent(normalizedName)}.com&sz=96`;
}

function getProviderIcon(category: ServiceCategoryKey) {
  if (category === "parsing") {
    return <ScanOutlined />;
  }
  return category === "academic" ? <ReadOutlined /> : <SearchOutlined />;
}

function getServiceFields(provider: ApiExternalProvider, category: ServiceCategoryKey): Array<keyof ExternalServiceFormValues> {
  if (category !== "parsing") {
    return ["apiKey"];
  }
  return provider.base_url ? ["baseUrl", "apiKey"] : ["apiKey"];
}

function serviceMatchesKeyword(service: ExternalServiceConfig, keyword: string) {
  const normalizedKeyword = keyword.trim().toLowerCase();
  if (!normalizedKeyword) {
    return true;
  }
  return [service.name, service.description, service.summary].some((value) =>
    value.toLowerCase().includes(normalizedKeyword),
  );
}

function getBaseUrlPresetLabelKey(serviceName: string, presetKey?: string) {
  if (normalizeProviderName(serviceName) !== "mineru") {
    return undefined;
  }
  if (presetKey === "local") {
    return "modelProvider.external.mineruLocalPreset";
  }
  return "modelProvider.external.mineruOfficialPreset";
}

function getBaseUrlPresetDescKey(serviceName: string, presetKey?: string) {
  if (normalizeProviderName(serviceName) !== "mineru") {
    return undefined;
  }
  if (presetKey === "local") {
    return "modelProvider.external.mineruLocalPresetDesc";
  }
  return "modelProvider.external.mineruOfficialPresetDesc";
}

function createBaseUrlPreset(
  serviceName: string,
  value: string,
  presetKey?: string,
): BaseUrlPreset | null {
  const trimmedValue = value.trim();
  if (!trimmedValue) {
    return null;
  }
  return {
    key: presetKey,
    value: trimmedValue,
    labelKey: getBaseUrlPresetLabelKey(serviceName, presetKey),
    descKey: getBaseUrlPresetDescKey(serviceName, presetKey),
  };
}

function mapBaseUrlPresets(provider: ApiExternalProvider, fallback?: ExternalServiceConfig): BaseUrlPreset[] | undefined {
  const apiPresets: BaseUrlPreset[] = [];
  (provider.base_url_presets || []).forEach((preset) => {
    const nextPreset = createBaseUrlPreset(provider.name, preset.value || "", preset.key);
    if (nextPreset) {
      apiPresets.push(nextPreset);
    }
  });

  if (apiPresets.length > 0) {
    return apiPresets;
  }

  const officialPreset = createBaseUrlPreset(provider.name, provider.base_url || "", "official");
  if (officialPreset) {
    return [officialPreset];
  }

  return fallback?.baseUrlPresets;
}

function mapApiProviderToService(provider: ApiExternalProvider, t: ReturnType<typeof useTranslation>["t"]): ExternalServiceConfig {
  const fallback = fallbackServiceByName.get(normalizeProviderName(provider.name));
  const category = fallback?.category || mapProviderCategory(provider.category);
  const description = provider.description?.trim() || fallback?.description || t("modelProvider.external.providerDescriptionFallback");

  return {
    key: provider.id,
    name: provider.name,
    description,
    summary: description,
    category,
    fields: fallback?.fields || getServiceFields(provider, category),
    logo: fallback?.logo || getProviderIcon(category),
    logoUrl: fallback?.logoUrl || getProviderLogoUrl(provider.name),
    tone: fallback?.tone || serviceToneByCategory[category],
    status: provider.is_configured ? "configured" : "missing",
    baseUrl: provider.base_url,
    baseUrlPresets: mapBaseUrlPresets(provider, fallback),
  };
}

async function fetchExternalProviders(keyword: string, signal: AbortSignal) {
  const normalizedKeyword = keyword.trim();
  const response = await modelProvidersApi.apiCoreModelProvidersGet(
    {
      excludeCategory: "model,datasource",
      keyword: normalizedKeyword || undefined,
    },
    { signal },
  );
  const providers = unwrapModelProviderData<{ providers?: ApiExternalProvider[] }>(response.data).providers || [];

  const shouldLoadSciverse =
    !normalizedKeyword ||
    normalizeProviderName("Sciverse").includes(normalizeProviderName(normalizedKeyword));
  if (!shouldLoadSciverse) {
    return providers;
  }

  let sciverseProvider: ApiExternalProvider | undefined;
  try {
    const sciverseResponse = await modelProvidersApi.apiCoreModelProvidersGet(
      {
        category: "datasource",
        keyword: "Sciverse",
      },
      { signal },
    );
    const datasourceProviders =
      unwrapModelProviderData<{ providers?: ApiExternalProvider[] }>(sciverseResponse.data).providers || [];
    sciverseProvider = datasourceProviders.find(
      (provider) => normalizeProviderName(provider.name) === "sciverse",
    );
  } catch (error) {
    if (
      signal.aborted ||
      (error && typeof error === "object" && "name" in error && error.name === "AbortError")
    ) {
      throw error;
    }
    return providers;
  }

  if (!sciverseProvider) {
    return providers;
  }

  const existingProviderNames = new Set(providers.map((provider) => normalizeProviderName(provider.name)));
  if (existingProviderNames.has("sciverse")) {
    return providers;
  }

  return [...providers, sciverseProvider];
}

async function listProviderGroups(serviceKey: string) {
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGet({
    modelProviderId: serviceKey,
  });
  return unwrapModelProviderData<{ groups?: ApiExternalGroup[] }>(response.data);
}

async function updateProviderGroup(
  service: ExternalServiceConfig,
  group: ApiExternalGroup,
  baseUrl: string,
) {
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdPatch({
    modelProviderId: service.key,
    groupId: group.id,
    updateModelProviderGroupOpenAPIRequest: {
      name: group.name || service.name,
      base_url: baseUrl,
      verify: false,
    },
  });
  return unwrapModelProviderData<SaveExternalGroupResponse>(response.data);
}

async function createProviderGroup(
  service: ExternalServiceConfig,
  payload: { name: string; base_url: string; api_key?: string; verify: boolean },
) {
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsPost(
    {
      modelProviderId: service.key,
      createModelProviderGroupOpenAPIRequest: payload,
    },
    payload.api_key ? { timeout: 3 * 60 * 1000 } : undefined,
  );
  return unwrapModelProviderData<SaveExternalGroupResponse>(response.data);
}

function selectServiceProvider(service: ExternalServiceConfig, groupId: string) {
  return modelProvidersApi.apiCoreModelProvidersSelectedProvidersPut({
    setSelectedProviderOpenAPIRequest: {
      selections: [{ category: getServiceProviderCategory(service), group_id: groupId }],
    },
  });
}

function ExternalServiceLogo({ service }: { service: ExternalServiceConfig }) {
  const [imageReady, setImageReady] = useState(false);

  return (
    <span className={`model-provider-service-logo model-provider-service-logo-${service.tone}`}>
      {!imageReady ? <span className="model-provider-service-logo-icon">{service.logo}</span> : null}
      <img
        alt=""
        className={imageReady ? "is-loaded" : undefined}
        loading="lazy"
        referrerPolicy="no-referrer"
        src={service.logoUrl}
        onLoad={() => setImageReady(true)}
        onError={(event) => {
          event.currentTarget.style.display = "none";
        }}
      />
    </span>
  );
}

export default function ExternalServicesPage({ section = "parsing" }: ExternalServicesPageProps) {
  const { t } = useTranslation();
  const [form] = Form.useForm<Record<string, ExternalServiceFormValues>>();
  const [activeService, setActiveService] = useState<ExternalServiceConfig | null>(null);
  const [services, setServices] = useState<ExternalServiceConfig[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const requestIdRef = useRef(0);
  const normalizedSearchValue = "";
  const [categorySearchValues, setCategorySearchValues] = useState<Record<"parsing" | "search", string>>({
    parsing: "",
    search: "",
  });

  // Multi-key state
  const [keyList, setKeyList] = useState<string[]>([]);
  const [newKeyValue, setNewKeyValue] = useState("");
  const [newKeyEngineId, setNewKeyEngineId] = useState("");
  const [addingKey, setAddingKey] = useState(false);
  const [savingServiceConfig, setSavingServiceConfig] = useState(false);
  const [visibleKeys, setVisibleKeys] = useState<Set<number>>(new Set());
  const [groupForActiveService, setGroupForActiveService] = useState<ApiExternalGroup | null>(null);
  const originalBaseUrlRef = useRef("");
  const loadGroupKeysGenRef = useRef(0);

  const loadExternalServices = useCallback((keyword: string) => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    const controller = new AbortController();

    setLoading(true);
    setLoadError(null);

    fetchExternalProviders(keyword, controller.signal)
      .then((providers) => {
        if (requestIdRef.current !== requestId) {
          return;
        }
        setServices(providers.map((provider) => mapApiProviderToService(provider, t)));
      })
      .catch((error) => {
        if (controller.signal.aborted || requestIdRef.current !== requestId) {
          return;
        }
        setServices([]);
        setLoadError(getLocalizedErrorMessage(error, t("modelProvider.external.loadFailed")) || t("modelProvider.external.loadFailed"));
      })
      .finally(() => {
        if (requestIdRef.current === requestId) {
          setLoading(false);
        }
      });

    return () => controller.abort();
  }, [t]);

  useEffect(() => loadExternalServices(normalizedSearchValue), [loadExternalServices, normalizedSearchValue]);

  function maskAPIKey(raw: string) {
    const trimmed = raw.trim();
    if (trimmed.length <= 8) {
      return "*".repeat(trimmed.length);
    }
    return `${trimmed.slice(0, 4)}****...${trimmed.slice(-4)}`;
  }

  function toggleKeyVisibility(idx: number) {
    setVisibleKeys((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) {
        next.delete(idx);
      } else {
        next.add(idx);
      }
      return next;
    });
  }

  async function writeTextToClipboard(text: string) {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.readOnly = true;
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    textarea.style.top = "0";
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    let copied = false;
    try {
      if (typeof document.execCommand === "function") {
        copied = document.execCommand("copy");
      }
    } finally {
      document.body.removeChild(textarea);
    }
    if (copied) {
      return;
    }

    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return;
    }

    throw new Error("Copy command failed");
  }

  async function copyKeyToClipboard(key: string) {
    try {
      await writeTextToClipboard(key);
      message.success(t("common.copySuccess"));
    } catch {
      message.error(t("common.copyFailedManual"));
    }
  }

  async function loadGroupKeys(serviceKey: string) {
    const gen = loadGroupKeysGenRef.current;
    try {
      const groupData = await listProviderGroups(serviceKey);
      if (loadGroupKeysGenRef.current !== gen) return;
      const group = (groupData.groups || [])[0] || null;
      setGroupForActiveService(group);
      if (group) {
        const rawKey = (group as any).api_key || "";
        const keys = rawKey.split("\n").map((k: string) => k.trim()).filter(Boolean);
        setKeyList(keys);
        // When the group has a custom base_url, use it as the initial form value.
        // This ensures the user's previously-saved base_url is shown after page refresh,
        // not the catalog default from user_model_providers.base_url.
        if (group.base_url) {
          form.setFieldValue([serviceKey, "baseUrl"], group.base_url);
          originalBaseUrlRef.current = group.base_url;
        }
      } else {
        setKeyList([]);
      }
    } catch {
      if (loadGroupKeysGenRef.current !== gen) return;
      setGroupForActiveService(null);
      setKeyList([]);
    }
  }

  async function loadFirstGroup(serviceKey: string) {
    const groupData = await listProviderGroups(serviceKey);
    return (groupData.groups || [])[0] || null;
  }

  async function handleBaseUrlChange() {
    if (!activeService) {
      return;
    }
    const currentUrl = form.getFieldValue([activeService.key, "baseUrl"]) || "";
    if (currentUrl === originalBaseUrlRef.current) {
      return;
    }
    if (!currentUrl.trim()) {
      form.setFieldValue([activeService.key, "baseUrl"], originalBaseUrlRef.current);
      return;
    }

    const isRealChange = normalizeBaseUrlForCompare(currentUrl) !== normalizeBaseUrlForCompare(originalBaseUrlRef.current);

    if (keyList.length === 0) {
      // No keys: update backend if group exists, otherwise just update ref
      if (groupForActiveService) {
        try {
          await updateProviderGroup(activeService, groupForActiveService, currentUrl);
          message.success(t("modelProvider.external.baseUrlChanged"));
        } catch (error) {
          message.error(getLocalizedErrorMessage(error, t("modelProvider.external.saveFailed")));
          return;
        }
      }
      originalBaseUrlRef.current = currentUrl;
      return;
    }

    if (!isRealChange) {
      // Trivial change (e.g. trailing slash): PATCH without confirm, keep keyList
      try {
        await updateProviderGroup(activeService, groupForActiveService!, currentUrl);
        message.success(t("modelProvider.external.baseUrlChanged"));
        originalBaseUrlRef.current = currentUrl;
      } catch (error) {
        message.error(getLocalizedErrorMessage(error, t("modelProvider.external.saveFailed")));
      }
      return;
    }

    // Real change + has keys: show confirmation dialog, backend will clear keys
    Modal.confirm({
      title: t("modelProvider.external.baseUrlChangeTitle"),
      content: t("modelProvider.external.baseUrlChangeContent", { count: keyList.length }),
      okText: t("modelProvider.external.confirmChange"),
      cancelText: t("modelProvider.external.cancelChange"),
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          const updatedGroup = await updateProviderGroup(activeService, groupForActiveService!, currentUrl);
          setKeyList([]);
          setGroupForActiveService(updatedGroup);
          loadGroupKeysGenRef.current += 1;
          originalBaseUrlRef.current = currentUrl;
          if (isCustomServiceBaseUrl(activeService, currentUrl)) {
            await selectServiceProvider(activeService, updatedGroup.id);
          }
          message.success(t("modelProvider.external.baseUrlChanged"));
          void loadExternalServices(normalizedSearchValue);
          closeConfigModal();
        } catch (error) {
          message.error(getLocalizedErrorMessage(error, t("modelProvider.external.saveFailed")));
        }
      },
      onCancel: () => {
        form.setFieldValue([activeService.key, "baseUrl"], originalBaseUrlRef.current);
      },
    });
  }

  async function handleAddKey() {
    if (!activeService) {
      return;
    }
    const rawKey = newKeyValue.trim();
    if (!rawKey) {
      return;
    }
    const engineId = newKeyEngineId.trim();
    const isGoogle = isGoogleCustomSearch(activeService);
    if (isGoogle && !engineId) {
      return;
    }
    const apiKey = isGoogle ? `${rawKey}|${engineId}` : rawKey;

    setAddingKey(true);
    try {
      if (!groupForActiveService) {
        // Create group with first key
        const baseUrl = form.getFieldValue([activeService.key, "baseUrl"]) || activeService.baseUrl || "";
        const payload: Record<string, unknown> = {
          name: activeService.name,
          base_url: baseUrl,
          api_key: apiKey,
          verify: true,
        };
        const savedGroup = await createProviderGroup(
          activeService,
          payload as { name: string; base_url: string; api_key?: string; verify: boolean },
        );
        if (savedGroup.check && savedGroup.check.success !== true) {
          message.error(getCheckFailureMessage(savedGroup.check) || t("modelProvider.external.checkFailed"));
          return;
        }
        setGroupForActiveService(savedGroup);
        setKeyList([apiKey]);

        // Select the provider
        await selectServiceProvider(activeService, savedGroup.id);
      } else {
        // Add key to existing group
        await modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysPost(
          {
            modelProviderId: activeService.key,
            groupId: groupForActiveService.id,
          },
          withModelProviderJsonOptions({
            data: { api_key: apiKey },
            timeout: 3 * 60 * 1000,
          }),
        );
        setKeyList((prev) => [...prev, apiKey]);
      }
      setNewKeyValue("");
      setNewKeyEngineId("");
      void loadExternalServices(normalizedSearchValue);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.external.saveFailed")));
    } finally {
      setAddingKey(false);
    }
  }

  async function handleSaveServiceConfig() {
    if (!activeService || addingKey || savingServiceConfig) {
      return;
    }
    setSavingServiceConfig(true);
    try {
      await form.validateFields();
      const baseUrl = form.getFieldValue([activeService.key, "baseUrl"]) || activeService.baseUrl || "";
      const normalizedBaseUrl = baseUrl.trim();
      let savedGroup = groupForActiveService;

      if (activeService.fields.includes("baseUrl")) {
        if (!savedGroup) {
          savedGroup = await loadFirstGroup(activeService.key);
        }
        if (savedGroup) {
          savedGroup = await updateProviderGroup(activeService, savedGroup, normalizedBaseUrl);
        } else {
          savedGroup = await createProviderGroup(activeService, {
            name: activeService.name,
            base_url: normalizedBaseUrl,
            verify: true,
          });
        }
        setGroupForActiveService(savedGroup);
        originalBaseUrlRef.current = normalizedBaseUrl;
      }

      if (savedGroup && (keyList.length > 0 || isCustomServiceBaseUrl(activeService, normalizedBaseUrl))) {
        await selectServiceProvider(activeService, savedGroup.id);
      }

      message.success(t("modelProvider.external.configSaved", { name: activeService.name }));
      void loadExternalServices(normalizedSearchValue);
      closeConfigModal();
    } catch (error) {
      if (isFormValidationError(error)) {
        return;
      }
      message.error(getLocalizedErrorMessage(error, t("modelProvider.external.saveFailed")));
    } finally {
      setSavingServiceConfig(false);
    }
  }

  async function handleRemoveKey(targetKey: string) {
    if (!activeService || !groupForActiveService) {
      return;
    }
    try {
      await modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete(
        {
          modelProviderId: activeService.key,
          groupId: groupForActiveService.id,
        },
        withModelProviderJsonOptions({ data: { api_key: targetKey } }),
      );
      setKeyList((prev) => prev.filter((k) => k !== targetKey));
      void loadExternalServices(normalizedSearchValue);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.external.saveFailed")));
    }
  }

  const closeConfigModal = () => {
    if (addingKey) {
      return;
    }
    setActiveService(null);
    setKeyList([]);
    setNewKeyValue("");
    setNewKeyEngineId("");
    setVisibleKeys(new Set());
    setGroupForActiveService(null);
  };

  const openConfigModal = (service: ExternalServiceConfig) => {
    setActiveService(service);
    setKeyList([]);
    setNewKeyValue("");
    setNewKeyEngineId("");
    setVisibleKeys(new Set());
    setGroupForActiveService(null);
    void loadGroupKeys(service.key);
    if (service.fields.includes("baseUrl")) {
      const fallbackBaseUrl = service.baseUrl || service.baseUrlPresets?.[0]?.value || "";
      const currentFormValue = form.getFieldValue([service.key, "baseUrl"]);
      originalBaseUrlRef.current = currentFormValue || fallbackBaseUrl;
      window.setTimeout(() => {
        const currentBaseUrl = form.getFieldValue([service.key, "baseUrl"]);
        if (!currentBaseUrl) {
          if (fallbackBaseUrl) {
            form.setFieldValue([service.key, "baseUrl"], fallbackBaseUrl);
          }
        }
      }, 0);
    }

    void listProviderGroups(service.key)
      .then((groupData) => {
        const existingGroup = (groupData.groups || [])[0];
        const nextBaseUrl = existingGroup?.base_url?.trim() || service.baseUrl || "";
        form.setFieldValue([service.key, "baseUrl"], nextBaseUrl);
      })
      .catch(() => {
        form.setFieldValue([service.key, "baseUrl"], service.baseUrl || "");
      });
  };

  const categorizedServices = useMemo(() => {
    const byCategory: Record<ServiceCategoryKey, ExternalServiceConfig[]> = {
      parsing: [],
      search: [],
      academic: [],
    };
    services.forEach((service) => {
      byCategory[service.category].push(service);
    });
    return byCategory;
  }, [services]);

  const filteredCategorizedServices = useMemo(() => {
    return {
      parsing: categorizedServices.parsing.filter((service) =>
        serviceMatchesKeyword(service, categorySearchValues.parsing),
      ),
      search: categorizedServices.search.filter((service) =>
        serviceMatchesKeyword(service, categorySearchValues.search),
      ),
      academic: categorizedServices.academic,
    };
  }, [categorizedServices, categorySearchValues]);
  const activeServiceDisplayStatus =
    activeService?.status === "tbd"
      ? "tbd"
      : activeService?.fields.includes("apiKey")
        ? keyList.length > 0
          ? "configured"
          : "missing"
        : activeService?.status;

  const renderCategorySearch = (categoryKey: ServiceCategoryKey) => {
    if (categoryKey !== "parsing" && categoryKey !== "search") {
      return null;
    }

    return (
      <Input
        allowClear
        className="model-provider-category-search"
        prefix={<SearchOutlined />}
        placeholder={
          categoryKey === "parsing"
            ? t("modelProvider.external.parsingSearchPlaceholder")
            : t("modelProvider.external.searchEngineSearchPlaceholder")
        }
        value={categorySearchValues[categoryKey]}
        onChange={(event) =>
          setCategorySearchValues((previous) => ({
            ...previous,
            [categoryKey]: event.target.value,
          }))
        }
      />
    );
  };

  const renderServiceCategory = (categoryKey: ServiceCategoryKey) => {
    const category = serviceCategories.find((item) => item.key === categoryKey);
    if (!category) {
      return null;
    }

    const categoryServices = categorizedServices[categoryKey];
    const visibleServices = filteredCategorizedServices[categoryKey];
    if (!categoryServices.length) {
      return null;
    }

    return (
      <section className="model-provider-service-category" key={category.key}>
        <div className="model-provider-service-category-top">
          <div className="model-provider-service-category-head">
            <span>{category.icon}</span>
            <div>
              <h3>{t(category.titleKey)}</h3>
              <p>{t(category.descKey)}</p>
            </div>
          </div>
          {renderCategorySearch(category.key)}
        </div>

        {visibleServices.length ? (
          <div className="model-provider-service-grid">
            {visibleServices.map((service) => (
              <button
                aria-label={t("modelProvider.external.configModalTitle", { name: service.name })}
                className="model-provider-service-card"
                key={service.key}
                onClick={() => openConfigModal(service)}
                type="button"
              >
                <ExternalServiceLogo service={service} />
                <div className="model-provider-service-card-copy">
                  <div>
                    <div className="model-provider-service-title-row">
                      <h4>{service.name}</h4>
                      <Tag
                        className="model-provider-service-status"
                        color={
                          service.status === "configured"
                            ? "success"
                            : service.status === "tbd"
                              ? "warning"
                              : "default"
                        }
                      >
                        {t(`modelProvider.external.status.${service.status}`)}
                      </Tag>
                    </div>
                    <Tooltip placement="topLeft" title={service.summary}>
                      <span className="model-provider-service-summary-wrap">
                        <p className="model-provider-service-summary">{service.summary}</p>
                      </span>
                    </Tooltip>
                  </div>
                </div>
                <span className="model-provider-service-card-arrow" aria-hidden="true">
                  <RightOutlined />
                </span>
              </button>
            ))}
          </div>
        ) : (
          <div className="model-provider-category-empty">
            <Empty
              description={t("modelProvider.external.noMatchedServices")}
              image={Empty.PRESENTED_IMAGE_SIMPLE}
            />
          </div>
        )}
      </section>
    );
  };

  return (
    <div className="model-provider-service-page">
      <Spin spinning={loading}>
        <div className="model-provider-service-stack">
          {loadError ? (
            <Alert
              action={
                <Button size="small" type="primary" onClick={() => loadExternalServices(normalizedSearchValue)}>
                  {t("common.retry")}
                </Button>
              }
              message={loadError}
              showIcon
              type="error"
            />
          ) : null}

          {!loading && !loadError && services.length === 0 && normalizedSearchValue ? (
            <div className="model-provider-empty-state" role="status">
              <Empty
                description={normalizedSearchValue ? t("modelProvider.external.noMatchedServices") : t("common.noData")}
                image={Empty.PRESENTED_IMAGE_SIMPLE}
              />
            </div>
          ) : null}

          {section === "tools" ? (
            <div className="model-provider-tools-substack">
              {renderServiceCategory("search")}
              {renderServiceCategory("academic")}
              <ToolManagementSection view="builtin" />
              <ToolManagementSection view="mcp" />
            </div>
          ) : (
            renderServiceCategory("parsing")
          )}
        </div>
      </Spin>

      <Modal
        className="model-provider-service-config-modal"
        destroyOnClose
        onCancel={closeConfigModal}
        open={!!activeService}
        width={600}
        title={
          activeService
            ? t("modelProvider.external.configModalTitle", { name: activeService.name })
            : t("modelProvider.external.configureAction")
        }
        footer={[
          <Button
            disabled={addingKey}
            key="save"
            loading={savingServiceConfig}
            onClick={handleSaveServiceConfig}
            type="primary"
          >
            {t("modelProvider.external.saveConfig")}
          </Button>,
        ]}
      >
        {activeService && (
          <>
            <div className="model-provider-service-config-identity">
              <ExternalServiceLogo service={activeService} />
              <div>
                <div className="model-provider-service-title-row">
                  <h4>{activeService.name}</h4>
                  <Tag
                    color={
                      activeServiceDisplayStatus === "configured"
                        ? "success"
                        : activeServiceDisplayStatus === "tbd"
                          ? "warning"
                          : "default"
                    }
                  >
                    {t(`modelProvider.external.status.${activeServiceDisplayStatus}`)}
                  </Tag>
                </div>
                <p>{activeService.description}</p>
              </div>
            </div>
            <Form form={form} layout="vertical">
              {activeService.fields.includes("baseUrl") ? (
                <Form.Item
                  extra={
                    normalizeProviderName(activeService.name) === "mineru"
                      ? t("modelProvider.external.mineruBaseUrlPresetExtra")
                      : undefined
                  }
                  label="Base URL"
                  name={[activeService.key, "baseUrl"]}
                  normalize={(value: string | undefined) => value?.trim()}
                  rules={[
                    { required: true, message: t("modelProvider.validation.baseUrlRequired") },
                    {
                      validator: (_, value?: string) =>
                        validateHttpBaseUrl(value)
                          ? Promise.resolve()
                          : Promise.reject(new Error(t("modelProvider.validation.baseUrlInvalid"))),
                    },
                    { max: 512, message: t("modelProvider.validation.baseUrlMax") },
                  ]}
                >
                  {activeService.baseUrlPresets?.length ? (
                    <AutoComplete
                      allowClear
                      filterOption={false}
                      onBlur={() => handleBaseUrlChange()}
                      options={activeService.baseUrlPresets.map((preset) => ({
                        value: preset.value,
                        label: (
                          <span className="model-provider-service-preset-option">
                            <strong>{preset.labelKey ? t(preset.labelKey) : preset.value}</strong>
                            <small>{preset.value}</small>
                            {preset.descKey ? <small>{t(preset.descKey)}</small> : null}
                          </span>
                        ),
                      }))}
                      placeholder="https://api.example.com"
                      popupClassName="model-provider-service-preset-dropdown"
                      onChange={(value) => form.setFieldValue([activeService.key, "baseUrl"], value)}
                    />
                  ) : (
                    <Input maxLength={512} onBlur={() => handleBaseUrlChange()} placeholder="https://api.example.com" />
                  )}
                </Form.Item>
              ) : null}
            </Form>

            <div className="model-provider-key-list">
              <div className="model-provider-key-list-label">API Keys</div>
              {keyList.length === 0 ? (
                <div className="model-provider-key-empty">
                  {t("modelProvider.external.noKeysConfigured")}
                </div>
              ) : (
                keyList.map((key, idx) => (
                  <div className="model-provider-key-item" key={key}>
                    <span className="model-provider-key-value" title={visibleKeys.has(idx) ? key : maskAPIKey(key)}>
                      {visibleKeys.has(idx) ? key : maskAPIKey(key)}
                    </span>
                    <div className="model-provider-key-actions">
                      <Tooltip title={t("common.copy")}>
                        <Button
                          size="small"
                          type="text"
                          icon={<CopyOutlined />}
                          onClick={() => copyKeyToClipboard(key)}
                        />
                      </Tooltip>
                      <Tooltip title={visibleKeys.has(idx) ? t("common.hide") : t("common.show")}>
                        <Button
                          size="small"
                          type="text"
                          icon={visibleKeys.has(idx) ? <EyeInvisibleOutlined /> : <EyeOutlined />}
                          onClick={() => toggleKeyVisibility(idx)}
                        />
                      </Tooltip>
                      <Tag color="success">{t("modelProvider.external.keyVerified")}</Tag>
                      <Button
                        size="small"
                        type="text"
                        danger
                        icon={<DeleteOutlined />}
                        onClick={() => handleRemoveKey(key)}
                      />
                    </div>
                  </div>
                ))
              )}
              <div className="model-provider-key-add">
                <Space direction="vertical" size={10} style={{ width: "100%" }}>
                  {isGoogleCustomSearch(activeService) ? (
                    <Space className="model-provider-key-input-row">
                      <Input.Password
                        autoComplete="new-password"
                        maxLength={512}
                        placeholder={t("modelProvider.external.keyPlaceholder")}
                        value={newKeyValue}
                        onChange={(e) => setNewKeyValue(e.target.value)}
                        visibilityToggle={false}
                      />
                      <Input
                        autoComplete="off"
                        maxLength={512}
                        placeholder={t("modelProvider.external.googleSearchEngineIdPlaceholder")}
                        value={newKeyEngineId}
                        onChange={(e) => setNewKeyEngineId(e.target.value)}
                      />
                    </Space>
                  ) : (
                    <div className="model-provider-key-input-row">
                      <Input.Password
                        autoComplete="new-password"
                        maxLength={512}
                        placeholder={t("modelProvider.external.keyPlaceholder")}
                        value={newKeyValue}
                        onChange={(e) => setNewKeyValue(e.target.value)}
                        visibilityToggle={false}
                      />
                    </div>
                  )}
                  <div className="model-provider-key-extra">{t("modelProvider.external.keyExtra")}</div>
                  <Button
                    className="model-provider-key-add-button"
                    icon={<PlusOutlined />}
                    loading={addingKey}
                    onClick={handleAddKey}
                    type="primary"
                  >
                    {t("modelProvider.external.verifyAndAddKey")}
                  </Button>
                </Space>
              </div>
            </div>
          </>
        )}
      </Modal>

      <section className="model-provider-service-alert" aria-label={t("modelProvider.external.apiContractTitle")}>
        <div className="model-provider-service-alert-icon" aria-hidden="true">
          <InfoCircleFilled />
        </div>
        <div className="model-provider-service-alert-copy">
          <h3>{t("modelProvider.external.apiContractTitle")}</h3>
          <p>{t("modelProvider.external.apiContractDesc")}</p>
        </div>
      </section>
    </div>
  );
}
