import { useEffect, useRef, useState } from "react";
import { Form, message } from "antd";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { dataSourceCloudOauthApi } from "@/modules/dataSource/api/clients";
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
  consumeCloudDataSourceOAuthResult,
  consumeFeishuDataSourceOAuthResult,
  type CloudDataSourceProvider,
  type FeishuDataSourceOAuthMessage,
} from "@/modules/dataSource/common/feishuOAuth";
import { createOAuthEngine } from "@/modules/dataSource/hooks/management/createOAuthEngine";
import type { ManagementContext } from "@/modules/dataSource/hooks/management/context";
import type { FeishuAppSetup, OAuthState, PendingOAuthAttempt } from "@/modules/dataSource/constants/types";
import { loadNotionAppSetup, persistNotionAppSetup } from "@/modules/dataSource/utils/notionSetup";
import { persistFeishuAppSetup } from "@/modules/dataSource/common/feishuAccounts";
import type { CloudSetupIntent } from "@/modules/dataSource/hooks/management/context";
import {
  getCloudConnectionItems,
  mapCloudConnectionToDataSourceConnection,
} from "@/modules/dataSource/mappers/dataSourceConnection";
import {
  CLOUD_DOCUMENTS_FEISHU_PATH,
  CLOUD_DOCUMENTS_GOOGLE_DRIVE_PATH,
  CLOUD_DOCUMENTS_LOCAL_PATH,
  CLOUD_DOCUMENTS_PATH,
} from "../utils/cloudDocumentUrls";
import { useLocalDataSourceSettings } from "./useLocalDataSourceSettings";

