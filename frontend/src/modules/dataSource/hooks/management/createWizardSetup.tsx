import { Modal, message } from "antd";
import { WarningFilled } from "@ant-design/icons";
import {
  clearFeishuAppSetup,
  getOAuthStateFromConnection,
  persistFeishuAppSetup,
  type FeishuAuthAccount,
} from "../../common/feishuAccounts";
import { clearFeishuDataSourceWizardDraft } from "@/modules/dataSource/common/feishuOAuth";
import { DEFAULT_DATA_SOURCE_FILE_TYPES } from "../../constants/options";
import type {
  DataSourceItem,
  FeishuAppSetup,
  SourceType,
} from "../../constants/types";
import {
  DEFAULT_SCHEDULE_TIME,
  DEFAULT_SCHEDULE_WEEKDAYS,
  inferScheduleWeekdays,
  normalizeScheduleTime,
} from "../../utils/schedule";
import { normalizeDataSourceFileTypes } from "../../utils/fileTypes";
import {
  normalizeCloudTargetRefs,
  normalizeFeishuTargetRefs,
  normalizeLocalPathRefs,
} from "../../utils/feishuTarget";
import { clearNotionAppSetup, persistNotionAppSetup } from "../../utils/notionSetup";
import type {
  CloudSetupIntent,
  FeishuSetupIntent,
  ManagementContext,
} from "./context";
import type { CloudDataSourceProvider } from "@/modules/dataSource/common/feishuOAuth";

