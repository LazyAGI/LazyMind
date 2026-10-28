import { useSettingsDraft, useSettingsEditor } from "@/modules/settings/SettingsNavigationGuard";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Alert, AutoComplete, Button, Empty, Form, Input, Modal, Space, Spin, Tag, Tooltip, message } from "antd";
import {
  CloudServerOutlined,
  CompassOutlined,
  DeleteOutlined,
  DownOutlined,
  FilePdfOutlined,
  GoogleOutlined,
  InfoCircleFilled,
  PlusOutlined,
  ReadOutlined,
  RightOutlined,
  ScanOutlined,
  SearchOutlined,
  TranslationOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import {
  getLocalizedErrorMessage,
  localizeErrorCode,
} from "@/components/request";
import {
  modelProvidersApi,
  modelProvidersDefaultApi,
  unwrapModelProviderData,
  withModelProviderJsonOptions,
  type StoredProviderKey,
} from "../api";
import ToolManagementSection from "../components/ToolManagementSection";
import DependencyInstallSection from "../components/DependencyInstallSection";
import {
  DEVELOPER_ACTIVE_EVENT,
  isDeveloperModeActive,
} from "@/utils/developerMode";

type ServiceCategoryKey = "parsing" | "search" | "academic" | "translation";
type ServiceProviderCategory = "ocr" | "search" | "datasource" | "translation";
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
  capabilities?: string[];
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
  has_api_key?: boolean;
  keys?: StoredProviderKey[];
  base_url?: string;
  id: string;
  is_verified?: boolean;
  name?: string;
  user_model_provider_id?: string;
}

