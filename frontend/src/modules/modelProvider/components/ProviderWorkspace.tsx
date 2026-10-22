import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  ArrowLeftOutlined,
  CheckCircleFilled,
  CloudOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import {
  Button,
  Drawer,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Spin,
  Switch,
  Tag,
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
import {
  isLlmChatCapability,
  resolveLlmMaxInputTokens,
} from "../maxInputTokens";
import { ModelModalityMarks } from "./ModelModalityMarks";
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
  ) => void;
  onCheck: (provider: AddedProvider, group: ProviderConnectionGroup) => void;
  checking: Record<string, boolean>;
  onReload: () => Promise<void>;
  cloudAvailable: boolean;
  cloudContent: ReactNode;
}

function Logo({ provider }: { provider: ProviderOption }) {
  const logo = getProviderLogoUrl(
    provider.protocolName || provider.source || provider.name,
    provider.isCustom,
  );
  return (
    <span className="provider-workspace-logo" aria-hidden="true">
      {logo ? <img src={logo} alt="" /> : provider.name.slice(0, 2)}
    </span>
  );
}

export function ProviderWorkspace(props: Props) {
  const { t } = useTranslation();
  const label = (key: string, options?: Record<string, unknown>) =>
    t(`modelProvider.workspace.${key}`, options);
  const [selectedId, setSelectedId] = useState(props.highlightProviderId || "");
  const [search, setSearch] = useState("");
  const [modelSearch, setModelSearch] = useState("");
  const [modelType, setModelType] = useState<string>("all");
  const [configuredOnly, setConfiguredOnly] = useState(false);
  const [cloudOpen, setCloudOpen] = useState(false);
  const [mobileDetail, setMobileDetail] = useState(false);
  const [endpointSaving, setEndpointSaving] = useState(false);
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
  const [endpointForm] = Form.useForm<{ baseUrl: string }>();
  const [keyForm] = Form.useForm<{ apiKey: string }>();
  const keyDraft = useSettingsFormDraft(keyForm, keyOpen, keySaving);
  const endpointValue = Form.useWatch("baseUrl", endpointForm);
  const loadedGroups = useRef(new Set<string>());
  const providers = useMemo(
    () =>
      props.providers.map(
        (provider): AddedProvider =>
          props.configuredProviders.find((item) => item.id === provider.id) || {
            ...provider,
            groups: [],
          },
      ),
    [props.providers, props.configuredProviders],
  );
  const provider =
    providers.find((item) => item.id === selectedId) || providers[0];
  // One private compatibility record belongs to each supplier.
  const group = provider?.groups[0];
  const groupKey = provider && group ? `${provider.id}:${group.id}` : "";
  const endpointDirty =
    Boolean(provider) &&
    (endpointValue ?? provider.baseUrl).trim() !== provider.baseUrl;
  const confirmEndpointLeave = useSettingsDraft({
    dirty: endpointDirty,
    saving: endpointSaving,
    discard: () =>
      endpointForm.setFieldsValue({ baseUrl: provider?.baseUrl || "" }),
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
      await updateProviderEndpoint(provider.id, baseUrl.trim());
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
  const addKey = async ({ apiKey }: { apiKey: string }) => {
    if (!provider || keySaving) return;
    setKeySaving(true);
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
        await modelProvidersApi.apiCoreModelProvidersModelProviderIdGroupsPost(
          {
            modelProviderId: provider.id,
            createModelProviderGroupOpenAPIRequest: {
              name: provider.name,
              api_key: apiKey.trim(),
              verify: true,
            },
          },
          { timeout: 180000 },
        );
      }
      keyDraft.acceptSaved();
      keyForm.resetFields();
      setKeyOpen(false);
      await props.onReload();
      message.success(label("keyAdded"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
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
    if (group) {
      action(provider, group);
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
        baseUrl: provider.baseUrl,
        apiKeyConfigured: false,
        verified: created.is_verified === true,
        models: [],
        keys: [],
      };
      await props.onReload();
      action({ ...provider, groups: [canonical] }, canonical);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      setInitializing(false);
    }
  };
  const visibleProviders = providers.filter(
    (item) =>
      (!configuredOnly || item.groups.length > 0) &&
      item.name.toLowerCase().includes(search.toLowerCase().trim()),
  );
  const models = (group?.models || []).filter(
    (model) =>
      model.name.toLowerCase().includes(modelSearch.toLowerCase().trim()) &&
      (modelType === "all" || model.capability === modelType),
  );
  const modelTypes = Array.from(
    new Set((group?.models || []).map((model) => model.capability)),
  );

  return (
    <section
      className={`provider-workspace${mobileDetail ? " is-mobile-detail" : ""}`}
      aria-label={label("title")}
    >
      {deleteModalContext}
      <header className="provider-workspace-topbar">
        <div>
          {props.onBack ? (
            <button
              type="button"
              className="provider-workspace-back"
              onClick={() => confirmEndpointLeave(props.onBack!)}
            >
              <ArrowLeftOutlined aria-hidden="true" />
              {label("back")}
            </button>
          ) : null}
          <h1>{label("title")}</h1>
        </div>
        <div>
          <Button
            icon={<CloudOutlined aria-hidden="true" />}
            onClick={() => setCloudOpen(true)}
          >
            {label("cloud")}
          </Button>
        </div>
      </header>
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
            {visibleProviders.map((item) => (
              <button
                type="button"
                key={item.id}
                className={`provider-workspace-provider${provider?.id === item.id ? " is-active" : ""}`}
                aria-current={provider?.id === item.id ? "page" : undefined}
                disabled={initializing || providerSaving || providerDeleting}
                onClick={() => changeProvider(item.id)}
              >
                <Logo provider={item} />
                <span>{item.name}</span>
                {item.groups.length > 0 ? (
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
            ))}
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
          <Button
            className="provider-workspace-add-supplier"
            icon={<PlusOutlined aria-hidden="true" />}
            onClick={() => openProviderModal("create")}
            disabled={initializing || providerSaving}
          >
            {label("addSupplier")}
          </Button>
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
                    onClick={() => setMobileDetail(false)}
                  />
                  <Logo provider={provider} />
                  <div>
                    <h2>{provider.name}</h2>
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
                  <span
                    className={`provider-workspace-status${provider.groups.some((candidate) => candidate.verified) ? " is-ready" : ""}`}
                  >
                    {provider.groups.some((candidate) => candidate.verified) ? (
                      <CheckCircleFilled aria-hidden="true" />
                    ) : (
                      <span className="provider-workspace-status-dot" />
                    )}
                    {provider.groups.some((candidate) => candidate.verified)
                      ? label("connected")
                      : label("notConnected")}
                  </span>
                </div>
              </header>
              <div className="provider-workspace-scroll">
                <Form
                  name="supplier-endpoint"
                  form={endpointForm}
                  layout="vertical"
                  onFinish={saveEndpoint}
                  className="provider-workspace-endpoint"
                >
                  <div className="provider-workspace-section-heading">
                    <h3>{label("endpoint")}</h3>
                  </div>
                  <div className="provider-workspace-endpoint-row">
                    <Form.Item
                      label="API Host"
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
                                !parsed.password
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
                        aria-label="API Host"
                        disabled={Boolean(changingActiveKey)}
                        maxLength={512}
                        spellCheck={false}
                        autoComplete="off"
                      />
                    </Form.Item>
                    <Button
                      htmlType="submit"
                      loading={endpointSaving}
                      disabled={!endpointDirty || Boolean(changingActiveKey)}
                    >
                      {label("saveEndpoint")}
                    </Button>
                  </div>
                  {endpointDirty ? <p>{label("endpointHint")}</p> : null}
                </Form>
                <section
                  className="provider-workspace-api-keys"
                  aria-label="API Key"
                >
                  <div className="provider-workspace-key-heading">
                    <h3>
                      API Key{" "}
                      <span className="provider-workspace-count">
                        {group?.keys.length || 0}
                      </span>
                    </h3>
                    <div className="provider-workspace-key-actions">
                      <Button
                        type="text"
                        onClick={() => void withProviderModels(props.onCheck)}
                        disabled={
                          endpointDirty ||
                          endpointSaving ||
                          Boolean(changingActiveKey)
                        }
                        loading={initializing || props.checking[groupKey]}
                      >
                        {label("checkConnection")}
                      </Button>
                      <Button
                        type="link"
                        icon={<PlusOutlined aria-hidden="true" />}
                        disabled={endpointDirty || Boolean(changingActiveKey)}
                        onClick={openAddKey}
                      >
                        {label("addKey")}
                      </Button>
                    </div>
                  </div>
                  <div className="provider-workspace-keys">
                    {group?.keys.length ? (
                      group.keys.map((key, index) => (
                        <div className="provider-workspace-key" key={key.id}>
                          <span>
                            {label("keyNumber", { number: index + 1 })}
                          </span>
                          <code>{key.masked}</code>
                          <span
                            className={`provider-workspace-key-state${key.enabled ? " is-enabled" : ""}`}
                          >
                            {label(key.enabled ? "keyEnabled" : "keyDisabled")}
                          </span>
                          <Switch
                            size="small"
                            aria-label={label("useKeyNumber", {
                              number: index + 1,
                            })}
                            checked={key.enabled === true}
                            loading={changingActiveKey === key.id}
                            disabled={
                              endpointDirty ||
                              endpointSaving ||
                              Boolean(changingActiveKey) ||
                              Boolean(removingKey) ||
                              keySaving
                            }
                            onChange={(enabled) =>
                              void changeActiveKey(key.id, enabled)
                            }
                          />
                          <Popconfirm
                            title={label("deleteKey")}
                            description={label("deleteKeyHint")}
                            okText={t("common.delete")}
                            cancelText={t("common.cancel")}
                            okButtonProps={{ danger: true }}
                            onConfirm={() => deleteKey(key.id)}
                          >
                            <Button
                              type="text"
                              aria-label={label("deleteKeyNumber", {
                                number: index + 1,
                              })}
                              icon={<DeleteOutlined aria-hidden="true" />}
                              loading={removingKey === key.id}
                              disabled={Boolean(changingActiveKey)}
                            />
                          </Popconfirm>
                        </div>
                      ))
                    ) : (
                      <p className="provider-workspace-key-empty">
                        {group?.apiKeyConfigured
                          ? label("keyStored")
                          : label("noKeys")}
                      </p>
                    )}
                  </div>
                </section>
                <section
                  className="provider-workspace-models"
                  aria-label={label("models")}
                >
                  <div className="provider-workspace-section-heading">
                    <h3>
                      {label("models")}{" "}
                      <span className="provider-workspace-count">
                        {group?.models.length || 0}
                      </span>
                    </h3>
                    <div>
                      <Tooltip title={label("refreshHint")}>
                        <Button
                          type="text"
                          aria-label={label("refreshModels")}
                          icon={<ReloadOutlined aria-hidden="true" />}
                          loading={props.modelsLoading[groupKey]}
                          disabled={initializing}
                          onClick={() =>
                            void withProviderModels(props.onLoadModels)
                          }
                        />
                      </Tooltip>
                      <Button
                        icon={<ReloadOutlined aria-hidden="true" />}
                        disabled={initializing || endpointDirty}
                        onClick={() =>
                          void withProviderModels(props.onSyncModels)
                        }
                      >
                        {label("syncModels")}
                      </Button>
                      <Button
                        icon={<PlusOutlined aria-hidden="true" />}
                        disabled={initializing || endpointDirty}
                        onClick={() =>
                          void withProviderModels(props.onAddModel)
                        }
                      >
                        {t("modelProvider.customModel")}
                      </Button>
                    </div>
                  </div>
                  <div className="provider-workspace-model-toolbar">
                    <Input
                      allowClear
                      prefix={<SearchOutlined aria-hidden="true" />}
                      aria-label={label("searchModel")}
                      placeholder={label("searchModel")}
                      value={modelSearch}
                      onChange={(event) => setModelSearch(event.target.value)}
                    />
                    <Select
                      aria-label={label("modelType")}
                      value={modelType}
                      onChange={setModelType}
                      options={[
                        { value: "all", label: label("allTypes") },
                        ...modelTypes.map((capability) => ({
                          value: capability,
                          label: props.capabilityLabel(capability),
                        })),
                      ]}
                    />
                  </div>
                  <div
                    className="provider-workspace-model-table"
                    role="table"
                    aria-label={label("models")}
                  >
                    <div
                      className="provider-workspace-model-row is-heading"
                      role="row"
                    >
                      <span role="columnheader">{label("modelId")}</span>
                      <span role="columnheader">{label("type")}</span>
                      <span role="columnheader">{label("modalities")}</span>
                      <span role="columnheader">{label("context")}</span>
                      <span role="columnheader">{label("actions")}</span>
                    </div>
                    {props.modelsLoading[groupKey] ? (
                      <div className="provider-workspace-empty">
                        <Spin size="small" />
                      </div>
                    ) : (
                      models.map((model) => (
                        <div
                          className="provider-workspace-model-row"
                          role="row"
                          key={model.id}
                        >
                          <span
                            role="cell"
                            className="provider-workspace-model-name"
                            title={model.name}
                          >
                            {model.name}
                            {!model.builtIn ? (
                              <Tag>{t("modelProvider.custom")}</Tag>
                            ) : null}
                          </span>
                          <span role="cell" className="provider-workspace-type">
                            {props.capabilityLabel(model.capability)}
                            {model.vision && model.capability === "LLM_CHAT" ? (
                              <span title={t("modelProvider.visionSupported")}>
                                {" "}
                                · V
                              </span>
                            ) : null}
                          </span>
                          <span role="cell">
                            <ModelModalityMarks
                              providerName={
                                provider.protocolName ||
                                provider.source ||
                                provider.name
                              }
                              modelName={model.name}
                              compact
                            />
                          </span>
                          <span
                            role="cell"
                            className="provider-workspace-context"
                          >
                            {isLlmChatCapability(model.capability)
                              ? resolveLlmMaxInputTokens(model.maxInputTokens)
                              : "—"}
                          </span>
                          <span
                            role="cell"
                            className="provider-workspace-model-actions"
                          >
                            <>
                              {!model.builtIn &&
                              isLlmChatCapability(model.capability) ? (
                                <Button
                                  type="text"
                                  aria-label={t(
                                    "modelProvider.editModelWindowAria",
                                    { name: model.name },
                                  )}
                                  icon={<EditOutlined aria-hidden="true" />}
                                  onClick={() =>
                                    group &&
                                    props.onEditModel(provider, group, model)
                                  }
                                />
                              ) : null}
                              <Popconfirm
                                title={t("modelProvider.confirmDeleteModel", {
                                  name: model.name,
                                })}
                                description={t(
                                  model.capability === "EMBEDDING"
                                    ? "modelProvider.confirmDeleteEmbeddingDesc"
                                    : "modelProvider.confirmDeleteModelDesc",
                                )}
                                okButtonProps={{ danger: true }}
                                okText={t("common.delete")}
                                cancelText={t("common.cancel")}
                                onConfirm={() =>
                                  group &&
                                  props.onDeleteModel(provider, group, model)
                                }
                              >
                                <Button
                                  type="text"
                                  aria-label={t(
                                    "modelProvider.deleteModelAria",
                                    { name: model.name },
                                  )}
                                  icon={<DeleteOutlined aria-hidden="true" />}
                                />
                              </Popconfirm>
                            </>
                          </span>
                        </div>
                      ))
                    )}
                    {!props.modelsLoading[groupKey] && !models.length ? (
                      <div className="provider-workspace-empty">
                        {t("modelProvider.noModels")}
                      </div>
                    ) : null}
                  </div>
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
      <Drawer
        title={label("cloud")}
        open={cloudOpen}
        width={460}
        onClose={() => setCloudOpen(false)}
      >
        {props.cloudContent}
      </Drawer>
      <Modal
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
              { max: 255, message: label("supplierNameMax") },
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
                    .map((item) => ({ value: item.id, label: item.name }))}
                  onChange={(id) =>
                    providerForm.setFieldValue(
                      "baseUrl",
                      providers.find((item) => item.id === id)?.baseUrl || "",
                    )
                  }
                />
              </Form.Item>
              <Form.Item
                label="API Host"
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
            label="API Key"
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
