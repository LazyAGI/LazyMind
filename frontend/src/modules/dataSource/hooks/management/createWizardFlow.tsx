import { message } from "antd";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "../../api/clients";
import {
  deleteDatabaseConnection,
  updateDatabaseConnection,
} from "../../api/databaseConnections";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  finishFeishuDataSourceOAuth,
  saveFeishuDataSourceWizardDraft,
  type CloudDataSourceProvider,
  type FeishuDataSourceConnection,
} from "@/modules/dataSource/common/feishuOAuth";
import { getOAuthStateFromConnection } from "../../common/feishuAccounts";
import { DEFAULT_DATA_SOURCE_FILE_TYPES } from "../../constants/options";
import {
  DEFAULT_SCHEDULE_TIME,
  DEFAULT_SCHEDULE_WEEKDAYS,
} from "../../utils/schedule";
import type {
  DataSourceItem,
  DetailDocumentItem,
  SourceType,
} from "../../constants/types";
import { parseFeishuOAuthCallbackInput } from "../../utils/feishuAccount";
import { mapScanSyncDetail } from "../../mappers/scanDocument";
import {
  CLOUD_DOCUMENTS_FEISHU_SETUP_PATH,
  CLOUD_DOCUMENTS_NOTION_SETUP_PATH,
} from "@/modules/modelProvider/utils/cloudDocumentUrls";
import type { FeishuDataSourceWizardDraft } from "@/modules/dataSource/common/feishuOAuth";
import type { ManagementContext } from "./context";

