import { useEffect, useRef, useState } from "react";
import { Alert, Button, Select, Skeleton, Switch, Tag, Tooltip } from "antd";
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
import { runtimeFeatures } from "@/runtime/features";
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

export type {
  CloudServiceSlotKey,
  ModelCapability,
  SetupAvailabilityState,
} from "../hooks/useDefaultModelConfig";

const LAZYMIND_CLOUD_PROVIDER_KEY = "lazymind_cloud";

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
  const { t } = useTranslation();
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
  const highlightedRowRef = useRef<HTMLDivElement | null>(null);
  const focusedHighlightRef = useRef<string | null>(null);

  useEffect(() => {
    if (
      !highlightTarget ||
      !highlightedRowRef.current ||
      focusedHighlightRef.current === highlightTarget
    ) {
      return;
    }
    focusedHighlightRef.current = highlightTarget;
    const frame = window.requestAnimationFrame(() => {
      highlightedRowRef.current?.scrollIntoView({
        behavior: "smooth",
        block: "center",
      });
      highlightedRowRef.current?.focus({ preventScroll: true });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [highlightTarget, modelProviderSetupState, moduleModelOptions, defaultLoadState]);

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
      !keyword || `${option.model.name} ${option.provider.name} ${option.group.name}`.toLowerCase().includes(keyword),
    );
    const moduleTitle = t(module.titleKey);
    const moduleSubtitle = t(module.subtitleKey);
    const maxInputTokens = selectedModelMaxInputTokens[module.key];
    const shouldShowMaxInputTokens = Boolean(maxInputTokens?.trim());
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
          >
            {module.required ? (
              <span className="is-required">*</span>
            ) : null}
            <span>{moduleTitle}</span>
          </label>
          {configured && shouldShowMaxInputTokens ? (
            <span className="model-provider-max-input-tokens">
              {t("modelProvider.maxInputTokens", {
                value: maxInputTokens,
              })}
            </span>
          ) : null}
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
          {configured && isAdmin && !runtimeFeatures.hideUserGroupSurfaces && !selectedIsCloud ? (
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
                disabled={saving}
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
          <Button className="model-provider-configure-capability" size="small"
            disabled={(module.restricted && !isAdmin) || saving} onClick={onConfigureProviders}>
            {t(module.restricted && !isAdmin
              ? "modelProvider.contactAdminToConfigure"
              : "modelProvider.configureCapability")}
          </Button>
        ) : null}
        {modelProviderSetupState === "ready" && (configured || hasAvailableOptions) && <Select
          allowClear={!module.required}
          className="model-provider-model-select"
          disabled={(module.restricted && !isAdmin) || saving}
          filterOption={false}
          id={`model-provider-${module.key.toLowerCase()}`}
          listHeight={340}
          optionLabelProp="label"
          placeholder={
            modelReadyStatus[module.key]?.model_name || (module.restricted && !isAdmin
              ? t("modelProvider.restrictedPlaceholder")
              : module.required
                ? t("modelProvider.requiredModelPlaceholder")
                : t("modelProvider.optionalModelPlaceholder"))
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
          }}
          onDropdownVisibleChange={(open) => {
            if (open) {
              void loadModuleModels(module.key, true);
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
          {visibleOptions.map((option) => {
            const { provider, group, model, value } = option;
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
                title={`${displayName} · ${group.name || provider.name}`}
                disabled={
                  model.availability === "unavailable" ||
                  model.lifecycle === "deprecated" ||
                  model.lifecycle === "retired"
                }
                label={
                  <span className="model-provider-select-value">
                    <ProviderLogo provider={provider} compact />
                    <span className="model-provider-select-value-text">
                      {displayName} · {group.name || provider.name}
                    </span>
                  </span>
                }
                value={value}
              >
                <Tooltip
                  title={`${displayName} · ${group.name || provider.name}`}
                  placement="right"
                  overlayClassName="model-provider-option-tooltip"
                >
                  <span className="model-provider-select-option" title="">
                    <ProviderLogo provider={provider} compact />
                    <span className="model-provider-select-copy">
                      <strong>{displayName}</strong>
                      <small>
                        {option.source === "cloud"
                          ? t("modelProvider.cloudSystemReadOnly")
                          : `${provider.name} / ${group.name}`}
                        {option.source === "cloud"
                          ? ""
                          : model.builtIn
                          ? t("modelProvider.builtInModelSuffix")
                          : t("modelProvider.customModelSuffix")}
                      </small>
                    </span>
                  </span>
                </Tooltip>
              </Select.Option>
            );
          })}
        </Select>}
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
          {configured && setupState === "ready" && isAdmin && !runtimeFeatures.hideUserGroupSurfaces ? (
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
                disabled={saving}
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
        {setupState === "empty" || (!configured && setupState === "ready" && optionState === "ready" && !options.length) ? (
          <Button className="model-provider-configure-capability" size="small" disabled={saving} onClick={() => onConfigureCloudService(service.key)}>
            {t("modelProvider.configureCapability")}
          </Button>
        ) : null}
        {setupState === "ready" && (configured || options.length > 0) && <Select
          allowClear
          disabled={saving}
          className="model-provider-model-select"
          filterOption={false}
          id={`model-provider-cloud-${service.key}`}
          optionLabelProp="label"
          placeholder={cloudReady?.group_name || t("modelProvider.cloudServicePlaceholder")}
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
          }}
          onDropdownVisibleChange={(open) => {
            if (open) {
              void loadVerifiedCloudService(service);
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
      </div>
    ) };
  });

  const cards = [...modelCards, ...cloudCards];
  const configuredCards = cards.filter((card) => card.configured);
  const pendingCards = cards.filter((card) => !card.configured);

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
          <section className="model-provider-capability-section" aria-label={t("modelProvider.configuredCapabilities")}>
            <div className="model-provider-capability-heading">
              <h3>{t("modelProvider.configuredCapabilities")} <span>{configuredCards.length}</span></h3>
            </div>
            {configuredCards.length ? (
              <div className="model-provider-default-list">{configuredCards.map((card) => card.node)}</div>
            ) : <p className="model-provider-capability-empty">{t("modelProvider.configuredCapabilitiesEmpty")}</p>}
          </section>
          <section className="model-provider-capability-section" aria-label={t("modelProvider.pendingCapabilities")}>
            <div className="model-provider-capability-heading">
              <h3>{t("modelProvider.pendingCapabilities")} <span>{pendingCards.length}</span></h3>

            </div>
            {pendingCards.length ? (
              <div className="model-provider-pending-list">{pendingCards.map((card) => card.node)}</div>
            ) : <p className="model-provider-capability-empty">{t("modelProvider.pendingCapabilitiesEmpty")}</p>}
          </section>
        </>
      )}
    </section>
  );
}
