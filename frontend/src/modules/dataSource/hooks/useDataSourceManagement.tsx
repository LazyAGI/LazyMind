import { useEffect, useRef, useState } from "react";
import { Form } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { AgentAppsAuth } from "@/components/auth";
import type { TypedConfirmModalRef } from '@/components/ui/TypedConfirmModal';
import type { DatabaseConnectionItem } from "../api/databaseConnections";
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
  bootstrapOAuthSession,
  type CloudDataSourceProvider,
  type FeishuDataSourceConnection,
  type FeishuDataSourceOAuthMessage,
} from "@/modules/dataSource/common/feishuOAuth";
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
import { loadNotionAppSetup } from "../utils/notionSetup";
import { sourceTypeOptions } from "../constants/sourceTypeOptions";
import { useLocalPathTree } from "./useLocalPathTree";
import { useFeishuTargetTree } from "./useFeishuTargetTree";
import type {
  CloudSetupIntent,
  DataSourceSaveMode,
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
  const [authSelectProvider, setAuthSelectProvider] =
    useState<CloudDataSourceProvider | null>(null);
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
  const [notionAuthAccounts, setNotionAuthAccounts] = useState<FeishuAuthAccount[]>([]);
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
  const [databaseEditingConnection, setDatabaseEditingConnection] = useState<DatabaseConnectionItem | null>(null);
  const [databaseEditSaving, setDatabaseEditSaving] = useState(false);
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
  const validNotionAccounts = notionAuthAccounts.filter(
    (account) =>
      account.status === "connected" && Boolean(account.connection?.connectionId),
  );
  const isFeishuAuthValid = validFeishuAccounts.length > 0;
  const isNotionAuthValid = validNotionAccounts.length > 0;

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
    seedFeishuTargetTree,
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
    activeView: "assets" as const,
    setActiveView: () => undefined,
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
    authSelectProvider,
    setAuthSelectProvider,
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
    databaseEditingConnection,
    setDatabaseEditingConnection,
    databaseEditSaving,
    setDatabaseEditSaving,
    oauthState,
    setOauthState,
    connectionVerified,
    setConnectionVerified,
    oauthConnection,
    setOauthConnection,
    notionOauthConnection,
    setNotionOauthConnection,
    notionAuthAccounts,
    setNotionAuthAccounts,
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
    seedFeishuTargetTree,
  });
  Object.assign(ctx, createListActions(ctx));
  Object.assign(ctx, createOAuthEngine(ctx));
  Object.assign(ctx, createWizardSetup(ctx));
  Object.assign(ctx, createWizardFlow(ctx));
  Object.assign(ctx, createSaveActions(ctx));

  useEffect(() => {
    bootstrapOAuthSession({
      form,
      setAuthSelectModalOpen,
      setAuthSelectProvider,
      setWizardMode,
      setWizardOpen,
      setWizardStep,
      setSelectedType,
      setEditingId,
      setValidatedAgentId,
      setOauthState,
      setConnectionVerified,
      setOauthConnection,
      applyOauthResult: (payload, options) => {
        ctx.applyOauthResult(payload, options);
      },
      reopenCloudSetupModal: (type) => {
        if (type === "feishu" || type === "notion") {
          ctx.openCloudSetupModal(type, "create");
        }
      },
    });

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
    void ctx.refreshNotionAuthAccounts();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
    const isDatabase = record.type === "database";
    pendingConfirmActionRef.current = () => (
      isDatabase
        ? ctx.executeDeleteDatabaseConnection(record)
        : ctx.executeDeleteSource(record)
    );
    confirmRef.current?.onOpen({
      id: record.id,
      title: t(
        isDatabase ? "admin.dataSourceDatabaseDeleteTitle" : "admin.dataSourceDeleteTitle",
        { name: record.name },
      ),
      content: t(
        isDatabase ? "admin.dataSourceDatabaseDeleteContent" : "admin.dataSourceDeleteContent",
        { name: record.name },
      ),
      confirmText: t(
        isDatabase ? "common.delete" : "admin.dataSourceDeleteConfirmText",
        { name: record.name },
      ),
    });
  };

  const requestSaveWithSyncConfirm = (mode: DataSourceSaveMode) => {
    void ctx.handleSave(mode);
  };

  return {
    t,
    navigate,
    form,
    feishuSetupForm,
    sources,
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
    authSelectProvider,
    manualOauthModalOpen,
    setManualOauthModalOpen,
    manualOauthCallbackValue,
    setManualOauthCallbackValue,
    manualOauthSubmitting,
    databaseEditingConnection,
    databaseEditSaving,
    cloudSetupProvider,
    feishuSetupModalOpen,
    setFeishuSetupModalOpen,
    feishuSetupIntent,
    setFeishuSetupIntent,
    feishuSetupSubmitting,
    notionOauthConnection,
    canCreateLocalSource,
    creatableSourceTypeOptions,
    isFeishuSetupReady,
    isNotionSetupReady,
    isFeishuAuthValid,
    isNotionAuthValid,
    validFeishuAccounts,
    validNotionAccounts,
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
    openSourceCreateWizard: ctx.openSourceCreateWizard,
    handleCreateProviderSelect: ctx.handleCreateProviderSelect,
    handleOpenFeishuGuideFromAuthSelect: ctx.handleOpenFeishuGuideFromAuthSelect,
    handleAddFeishuAuthFromSelect: ctx.handleAddFeishuAuthFromSelect,
    handleAddNotionAuthFromSelect: ctx.handleAddNotionAuthFromSelect,
    handleSelectFeishuAuthConnection: ctx.handleSelectFeishuAuthConnection,
    handleSelectNotionAuthConnection: ctx.handleSelectNotionAuthConnection,
    handleOpenNotionGuideFromAuthSelect: ctx.handleOpenNotionGuideFromAuthSelect,
    handleSubmitManualOauthCallback: ctx.handleSubmitManualOauthCallback,
    handleCloseWizard: ctx.handleCloseWizard,
    handleNextStep: ctx.handleNextStep,
    handleSave: ctx.handleSave,
    handleSelectType: ctx.handleSelectType,
    handleResetFeishuSetup: ctx.handleResetFeishuSetup,
    handleResetNotionSetup: ctx.handleResetNotionSetup,
    handleSaveFeishuSetup: ctx.handleSaveFeishuSetup,
    handleCancelCloudSetup: ctx.handleCancelCloudSetup,
    openDetailPage: ctx.openDetailPage,
    openDatabaseConnectionConfig: ctx.openDatabaseConnectionConfig,
    closeDatabaseConnectionConfig: ctx.closeDatabaseConnectionConfig,
    handleSaveDatabaseConnectionConfig: ctx.handleSaveDatabaseConnectionConfig,
    openEditWizard: ctx.openEditWizard,
    requestDeleteSourceConfirm,
    executeDeleteDatabaseConnection: ctx.executeDeleteDatabaseConnection,
    requestSaveWithSyncConfirm,
    confirmRef,
    handleTypedConfirm,
  };
}

export type DataSourceManagementVm = ReturnType<typeof useDataSourceManagement>;
