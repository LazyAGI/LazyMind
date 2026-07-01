import { useEffect, useRef, useState } from "react";
import { Form } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { AgentAppsAuth } from "@/components/auth";
import type { TypedConfirmModalRef } from '@/components/ui/TypedConfirmModal';
import {
  createFeishuAccountId,
  getOAuthStateFromConnection,
  loadFeishuAppSetup,
  loadFeishuAuthAccounts,
  persistFeishuAuthAccounts,
  type FeishuAccountFormValues,
  type FeishuAuthAccount,
} from "../common/feishuAccounts";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  consumeCloudDataSourceOAuthResult,
  consumeFeishuDataSourceOAuthResult,
  consumeFeishuDataSourceWizardDraft,
  type CloudDataSourceProvider,
  type FeishuDataSourceConnection,
  type FeishuDataSourceOAuthMessage,
} from "@/modules/dataSource/common/feishuOAuth";
import { DEFAULT_DATA_SOURCE_FILE_TYPES } from "../constants/options";
import type {
  DataSourceItem,
  FeishuAppSetup,
  FeishuTargetType,
  OAuthState,
  PendingOAuthAttempt,
  SourceFormValues,
  SourceType,
} from "../constants/types";
import { type ScanV2AgentHint } from "../utils/scanAccessors";
import { pickScanAgent } from "../utils/cloudSync";
import { isAdminRole } from "../utils/role";
import { resolveSourceTypeFromValues } from "../utils/feishuTarget";
import { getSourceTypeTitle } from "../utils/status";
import { loadNotionAppSetup } from "../utils/notionSetup";
import { sourceTypeOptions } from "../constants/sourceTypeOptions";
import { useLocalPathTree } from "./useLocalPathTree";
import { useFeishuTargetTree } from "./useFeishuTargetTree";
import type {
  CloudSetupIntent,
  DataSourceSaveMode,
  DataSourceView,
  ManagementContext,
} from "./management/context";
import { createListActions } from "./management/createListActions";
import { createOAuthEngine } from "./management/createOAuthEngine";
import { createWizardSetup } from "./management/createWizardSetup";
import { createWizardFlow } from "./management/createWizardFlow";
import { createSaveActions } from "./management/createSaveActions";

const DATA_SOURCE_LIST_DEFAULT_PAGE_SIZE = 10;

