import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  ArrowLeftOutlined,
  CheckCircleFilled,
  CloudOutlined,
  DeleteOutlined,
  EditOutlined,
  MoreOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import {
  Alert,
  Button,
  Drawer,
  Dropdown,
  Empty,
  Form,
  Input,
  Modal,
  Select,
  Spin,
  Tooltip,
  message,
} from "antd";
import { useTranslation } from "react-i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  useSettingsDraft,
  useSettingsFormDraft,
} from "@/modules/settings/SettingsNavigationGuard";
import {
  modelProvidersDefaultApi,
  modelProvidersApi,
  createCustomProvider,
  updateCustomProviderName,
  deleteCustomProvider,
  unwrapModelProviderData,
  updateProviderEndpoint,
  setProviderActiveKey,
  withModelProviderJsonOptions,
} from "../api";
import { getProviderLogoUrl } from "../providerBranding";
import { getProviderDisplayName, getProviderSearchText } from "../providerDisplayNames";
import { useSupplierOrder } from "../hooks/useSupplierOrder";
import { useFocusModality } from "../hooks/useFocusModality";
import { canDiscoverWithoutApiKey } from "../providerAuthentication";
import { getProviderConnectionFailureMessage } from "../connectionFailure";
import { SortableSupplierList } from "./SortableSupplierList";
import { ModelTaskTabs, matchesModelTaskFilter, type ModelTaskFilter } from "./ModelTaskTabs";
import { SavedProviderModels } from "./SavedProviderModels";
import { ProviderKeyManager } from "./ProviderKeyManager";
import { ProviderRequestConfigDrawer } from "./ProviderRequestConfigDrawer";
import type {
  AddedProvider,
  ModelCapability,
  ProviderConnectionGroup,
  ProviderModel,
  ProviderOption,
} from "../pages/ModelProvidersPage";

interface Props {
  providers: ProviderOption[];
  configuredProviders: AddedProvider[];
  loading: boolean;
  loadError: boolean;
  onRetry: () => void;
  highlightProviderId?: string;
  onBack?: () => void;
  capabilityLabel: (capability: ModelCapability) => string;
  onLoadModels: (
    provider: AddedProvider,
    group: ProviderConnectionGroup,
  ) => void;
  modelsLoading: Record<string, boolean>;
  modelsError?: Record<string, string>;
  onAddModel: (provider: AddedProvider, group: ProviderConnectionGroup) => void;
  onSyncModels: (
    provider: AddedProvider,
    group: ProviderConnectionGroup,
  ) => void;
  onEditModel: (
    provider: AddedProvider,
    group: ProviderConnectionGroup,
    model: ProviderModel,
  ) => void;
  onDeleteModel: (
    provider: AddedProvider,
    group: ProviderConnectionGroup,
    model: ProviderModel,
  ) => void | Promise<boolean>;
  onCheck: (provider: AddedProvider, group: ProviderConnectionGroup) => void;
  checking: Record<string, boolean>;
  onReload: () => Promise<void>;
  onKeySaved?: (
    provider: AddedProvider,
    groupId: string,
    isCurrent: () => boolean,
  ) => Promise<void>;
  cloudContent: ReactNode;
}

function Logo({ provider }: { provider: ProviderOption }) {
  const logo = getProviderLogoUrl(
    provider.name || provider.protocolName || provider.source,
    provider.isCustom,
  );
  return (
    <span className="provider-workspace-logo" aria-hidden="true">
      {logo ? <img src={logo} alt="" /> : provider.name.slice(0, 2)}
    </span>
  );
}

function hasProviderConfiguration(provider: AddedProvider): boolean {
  return provider.groups.some(
    (group) => group.apiKeyConfigured || group.keys.length > 0 || (group.modelCount || 0) > 0 || group.models.length > 0,
  );
}