export function useCloudDocumentProviders() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const localSettings = useLocalDataSourceSettings();
  const [feishuSetupForm] = Form.useForm<FeishuAccountFormValues>();
  const [feishuAuthAccounts, setFeishuAuthAccounts] = useState<FeishuAuthAccount[]>(() =>
    loadFeishuAuthAccounts(),
  );
  const [feishuAppSetup, setFeishuAppSetup] = useState<FeishuAppSetup | null>(() =>
    loadFeishuAppSetup(),
  );
  const [notionAppSetup, setNotionAppSetup] = useState<FeishuAppSetup | null>(() =>
    loadNotionAppSetup(),
  );
  const [feishuSecretConfigured, setFeishuSecretConfigured] = useState(() =>
    Boolean(loadFeishuAppSetup()?.appSecret.trim()),
  );
  const [notionSecretConfigured, setNotionSecretConfigured] = useState(() =>
    Boolean(loadNotionAppSetup()?.appSecret.trim()),
  );
  const [notionOauthConnection, setNotionOauthConnection] =
    useState<ManagementContext["notionOauthConnection"]>(null);
  const [notionAuthAccounts, setNotionAuthAccounts] = useState<
    FeishuAuthAccount[]
  >([]);
  const [googleDriveConnection, setGoogleDriveConnection] =
    useState<ManagementContext["notionOauthConnection"]>(null);
  const [oauthConnection, setOauthConnection] = useState<ManagementContext["oauthConnection"]>(null);
  const [oauthState, setOauthState] = useState<OAuthState>("pending");
  const [connectionVerified, setConnectionVerified] = useState(false);
  const [cloudSetupProvider, setCloudSetupProvider] =
    useState<CloudDataSourceProvider>("feishu");
  const [feishuSetupModalOpen, setFeishuSetupModalOpen] = useState(false);
  const [feishuSetupIntent, setFeishuSetupIntent] = useState<CloudSetupIntent>(null);
  const [feishuSetupSubmitting, setFeishuSetupSubmitting] = useState(false);
  const [editingFeishuAccountId, setEditingFeishuAccountId] = useState<string | null>(null);
  const [oauthLoading, setOauthLoading] = useState(true);
  const oauthAttemptRef = useRef<PendingOAuthAttempt | null>(null);
  const feishuAuthAccountsLoadedRef = useRef(false);
  const loading = localSettings.loading || oauthLoading;

  const isFeishuSetupReady = Boolean(
    feishuAppSetup?.appId.trim() && (feishuAppSetup?.appSecret.trim() || feishuSecretConfigured),
  );
  const isNotionSetupReady = Boolean(
    notionAppSetup?.appId.trim() && (notionAppSetup?.appSecret.trim() || notionSecretConfigured),
  );
  const validFeishuAccounts = feishuAuthAccounts.filter(
    (account) =>
      account.status === "connected" && Boolean(account.connection?.connectionId),
  );
  const isFeishuAuthValid = validFeishuAccounts.length > 0;
  const isNotionAuthValid =
    notionOauthConnection?.status === "connected" &&
    Boolean(notionOauthConnection.connectionId);
  const isGoogleDriveAuthValid =
    googleDriveConnection?.status === "connected" &&
    Boolean(googleDriveConnection.connectionId);

  const ctx = {} as ManagementContext;
  Object.assign(ctx, {
    t,
    navigate,
    form: {} as ManagementContext["form"],
    feishuSetupForm,
    sources: [],
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
    wizardOpen: false,
    setWizardOpen: () => undefined,
    wizardStep: 0,
    setWizardStep: () => undefined,
    wizardMode: "create" as const,
    setWizardMode: () => undefined,
    selectedType: null,
    setSelectedType: () => undefined,
    editingId: null,
    setEditingId: () => undefined,
    wizardSaving: false,
    setWizardSaving: () => undefined,
    wizardSavingMode: null,
    setWizardSavingMode: () => undefined,
    createProviderModalOpen: false,
    setCreateProviderModalOpen: () => undefined,
    authSelectModalOpen: false,
    setAuthSelectModalOpen: () => undefined,
    cloudSetupProvider,
    setCloudSetupProvider,
    feishuSetupModalOpen,
    setFeishuSetupModalOpen,
    feishuSetupIntent,
    setFeishuSetupIntent,
    feishuSetupSubmitting,
    setFeishuSetupSubmitting,
    manualOauthModalOpen: false,
    setManualOauthModalOpen: () => undefined,
    manualOauthCallbackValue: "",
    setManualOauthCallbackValue: () => undefined,
    manualOauthSubmitting: false,
    setManualOauthSubmitting: () => undefined,
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
    localScanChatEnabled: false,
    setLocalScanChatEnabled: () => undefined,
    localScanChatSaving: false,
    setLocalScanChatSaving: () => undefined,
    validatedAgentId: null,
    setValidatedAgentId: () => undefined,
    canCreateLocalSource: localSettings.canCreateLocalSource,
    scanAgents: [],
    isFeishuSetupReady,
    isNotionSetupReady,
    isFeishuAuthValid,
    isNotionAuthValid,
    sourceListRequestSeqRef: { current: 0 },
    feishuAuthAccountsLoadedRef,
    feishuTargetTreeData: [],
    resetLocalPathBrowseOptions: () => undefined,
    resetFeishuTargetBrowseOptions: () => undefined,
    refreshSources: async () => undefined,
    handleToggleLocalScanChat: async () => undefined,
  });
  Object.assign(ctx, createOAuthEngine(ctx));

  const refreshCloudAppCredential = async (
    provider: Extract<CloudDataSourceProvider, "feishu" | "notion">,
  ) => {
    try {
      const response =
        await dataSourceCloudOauthApi.getOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsGet({
          provider,
        });
      const appId = (response.data.app_id || "").trim();
      const secretConfigured = Boolean(response.data.secret_configured);
      if (!appId || !secretConfigured) {
        return;
      }
      const setup = { appId, appSecret: "" };
      if (provider === "feishu") {
        setFeishuAppSetup((current) =>
          current?.appId === appId && current.appSecret.trim() ? current : setup,
        );
        setFeishuSecretConfigured(true);
      } else {
        setNotionAppSetup((current) =>
          current?.appId === appId && current.appSecret.trim() ? current : setup,
        );
        setNotionSecretConfigured(true);
      }
    } catch (error) {
      console.error(`Failed to refresh ${provider} app credentials`, error);
    }
  };

  const refreshGoogleDriveConnection = async () => {
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "googledrive",
          status: null,
        });
      const nextConnection = getCloudConnectionItems(response.data)
        .map((item) => mapCloudConnectionToDataSourceConnection(item, "googledrive"))
        .find(
          (connection) =>
            connection.status === "connected" && Boolean(connection.connectionId),
        ) || null;
      setGoogleDriveConnection(nextConnection);
    } catch {
      setGoogleDriveConnection(null);
    }
  };

  const refreshPageData = async () => {
    setOauthLoading(true);
    try {
      await Promise.all([
        refreshCloudAppCredential("feishu"),
        refreshCloudAppCredential("notion"),
        ctx.refreshFeishuAuthAccounts(),
        ctx.refreshNotionAuthConnection(),
        refreshGoogleDriveConnection(),
      ]);
    } finally {
      setOauthLoading(false);
    }
  };
  const openCloudSetupModal = (
    provider: CloudDataSourceProvider,
    intent: CloudSetupIntent = "auth",
    account?: FeishuAuthAccount | null,
  ) => {
    const activeSetup = provider === "feishu" ? feishuAppSetup : notionAppSetup;
    setCloudSetupProvider(provider);
    setFeishuSetupIntent(intent);
    setEditingFeishuAccountId(account?.id || null);
    feishuSetupForm.setFieldsValue({
      name: account?.name || "",
      appId: account?.appId || activeSetup?.appId || "",
      appSecret: account?.appSecret || activeSetup?.appSecret || "",
    });
    setFeishuSetupModalOpen(true);
  };

  const handleSaveFeishuSetup = async () => {
    if (feishuSetupSubmitting) {
      return;
    }

    setFeishuSetupSubmitting(true);
    try {
      const values = await feishuSetupForm.validateFields();
      const nextSetup: FeishuAppSetup = {
        appId: values.appId.trim(),
        appSecret: values.appSecret.trim(),
      };
      const provider = cloudSetupProvider;
      const shouldStartOAuth = feishuSetupIntent === "auth";
      const nextAccount =
        provider === "feishu" ? ctx.upsertFeishuAuthAccount(values, "waiting") : null;

      await ctx.saveCloudAppCredentials(provider, nextSetup);
      if (provider === "feishu") {
        persistFeishuAppSetup(nextSetup);
        setFeishuAppSetup(nextSetup);
        setFeishuSecretConfigured(true);
      } else {
        persistNotionAppSetup(nextSetup);
        setNotionAppSetup(nextSetup);
        setNotionSecretConfigured(true);
      }
      setFeishuSetupModalOpen(false);
      setFeishuSetupIntent(null);
      setEditingFeishuAccountId(null);
      message.success(
        provider === "feishu"
          ? t("modelProvider.cloudDocuments.feishuCredentialSaved")
          : t("modelProvider.cloudDocuments.notionCredentialSaved"),
      );

      if (shouldStartOAuth) {
        await ctx.startCloudOAuth(provider, {
          setup: nextSetup,
          draftWizardOpen: false,
          draftSelectedType: provider === "googledrive" ? null : provider,
          draftWizardStep: 0,
          previousState: "pending",
          previousVerified: false,
          previousConnection: null,
          accountId: nextAccount?.id,
          appId: nextSetup.appId,
        });
        if (provider === "notion") {
          void ctx.refreshNotionAuthConnection();
        } else {
          void ctx.refreshFeishuAuthAccounts();
        }
      }
    } finally {
      setFeishuSetupSubmitting(false);
    }
  };

  const handleManageFeishuAuth = () => {
    navigate(CLOUD_DOCUMENTS_FEISHU_PATH);
  };

  const handleManageLocalSource = () => {
    navigate(CLOUD_DOCUMENTS_LOCAL_PATH);
  };

  const handleManageGoogleDrive = () => {
    navigate(CLOUD_DOCUMENTS_GOOGLE_DRIVE_PATH);
  };

  const handleOpenNotionSetup = () => {
    openCloudSetupModal("notion", "auth");
  };

  useEffect(() => {
    void refreshPageData();

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
  }, []);

  useEffect(() => {
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
  }, [feishuAppSetup, feishuAuthAccounts.length, oauthConnection]);

  return {
    t,
    loading,
    feishuSetupForm,
    cloudSetupProvider,
    feishuSetupModalOpen,
    setFeishuSetupModalOpen,
    feishuSetupIntent,
    setFeishuSetupIntent,
    feishuSetupSubmitting,
    canCreateLocalSource: localSettings.canCreateLocalSource,
    localSourceCount: localSettings.localSourceCount,
    isFeishuAuthValid,
    isNotionAuthValid,
    isGoogleDriveAuthValid,
    isFeishuSetupReady,
    isNotionSetupReady,
    validFeishuAccounts,
    notionOauthConnection,
    googleDriveConnection,
    handleManageFeishuAuth,
    handleManageLocalSource,
    handleManageGoogleDrive,
    handleOpenNotionSetup,
    openCloudSetupModal,
    handleSaveFeishuSetup,
    refreshPageData,
    cloudDocumentsPath: CLOUD_DOCUMENTS_PATH,
  };
}

export type CloudDocumentProvidersVm = ReturnType<typeof useCloudDocumentProviders>;
