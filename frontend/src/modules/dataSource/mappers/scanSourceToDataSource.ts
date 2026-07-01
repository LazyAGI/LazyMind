import type { TFunction } from "i18next";
import type { DataSourceItem } from "../constants/types";
import { formatDateTime, resolveParsedDocumentCount, resolveStorageUsed } from "../utils/format";
import {
  getSourceTypeDescription,
  normalizeDataSourceConnectionState,
  normalizeDataSourceStatus,
} from "../utils/status";
import {
  buildFeishuNextSyncLabel,
  buildFeishuScheduleLabel,
  buildScanNextSyncLabel,
  buildScanScheduleLabel,
  parseFeishuScheduleExpr,
} from "../utils/schedule";
import { getBindingFileTypes } from "../utils/fileTypes";
import {
  getFeishuBindingTargetTypes,
  hasFeishuTargetTypes,
  normalizeNotionTargetType,
  toUiFeishuTargetType,
} from "../utils/feishuTarget";
import {
  getBindingLastError,
  getBindingSchedule,
  getScanBindingAgentId,
  getScanBindingId,
  getScanBindingTarget,
  getScanSourceConfigVersion,
  getScanSourceDatasetId,
  getScanSourceId,
  getScanSourceName,
  getScanSourceUpdatedAt,
  getScanTenantId,
  inferSourceKind,
  type ScanV2Binding,
  type ScanV2Source,
} from "../utils/scanAccessors";

