import { useCallback, useEffect, useRef, useState } from "react";
import { Form } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { AgentAppsAuth } from "@/components/auth";
import {
  createFeishuAccountId,
  getOAuthStateFromConnection,
  loadFeishuAppSetup,
  loadFeishuAuthAccounts,
  persistFeishuAuthAccounts,
  type FeishuAccountFormValues,
  type FeishuAuthAccount,
} from "@/modules/dataSource/common/feishuAccounts";
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
} from "@/modules/dataSource/constants/types";
import { sourceTypeOptions } from "@/modules/dataSource/constants/sourceTypeOptions";
import { useLocalPathTree } from "@/modules/dataSource/hooks/useLocalPathTree";
import { useFeishuTargetTree } from "@/modules/dataSource/hooks/useFeishuTargetTree";
import type {
  CloudSetupIntent,
  DataSourceSaveMode,
  ManagementContext,
} from "@/modules/dataSource/hooks/management/context";
import { createOAuthEngine } from "@/modules/dataSource/hooks/management/createOAuthEngine";
import { createWizardSetup } from "@/modules/dataSource/hooks/management/createWizardSetup";
import { createWizardFlow } from "@/modules/dataSource/hooks/management/createWizardFlow";
import { createSaveActions } from "@/modules/dataSource/hooks/management/createSaveActions";
import { type ScanV2AgentHint } from "@/modules/dataSource/utils/scanAccessors";
import { pickScanAgent } from "@/modules/dataSource/utils/cloudSync";
import { isAdminRole } from "@/modules/dataSource/utils/role";
import { loadNotionAppSetup } from "@/modules/dataSource/utils/notionSetup";

interface UseSyncKnowledgeBaseCreationOptions {
  onSuccess?: () => void | Promise<void>;
}

export function useSyncKnowledgeBaseCreation(options: UseSyncKnowledgeBaseCreationOptions = {}) {
  const { onSuccess } = options;
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [form] = Form.useForm<SourceFormValues>();
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
  const [feishuAuthAccounts, setFeishuAuthAccounts] = useState<FeishuAuthAccount[]>(
    () => loadFeishuAuthAccounts(),
  );
  const [editingFeishuAccountId, setEditingFeishuAccountId] = useState<string | null>(null);
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
  const [feishuSetupIntent, setFeishuSetupIntent] = useState<CloudSetupIntent>(null);
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
  const [validatedAgentId, setValidatedAgentId] = useState<string | null>(null);
  const [wizardSaving, setWizardSaving] = useState(false);
  const [wizardSavingMode, setWizardSavingMode] = useState<DataSourceSaveMode | null>(null);
  const feishuAuthAccountsLoadedRef = useRef(false);
  const sourceListRequestSeqRef = useRef(0);
  const onSuccessRef = useRef(onSuccess);

  onSuccessRef.current = onSuccess;

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
    const selectedAgent = pickScanAgent(scanAgents, validatedAgentId);
    return selectedAgent?.agent_id || validatedAgentId || "";
  };

  const {
    localPathOptions,
    localPathLoading,
    loadLocalPathOptions,
    handleSearchLocalPathOptions,
    handleLoadLocalPathChildren,
    resetLocalPathBrowseOptions,
  } = useLocalPathTree({ t, form, getPreferredLocalAgentId });

  const getActiveFeishuAuthConnectionId = () => oauthConnection?.connectionId || "";

  const {
    feishuTargetTreeData,
    feishuTargetLoading,
    loadFeishuTargetOptions,
    handleSearchFeishuTargetOptions,
    handleLoadFeishuTargetChildren,
    resetFeishuTargetBrowseOptions,
    seedFeishuTargetTree,
  } = useFeishuTargetTree({ t, feishuTargetType, getActiveFeishuAuthConnectionId });

  const refreshSourcesAfterCreate = useCallback(async () => {
    await onSuccessRef.current?.();
  }, []);

  const ctx = {} as ManagementContext;
  Object.assign(ctx, {
    t,
    navigate,
    form,
    feishuSetupForm,
    sources: [] as DataSourceItem[],
    setSources: () => undefined,
    activeView: "assets" as const,
    setActiveView: () => undefined,
    assetSearchValue: "",
    setAssetSearchValue: () => undefined,
    sourceListPage: 1,
    setSourceListPage: () => undefined,
    sourceListPageSize: 10,
    setSourceListPageSize: () => undefined,
    sourceListTotal: 0,
    setSourceListTotal: () => undefined,
    scanLoading: false,
    setScanLoading: () => undefined,
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
    createSuccessMessageKey: "knowledge.createFromCloudDocumentsSuccess",
    refreshSources: refreshSourcesAfterCreate,
    handleToggleLocalScanChat: async () => undefined,
    executeDeleteSource: async () => undefined,
    openDetailPage: () => undefined,
    openEditWizard: () => undefined,
  });
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
    void ctx.refreshFeishuAuthAccounts();
    void ctx.refreshNotionAuthAccounts();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openCreateModal = useCallback(() => {
    setCreateProviderModalOpen(true);
  }, []);

  const requestSaveWithSyncConfirm = (mode: DataSourceSaveMode) => {
    void ctx.handleSave(mode);
  };

  return {
    t,
    form,
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
    handleSelectType: ctx.handleSelectType,
    handleResetFeishuSetup: ctx.handleResetFeishuSetup,
    handleResetNotionSetup: ctx.handleResetNotionSetup,
    requestSaveWithSyncConfirm,
    openCreateModal,
    feishuSetupForm: ctx.feishuSetupForm,
    cloudSetupProvider: ctx.cloudSetupProvider,
    feishuSetupModalOpen: ctx.feishuSetupModalOpen,
    setFeishuSetupModalOpen: ctx.setFeishuSetupModalOpen,
    setFeishuSetupIntent: ctx.setFeishuSetupIntent,
    feishuSetupSubmitting: ctx.feishuSetupSubmitting,
    handleSaveFeishuSetup: ctx.handleSaveFeishuSetup,
    handleCancelCloudSetup: ctx.handleCancelCloudSetup,
    openEditWizard: ctx.openEditWizard,
  };
}

export type SyncKnowledgeBaseCreationVm = ReturnType<typeof useSyncKnowledgeBaseCreation>;