export function ProviderWorkspace(props: Props) {
  const { t, i18n } = useTranslation();
  const focusModality = useFocusModality();
  const language = i18n?.resolvedLanguage || i18n?.language;
  const label = (key: string, options?: Record<string, unknown>) =>
    t(`modelProvider.workspace.${key}`, options);
  const [selectedId, setSelectedId] = useState(props.highlightProviderId || "");
  const [search, setSearch] = useState("");
  const [modelSearch, setModelSearch] = useState("");
  const [modelType, setModelType] = useState<ModelTaskFilter>("all");
  const [configuredOnly, setConfiguredOnly] = useState(false);
  const [cloudOpen, setCloudOpen] = useState(false);
  const [requestConfigOpen, setRequestConfigOpen] = useState(false);
  const [mobileDetail, setMobileDetail] = useState(false);
  const [endpointSaving, setEndpointSaving] = useState(false);
  const [savedEndpoint, setSavedEndpoint] = useState<{ providerId: string; baseUrl: string }>();
  const [keyOpen, setKeyOpen] = useState(false);
  const [keySaving, setKeySaving] = useState(false);
  const [removingKey, setRemovingKey] = useState("");
  const [changingActiveKey, setChangingActiveKey] = useState("");
  const [providerModal, setProviderModal] = useState<"create" | "edit" | null>(
    null,
  );
  const [providerSaving, setProviderSaving] = useState(false);
  const [providerDeleting, setProviderDeleting] = useState(false);
  const [deleteModal, deleteModalContext] = Modal.useModal();
  const [initializing, setInitializing] = useState(false);
  const [providerForm] = Form.useForm<{
    name: string;
    sourceProviderId: string;
    baseUrl: string;
  }>();
  const providerDraft = useSettingsFormDraft(
    providerForm,
    Boolean(providerModal),
    providerSaving,
  );
  const activeKeyRequestPending = useRef(false);
  const addKeyRequestPending = useRef(false);
  const [endpointForm] = Form.useForm<{ baseUrl: string }>();
  const [keyForm] = Form.useForm<{ apiKey: string }>();
  const keyDraft = useSettingsFormDraft(keyForm, keyOpen, keySaving);
  const endpointValue = Form.useWatch("baseUrl", endpointForm);
  const sourceTemplateId = Form.useWatch("sourceProviderId", providerForm);
  const loadedGroups = useRef(new Set<string>());
  const mergedProviders = useMemo(
    () =>
      props.providers
        .map(
          (provider): AddedProvider =>
            props.configuredProviders.find(
              (item) => item.id === provider.id,
            ) || {
              ...provider,
              groups: [],
            },
        ),
    [props.providers, props.configuredProviders],
  );
  const { providers, reorder: reorderSuppliers } = useSupplierOrder(mergedProviders);
  const provider =
    providers.find((item) => item.id === selectedId) || providers[0];
  const activeSupplierId = useRef<string | undefined>(provider?.id);
  activeSupplierId.current = provider?.id;
  useEffect(() => {
    activeSupplierId.current = provider?.id;
    return () => { activeSupplierId.current = undefined; };
  }, [provider?.id]);
  const providerDisplayName = provider
    ? getProviderDisplayName(provider, language)
    : "";
  // One private compatibility record belongs to each supplier.
  const group = provider?.groups[0];
  const groupKey = provider && group ? `${provider.id}:${group.id}` : "";
  const hasSavedKeys = Boolean(group?.keys.length || group?.apiKeyConfigured);
  const hasEnabledKey = group?.keys.length ? group.keys.some((key) => key.enabled) : Boolean(group?.apiKeyConfigured);
  const keylessCustom = provider ? canDiscoverWithoutApiKey(provider, group) : false;
  const verifiedSelection = (hasEnabledKey || keylessCustom) && group?.verified === true;
  const supplierStatus = verifiedSelection ? "connected" : keylessCustom ? "notConnected" : !hasSavedKeys ? "notConfigured" : !hasEnabledKey ? "disabled" : "notConnected";
  const modelsError = groupKey ? props.modelsError?.[groupKey] : undefined;
  const endpointBaseline = savedEndpoint && savedEndpoint.providerId === provider?.id ? savedEndpoint.baseUrl : provider?.baseUrl || "";
  const endpointDirty =
    Boolean(provider && savedEndpoint?.providerId === provider.id) &&
    (endpointValue ?? endpointBaseline).trim() !== endpointBaseline;
  const confirmEndpointLeave = useSettingsDraft({
    dirty: endpointDirty,
    saving: endpointSaving,
    discard: () =>
      endpointForm.setFieldsValue({ baseUrl: endpointBaseline }),
  });

  useEffect(() => {
    if (props.highlightProviderId) {
      setSelectedId(props.highlightProviderId);
      setMobileDetail(true);
    }
  }, [props.highlightProviderId]);
  useEffect(() => {
    if (!provider) return;
    setSelectedId(provider.id);
    setSavedEndpoint({ providerId: provider.id, baseUrl: provider.baseUrl });
    endpointForm.setFieldsValue({ baseUrl: provider.baseUrl });
  }, [provider?.id, provider?.baseUrl]);
  useEffect(() => {
    if (!provider || !group || loadedGroups.current.has(groupKey)) return;
    loadedGroups.current.add(groupKey);
    props.onLoadModels(provider, group);
  }, [groupKey]);

  const changeProvider = (id: string) =>
    confirmEndpointLeave(() => {
      setSelectedId(id);
      setModelSearch("");
      setModelType("all");
      setMobileDetail(true);
    });
  const saveEndpoint = async ({ baseUrl }: { baseUrl: string }) => {
    if (!provider || endpointSaving) return;
    setEndpointSaving(true);
    try {
      const saved = await updateProviderEndpoint(provider.id, baseUrl.trim());
      const canonical = saved?.base_url ?? baseUrl.trim();
      if (activeSupplierId.current === provider.id) {
        setSavedEndpoint({ providerId: provider.id, baseUrl: canonical });
        endpointForm.setFieldsValue({ baseUrl: canonical });
      }
      confirmEndpointLeave.acceptSaved();
      await props.onReload();
      message.success(label("endpointSaved"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setEndpointSaving(false);
    }
  };
  const openAddKey = () => {
    keyForm.resetFields();
    setKeyOpen(true);
    keyDraft.captureSavedValues();
  };
  const checkSavedKey = (supplier: AddedProvider, groupId: string, isCurrent: () => boolean) => {
    void (async () => {
      try {
        await props.onKeySaved?.(supplier, groupId, isCurrent);
      } catch (error) {
        if (isCurrent()) message.error(getProviderConnectionFailureMessage(error, t));
      }
    })();
  };
  const addKey = async ({ apiKey }: { apiKey: string }): Promise<boolean> => {
    if (!provider || addKeyRequestPending.current || !apiKey.trim()) return false;
    const supplierId = provider.id;
    addKeyRequestPending.current = true;
    setKeySaving(true);
    let saved = false;
    let savedGroupId = group?.id || "";
    try {
      if (group) {
        await modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysPost(
          { modelProviderId: provider.id, groupId: group.id },
          withModelProviderJsonOptions({
            data: { api_key: apiKey.trim() },
            timeout: 180000,
          }),
        );
      } else {
        const response = await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsPost(
          {
            modelProviderId: provider.id,
            createModelProviderGroupOpenAPIRequest: {
              name: provider.name,
              api_key: apiKey.trim(),
              verify: false,
            },
          },
          { timeout: 180000 },
        );
        const created = unwrapModelProviderData<{ id: string; is_verified?: boolean }>(response.data);
        savedGroupId = created.id;
      }
      saved = true;
      if (activeSupplierId.current === supplierId) {
        keyDraft.acceptSaved();
        keyForm.resetFields();
        setKeyOpen(false);
      }
      await props.onReload();
      if (activeSupplierId.current === supplierId) message.success(label("keyAdded"));
      if (savedGroupId && activeSupplierId.current === supplierId) {
        checkSavedKey(provider, savedGroupId, () => activeSupplierId.current === supplierId);
      }
      return true;
    } catch (error) {
      const errorMessage = saved ? label("savedRefreshFailed") : getLocalizedErrorMessage(error);
      message.error(activeSupplierId.current === supplierId ? errorMessage : `${providerDisplayName}: ${errorMessage}`);
      return saved;
    } finally {
      addKeyRequestPending.current = false;
      setKeySaving(false);
    }
  };
  const deleteKey = async (keyId: string) => {
    if (!provider || !group || removingKey) return;
    setRemovingKey(keyId);
    try {
      await modelProvidersDefaultApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdKeysDelete(
        { modelProviderId: provider.id, groupId: group.id },
        withModelProviderJsonOptions({ data: { key_id: keyId } }),
      );
      await props.onReload();
      message.success(label("keyRemoved"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setRemovingKey("");
    }
  };
  const changeActiveKey = async (keyId: string, enabled: boolean) => {
    if (!provider || !group || activeKeyRequestPending.current) return;
    activeKeyRequestPending.current = true;
    setChangingActiveKey(keyId);
    try {
      await setProviderActiveKey(
        provider.id,
        enabled ? group.id : "",
        enabled ? keyId : "",
      );
      await props.onReload();
      message.success(label(enabled ? "keyActivated" : "keysDisabled"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      activeKeyRequestPending.current = false;
      setChangingActiveKey("");
    }
  };
  const openProviderModal = (mode: "create" | "edit") =>
    confirmEndpointLeave(() => {
      const source =
        providers.find(
          (item) => !item.isCustom && item.source.toLowerCase() === "openai",
        ) ||
        providers.find((item) => !item.isCustom) ||
        provider;
      providerForm.setFieldsValue({
        name: mode === "edit" ? provider?.name || "" : "",
        sourceProviderId: source?.id || "",
        baseUrl: source?.baseUrl || "",
      });
      setProviderModal(mode);
      providerDraft.captureSavedValues();
    });
  const saveProvider = async (values: {
    name: string;
    sourceProviderId: string;
    baseUrl: string;
  }) => {
    if (providerSaving || !providerModal) return;
    setProviderSaving(true);
    try {
      let nextId = provider?.id;
      if (providerModal === "create") {
        const created = await createCustomProvider({
          name: values.name.trim(),
          source_provider_id: values.sourceProviderId,
          base_url: values.baseUrl.trim(),
        });
        nextId = created.id;
      } else if (provider) {
        await updateCustomProviderName(provider.id, values.name.trim());
      }
      providerDraft.acceptSaved();
      setProviderModal(null);
      providerForm.resetFields();
      await props.onReload();
      if (nextId) {
        setSelectedId(nextId);
        setMobileDetail(true);
      }
      message.success(label("supplierSaved"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setProviderSaving(false);
    }
  };
  const removeProvider = async (confirmIndexedDowngrade: boolean) => {
    if (!provider?.isCustom || providerDeleting) return;
    setProviderDeleting(true);
    try {
      await deleteCustomProvider(provider.id, confirmIndexedDowngrade);
      await props.onReload();
      setSelectedId("");
      message.success(label("supplierDeleted"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setProviderDeleting(false);
    }
  };
  const confirmDeleteProvider = async () => {
    if (!provider?.isCustom || providerDeleting) return;
    setProviderDeleting(true);
    try {
      let hasEmbedding = false;
      if (group) {
        const response =
          await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsGroupIdModelsGet(
            { modelProviderId: provider.id, groupId: group.id },
          );
        const data = unwrapModelProviderData<{
          models?: Array<{ model_type?: string }>;
        }>(response.data);
        hasEmbedding = (data.models || []).some((model) =>
          ["embed", "embedding", "embed_main"].includes(
            (model.model_type || "").trim().toLowerCase(),
          ),
        );
      }
      deleteModal.confirm({
        title: label("deleteSupplier"),
        content: hasEmbedding
          ? t("modelProvider.confirmDeleteEmbeddingDesc")
          : label("deleteSupplierHint"),
        okText: t("common.delete"),
        cancelText: t("common.cancel"),
        okButtonProps: { danger: true },
        onOk: () => removeProvider(hasEmbedding),
      });
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setProviderDeleting(false);
    }
  };
  const withProviderModels = async (
    action: (
      current: AddedProvider,
      canonical: ProviderConnectionGroup,
    ) => void,
  ) => {
    if (!provider || initializing) return;
    const currentProvider = provider.baseUrl === endpointBaseline ? provider : { ...provider, baseUrl: endpointBaseline };
    if (group) {
      action(currentProvider, group.baseUrl === endpointBaseline ? group : { ...group, baseUrl: endpointBaseline });
      return;
    }
    setInitializing(true);
    try {
      const response =
        await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsPost({
          modelProviderId: provider.id,
          createModelProviderGroupOpenAPIRequest: {
            name: provider.name,
            api_key: "",
            verify: false,
          },
        });
      const created = unwrapModelProviderData<{
        id: string;
        is_verified?: boolean;
      }>(response.data);
      const canonical: ProviderConnectionGroup = {
        id: created.id,
        name: provider.name,
        source: provider.source,
        baseUrl: endpointBaseline,
        apiKeyConfigured: false,
        verified: created.is_verified === true,
        models: [],
        keys: [],
      };
      await props.onReload();
      action({ ...currentProvider, groups: [canonical] }, canonical);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setInitializing(false);
    }
  };
  const visibleProviders = providers.filter(
    (item) =>
      (!configuredOnly || hasProviderConfiguration(item)) &&
      getProviderSearchText(item, language).includes(search.toLowerCase().trim()),
  );
  const searchedModels = (group?.models || []).filter((model) =>
    model.name.toLowerCase().includes(modelSearch.toLowerCase().trim()),
  );
  const models = searchedModels.filter((model) => matchesModelTaskFilter(model, modelType));

  return (
    <section
      className={`provider-workspace model-provider-focus-${focusModality}${mobileDetail ? " is-mobile-detail" : ""}`}
      data-focus-modality={focusModality}
      aria-label={label("title")}
    >
      {deleteModalContext}
      {props.onBack ? (
        <header className="provider-workspace-topbar">
          <button
            type="button"
            className="provider-workspace-back"
            onClick={() => confirmEndpointLeave(props.onBack!)}
          >
            <ArrowLeftOutlined aria-hidden="true" />
            {label("back")}
          </button>
        </header>
      ) : null}
      {props.loadError ? (
        <div className="provider-workspace-load-error" role="alert">
          <span>{label("loadFailed")}</span>
          <Button type="text" onClick={props.onRetry} loading={props.loading}>
            {label("retry")}
          </Button>
        </div>
      ) : null}
      <div className="provider-workspace-body">
        <aside
          className="provider-workspace-sidebar"
          aria-label={label("suppliers")}
        >
          <Input
            allowClear
            prefix={<SearchOutlined aria-hidden="true" />}
            aria-label={t("modelProvider.searchAria")}
            placeholder={label("searchSupplier")}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <div className="provider-workspace-filters">
            <button
              className={!configuredOnly ? "is-active" : ""}
              type="button"
              onClick={() => setConfiguredOnly(false)}
            >
              {label("all")} <span>{providers.length}</span>
            </button>
            <button
              className={configuredOnly ? "is-active" : ""}
              type="button"
              onClick={() => setConfiguredOnly(true)}
            >
              {label("configured")}
            </button>
          </div>
          <nav
            className="provider-workspace-provider-list"
            aria-label={label("suppliers")}
          >
            <SortableSupplierList items={visibleProviders}
              disabled={initializing || providerSaving || providerDeleting}
              onReorder={reorderSuppliers}>
            {(item) => (
              <button
                type="button"
                key={item.id}
                className={`provider-workspace-provider${provider?.id === item.id ? " is-active" : ""}`}
                aria-current={provider?.id === item.id ? "page" : undefined}
                disabled={initializing || providerSaving || providerDeleting}
                onClick={() => changeProvider(item.id)}
                title={getProviderDisplayName(item, language)}
              >
                <Logo provider={item} />
                <span>{getProviderDisplayName(item, language)}</span>
                {item.groups.some((candidate) => candidate.apiKeyConfigured || candidate.keys.length > 0) ? (
                  <i
                    className={
                      item.groups.some((candidate) => candidate.verified)
                        ? "is-ready"
                        : ""
                    }
                    aria-label={
                      item.groups.some((candidate) => candidate.verified)
                        ? t("modelProvider.verified")
                        : label("configured")
                    }
                  />
                ) : null}
              </button>
            )}
            </SortableSupplierList>
            {!visibleProviders.length ? (
              <div className="provider-workspace-empty">
                {props.loading ? (
                  <Spin size="small" />
                ) : (
                  t("modelProvider.noMatchedProviders")
                )}
              </div>
            ) : null}
          </nav>
          <div className="provider-workspace-sidebar-actions">
            <Tooltip title={label("cloud")}>
              <Button
                aria-label={label("cloud")}
                icon={<CloudOutlined aria-hidden="true" />}
                onClick={() => setCloudOpen(true)}
              />
            </Tooltip>
            <Tooltip title={label("addSupplier")}>
              <Button
                aria-label={label("addSupplier")}
                icon={<PlusOutlined aria-hidden="true" />}
                onClick={() => openProviderModal("create")}
                disabled={initializing || providerSaving}
              />
            </Tooltip>
          </div>
        </aside>
        <main className="provider-workspace-detail">
          {provider ? (
            <>
              <header className="provider-workspace-supplier-header">
                <div className="provider-workspace-supplier-title">
                  <Button
                    type="text"
                    className="provider-workspace-mobile-back"
                    aria-label={label("suppliers")}
                    icon={<ArrowLeftOutlined aria-hidden="true" />}
                    onClick={() => confirmEndpointLeave(() => setMobileDetail(false))}
                  />
                  <Logo provider={provider} />
                  <div className="provider-workspace-supplier-identity">
                    <h2 title={providerDisplayName}>{providerDisplayName}</h2>
                    <span
                      className={`provider-workspace-status${verifiedSelection ? " is-ready" : ""}`}
                    >
                      {verifiedSelection ? (
                        <CheckCircleFilled aria-hidden="true" />
                      ) : (
                        <span className="provider-workspace-status-dot" />
                      )}
                      {label(supplierStatus)}
                    </span>
                  </div>
                </div>
                <div className="provider-workspace-supplier-actions">
                  {provider.isCustom ? (
                    <>
                      <Button
                        type="text"
                        aria-label={label("editSupplier")}
                        icon={<EditOutlined aria-hidden="true" />}
                        onClick={() => openProviderModal("edit")}
                        disabled={
                          Boolean(changingActiveKey) || providerDeleting
                        }
                      />
                      <Button
                        type="text"
                        danger
                        aria-label={label("deleteSupplier")}
                        icon={<DeleteOutlined aria-hidden="true" />}
                        loading={providerDeleting}
                        disabled={endpointDirty || Boolean(changingActiveKey)}
                        onClick={() => void confirmDeleteProvider()}
                      />
                    </>
                  ) : null}
                </div>
              </header>
              <div className="provider-workspace-scroll">
                <section
                  className="provider-workspace-api-keys"
                  aria-label={t("modelProvider.apiKeyLabel")}
                >
                  <ProviderKeyManager
                    providerId={provider.id}
                    providerName={providerDisplayName}
                    groupId={group?.id || ""}
                    keys={group?.keys || []}
                    disabled={endpointDirty || endpointSaving || keySaving}
                    changingKey={changingActiveKey}
                    removingKey={removingKey}
                    onAdd={openAddKey}
                    onCreate={(apiKey) => addKey({ apiKey })}
                    onDelete={deleteKey}
                    onToggle={changeActiveKey}
                    onReload={async () => {
                      const supplierId = provider.id;
                      await props.onReload();
                      if (group && activeSupplierId.current === supplierId) {
                        checkSavedKey(provider, group.id, () => activeSupplierId.current === supplierId);
                      }
                    }}
                    checkAction={
                      <Button
                        type="text"
                        onClick={() => void withProviderModels(props.onCheck)}
                        disabled={
                          endpointDirty ||
                          endpointSaving ||
                          keySaving ||
                          Boolean(changingActiveKey)
                        }
                        loading={initializing || props.checking[groupKey]}
                      >
                        {label("checkConnection")}
                      </Button>
                    }
                  />
                </section>
                <Form
                  name="supplier-endpoint"
                  form={endpointForm}
                  layout="vertical"
                  requiredMark={false}
                  onFinish={saveEndpoint}
                  className="provider-workspace-endpoint"
                >
                  <div className="provider-workspace-endpoint-row">
                    <Form.Item
                      label={t("modelProvider.apiAddressLabel")}
                      name="baseUrl"
                      normalize={(value: string) => value.trim()}
                      rules={[
                        {
                          required: true,
                          message: t(
                            "modelProvider.validation.baseUrlRequired",
                          ),
                        },
                        {
                          type: "url",
                          message: t("modelProvider.validation.baseUrlInvalid"),
                        },
                        {
                          max: 512,
                          message: t("modelProvider.validation.baseUrlMax"),
                        },
                        {
                          validator: (_, value?: string) => {
                            if (!value) return Promise.resolve();
                            try {
                              const parsed = new URL(value);
                              return ["http:", "https:"].includes(
                                parsed.protocol,
                              ) &&
                                !parsed.username &&
                                !parsed.password &&
                                !parsed.search &&
                                !parsed.hash
                                ? Promise.resolve()
                                : Promise.reject(
                                    new Error(
                                      t(
                                        "modelProvider.validation.baseUrlInvalid",
                                      ),
                                    ),
                                  );
                            } catch {
                              return Promise.reject(
                                new Error(
                                  t("modelProvider.validation.baseUrlInvalid"),
                                ),
                              );
                            }
                          },
                        },
                      ]}
                    >
                      <Input
                        aria-label={t("modelProvider.apiAddressLabel")}
                        disabled={Boolean(changingActiveKey)}
                        maxLength={512}
                        spellCheck={false}
                        autoComplete="off"
                      />
                    </Form.Item>
                    {endpointDirty || endpointSaving ? (
                      <Button
                        htmlType="submit"
                        loading={endpointSaving}
                        disabled={!endpointDirty || Boolean(changingActiveKey)}
                      >
                        {label("saveEndpoint")}
                      </Button>
                    ) : null}
                    <Tooltip title={t("modelProvider.requestConfig.title")}>
                      <Button
                        type="text"
                        className="provider-workspace-icon-button"
                        aria-label={t("modelProvider.requestConfig.title")}
                        icon={<SettingOutlined aria-hidden="true" />}
                        disabled={endpointSaving || Boolean(changingActiveKey)}
                        onClick={() =>
                          confirmEndpointLeave(() => setRequestConfigOpen(true))
                        }
                      />
                    </Tooltip>
                  </div>
                  {endpointDirty ? <p>{label("endpointHint")}</p> : null}
                </Form>
                <section
                  className="provider-workspace-models"
                  aria-label={label("models")}
                >
                  <div className="provider-workspace-section-heading provider-workspace-model-heading">
                    <h3>
                      {label("models")}{" "}
                      <span className="provider-workspace-count">
                        {group?.modelCount ?? group?.models.length ?? 0}
                      </span>
                    </h3>
                    <div className="provider-workspace-model-toolbar">
                      <Input
                        allowClear
                        prefix={<SearchOutlined aria-hidden="true" />}
                        aria-label={label("searchModel")}
                        placeholder={label("searchModel")}
                        value={modelSearch}
                        onChange={(event) => setModelSearch(event.target.value)}
                      />
                      <Tooltip title={label("syncModels")}>
                        <Button
                          className="provider-workspace-fetch-models"
                          aria-label={label("getModelList")}
                          icon={<ReloadOutlined aria-hidden="true" />}
                          loading={props.modelsLoading[groupKey]}
                          disabled={initializing || endpointDirty || endpointSaving || props.modelsLoading[groupKey]}
                          onClick={() =>
                            void withProviderModels(props.onSyncModels)
                          }
                        >
                          {label("getModelList")}
                        </Button>
                      </Tooltip>
                      <Tooltip title={t("modelProvider.customModel")}>
                        <Button
                          type="text"
                          className="provider-workspace-icon-button"
                          aria-label={t("modelProvider.customModel")}
                          icon={<PlusOutlined aria-hidden="true" />}
                          disabled={initializing || endpointDirty || endpointSaving || props.modelsLoading[groupKey]}
                          onClick={() =>
                            void withProviderModels(props.onAddModel)
                          }
                        />
                      </Tooltip>
                      <Tooltip title={label("moreModelActions")}>
                        <Dropdown
                          trigger={["click"]}
                          menu={{
                            items: [
                              {
                                key: "refresh",
                                label: label("refreshSavedModels"),
                                icon: <ReloadOutlined aria-hidden="true" />,
                                disabled: !group || initializing || props.modelsLoading[groupKey],
                                onClick: () => {
                                  if (group) props.onLoadModels(provider, group);
                                },
                              },
                              {
                                key: "request-config",
                                label: t("modelProvider.requestConfig.title"),
                                icon: <SettingOutlined aria-hidden="true" />,
                                disabled: endpointSaving || Boolean(changingActiveKey),
                                onClick: () => confirmEndpointLeave(() => setRequestConfigOpen(true)),
                              },
                            ],
                          }}
                        >
                          <Button
                            type="text"
                            className="provider-workspace-icon-button"
                            aria-label={label("moreModelActions")}
                            icon={<MoreOutlined aria-hidden="true" />}
                          />
                        </Dropdown>
                      </Tooltip>
                    </div>
                  </div>
                  <ModelTaskTabs models={searchedModels} value={modelType} onChange={setModelType} />
                  {modelsError ? (
                    <Alert
                      className="provider-workspace-models-error"
                      type="error"
                      showIcon
                      message={label("modelsLoadFailed")}
                      description={modelsError}
                      action={
                        <Button
                          size="small"
                          loading={props.modelsLoading[groupKey]}
                          onClick={() => {
                            if (group) props.onLoadModels(provider, group);
                          }}
                        >
                          {label("retry")}
                        </Button>
                      }
                    />
                  ) : null}
                  <SavedProviderModels
                    models={models}
                    emptyDescription={
                      modelType === "all" && !modelSearch.trim()
                        ? label("syncModelsEmpty")
                        : label("noMatchingModels")
                    }
                    loading={Boolean(props.modelsLoading[groupKey])}
                    providerName={
                      provider.protocolName || provider.source || provider.name
                    }
                    supplierName={provider.name}
                    probe={group ? {
                      providerId: provider.id,
                      groupId: group.id,
                      configurationKey: JSON.stringify([provider.protocolName || provider.source, endpointBaseline, group.baseUrl, group.keys.map((key) => [key.id, key.enabled])]),
                      disabledReason: endpointDirty || endpointSaving ? t("modelProvider.probe.saveAddressFirst") : hasSavedKeys && !hasEnabledKey ? t("modelProvider.probe.reason.key_disabled") : undefined,
                    } : undefined}
                    capabilityLabel={props.capabilityLabel}
                    onEdit={(model) => {
                      if (group) props.onEditModel(provider, group, model);
                    }}
                    onRemove={(model) => {
                      if (group) return props.onDeleteModel(provider, group, model);
                    }}
                  />
                </section>
              </div>
            </>
          ) : (
            <div className="provider-workspace-empty">
              {props.loading ? (
                <Spin />
              ) : (
                <Empty description={t("modelProvider.noMatchedProviders")} />
              )}
            </div>
          )}
        </main>
      </div>
      {provider ? (
        <ProviderRequestConfigDrawer
          providerId={provider.id}
          providerName={providerDisplayName}
          open={requestConfigOpen}
          onClose={() => setRequestConfigOpen(false)}
          onSaved={props.onReload}
        />
      ) : null}
      <Drawer
        rootClassName={`model-provider-focus-${focusModality}`}
        title={label("cloud")}
        open={cloudOpen}
        width={460}
        onClose={() => setCloudOpen(false)}
      >
        {props.cloudContent}
      </Drawer>
      <Modal
        rootClassName={`model-provider-focus-${focusModality}`}
        title={label(providerModal === "edit" ? "editSupplier" : "addSupplier")}
        open={Boolean(providerModal)}
        centered
        confirmLoading={providerSaving}
        maskClosable={!providerSaving}
        okText={t("common.save")}
        onCancel={() =>
          providerDraft.confirmClose(() => {
            setProviderModal(null);
            providerForm.resetFields();
          })
        }
        onOk={() => providerForm.submit()}
        destroyOnHidden
      >
        <Form
          name="custom-supplier"
          form={providerForm}
          layout="vertical"
          onFinish={saveProvider}
        >
          <Form.Item
            label={label("supplierName")}
            name="name"
            rules={[
              {
                required: true,
                whitespace: true,
                message: label("supplierNameRequired"),
              },
              {
                validator: (_, value?: string) =>
                  new TextEncoder().encode(value?.trim() || "").length <= 255
                    ? Promise.resolve()
                    : Promise.reject(new Error(label("supplierNameBytes"))),
              },
            ]}
          >
            <Input maxLength={255} autoComplete="off" />
          </Form.Item>
          {providerModal === "create" ? (
            <>
              <Form.Item
                label={label("supplierProtocol")}
                name="sourceProviderId"
                rules={[
                  {
                    required: true,
                    message: label("supplierProtocolRequired"),
                  },
                ]}
              >
                <Select
                  options={providers
                    .filter((item) => !item.isCustom)
                    .map((item) => ({
                      value: item.id,
                      label: getProviderDisplayName(item, language),
                    }))}
                  onChange={(id) => {
                    const currentUrl = (providerForm.getFieldValue("baseUrl") || "").trim();
                    const previousUrl = providers.find((item) => item.id === sourceTemplateId)?.baseUrl.trim();
                    if (!currentUrl || currentUrl === previousUrl) {
                      providerForm.setFieldValue(
                        "baseUrl",
                        providers.find((item) => item.id === id)?.baseUrl || "",
                      );
                    }
                  }}
                />
              </Form.Item>
              <Form.Item
                label={t("modelProvider.apiAddressLabel")}
                name="baseUrl"
                rules={[
                  {
                    required: true,
                    message: t("modelProvider.validation.baseUrlRequired"),
                  },
                  {
                    type: "url",
                    message: t("modelProvider.validation.baseUrlInvalid"),
                  },
                  {
                    max: 512,
                    message: t("modelProvider.validation.baseUrlMax"),
                  },
                ]}
              >
                <Input maxLength={512} autoComplete="off" spellCheck={false} />
              </Form.Item>
            </>
          ) : null}
        </Form>
      </Modal>
      <Modal
        rootClassName={`model-provider-focus-${focusModality}`}
        title={label("addKey")}
        open={keyOpen}
        centered
        confirmLoading={keySaving}
        maskClosable={!keySaving}
        okText={label("addAndCheckKey")}
        onCancel={() =>
          keyDraft.confirmClose(() => {
            setKeyOpen(false);
            keyForm.resetFields();
          })
        }
        onOk={() => keyForm.submit()}
        destroyOnHidden
      >
        <Form
          name="supplier-key"
          form={keyForm}
          layout="vertical"
          onFinish={addKey}
        >
          <Form.Item
            label={t("modelProvider.apiKeyLabel")}
            name="apiKey"
            rules={[
              {
                required: true,
                whitespace: true,
                message: t("modelProvider.validation.apiKeyRequired"),
              },
              { max: 512, message: t("modelProvider.validation.apiKeyMax") },
              {
                pattern: /^\S+$/,
                message: t("modelProvider.validation.apiKeyNoSpaces"),
              },
            ]}
          >
            <Input.Password
              autoComplete="new-password"
              maxLength={512}
              visibilityToggle={false}
            />
          </Form.Item>
        </Form>
      </Modal>
    </section>
  );
}
