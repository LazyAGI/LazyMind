import { message } from "antd";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "../../api/clients";
import {
  FEISHU_EXCLUDE_PATTERNS,
  FEISHU_MAX_OBJECT_SIZE_BYTES,
} from "../../constants/options";
import type { SourceFormValues } from "../../constants/types";
import { getSourceTypeTitle } from "../../utils/status";
import { createScanRequestId } from "../../utils/scanAccessors";
import { buildSchedulePolicy } from "../../utils/schedule";
import {
  getDataSourceFileTypeExtensions,
  getDataSourceFileTypeIncludePatterns,
} from "../../utils/fileTypes";
import {
  collectFeishuTargetRefs,
  collectFeishuTargetTypes,
  normalizeCloudTargetRefs,
  normalizeFeishuTargetRefs,
  normalizeFeishuTargetType,
  normalizeFeishuTargetTypeRecord,
  normalizeLocalPathRefs,
  normalizeNotionTargetType,
  resolveSourceTypeFromValues,
  toScanFeishuTargetType,
} from "../../utils/feishuTarget";
import { pickScanAgent, waitForCloudSyncRun } from "../../utils/cloudSync";
import { isKnowledgeBaseNameDuplicatedError } from "../../utils/dataSourceErrors";
import type { DataSourceSaveMode, ManagementContext } from "./context";