export function createWizardFlow(ctx: ManagementContext) {
  const {
    t,
    navigate,
    form,
    setCreateProviderModalOpen,
    setAuthSelectModalOpen,
    setAuthSelectProvider,
    setWizardMode,
    setEditingId,
    setWizardStep,
    setWizardOpen,
    setOauthConnection,
    setNotionOauthConnection,
    setOauthState,
    setConnectionVerified,
    setManualOauthModalOpen,
    setManualOauthCallbackValue,
    setManualOauthSubmitting,
  } = ctx;

  const buildAuthSelectWizardDraft = (
    provider: CloudDataSourceProvider,
  ): FeishuDataSourceWizardDraft => ({
    authSelectModalOpen: true,
    authSelectProvider: provider,
    wizardOpen: false,
    wizardStep: ctx.wizardStep,
    wizardMode: ctx.wizardMode,
    selectedType: ctx.selectedType,
    editingId: ctx.editingId,
    validatedAgentId: ctx.validatedAgentId,
    oauthState: ctx.oauthState,
    connectionVerified: ctx.connectionVerified,
    oauthConnection: ctx.oauthConnection,
    formValues: form.getFieldsValue(true),
  });

  const handleSelectType = (type: SourceType) => {
    if (type === "local" && !ctx.canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }
    if (type === "feishu" && !ctx.isFeishuSetupReady) {
      ctx.openCloudSetupModal("feishu", "create");
      return;
    }
    if (type === "notion" && !ctx.isNotionSetupReady) {
      ctx.openCloudSetupModal("notion", "create");
      return;
    }
    ctx.applySourceType(type);
  };

  const buildCloudCreateFormValues = (type: CloudDataSourceProvider) => ({
    syncMode: "scheduled" as const,
    scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
    scheduleTime: DEFAULT_SCHEDULE_TIME,
    conflictPolicy: "versioned" as const,
    path: [],
    target: type === "feishu" ? [] : "",
    targetType: type === "feishu" ? ("wiki_space" as const) : ("page" as const),
    fileTypes: DEFAULT_DATA_SOURCE_FILE_TYPES,
  });

  const startCloudAuthForCreate = (type: CloudDataSourceProvider) => {
    ctx.resetWizard();
    setWizardMode("create");
    setEditingId(null);
    ctx.applySourceType(type);
    setWizardStep(1);
    setWizardOpen(false);

    const setup = type === "feishu" ? ctx.feishuAppSetup : ctx.notionAppSetup;
    if (!setup) {
      ctx.openCloudSetupModal(type, "create");
      return;
    }

    void ctx.startCloudOAuth(type, {
      setup,
      draftSelectedType: type,
      draftWizardStep: 1,
      draftWizardMode: "create",
      draftWizardOpen: true,
      draftFormValues: buildCloudCreateFormValues(type),
      previousState: "pending",
      previousVerified: false,
      previousConnection: null,
      openWizardOnSuccess: true,
      reopenSetupOnFailure: true,
    });
  };

  const openSourceCreateWizard = (
    type: SourceType,
    options?: { connection?: FeishuDataSourceConnection | null },
  ) => {
    if (type === "local" && !ctx.canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }
    const reusableConnection =
      type === "feishu" || type === "notion"
        ? options?.connection ||
          (type === "notion" ? ctx.notionOauthConnection : ctx.oauthConnection)
        : null;
    ctx.resetWizard();
    setWizardMode("create");
    setEditingId(null);
    setCreateProviderModalOpen(false);
    setAuthSelectModalOpen(false);
    setAuthSelectProvider(null);
    ctx.applySourceType(type);
    setWizardStep(1);
    setWizardOpen(true);

    if (
      (type === "feishu" || type === "notion") &&
      reusableConnection?.connectionId
    ) {
      setOauthConnection(reusableConnection);
      if (type === "notion") {
        setNotionOauthConnection(reusableConnection);
      }
      const nextState = getOAuthStateFromConnection(reusableConnection);
      setOauthState(nextState);
      setConnectionVerified(nextState === "connected");
    }
  };

  const handleCreateProviderSelect = (type: SourceType) => {
    if (type !== "feishu" && type !== "notion") {
      setCreateProviderModalOpen(false);
      openSourceCreateWizard(type);
      return;
    }

    if (type === "feishu" && ctx.isFeishuAuthValid) {
      setCreateProviderModalOpen(false);
      setAuthSelectProvider("feishu");
      setAuthSelectModalOpen(true);
      return;
    }

    if (type === "notion" && ctx.isNotionAuthValid) {
      setCreateProviderModalOpen(false);
      setAuthSelectProvider("notion");
      setAuthSelectModalOpen(true);
      return;
    }

    setCreateProviderModalOpen(false);

    if (type === "feishu" && !ctx.isFeishuAuthValid) {
      if (!ctx.isFeishuSetupReady) {
        ctx.openCloudSetupModal("feishu", "create");
        return;
      }
      startCloudAuthForCreate("feishu");
      return;
    }

    if (type === "notion" && !ctx.isNotionAuthValid) {
      if (!ctx.isNotionSetupReady) {
        ctx.openCloudSetupModal("notion", "create");
        return;
      }
      startCloudAuthForCreate("notion");
    }
  };

  const handleSelectFeishuAuthConnection = (
    connection: FeishuDataSourceConnection,
  ) => {
    setAuthSelectModalOpen(false);
    setAuthSelectProvider(null);
    openSourceCreateWizard("feishu", { connection });
  };

  const handleSelectNotionAuthConnection = (
    connection: FeishuDataSourceConnection,
  ) => {
    setAuthSelectModalOpen(false);
    setAuthSelectProvider(null);
    openSourceCreateWizard("notion", { connection });
  };

  const handleManageFeishuAuth = () => {
    ctx.openCloudSetupModal("feishu", "auth");
  };

  const handleAddFeishuAuthFromSelect = () => {
    saveFeishuDataSourceWizardDraft(buildAuthSelectWizardDraft("feishu"));
    setAuthSelectModalOpen(false);
    ctx.openCloudSetupModal("feishu", "auth");
  };

  const handleAddNotionAuthFromSelect = () => {
    saveFeishuDataSourceWizardDraft(buildAuthSelectWizardDraft("notion"));
    setAuthSelectModalOpen(false);
    ctx.openCloudSetupModal("notion", "auth");
  };

  const handleOpenFeishuGuideFromAuthSelect = () => {
    saveFeishuDataSourceWizardDraft(buildAuthSelectWizardDraft("feishu"));
    navigate(`${CLOUD_DOCUMENTS_FEISHU_SETUP_PATH}?from=create-source`);
  };

  const handleOpenNotionGuideFromAuthSelect = () => {
    saveFeishuDataSourceWizardDraft(buildAuthSelectWizardDraft("notion"));
    navigate(`${CLOUD_DOCUMENTS_NOTION_SETUP_PATH}?from=create-source`);
  };

  const handleSubmitManualOauthCallback = async () => {
    const parsed = parseFeishuOAuthCallbackInput(ctx.manualOauthCallbackValue);
    if (!parsed) {
      message.warning(t("admin.dataSourceOauthManualCallbackInvalid"));
      return;
    }

    try {
      setManualOauthSubmitting(true);
      const connection = await finishFeishuDataSourceOAuth(
        parsed.code,
        parsed.state,
      );
      ctx.applyOauthResult({
        channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
        source: "feishu-data-source",
        status: "success",
        connection,
      });
      setManualOauthModalOpen(false);
      setManualOauthCallbackValue("");
    } catch (error) {
      const requestError = error as { response?: unknown; request?: unknown };
      if (requestError?.response || requestError?.request) {
        ctx.restorePreviousOauthState();
      } else {
        const errorMessage = getLocalizedErrorMessage(error);
        ctx.applyOauthResult({
          channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
          source: "feishu-data-source",
          status: "error",
          message: errorMessage,
        });
      }
    } finally {
      setManualOauthSubmitting(false);
    }
  };

  const openDetailPage = (record: DataSourceItem) => {
    if (record.type === "database") {
      navigate("/databases");
      return;
    }

    const detailDocuments: DetailDocumentItem[] =
      record.detailDocuments ||
      record.fileCandidates.map((item) => ({
        id: item.id,
        name: item.name,
        path: item.path,
        size: item.size,
        tags: [],
        updateState: item.updateState,
        syncDetail: mapScanSyncDetail(item.updateState, t),
        parseStatus: item.updateState === "deleted" ? "deleted" : "parsed",
        sourceUpdatedAt: record.lastSync,
        updatedAt: record.lastSync,
      }));

    navigate(`/data-sources/${record.id}`, {
      state: {
        source: {
          id: record.id,
          name: record.name,
          target: record.target,
          rootPath: record.rootPath,
          targetRef: record.targetRef,
          targetRefs: record.targetRefs,
          targetType: record.targetType,
          targetTypes: record.targetTypes,
          sourceType: record.type,
          documentCount: record.documentCount,
          parsedDocumentCount: record.parsedDocumentCount,
          status: record.status,
          lastSync: record.lastSync,
          addCount: record.addCount,
          deleteCount: record.deleteCount,
          changeCount: record.changeCount,
          storageUsed: record.storageUsed || "0 B",
          documents: detailDocuments,
          scanManaged: record.scanManaged,
          tenantId: record.tenantId,
          agentId: record.agentId,
          bindingId: record.bindingId,
          bindingIds: record.bindingIds,
          bindingTreeKey: record.bindingTreeKey,
          bindingTreeKeys: record.bindingTreeKeys,
          configVersion: record.configVersion,
        },
      },
    });
  };

  const getDatabaseConnectionId = (record: DataSourceItem) =>
    record.databaseConnectionId || record.id.replace(/^database:/, "");

  const openDatabaseConnectionConfig = (record: DataSourceItem) => {
    if (!record.databaseConnection) {
      message.error(t("admin.dataSourceDatabaseConfigMissing"));
      return;
    }
    ctx.setDatabaseEditingConnection(record.databaseConnection);
  };

  const closeDatabaseConnectionConfig = () => {
    if (ctx.databaseEditSaving) {
      return;
    }
    ctx.setDatabaseEditingConnection(null);
  };

  const handleSaveDatabaseConnectionConfig = async (
    payload: Parameters<typeof updateDatabaseConnection>[1],
  ) => {
    if (!ctx.databaseEditingConnection || ctx.databaseEditSaving) {
      return;
    }
    ctx.setDatabaseEditSaving(true);
    try {
      await updateDatabaseConnection(ctx.databaseEditingConnection.id, payload);
      message.success(t("admin.dataSourceDatabaseUpdated"));
      ctx.setDatabaseEditingConnection(null);
      await ctx.refreshSources(false, { page: ctx.sourceListPage });
    } catch {
    } finally {
      ctx.setDatabaseEditSaving(false);
    }
  };

  const executeDeleteDatabaseConnection = async (record: DataSourceItem) => {
    try {
      await deleteDatabaseConnection(getDatabaseConnectionId(record));
      message.success(t("admin.dataSourceDatabaseDeleted"));
      await ctx.refreshSources(false, { page: ctx.sourceListPage });
    } catch (error) {
      throw error;
    }
  };

  const executeDeleteSource = async (record: DataSourceItem) => {
    try {
      await dataSourceScanApi.deleteSource({ sourceId: record.id });
      message.success(t("admin.dataSourceDeleteSuccess"));
      const nextPage =
        ctx.sources.length <= 1 && ctx.sourceListPage > 1
          ? ctx.sourceListPage - 1
          : ctx.sourceListPage;
      await Promise.all([ctx.refreshSources(false, { page: nextPage })]);
    } catch (error) {
      throw error;
    }
  };

  const handleNextStep = () => {
    if (ctx.wizardStep === 0) {
      if (!ctx.selectedType) {
        message.warning(t("admin.dataSourceSelectOneTypeFirst"));
        return;
      }
      if (
        ctx.selectedType === "feishu" &&
        !(
          ctx.oauthConnection?.provider === "feishu" &&
          ctx.oauthConnection.connectionId
        )
      ) {
        if (
          ctx.isFeishuSetupReady &&
          ctx.feishuAppSetup &&
          ctx.oauthState !== "waiting"
        ) {
          void ctx.startCloudOAuth("feishu", {
            setup: ctx.feishuAppSetup,
            draftSelectedType: "feishu",
            draftWizardStep: 0,
            previousState: ctx.oauthState,
            previousVerified: ctx.connectionVerified,
            previousConnection: ctx.oauthConnection,
          });
        }
        message.warning(t("admin.dataSourceOauthRequiredBeforeSave"));
        return;
      }
      if (
        ctx.selectedType === "notion" &&
        !(
          ctx.oauthConnection?.provider === "notion" &&
          ctx.oauthConnection.connectionId
        )
      ) {
        if (
          ctx.isNotionSetupReady &&
          ctx.notionAppSetup &&
          ctx.oauthState !== "waiting"
        ) {
          void ctx.startCloudOAuth("notion", {
            setup: ctx.notionAppSetup,
            draftSelectedType: "notion",
            draftWizardStep: 0,
            previousState: ctx.oauthState,
            previousVerified: ctx.connectionVerified,
            previousConnection: ctx.oauthConnection,
          });
        }
        message.warning(t("admin.dataSourceNotionAuthRequired"));
        return;
      }
      setWizardStep(1);
    }
  };

  return {
    handleSelectType,
    openSourceCreateWizard,
    handleCreateProviderSelect,
    handleSelectFeishuAuthConnection,
    handleSelectNotionAuthConnection,
    handleManageFeishuAuth,
    handleAddFeishuAuthFromSelect,
    handleAddNotionAuthFromSelect,
    handleOpenFeishuGuideFromAuthSelect,
    handleOpenNotionGuideFromAuthSelect,
    handleSubmitManualOauthCallback,
    openDetailPage,
    openDatabaseConnectionConfig,
    closeDatabaseConnectionConfig,
    handleSaveDatabaseConnectionConfig,
    executeDeleteSource,
    executeDeleteDatabaseConnection,
    handleNextStep,
  };
}