interface SelectedExternalProvider {
  category: string;
  user_model_provider_id: string;
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
  {
    key: "translation",
    titleKey: "modelProvider.external.translationCategoryTitle",
    descKey: "modelProvider.external.translationCategoryDesc",
    icon: <TranslationOutlined />,
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
    logoUrl: "https://mineru.net/favicon-96x96.png",
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

/** Display order for search engines: Tavily → Bocha → Bing → Google Custom Search */
const SEARCH_ENGINE_DISPLAY_ORDER: Array<{ match: RegExp; order: number }> = [
  { match: /^tavily/, order: 0 },
  { match: /^bocha/, order: 1 },
  { match: /^bing/, order: 2 },
  { match: /^google/, order: 3 },
];

function getSearchEngineDisplayOrder(name: string) {
  const normalizedName = normalizeProviderName(name);
  const matched = SEARCH_ENGINE_DISPLAY_ORDER.find(({ match }) => match.test(normalizedName));
  return matched?.order ?? Number.MAX_SAFE_INTEGER;
}

const serviceToneByCategory: Record<ServiceCategoryKey, ServiceTone> = {
  parsing: "blue",
  search: "green",
  academic: "violet",
  translation: "cyan",
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

function isGoogleCustomSearch(service?: ExternalServiceConfig | null) {
  return normalizeProviderName(service?.name || "") === "googlecustomsearch";
}

function isTencentTranslation(service?: ExternalServiceConfig | null) {
  return normalizeProviderName(service?.name || "") === "tencenttranslation";
}

const translationCredentialPairs: Record<string, { first: string; second: string; secondRequired?: boolean }> = {
  tencenttranslation: { first: "tencentSecretIdPlaceholder", second: "tencentSecretKeyPlaceholder" },
  baidutranslation: { first: "baiduAppIdPlaceholder", second: "baiduSecretPlaceholder" },
  aliyuntranslation: { first: "aliyunAccessKeyIdPlaceholder", second: "aliyunAccessKeySecretPlaceholder" },
  volcenginetranslation: { first: "volcengineAccessKeyIdPlaceholder", second: "volcengineSecretAccessKeyPlaceholder" },
  youdaotranslation: { first: "youdaoAppKeyPlaceholder", second: "youdaoAppSecretPlaceholder" },
  microsofttranslator: { first: "microsoftKeyPlaceholder", second: "microsoftRegionPlaceholder", secondRequired: false },
};

// These links mirror the provider catalog's official setup and API references.
const translationSetupUrls: Record<string, string> = {
  baidutranslation: "https://fanyi-api.baidu.com/",
  aliyuntranslation: "https://help.aliyun.com/zh/machine-translation/developer-reference/api-reference-machine-translation-universal-version-call-guide",
  volcenginetranslation: "https://www.volcengine.com/docs/4640/65067",
  microsofttranslator: "https://learn.microsoft.com/azure/ai-services/translator/text-translation/quickstart/rest-api",
  googlecloudtranslation: "https://docs.cloud.google.com/translate/docs/basic/translating-text",
  niutrans: "https://niutrans.com/doc-api",
  youdaotranslation: "https://ai.youdao.com/DOCSIRMA/html/trans/api/wbfy/index.html",
  deepl: "https://developers.deepl.com/docs/getting-started/auth",
  caiyuntranslation: "https://platform.caiyunapp.com/",
  tencenttokenhubtranslation: "https://cloud.tencent.com/document/product/1823/130079",
  tencenttranslation: "https://console.cloud.tencent.com/cam/capi",
};

function getTranslationSetupUrl(service: ExternalServiceConfig) {
  const knownUrl = translationSetupUrls[normalizeProviderName(service.name)];
  if (knownUrl) return knownUrl;
  return service.description.match(/https:\/\/[^\s，。；、）)]+/)?.[0];
}

function isKeylessTranslation(service?: ExternalServiceConfig | null) {
  return service?.category === "translation" && service.capabilities?.includes("keyless_translation") === true;
}

function translationCredentialPair(service?: ExternalServiceConfig | null) {
  return translationCredentialPairs[normalizeProviderName(service?.name || "")];
}

function hasVisibleSecondCredentialField(service?: ExternalServiceConfig | null) {
  return isGoogleCustomSearch(service) || normalizeProviderName(service?.name || "") === "microsofttranslator";
}

function getServiceProviderCategory(service: ExternalServiceConfig): ServiceProviderCategory {
  if (service.category === "parsing") {
    return "ocr";
  }
  if (service.category === "academic") {
    return "datasource";
  }
  if (service.category === "translation") {
    return "translation";
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
  if (normalizedCategory === "translation") {
    return "translation";
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
  if (category === "translation") {
    return <TranslationOutlined />;
  }
  return category === "academic" ? <ReadOutlined /> : <SearchOutlined />;
}

function getServiceFields(provider: ApiExternalProvider, category: ServiceCategoryKey): Array<keyof ExternalServiceFormValues> {
  if (category === "translation") {
    if (provider.capabilities?.includes("keyless_translation")) return [];
    return translationCredentialPairs[normalizeProviderName(provider.name)] ? ["apiKey", "searchEngineId"] : ["apiKey"];
  }
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

export function renderExternalServiceDescription(description: string) {
  const parts = description.split(/(https?:\/\/[^\s，。；、）)]+)/g);

  return parts.map((part, index) => {
    if (/^https?:\/\//.test(part)) {
      return (
        <a
          href={part}
          key={`${part}-${index}`}
          rel="noreferrer"
          target="_blank"
        >
          {part}
        </a>
      );
    }

    return <span key={`${part}-${index}`}>{part}</span>;
  });
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
    capabilities: provider.capabilities,
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

async function replaceProviderCredential(
  service: ExternalServiceConfig,
  group: ApiExternalGroup,
  apiKey: string,
) {
  const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdPatch({
    modelProviderId: service.key,
    groupId: group.id,
    updateModelProviderGroupOpenAPIRequest: {
      name: group.name || service.name,
      base_url: group.base_url || service.baseUrl || "",
      api_key: apiKey,
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

interface ExternalServiceCardProps {
  ariaLabel: string;
  onOpen: (service: ExternalServiceConfig) => void;
  service: ExternalServiceConfig;
  selected?: boolean;
  selectedLabel?: string;
  statusLabel: string;
}

export function ExternalServiceCard({
  ariaLabel,
  onOpen,
  service,
  selected = false,
  selectedLabel,
  statusLabel,
}: ExternalServiceCardProps) {
  const summaryRef = useRef<HTMLParagraphElement>(null);
  const [summaryOverflowing, setSummaryOverflowing] = useState(false);
  const overflowAware = service.category === "parsing";

  useLayoutEffect(() => {
    if (!overflowAware) return;
    const summary = summaryRef.current;
    if (!summary) return;

    const measure = () => {
      setSummaryOverflowing(
        summary.scrollHeight > summary.clientHeight + 1
        || summary.scrollWidth > summary.clientWidth + 1,
      );
    };
    measure();
    const deferredMeasure = window.setTimeout(measure, 0);
    window.addEventListener("resize", measure);

    const observer = typeof ResizeObserver === "undefined"
      ? undefined
      : new ResizeObserver(measure);
    observer?.observe(summary);
    return () => {
      window.clearTimeout(deferredMeasure);
      window.removeEventListener("resize", measure);
      observer?.disconnect();
    };
  }, [overflowAware, service.summary]);

  const summary = (
    <span className="model-provider-service-summary-wrap">
      <p
        className="model-provider-service-summary"
        ref={overflowAware ? summaryRef : undefined}
      >
        {service.summary}
      </p>
    </span>
  );
  const card = (
    <button
      aria-label={ariaLabel}
      className="model-provider-service-card"
      onClick={() => onOpen(service)}
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
              {statusLabel}
            </Tag>
            {selected && selectedLabel ? <Tag color="processing">{selectedLabel}</Tag> : null}
          </div>
          {overflowAware ? (
            summary
          ) : (
            <Tooltip placement="topLeft" title={service.summary}>
              {summary}
            </Tooltip>
          )}
        </div>
      </div>
      <span className="model-provider-service-card-arrow" aria-hidden="true">
        <RightOutlined />
      </span>
    </button>
  );

  if (!overflowAware) return card;
  return (
    <Tooltip
      classNames={{ root: "model-provider-tool-popover" }}
      placement="bottomLeft"
      trigger={["hover", "focus"]}
      title={
        summaryOverflowing
          ? <div className="model-provider-tool-popover-content">{service.summary}</div>
          : undefined
      }
    >
      {card}
    </Tooltip>
  );
}

interface ExternalServicesPageProps {
  includeMcp?: boolean;
  includeBuiltinTools?: boolean;
  includeDependencies?: boolean;
  visibleCategories?: ServiceCategoryKey[];
  variant?: "default-models";
  focusCategory?: "parsing" | "search" | "academic" | "translation";
  parsingControl?: ReactNode;
  onConfigurationChanged?: () => void;
}

export default function ExternalServicesPage({
  includeMcp = true,
  includeBuiltinTools = true,
  includeDependencies = true,
  visibleCategories,
  variant,
  focusCategory,
  parsingControl,
  onConfigurationChanged,
}: ExternalServicesPageProps = {}) {
  const { t, i18n } = useTranslation();
  const currentLanguage = i18n.resolvedLanguage || i18n.language || "zh-CN";
  const [form] = Form.useForm<Record<string, ExternalServiceFormValues>>();
  const configLocation = useSettingsEditor("service");
  const [activeService, setActiveService] = useState<ExternalServiceConfig | null>(null);
  const [services, setServices] = useState<ExternalServiceConfig[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [developerActive, setDeveloperActive] = useState(isDeveloperModeActive);
  const requestIdRef = useRef(0);
  const normalizedSearchValue = "";
  const [categorySearchValues, setCategorySearchValues] = useState<Record<"parsing" | "search", string>>({
    parsing: "",
    search: "",
  });
  const [expandedCategories, setExpandedCategories] = useState<ServiceCategoryKey[]>([]);

  useEffect(() => {
    if (variant !== "default-models" || !focusCategory) return;
    setExpandedCategories((current) => current.includes(focusCategory) ? current : [...current, focusCategory]);
  }, [focusCategory, variant]);

  // Multi-key state
  const [keyList, setKeyList] = useState<StoredProviderKey[]>([]);
  const [newKeyValue, setNewKeyValue] = useState("");
  const [newKeyEngineId, setNewKeyEngineId] = useState("");
  const [addingKey, setAddingKey] = useState(false);
  const [savingServiceConfig, setSavingServiceConfig] = useState(false);
  const [keysLoaded, setKeysLoaded] = useState(false);
  const [keysError, setKeysError] = useState(false);
  const [groupForActiveService, setGroupForActiveService] = useState<ApiExternalGroup | null>(null);
  const [selectedTranslationProviderId, setSelectedTranslationProviderId] = useState<string | null>(null);
  const originalBaseUrlRef = useRef("");
  const loadGroupKeysGenRef = useRef(0);
  const selectedTranslationLoadGenRef = useRef(0);
  const formValues = Form.useWatch([], form);
  const confirmConfigClose = useSettingsDraft({
    dirty: Boolean(activeService) && (Boolean(newKeyValue || newKeyEngineId) || (keysLoaded &&
      normalizeBaseUrlForCompare(formValues?.[activeService!.key]?.baseUrl || "") !== normalizeBaseUrlForCompare(originalBaseUrlRef.current))),
    saving: addingKey || savingServiceConfig,
    discard: () => { setNewKeyValue(""); setNewKeyEngineId(""); },
  });

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
        setLoadError(getLocalizedErrorMessage(error));
      })
      .finally(() => {
        if (requestIdRef.current === requestId) {
          setLoading(false);
        }
      });

    return () => controller.abort();
  }, [currentLanguage, t]);

  useEffect(() => loadExternalServices(normalizedSearchValue), [loadExternalServices, normalizedSearchValue]);

  useEffect(() => {
    const generation = ++selectedTranslationLoadGenRef.current;
    void modelProvidersApi.apiCoreModelProvidersSelectedProvidersGet()
      .then((response) => {
        if (selectedTranslationLoadGenRef.current !== generation) return;
        const selections = unwrapModelProviderData<{ selections?: SelectedExternalProvider[] }>(response.data).selections || [];
        setSelectedTranslationProviderId(selections.find((item) => item.category === "translation")?.user_model_provider_id || null);
      })
      .catch(() => {
        if (selectedTranslationLoadGenRef.current === generation) setSelectedTranslationProviderId(null);
      });
    return () => { selectedTranslationLoadGenRef.current += 1; };
  }, []);

  async function selectTranslationService(service: ExternalServiceConfig, groupId: string) {
    await selectServiceProvider(service, groupId);
    selectedTranslationLoadGenRef.current += 1;
    setSelectedTranslationProviderId(service.key);
  }

  useEffect(() => {
    const syncDeveloperActive = () => {
      setDeveloperActive(isDeveloperModeActive());
    };
    const handleDeveloperActiveChange = (event: Event) => {
      const nextActive = (event as CustomEvent<{ active?: boolean }>).detail
        ?.active;
      setDeveloperActive(
        typeof nextActive === "boolean" ? nextActive : isDeveloperModeActive(),
      );
    };

    window.addEventListener("storage", syncDeveloperActive);
    window.addEventListener(
      DEVELOPER_ACTIVE_EVENT,
      handleDeveloperActiveChange,
    );
    return () => {
      window.removeEventListener("storage", syncDeveloperActive);
      window.removeEventListener(
        DEVELOPER_ACTIVE_EVENT,
        handleDeveloperActiveChange,
      );
    };
  }, []);

  async function loadGroupKeys(serviceKey: string, initializeForm = false) {
    const gen = loadGroupKeysGenRef.current;
    setKeysError(false);
    try {
      const groupData = await listProviderGroups(serviceKey);
      if (loadGroupKeysGenRef.current !== gen) return;
      const group = (groupData.groups || [])[0] || null;
      setGroupForActiveService(group);
      setKeysLoaded(true);
      if (group) {
        setKeyList(group.keys || []);
        // When the group has a custom base_url, use it as the initial form value.
        // This ensures the user's previously-saved base_url is shown after page refresh,
        // not the catalog default from user_model_providers.base_url.
        if (group.base_url && initializeForm) {
          form.setFieldValue([serviceKey, "baseUrl"], group.base_url);
          originalBaseUrlRef.current = group.base_url;
        }
      } else {
        setKeyList([]);
      }
    } catch {
      if (loadGroupKeysGenRef.current !== gen) return;
      setKeysError(true);
    }
  }

  async function loadFirstGroup(serviceKey: string) {
    const groupData = await listProviderGroups(serviceKey);
    return (groupData.groups || [])[0] || null;
  }

  async function handleAddKey() {
    if (!activeService || !keysLoaded || keysError || isKeylessTranslation(activeService)) {
      return;
    }
    const rawKey = newKeyValue.trim();
    if (!rawKey) {
      return;
    }
    const engineId = newKeyEngineId.trim();
    const isGoogle = isGoogleCustomSearch(activeService);
    const pair = translationCredentialPair(activeService);
    if ((isGoogle || (pair && pair.secondRequired !== false)) && !engineId) {
      return;
    }
    const apiKey = (isGoogle || pair) && engineId ? `${rawKey}|${engineId}` : rawKey;
    const isTranslation = activeService.category === "translation";

    setAddingKey(true);
    try {
      if (!groupForActiveService) {
        // Create group with first key
        const baseUrl = form.getFieldValue([activeService.key, "baseUrl"]) || activeService.baseUrl || "";
        const payload = {
          name: activeService.name,
          base_url: baseUrl,
          api_key: apiKey,
          verify: !isTranslation,
        };
        const savedGroup = await createProviderGroup(activeService, payload);
        if (savedGroup.check && savedGroup.check.success !== true) {
          message.error(localizeErrorCode("2000509"));
          return;
        }
        setGroupForActiveService(savedGroup);

        // Select the provider
        if (isTranslation) await selectTranslationService(activeService, savedGroup.id);
        else await selectServiceProvider(activeService, savedGroup.id);
      } else if (isTranslation) {
        const savedGroup = await replaceProviderCredential(activeService, groupForActiveService, apiKey);
        setGroupForActiveService(savedGroup);
        await selectTranslationService(activeService, savedGroup.id);
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
      }
      await loadGroupKeys(activeService.key);
      setNewKeyValue("");
      setNewKeyEngineId("");
      void loadExternalServices(normalizedSearchValue);
      onConfigurationChanged?.();
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setAddingKey(false);
    }
  }

  async function handleEnableKeylessTranslation() {
    if (!activeService || !isKeylessTranslation(activeService) || !keysLoaded || keysError || addingKey) return;
    setAddingKey(true);
    try {
      const group = groupForActiveService || await createProviderGroup(activeService, {
        name: activeService.name,
        base_url: activeService.baseUrl || "",
        api_key: "",
        verify: false,
      });
      await selectTranslationService(activeService, group.id);
      setGroupForActiveService(group);
      setActiveService((current) => current?.key === activeService.key
        ? { ...current, status: "configured" }
        : current);
      void loadExternalServices(normalizedSearchValue);
      onConfigurationChanged?.();
      message.success(t("modelProvider.external.configSaved", { name: activeService.name }));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setAddingKey(false);
    }
  }

  async function handleSelectExistingTranslation() {
    if (!activeService || activeService.category !== "translation" || isKeylessTranslation(activeService)
      || !keysLoaded || keysError || addingKey || !groupForActiveService
      || !(groupForActiveService.has_api_key || keyList.length > 0)) return;
    setAddingKey(true);
    try {
      await selectTranslationService(activeService, groupForActiveService.id);
      onConfigurationChanged?.();
      message.success(t("modelProvider.external.configSaved", { name: activeService.name }));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setAddingKey(false);
    }
  }

  async function handleSaveServiceConfig() {
    if (!activeService || !keysLoaded || keysError || addingKey || savingServiceConfig) {
      return;
    }
    if (newKeyValue || newKeyEngineId) {
      message.warning(t("modelProvider.external.addKeyBeforeSaving"));
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
          // Address updates retain the saved pool. The patch response contains
          // verification/address fields, so preserve its masked key metadata.
          savedGroup = { ...savedGroup, ...await updateProviderGroup(activeService, savedGroup, normalizedBaseUrl) };
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

      const hasSavedKeys = savedGroup?.has_api_key || Boolean(savedGroup?.keys?.length) || keyList.length > 0;
      if (savedGroup && (hasSavedKeys || isCustomServiceBaseUrl(activeService, normalizedBaseUrl))) {
        await selectServiceProvider(activeService, savedGroup.id);
      }

      message.success(t("modelProvider.external.configSaved", { name: activeService.name }));
      void loadExternalServices(normalizedSearchValue);
      onConfigurationChanged?.();
      confirmConfigClose.acceptSaved();
      closeConfigModal();
    } catch (error) {
      if (isFormValidationError(error)) {
        return;
      }
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setSavingServiceConfig(false);
    }
  }

  async function handleRemoveKey(targetKey: StoredProviderKey) {
    if (!activeService || !groupForActiveService) {
      return;
    }
    try {
      await modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete(
        {
          modelProviderId: activeService.key,
          groupId: groupForActiveService.id,
        },
        withModelProviderJsonOptions({ data: { key_id: targetKey.id } }),
      );
      await loadGroupKeys(activeService.key);
      void loadExternalServices(normalizedSearchValue);
      onConfigurationChanged?.();
    } catch (error) {
    }
  }

  const closeConfigModal = () => {
    if (addingKey) {
      return;
    }
    configLocation.select();
    loadGroupKeysGenRef.current += 1;
    setActiveService(null);
    setKeyList([]);
    setNewKeyValue("");
    setNewKeyEngineId("");
    setKeysLoaded(false);
    setGroupForActiveService(null);
  };

  const openConfigModal = (service: ExternalServiceConfig) => {
    if (configLocation.managed && configLocation.item !== service.key) {
      configLocation.select(service.key);
      return;
    }
    loadGroupKeysGenRef.current += 1;
    setActiveService(service);
    setKeyList([]);
    setNewKeyValue("");
    setNewKeyEngineId("");
    setKeysLoaded(false);
    setGroupForActiveService(null);
    void loadGroupKeys(service.key, true);
    if (service.fields.includes("baseUrl")) {
      const fallbackBaseUrl = service.baseUrl || service.baseUrlPresets?.[0]?.value || "";
      originalBaseUrlRef.current = fallbackBaseUrl;
      form.setFieldValue([service.key, "baseUrl"], fallbackBaseUrl);
    }

  };

  useEffect(() => {
    if (!configLocation.managed) return;
    if (!configLocation.item) { loadGroupKeysGenRef.current += 1; setActiveService(null); return; }
    if (loading || activeService?.key === configLocation.item) return;
    const service = services.find((item) => item.key === configLocation.item);
    if (service) openConfigModal(service);
  }, [configLocation.item, loading, services]);

  const categorizedServices = useMemo(() => {
    const byCategory: Record<ServiceCategoryKey, ExternalServiceConfig[]> = {
      parsing: [],
      search: [],
      academic: [],
      translation: [],
    };
    services.forEach((service) => {
      byCategory[service.category].push(service);
    });
    byCategory.search.sort((a, b) => {
      const orderA = getSearchEngineDisplayOrder(a.name);
      const orderB = getSearchEngineDisplayOrder(b.name);
      if (orderA !== orderB) {
        return orderA - orderB;
      }
      return a.name.localeCompare(b.name);
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
      translation: categorizedServices.translation,
    };
  }, [categorizedServices, categorySearchValues]);
  const activeServiceDisplayStatus =
    activeService?.status === "tbd"
      ? "tbd"
      : activeService?.fields.includes("apiKey")
        ? groupForActiveService?.has_api_key || keyList.length > 0
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

  const renderServiceCards = (items: ExternalServiceConfig[]) => items.length ? (
    <div className="model-provider-service-grid">
      {items.map((service) => (
        <ExternalServiceCard
          ariaLabel={t("modelProvider.external.configModalTitle", { name: service.name })}
          key={service.key}
          onOpen={openConfigModal}
          service={service}
          selected={service.category === "translation" && selectedTranslationProviderId === service.key}
          selectedLabel={t("modelProvider.external.translationSelected")}
          statusLabel={variant === "default-models" && isKeylessTranslation(service) && service.status !== "configured"
            ? t("modelProvider.external.keylessTranslationStatus")
            : t(`modelProvider.external.status.${service.status}`)}
        />
      ))}
    </div>
  ) : (
    <div className="model-provider-category-empty">
      <Empty
        description={t("modelProvider.external.noMatchedServices")}
        image={Empty.PRESENTED_IMAGE_SIMPLE}
      />
    </div>
  );

  const renderServiceCategory = (categoryKey: ServiceCategoryKey) => {
    if (visibleCategories && !visibleCategories.includes(categoryKey)) {
      return null;
    }
    const category = serviceCategories.find((item) => item.key === categoryKey);
    if (!category) {
      return null;
    }

    const categoryServices = categorizedServices[categoryKey];
    const visibleServices = filteredCategorizedServices[categoryKey];
    if (!categoryServices.length && !(variant === "default-models" && category.key === "parsing" && parsingControl)) {
      return null;
    }

    if (variant === "default-models") {
      const expanded = expandedCategories.includes(category.key);
      const contentId = `default-service-${category.key}-content`;
      const titleId = `default-service-${category.key}-title`;
      const configured = categoryServices.filter((service) => service.status === "configured").length;
      const keylessTranslation = visibleServices.filter(isKeylessTranslation);
      const keyedTranslation = visibleServices.filter((service) => !isKeylessTranslation(service));

      return (
        <section
          aria-labelledby={titleId}
          className={`model-provider-default-service-group${expanded ? " is-expanded" : ""}`}
          data-service-category={category.key}
          id={`default-service-${category.key}`}
          key={category.key}
        >
          <h3 className="model-provider-default-service-heading">
            <button
              aria-controls={contentId}
              aria-expanded={expanded}
              className="model-provider-default-service-toggle"
              onClick={() => setExpandedCategories((current) => expanded
                ? current.filter((key) => key !== category.key)
                : [...current, category.key])}
              type="button"
            >
              <span className="model-provider-default-service-icon" aria-hidden="true">{category.icon}</span>
              <span className="model-provider-default-service-copy">
                <strong id={titleId}>{t(category.titleKey)}</strong>
                <small>{t(category.descKey)}</small>
              </span>
              <span className="model-provider-default-service-count">
                {t("modelProvider.defaults.configuredCount", { configured, total: categoryServices.length })}
              </span>
              <DownOutlined className="model-provider-default-service-caret" aria-hidden="true" />
            </button>
          </h3>
          <div className="model-provider-default-service-content" hidden={!expanded} id={contentId}>
            {category.key === "parsing" && parsingControl ? (
              <div className="model-provider-default-parsing-control">{parsingControl}</div>
            ) : null}
            {renderCategorySearch(category.key)}
            {category.key === "translation" ? (
              <div className="model-provider-translation-tiers">
                {[
                  {
                    key: "keyless",
                    title: t("modelProvider.external.keylessTranslationTitle"),
                    description: t("modelProvider.external.keylessTranslationDesc"),
                    services: keylessTranslation,
                  },
                  {
                    key: "keyed",
                    title: t("modelProvider.external.keyedTranslationTitle"),
                    description: t("modelProvider.external.keyedTranslationDesc"),
                    services: keyedTranslation,
                  },
                ].filter((tier) => tier.services.length > 0).map((tier) => (
                  <section className="model-provider-translation-tier" key={tier.key}>
                    <div className="model-provider-translation-tier-heading">
                      <h4>{tier.title}</h4>
                      <p>{tier.description}</p>
                    </div>
                    {renderServiceCards(tier.services)}
                  </section>
                ))}
              </div>
            ) : category.key === "parsing" && categoryServices.length === 0 ? (
              <Empty description={t("common.noData")} image={Empty.PRESENTED_IMAGE_SIMPLE} />
            ) : renderServiceCards(visibleServices)}
          </div>
        </section>
      );
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

        {renderServiceCards(visibleServices)}
      </section>
    );
  };

  return (
    <div className={`model-provider-service-page${variant === "default-models" ? " is-default-models" : ""}`}>
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

          <div className="model-provider-tools-substack">
            {renderServiceCategory("parsing")}
            {variant !== "default-models" && includeDependencies ? <DependencyInstallSection /> : null}
            {renderServiceCategory("search")}
            {renderServiceCategory("academic")}
            {renderServiceCategory("translation")}
            {variant !== "default-models" && developerActive && includeBuiltinTools ? <ToolManagementSection view="builtin" /> : null}
            {variant !== "default-models" && includeMcp ? <ToolManagementSection view="mcp" /> : null}
          </div>
        </div>
      </Spin>

      <Modal
        className="model-provider-service-config-modal"
        destroyOnClose
        onCancel={() => confirmConfigClose(closeConfigModal)}
        open={!!activeService}
        width={600}
        title={
          activeService
            ? t("modelProvider.external.configModalTitle", { name: activeService.name })
            : t("modelProvider.external.configureAction")
        }
        footer={activeService?.category === "translation" && !isKeylessTranslation(activeService) ? (
          groupForActiveService && (groupForActiveService.has_api_key || keyList.length > 0) ? [
            <Button
              disabled={!keysLoaded || keysError || addingKey || selectedTranslationProviderId === activeService.key}
              key="select-translation"
              loading={addingKey}
              onClick={handleSelectExistingTranslation}
              type="primary"
            >
              {t(selectedTranslationProviderId === activeService.key ? "modelProvider.external.translationSelected" : "modelProvider.external.selectTranslationSource")}
            </Button>,
          ] : null
        ) : [
          <Button
            disabled={addingKey || !keysLoaded || keysError}
            key="save"
            loading={isKeylessTranslation(activeService) ? addingKey : savingServiceConfig}
            onClick={isKeylessTranslation(activeService) ? handleEnableKeylessTranslation : handleSaveServiceConfig}
            type="primary"
          >
            {t(isKeylessTranslation(activeService) ? "modelProvider.external.enableTranslationSource" : "modelProvider.external.saveConfig")}
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
                  {activeService.category === "translation" && selectedTranslationProviderId === activeService.key
                    ? <Tag color="processing">{t("modelProvider.external.translationSelected")}</Tag> : null}
                </div>
                <p>{renderExternalServiceDescription(activeService.description)}</p>
              </div>
            </div>
            {activeService.category === "translation" && !isKeylessTranslation(activeService) ? (
              <section
                aria-label={t("modelProvider.external.translationGuideTitle", { name: activeService.name })}
                className="model-provider-translation-setup-guide"
              >
                <div className="model-provider-translation-setup-head">
                  <strong>{t("modelProvider.external.translationGuideTitle", { name: activeService.name })}</strong>
                  {getTranslationSetupUrl(activeService) ? (
                    <a href={getTranslationSetupUrl(activeService)} rel="noreferrer" target="_blank">
                      {t("modelProvider.external.translationGuideOpenOfficial")}
                      <RightOutlined aria-hidden="true" />
                    </a>
                  ) : null}
                </div>
                <ol>
                  <li>{t(isTencentTranslation(activeService)
                    ? "modelProvider.external.tencentSetupStepEnable"
                    : "modelProvider.external.translationGuideStepOpen")}</li>
                  <li>{t(isTencentTranslation(activeService)
                    ? "modelProvider.external.tencentSetupStepCredential"
                    : "modelProvider.external.translationGuideStepCreate")}</li>
                  <li>{t(isTencentTranslation(activeService)
                    ? "modelProvider.external.tencentSetupStepPaste"
                    : "modelProvider.external.translationGuideStepSave")}</li>
                </ol>
              </section>
            ) : null}
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
                      disabled={!keysLoaded || keysError || savingServiceConfig}
                      allowClear
                      filterOption={false}
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
                    <Input disabled={!keysLoaded || keysError || savingServiceConfig} maxLength={512} placeholder="https://api.example.com" />
                  )}
                </Form.Item>
              ) : null}
            </Form>

            {isKeylessTranslation(activeService) ? (
              <Alert type="info" showIcon message={t("modelProvider.external.keylessTranslationNote")} />
            ) : <div className="model-provider-key-list">
              <div className="model-provider-key-list-label">
                {isTencentTranslation(activeService) ? t("modelProvider.external.tencentCredentials") : t("modelProvider.external.credentials")}
              </div>
              {keysError ? (
                <Alert
                  type="error"
                  message={localizeErrorCode("2000509")}
                  action={<Button onClick={() => void loadGroupKeys(activeService.key, true)}>{t("common.retry")}</Button>}
                />
              ) : !keysLoaded ? <Spin size="small" /> : keyList.length === 0 ? (
                <div className="model-provider-key-empty">
                  {t("modelProvider.external.noKeysConfigured")}
                </div>
              ) : (
                keyList.map((key) => (
                  <div className="model-provider-key-item" key={key.id}>
                    <span className="model-provider-key-value" title={key.masked}>
                      {key.masked}
                    </span>
                    <div className="model-provider-key-actions">
                      <Tag color="success">{t(activeService.category === "translation" ? "modelProvider.external.credentialSaved" : "modelProvider.external.keyVerified")}</Tag>
                      <Button
                        size="small"
                        type="text"
                        danger
                        icon={<DeleteOutlined />}
                        aria-label={t("common.delete")}
                        onClick={() => handleRemoveKey(key)}
                      />
                    </div>
                  </div>
                ))
              )}
              <div className="model-provider-key-add">
                <Space direction="vertical" size={10} style={{ width: "100%" }}>
                  {isGoogleCustomSearch(activeService) || translationCredentialPair(activeService) ? (
                    <Space className="model-provider-key-input-row">
                      <Input.Password
                        autoComplete="new-password"
                        maxLength={512}
                        placeholder={translationCredentialPair(activeService)
                          ? t(`modelProvider.external.${translationCredentialPair(activeService)!.first}`)
                          : t("modelProvider.external.keyPlaceholder")}
                        value={newKeyValue}
                        onChange={(e) => setNewKeyValue(e.target.value)}
                        visibilityToggle={false}
                      />
                      {hasVisibleSecondCredentialField(activeService) ? (
                        <Input
                          autoComplete="off"
                          maxLength={512}
                          placeholder={translationCredentialPair(activeService)
                            ? t(`modelProvider.external.${translationCredentialPair(activeService)!.second}`)
                            : t("modelProvider.external.googleSearchEngineIdPlaceholder")}
                          value={newKeyEngineId}
                          onChange={(e) => setNewKeyEngineId(e.target.value)}
                        />
                      ) : (
                        <Input.Password
                          autoComplete="off"
                          maxLength={512}
                          placeholder={t(`modelProvider.external.${translationCredentialPair(activeService)!.second}`)}
                          value={newKeyEngineId}
                          onChange={(e) => setNewKeyEngineId(e.target.value)}
                          visibilityToggle={false}
                        />
                      )}
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
                  <div className="model-provider-key-extra">{t(activeService.category === "translation" ? "modelProvider.external.translationCredentialExtra" : "modelProvider.external.keyExtra")}</div>
                  <Button
                    className="model-provider-key-add-button"
                    icon={<PlusOutlined />}
                    aria-label={t(activeService.category === "translation" ? "modelProvider.external.saveTranslationCredential" : "modelProvider.external.verifyAndAddKey")}
                    loading={addingKey}
                    disabled={!keysLoaded || keysError}
                    onClick={handleAddKey}
                    type="primary"
                  >
                    {t(activeService.category === "translation" ? "modelProvider.external.saveTranslationCredential" : "modelProvider.external.verifyAndAddKey")}
                  </Button>
                </Space>
              </div>
            </div>}
          </>
        )}
      </Modal>

      {variant !== "default-models" ? <section className="model-provider-service-alert" aria-label={t("modelProvider.external.apiContractTitle")}>
        <div className="model-provider-service-alert-icon" aria-hidden="true">
          <InfoCircleFilled />
        </div>
        <div className="model-provider-service-alert-copy">
          <h3>{t("modelProvider.external.apiContractTitle")}</h3>
          <p>{t("modelProvider.external.apiContractDesc")}</p>
        </div>
      </section> : null}
    </div>
  );
}
