import { message } from "antd";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "../../api/clients";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  finishFeishuDataSourceOAuth,
  saveFeishuDataSourceWizardDraft,
  type FeishuDataSourceConnection,
} from "@/modules/dataSource/common/feishuOAuth";
import { getOAuthStateFromConnection } from "../../common/feishuAccounts";
import type {
  DataSourceItem,
  DetailDocumentItem,
  SourceType,
} from "../../constants/types";
import { parseFeishuOAuthCallbackInput } from "../../utils/feishuAccount";
import { mapScanSyncDetail } from "../../mappers/scanDocument";
import type { ManagementContext } from "./context";

export function createWizardFlow(ctx: ManagementContext) {
  const {
    t,
    navigate,
    form,
    setCreateProviderModalOpen,
    setAuthSelectModalOpen,
    setWizardMode,
    setEditingId,
    setWizardStep,
    setWizardOpen,
    setOauthConnection,
    setOauthState,
    setConnectionVerified,
    setManualOauthModalOpen,
    setManualOauthCallbackValue,
    setManualOauthSubmitting,
  } = ctx;

  const handleSelectType = (type: SourceType) => {
    if (type === "local" && !ctx.canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }
    if (type === "feishu" && !ctx.isFeishuSetupReady) {
      ctx.openFeishuSetupModal("create");
      return;
    }
    if (type === "notion" && !ctx.isNotionSetupReady) {
      ctx.openCloudSetupModal("notion", "create");
      return;
    }
    ctx.applySourceType(type);
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
        ? options?.connection || (type === "notion" ? ctx.notionOauthConnection : ctx.oauthConnection)
        : null;
    ctx.resetWizard();
    setWizardMode("create");
    setEditingId(null);
    setCreateProviderModalOpen(false);
    setAuthSelectModalOpen(false);
    ctx.applySourceType(type);
    setWizardStep(1);
    setWizardOpen(true);

    if (
      (type === "feishu" || type === "notion") &&
      reusableConnection?.connectionId &&
      getOAuthStateFromConnection(reusableConnection) === "connected"
    ) {
      setOauthConnection(reusableConnection);
      setOauthState("connected");
      setConnectionVerified(true);
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
      setAuthSelectModalOpen(true);
      return;
    }

    if (type === "notion" && ctx.isNotionAuthValid) {
      setCreateProviderModalOpen(false);
      openSourceCreateWizard("notion", { connection: ctx.notionOauthConnection });
      return;
    }

    setCreateProviderModalOpen(false);
    ctx.resetWizard();
    setWizardMode("create");
    setEditingId(null);
    ctx.applySourceType(type);
    setWizardStep(1);

    if (type === "feishu" && !ctx.isFeishuAuthValid) {
      ctx.openCloudSetupModal("feishu", "create");
      return;
    }
    if (type === "notion" && !ctx.isNotionAuthValid) {
      ctx.openCloudSetupModal("notion", "create");
      return;
    }
  };

  const handleSelectFeishuAuthConnection = (
    connection: FeishuDataSourceConnection,
  ) => {
    setAuthSelectModalOpen(false);
    openSourceCreateWizard("feishu", { connection });
  };

  const handleManageFeishuAuth = () => {
    navigate("/data-sources/providers/feishu");
  };

  const handleOpenFeishuGuideFromAuthSelect = () => {
    saveFeishuDataSourceWizardDraft({
      activeView: ctx.activeView,
      authSelectModalOpen: true,
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
    navigate("/data-sources/docs/feishu-setup?from=create-source");
  };

  const handleSubmitManualOauthCallback = async () => {
    const parsed = parseFeishuOAuthCallbackInput(ctx.manualOauthCallbackValue);
    if (!parsed) {
      message.warning(t("admin.dataSourceOauthManualCallbackInvalid"));
      return;
    }

    try {
      setManualOauthSubmitting(true);
      const connection = await finishFeishuDataSourceOAuth(parsed.code, parsed.state);
      ctx.applyOauthResult({
        channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
        source: "feishu-data-source",
        status: "success",
        connection,
      });
      setManualOauthModalOpen(false);
      setManualOauthCallbackValue("");
    } catch (error: any) {
      const errorMessage =
        error?.message || t("admin.dataSourceOauthFailedRetry");
      ctx.applyOauthResult({
        channel: FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
        source: "feishu-data-source",
        status: "error",
        message: errorMessage,
      });
    } finally {
      setManualOauthSubmitting(false);
    }
  };

  const openDetailPage = (record: DataSourceItem) => {
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
      message.error(
        getLocalizedErrorMessage(error, t("admin.dataSourceDeleteFailed")) ||
          t("admin.dataSourceDeleteFailed"),
      );
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
        !(ctx.oauthConnection?.provider === "feishu" && ctx.oauthConnection.connectionId)
      ) {
        if (ctx.isFeishuSetupReady && ctx.feishuAppSetup && ctx.oauthState !== "waiting") {
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
        !(ctx.oauthConnection?.provider === "notion" && ctx.oauthConnection.connectionId)
      ) {
        if (ctx.isNotionSetupReady && ctx.notionAppSetup && ctx.oauthState !== "waiting") {
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
    handleManageFeishuAuth,
    handleOpenFeishuGuideFromAuthSelect,
    handleSubmitManualOauthCallback,
    openDetailPage,
    executeDeleteSource,
    handleNextStep,
  };
}