export function createWizardSetup(ctx: ManagementContext) {
  const {
    t,
    form,
    feishuSetupForm,
    setWizardMode,
    setWizardStep,
    setWizardOpen,
    setSelectedType,
    setEditingId,
    setCreateProviderModalOpen,
    setAuthSelectModalOpen,
    setOauthState,
    setConnectionVerified,
    setOauthConnection,
    setNotionOauthConnection,
    setValidatedAgentId,
    setManualOauthModalOpen,
    setManualOauthCallbackValue,
    setManualOauthSubmitting,
    setCloudSetupProvider,
    setFeishuSetupIntent,
    setEditingFeishuAccountId,
    setFeishuSetupModalOpen,
    setFeishuSetupSubmitting,
    setFeishuAppSetup,
    setNotionAppSetup,
    resetLocalPathBrowseOptions,
    resetFeishuTargetBrowseOptions,
  } = ctx;

  const resetWizard = () => {
    form.resetFields();
    setWizardMode("create");
    setWizardStep(0);
    setSelectedType(null);
    setEditingId(null);
    setCreateProviderModalOpen(false);
    setAuthSelectModalOpen(false);
    setOauthState("pending");
    setConnectionVerified(false);
    setOauthConnection(null);
    setValidatedAgentId(null);
    setManualOauthModalOpen(false);
    setManualOauthCallbackValue("");
    setManualOauthSubmitting(false);
    resetLocalPathBrowseOptions();
    resetFeishuTargetBrowseOptions();
  };

  const openEditWizard = (record: DataSourceItem) => {
    resetWizard();
    setWizardMode("edit");
    setWizardOpen(true);
    setWizardStep(1);
    setSelectedType(record.type);
    setEditingId(record.id);
    setOauthConnection(record.oauthConnection || null);
    setOauthState(
      record.oauthConnection
        ? getOAuthStateFromConnection(record.oauthConnection)
        : record.connectionState === "connected"
          ? "connected"
          : record.connectionState === "expired"
            ? "expired"
            : record.connectionState === "error"
              ? "error"
              : "pending",
    );
    setConnectionVerified(record.connectionState === "connected");
    setValidatedAgentId(record.agentId || null);
    form.setFieldsValue({
      knowledgeBase: record.knowledgeBase,
      syncMode: record.syncMode,
      scheduleWeekdays: inferScheduleWeekdays(record.scheduleLabel),
      scheduleTime: normalizeScheduleTime(
        record.scheduleLabel.match(/\d{2}:\d{2}(?::\d{2})?/)?.[0],
      ),
      conflictPolicy: record.conflictPolicy,
      path:
        record.type === "local"
          ? normalizeLocalPathRefs(record.targetRefs || record.targetRef || record.target)
          : undefined,
      target:
        record.type === "feishu"
          ? normalizeFeishuTargetRefs(record.targetRefs || record.targetRef || record.target)
          : record.type === "notion"
            ? normalizeCloudTargetRefs(record.targetRefs || record.targetRef || record.target)
          : undefined,
      targetType:
        record.type === "feishu"
          ? record.targetType || "wiki_space"
          : record.type === "notion"
            ? record.targetType || "page"
            : undefined,
      fileTypes: normalizeDataSourceFileTypes(record.fileTypes),
      bucket:
        record.type === "s3"
          ? record.target.replace("s3://", "").split("/")[0]
          : undefined,
      prefix:
        record.type === "s3"
          ? record.target.replace(/^s3:\/\/[^/]+\/?/, "")
          : undefined,
      region: record.type === "s3" ? "ap-southeast-1" : undefined,
    });
  };

  const handleCloseWizard = () => {
    setWizardOpen(false);
    clearFeishuDataSourceWizardDraft();
    resetWizard();
  };

  const applySourceType = (type: SourceType) => {
    setSelectedType(type);
    setConnectionVerified(false);
    setOauthState("pending");
    setOauthConnection(null);
    setValidatedAgentId(null);
    resetLocalPathBrowseOptions();
    resetFeishuTargetBrowseOptions();
    form.setFieldsValue({
      syncMode: "scheduled",
      scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
      scheduleTime: DEFAULT_SCHEDULE_TIME,
      conflictPolicy: "versioned",
      path: [],
      target: type === "feishu" ? [] : "",
      targetType:
        type === "feishu"
          ? "wiki_space"
          : type === "notion"
            ? "page"
            : undefined,
      fileTypes: DEFAULT_DATA_SOURCE_FILE_TYPES,
    });
  };

  const openCloudSetupModal = (
    provider: CloudDataSourceProvider,
    intent: CloudSetupIntent = null,
    account?: FeishuAuthAccount | null,
  ) => {
    const activeSetup = provider === "feishu" ? ctx.feishuAppSetup : ctx.notionAppSetup;
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

  const openFeishuSetupModal = (
    intent: FeishuSetupIntent = null,
    account?: FeishuAuthAccount | null,
  ) => openCloudSetupModal("feishu", intent, account);

  const handleSaveFeishuSetup = async () => {
    if (ctx.feishuSetupSubmitting) {
      return;
    }

    setFeishuSetupSubmitting(true);
    try {
      const values = await feishuSetupForm.validateFields();
      const nextSetup: FeishuAppSetup = {
        appId: values.appId.trim(),
        appSecret: values.appSecret.trim(),
      };
      const cloudSetupProvider = ctx.cloudSetupProvider;
      const shouldStartOAuth = ctx.feishuSetupIntent === "create" || ctx.feishuSetupIntent === "auth";
      const nextAccount =
        cloudSetupProvider === "feishu"
          ? ctx.upsertFeishuAuthAccount(values, "waiting")
          : null;

      await ctx.saveCloudAppCredentials(cloudSetupProvider, nextSetup);
      if (cloudSetupProvider === "feishu") {
        persistFeishuAppSetup(nextSetup);
        setFeishuAppSetup(nextSetup);
      } else {
        persistNotionAppSetup(nextSetup);
        setNotionAppSetup(nextSetup);
      }
      setFeishuSetupModalOpen(false);
      const setupIntent = ctx.feishuSetupIntent;
      setFeishuSetupIntent(null);
      setEditingFeishuAccountId(null);
      message.success(
        cloudSetupProvider === "feishu"
          ? t("admin.dataSourceFeishuCredentialSaved")
          : t("admin.dataSourceNotionCredentialSaved"),
      );

      if (shouldStartOAuth) {
        resetWizard();
        setWizardMode("create");
        setEditingId(null);
        const cloudFormValues = {
          syncMode: "scheduled",
          scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
          scheduleTime: DEFAULT_SCHEDULE_TIME,
          conflictPolicy: "versioned",
          path: [],
          target: cloudSetupProvider === "feishu" ? [] : "",
          targetType: cloudSetupProvider === "feishu" ? "wiki_space" : "page",
        };

        applySourceType(cloudSetupProvider);
        setWizardOpen(setupIntent === "create");
        setWizardStep(1);
        await ctx.startCloudOAuth(cloudSetupProvider, {
          setup: nextSetup,
          draftSelectedType: cloudSetupProvider,
          draftWizardStep: 1,
          draftWizardMode: "create",
          draftEditingId: null,
          draftFormValues: cloudFormValues,
          draftWizardOpen: setupIntent === "create",
          previousState: "pending",
          previousVerified: false,
          previousConnection: null,
          accountId: nextAccount?.id,
          appId: nextSetup.appId,
        });
      }
    } finally {
      setFeishuSetupSubmitting(false);
    }
  };

  const handleResetFeishuSetup = () => {
    Modal.confirm({
      title: t("admin.dataSourceFeishuCredentialResetConfirmTitle"),
      content: t("admin.dataSourceFeishuCredentialResetConfirmContent"),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      icon: <WarningFilled />,
      onOk: () => {
        ctx.clearOauthAttempt();
        clearFeishuAppSetup();
        setFeishuAppSetup(null);
        setSelectedType((current) => (current === "feishu" ? null : current));
        setOauthState("pending");
        setConnectionVerified(false);
        setOauthConnection(null);
        message.success(t("admin.dataSourceFeishuCredentialReset"));
      },
    });
  };

  const handleResetNotionSetup = () => {
    Modal.confirm({
      title: t("admin.dataSourceNotionCredentialResetConfirmTitle"),
      content: t("admin.dataSourceNotionCredentialResetConfirmContent"),
      okText: t("common.confirm"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      icon: <WarningFilled />,
      onOk: () => {
        ctx.clearOauthAttempt();
        clearNotionAppSetup();
        setNotionAppSetup(null);
        setNotionOauthConnection(null);
        setSelectedType((current) => (current === "notion" ? null : current));
        setOauthState("pending");
        setConnectionVerified(false);
        setOauthConnection(null);
        message.success(t("admin.dataSourceNotionCredentialReset"));
      },
    });
  };

  return {
    resetWizard,
    openEditWizard,
    handleCloseWizard,
    applySourceType,
    openCloudSetupModal,
    openFeishuSetupModal,
    handleSaveFeishuSetup,
    handleResetFeishuSetup,
    handleResetNotionSetup,
  };
}
