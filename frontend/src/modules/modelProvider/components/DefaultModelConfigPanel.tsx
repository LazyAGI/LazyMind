import { useEffect, useId, useRef, useState, useSyncExternalStore, type ReactElement } from "react";
import { Alert, Button, Select, Skeleton, Switch, Tag, Tooltip } from "antd";
import {
  CheckCircleOutlined,
  AudioOutlined,
  CloudServerOutlined,
  CompassOutlined,
  DownOutlined,
  FilePdfOutlined,
  GoogleOutlined,
  MinusCircleOutlined,
  QuestionCircleOutlined,
  PictureOutlined,
  RightOutlined,
  RocketOutlined,
  ScanOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import { runtimeFeatures } from "@/runtime/features";
import { getProviderDisplayName, getProviderSearchText } from "../providerDisplayNames";
import { useFocusModality } from "../hooks/useFocusModality";
import {
  cloudServiceConfigs,
  normalizeProviderKey,
  useDefaultModelConfig,
  type CloudServiceCategory,
  type CloudServiceSlotKey,
  type ModelCapability,
  type ModelReadyResponse,
  type ProviderOption,
  type UseDefaultModelConfigOptions,
  type VerifiedCloudServiceResponse,
} from "../hooks/useDefaultModelConfig";
import { formatContextWindow } from "../maxInputTokens";
import { probeHealthVersion, readModelProbeHealth, subscribeProbeHealth } from "../modelProbeHealth";
import "./DefaultModelConfigPanel.scss";

export type {
  CloudServiceSlotKey,
  ModelCapability,
  SetupAvailabilityState,
} from "../hooks/useDefaultModelConfig";

const LAZYMIND_CLOUD_PROVIDER_KEY = "lazymind_cloud";

const defaultCapabilityGroups = [
  { key: "visual", icon: PictureOutlined, capabilities: ["vlm", "image_generator", "video_generator"] },
  { key: "retrieval", icon: SearchOutlined, capabilities: ["searchEngine", "cloudParsing", "reranker", "embed_image"] },
  { key: "voice", icon: AudioOutlined, capabilities: ["speech_to_text", "tts"] },
  { key: "evolution", icon: RocketOutlined, capabilities: ["evo_llm"] },
] as const;

function getDefaultCapabilityGroup(target?: ModelCapability) {
  return defaultCapabilityGroups.find((group) =>
    group.capabilities.some((capability) => capability === target),
  )?.key;
}

interface DefaultModelConfigPanelProps extends UseDefaultModelConfigOptions {
  onConfigureCloudService: (service: CloudServiceSlotKey) => void;
  onConfigureProviders: () => void;
  onRetrySetup: () => void;
}

// Catalog keeps the real provider model name; editable models get a localized,
// display-only suffix when shown in the unified image-generation dropdown.
function formatImageEditingDisplayName(name: string, suffix: string) {
  return name.endsWith(suffix)
    ? name
    : `${name}${suffix}`;
}

function formatUnifiedImageDisplayName(
  option: {
    model: { name: string };
    isEditable?: boolean;
  },
  editableSuffix: string,
) {
  return option.isEditable
    ? formatImageEditingDisplayName(option.model.name, editableSuffix)
    : option.model.name;
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

function getModelReadyTooltip(
  t: ReturnType<typeof useTranslation>["t"],
  readyStatus?: ModelReadyResponse,
) {
  if (!readyStatus) {
    return undefined;
  }
  if (!readyStatus.ready) {
    if (readyStatus.source === "cloud" && readyStatus.reason === "cloud_plan_required") {
      return t("modelProvider.cloudSystemPlanRequired");
    }
    if (readyStatus.source === "cloud") {
      return t("modelProvider.cloudSystemUnavailable");
    }
    return t("modelProvider.modelNotReadyTip");
  }
  if (readyStatus.source === "cloud") {
    return t("modelProvider.lazyMindCloudAvailable");
  }
	if (readyStatus.fallback_from === "cloud") {
	  return t("modelProvider.cloudModelLocalFallbackTip");
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
  logoUrl,
  compact = false,
}: {
  provider: ProviderOption;
  logoUrl?: string;
  compact?: boolean;
}) {
  return (
    <span
      aria-hidden="true"
      className={`model-provider-logo is-${provider.isCustom ? "custom" : normalizeProviderKey(provider.source || provider.name)}${compact ? " is-compact" : ""}`}
    >
      <span className="model-provider-logo-fallback">{provider.brand}</span>
      {logoUrl || provider.logoUrl ? (
        <img
          alt=""
          loading="lazy"
          src={logoUrl || provider.logoUrl}
          onLoad={(event) => { event.currentTarget.style.display = ""; }}
          onError={(event) => {
            event.currentTarget.style.display = "none";
          }}
        />
      ) : null}
    </span>
  );
}

export default function DefaultModelConfigPanel({
  cloudServiceSetupStates,
  modelProviderSetupState,
  onConfigureCloudService,
  onConfigureProviders,
  onModelSelectionChanged,
  onRetrySetup,
  highlightTarget,
  onHighlightResolved,
}: DefaultModelConfigPanelProps) {
  const { t, i18n } = useTranslation();
  const focusModality = useFocusModality();
  const language = i18n?.resolvedLanguage || i18n?.language;
  const probeHealth = useSyncExternalStore(subscribeProbeHealth, probeHealthVersion, () => 0);
  const {
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
  } = useDefaultModelConfig({
    cloudServiceSetupStates,
    modelProviderSetupState,
    onModelSelectionChanged,
    highlightTarget,
    onHighlightResolved,
  });
  const [moduleModelSearchKeywords, setModuleModelSearchKeywords] = useState<
    Partial<Record<ModelCapability, string>>
  >({});
  const [cloudServiceSearchKeywords, setCloudServiceSearchKeywords] = useState<
    Partial<Record<CloudServiceSlotKey, string>>
  >({});
  const [collapsedSuppliers, setCollapsedSuppliers] = useState<Partial<Record<ModelCapability, string[]>>>({});
  const [keyboardNavigation, setKeyboardNavigation] = useState(false);
  const groupId = useId();
  const highlightedGroup = getDefaultCapabilityGroup(highlightTarget);
  const [expandedGroups, setExpandedGroups] = useState<string[]>(() =>
    defaultCapabilityGroups.map(({ key }) => key),
  );

  useEffect(() => {
    if (highlightedGroup) {
      setExpandedGroups((current) => current.includes(highlightedGroup)
        ? current : [...current, highlightedGroup]);
    }
  }, [highlightedGroup, highlightTarget]);
  const selectInteractionProps = {
    popupClassName: `model-provider-select-dropdown model-defaults-select-dropdown${keyboardNavigation ? " is-keyboard-navigation" : ""}`,
    onMouseDown: () => setKeyboardNavigation(false),
    onInputKeyDown: () => setKeyboardNavigation(true),
    popupRender: (menu: ReactElement) => (
      <div onMouseMove={() => setKeyboardNavigation(false)} onMouseLeave={() => setKeyboardNavigation(false)}>
        {menu}
      </div>
    ),
  };
  const highlightedRowRef = useRef<HTMLDivElement | null>(null);
  const focusedHighlightRef = useRef<string | null>(null);

  useEffect(() => {
    if (!highlightTarget) focusedHighlightRef.current = null;
    if (
      !highlightTarget ||
      !highlightedRowRef.current ||
      focusedHighlightRef.current === highlightTarget
    ) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      if (!highlightedRowRef.current) return;
      focusedHighlightRef.current = highlightTarget;
      highlightedRowRef.current?.scrollIntoView({
        behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
        block: "center",
      });
      highlightedRowRef.current?.focus({ preventScroll: true });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [highlightTarget, modelProviderSetupState, moduleModelOptions, defaultLoadState, expandedGroups]);

  const modelCards = visibleModuleConfigs.map((module) => {
    const options = (moduleModelOptions[module.key] || []).filter(
			(option) => lazyMindCloudAvailable || option.source !== "cloud",
		  );
    const optionState = moduleModelOptionStates[module.key];
    const optionLoading = optionState === "loading";
    const hasAvailableOptions = options.some((option) =>
      option.model.availability !== "unavailable" &&
      option.model.lifecycle !== "deprecated" && option.model.lifecycle !== "retired",
    );
    const keyword = (moduleModelSearchKeywords[module.key] || "").trim().toLowerCase();
    const visibleOptions = options.filter((option) =>
      !keyword || `${option.model.name} ${getProviderSearchText(option.provider, language)}`.toLowerCase().includes(keyword),
    );
    const supplierGroups = new Map<string, { provider: ProviderOption; options: typeof options }>();
    for (const option of visibleOptions) {
      const key = `${option.source}:${option.provider.id || normalizeProviderKey(option.provider.name)}`;
      const group = supplierGroups.get(key);
      if (group) group.options.push(option);
      else supplierGroups.set(key, { provider: option.provider, options: [option] });
    }
    const renderModelValue = (option: typeof options[number]) => (
      <span className="model-provider-select-value">
        <ProviderLogo provider={option.provider} logoUrl={option.model.logoDataUrl} compact />
        <span className="model-provider-select-value-text">
          {module.key === "image_generator"
            ? formatUnifiedImageDisplayName(option, t("modelProvider.editableModelSuffix"))
            : option.model.name} · {getProviderDisplayName(option.provider, language)}
        </span>
      </span>
    );
    const moduleTitle = t(module.titleKey);
    const moduleSubtitle = t(module.subtitleKey);
    const maxInputTokens = selectedModelMaxInputTokens[module.key];
    const contextWindow = formatContextWindow(maxInputTokens);
    const selectedOption = options.find(
      (option) => option.value === selectedModels[module.key],
    );
    const selectedIsCloud = selectedOption?.source === "cloud";

    const configured = isModelConfigured(module.key);
    const saving = savingCapabilities.has(module.key);
    return { key: module.key, configured, node: (
      <div
        ref={module.key === highlightTarget ? highlightedRowRef : undefined}
        role="group"
        aria-label={moduleTitle}
        className={`model-provider-default-row${!configured ? " is-pending" : ""}${module.restricted && !isAdmin ? " is-restricted" : ""}${module.key === highlightTarget ? " is-config-highlighted" : ""}`}
        key={module.key}
        tabIndex={module.key === highlightTarget ? -1 : undefined}
      >
        <div className="model-provider-default-meta">
          <label
            className="model-provider-default-title"
            htmlFor={`model-provider-${module.key.toLowerCase()}`}
            title={moduleTitle}
          >
            {module.required ? (
              <span className="is-required">*</span>
            ) : null}
            <span className="model-provider-default-title-text">{moduleTitle}</span>
          </label>
          <Tooltip placement="top" title={moduleSubtitle} trigger={["hover", "focus"]}>
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
          {!isAdmin && capabilityReadyStates[module.key] === "ready" ? (
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

        <div className="model-provider-default-controls">
        <div className="model-provider-default-input">
        {!configured && <p className="model-provider-pending-description">{moduleSubtitle}</p>}
        {!isAdmin && (capabilityReadyStates[module.key] === "error" || capabilityReadyStates[module.key] === "loading") ? (
          <Alert type="warning" showIcon message={t("modelProvider.capabilityStatusLoadFailed")}
            action={<Button size="small" aria-label={t("common.retry")} disabled={capabilityReadyStates[module.key] === "loading"}
              loading={capabilityReadyStates[module.key] === "loading"}
              onClick={() => void retryCapabilityReadiness(module.key)}>{t("common.retry")}</Button>} />
        ) : null}
        {modelProviderSetupState === "loading" || (!configured && !hasAvailableOptions && modelProviderSetupState === "ready" && (!optionState || optionLoading)) ? (
          <div role="status" aria-label={t("common.loading")}><Skeleton.Input active block size="small" /></div>
        ) : null}
        {modelProviderSetupState === "error" || (!configured && optionState === "error") ? (
          <Alert type="error" showIcon message={t("modelProvider.providerSetupLoadFailed")}
            action={<Button size="small" onClick={modelProviderSetupState === "error" ? onRetrySetup : () => void loadModuleModels(module.key, true)}>{t("common.retry")}</Button>} />
        ) : null}
        {modelProviderSetupState === "empty" || (!configured && modelProviderSetupState === "ready" && optionState === "ready" && !hasAvailableOptions) ? (
          <Button className="model-provider-configure-capability" type="text" size="small"
            disabled={(module.restricted && !isAdmin) || saving} onClick={onConfigureProviders}>
            {t(module.restricted && !isAdmin
              ? "modelProvider.contactAdminToConfigure"
              : "modelProvider.configureCapability")}
            <RightOutlined aria-hidden="true" />
          </Button>
        ) : null}
        {modelProviderSetupState === "ready" && (configured || hasAvailableOptions) && <Select
          allowClear={!module.required}
          className={`model-provider-model-select${configured && contextWindow ? " has-context-window" : ""}`}
          disabled={(module.restricted && !isAdmin) || saving}
          filterOption={false}
          id={`model-provider-${module.key.toLowerCase()}`}
          listHeight={340}
          virtual={false}
          optionLabelProp="label"
          labelRender={({ value, label }) => {
            const option = options.find((item) => item.value === value);
            return option ? renderModelValue(option) : label;
          }}
          placeholder={
            (modelReadyStatus[module.key]?.model_name && modelReadyStatus[module.key]?.logo_data_url
              ? <span className="model-provider-select-value">
                  <span className="model-provider-logo is-custom is-compact" aria-hidden="true">
                    <img src={modelReadyStatus[module.key]?.logo_data_url} alt="" />
                  </span>
                  <span className="model-provider-select-value-text">
                    {modelReadyStatus[module.key]?.model_name}
                  </span>
                </span>
              : modelReadyStatus[module.key]?.model_name) || (module.restricted && !isAdmin
              ? t("modelProvider.restrictedPlaceholder")
              : module.required
                ? t("modelProvider.requiredModelPlaceholder")
                : t("modelProvider.optionalModelPlaceholder"))
          }
          {...selectInteractionProps}
          showSearch
          suffixIcon={
            <span className="model-provider-select-suffix">
              {configured && contextWindow ? (
                <span className="model-provider-context-badge" title={t("modelProvider.maxInputTokens", { value: maxInputTokens })}>
                  {contextWindow}
                </span>
              ) : null}
              <DownOutlined className="model-provider-select-caret" />
            </span>
          }
          value={selectedModels[module.key]}
          onChange={(value) => handleModelSelection(module.key, value)}
          onSearch={(value) => {
            if (value.trim()) {
              setCollapsedSuppliers((current) => ({ ...current, [module.key]: [] }));
            }
            setModuleModelSearchKeywords((current) => ({
              ...current,
              [module.key]: value,
            }));
          }}
          onDropdownVisibleChange={(open) => {
            if (open) {
              void loadModuleModels(module.key, true);
            } else {
              setKeyboardNavigation(false);
            }
          }}
          loading={optionLoading || saving}
          notFoundContent={
            optionLoading
              ? t("common.loading")
              : <div className="model-provider-options-empty">
                  <span>{t("modelProvider.noModelOptions")}</span>
                  <Button type="link" size="small" onClick={onConfigureProviders}>{t("modelProvider.providerSetupAction")}</Button>
                </div>
          }
        >
          {Array.from(supplierGroups, ([supplierKey, supplier]) => {
            const collapsed = collapsedSuppliers[module.key]?.includes(supplierKey) ?? false;
            const supplierName = getProviderDisplayName(supplier.provider, language);
            return (
              <Select.OptGroup key={supplierKey} label={
                <button type="button" className="model-provider-supplier-group-toggle"
                  aria-expanded={!collapsed}
                  onMouseDown={(event) => event.preventDefault()}
                  onKeyDown={(event) => event.stopPropagation()}
                  onClick={(event) => {
                    event.stopPropagation();
                    setCollapsedSuppliers((current) => {
                      const keys = current[module.key] || [];
                      return { ...current, [module.key]: collapsed
                        ? keys.filter((key) => key !== supplierKey) : [...keys, supplierKey] };
                    });
                  }}>
                  <ProviderLogo provider={supplier.provider} compact />
                  <span className="model-provider-supplier-group-name">{supplierName}</span>
                  <span className="model-provider-supplier-group-count">{supplier.options.length}</span>
                  <DownOutlined className={collapsed ? "is-collapsed" : ""} aria-hidden="true" />
                </button>
              }>
                {(collapsed ? [] : supplier.options).map((option) => {
                  const { provider, model, value } = option;
                  const supplierDisplayName = getProviderDisplayName(provider, language);
                  const displayName =
                    module.key === "image_generator"
                      ? formatUnifiedImageDisplayName(
                          option,
                          t("modelProvider.editableModelSuffix"),
                        )
                      : model.name;
                  return (
                    <Select.Option
                      key={value}
                      title={`${displayName} · ${supplierDisplayName}`}
                      disabled={
                        model.availability === "unavailable" ||
                        model.enabled === false ||
                        model.lifecycle === "deprecated" ||
                        model.lifecycle === "retired" ||
                        (probeHealth >= 0 && readModelProbeHealth(model.id)?.status === "failed")
                      }
                      label={renderModelValue(option)}
                      value={value}
                    >
                      <Tooltip
                        title={`${displayName} · ${supplierDisplayName}`}
                        placement="right"
                        overlayClassName="model-provider-option-tooltip"
                      >
                        <span className="model-provider-select-option is-child" title="">
                          <ProviderLogo provider={provider} logoUrl={model.logoDataUrl} compact />
                          <span className="model-provider-select-copy">
                            <strong>{displayName}</strong>
                            <small>
                              {option.source === "cloud"
                                ? t("modelProvider.cloudSystemReadOnly")
                                : supplierDisplayName}
                            </small>
                          </span>
                          {formatContextWindow(model.maxInputTokens) ? (
                            <span className="model-provider-context-badge">{formatContextWindow(model.maxInputTokens)}</span>
                          ) : null}
                        </span>
                      </Tooltip>
                    </Select.Option>
                  );
                })}
              </Select.OptGroup>
            );
          })}
        </Select>}
        </div>
        {configured && isAdmin && !runtimeFeatures.hideUserGroupSurfaces && !selectedIsCloud ? (
          <Tooltip title={t(shareStatus[module.key] ? "modelProvider.shareOn" : "modelProvider.shareOff")}>
            <span className="model-provider-share-control">
              <span aria-hidden="true">{t(shareStatus[module.key] ? "modelProvider.shared" : "modelProvider.unshared")}</span>
              <Switch
                aria-label={t("modelProvider.shareToggleAria", { title: moduleTitle })}
                checked={!!shareStatus[module.key]}
                disabled={saving}
                className="model-provider-share-switch"
                size="small"
                onChange={(checked) => void toggleShareModel(module.key, checked)}
              />
            </span>
          </Tooltip>
        ) : null}
        </div>
      </div>
    ) };
  });

  const cloudCards = cloudServiceConfigs.map((service) => {
    const serviceTitle = t(service.titleKey);
    const serviceSubtitle = t(service.subtitleKey);
    const setupState = cloudServiceSetupStates[service.key];
    const options = cloudServiceOptions[service.key] || [];
    const optionState = cloudServiceOptionStates[service.key];
    const optionLoading = optionState === "loading";
    const keyword = (cloudServiceSearchKeywords[service.key] || "").trim().toLowerCase();
    const visibleOptions = options.filter((option) =>
      !keyword || `${option.providerName} ${option.groupName} ${option.baseUrl}`.toLowerCase().includes(keyword),
    );
    const cloudReady = cloudServiceReadyStatus[service.key];

    const configured = isCloudServiceConfigured(service.key);
    const saving = savingCapabilities.has(service.key);
    return { key: service.key, configured, node: (
      <div
        role="group"
        aria-label={serviceTitle}
        className={`model-provider-default-row model-provider-cloud-service-row${!configured ? " is-pending" : ""}`}
        key={service.key}
      >
        <div className="model-provider-default-meta">
          <label
            className="model-provider-default-title"
            htmlFor={`model-provider-cloud-${service.key}`}
            title={serviceTitle}
          >
            <span className="model-provider-default-title-text">{serviceTitle}</span>
          </label>
          <Tooltip placement="top" title={serviceSubtitle} trigger={["hover", "focus"]}>
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
          {setupState === "ready" && !isAdmin && capabilityReadyStates[service.key] === "ready" ? (
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

        <div className="model-provider-default-controls">
        <div className="model-provider-default-input">
        {!configured && <p className="model-provider-pending-description">{t(service.setupDescriptionKey)}</p>}
        {!isAdmin && (capabilityReadyStates[service.key] === "error" || capabilityReadyStates[service.key] === "loading") ? (
          <Alert type="warning" showIcon message={t("modelProvider.capabilityStatusLoadFailed")}
            action={<Button size="small" aria-label={t("common.retry")} disabled={capabilityReadyStates[service.key] === "loading"}
              loading={capabilityReadyStates[service.key] === "loading"}
              onClick={() => void retryCapabilityReadiness(service.key)}>{t("common.retry")}</Button>} />
        ) : null}
        {setupState === "loading" || (!configured && !options.length && setupState === "ready" && (!optionState || optionLoading)) ? (
          <div role="status" aria-label={t("common.loading")}><Skeleton.Input active block size="small" /></div>
        ) : null}
        {setupState === "error" || (!configured && optionState === "error") ? (
          <Alert type="error" showIcon message={t("modelProvider.cloudServiceSetupLoadFailed")}
            action={<Button size="small" onClick={setupState === "error" ? onRetrySetup : () => void loadVerifiedCloudService(service)}>{t("common.retry")}</Button>} />
        ) : null}
        {setupState === "ready" && (configured || options.length > 0) && <Select
          allowClear
          disabled={saving}
          className="model-provider-model-select"
          filterOption={false}
          id={`model-provider-cloud-${service.key}`}
          optionLabelProp="label"
          placeholder={cloudReady?.group_name || t("modelProvider.cloudServicePlaceholder")}
          {...selectInteractionProps}
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
          }}
          onDropdownVisibleChange={(open) => {
            if (open) {
              void loadVerifiedCloudService(service);
            } else {
              setKeyboardNavigation(false);
            }
          }}
          loading={optionLoading || saving}
          notFoundContent={
            optionLoading
              ? t("common.loading")
              : <div className="model-provider-options-empty">
                  <span>{t("modelProvider.noCloudServiceOptions")}</span>
                  <Button type="link" size="small" onClick={() => onConfigureCloudService(service.key)}>{t(service.setupActionKey)}</Button>
                </div>
          }
        >
          {visibleOptions.map((option) => (
            <Select.Option
              key={option.groupId}
              title={`${option.providerName} · ${option.groupName}`}
              label={
                <span className="model-provider-select-value">
                  <span className="model-provider-cloud-service-icon">
                    {getCloudServiceIcon(option.providerName, service.category)}
                  </span>
                  <span className="model-provider-select-value-text">
                    {option.providerName} · {option.groupName}
                  </span>
                </span>
              }
              value={option.groupId}
            >
              <Tooltip
                title={`${option.providerName} · ${option.groupName}`}
                placement="right"
                overlayClassName="model-provider-option-tooltip"
              >
                <span className="model-provider-select-option" title="">
                  <span className="model-provider-cloud-service-icon">
                    {getCloudServiceIcon(option.providerName, service.category)}
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
              </Tooltip>
            </Select.Option>
          ))}
        </Select>}
        {setupState === "empty" || setupState === "ready" ? (
          <Button className="model-provider-configure-capability" type="text" size="small" disabled={saving} onClick={() => onConfigureCloudService(service.key)}>
            {t("modelProvider.configureCapability")}
            <RightOutlined aria-hidden="true" />
          </Button>
        ) : null}

        </div>
        {configured && setupState === "ready" && isAdmin && !runtimeFeatures.hideUserGroupSurfaces ? (
          <Tooltip title={t(cloudServiceShareStatus[service.key] ? "modelProvider.shareOn" : "modelProvider.shareOff")}>
            <span className="model-provider-share-control">
              <span aria-hidden="true">{t(cloudServiceShareStatus[service.key] ? "modelProvider.shared" : "modelProvider.unshared")}</span>
              <Switch
                aria-label={t("modelProvider.shareToggleAria", { title: serviceTitle })}
                checked={!!cloudServiceShareStatus[service.key]}
                disabled={saving}
                className="model-provider-share-switch"
                size="small"
                onChange={(checked) => toggleShareCloudService(service.key, checked)}
              />
            </span>
          </Tooltip>
        ) : null}
        </div>
      </div>
    ) };
  });

  const cards = [...modelCards, ...cloudCards];
  const basicCards = cards.filter((card) => card.key === "llm" || card.key === "embed_main");

  return (
    <section
      className={`model-provider-config-panel model-defaults-panel model-provider-focus-${focusModality}`}
      data-focus-modality={focusModality}
      aria-label={t("modelProvider.defaultConfigAria")}
    >
      <div className="model-provider-panel-title-row">
        <div>
          <h2 className="model-provider-section-title">
            {t("modelProvider.defaultTitle")}
          </h2>
          <p className="model-provider-section-subtitle model-defaults-intro">
            {t("modelProvider.defaults.intro")}
          </p>
        </div>
      </div>

	  {lazyMindCloudAvailable ? (
		<Alert
		  showIcon
		  data-provider-key={LAZYMIND_CLOUD_PROVIDER_KEY}
		  type="success"
		  message={t("modelProvider.lazyMindCloudTitle")}
		  description={t("modelProvider.lazyMindCloudAvailable")}
		/>
	  ) : null}

      {defaultLoadState === "loading" ? (
        <div role="status" aria-live="polite">
          <span>{t("common.loading")}</span>
          <Skeleton active paragraph={{ rows: 4 }} />
        </div>
      ) : defaultLoadState === "error" ? (
        <Alert type="error" showIcon message={t("modelProvider.defaultConfigLoadFailed")}
          action={<Button onClick={retryDefaultModelState}>{t("common.retry")}</Button>} />
      ) : (
        <>
          <section className="model-defaults-basic" aria-label={t("modelProvider.defaults.basicTitle")}>
            <div className="model-defaults-section-heading">
              <h3>{t("modelProvider.defaults.basicTitle")}</h3>
            </div>
            <div className="model-defaults-rows">{basicCards.map((card) => card.node)}</div>
          </section>
          <div className="model-defaults-extensions-heading">
            <h3>{t("modelProvider.defaults.extensionsTitle")}</h3>
            <p>{t("modelProvider.defaults.extensionsDescription")}</p>
          </div>
          <div className="model-defaults-groups">
            {defaultCapabilityGroups.map(({ key, icon: Icon, capabilities }) => {
              const groupCards = capabilities.flatMap((capability) => cards.filter((card) => card.key === capability));
              if (!groupCards.length) return null;
              const expanded = expandedGroups.includes(key);
              const title = t(`modelProvider.defaults.${key}Title`);
              const contentId = `${groupId}-${key}`;
              const count = groupCards.filter((card) => card.configured).length;
              return (
                <div className={`model-defaults-group${expanded ? " is-expanded" : ""}`} key={key}>
                  <h3 className="model-defaults-group-heading">
                    <button type="button" className="model-defaults-group-toggle"
                      aria-expanded={expanded} aria-controls={contentId}
                      onClick={() => setExpandedGroups((current) => expanded
                        ? current.filter((item) => item !== key) : [...current, key])}>
                      <span className="model-defaults-group-icon" aria-hidden="true"><Icon /></span>
                      <span className="model-defaults-group-copy">
                        <span className="model-defaults-group-title">{title}</span>
                        <span className="model-defaults-group-description">{t(`modelProvider.defaults.${key}Description`)}</span>
                      </span>
                      <span className={`model-defaults-group-count${count ? " has-configured" : ""}`}>
                        {t("modelProvider.defaults.configuredCount", { configured: count, total: groupCards.length })}
                      </span>
                      <DownOutlined className="model-defaults-group-caret" aria-hidden="true" />
                    </button>
                  </h3>
                  <section id={contentId} aria-label={title} hidden={!expanded}>
                    {expanded && <div className="model-defaults-rows">{groupCards.map((card) => card.node)}</div>}
                  </section>
                </div>
              );
            })}
          </div>
        </>
      )}
    </section>
  );
}