export function useDataSourceManagement() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [form] = Form.useForm<SourceFormValues>();
  const [sources, setSources] = useState<DataSourceItem[]>([]);
  const [activeView, setActiveView] = useState<DataSourceView>(() =>
    new URLSearchParams(window.location.search).get("view") === "connectors"
      ? "connectors"
      : "assets",
  );
  const [assetSearchValue, setAssetSearchValue] = useState("");
  const [sourceListPage, setSourceListPage] = useState(1);
  const [sourceListPageSize, setSourceListPageSize] = useState(
    DATA_SOURCE_LIST_DEFAULT_PAGE_SIZE,
  );
  const [sourceListTotal, setSourceListTotal] = useState(0);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [wizardStep, setWizardStep] = useState(0);
  const [wizardMode, setWizardMode] = useState<"create" | "edit">("create");
  const [selectedType, setSelectedType] = useState<SourceType | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [createProviderModalOpen, setCreateProviderModalOpen] = useState(false);
  const [authSelectModalOpen, setAuthSelectModalOpen] = useState(false);
  const [oauthState, setOauthState] = useState<OAuthState>("pending");
  const [connectionVerified, setConnectionVerified] = useState(false);
  const [oauthConnection, setOauthConnection] =
    useState<FeishuDataSourceConnection | null>(null);
  const [feishuAuthAccounts, setFeishuAuthAccounts] = useState<
    FeishuAuthAccount[]
  >(() => loadFeishuAuthAccounts());
  const [editingFeishuAccountId, setEditingFeishuAccountId] = useState<
    string | null
  >(null);
  const [feishuAppSetup, setFeishuAppSetup] = useState<FeishuAppSetup | null>(
    () => loadFeishuAppSetup(),
  );
  const [notionAppSetup, setNotionAppSetup] = useState<FeishuAppSetup | null>(
    () => loadNotionAppSetup(),
  );
  const [notionOauthConnection, setNotionOauthConnection] =
    useState<FeishuDataSourceConnection | null>(null);
  const [cloudSetupProvider, setCloudSetupProvider] =
    useState<CloudDataSourceProvider>("feishu");
  const [feishuSetupModalOpen, setFeishuSetupModalOpen] = useState(false);
  const [feishuSetupIntent, setFeishuSetupIntent] =
    useState<CloudSetupIntent>(null);
  const [feishuSetupSubmitting, setFeishuSetupSubmitting] = useState(false);
  const [feishuSetupForm] = Form.useForm<FeishuAccountFormValues>();
  const [manualOauthModalOpen, setManualOauthModalOpen] = useState(false);
  const [manualOauthCallbackValue, setManualOauthCallbackValue] = useState("");
  const [manualOauthSubmitting, setManualOauthSubmitting] = useState(false);
  const oauthAttemptRef = useRef<PendingOAuthAttempt | null>(null);
  const canCreateLocalSource = isAdminRole(AgentAppsAuth.getUserInfo()?.role);
  const creatableSourceTypeOptions = sourceTypeOptions.filter(
    (item) => !item.adminOnly || canCreateLocalSource,
  );
  const scanAgents: ScanV2AgentHint[] = [];
  const [localScanChatEnabled, setLocalScanChatEnabled] = useState(false);
  const [localScanChatSaving, setLocalScanChatSaving] = useState(false);
  const [scanLoading, setScanLoading] = useState(false);
  const [validatedAgentId, setValidatedAgentId] = useState<string | null>(null);
  const [wizardSaving, setWizardSaving] = useState(false);
  const [wizardSavingMode, setWizardSavingMode] = useState<DataSourceSaveMode | null>(null);
  const sourceListRequestSeqRef = useRef(0);
  const assetSearchInitializedRef = useRef(false);
  const feishuAuthAccountsLoadedRef = useRef(false);
  const confirmRef = useRef<TypedConfirmModalRef>(null);
  const pendingConfirmActionRef = useRef<(() => void | Promise<void>) | null>(null);

  const syncMode = Form.useWatch("syncMode", form) || "scheduled";
  const feishuTargetType = (Form.useWatch("targetType", form) || "wiki_space") as FeishuTargetType;
  const isFeishuSetupReady = Boolean(
    feishuAppSetup?.appId.trim() && feishuAppSetup?.appSecret.trim(),
  );
  const isNotionSetupReady = Boolean(
    notionAppSetup?.appId.trim() && notionAppSetup?.appSecret.trim(),
  );
  const validFeishuAccounts = feishuAuthAccounts.filter(
    (account) =>
      account.status === "connected" && Boolean(account.connection?.connectionId),
  );
  const isFeishuAuthValid = validFeishuAccounts.length > 0;
  const isNotionAuthValid =
    notionOauthConnection?.status === "connected" &&
    Boolean(notionOauthConnection.connectionId);
  const localSourceCount = sources.filter((item) => item.type === "local").length;

  const getPreferredLocalAgentId = () => {
    const currentLocalSource =
      editingId && selectedType === "local"
        ? sources.find((item) => item.id === editingId && item.type === "local")
        : undefined;
    const selectedAgent = pickScanAgent(
      scanAgents,
      validatedAgentId || currentLocalSource?.agentId,
    );

    return selectedAgent?.agent_id || validatedAgentId || currentLocalSource?.agentId || "";
  };

  const {
    localPathOptions,
    localPathLoading,
    loadLocalPathOptions,
    handleSearchLocalPathOptions,
    handleLoadLocalPathChildren,
    resetLocalPathBrowseOptions,
  } = useLocalPathTree({ t, form, getPreferredLocalAgentId });

  const getActiveFeishuAuthConnectionId = () => {
    if (oauthConnection?.connectionId) {
      return oauthConnection.connectionId;
    }
    if (wizardMode === "edit" && editingId) {
      return sources.find((item) => item.id === editingId && item.type === "feishu")
        ?.authConnectionId || "";
    }
    return "";
  };

  const {
    feishuTargetTreeData,
    feishuTargetLoading,
    loadFeishuTargetOptions,
    handleSearchFeishuTargetOptions,
    handleLoadFeishuTargetChildren,
    resetFeishuTargetBrowseOptions,
  } = useFeishuTargetTree({ t, feishuTargetType, getActiveFeishuAuthConnectionId });

  // Build the shared context once per render with all state, setters and refs,
  // then progressively layer the handler factories on top of it.
  const ctx = {} as ManagementContext;
  Object.assign(ctx, {
    t,
    navigate,
    form,
    feishuSetupForm,
    sources,
    setSources,
    activeView,
    setActiveView,
    assetSearchValue,
    setAssetSearchValue,
    sourceListPage,
    setSourceListPage,
    sourceListPageSize,
    setSourceListPageSize,
    sourceListTotal,
    setSourceListTotal,
    scanLoading,
    setScanLoading,
    wizardOpen,
    setWizardOpen,
    wizardStep,
    setWizardStep,
    wizardMode,
    setWizardMode,
    selectedType,
    setSelectedType,
    editingId,
    setEditingId,
    wizardSaving,
    setWizardSaving,
    wizardSavingMode,
    setWizardSavingMode,
    createProviderModalOpen,
    setCreateProviderModalOpen,
    authSelectModalOpen,
    setAuthSelectModalOpen,
    cloudSetupProvider,
    setCloudSetupProvider,
    feishuSetupModalOpen,
    setFeishuSetupModalOpen,
    feishuSetupIntent,
    setFeishuSetupIntent,
    feishuSetupSubmitting,
    setFeishuSetupSubmitting,
    manualOauthModalOpen,
    setManualOauthModalOpen,
    manualOauthCallbackValue,
    setManualOauthCallbackValue,
    manualOauthSubmitting,
    setManualOauthSubmitting,
    oauthState,
    setOauthState,
    connectionVerified,
    setConnectionVerified,
    oauthConnection,
    setOauthConnection,
    notionOauthConnection,
    setNotionOauthConnection,
    feishuAuthAccounts,
    setFeishuAuthAccounts,
    editingFeishuAccountId,
    setEditingFeishuAccountId,
    feishuAppSetup,
    setFeishuAppSetup,
    notionAppSetup,
    setNotionAppSetup,
    oauthAttemptRef,
    localScanChatEnabled,
    setLocalScanChatEnabled,
    localScanChatSaving,
    setLocalScanChatSaving,
    validatedAgentId,
    setValidatedAgentId,
    canCreateLocalSource,
    scanAgents,
    isFeishuSetupReady,
    isNotionSetupReady,
    isFeishuAuthValid,
    isNotionAuthValid,
    sourceListRequestSeqRef,
    feishuAuthAccountsLoadedRef,
    feishuTargetTreeData,
    resetLocalPathBrowseOptions,
    resetFeishuTargetBrowseOptions,
  });
  Object.assign(ctx, createListActions(ctx));
  Object.assign(ctx, createOAuthEngine(ctx));
  Object.assign(ctx, createWizardSetup(ctx));
  Object.assign(ctx, createWizardFlow(ctx));
  Object.assign(ctx, createSaveActions(ctx));

  useEffect(() => {
    const draft = consumeFeishuDataSourceWizardDraft();
    if (draft) {
      const normalizedWizardStep = Math.min(Math.max(draft.wizardStep, 0), 1);
      if (draft.activeView) {
        setActiveView(draft.activeView);
      }
      setAuthSelectModalOpen(Boolean(draft.authSelectModalOpen));
      setWizardMode(draft.wizardMode);
      setWizardOpen(draft.wizardOpen);
      setWizardStep(normalizedWizardStep);
      setSelectedType((draft.selectedType as SourceType | null) || null);
      setEditingId(draft.editingId);
      setValidatedAgentId(draft.validatedAgentId || null);
      setOauthState((draft.oauthState as OAuthState) || "pending");
      setConnectionVerified(Boolean(draft.connectionVerified));
      setOauthConnection(draft.oauthConnection || null);
      window.setTimeout(() => {
        form.setFieldsValue({
          fileTypes: DEFAULT_DATA_SOURCE_FILE_TYPES,
          ...draft.formValues,
        });
      }, 0);
    }

    if (feishuAuthAccounts.length === 0 && feishuAppSetup) {
      const seededAccounts: FeishuAuthAccount[] = [
        {
          id: createFeishuAccountId(),
          name: feishuAppSetup.appId,
          appId: feishuAppSetup.appId,
          appSecret: feishuAppSetup.appSecret,
          chatEnabled: false,
          status: getOAuthStateFromConnection(oauthConnection),
          connection: oauthConnection,
          createdAt: new Date().toISOString(),
        },
      ];
      setFeishuAuthAccounts(seededAccounts);
      persistFeishuAuthAccounts(seededAccounts);
    }

    const storedResult = consumeFeishuDataSourceOAuthResult();
    if (storedResult) {
      window.setTimeout(() => {
        ctx.applyOauthResult(storedResult);
      }, 0);
    }

    const storedNotionResult = consumeCloudDataSourceOAuthResult("notion");
    if (storedNotionResult) {
      window.setTimeout(() => {
        ctx.applyOauthResult(storedNotionResult);
      }, 0);
    }

    const handleMessage = (event: MessageEvent<FeishuDataSourceOAuthMessage>) => {
      if (event.origin !== window.location.origin) {
        return;
      }
      if (!event.data || event.data.channel !== FEISHU_DATA_SOURCE_OAUTH_CHANNEL) {
        return;
      }
      ctx.applyOauthResult(event.data);
    };

    window.addEventListener("message", handleMessage);

    return () => {
      window.removeEventListener("message", handleMessage);
      ctx.clearOauthAttempt();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form]);

  useEffect(() => {
    void ctx.refreshSources(false);
    void ctx.refreshFeishuAuthAccounts();
    void ctx.refreshNotionAuthConnection();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (activeView !== "connectors" || feishuAuthAccountsLoadedRef.current) {
      return;
    }
    void ctx.refreshFeishuAuthAccounts();
    void ctx.refreshNotionAuthConnection();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeView]);

  useEffect(() => {
    if (!assetSearchInitializedRef.current) {
      assetSearchInitializedRef.current = true;
      return;
    }

    const timer = window.setTimeout(() => {
      void ctx.refreshSources(false, {
        page: 1,
        pageSize: sourceListPageSize,
        keyword: assetSearchValue,
      });
    }, 300);

    return () => {
      window.clearTimeout(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [assetSearchValue]);

  const handleTypedConfirm = (_id: string) => {
    const action = pendingConfirmActionRef.current;
    pendingConfirmActionRef.current = null;
    if (action) {
      void action();
    }
  };

  const requestDeleteSourceConfirm = (record: DataSourceItem) => {
    pendingConfirmActionRef.current = () => ctx.executeDeleteSource(record);
    confirmRef.current?.onOpen({
      id: record.id,
      title: t("admin.dataSourceDeleteTitle", { name: record.name }),
      content: t("admin.dataSourceDeleteContent"),
      confirmText: t("admin.dataSourceDeleteConfirmText", { name: record.name }),
    });
  };

  const requestSaveWithSyncConfirm = (mode: DataSourceSaveMode) => {
    if (mode !== "createAndSync") {
      void ctx.handleSave(mode);
      return;
    }

    const values = form.getFieldsValue(true);
    const effectiveSourceType = resolveSourceTypeFromValues(selectedType, values);
    const fallbackType = effectiveSourceType || selectedType || "local";
    const kbName = `${values.knowledgeBase || getSourceTypeTitle(fallbackType, t)}`.trim();
    const isEditMode = wizardMode === "edit";

    pendingConfirmActionRef.current = () => ctx.handleSave(mode);
    confirmRef.current?.onOpen({
      id: mode,
      title: t(
        isEditMode ? "admin.dataSourceSaveSyncTitle" : "admin.dataSourceCreateSyncTitle",
        { name: kbName },
      ),
      content: t(
        isEditMode ? "admin.dataSourceSaveSyncContent" : "admin.dataSourceCreateSyncContent",
      ),
      confirmText: t(
        isEditMode ? "admin.dataSourceSaveSyncConfirmText" : "admin.dataSourceCreateSyncConfirmText",
        { name: kbName },
      ),
    });
  };

  return {
    t,
    form,
    feishuSetupForm,
    sources,
    activeView,
    setActiveView,
    assetSearchValue,
    setAssetSearchValue,
    sourceListPage,
    sourceListPageSize,
    sourceListTotal,
    scanLoading,
    refreshSources: ctx.refreshSources,
    wizardOpen,
    wizardStep,
    setWizardStep,
    wizardMode,
    selectedType,
    syncMode,
    wizardSaving,
    wizardSavingMode,
    createProviderModalOpen,
    setCreateProviderModalOpen,
    authSelectModalOpen,
    setAuthSelectModalOpen,
    manualOauthModalOpen,
    setManualOauthModalOpen,
    manualOauthCallbackValue,
    setManualOauthCallbackValue,
    manualOauthSubmitting,
    cloudSetupProvider,
    feishuSetupModalOpen,
    setFeishuSetupModalOpen,
    feishuSetupIntent,
    setFeishuSetupIntent,
    feishuSetupSubmitting,
    oauthConnection,
    notionOauthConnection,
    canCreateLocalSource,
    creatableSourceTypeOptions,
    localScanChatEnabled,
    localScanChatSaving,
    localSourceCount,
    isFeishuSetupReady,
    isNotionSetupReady,
    isFeishuAuthValid,
    isNotionAuthValid,
    validFeishuAccounts,
    localPathOptions,
    localPathLoading,
    loadLocalPathOptions,
    handleSearchLocalPathOptions,
    handleLoadLocalPathChildren,
    resetLocalPathBrowseOptions,
    feishuTargetTreeData,
    feishuTargetLoading,
    loadFeishuTargetOptions,
    handleSearchFeishuTargetOptions,
    handleLoadFeishuTargetChildren,
    resetFeishuTargetBrowseOptions,
    handleToggleLocalScanChat: ctx.handleToggleLocalScanChat,
    handleManageFeishuAuth: ctx.handleManageFeishuAuth,
    openSourceCreateWizard: ctx.openSourceCreateWizard,
    openCloudSetupModal: ctx.openCloudSetupModal,
    handleCreateProviderSelect: ctx.handleCreateProviderSelect,
    handleOpenFeishuGuideFromAuthSelect: ctx.handleOpenFeishuGuideFromAuthSelect,
    handleSelectFeishuAuthConnection: ctx.handleSelectFeishuAuthConnection,
    handleSubmitManualOauthCallback: ctx.handleSubmitManualOauthCallback,
    handleSaveFeishuSetup: ctx.handleSaveFeishuSetup,
    handleCloseWizard: ctx.handleCloseWizard,
    handleNextStep: ctx.handleNextStep,
    handleSave: ctx.handleSave,
    handleSelectType: ctx.handleSelectType,
    handleResetFeishuSetup: ctx.handleResetFeishuSetup,
    handleResetNotionSetup: ctx.handleResetNotionSetup,
    openDetailPage: ctx.openDetailPage,
    openEditWizard: ctx.openEditWizard,
    requestDeleteSourceConfirm,
    requestSaveWithSyncConfirm,
    confirmRef,
    handleTypedConfirm,
  };
}

export type DataSourceManagementVm = ReturnType<typeof useDataSourceManagement>;