export function createSaveActions(ctx: ManagementContext) {
  const { t, form, scanAgents } = ctx;

  const markKnowledgeBaseNameDuplicated = () => {
    form.setFields([
      {
        name: "knowledgeBase",
        errors: [t("admin.dataSourceKnowledgeBaseNameDuplicated")],
      },
    ]);
    form.scrollToField("knowledgeBase", { block: "center" });
  };

  const handleSaveLocalSource = async (
    values: SourceFormValues,
    saveMode: DataSourceSaveMode,
  ) => {
    const rootPaths = normalizeLocalPathRefs(values.path);
    const sourceName = `${values.knowledgeBase || getSourceTypeTitle("local", t)}`.trim();
    const isScheduled = (values.syncMode || "scheduled") === "scheduled";
    const schedulePolicy = isScheduled
      ? buildSchedulePolicy(values.scheduleWeekdays, values.scheduleTime)
      : undefined;
    const includeExtensions = getDataSourceFileTypeExtensions(values.fileTypes);
    const includePatterns = getDataSourceFileTypeIncludePatterns(values.fileTypes);
    const currentLocalSource =
      ctx.editingId && ctx.selectedType === "local"
        ? ctx.sources.find((item) => item.id === ctx.editingId && item.type === "local")
        : undefined;
    let datasetIdForLocalSource = currentLocalSource?.datasetId || "";

    if (rootPaths.length === 0) {
      message.warning(t("admin.dataSourceAccessPathRequired"));
      return;
    }

    const client = dataSourceScanApi;
    const selectedAgent = pickScanAgent(
      scanAgents,
      ctx.validatedAgentId || currentLocalSource?.agentId,
    );
    const buildBindingRequest = (targetRef: string) => ({
      connector_type: "local_fs",
      target_type: "local_path",
      target_ref: targetRef,
      sync_mode: isScheduled ? "scheduled" : "manual",
      schedule_policy: schedulePolicy,
      agent_id: selectedAgent?.agent_id || ctx.validatedAgentId || currentLocalSource?.agentId,
      include_extensions: includeExtensions,
      provider_options: {
        include_patterns: includePatterns,
      },
    });

    try {
      if (currentLocalSource?.scanManaged) {
        await client.updateSource({
          sourceId: currentLocalSource.id,
          updateSourceRequest: {
            name: sourceName,
            config_version: currentLocalSource.configVersion || 0,
            bindings: rootPaths.map((pathValue, index) => ({
              ...buildBindingRequest(pathValue),
              binding_id:
                currentLocalSource.bindingIds?.[index] ||
                (index === 0 ? currentLocalSource.bindingId : undefined),
            })) as any,
            source_options: {
              source_type: "local",
            },
          },
        });
      } else {
        const createSourceResponse = await client.createSource({
          createSourceRequest: {
            request_id: createScanRequestId("local-source"),
            name: sourceName,
            bindings: rootPaths.map((pathValue) => buildBindingRequest(pathValue)) as any,
            source_options: {
              source_type: "local",
              dataset_id: datasetIdForLocalSource,
            },
          },
        });
        datasetIdForLocalSource = createSourceResponse.data.source.dataset_id || "";
        const sourceId = createSourceResponse.data.source.source_id || "";
        if (saveMode === "createAndSync" && sourceId) {
          await client.triggerSourceSync({
            sourceId,
            triggerSourceSyncRequest: {
              request_id: createScanRequestId("local-sync"),
              scope_type: "full",
              scope_ref: {},
            },
          });
        }
      }

      ctx.setValidatedAgentId(selectedAgent?.agent_id || ctx.validatedAgentId);
      await ctx.refreshSources(false);
      message.success(
        ctx.editingId ? t("admin.dataSourceConfigUpdated") : t("admin.dataSourceCreated"),
      );
      ctx.handleCloseWizard();
    } catch (error) {
      if (isKnowledgeBaseNameDuplicatedError(error)) {
        markKnowledgeBaseNameDuplicated();
        return;
      }

      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    }
  };

  const handleSaveFeishuSource = async (
    values: SourceFormValues,
    saveMode: DataSourceSaveMode,
  ) => {
    const sourceName = `${values.knowledgeBase || getSourceTypeTitle("feishu", t)}`.trim();
    const selectedTargetValues = normalizeFeishuTargetRefs(values.target);
    const currentFeishuSource =
      ctx.editingId && ctx.selectedType === "feishu"
        ? ctx.sources.find((item) => item.id === ctx.editingId && item.type === "feishu")
        : undefined;

    const authConnectionId =
      ctx.oauthConnection?.provider === "feishu" && ctx.oauthConnection.connectionId
        ? ctx.oauthConnection.connectionId
        : ctx.wizardMode === "edit"
          ? currentFeishuSource?.authConnectionId
          : "";

    if (selectedTargetValues.length === 0) {
      message.warning(t("admin.dataSourceFeishuSpaceRequired"));
      return;
    }

    const client = dataSourceScanApi;
    const selectedAgent = pickScanAgent(
      scanAgents,
      ctx.validatedAgentId || currentFeishuSource?.agentId,
    );
    const treeTargetTypeMap = collectFeishuTargetTypes(ctx.feishuTargetTreeData);
    const treeTargetRefMap = collectFeishuTargetRefs(ctx.feishuTargetTreeData);
    const fallbackTargetTypes = normalizeFeishuTargetTypeRecord(currentFeishuSource?.targetTypes);
    const defaultTargetType =
      normalizeFeishuTargetType(currentFeishuSource?.targetType) ||
      normalizeFeishuTargetType(values.targetType) ||
      "wiki_space";
    const targets = selectedTargetValues.map((targetValue) => {
      const targetRef = treeTargetRefMap.get(targetValue) || targetValue;
      return {
        targetRef,
        targetType:
          treeTargetTypeMap.get(targetValue) ||
          treeTargetTypeMap.get(targetRef) ||
          fallbackTargetTypes?.[targetRef] ||
          normalizeFeishuTargetType(undefined, targetRef) ||
          defaultTargetType,
      };
    });

    try {
      let sourceId = currentFeishuSource?.id || "";
      const schedulePolicy =
        values.syncMode === "scheduled"
          ? buildSchedulePolicy(values.scheduleWeekdays, values.scheduleTime)
          : undefined;
      const includeExtensions = getDataSourceFileTypeExtensions(values.fileTypes);
      const includePatterns = getDataSourceFileTypeIncludePatterns(values.fileTypes);
      const bindingRequest = {
        connector_type: "feishu",
        sync_mode: values.syncMode === "scheduled" ? "scheduled" : "manual",
        schedule_policy: schedulePolicy,
        auth_connection_id: authConnectionId,
        include_extensions: includeExtensions,
        provider_options: {
          include_extensions: includeExtensions,
          include_patterns: includePatterns,
          exclude_patterns: FEISHU_EXCLUDE_PATTERNS,
          max_object_size_bytes: FEISHU_MAX_OBJECT_SIZE_BYTES,
          reconcile_after_sync: true,
          reconcile_delay_minutes: 10,
        },
      };

      if (currentFeishuSource?.scanManaged) {
        await client.updateSource({
          sourceId: currentFeishuSource.id,
          updateSourceRequest: {
            name: sourceName,
            config_version: currentFeishuSource.configVersion || 0,
            bindings: targets.map(({ targetRef, targetType }, index) => ({
              ...bindingRequest,
              target_type: toScanFeishuTargetType(targetType),
              target_ref: targetRef,
              binding_id:
                currentFeishuSource.bindingIds?.[index] ||
                (index === 0 ? currentFeishuSource.bindingId : undefined),
            })) as any,
            source_options: {
              source_type: "feishu",
              auth_connection_id: authConnectionId,
            },
          },
        });
      } else {
        const createSourceResponse = await client.createSource({
          createSourceRequest: {
            request_id: createScanRequestId("feishu-source"),
            name: sourceName,
            bindings: targets.map(({ targetRef, targetType }) => ({
              ...bindingRequest,
              target_type: toScanFeishuTargetType(targetType),
              target_ref: targetRef,
            })) as any,
            source_options: {
              source_type: "feishu",
              auth_connection_id: authConnectionId,
            },
          },
        });

        sourceId = createSourceResponse.data.source.source_id || "";
      }

      if (!sourceId) {
        message.error(t("admin.dataSourceCreateMissingSourceId"));
        return;
      }

      if (saveMode === "createAndSync") {
        message.info(t("admin.dataSourceDetailCloudSyncPreparing"));
        const triggerResponse = await client.triggerSourceSync({
          sourceId,
          triggerSourceSyncRequest: {
            request_id: createScanRequestId("feishu-sync"),
            scope_type: "full",
            scope_ref: {},
          },
        });
        await waitForCloudSyncRun(client, sourceId, t, triggerResponse.data.run_ids || []);
      }

      ctx.setValidatedAgentId(selectedAgent?.agent_id || ctx.validatedAgentId);
      await ctx.refreshSources(false);
      message.success(
        ctx.editingId ? t("admin.dataSourceConfigUpdated") : t("admin.dataSourceCreated"),
      );
      ctx.handleCloseWizard();
    } catch (error) {
      if (isKnowledgeBaseNameDuplicatedError(error)) {
        markKnowledgeBaseNameDuplicated();
        return;
      }

      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    }
  };

  const handleSaveNotionSource = async (
    values: SourceFormValues,
    saveMode: DataSourceSaveMode,
  ) => {
    const sourceName = `${values.knowledgeBase || getSourceTypeTitle("notion", t)}`.trim();
    const targetRefs = normalizeCloudTargetRefs(values.target);
    const currentNotionSource =
      ctx.editingId && ctx.selectedType === "notion"
        ? ctx.sources.find((item) => item.id === ctx.editingId && item.type === "notion")
        : undefined;
    const authConnectionId =
      ctx.oauthConnection?.provider === "notion" && ctx.oauthConnection.connectionId
        ? ctx.oauthConnection.connectionId
        : ctx.wizardMode === "edit"
          ? currentNotionSource?.authConnectionId
          : "";
    const targetType =
      normalizeNotionTargetType(`${values.targetType || ""}`) ||
      normalizeNotionTargetType(currentNotionSource?.targetType) ||
      "page";

    if (!authConnectionId) {
      message.warning(t("admin.dataSourceNotionAuthRequired"));
      return;
    }

    if (targetRefs.length === 0) {
      message.warning(t("admin.dataSourceNotionTargetRequired"));
      return;
    }

    const client = dataSourceScanApi;
    const selectedAgent = pickScanAgent(
      scanAgents,
      ctx.validatedAgentId || currentNotionSource?.agentId,
    );

    try {
      let sourceId = currentNotionSource?.id || "";
      const schedulePolicy =
        values.syncMode === "scheduled"
          ? buildSchedulePolicy(values.scheduleWeekdays, values.scheduleTime)
          : undefined;
      const bindingRequest = {
        connector_type: "notion",
        sync_mode: values.syncMode === "scheduled" ? "scheduled" : "manual",
        schedule_policy: schedulePolicy,
        auth_connection_id: authConnectionId,
        agent_id: selectedAgent?.agent_id || ctx.validatedAgentId || currentNotionSource?.agentId,
        provider_options: {
          reconcile_after_sync: true,
          reconcile_delay_minutes: 10,
        },
      };

      if (currentNotionSource?.scanManaged) {
        await client.updateSource({
          sourceId: currentNotionSource.id,
          updateSourceRequest: {
            name: sourceName,
            config_version: currentNotionSource.configVersion || 0,
            bindings: targetRefs.map((targetRef, index) => ({
              ...bindingRequest,
              target_type: targetType,
              target_ref: targetRef,
              binding_id:
                currentNotionSource.bindingIds?.[index] ||
                (index === 0 ? currentNotionSource.bindingId : undefined),
            })) as any,
            source_options: {
              source_type: "notion",
              auth_connection_id: authConnectionId,
            },
          },
        });
      } else {
        const createSourceResponse = await client.createSource({
          createSourceRequest: {
            request_id: createScanRequestId("notion-source"),
            name: sourceName,
            bindings: targetRefs.map((targetRef) => ({
              ...bindingRequest,
              target_type: targetType,
              target_ref: targetRef,
            })) as any,
            source_options: {
              source_type: "notion",
              auth_connection_id: authConnectionId,
            },
          },
        });
        sourceId = createSourceResponse.data.source.source_id || "";
      }

      if (!sourceId) {
        message.error(t("admin.dataSourceNotionSourceCreationFailed"));
        return;
      }

      if (saveMode === "createAndSync") {
        message.info(t("admin.dataSourceDetailCloudSyncPreparing"));
        const triggerResponse = await client.triggerSourceSync({
          sourceId,
          triggerSourceSyncRequest: {
            request_id: createScanRequestId("notion-sync"),
            scope_type: "full",
            scope_ref: {},
          },
        });
        await waitForCloudSyncRun(client, sourceId, t, triggerResponse.data.run_ids || []);
      }

      ctx.setValidatedAgentId(selectedAgent?.agent_id || ctx.validatedAgentId);
      await ctx.refreshSources(false);
      message.success(
        ctx.editingId ? t("admin.dataSourceConfigUpdated") : t("admin.dataSourceCreated"),
      );
      ctx.handleCloseWizard();
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    }
  };

  const handleSave = async (saveMode: DataSourceSaveMode = "createAndSync") => {
    if (ctx.wizardSaving) {
      return;
    }

    ctx.setWizardSaving(true);
    ctx.setWizardSavingMode(saveMode);
    try {
      const syncStrategyFields =
        form.getFieldValue("syncMode") === "scheduled"
          ? ["syncMode", "scheduleWeekdays", "scheduleTime", "fileTypes"]
          : ["syncMode", "fileTypes"];

      if (ctx.wizardMode === "edit") {
        await form.validateFields(syncStrategyFields);
      } else {
        await form.validateFields();
      }

      const values = form.getFieldsValue(true);
      const effectiveSourceType = resolveSourceTypeFromValues(ctx.selectedType, values);

      if (!effectiveSourceType) {
        message.warning(t("admin.dataSourceSelectTypeFirst"));
        return;
      }
      if (effectiveSourceType === "local" && !ctx.canCreateLocalSource) {
        message.error(t("admin.dataSourceAdminOnly"));
        return;
      }

      if (effectiveSourceType === "local") {
        await handleSaveLocalSource(values, saveMode);
        return;
      }
      if (effectiveSourceType === "notion") {
        await handleSaveNotionSource(values, saveMode);
        return;
      }
      await handleSaveFeishuSource(values, saveMode);
    } finally {
      ctx.setWizardSaving(false);
      ctx.setWizardSavingMode(null);
    }
  };

  return { handleSave };
}
