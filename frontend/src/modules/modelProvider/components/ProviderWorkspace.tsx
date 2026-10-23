import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import {
  ArrowLeftOutlined,
  CheckCircleFilled,
  CloudOutlined,
  DeleteOutlined,
  DownOutlined,
  EditOutlined,
  InfoCircleOutlined,
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
  type InputRef,
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
import { getProviderBrandDisplayName, getProviderDisplayName, getProviderServiceDisplayName } from "../providerDisplayNames";
import { filterSupplierNavigation, hasProgrammingScopeNotice, programmingScopeLink, projectSupplierNavigation } from "../supplierNavigation";
import { getSupplierBrandKey } from "../supplierServices";
import { useSupplierOrder } from "../hooks/useSupplierOrder";
import { useFocusModality } from "../hooks/useFocusModality";
import { canDiscoverWithoutApiKey } from "../providerAuthentication";
import { getProviderConnectionFailureMessage } from "../connectionFailure";
import { SortableSupplierList } from "./SortableSupplierList";
import { ModelTaskTabs, matchesModelTaskFilter, type ModelTaskFilter } from "./ModelTaskTabs";
import { SavedProviderModels } from "./SavedProviderModels";
import { ProviderKeyManager } from "./ProviderKeyManager";
import { ProviderRequestConfigDrawer } from "./ProviderRequestConfigDrawer";
import { ProviderInformationDrawer } from "./ProviderInformationDrawer";
import { getProviderInformationCopy } from "../providerInformationCopy";
import { CustomLogoPicker } from "./CustomLogoPicker";
import { getCustomLogoCopy } from "../customLogoCopy";
import { isCustomLogoDataUrl } from "../customLogo";
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
  const fallback = getProviderLogoUrl(
    provider.brandKey || provider.name || provider.protocolName || provider.source,
    provider.isCustom,
  ) || "/provider-icons/custom.svg";
  const logo = isCustomLogoDataUrl(provider.logoDataUrl) ? provider.logoDataUrl : fallback;
  return (
    <span className="provider-workspace-logo" aria-hidden="true">
      {logo ? <img src={logo} alt="" onError={(event) => {
        if (event.currentTarget.getAttribute("src") !== fallback) event.currentTarget.src = fallback;
      }} /> : provider.name.slice(0, 2)}
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
  const [expandedBrands, setExpandedBrands] = useState<Set<string>>(new Set());
  const navigationId = useId();
  const [search, setSearch] = useState("");
  const [modelSearch, setModelSearch] = useState("");
  const [modelType, setModelType] = useState<ModelTaskFilter>("all");
  const [configuredOnly, setConfiguredOnly] = useState(false);
  const [cloudOpen, setCloudOpen] = useState(false);
  const [requestConfigOpen, setRequestConfigOpen] = useState(false);
  const [requestConfigRevision, setRequestConfigRevision] = useState(0);
  const [informationOpen, setInformationOpen] = useState(false);
  const informationTrigger = useRef<HTMLButtonElement>(null);
  const [mobileDetail, setMobileDetail] = useState(false);
  const supplierSearch = useRef<InputRef>(null);
  const supplierSidebar = useRef<HTMLElement>(null);
  const supplierDetail = useRef<HTMLElement>(null);
  const supplierButtons = useRef(new Map<string, HTMLButtonElement>());
  const supplierReturnTarget = useRef<HTMLButtonElement | null>(null);
  const navigationFocus = useRef<"detail" | "list" | null>(null);
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
  const providerSavePending = useRef(false);
  const [providerLogoBusy, setProviderLogoBusy] = useState(false);
  const providerLogoBusyRef = useRef(false);
  const providerModalGeneration = useRef(0);
  const logoGeneration = providerModalGeneration.current;
  const providerModalTarget = useRef<AddedProvider>();
  const [providerDeleting, setProviderDeleting] = useState(false);
  const [deleteModal, deleteModalContext] = Modal.useModal();
  const [initializing, setInitializing] = useState(false);
  const [providerForm] = Form.useForm<{
    name: string;
    sourceProviderId: string;
    baseUrl: string;
    logoDataUrl?: string;
  }>();
  const providerDraft = useSettingsFormDraft(
    providerForm,
    Boolean(providerModal),
    providerSaving || providerLogoBusy,
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
  const projectedNavigation = useMemo(() => projectSupplierNavigation(mergedProviders), [mergedProviders]);
  const { providers: navigation, reorder: reorderSuppliers } = useSupplierOrder(projectedNavigation);
  const providers = useMemo(() => navigation.flatMap((node) => node.children), [navigation]);
  const provider =
    providers.find((item) => item.id === selectedId) || providers[0];
  const selectedAncestorId = navigation.find((node) => node.isBrandGroup && node.children.some((child) => child.id === provider?.id))?.id;
  const activeSupplierId = useRef<string | undefined>(provider?.id);
  activeSupplierId.current = provider?.id;
  useEffect(() => {
    activeSupplierId.current = provider?.id;
    return () => { activeSupplierId.current = undefined; };
  }, [provider?.id]);
  const providerDisplayName = provider
    ? getProviderDisplayName(provider, language)
    : "";
  const informationCopy = getProviderInformationCopy(language);
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
    if (selectedAncestorId) setExpandedBrands((current) => current.has(selectedAncestorId) ? current : new Set(current).add(selectedAncestorId));
  }, [provider?.id, selectedAncestorId]);
  useEffect(() => {
    setInformationOpen(false);
  }, [provider?.id]);
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

  useEffect(() => {
    const narrowViewport = window.matchMedia("(max-width: 800px)");
    const focusList = () => {
      const target = [supplierReturnTarget.current, supplierButtons.current.get(provider?.id || ""),
        supplierButtons.current.get(selectedAncestorId || "")].find((button) => button?.isConnected && !button.disabled && !button.closest("[hidden]"));
      if (target) target.focus({ preventScroll: true });
      else supplierSearch.current?.focus({ preventScroll: true });
    };
    const focusDetail = () => informationTrigger.current?.focus({ preventScroll: true });
    const destination = navigationFocus.current;
    if (destination && (destination === "detail") === mobileDetail) {
      navigationFocus.current = null;
      // Desktop keeps both panes visible, so retain the initiating control's focus.
      if (narrowViewport.matches) {
        if (destination === "detail") focusDetail(); else focusList();
      }
    }
    const onBreakpointChange = (event: MediaQueryListEvent) => {
      if (!event.matches) return;
      const focused = document.activeElement;
      if (mobileDetail && supplierSidebar.current?.contains(focused)) focusDetail();
      else if (!mobileDetail && supplierDetail.current?.contains(focused)) focusList();
    };
    narrowViewport.addEventListener("change", onBreakpointChange);
    return () => narrowViewport.removeEventListener("change", onBreakpointChange);
  }, [mobileDetail, provider?.id, selectedAncestorId]);

  const selectProvider = (id: string, trigger?: HTMLButtonElement) => {
    supplierReturnTarget.current = trigger || supplierButtons.current.get(id) || null;
    navigationFocus.current = "detail";
    setSelectedId(id);
    setModelSearch("");
    setModelType("all");
    setMobileDetail(true);
  };
  const changeProvider = (id: string, trigger: HTMLButtonElement) =>
    confirmEndpointLeave(() => selectProvider(id, trigger));
  const returnToSuppliers = () => confirmEndpointLeave(() => {
    navigationFocus.current = "list";
    setMobileDetail(false);
  });
  const configureInformationProvider = (id: string) => {
    if (id === provider?.id) {
      setInformationOpen(false);
      return;
    }
    if (!providers.some((item) => item.id === id)) return;
    confirmEndpointLeave(() => {
      setInformationOpen(false);
      selectProvider(id);
    });
  };
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
  const resetProviderModal = () => {
    providerModalGeneration.current += 1;
    providerLogoBusyRef.current = false;
    setProviderLogoBusy(false);
    providerModalTarget.current = undefined;
    setProviderModal(null);
    providerForm.resetFields();
  };
  const openProviderModal = (mode: "create" | "edit") => {
    if (providerSavePending.current || providerLogoBusyRef.current) return;
    confirmEndpointLeave(() => {
      providerModalGeneration.current += 1;
      providerLogoBusyRef.current = false;
      setProviderLogoBusy(false);
      providerModalTarget.current = mode === "edit" ? provider : undefined;
      const source =
        providers.find(
          (item) => !item.isCustom && getSupplierBrandKey(item) === "OpenAI",
        ) ||
        providers.find((item) => !item.isCustom) ||
        provider;
      providerForm.resetFields();
      providerForm.setFieldsValue({
        name: mode === "edit" ? provider?.name || "" : "",
        sourceProviderId: source?.id || "",
        baseUrl: source?.baseUrl || "",
        logoDataUrl: mode === "edit" ? provider?.logoDataUrl || undefined : undefined,
      });
      setProviderModal(mode);
      providerDraft.captureSavedValues();
    });
  };
  const saveProvider = async (values: {
    name: string;
    sourceProviderId: string;
    baseUrl: string;
    logoDataUrl?: string;
  }) => {
    if (providerSavePending.current || providerLogoBusyRef.current || !providerModal) return;
    const target = providerModalTarget.current;
    providerSavePending.current = true;
    setProviderSaving(true);
    try {
      let nextId = target?.id;
      if (providerModal === "create") {
        const created = await createCustomProvider({
          name: values.name.trim(),
          source_provider_id: values.sourceProviderId,
          base_url: values.baseUrl.trim(),
          ...(values.logoDataUrl ? { logo_data_url: values.logoDataUrl } : {}),
        });
        nextId = created.id;
      } else if (target) {
        if ((values.logoDataUrl || "") !== (target.logoDataUrl || "")) {
          await updateCustomProviderName(target.id, values.name.trim(), values.logoDataUrl || "");
        } else {
          await updateCustomProviderName(target.id, values.name.trim());
        }
      }
      providerDraft.acceptSaved();
      resetProviderModal();
      await props.onReload();
      if (nextId) {
        setSelectedId(nextId);
        setMobileDetail(true);
      }
      message.success(label("supplierSaved"));
    } catch (error) {
      message.error(getLocalizedErrorMessage(error));
    } finally {
      providerSavePending.current = false;
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
  const visibleNavigation = filterSupplierNavigation(navigation, search, configuredOnly, hasProviderConfiguration, language);
  const configuredCount = filterSupplierNavigation(navigation, search, true, hasProviderConfiguration, language).length;
  const statusMark = (items: AddedProvider[], includeModels = false) => items.some((item) => includeModels ? hasProviderConfiguration(item) : item.groups.some((candidate) => candidate.apiKeyConfigured || candidate.keys.length > 0)) ? (
    <i className={items.some((item) => item.groups.some((candidate) => candidate.verified &&
      (candidate.keys.some((key) => key.enabled) || (!candidate.keys.length && candidate.apiKeyConfigured) || canDiscoverWithoutApiKey(item, candidate)))) ? "is-ready" : ""}
      aria-label={label("configured")} />
  ) : null;
  const leafButton = (item: AddedProvider, nested = false) => (
    <button type="button" key={item.id} data-provider-id={item.id}
      ref={(button) => { if (button) supplierButtons.current.set(item.id, button); else supplierButtons.current.delete(item.id); }}
      aria-label={nested ? getProviderServiceDisplayName(item, language) : getProviderDisplayName(item, language)}
      className={`provider-workspace-provider${nested ? " is-service" : ""}${provider?.id === item.id ? " is-active" : ""}`}
      aria-current={provider?.id === item.id ? "page" : undefined}
      disabled={initializing || providerSaving || providerDeleting}
      onClick={(event) => changeProvider(item.id, event.currentTarget)} title={getProviderDisplayName(item, language)}>
      {!nested ? <Logo provider={item} /> : null}
      <span className="provider-workspace-provider-name">{nested ? getProviderServiceDisplayName(item, language) : getProviderDisplayName(item, language)}</span>
      {statusMark([item])}
    </button>
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
          ref={supplierSidebar}
          className="provider-workspace-sidebar"
          aria-label={label("suppliers")}
        >
          <Input
            ref={supplierSearch}
            allowClear
            prefix={<SearchOutlined aria-hidden="true" />}
            aria-label={t("modelProvider.searchAria")}
            placeholder={label("searchSupplier")}
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
          <div className="provider-workspace-filters">
            <button
              aria-label={label("all")}
              aria-pressed={!configuredOnly}
              className={!configuredOnly ? "is-active" : ""}
              type="button"
              onClick={() => setConfiguredOnly(false)}
            >
              {label("all")} <span>{navigation.length}</span>
            </button>
            <button
              aria-label={label("configured")}
              aria-pressed={configuredOnly}
              className={configuredOnly ? "is-active" : ""}
              type="button"
              onClick={() => setConfiguredOnly(true)}
            >
              {label("configured")} <span>{configuredCount}</span>
            </button>
          </div>
          <nav
            className="provider-workspace-provider-list"
            aria-label={label("suppliers")}
          >
            <SortableSupplierList items={visibleNavigation}
              disabled={initializing || providerSaving || providerDeleting}
              onReorder={reorderSuppliers}>
            {(node) => {
              if (!node.isBrandGroup) return leafButton(node.children[0]);
              const expanded = Boolean(search.trim()) || expandedBrands.has(node.id);
              const listId = `${navigationId}-${node.id}`;
              return <>
                <button type="button" className="provider-workspace-provider is-brand"
                  ref={(button) => { if (button) supplierButtons.current.set(node.id, button); else supplierButtons.current.delete(node.id); }}
                  aria-label={getProviderBrandDisplayName(node, language)} aria-expanded={expanded} aria-controls={listId}
                  disabled={initializing || providerSaving || providerDeleting}
                  title={getProviderBrandDisplayName(node, language)}
                  onClick={() => setExpandedBrands((current) => {
                    const next = new Set(current);
                    if (next.has(node.id)) next.delete(node.id); else next.add(node.id);
                    return next;
                  })}>
                  <Logo provider={node.children[0]} />
                  <span className="provider-workspace-provider-name">{getProviderBrandDisplayName(node, language)}</span>
                  {statusMark(node.children, true)}
                  <DownOutlined className={`provider-workspace-brand-arrow${expanded ? " is-expanded" : ""}`} aria-hidden="true" />
                </button>
                <ul id={listId} className="provider-workspace-services" hidden={!expanded}>
                  {node.children.map((child) => <li key={child.id}>{leafButton(child, true)}</li>)}
                </ul>
              </>;
            }}
            </SortableSupplierList>
            {!visibleNavigation.length ? (
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
        <main ref={supplierDetail} className="provider-workspace-detail">
          {provider ? (
            <>
              <header className="provider-workspace-supplier-header">
                <div className="provider-workspace-supplier-title">
                  <Button
                    type="text"
                    className="provider-workspace-mobile-back"
                    aria-label={label("suppliers")}
                    icon={<ArrowLeftOutlined aria-hidden="true" />}
                    onClick={returnToSuppliers}
                  />
                  <Logo provider={provider} />
                  <div className="provider-workspace-supplier-identity">
                    <h2 title={providerDisplayName}>
                      <button
                        ref={informationTrigger}
                        type="button"
                        className="provider-workspace-information-trigger"
                        title={informationCopy.open}
                        aria-haspopup="dialog"
                        aria-expanded={informationOpen}
                        onClick={() => setInformationOpen(true)}
                      >
                        <span>{providerDisplayName}</span>
                        <InfoCircleOutlined aria-hidden="true" />
                      </button>
                    </h2>
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
                {hasProgrammingScopeNotice(provider) ? <Alert
                  className="provider-workspace-service-scope" type="info" showIcon
                  message={<span>{label(getSupplierBrandKey(provider) === "GLM" ? "serviceScopeGlm" : getSupplierBrandKey(provider) === "Qwen" ? "serviceScopeQwen" : "serviceScopeGeneric")} {" "}
                    <a href={programmingScopeLink(provider)} target="_blank" rel="noopener noreferrer">{label("serviceScopeOfficialLink")}</a></span>}
                /> : null}
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
                    providerLogoDataUrl={provider.logoDataUrl}
                    probe={group ? {
                      providerId: provider.id,
                      groupId: group.id,
                      configurationKey: JSON.stringify([provider.protocolName || provider.source, endpointBaseline, group.baseUrl, group.keys.map((key) => [key.id, key.enabled]), requestConfigRevision]),
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
        <ProviderInformationDrawer
          open={informationOpen}
          provider={{ ...provider, baseUrl: endpointBaseline }}
          providers={providers.map((item) => item.id === provider.id ? { ...item, baseUrl: endpointBaseline } : item)}
          onClose={() => setInformationOpen(false)}
          onConfigure={configureInformationProvider}
          onAfterClose={() => informationTrigger.current?.focus({ preventScroll: true })}
        />
      ) : null}
      {provider ? (
        <ProviderRequestConfigDrawer
          providerId={provider.id}
          providerName={providerDisplayName}
          open={requestConfigOpen}
          onClose={() => setRequestConfigOpen(false)}
          onSaved={async () => {
            await props.onReload();
            setRequestConfigRevision((current) => current + 1);
          }}
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
        closable={!providerSaving && !providerLogoBusy}
        maskClosable={!providerSaving && !providerLogoBusy}
        keyboard={!providerSaving && !providerLogoBusy}
        cancelButtonProps={{ disabled: providerSaving || providerLogoBusy }}
        okButtonProps={{ disabled: providerSaving || providerLogoBusy }}
        okText={t("common.save")}
        onCancel={() => {
          if (providerSavePending.current || providerLogoBusyRef.current) return;
          providerDraft.confirmClose(() => {
            resetProviderModal();
          });
        }}
        onOk={() => { if (!providerLogoBusyRef.current) providerForm.submit(); }}
        destroyOnHidden
      >
        <Form
          name="custom-supplier"
          form={providerForm}
          layout="vertical"
          onFinish={saveProvider}
          disabled={providerSaving || providerLogoBusy}
        >
          <Form.Item name="logoDataUrl" normalize={(value?: string) => value || undefined}>
            {providerModal ? <CustomLogoPicker key={logoGeneration} copy={getCustomLogoCopy(t)} disabled={providerSaving}
              onBusyChange={(busy) => {
                if (providerModalGeneration.current !== logoGeneration) return;
                providerLogoBusyRef.current = busy;
                setProviderLogoBusy(busy);
              }} /> : <span />}
          </Form.Item>
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