export function mapScanSourceToDataSource(
  source: ScanV2Source,
  t: TFunction,
  fallback?: DataSourceItem,
  binding: ScanV2Binding | null = null,
  bindings: ScanV2Binding[] = binding ? [binding] : [],
): DataSourceItem {
  const summary = (source.summary || {}) as Record<string, any>;
  const sourceKind = inferSourceKind(source, binding);
  const isFeishuSource = sourceKind === "feishu";
  const isNotionSource = sourceKind === "notion";
  const sourceId = getScanSourceId(source);
  const sourceName = getScanSourceName(source);
  const targetRef = getScanBindingTarget(binding);
  const targetRefs = bindings.map(getScanBindingTarget).filter(Boolean);
  const targetLabel =
    targetRefs.length > 1 ? targetRefs.join("、") : targetRef || fallback?.target || "-";
  const sourceStatus = normalizeDataSourceStatus(
    binding?.status || source.status,
    isFeishuSource ? true : binding?.sync_mode !== "manual",
  );
  const connectionState = normalizeDataSourceConnectionState(binding?.status || source.status);
  const currentTime = formatDateTime(binding?.updated_at || getScanSourceUpdatedAt(source));
  const detailDocuments = fallback?.detailDocuments || [];
  const fileCandidates = fallback?.fileCandidates || [];
  const documentCount =
    summary?.document_objects ??
    summary?.total_objects ??
    summary?.total_document_count ??
    fallback?.documentCount ??
    0;
  const addCount = summary?.new_count ?? fallback?.addCount ?? 0;
  const deleteCount = summary?.deleted_count ?? fallback?.deleteCount ?? 0;
  const changeCount = summary?.modified_count ?? fallback?.changeCount ?? 0;
  const parsedDocumentCount = resolveParsedDocumentCount(
    summary,
    fallback?.parsedDocumentCount ?? 0,
  );
  const storageUsed = resolveStorageUsed(summary, fallback?.storageUsed);
  const fileTypes = getBindingFileTypes(binding, fallback?.fileTypes);

  if (isFeishuSource) {
    const bindingTargetTypes = getFeishuBindingTargetTypes(bindings);
    const targetTypes = hasFeishuTargetTypes(bindingTargetTypes)
      ? bindingTargetTypes
      : fallback?.targetTypes;

    return {
      id: sourceId,
      name: sourceName,
      type: "feishu",
      knowledgeBase: sourceName,
      description: t("admin.dataSourceTypeFeishuDesc"),
      target: targetLabel,
      syncMode: parseFeishuScheduleExpr(getBindingSchedule(binding)) ? "scheduled" : "manual",
      scheduleLabel: buildFeishuScheduleLabel(binding, t),
      status: sourceStatus,
      connectionState,
      lastSync: currentTime,
      nextSync: buildFeishuNextSyncLabel(binding, t),
      documentCount,
      parsedDocumentCount,
      addCount,
      deleteCount,
      changeCount,
      permissions: [t("admin.dataSourcePermissionReadOnly")],
      conflictPolicy: "versioned",
      enabled: Boolean(binding?.enabled ?? true),
      scopeMode: "all",
      selectedFiles: [],
      fileTypes,
      fileCandidates,
      logs: [
        {
          id: `scan-log-${sourceId}-${binding?.updated_at || getScanSourceUpdatedAt(source)}`,
          time: currentTime,
          result:
            sourceStatus === "error"
              ? "failed"
              : sourceStatus === "paused"
                ? "warning"
                : "success",
          title:
            sourceStatus === "error"
              ? t("admin.dataSourceStatusError")
              : t("admin.dataSourceConnectionConnected"),
          description:
            getBindingLastError(binding) ||
            (parseFeishuScheduleExpr(getBindingSchedule(binding))
              ? t("admin.dataSourceSyncModeScheduledDesc")
              : t("admin.dataSourceSyncModeManualDesc")),
        },
      ],
      warning: getBindingLastError(binding) || t("admin.dataSourceReadonlyPermissionHint"),
      oauthConnection:
        fallback?.oauthConnection &&
        fallback.oauthConnection.connectionId === binding?.auth_connection_id
          ? fallback.oauthConnection
          : null,
      agentId: getScanBindingAgentId(binding),
      tenantId: source.tenant_id || getScanTenantId(),
      scanManaged: true,
      storageUsed,
      detailDocuments,
      rootPath: targetRef,
      targetRef: targetRef || fallback?.targetRef,
      targetRefs: targetRefs.length > 0 ? targetRefs : fallback?.targetRefs,
      targetType: toUiFeishuTargetType(binding?.target_type) || fallback?.targetType,
      targetTypes,
      authConnectionId: binding?.auth_connection_id || fallback?.authConnectionId,
      datasetId: getScanSourceDatasetId(source),
      bindingId: getScanBindingId(binding),
      bindingIds: bindings.map(getScanBindingId).filter(Boolean),
      bindingTreeKey: binding?.tree_key,
      bindingTreeKeys: bindings.map((item) => item.tree_key).filter(Boolean),
      configVersion: getScanSourceConfigVersion(source),
    };
  }

  if (isNotionSource) {
    return {
      id: sourceId,
      name: sourceName,
      type: "notion",
      knowledgeBase: sourceName,
      description: getSourceTypeDescription("notion", t),
      target: targetLabel,
      syncMode:
        binding?.sync_mode === "scheduled" || binding?.sync_mode === "watch"
          ? "scheduled"
          : "manual",
      scheduleLabel: buildScanScheduleLabel(binding, t),
      status: sourceStatus,
      connectionState,
      lastSync: currentTime,
      nextSync: buildScanNextSyncLabel(binding, t),
      documentCount,
      parsedDocumentCount,
      addCount,
      deleteCount,
      changeCount,
      permissions: [t("admin.dataSourcePermissionReadOnly")],
      conflictPolicy: "versioned",
      enabled: Boolean(binding?.enabled ?? true),
      scopeMode: "all",
      selectedFiles: [],
      fileCandidates,
      logs: [
        {
          id: `scan-log-${sourceId}-${binding?.updated_at || getScanSourceUpdatedAt(source)}`,
          time: currentTime,
          result:
            sourceStatus === "error"
              ? "failed"
              : sourceStatus === "paused"
                ? "warning"
                : "success",
          title:
            sourceStatus === "error"
              ? t("admin.dataSourceStatusError")
              : t("admin.dataSourceConnectionConnected"),
          description:
            getBindingLastError(binding) ||
            (binding?.sync_mode === "scheduled" || binding?.sync_mode === "watch"
              ? t("admin.dataSourceSyncModeScheduledDesc")
              : t("admin.dataSourceSyncModeManualDesc")),
        },
      ],
      warning: getBindingLastError(binding) || t("admin.dataSourceReadonlyPermissionHint"),
      oauthConnection:
        fallback?.oauthConnection &&
        fallback.oauthConnection.connectionId === binding?.auth_connection_id
          ? fallback.oauthConnection
          : null,
      agentId: getScanBindingAgentId(binding),
      tenantId: source.tenant_id || getScanTenantId(),
      scanManaged: true,
      storageUsed,
      detailDocuments,
      rootPath: targetRef,
      targetRef: targetRef || fallback?.targetRef,
      targetRefs: targetRefs.length > 0 ? targetRefs : fallback?.targetRefs,
      targetType: normalizeNotionTargetType(binding?.target_type) || fallback?.targetType,
      authConnectionId: binding?.auth_connection_id || fallback?.authConnectionId,
      datasetId: getScanSourceDatasetId(source),
      bindingId: getScanBindingId(binding),
      bindingIds: bindings.map(getScanBindingId).filter(Boolean),
      bindingTreeKey: binding?.tree_key,
      bindingTreeKeys: bindings.map((item) => item.tree_key).filter(Boolean),
      configVersion: getScanSourceConfigVersion(source),
    };
  }

  return {
    id: sourceId,
    name: sourceName,
    type: "local",
    knowledgeBase: sourceName,
    description: t("admin.dataSourceTypeLocalDesc"),
    target: targetLabel,
    syncMode:
      binding?.sync_mode === "scheduled" || binding?.sync_mode === "watch"
        ? "scheduled"
        : "manual",
    scheduleLabel: buildScanScheduleLabel(binding, t),
    status: sourceStatus,
    connectionState,
    lastSync: currentTime,
    nextSync: buildScanNextSyncLabel(binding, t),
    documentCount,
    parsedDocumentCount,
    addCount,
    deleteCount,
    changeCount,
    permissions: [t("admin.dataSourcePermissionReadOnly")],
    conflictPolicy: "overwrite",
    enabled: sourceStatus === "active",
    scopeMode: "all",
    selectedFiles: [],
    fileTypes,
    fileCandidates,
    logs: [
      {
        id: `scan-log-${sourceId}-${getScanSourceUpdatedAt(source)}`,
        time: currentTime,
        result:
          sourceStatus === "error"
            ? "failed"
            : sourceStatus === "paused"
              ? "warning"
              : "success",
        title:
          sourceStatus === "error"
            ? t("admin.dataSourceStatusError")
            : t("admin.dataSourceConnectionConnected"),
        description:
          binding?.sync_mode === "scheduled" || binding?.sync_mode === "watch"
            ? t("admin.dataSourceSyncModeScheduledDesc")
            : t("admin.dataSourceSyncModeManualDesc"),
      },
    ],
    warning: t("admin.dataSourceReadonlyPermissionHint"),
    oauthConnection: null,
    agentId: getScanBindingAgentId(binding),
    tenantId: source.tenant_id || getScanTenantId(),
    scanManaged: true,
    storageUsed,
    detailDocuments,
    rootPath: targetRef,
    targetRef,
    targetRefs: targetRefs.length > 0 ? targetRefs : fallback?.targetRefs,
    targetType: toUiFeishuTargetType(binding?.target_type),
    datasetId: getScanSourceDatasetId(source),
    bindingId: getScanBindingId(binding),
    bindingIds: bindings.map(getScanBindingId).filter(Boolean),
    bindingTreeKey: binding?.tree_key,
    bindingTreeKeys: bindings.map((item) => item.tree_key).filter(Boolean),
    configVersion: getScanSourceConfigVersion(source),
  };
}
