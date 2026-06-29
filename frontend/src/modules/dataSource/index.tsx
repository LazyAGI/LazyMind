import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  Alert,
  Button,
  Form,
  Input,
  Modal,
  Space,
  Tag,
  Table,
  Typography,
  Tooltip,
  message,
} from "antd";
import type { TreeSelectProps } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { DataNode } from "antd/es/tree";
import {
  ApiOutlined,
  ArrowRightOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  EyeOutlined,
  FileTextOutlined,
  FolderOpenOutlined,
  PlusOutlined,
  SearchOutlined,
  WarningFilled,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useNavigate } from "react-router-dom";
import {
  type CloudConnectionResponse,
  type CloudOAuthAppCredentialBody,
} from "@/api/generated/auth-client";
import { AgentAppsAuth } from "@/components/auth";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  dataSourceCloudOauthApi,
  getLocalFSChatSetting,
  updateLocalFSChatSetting,
} from "./api";

import "./index.scss";
import DataSourceWizardModal from "./components/DataSourceWizardModal";
import {
  clearFeishuAppSetup,
  createFeishuAccountId,
  getOAuthStateFromConnection,
  loadFeishuAppSetup,
  loadFeishuAuthAccounts,
  persistFeishuAppSetup,
  persistFeishuAuthAccounts,
  type FeishuAccountFormValues,
  type FeishuAuthAccount,
} from "./common/feishuAccounts";
import { FeishuCredentialHintAlertFromForm } from "./common/FeishuCredentialHintAlert";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  clearFeishuDataSourceWizardDraft,
  consumeCloudDataSourceOAuthResult,
  consumeFeishuDataSourceOAuthResult,
  consumeFeishuDataSourceWizardDraft,
  enableCloudConnectionForChat,
  finishFeishuDataSourceOAuth,
  requestCloudDataSourceAuthorizeUrl,
  openCenteredPopup,
  requestFeishuDataSourceAuthorizeUrl,
  saveFeishuDataSourceWizardDraft,
  type CloudDataSourceProvider,
  type FeishuConnectionStatus,
  type FeishuDataSourceConnection,
  type FeishuDataSourceOAuthMessage,
  type FeishuDataSourceWizardDraft,
} from "@/modules/dataSource/common/feishuOAuth";
import {
  CLOUD_SYNC_POLL_INTERVAL_MS,
  CLOUD_SYNC_TIMEOUT_MS,
  DATA_SOURCE_FILE_TYPE_OPTIONS,
  DEFAULT_DATA_SOURCE_FILE_TYPES,
  FEISHU_DEFAULT_SCOPES,
  FEISHU_EXCLUDE_PATTERNS,
  FEISHU_MAX_OBJECT_SIZE_BYTES,
  NOTION_APP_SETUP_STORAGE_KEY,
  type DataSourceItem,
  type DetailDocumentItem,
  type DataSourceFileType,
  type FeishuAppSetup,
  type FeishuTargetType,
  type NotionTargetType,
  type FileUpdateState,
  type OAuthState,
  type PendingOAuthAttempt,
  type SourceFormValues,
  type SourceType,
  formatDateTime,
  getConnectionMeta,
  getSourceTypeDescription,
  getSourceTypeTitle,
  getStatusMeta,
  getSyncModeLabel,
  normalizeDataSourceConnectionState,
  normalizeDataSourceStatus,
  resolveParsedDocumentCount,
  resolveStorageUsed,
} from "./shared";
import {
  createScanRequestId,
  createScanV2ApiClient,
  getBindingLastError,
  getBindingSchedule,
  getFirstScanBinding,
  getScanBindingAgentId,
  getScanBindingId,
  getScanBindingTarget,
  getScanSourceConfigVersion,
  getScanSourceDatasetId,
  getScanSourceId,
  getScanSourceName,
  getScanSourceUpdatedAt,
  getScanTenantId,
  getScanTreeNodePath,
  inferSourceKind,
  type ScanV2AgentHint,
  type ScanV2Binding,
  type ScanV2Client,
  type ScanV2Source,
  type ScanV2TreeNode,
} from "./scanV2Api";

const { Paragraph, Text } = Typography;
const DEFAULT_SCHEDULE_TIME = "02:00:00";
const SCHEDULE_TIME_PATTERN = /^([01]\d|2[0-3]):[0-5]\d:[0-5]\d$/;
const LOCAL_PATH_CACHE_ROOT_KEY = "__root__";
const FEISHU_TARGET_CACHE_ROOT_KEY = "__root__";
const FEISHU_MANUAL_TARGET_VALUE_PREFIX = "__scan-feishu-manual-target__";
const FEISHU_DRIVE_ROOT_REF = "feishu:drive:root";
const FEISHU_WIKI_SPACES_REF = "feishu:wiki:spaces";
const DATA_SOURCE_LIST_DEFAULT_PAGE_SIZE = 10;
const DEFAULT_SCHEDULE_WEEKDAYS = ["1", "2", "3", "4", "5", "6", "7"];
const SCHEDULE_WEEKDAY_API_MAP: Record<string, string> = {
  "1": "mon",
  "2": "tue",
  "3": "wed",
  "4": "thu",
  "5": "fri",
  "6": "sat",
  "7": "sun",
};
type DataSourceView = "assets" | "connectors";
type FeishuSetupIntent = "create" | "auth" | null;
type CloudSetupIntent = FeishuSetupIntent;
type DataSourceSaveMode = "create" | "createAndSync";
type FeishuManualTargetKind = "current" | "wiki" | "drive";
type FeishuTargetTreeNode = DataNode & {
  value: string;
  nodeRef?: string;
  targetRef?: string;
  targetType?: FeishuTargetType;
  children?: FeishuTargetTreeNode[];
};
type LocalPathTreeNode = DataNode & {
  value: string;
  nodeRef?: string;
  targetRef?: string;
  childrenLoaded?: boolean;
  children?: LocalPathTreeNode[];
};

function normalizeScheduleTime(scheduleTime?: string) {
  const value = `${scheduleTime || ""}`.trim();
  const minutePrecisionMatch = value.match(/^([01]\d|2[0-3]):[0-5]\d$/);
  if (minutePrecisionMatch) {
    return `${value}:00`;
  }
  return SCHEDULE_TIME_PATTERN.test(value) ? value : DEFAULT_SCHEDULE_TIME;
}

function normalizeScheduleWeekdays(scheduleWeekdays?: string[]) {
  const uniqueDays = Array.from(
    new Set((scheduleWeekdays || []).map((day) => `${day}`.trim())),
  ).filter((day) => /^[1-7]$/.test(day));
  if (uniqueDays.length === 0) {
    return DEFAULT_SCHEDULE_WEEKDAYS;
  }
  return uniqueDays.sort((left, right) => Number(left) - Number(right));
}

function buildSchedulePolicy(scheduleWeekdays?: string[], scheduleTime?: string) {
  const weekdays = normalizeScheduleWeekdays(scheduleWeekdays);
  const days =
    weekdays.length === DEFAULT_SCHEDULE_WEEKDAYS.length
      ? ["everyday"]
      : weekdays.map((day) => SCHEDULE_WEEKDAY_API_MAP[day]).filter(Boolean);
  return {
    timezone: "Asia/Shanghai",
    calendar: "weekly",
    rules: [
      {
        days,
        time: normalizeScheduleTime(scheduleTime),
      },
    ],
  };
}

function resolveSourceTypeFromValues(
  fallbackType: SourceType | null,
  values: SourceFormValues,
): SourceType | null {
  const localPaths = normalizeLocalPathRefs(values.path);
  const feishuTargets = normalizeFeishuTargetRefs(values.target);
  const notionTargets = normalizeCloudTargetRefs(values.target);
  if (localPaths.length > 0 && feishuTargets.length === 0) {
    return "local";
  }
  if (fallbackType === "notion" && notionTargets.length > 0 && localPaths.length === 0) {
    return "notion";
  }
  if (feishuTargets.length > 0 && localPaths.length === 0) {
    return "feishu";
  }
  return fallbackType;
}

function loadNotionAppSetup(): FeishuAppSetup | null {
  try {
    const raw = localStorage.getItem(NOTION_APP_SETUP_STORAGE_KEY);
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as Partial<FeishuAppSetup>;
    const appId = typeof parsed.appId === "string" ? parsed.appId.trim() : "";
    const appSecret =
      typeof parsed.appSecret === "string" ? parsed.appSecret.trim() : "";
    if (!appId || !appSecret) {
      return null;
    }
    return { appId, appSecret };
  } catch {
    return null;
  }
}

function persistNotionAppSetup(setup: FeishuAppSetup) {
  localStorage.setItem(NOTION_APP_SETUP_STORAGE_KEY, JSON.stringify(setup));
}

function clearNotionAppSetup() {
  localStorage.removeItem(NOTION_APP_SETUP_STORAGE_KEY);
}

const sourceTypeOptions: Array<{
  type: SourceType;
  icon: ReactNode;
  logoUrl?: string;
  adminOnly?: boolean;
}> = [
  {
    type: "local",
    icon: <FolderOpenOutlined />,
    adminOnly: true,
  },
  {
    type: "feishu",
    icon: <ApiOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=feishu.cn&sz=96",
  },
  {
    type: "notion",
    icon: <DatabaseOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=notion.so&sz=96",
  },
];
const providerAuthOptions = sourceTypeOptions.filter(
  (item) => item.type === "feishu" || item.type === "notion",
);

function isAdminRole(role?: string) {
  const normalizedRole = (role || "").trim().toLowerCase();
  return (
    normalizedRole === "admin" ||
    normalizedRole === "system-admin" ||
    normalizedRole === "system_admin" ||
    normalizedRole.endsWith(".admin")
  );
}

function normalizeFeishuAccountStatus(status?: string): FeishuConnectionStatus {
  const normalized = `${status || ""}`.trim().toLowerCase();
  if (["active", "connected", "success", "succeeded", "enabled"].includes(normalized)) {
    return "connected";
  }
  if (["expired", "inactive"].includes(normalized)) {
    return "expired";
  }
  if (["error", "failed", "failure", "invalid"].includes(normalized)) {
    return "error";
  }
  return "pending";
}

function splitFeishuScopes(value?: string | null) {
  return `${value || ""}`
    .split(/[,\s]+/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function getCloudConnectionItems(payload: unknown): CloudConnectionResponse[] {
  const responsePayload = payload as {
    items?: CloudConnectionResponse[];
    data?: { items?: CloudConnectionResponse[] };
  };

  if (Array.isArray(responsePayload.items)) {
    return responsePayload.items;
  }
  if (Array.isArray(responsePayload.data?.items)) {
    return responsePayload.data.items;
  }
  return [];
}

function mapCloudConnectionToFeishuAccount(
  connection: CloudConnectionResponse,
  cachedAccounts: FeishuAuthAccount[],
): FeishuAuthAccount {
  const providerMeta = connection.provider_account_meta || {};
  const cachedAccount =
    cachedAccounts.find((item) => item.connection?.connectionId === connection.connection_id) ||
    cachedAccounts.find(
      (item) =>
        item.appId &&
        (item.appId === providerMeta.client_id ||
          item.appId === providerMeta.app_id ||
          item.appId === connection.provider_account_id),
    );
  const appId = `${providerMeta.client_id || providerMeta.app_id || cachedAccount?.appId || connection.provider_account_id || connection.connection_id}`;
  const displayName =
    connection.display_name ||
    providerMeta.name ||
    providerMeta.display_name ||
    providerMeta.tenant_name ||
    cachedAccount?.name ||
    appId;
  const status = normalizeFeishuAccountStatus(connection.status);
  const providerOptions = connection.provider_options || {};
  const serverChatEnabled =
    providerOptions.chat_enabled ?? providerOptions.chatEnabled ??
    providerMeta.chat_enabled ?? providerMeta.chatEnabled;
  const rawChatEnabled =
    serverChatEnabled != null ? Boolean(serverChatEnabled) : (cachedAccount?.chatEnabled ?? false);

  return {
    id: connection.connection_id,
    name: displayName,
    appId,
    appSecret: cachedAccount?.appSecret || "",
    chatEnabled: status === "connected" ? rawChatEnabled : false,
    status,
    connection: {
      provider: "feishu",
      connectionId: connection.connection_id,
      status,
      accountName: displayName,
      grantedScopes: splitFeishuScopes(connection.scope),
      connectedAt: connection.last_used_at || connection.updated_at || connection.created_at,
      tenantKey: connection.provider_tenant_key,
      openId: connection.provider_account_id,
    },
    createdAt: connection.created_at,
    updatedAt: connection.updated_at || undefined,
    lastAuthorizedAt: connection.last_used_at || connection.updated_at || undefined,
  };
}

function mapCloudConnectionToDataSourceConnection(
  connection: CloudConnectionResponse,
  provider: CloudDataSourceProvider,
): FeishuDataSourceConnection {
  const providerMeta = connection.provider_account_meta || {};
  const status = normalizeFeishuAccountStatus(connection.status);
  const accountName =
    connection.display_name ||
    providerMeta.name ||
    providerMeta.display_name ||
    providerMeta.workspace_name ||
    providerMeta.tenant_name ||
    providerMeta.owner_name ||
    connection.provider_account_id ||
    connection.connection_id;

  return {
    provider,
    connectionId: connection.connection_id,
    status,
    accountName,
    grantedScopes: splitFeishuScopes(connection.scope),
    connectedAt: connection.last_used_at || connection.updated_at || connection.created_at,
    tenantKey: connection.provider_tenant_key,
    openId: connection.provider_account_id,
    avatarUrl: providerMeta.avatar_url || providerMeta.icon_url,
  };
}

function sleep(ms: number) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

async function waitForCloudSyncRun(
  client: ScanV2Client,
  sourceId: string,
  t: TFunction,
  runIds: string[] = [],
) {
  const deadline = Date.now() + CLOUD_SYNC_TIMEOUT_MS;
  const expectedRuns = Math.max(runIds.length, 1);

  while (Date.now() < deadline) {
    const [detailResponse, summaryResponse] = await Promise.all([
      client.getSource({ sourceId }).catch(() => null),
      client.getSourceSummary({ sourceId }).catch(() => null),
    ]);
    const bindings = (detailResponse?.data.bindings || []) as ScanV2Binding[];
    const summary = summaryResponse?.data as Record<string, any> | undefined;
    const errorBinding = bindings.find((item) => {
      const status = `${item.status || ""}`.toUpperCase();
      return status.includes("FAILED") || status.includes("ERROR") || status.includes("CANCEL");
    });
    const status = `${errorBinding?.status || summary?.status || ""}`.toUpperCase();
    const summaryBindings = Array.isArray(summary?.bindings)
      ? (summary.bindings as Record<string, any>[])
      : [];
    const finishedBindings = summaryBindings.filter((item) =>
      Boolean(item.last_success_at || item.lastSuccessAt),
    );

    if (
      finishedBindings.length >= expectedRuns ||
      (expectedRuns === 1 && Boolean(summary?.last_success_at || summary?.lastSuccessAt))
    ) {
      return { run_ids: runIds, status: "SUCCEEDED" };
    }

    if (
      status.includes("FAILED") ||
      status.includes("ERROR") ||
      status.includes("CANCEL")
    ) {
      throw new Error(
        getBindingLastError(errorBinding) ||
          t("admin.dataSourceDetailCloudSyncFailedFallback"),
      );
    }

    await sleep(CLOUD_SYNC_POLL_INTERVAL_MS);
  }

  throw new Error(t("admin.dataSourceDetailCloudSyncTimeout"));
}

function parseFeishuScheduleExpr(expr?: string) {
  const parsed = parseReconcileSchedule(expr);
  if (!parsed) {
    return null;
  }
  return {
    syncMode: "scheduled" as const,
    scheduleWeekdays: parsed.scheduleWeekdays,
    scheduleTime: parsed.scheduleTime,
  };
}

function normalizeFeishuTargetType(
  targetType?: string,
  targetRef?: string,
): FeishuTargetType | undefined {
  const normalizedRef = `${targetRef || ""}`.trim().toLowerCase();
  if (normalizedRef.includes("feishu:drive:") || normalizedRef === "drive") {
    return "drive_folder";
  }
  if (normalizedRef.includes("feishu:wiki:") || normalizedRef === "wiki") {
    return "wiki_space";
  }

  const normalizedType = `${targetType || ""}`.trim().toLowerCase();
  if (
    normalizedType === "drive_folder" ||
    normalizedType === "drive" ||
    normalizedType === "folder"
  ) {
    return "drive_folder";
  }
  if (
    normalizedType === "wiki_space" ||
    normalizedType === "wiki_node" ||
    normalizedType === "wiki"
  ) {
    return "wiki_space";
  }

  return undefined;
}

function toScanFeishuTargetType(targetType: FeishuTargetType) {
  return targetType === "wiki_space" ? "wiki_node" : targetType;
}

function toUiFeishuTargetType(targetType?: string): FeishuTargetType | undefined {
  return normalizeFeishuTargetType(targetType);
}

function buildManualFeishuTargetValue(
  kind: FeishuManualTargetKind,
  targetRef: string,
) {
  return `${FEISHU_MANUAL_TARGET_VALUE_PREFIX}:${kind}:${encodeURIComponent(targetRef)}`;
}

function parseManualFeishuTargetValue(value: string) {
  const normalizedValue = value.trim();
  if (!normalizedValue.startsWith(`${FEISHU_MANUAL_TARGET_VALUE_PREFIX}:`)) {
    return null;
  }

  const parts = normalizedValue.split(":");
  const rawKind = parts[1] || "";
  const encodedTargetRef = parts.slice(2).join(":");
  if (!["current", "wiki", "drive"].includes(rawKind)) {
    return null;
  }

  let targetRef = encodedTargetRef;
  try {
    targetRef = decodeURIComponent(encodedTargetRef);
  } catch {
  }

  const normalizedTargetRef = targetRef.trim();
  if (!normalizedTargetRef) {
    return null;
  }

  const kind = rawKind as FeishuManualTargetKind;
  return {
    kind,
    targetRef: normalizedTargetRef,
    targetType:
      kind === "wiki"
        ? "wiki_space"
        : kind === "drive"
          ? "drive_folder"
          : undefined,
  };
}

function normalizeNotionTargetType(value?: string): NotionTargetType | undefined {
  const normalized = `${value || ""}`.trim().toLowerCase();
  if (normalized === "database" || normalized === "notion_database") {
    return "database";
  }
  if (normalized === "page" || normalized === "notion_page") {
    return "page";
  }
  return undefined;
}

function collectFeishuTargetTypes(
  nodes: FeishuTargetTreeNode[],
  inheritedTargetType?: FeishuTargetType,
  targetTypes = new Map<string, FeishuTargetType>(),
) {
  nodes.forEach((node) => {
    const value = `${node.value || ""}`.trim();
    const targetRef = `${node.targetRef || node.value || ""}`.trim();
    const nodeRef = `${node.nodeRef || ""}`.trim();
    const targetType =
      normalizeFeishuTargetType(
        node.targetType,
        `${targetRef || nodeRef || value}`,
      ) || inheritedTargetType;

    if (targetType) {
      const refs = value.startsWith(FEISHU_MANUAL_TARGET_VALUE_PREFIX)
        ? [value]
        : [targetRef, nodeRef, value];
      refs
        .filter(Boolean)
        .forEach((ref) => {
          targetTypes.set(ref, targetType);
        });
    }

    if (node.children) {
      collectFeishuTargetTypes(node.children, targetType, targetTypes);
    }
  });

  return targetTypes;
}

function collectFeishuTargetRefs(
  nodes: FeishuTargetTreeNode[],
  targetRefs = new Map<string, string>(),
) {
  nodes.forEach((node) => {
    const value = `${node.value || ""}`.trim();
    const targetRef = `${node.targetRef || node.value || ""}`.trim();
    const nodeRef = `${node.nodeRef || ""}`.trim();

    if (targetRef) {
      [targetRef, nodeRef, value]
        .filter(Boolean)
        .forEach((ref) => {
          targetRefs.set(ref, targetRef);
        });
    }

    if (node.children) {
      collectFeishuTargetRefs(node.children, targetRefs);
    }
  });

  return targetRefs;
}

function normalizeFeishuTargetRefs(value?: SourceFormValues["target"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values
    .map((item) => {
      const normalizedValue = `${item || ""}`.trim();
      return parseManualFeishuTargetValue(normalizedValue)?.targetRef || normalizedValue;
    })
    .filter(Boolean);
}

function collectManualFeishuTargetTypes(value?: SourceFormValues["target"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  const targetTypes = new Map<string, FeishuTargetType>();

  values.forEach((item) => {
    const parsed = parseManualFeishuTargetValue(`${item || ""}`);
    if (parsed?.targetType) {
      targetTypes.set(parsed.targetRef, parsed.targetType);
    }
  });

  return targetTypes;
}

function normalizeCloudTargetRefs(value?: SourceFormValues["target"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values
    .flatMap((item) => `${item || ""}`.split(/\n+/))
    .map((item) => item.trim())
    .filter(Boolean);
}

function normalizeLocalPathRefs(value?: SourceFormValues["path"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values.map((item) => `${item || ""}`.trim()).filter(Boolean);
}

const LEGACY_DATA_SOURCE_FILE_TYPE_MAP: Record<string, DataSourceFileType[]> = {
  word: ["doc", "docx"],
  excel: ["xls", "xlsx", "csv"],
  powerpoint: ["ppt", "pptx", "pptm"],
  image: ["jpg", "jpeg", "png", "gif", "bmp", "webp", "tiff", "tif"],
  notebook: ["ipynb"],
  ebook: ["epub"],
  markdown: ["md"],
  mailbox: ["mbox"],
  audio: ["mp3"],
  video: ["mp4"],
  text: ["txt"],
};

function normalizeDataSourceFileTypes(value?: SourceFormValues["fileTypes"]) {
  const allowedTypes = new Set(DATA_SOURCE_FILE_TYPE_OPTIONS.map((item) => item.value));
  const values = Array.isArray(value) ? value : [];
  const normalizedValues = Array.from(
    new Set(
      values
        .flatMap((item) => {
          const normalizedItem = `${item || ""}`.trim().toLowerCase();
          return LEGACY_DATA_SOURCE_FILE_TYPE_MAP[normalizedItem] || normalizedItem;
        })
        .map((item) => item as DataSourceFileType)
        .filter((item) => allowedTypes.has(item)),
    ),
  );
  return normalizedValues.length > 0 ? normalizedValues : DEFAULT_DATA_SOURCE_FILE_TYPES;
}

function getDataSourceFileTypeExtensions(value?: SourceFormValues["fileTypes"]) {
  const selectedTypes = new Set(normalizeDataSourceFileTypes(value));
  return DATA_SOURCE_FILE_TYPE_OPTIONS
    .filter((item) => selectedTypes.has(item.value))
    .flatMap((item) => item.extensions);
}

function getDataSourceFileTypeIncludePatterns(value?: SourceFormValues["fileTypes"]) {
  return getDataSourceFileTypeExtensions(value).map((extension) => `**/*.${extension}`);
}

function getExtensionsFromIncludePatterns(value: unknown) {
  const patterns = Array.isArray(value) ? value : [];
  return patterns
    .map((pattern) => `${pattern || ""}`.trim().toLowerCase())
    .map((pattern) => pattern.match(/\.([a-z0-9]+)$/)?.[1] || "")
    .filter(Boolean);
}

function getBindingFileTypes(
  binding?: ScanV2Binding | null,
  fallbackTypes?: DataSourceFileType[],
) {
  const providerOptions = (binding?.provider_options || {}) as Record<string, unknown>;
  const rawExtensions = [
    ...((binding?.include_extensions || []) as string[]),
    ...((providerOptions.include_extensions || []) as string[]),
    ...getExtensionsFromIncludePatterns(providerOptions.include_patterns),
  ];
  const extensionSet = new Set(
    rawExtensions.map((extension) => `${extension || ""}`.replace(/^\./, "").toLowerCase()),
  );

  if (extensionSet.size === 0) {
    return fallbackTypes || DEFAULT_DATA_SOURCE_FILE_TYPES;
  }

  const fileTypes = DATA_SOURCE_FILE_TYPE_OPTIONS
    .filter((item) => item.extensions.some((extension) => extensionSet.has(extension)))
    .map((item) => item.value);
  return fileTypes.length > 0 ? fileTypes : fallbackTypes || DEFAULT_DATA_SOURCE_FILE_TYPES;
}

function getDataSourceErrorMessage(error: unknown) {
  const payload = (error as any)?.response?.data ?? error;
  const detail = payload?.detail;

  if (Array.isArray(detail)) {
    const messages = detail
      .map((item) => (typeof item === "string" ? item : item?.message || item?.msg))
      .filter(Boolean);

    if (messages.length > 0) {
      return messages.join("；");
    }
  }

  return `${payload?.message || (error as any)?.message || ""}`.trim();
}

function isKnowledgeBaseNameDuplicatedError(error: unknown) {
  const payload = (error as any)?.response?.data ?? error;
  const errorCode = `${payload?.code || payload?.error_code || payload?.errorCode || ""}`.trim();
  const rawMessage = getDataSourceErrorMessage(error).toLowerCase();

  return errorCode === "2001102" || rawMessage === "dataset name already exists";
}

function hasFeishuTargetTypes(targetTypes?: Record<string, unknown>) {
  if (!targetTypes) {
    return false;
  }
  return Object.values(targetTypes).some((targetType) =>
    Boolean(normalizeFeishuTargetType(`${targetType || ""}`)),
  );
}

function getFeishuBindingTargetTypes(bindings: ScanV2Binding[]) {
  const targetTypes: Record<string, FeishuTargetType> = {};

  bindings.forEach((binding) => {
    const targetRef = getScanBindingTarget(binding);
    const targetType = toUiFeishuTargetType(binding.target_type);
    if (targetRef && targetType) {
      targetTypes[targetRef] = targetType;
    }
  });

  return targetTypes;
}

function normalizeFeishuTargetTypeRecord(
  targetTypes?: Record<string, unknown>,
) {
  if (!targetTypes) {
    return undefined;
  }

  const normalizedTypes: Record<string, FeishuTargetType> = {};
  Object.entries(targetTypes).forEach(([targetRef, targetType]) => {
    const normalizedTargetRef = `${targetRef || ""}`.trim();
    const normalizedTargetType = normalizeFeishuTargetType(
      `${targetType || ""}`,
      normalizedTargetRef,
    );
    if (normalizedTargetRef && normalizedTargetType) {
      normalizedTypes[normalizedTargetRef] = normalizedTargetType;
    }
  });

  return hasFeishuTargetTypes(normalizedTypes) ? normalizedTypes : undefined;
}

function buildFeishuTargetOptionsCacheKey(
  authConnectionId: string,
  keyword = "",
) {
  const normalizedKeyword = keyword.trim();
  return [
    authConnectionId.trim(),
    normalizedKeyword || FEISHU_TARGET_CACHE_ROOT_KEY,
  ].join("::");
}

function buildFeishuTargetChildrenCacheKey(params: {
  authConnectionId: string;
  targetType: FeishuTargetType;
  targetRef: string;
  nodeRef: string;
}) {
  return [
    params.authConnectionId.trim(),
    params.targetType,
    params.targetRef.trim(),
    params.nodeRef.trim(),
  ].join("::");
}

function isFeishuHelperNode(node: FeishuTargetTreeNode) {
  return `${node.value || ""}`.startsWith("__scan-feishu-target-helper__");
}

function isManualFeishuTargetNode(node: FeishuTargetTreeNode) {
  return `${node.value || ""}`.startsWith(FEISHU_MANUAL_TARGET_VALUE_PREFIX);
}

function isFeishuRootTargetNode(node: FeishuTargetTreeNode) {
  const ref = `${node.targetRef || node.value || node.key || ""}`.trim().toLowerCase();
  return ref === FEISHU_DRIVE_ROOT_REF || ref === FEISHU_WIKI_SPACES_REF;
}

function buildFeishuRootTargetNodes(): FeishuTargetTreeNode[] {
  return [
    {
      key: FEISHU_DRIVE_ROOT_REF,
      value: FEISHU_DRIVE_ROOT_REF,
      title: "Drive",
      isLeaf: false,
      targetRef: FEISHU_DRIVE_ROOT_REF,
      targetType: "drive_folder",
    },
    {
      key: FEISHU_WIKI_SPACES_REF,
      value: FEISHU_WIKI_SPACES_REF,
      title: "Wiki",
      isLeaf: false,
      targetRef: FEISHU_WIKI_SPACES_REF,
      targetType: "wiki_space",
    },
  ];
}

function extractFeishuRootTargetNodes(nodes: FeishuTargetTreeNode[]): FeishuTargetTreeNode[] {
  const roots = nodes.filter(isFeishuRootTargetNode);
  const existingRefs = new Set(
    roots.map((node) => `${node.targetRef || node.value || ""}`.trim().toLowerCase()),
  );

  return [
    ...roots,
    ...buildFeishuRootTargetNodes().filter(
      (node) => !existingRefs.has(`${node.targetRef}`.toLowerCase()),
    ),
  ];
}

function mergeFeishuTargetSearchResults(
  rootNodes: FeishuTargetTreeNode[],
  searchNodes: FeishuTargetTreeNode[],
): FeishuTargetTreeNode[] {
  const rootRefs = new Set(
    rootNodes.map((node) => `${node.targetRef || node.value || ""}`.trim().toLowerCase()),
  );

  const filteredSearchNodes = searchNodes.filter((node) => {
    if (isFeishuRootTargetNode(node)) {
      return false;
    }
    const ref = `${node.targetRef || node.value || ""}`.trim().toLowerCase();
    return !rootRefs.has(ref);
  });

  return [...rootNodes, ...filteredSearchNodes];
}

// Shared schedule expression helpers (used by both local reconcile_schedule and
// cloud schedule_expr). New weekly format is `weekly:1,2,3@HH:MM:SS`;
// legacy `daily@HH:MM:SS`, `every2d@HH:MM:SS`, and `every7d@HH:MM:SS`
// are still parsed for existing records.
function parseReconcileSchedule(expr?: string): {
  scheduleWeekdays: string[];
  scheduleTime: string;
} | null {
  if (!expr) return null;
  const trimmed = expr.trim();
  if (!trimmed) return null;
  const lower = trimmed.toLowerCase();
  if (lower === "manual" || lower === "manual_only") return null;

  const weeklyMatch = trimmed.match(
    /^weekly:([1-7](?:,[1-7])*)@(([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)$/i,
  );
  if (weeklyMatch) {
    return {
      scheduleWeekdays: normalizeScheduleWeekdays(weeklyMatch[1].split(",")),
      scheduleTime: normalizeScheduleTime(weeklyMatch[2]),
    };
  }
  const dailyMatch = trimmed.match(/^daily@(([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)$/i);
  if (dailyMatch) {
    return {
      scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
      scheduleTime: normalizeScheduleTime(dailyMatch[1]),
    };
  }
  const everyMatch = trimmed.match(/^every(\d+)d@(([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)$/i);
  if (everyMatch) {
    return {
      scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
      scheduleTime: normalizeScheduleTime(everyMatch[2]),
    };
  }
  return null;
}

function getScheduleWeekdaysLabel(scheduleWeekdays: string[], t: TFunction): string {
  const weekdays = normalizeScheduleWeekdays(scheduleWeekdays);
  if (weekdays.length === 7) {
    return t("admin.dataSourceScheduleEveryday");
  }
  return weekdays
    .map((day) => t(`admin.dataSourceScheduleWeekday${day}`))
    .join("、");
}

function buildFeishuScheduleLabel(binding: ScanV2Binding | null, t: TFunction) {
  const parsed = parseFeishuScheduleExpr(getBindingSchedule(binding));
  if (!parsed) {
    return t("admin.dataSourceSyncModeManual");
  }

  return t("admin.dataSourceScheduleLabel", {
    cycle: getScheduleWeekdaysLabel(parsed.scheduleWeekdays, t),
    time: parsed.scheduleTime,
  });
}

function buildFeishuNextSyncLabel(binding: ScanV2Binding | null, t: TFunction) {
  const nextSyncAt = formatDateTime(binding?.next_sync_at || binding?.nextSyncAt);
  if (nextSyncAt !== "-") {
    return t("admin.dataSourceNextSyncPlanned", {
      time: nextSyncAt,
    });
  }

  const parsed = parseFeishuScheduleExpr(getBindingSchedule(binding));
  if (!parsed) {
    return t("admin.dataSourceNextSyncManual");
  }

  return t("admin.dataSourceNextSyncPlanned", {
    time: parsed.scheduleTime,
  });
}

function mapScanSyncDetail(updateState: FileUpdateState, t: TFunction) {
  if (updateState === "new") {
    return t("admin.dataSourceFileUpdateNewDetail");
  }
  if (updateState === "changed") {
    return t("admin.dataSourceFileUpdateChangedDetail");
  }
  if (updateState === "deleted") {
    return t("admin.dataSourceFileUpdateDeletedDetail");
  }
  return t("admin.dataSourceFileUpdateUnchangedDetail");
}

function pickScanAgent(agents: ScanV2AgentHint[], preferredAgentId?: string) {
  if (preferredAgentId) {
    const preferred = agents.find((item) => item.agent_id === preferredAgentId);
    if (preferred) {
      return preferred;
    }
  }

  const onlineAgent = agents.find((item) => {
    const status = (item.status || "").toLowerCase();
    return (
      status.includes("online") ||
      status.includes("active") ||
      status.includes("running")
    );
  });

  return onlineAgent || agents[0];
}

function inferScheduleWeekdays(scheduleLabel: string) {
  const normalized = scheduleLabel.toLowerCase();
  if (
    scheduleLabel.includes("每天") ||
    scheduleLabel.includes("全天") ||
    normalized.includes("daily") ||
    normalized.includes("every day")
  ) {
    return DEFAULT_SCHEDULE_WEEKDAYS;
  }
  const weekdayMap: Array<[string, string[]]> = [
    ["1", ["周一", "星期一", "monday", "mon"]],
    ["2", ["周二", "星期二", "tuesday", "tue"]],
    ["3", ["周三", "星期三", "wednesday", "wed"]],
    ["4", ["周四", "星期四", "thursday", "thu"]],
    ["5", ["周五", "星期五", "friday", "fri"]],
    ["6", ["周六", "星期六", "saturday", "sat"]],
    ["7", ["周日", "周天", "星期日", "星期天", "sunday", "sun"]],
  ];
  const matchedDays = weekdayMap
    .filter(([, labels]) =>
      labels.some((label) => normalized.includes(label.toLowerCase())),
    )
    .map(([day]) => day);
  return normalizeScheduleWeekdays(matchedDays);
}

function parseFeishuOAuthCallbackInput(value: string) {
  const normalized = value.trim();
  if (!normalized) {
    return null;
  }

  try {
    const url = new URL(normalized);
    const code = url.searchParams.get("code");
    const state = url.searchParams.get("state");
    if (code && state) {
      return { code, state };
    }
  } catch {
  }

  const search = normalized.startsWith("?") ? normalized.slice(1) : normalized;
  const params = new URLSearchParams(search);
  const code = params.get("code");
  const state = params.get("state");
  if (code && state) {
    return { code, state };
  }

  const matchCode = normalized.match(/[?&]code=([^&]+)/);
  const matchState = normalized.match(/[?&]state=([^&]+)/);
  if (matchCode?.[1] && matchState?.[1]) {
    return {
      code: decodeURIComponent(matchCode[1]),
      state: decodeURIComponent(matchState[1]),
    };
  }

  return null;
}

export default function DataSourceManagement() {
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
  const [localPathOptions, setLocalPathOptions] = useState<LocalPathTreeNode[]>([]);
  const [localPathLoading, setLocalPathLoading] = useState(false);
  const localPathRequestSeqRef = useRef(0);
  const localPathSearchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const localPathOptionsCacheRef = useRef(new Map<string, LocalPathTreeNode[]>());
  const localPathChildrenCacheRef = useRef(new Map<string, LocalPathTreeNode[]>());
  const localPathActiveOptionsCacheKeyRef = useRef("");
  const [feishuTargetTreeData, setFeishuTargetTreeData] = useState<FeishuTargetTreeNode[]>([]);
  const [feishuTargetLoading, setFeishuTargetLoading] = useState(false);
  const feishuTargetRequestSeqRef = useRef(0);
  const feishuTargetSearchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const feishuTargetOptionsCacheRef = useRef(new Map<string, FeishuTargetTreeNode[]>());
  const feishuTargetChildrenCacheRef = useRef(new Map<string, FeishuTargetTreeNode[]>());
  const sourceListRequestSeqRef = useRef(0);
  const assetSearchInitializedRef = useRef(false);
  const feishuAuthAccountsLoadedRef = useRef(false);

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

  const buildScanScheduleLabel = (binding?: ScanV2Binding | null) => {
    if (binding?.sync_mode !== "scheduled" && binding?.sync_mode !== "watch") {
      return t("admin.dataSourceSyncModeManual");
    }

    const parsed = parseReconcileSchedule(getBindingSchedule(binding));
    if (parsed) {
      const cycleLabel = getScheduleWeekdaysLabel(parsed.scheduleWeekdays, t);
      return `${cycleLabel} ${parsed.scheduleTime} ${t("admin.dataSourceScheduleAutoSuffix")}`;
    }

    return t("admin.dataSourceSyncModeScheduled");
  };

  const buildScanNextSyncLabel = (binding?: ScanV2Binding | null) => {
    if (binding?.sync_mode !== "scheduled" && binding?.sync_mode !== "watch") {
      return t("admin.dataSourceNextSyncManual");
    }
    const parsed = parseReconcileSchedule(getBindingSchedule(binding));
    if (parsed) {
      return t("admin.dataSourceNextSyncPlanned", { time: parsed.scheduleTime });
    }
    return t("admin.dataSourceNextSyncPlanned", { time: "-" });
  };

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

  const buildManualLocalPathOptions = (
    pathValue: string,
    helperText?: string,
  ): LocalPathTreeNode[] => {
    const normalizedPath = pathValue.trim();
    const options: LocalPathTreeNode[] = [];

    if (normalizedPath) {
      options.push({
        key: normalizedPath,
        value: normalizedPath,
        title: t("admin.dataSourceUseCurrentInput", { value: normalizedPath }),
        isLeaf: true,
      });
    }

    if (helperText) {
      options.push({
        key: "__scan-local-path-helper__",
        value: "__scan-local-path-helper__",
        title: helperText,
        disabled: true,
        isLeaf: true,
      });
    }

    return options;
  };

  const mapLocalPathNodes = (nodes: ScanV2TreeNode[]): LocalPathTreeNode[] =>
    nodes
      .filter((node) => node.is_container || !node.is_document)
      .map((node) => {
        const value = getScanTreeNodePath(node) || `${node.key || node.node_ref || node.display_name}`;
        const title = node.display_name || node.object_key || value;
        return {
          key: value,
          value,
          title,
          isLeaf: !node.has_children,
          selectable: node.selectable !== false,
          disabled: node.selectable === false,
          nodeRef: node.node_ref,
          targetRef: node.target_ref || value,
        };
      })
      .filter((node) => Boolean(node.value));

  const mergeLocalPathChildren = (
    list: LocalPathTreeNode[],
    key: React.Key,
    children: LocalPathTreeNode[],
  ): LocalPathTreeNode[] =>
    list.map((node) => {
      if (node.key === key || node.value === key) {
        return { ...node, children, childrenLoaded: true };
      }
      if (node.children) {
        return {
          ...node,
          children: mergeLocalPathChildren(node.children, key, children),
        };
      }
      return node;
    });

  const buildLocalPathOptionsCacheKey = (agentId: string, keyword = "") =>
    [agentId.trim(), keyword.trim() || LOCAL_PATH_CACHE_ROOT_KEY].join("::");

  const buildLocalPathChildrenCacheKey = (params: {
    agentId: string;
    targetRef: string;
    nodeRef: string;
  }) =>
    [
      params.agentId.trim(),
      params.targetRef.trim(),
      params.nodeRef.trim(),
    ].join("::");

  const loadLocalPathOptions = async (pathValue?: string) => {
    const fallbackPathValue = form.getFieldValue("path");
    const normalizedPath =
      typeof pathValue === "string"
        ? pathValue.trim()
        : Array.isArray(fallbackPathValue)
          ? ""
          : `${fallbackPathValue || ""}`.trim();
    const requestSeq = localPathRequestSeqRef.current + 1;
    localPathRequestSeqRef.current = requestSeq;

    const agentId = getPreferredLocalAgentId();
    const cacheKey = buildLocalPathOptionsCacheKey(agentId, normalizedPath);
    const cachedNodes = localPathOptionsCacheRef.current.get(cacheKey);
    if (cachedNodes) {
      localPathActiveOptionsCacheKeyRef.current = cacheKey;
      setLocalPathOptions(cachedNodes);
      setLocalPathLoading(false);
      return;
    }

    setLocalPathLoading(true);
    try {
      const client = createScanV2ApiClient();
      const response = normalizedPath
        ? await client.searchBindingTargets({
            bindingTargetSearchRequest: {
              connector_type: "local_fs",
              target_type: "local_path",
              keyword: normalizedPath,
              agent_id: agentId || undefined,
              include_files: false,
              list_mode: "page",
              page_size: 50,
            } as any,
          })
        : await client.listBindingTargetChildren({
            bindingTargetChildrenRequest: {
              connector_type: "local_fs",
              target_type: "local_path",
              target_ref: "/",
              agent_id: agentId || undefined,
              include_files: false,
              list_mode: "page",
              page_size: 50,
            } as any,
          });

      if (localPathRequestSeqRef.current !== requestSeq) {
        return;
      }

      const nodes = [
        ...buildManualLocalPathOptions(normalizedPath),
        ...mapLocalPathNodes(response.data.items || []).filter(
          (node) => node.value !== normalizedPath,
        ),
      ];
      const nextNodes =
        nodes.length > 0
          ? nodes
          : buildManualLocalPathOptions("", t("admin.dataSourceNoLocalDirectories"));
      localPathOptionsCacheRef.current.set(cacheKey, nextNodes);
      localPathActiveOptionsCacheKeyRef.current = cacheKey;
      setLocalPathOptions(nextNodes);
    } catch (error) {
      if (localPathRequestSeqRef.current !== requestSeq) {
        return;
      }
      setLocalPathOptions(
        buildManualLocalPathOptions(
          normalizedPath,
          agentId
            ? getLocalizedErrorMessage(
              error,
              t("admin.dataSourceLocalDirectoryListFailedManual"),
            )
            : t("admin.dataSourceNoScanAgentManual"),
        ),
      );
    } finally {
      if (localPathRequestSeqRef.current === requestSeq) {
        setLocalPathLoading(false);
      }
    }
  };

  const handleSearchLocalPathOptions = (keyword: string) => {
    const normalizedKeyword = `${keyword || ""}`.trim();
    if (localPathSearchTimerRef.current) {
      clearTimeout(localPathSearchTimerRef.current);
    }

    if (!normalizedKeyword) {
      const agentId = getPreferredLocalAgentId();
      const rootCacheKey = buildLocalPathOptionsCacheKey(agentId);
      const cachedRootNodes = localPathOptionsCacheRef.current.get(rootCacheKey);
      if (cachedRootNodes) {
        localPathActiveOptionsCacheKeyRef.current = rootCacheKey;
        setLocalPathOptions(cachedRootNodes);
      }
      localPathSearchTimerRef.current = setTimeout(() => {
        void loadLocalPathOptions("");
      }, 300);
      return;
    }

    setLocalPathOptions(buildManualLocalPathOptions(normalizedKeyword));
    localPathSearchTimerRef.current = setTimeout(() => {
      void loadLocalPathOptions(normalizedKeyword);
    }, 300);
  };

  const handleLoadLocalPathChildren: TreeSelectProps["loadData"] = async (node) => {
    const treeNode = node as LocalPathTreeNode;
    const nodeRef = `${treeNode.nodeRef || ""}`.trim();
    const targetRef = `${treeNode.targetRef || treeNode.value || ""}`.trim();

    if (!targetRef || treeNode.childrenLoaded) {
      return;
    }

    const agentId = getPreferredLocalAgentId();
    const cacheKey = buildLocalPathChildrenCacheKey({ agentId, targetRef, nodeRef });
    const cachedChildren = localPathChildrenCacheRef.current.get(cacheKey);
    if (cachedChildren) {
      setLocalPathOptions((current) => {
        const nextTreeData = mergeLocalPathChildren(
          current,
          treeNode.key || treeNode.value,
          cachedChildren,
        );
        localPathOptionsCacheRef.current.set(
          localPathActiveOptionsCacheKeyRef.current ||
            buildLocalPathOptionsCacheKey(agentId),
          nextTreeData,
        );
        return nextTreeData;
      });
      return;
    }

    if (treeNode.children) {
      localPathChildrenCacheRef.current.set(cacheKey, treeNode.children);
      setLocalPathOptions((current) => {
        const nextTreeData = mergeLocalPathChildren(
          current,
          treeNode.key || treeNode.value,
          treeNode.children || [],
        );
        localPathOptionsCacheRef.current.set(
          localPathActiveOptionsCacheKeyRef.current ||
            buildLocalPathOptionsCacheKey(agentId),
          nextTreeData,
        );
        return nextTreeData;
      });
      return;
    }

    const response = await createScanV2ApiClient().listBindingTargetChildren({
      bindingTargetChildrenRequest: {
        connector_type: "local_fs",
        target_type: "local_path",
        target_ref: targetRef,
        node_ref: nodeRef || undefined,
        agent_id: agentId || undefined,
        include_files: false,
        list_mode: "page",
        page_size: 50,
      } as any,
    });

    const children = mapLocalPathNodes(response.data.items || []);
    localPathChildrenCacheRef.current.set(cacheKey, children);
    setLocalPathOptions((current) => {
      const nextTreeData = mergeLocalPathChildren(
        current,
        treeNode.key || treeNode.value,
        children,
      );
      localPathOptionsCacheRef.current.set(
        localPathActiveOptionsCacheKeyRef.current ||
          buildLocalPathOptionsCacheKey(agentId),
        nextTreeData,
      );
      return nextTreeData;
    });
  };

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

  const buildFeishuHelperNode = (title: string): FeishuTargetTreeNode => ({
    key: "__scan-feishu-target-helper__",
    value: "__scan-feishu-target-helper__",
    title,
    disabled: true,
    isLeaf: true,
  });

  const buildManualFeishuTargetNode = (
    targetRef: string,
    kind: FeishuManualTargetKind,
  ): FeishuTargetTreeNode => {
    const normalizedTargetRef = targetRef.trim();
    const targetType =
      kind === "wiki"
        ? "wiki_space"
        : kind === "drive"
          ? "drive_folder"
          : normalizeFeishuTargetType(undefined, normalizedTargetRef) ||
            feishuTargetType;
    const title =
      kind === "wiki"
        ? t("admin.dataSourceUseCurrentFeishuWikiInput", { value: normalizedTargetRef })
        : kind === "drive"
          ? t("admin.dataSourceUseCurrentFeishuDriveInput", { value: normalizedTargetRef })
          : t("admin.dataSourceUseCurrentInput", { value: normalizedTargetRef });
    const value = buildManualFeishuTargetValue(kind, normalizedTargetRef);

    return {
      key: value,
      value,
      title,
      isLeaf: true,
      targetRef: normalizedTargetRef,
      targetType,
    };
  };

  const buildManualFeishuTargetNodes = (
    targetRef: string,
  ): FeishuTargetTreeNode[] =>
    (["current", "wiki", "drive"] as FeishuManualTargetKind[]).map((kind) =>
      buildManualFeishuTargetNode(targetRef, kind),
    );

  const hasFeishuTargetRef = (
    nodes: FeishuTargetTreeNode[],
    targetRef: string,
  ): boolean =>
    nodes.some((node) => {
      const refs = [node.value, node.targetRef, node.nodeRef]
        .map((item) => `${item || ""}`.trim())
        .filter(Boolean);

      return refs.includes(targetRef) || Boolean(
        node.children && hasFeishuTargetRef(node.children, targetRef),
      );
    });

  const prependManualFeishuTargetOption = (
    targetRef: string,
    nodes: FeishuTargetTreeNode[],
  ): FeishuTargetTreeNode[] => {
    const normalizedTargetRef = targetRef.trim();
    if (!normalizedTargetRef || hasFeishuTargetRef(nodes, normalizedTargetRef)) {
      return nodes;
    }
    return [...buildManualFeishuTargetNodes(normalizedTargetRef), ...nodes];
  };

  const getFeishuRootTargetNodes = (): FeishuTargetTreeNode[] => {
    const authConnectionId = getActiveFeishuAuthConnectionId();
    const rootCacheKey = buildFeishuTargetOptionsCacheKey(authConnectionId);
    const cachedRootNodes = feishuTargetOptionsCacheRef.current.get(rootCacheKey);
    if (cachedRootNodes?.length) {
      const browsableNodes = cachedRootNodes.filter(
        (node) => !isFeishuHelperNode(node) && !isManualFeishuTargetNode(node),
      );
      if (browsableNodes.length > 0) {
        return extractFeishuRootTargetNodes(browsableNodes);
      }
    }
    return buildFeishuRootTargetNodes();
  };

  const mapFeishuTargetNodes = (
    nodes: ScanV2TreeNode[],
    inheritedTargetType?: FeishuTargetType,
  ): FeishuTargetTreeNode[] =>
    nodes.map((node) => {
      const value = getScanTreeNodePath(node) || `${node.key || node.node_ref || node.display_name}`;
      const title = node.display_name || node.title || node.object_key || value;
      const targetRef = node.target_ref || value;
      const nodeRef = node.node_ref;
      const targetType = normalizeFeishuTargetType(
        node.target_type,
        `${targetRef || nodeRef || value}`,
      ) || inheritedTargetType;

      return {
        key: value,
        value,
        title,
        isLeaf: !node.has_children,
        selectable: node.selectable !== false,
        disabled: node.selectable === false,
        nodeRef,
        targetRef,
        targetType,
      };
    });

  const mergeFeishuTargetChildren = (
    list: FeishuTargetTreeNode[],
    key: React.Key,
    children: FeishuTargetTreeNode[],
  ): FeishuTargetTreeNode[] =>
    list.map((node) => {
      if (node.key === key || node.value === key) {
        return { ...node, children };
      }
      if (node.children) {
        return {
          ...node,
          children: mergeFeishuTargetChildren(node.children, key, children),
        };
      }
      return node;
    });

  const loadFeishuTargetOptions = async (keyword = "") => {
    const requestSeq = feishuTargetRequestSeqRef.current + 1;
    feishuTargetRequestSeqRef.current = requestSeq;
    const authConnectionId = getActiveFeishuAuthConnectionId();

    if (!authConnectionId) {
      setFeishuTargetTreeData([
        buildFeishuHelperNode(t("admin.dataSourceFeishuAuthorizeFirstBrowse")),
      ]);
      setFeishuTargetLoading(false);
      return;
    }

    const normalizedKeyword = keyword.trim();
    const cacheKey = buildFeishuTargetOptionsCacheKey(
      authConnectionId,
      normalizedKeyword,
    );
    const cachedNodes = feishuTargetOptionsCacheRef.current.get(cacheKey);
    if (cachedNodes) {
      setFeishuTargetTreeData((current) =>
        normalizedKeyword || current.length === 0 || current.every(isFeishuHelperNode)
          ? cachedNodes
          : current,
      );
      setFeishuTargetLoading(false);
      return;
    }

    setFeishuTargetLoading(true);
    try {
      const client = createScanV2ApiClient();
      const response = normalizedKeyword
        ? await client.searchBindingTargets({
            bindingTargetSearchRequest: {
              connector_type: "feishu",
              auth_connection_id: authConnectionId,
              keyword: normalizedKeyword,
              include_files: true,
              list_mode: "page",
              page_size: 50,
              provider_options: {
                tenant_key: getScanTenantId(),
              },
            } as any,
          })
        : await client.listBindingTargetChildren({
            bindingTargetChildrenRequest: {
              connector_type: "feishu",
              auth_connection_id: authConnectionId,
              include_files: true,
              list_mode: "page",
              page_size: 50,
              provider_options: {
                tenant_key: getScanTenantId(),
              },
            } as any,
          });

      if (feishuTargetRequestSeqRef.current !== requestSeq) {
        return;
      }

      const nodes = mapFeishuTargetNodes(response.data.items || []);
      let nextNodes: FeishuTargetTreeNode[];

      if (normalizedKeyword) {
        const rootNodes = getFeishuRootTargetNodes();
        const mergedNodes = mergeFeishuTargetSearchResults(rootNodes, nodes);
        const baseNodes =
          nodes.length > 0
            ? mergedNodes
            : [
                ...mergedNodes,
                buildFeishuHelperNode(t("admin.dataSourceNoFeishuTargets")),
              ];
        nextNodes = prependManualFeishuTargetOption(normalizedKeyword, baseNodes);
      } else {
        const baseNodes =
          nodes.length > 0
            ? nodes
            : [buildFeishuHelperNode(t("admin.dataSourceNoFeishuTargets"))];
        nextNodes = baseNodes;
      }

      feishuTargetOptionsCacheRef.current.set(cacheKey, nextNodes);
      setFeishuTargetTreeData(nextNodes);
    } catch (error) {
      if (feishuTargetRequestSeqRef.current !== requestSeq) {
        return;
      }
      const fallbackNodes = [
        buildFeishuHelperNode(
          getLocalizedErrorMessage(
            error,
            t("admin.dataSourceFeishuDirectoryListFailedManual"),
          ) || t("admin.dataSourceFeishuDirectoryListFailedManual"),
        ),
      ];
      setFeishuTargetTreeData(
        normalizedKeyword
          ? prependManualFeishuTargetOption(normalizedKeyword, [
              ...getFeishuRootTargetNodes(),
              ...fallbackNodes,
            ])
          : fallbackNodes,
      );
    } finally {
      if (feishuTargetRequestSeqRef.current === requestSeq) {
        setFeishuTargetLoading(false);
      }
    }
  };

  const handleSearchFeishuTargetOptions = (keyword: string) => {
    const normalizedKeyword = `${keyword || ""}`.trim();
    if (feishuTargetSearchTimerRef.current) {
      clearTimeout(feishuTargetSearchTimerRef.current);
    }

    if (!normalizedKeyword) {
      const authConnectionId = getActiveFeishuAuthConnectionId();
      const rootCacheKey = buildFeishuTargetOptionsCacheKey(authConnectionId);
      const cachedRootNodes = feishuTargetOptionsCacheRef.current.get(rootCacheKey);
      if (cachedRootNodes) {
        setFeishuTargetTreeData(cachedRootNodes);
      }
      feishuTargetSearchTimerRef.current = setTimeout(() => {
        void loadFeishuTargetOptions("");
      }, 300);
      return;
    }

    setFeishuTargetTreeData(
      prependManualFeishuTargetOption(normalizedKeyword, getFeishuRootTargetNodes()),
    );
    feishuTargetSearchTimerRef.current = setTimeout(() => {
      void loadFeishuTargetOptions(normalizedKeyword);
    }, 300);
  };

  const handleLoadFeishuTargetChildren: TreeSelectProps["loadData"] = async (node) => {
    const authConnectionId = getActiveFeishuAuthConnectionId();
    if (!authConnectionId) {
      return;
    }

    const treeNode = node as FeishuTargetTreeNode;
    const nodeRef = `${treeNode.nodeRef || ""}`.trim();
    const targetRef = `${treeNode.targetRef || treeNode.value || ""}`.trim();
    const uiTargetType = normalizeFeishuTargetType(treeNode.targetType, targetRef) || feishuTargetType;
    const targetType = toScanFeishuTargetType(uiTargetType);
    const cacheKey = buildFeishuTargetChildrenCacheKey({
      authConnectionId,
      targetType: uiTargetType,
      targetRef,
      nodeRef,
    });
    const cachedChildren = feishuTargetChildrenCacheRef.current.get(cacheKey);
    if (cachedChildren) {
      setFeishuTargetTreeData((current) => {
        const nextTreeData = mergeFeishuTargetChildren(
          current,
          treeNode.key || treeNode.value,
          cachedChildren,
        );
        feishuTargetOptionsCacheRef.current.set(
          buildFeishuTargetOptionsCacheKey(authConnectionId),
          nextTreeData,
        );
        return nextTreeData;
      });
      return;
    }

    if (treeNode.children) {
      feishuTargetChildrenCacheRef.current.set(cacheKey, treeNode.children);
      setFeishuTargetTreeData((current) => {
        const nextTreeData = mergeFeishuTargetChildren(
          current,
          treeNode.key || treeNode.value,
          treeNode.children || [],
        );
        feishuTargetOptionsCacheRef.current.set(
          buildFeishuTargetOptionsCacheKey(authConnectionId),
          nextTreeData,
        );
        return nextTreeData;
      });
      return;
    }

    const response = await createScanV2ApiClient().listBindingTargetChildren({
      bindingTargetChildrenRequest: {
        connector_type: "feishu",
        target_type: targetType,
        auth_connection_id: authConnectionId,
        target_ref: targetRef || undefined,
        node_ref: nodeRef || undefined,
        include_files: true,
        list_mode: "page",
        page_size: 50,
        provider_options: {
          tenant_key: getScanTenantId(),
        },
      } as any,
    });

    const children = mapFeishuTargetNodes(response.data.items || [], uiTargetType);
    feishuTargetChildrenCacheRef.current.set(cacheKey, children);
    setFeishuTargetTreeData((current) => {
      const nextTreeData = mergeFeishuTargetChildren(
        current,
        treeNode.key || treeNode.value,
        children,
      );
      feishuTargetOptionsCacheRef.current.set(
        buildFeishuTargetOptionsCacheKey(authConnectionId),
        nextTreeData,
      );
      return nextTreeData;
    });
  };

  const handleToggleLocalScanChat = async (chatEnabled: boolean) => {
    if (localScanChatSaving) {
      return;
    }
    if (!canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }

    const previousValue = localScanChatEnabled;
    setLocalScanChatSaving(true);
    setLocalScanChatEnabled(chatEnabled);

    try {
      const setting = await updateLocalFSChatSetting(chatEnabled);
      setLocalScanChatEnabled(Boolean(setting.enabled));
      message.success(
        chatEnabled
          ? t("admin.dataSourceLocalScanChatEnabledSuccess")
          : t("admin.dataSourceLocalScanChatDisabledSuccess"),
      );
    } catch (error) {
      setLocalScanChatEnabled(previousValue);
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    } finally {
      setLocalScanChatSaving(false);
    }
  };

  const mapScanSourceToDataSource = (
    source: ScanV2Source,
    fallback?: DataSourceItem,
    binding: ScanV2Binding | null = null,
    bindings: ScanV2Binding[] = binding ? [binding] : [],
  ): DataSourceItem => {
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
    const currentTime = formatDateTime(
      binding?.updated_at || getScanSourceUpdatedAt(source),
    );
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
          fallback?.oauthConnection && fallback.oauthConnection.connectionId === binding?.auth_connection_id
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
        syncMode: binding?.sync_mode === "scheduled" || binding?.sync_mode === "watch" ? "scheduled" : "manual",
        scheduleLabel: buildScanScheduleLabel(binding),
        status: sourceStatus,
        connectionState,
        lastSync: currentTime,
        nextSync: buildScanNextSyncLabel(binding),
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
          fallback?.oauthConnection && fallback.oauthConnection.connectionId === binding?.auth_connection_id
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
      syncMode: binding?.sync_mode === "scheduled" || binding?.sync_mode === "watch" ? "scheduled" : "manual",
      scheduleLabel: buildScanScheduleLabel(binding),
      status: sourceStatus,
      connectionState,
      lastSync: currentTime,
      nextSync: buildScanNextSyncLabel(binding),
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
          description: binding?.sync_mode === "scheduled" || binding?.sync_mode === "watch"
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
  };

  const refreshSources = async (
    showSuccessMessage = false,
    options?: {
      page?: number;
      pageSize?: number;
      keyword?: string;
    },
  ) => {
    const client = createScanV2ApiClient();
    const nextPage = Math.max(1, options?.page ?? sourceListPage);
    const nextPageSize = Math.max(
      1,
      options?.pageSize ?? sourceListPageSize,
    );
    const keyword = `${options?.keyword ?? assetSearchValue}`.trim();
    const requestSeq = sourceListRequestSeqRef.current + 1;
    sourceListRequestSeqRef.current = requestSeq;

    setScanLoading(true);
    try {
      const [sourcesResponse, nextLocalFSChatSetting] = await Promise.all([
        client.listSources({
          keyword: keyword || undefined,
          page: nextPage,
          pageSize: nextPageSize,
        }),
        getLocalFSChatSetting().catch((error) => {
          console.error("Failed to refresh local fs chat setting", error);
          return { enabled: localScanChatEnabled };
        }),
      ]);
      const sourceList = (sourcesResponse.data.items || []) as ScanV2Source[];
      const visibleSourceList = sourceList.filter(
        (source) => normalizeDataSourceStatus(source.status) !== "deleted",
      );
      const previousSourceMap = new Map(
        sources.map((item) => [item.id, item]),
      );
      const nextSources = await Promise.all(
        visibleSourceList.map(async (source) => {
          const sourceId = getScanSourceId(source);
          const fallback = previousSourceMap.get(sourceId);
          try {
            const [detailResponse, summaryResponse] = await Promise.all([
              client.getSource({ sourceId }),
              client.getSourceSummary({ sourceId }).catch(() => null),
            ]);
            const detailSource = {
              ...source,
              ...detailResponse.data.source,
              summary: summaryResponse?.data || source.summary,
            };
            const bindings = (detailResponse.data.bindings || []) as ScanV2Binding[];
            return mapScanSourceToDataSource(
              detailSource,
              fallback,
              getFirstScanBinding(bindings),
              bindings,
            );
          } catch (error) {
            console.error("Failed to load source detail", error);
            return mapScanSourceToDataSource(source, fallback);
          }
        }),
      );
      if (sourceListRequestSeqRef.current !== requestSeq) {
        return;
      }
      setLocalScanChatEnabled(Boolean(nextLocalFSChatSetting.enabled));
      setSources(nextSources);
      setSourceListPage(nextPage);
      setSourceListPageSize(nextPageSize);
      setSourceListTotal(Number(sourcesResponse.data.total || 0));

      if (showSuccessMessage) {
        message.success(t("admin.dataSourceListRefreshed"));
      }
    } catch (error) {
      if (showSuccessMessage) {
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
      } else {
        console.error("Failed to refresh local sources", error);
      }
    } finally {
      if (sourceListRequestSeqRef.current === requestSeq) {
        setScanLoading(false);
      }
    }
  };

  const refreshFeishuAuthAccounts = async () => {
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "feishu",
          status: null,
        });
      const cachedAccounts = loadFeishuAuthAccounts();
      const nextAccounts = getCloudConnectionItems(response.data).map((item) =>
        mapCloudConnectionToFeishuAccount(item, cachedAccounts),
      );
      feishuAuthAccountsLoadedRef.current = true;
      setFeishuAuthAccounts(nextAccounts);
      persistFeishuAuthAccounts(nextAccounts);
      const connectedAccount = nextAccounts.find(
        (account) =>
          account.status === "connected" && Boolean(account.connection?.connectionId),
      );
      if (connectedAccount?.connection) {
        setOauthConnection(connectedAccount.connection);
        setOauthState("connected");
        setConnectionVerified(true);
      }
    } catch (error) {
      console.error("Failed to refresh Feishu auth accounts", error);
    }
  };

  const refreshNotionAuthConnection = async () => {
    try {
      const response =
        await dataSourceCloudOauthApi.listConnectionsApiAuthserviceV1CloudConnectionsGet({
          provider: "notion",
          status: null,
        });
      const nextConnection = getCloudConnectionItems(response.data)
        .map((item) => mapCloudConnectionToDataSourceConnection(item, "notion"))
        .find(
          (connection) =>
            connection.status === "connected" && Boolean(connection.connectionId),
        ) || null;
      setNotionOauthConnection(nextConnection);
      if (nextConnection && selectedType === "notion") {
        setOauthConnection(nextConnection);
        setOauthState("connected");
        setConnectionVerified(true);
      }
    } catch (error) {
      console.error("Failed to refresh Notion auth connection", error);
    }
  };

  const clearOauthAttempt = () => {
    if (oauthAttemptRef.current?.timerId) {
      window.clearInterval(oauthAttemptRef.current.timerId);
    }
    oauthAttemptRef.current = null;
  };

  const restorePreviousOauthState = (messageText?: string, level: "warning" | "error" = "warning") => {
    const attempt = oauthAttemptRef.current;
    if (!attempt) {
      if (messageText) {
        message[level](messageText);
      }
      return;
    }

    if (attempt.timerId) {
      window.clearInterval(attempt.timerId);
    }
    setOauthState(attempt.previousState);
    setConnectionVerified(attempt.previousVerified);
    setOauthConnection(attempt.previousConnection);
    if (attempt.accountId) {
      setFeishuAuthAccounts((current) => {
        const nextAccounts = current.map((item) =>
          item.id === attempt.accountId
            ? {
                ...item,
                status: attempt.previousState,
                connection: attempt.previousConnection,
                updatedAt: new Date().toISOString(),
              }
            : item,
        );
        persistFeishuAuthAccounts(nextAccounts);
        return nextAccounts;
      });
    }
    oauthAttemptRef.current = null;

    if (messageText) {
      message[level](messageText);
    }
  };

  const applyOauthResult = (payload: FeishuDataSourceOAuthMessage) => {
    const attempt = oauthAttemptRef.current;

    if (payload.channel !== FEISHU_DATA_SOURCE_OAUTH_CHANNEL) {
      return;
    }

    if (attempt?.timerId) {
      window.clearInterval(attempt.timerId);
    }
    if (attempt) {
      attempt.resolved = true;
    }

    if (payload.status === "success") {
      oauthAttemptRef.current = null;
      const nextOauthState = getOAuthStateFromConnection(payload.connection);
      setOauthConnection(payload.connection);
      setOauthState(nextOauthState);
      setConnectionVerified(nextOauthState === "connected");
      if (payload.connection.provider === "notion") {
        setNotionOauthConnection(payload.connection);
        if (nextOauthState === "connected") {
          void enableCloudConnectionForChat(payload.connection.connectionId).catch((error) => {
            console.error("Failed to enable Notion connection for chat", error);
          });
        }
      }
      if (nextOauthState === "connected") {
        setFeishuAuthAccounts((current) => {
          if (payload.connection.provider !== "feishu") {
            return current;
          }
          const matchedAccount = current.find(
            (item) =>
              (attempt?.accountId && item.id === attempt.accountId) ||
              item.appId === attempt?.appId ||
              item.appId === feishuAppSetup?.appId,
          );
          if (!matchedAccount) {
            return current;
          }

          const nextAccounts = current.map((item) =>
            item.id === matchedAccount.id
              ? {
                  ...item,
                  name:
                    item.name ||
                    payload.connection.accountName ||
                    item.appId,
                  status: nextOauthState,
                  connection: payload.connection,
                  updatedAt: new Date().toISOString(),
                  lastAuthorizedAt: new Date().toISOString(),
                }
              : item,
          );
          persistFeishuAuthAccounts(nextAccounts);
          return nextAccounts;
        });
      }
      setWizardStep(1);
      message.success(t("admin.dataSourceOauthSuccess"));
      return;
    }

    if (attempt?.previousConnection) {
      restorePreviousOauthState(
        t("admin.dataSourceOauthReconnectFailed", {
          message: payload.message ? ` ${payload.message}` : "",
        }),
        "error",
      );
      return;
    }

    oauthAttemptRef.current = null;
    setOauthConnection(null);
    setOauthState("error");
    setConnectionVerified(false);
    if (attempt?.accountId) {
      setFeishuAuthAccounts((current) => {
        const nextAccounts = current.map((item) =>
          item.id === attempt.accountId
            ? {
                ...item,
                status: "error" as OAuthState,
                connection: null,
                updatedAt: new Date().toISOString(),
              }
            : item,
        );
        persistFeishuAuthAccounts(nextAccounts);
        return nextAccounts;
      });
    }
    message.error(payload.message || t("admin.dataSourceOauthFailedRetry"));
  };

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
        applyOauthResult(storedResult);
      }, 0);
    }

    const storedNotionResult = consumeCloudDataSourceOAuthResult("notion");
    if (storedNotionResult) {
      window.setTimeout(() => {
        applyOauthResult(storedNotionResult);
      }, 0);
    }

    const handleMessage = (event: MessageEvent<FeishuDataSourceOAuthMessage>) => {
      if (event.origin !== window.location.origin) {
        return;
      }
      if (!event.data || event.data.channel !== FEISHU_DATA_SOURCE_OAUTH_CHANNEL) {
        return;
      }
      applyOauthResult(event.data);
    };

    window.addEventListener("message", handleMessage);

    return () => {
      window.removeEventListener("message", handleMessage);
      clearOauthAttempt();
    };
  }, [form]);

  useEffect(() => {
    void refreshSources(false);
    void refreshFeishuAuthAccounts();
    void refreshNotionAuthConnection();
  }, []);

  useEffect(() => {
    if (activeView !== "connectors" || feishuAuthAccountsLoadedRef.current) {
      return;
    }
    void refreshFeishuAuthAccounts();
    void refreshNotionAuthConnection();
  }, [activeView]);

  useEffect(() => {
    if (!assetSearchInitializedRef.current) {
      assetSearchInitializedRef.current = true;
      return;
    }

    const timer = window.setTimeout(() => {
      void refreshSources(false, {
        page: 1,
        pageSize: sourceListPageSize,
        keyword: assetSearchValue,
      });
    }, 300);

    return () => {
      window.clearTimeout(timer);
    };
  }, [assetSearchValue]);

  useEffect(
    () => () => {
      if (localPathSearchTimerRef.current) {
        clearTimeout(localPathSearchTimerRef.current);
      }
      if (feishuTargetSearchTimerRef.current) {
        clearTimeout(feishuTargetSearchTimerRef.current);
      }
    },
    [],
  );

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
    setLocalPathOptions([]);
    setLocalPathLoading(false);
    localPathOptionsCacheRef.current.clear();
    localPathChildrenCacheRef.current.clear();
    localPathActiveOptionsCacheKeyRef.current = "";
    localPathRequestSeqRef.current += 1;
    if (localPathSearchTimerRef.current) {
      clearTimeout(localPathSearchTimerRef.current);
      localPathSearchTimerRef.current = null;
    }
    setFeishuTargetTreeData([]);
    setFeishuTargetLoading(false);
    feishuTargetRequestSeqRef.current += 1;
    if (feishuTargetSearchTimerRef.current) {
      clearTimeout(feishuTargetSearchTimerRef.current);
      feishuTargetSearchTimerRef.current = null;
    }
  };

  const upsertFeishuAuthAccount = (
    setup: FeishuAccountFormValues,
    status: OAuthState = "pending",
  ) => {
    const now = new Date().toISOString();
    const appId = setup.appId.trim();
    const appSecret = setup.appSecret.trim();
    const existingAccount = editingFeishuAccountId
      ? feishuAuthAccounts.find((item) => item.id === editingFeishuAccountId)
      : feishuAuthAccounts.find((item) => item.appId === appId);
    const nextAccount: FeishuAuthAccount = {
      id: existingAccount?.id || createFeishuAccountId(),
      name: `${setup.name || ""}`.trim() || existingAccount?.name || appId,
      appId,
      appSecret,
      chatEnabled: existingAccount?.chatEnabled ?? false,
      status,
      connection: status === "pending" ? null : existingAccount?.connection || null,
      createdAt: existingAccount?.createdAt || now,
      updatedAt: now,
      lastAuthorizedAt:
        status === "connected" ? now : existingAccount?.lastAuthorizedAt,
    };
    const nextAccounts = existingAccount
      ? feishuAuthAccounts.map((item) =>
          item.id === existingAccount.id ? nextAccount : item,
        )
      : [nextAccount, ...feishuAuthAccounts];

    setFeishuAuthAccounts(nextAccounts);
    persistFeishuAuthAccounts(nextAccounts);
    return nextAccount;
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
    setLocalPathOptions([]);
    setLocalPathLoading(false);
    localPathOptionsCacheRef.current.clear();
    localPathChildrenCacheRef.current.clear();
    localPathActiveOptionsCacheKeyRef.current = "";
    setFeishuTargetTreeData([]);
    setFeishuTargetLoading(false);
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

  const startCloudOAuth = async (provider: CloudDataSourceProvider, options?: {
    setup?: FeishuAppSetup;
    draftSelectedType?: SourceType | null;
    draftWizardStep?: number;
    draftWizardOpen?: boolean;
    draftWizardMode?: "create" | "edit";
    draftEditingId?: string | null;
    draftFormValues?: Record<string, unknown>;
    previousState?: OAuthState;
    previousVerified?: boolean;
    previousConnection?: FeishuDataSourceConnection | null;
    accountId?: string;
    appId?: string;
  }) => {
    const activeSetup =
      options?.setup || (provider === "feishu" ? feishuAppSetup : notionAppSetup);
    const previousState = options?.previousState ?? oauthState;
    const previousVerified = options?.previousVerified ?? connectionVerified;
    const previousConnection = options?.previousConnection ?? oauthConnection;

    try {
      if (!activeSetup?.appId.trim() || !activeSetup.appSecret.trim()) {
        message.warning(
          provider === "feishu"
            ? t("admin.dataSourceFeishuCredentialRequired")
            : t("admin.dataSourceNotionCredentialRequired"),
        );
        return false;
      }

      const selectedAgent = pickScanAgent(scanAgents, validatedAgentId || undefined) || {
        agent_id: validatedAgentId || "",
        tenant_id: getScanTenantId(),
      };

      setOauthState("waiting");
      setValidatedAgentId(selectedAgent.agent_id || null);
      const requestAuthorizeUrl =
        provider === "feishu"
          ? requestFeishuDataSourceAuthorizeUrl
          : (input: Parameters<typeof requestCloudDataSourceAuthorizeUrl>[1]) =>
              requestCloudDataSourceAuthorizeUrl(provider, input);
      const authorizeUrl = await requestAuthorizeUrl({
        tenantId: selectedAgent.tenant_id || getScanTenantId(),
        appId: activeSetup.appId,
        appSecret: activeSetup.appSecret,
        scopes: provider === "feishu" ? FEISHU_DEFAULT_SCOPES : [],
        returnUrl: window.location.href,
      });

      const draft: FeishuDataSourceWizardDraft = {
        wizardOpen: options?.draftWizardOpen ?? true,
        wizardStep: options?.draftWizardStep ?? wizardStep,
        wizardMode: options?.draftWizardMode ?? wizardMode,
        selectedType: options?.draftSelectedType ?? selectedType,
        editingId: options?.draftEditingId ?? editingId,
        validatedAgentId: selectedAgent.agent_id || null,
        oauthState: "waiting",
        connectionVerified: previousVerified,
        oauthConnection: previousConnection,
        formValues: options?.draftFormValues || form.getFieldsValue(true),
      };

      saveFeishuDataSourceWizardDraft(draft);

      const popup = openCenteredPopup(
        authorizeUrl,
        provider === "feishu" ? t("admin.dataSourceFeishuAuthWindowTitle") : t("admin.dataSourceNotionAuthWindowTitle"),
      );

      if (options?.draftWizardOpen === false) {
        clearFeishuDataSourceWizardDraft();
      }

      oauthAttemptRef.current = {
        timerId: null,
        previousState,
        previousVerified,
        previousConnection,
        resolved: false,
        accountId: options?.accountId,
        appId: options?.appId || activeSetup.appId,
      };

      if (popup) {
        const timerId = window.setInterval(() => {
          if (!popup.closed) {
            return;
          }

          if (oauthAttemptRef.current?.resolved) {
            clearOauthAttempt();
            return;
          }

          // Fallback: postMessage may not have been processed yet —
          // check sessionStorage for OAuth result saved synchronously by callback page.
          const storedResult = consumeFeishuDataSourceOAuthResult();
          if (storedResult) {
            applyOauthResult(storedResult);
            return;
          }
          const storedCloudResult = consumeCloudDataSourceOAuthResult(
            (options?.draftSelectedType as CloudDataSourceProvider) || "notion",
          );
          if (storedCloudResult) {
            applyOauthResult(storedCloudResult);
            return;
          }

          restorePreviousOauthState(t("admin.dataSourceOauthWindowClosed"));
        }, 400);

        oauthAttemptRef.current.timerId = timerId;
        popup.focus();
        return true;
      }

      window.location.assign(authorizeUrl);
      return true;
    } catch (error: any) {
      setOauthState(previousState);
      setConnectionVerified(previousVerified);
      setOauthConnection(previousConnection);
      message.error(error?.message || t("admin.dataSourceAuthorizeUrlFailed"));
      return false;
    }
  };

  const saveCloudAppCredentials = async (
    provider: CloudDataSourceProvider,
    setup: FeishuAppSetup,
  ) => {
    const body: CloudOAuthAppCredentialBody = {
      client_id: setup.appId,
      client_secret: setup.appSecret,
    };
    await dataSourceCloudOauthApi.saveOauthAppCredentialsApiAuthserviceV1CloudProviderOauthAppCredentialsPut({
      provider,
      cloudOAuthAppCredentialBody: body,
    });
  };

  const openCloudSetupModal = (
    provider: CloudDataSourceProvider,
    intent: CloudSetupIntent = null,
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

  const openFeishuSetupModal = (
    intent: FeishuSetupIntent = null,
    account?: FeishuAuthAccount | null,
  ) => openCloudSetupModal("feishu", intent, account);

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
      const shouldStartOAuth = feishuSetupIntent === "create" || feishuSetupIntent === "auth";
      const nextAccount =
        cloudSetupProvider === "feishu"
          ? upsertFeishuAuthAccount(values, "waiting")
          : null;

      await saveCloudAppCredentials(cloudSetupProvider, nextSetup);
      if (cloudSetupProvider === "feishu") {
        persistFeishuAppSetup(nextSetup);
        setFeishuAppSetup(nextSetup);
      } else {
        persistNotionAppSetup(nextSetup);
        setNotionAppSetup(nextSetup);
      }
      setFeishuSetupModalOpen(false);
      const setupIntent = feishuSetupIntent;
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
        await startCloudOAuth(cloudSetupProvider, {
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
        clearOauthAttempt();
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
        clearOauthAttempt();
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

  const handleSelectType = (type: SourceType) => {
    if (type === "local" && !canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }
    if (type === "feishu" && !isFeishuSetupReady) {
      openFeishuSetupModal("create");
      return;
    }
    if (type === "notion" && !isNotionSetupReady) {
      openCloudSetupModal("notion", "create");
      return;
    }
    applySourceType(type);
  };

  const openSourceCreateWizard = (
    type: SourceType,
    options?: { connection?: FeishuDataSourceConnection | null },
  ) => {
    if (type === "local" && !canCreateLocalSource) {
      message.error(t("admin.dataSourceAdminOnly"));
      return;
    }
    const reusableConnection =
      type === "feishu" || type === "notion"
        ? options?.connection || (type === "notion" ? notionOauthConnection : oauthConnection)
        : null;
    resetWizard();
    setWizardMode("create");
    setEditingId(null);
    setCreateProviderModalOpen(false);
    setAuthSelectModalOpen(false);
    applySourceType(type);
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

    if (type === "feishu" && isFeishuAuthValid) {
      setCreateProviderModalOpen(false);
      setAuthSelectModalOpen(true);
      return;
    }

    if (type === "notion" && isNotionAuthValid) {
      setCreateProviderModalOpen(false);
      openSourceCreateWizard("notion", { connection: notionOauthConnection });
      return;
    }

    setCreateProviderModalOpen(false);
    resetWizard();
    setWizardMode("create");
    setEditingId(null);
    applySourceType(type);
    setWizardStep(1);

    if (type === "feishu" && !isFeishuAuthValid) {
      openCloudSetupModal("feishu", "create");
      return;
    }
    if (type === "notion" && !isNotionAuthValid) {
      openCloudSetupModal("notion", "create");
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
      activeView,
      authSelectModalOpen: true,
      wizardOpen: false,
      wizardStep,
      wizardMode,
      selectedType,
      editingId,
      validatedAgentId,
      oauthState,
      connectionVerified,
      oauthConnection,
      formValues: form.getFieldsValue(true),
    });
    navigate("/data-sources/docs/feishu-setup?from=create-source");
  };

  const handleSubmitManualOauthCallback = async () => {
    const parsed = parseFeishuOAuthCallbackInput(manualOauthCallbackValue);
    if (!parsed) {
      message.warning(t("admin.dataSourceOauthManualCallbackInvalid"));
      return;
    }

    try {
      setManualOauthSubmitting(true);
      const connection = await finishFeishuDataSourceOAuth(parsed.code, parsed.state);
      applyOauthResult({
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
      applyOauthResult({
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

  const handleDeleteSource = (record: DataSourceItem) => {
    Modal.confirm({
      title: t("admin.dataSourceDeleteTitle"),
      content: t("admin.dataSourceDeleteContent", { name: record.name }),
      okText: t("common.delete"),
      cancelText: t("common.cancel"),
      okButtonProps: { danger: true },
      icon: <WarningFilled />,
      onOk: async () => {
        try {
          await createScanV2ApiClient().deleteSource({ sourceId: record.id });
          message.success(t("admin.dataSourceDeleteSuccess"));
          const nextPage =
            sources.length <= 1 && sourceListPage > 1
              ? sourceListPage - 1
              : sourceListPage;
          await Promise.all([
            refreshSources(false, { page: nextPage }),
          ]);
        } catch (error) {
          message.error(
            getLocalizedErrorMessage(error, t("admin.dataSourceDeleteFailed")) ||
              t("admin.dataSourceDeleteFailed"),
          );
          throw error;
        }
      },
    });
  };

  const handleNextStep = () => {
    if (wizardStep === 0) {
      if (!selectedType) {
        message.warning(t("admin.dataSourceSelectOneTypeFirst"));
        return;
      }
      if (
        selectedType === "feishu" &&
        !(oauthConnection?.provider === "feishu" && oauthConnection.connectionId)
      ) {
        if (isFeishuSetupReady && feishuAppSetup && oauthState !== "waiting") {
          void startCloudOAuth("feishu", {
            setup: feishuAppSetup,
            draftSelectedType: "feishu",
            draftWizardStep: 0,
            previousState: oauthState,
            previousVerified: connectionVerified,
            previousConnection: oauthConnection,
          });
        }
        message.warning(t("admin.dataSourceOauthRequiredBeforeSave"));
        return;
      }
      if (
        selectedType === "notion" &&
        !(oauthConnection?.provider === "notion" && oauthConnection.connectionId)
      ) {
        if (isNotionSetupReady && notionAppSetup && oauthState !== "waiting") {
          void startCloudOAuth("notion", {
            setup: notionAppSetup,
            draftSelectedType: "notion",
            draftWizardStep: 0,
            previousState: oauthState,
            previousVerified: connectionVerified,
            previousConnection: oauthConnection,
          });
        }
        message.warning(t("admin.dataSourceNotionAuthRequired"));
        return;
      }
      setWizardStep(1);
    }
  };

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
      editingId && selectedType === "local"
        ? sources.find((item) => item.id === editingId && item.type === "local")
        : undefined;
    let datasetIdForLocalSource = currentLocalSource?.datasetId || "";

    if (rootPaths.length === 0) {
      message.warning(t("admin.dataSourceAccessPathRequired"));
      return;
    }

    const client = createScanV2ApiClient();
    const selectedAgent = pickScanAgent(
      scanAgents,
      validatedAgentId || currentLocalSource?.agentId,
    );
    const buildBindingRequest = (targetRef: string) => ({
      connector_type: "local_fs",
      target_type: "local_path",
      target_ref: targetRef,
      sync_mode: isScheduled ? "scheduled" : "manual",
      schedule_policy: schedulePolicy,
      agent_id: selectedAgent?.agent_id || validatedAgentId || currentLocalSource?.agentId,
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

      setValidatedAgentId(selectedAgent?.agent_id || validatedAgentId);
      await refreshSources(false);
      message.success(
        editingId ? t("admin.dataSourceConfigUpdated") : t("admin.dataSourceCreated"),
      );
      handleCloseWizard();
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
      editingId && selectedType === "feishu"
        ? sources.find((item) => item.id === editingId && item.type === "feishu")
        : undefined;

    const authConnectionId =
      oauthConnection?.provider === "feishu" && oauthConnection.connectionId
        ? oauthConnection.connectionId
        : wizardMode === "edit"
          ? currentFeishuSource?.authConnectionId
          : "";

    if (selectedTargetValues.length === 0) {
      message.warning(t("admin.dataSourceFeishuSpaceRequired"));
      return;
    }

    const client = createScanV2ApiClient();
    const selectedAgent = pickScanAgent(
      scanAgents,
      validatedAgentId || currentFeishuSource?.agentId,
    );
    const treeTargetTypeMap = collectFeishuTargetTypes(feishuTargetTreeData);
    const treeTargetRefMap = collectFeishuTargetRefs(feishuTargetTreeData);
    const manualTargetTypeMap = collectManualFeishuTargetTypes(values.target);
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
          manualTargetTypeMap.get(targetRef) ||
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

      setValidatedAgentId(selectedAgent?.agent_id || validatedAgentId);
      await refreshSources(false);
      message.success(
        editingId ? t("admin.dataSourceConfigUpdated") : t("admin.dataSourceCreated"),
      );
      handleCloseWizard();
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
      editingId && selectedType === "notion"
        ? sources.find((item) => item.id === editingId && item.type === "notion")
        : undefined;
    const authConnectionId =
      oauthConnection?.provider === "notion" && oauthConnection.connectionId
        ? oauthConnection.connectionId
        : wizardMode === "edit"
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

    const client = createScanV2ApiClient();
    const selectedAgent = pickScanAgent(
      scanAgents,
      validatedAgentId || currentNotionSource?.agentId,
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
        agent_id: selectedAgent?.agent_id || validatedAgentId || currentNotionSource?.agentId,
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

      setValidatedAgentId(selectedAgent?.agent_id || validatedAgentId);
      await refreshSources(false);
      message.success(
        editingId ? t("admin.dataSourceConfigUpdated") : t("admin.dataSourceCreated"),
      );
      handleCloseWizard();
    } catch (error) {
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    }
  };

  const handleSave = async (saveMode: DataSourceSaveMode = "createAndSync") => {
    if (wizardSaving) {
      return;
    }

    setWizardSaving(true);
    setWizardSavingMode(saveMode);
    try {
      const syncStrategyFields =
        form.getFieldValue("syncMode") === "scheduled"
          ? ["syncMode", "scheduleWeekdays", "scheduleTime", "fileTypes"]
          : ["syncMode", "fileTypes"];

      if (wizardMode === "edit") {
        await form.validateFields(syncStrategyFields);
      } else {
        await form.validateFields();
      }

      const values = form.getFieldsValue(true);
      const effectiveSourceType = resolveSourceTypeFromValues(selectedType, values);

      if (!effectiveSourceType) {
        message.warning(t("admin.dataSourceSelectTypeFirst"));
        return;
      }
      if (effectiveSourceType === "local" && !canCreateLocalSource) {
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
      setWizardSaving(false);
      setWizardSavingMode(null);
    }
  };

  const assetColumns: ColumnsType<DataSourceItem> = [
    {
      title: t("admin.dataSourceTableSource"),
      dataIndex: "name",
      key: "name",
      width: 260,
      render: (_value, record) => (
        <div className="data-source-table-name">
          <span className={`data-source-icon data-source-icon-${record.type}`}>
            {sourceTypeOptions.find((item) => item.type === record.type)?.icon}
          </span>
          <div className="data-source-table-copy">
            <Button
              type="link"
              className="data-source-link-button"
              onClick={() => openDetailPage(record)}
            >
              {record.name}
            </Button>
            <Tooltip title={record.description} placement="topLeft">
              <Text
                type="secondary"
                className="data-source-ellipsis"
                tabIndex={0}
              >
                {record.description}
              </Text>
            </Tooltip>
          </div>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceTableType"),
      dataIndex: "type",
      key: "type",
      width: 180,
      render: (type: SourceType) => (
        <Tag className="data-source-type-tag">{getSourceTypeTitle(type, t)}</Tag>
      ),
    },
    {
      title: t("admin.dataSourceTableKnowledgeBase"),
      dataIndex: "knowledgeBase",
      key: "knowledgeBase",
      width: 130,
      ellipsis: {
        showTitle: false,
      },
      render: (knowledgeBase: string) => (
        <Tooltip title={knowledgeBase} placement="topLeft">
          <span className="data-source-ellipsis">{knowledgeBase}</span>
        </Tooltip>
      ),
    },
    {
      title: t("admin.dataSourceTableSyncStrategy"),
      key: "syncMode",
      width: 205,
      render: (_value, record) => (
        <div className="data-source-sync-cell">
          <Text strong>{getSyncModeLabel(record.syncMode, t)}</Text>
          <Text type="secondary">{record.scheduleLabel}</Text>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceTableConnectionStatus"),
      key: "status",
      width: 105,
      render: (_value, record) => {
        const statusMeta = getStatusMeta(record.status, t);
        const connectionMeta = getConnectionMeta(record.connectionState, t);
        return (
          <Space direction="vertical" size={4}>
            <Tag color={statusMeta.color}>{statusMeta.text}</Tag>
            <Tag color={connectionMeta.color}>{connectionMeta.text}</Tag>
          </Space>
        );
      },
    },
    {
      title: t("admin.dataSourceTableLastSync"),
      key: "lastSync",
      width: 190,
      render: (_value, record) => (
        <div className="data-source-sync-cell">
          <Text>{record.lastSync}</Text>
          <Text type="secondary">{record.nextSync}</Text>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceTableSummary"),
      key: "summary",
      width: 150,
      render: (_value, record) => (
        <div className="data-source-sync-cell">
          <Text type="secondary">
            {t("admin.dataSourceSummaryChanges", {
              add: record.addCount,
              change: record.changeCount,
              del: record.deleteCount,
            })}
          </Text>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceTableActions"),
      key: "actions",
      width: 220,
      fixed: "right",
      className: "data-source-action-column",
      render: (_value, record) => (
        <Space size={12} className="data-source-table-actions">
          <Button type="link" icon={<EyeOutlined />} onClick={() => openDetailPage(record)}>
            {t("admin.dataSourceActionDetail")}
          </Button>
          <Button type="link" icon={<EditOutlined />} onClick={() => openEditWizard(record)}>
            {t("admin.dataSourceActionConfig")}
          </Button>
          <Button
            type="link"
            danger
            icon={<DeleteOutlined />}
            onClick={() => handleDeleteSource(record)}
          >
            {t("common.delete")}
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <div className="admin-page data-source-page">
      <div className="admin-page-toolbar data-source-page-toolbar">
        <div className="admin-page-toolbar-left data-source-page-toolbar-left">
          <div>
            <h2 className="admin-page-title">{t("admin.dataSourceManagement")}</h2>
            <Paragraph className="data-source-page-subtitle">
              {t("admin.dataSourceSubtitle")}
            </Paragraph>
          </div>
        </div>
      </div>

      <div className="data-source-view-tabs">
        <button
          type="button"
          className={activeView === "assets" ? "selected" : ""}
          onClick={() => setActiveView("assets")}
        >
          {t("admin.dataSourceListTitle")}
        </button>
        <button
          type="button"
          className={activeView === "connectors" ? "selected" : ""}
          onClick={() => setActiveView("connectors")}
        >
          {t("admin.dataSourceProviderTitle")}
        </button>
      </div>

      <section className="data-source-workbench">
        {activeView === "assets" ? (
          <main className="data-source-asset-directory">
            <div className="data-source-asset-toolbar">
              <Input
                allowClear
                prefix={<SearchOutlined />}
                value={assetSearchValue}
                onChange={(event) => setAssetSearchValue(event.target.value)}
                placeholder={t("admin.dataSourceAssetSearchPlaceholder")}
                className="data-source-asset-search"
              />
              <Button
                type="primary"
                icon={<PlusOutlined />}
                onClick={() => setCreateProviderModalOpen(true)}
              >
                {t("admin.dataSourceCreateKnowledgeSource")}
              </Button>
            </div>
            <div className="data-source-asset-table-wrap">
              <Table<DataSourceItem>
                className="admin-page-table data-source-asset-table"
                rowKey="id"
                columns={assetColumns}
                dataSource={sources}
                loading={scanLoading}
                pagination={{
                  current: sourceListPage,
                  pageSize: sourceListPageSize,
                  total: sourceListTotal,
                  showSizeChanger: true,
                  showTotal: (total) => t("common.totalItems", { total }),
                  onChange: (page, pageSize) => {
                    void refreshSources(false, {
                      page,
                      pageSize,
                      keyword: assetSearchValue,
                    });
                  },
                }}
                tableLayout="fixed"
                scroll={{ x: 1480, y: "calc(100vh - 380px)" }}
                locale={{
                  emptyText: (
                    <div className="data-source-asset-empty">
                      <DatabaseOutlined />
                      <Text strong>{t("admin.dataSourceAssetNoResultTitle")}</Text>
                      <Text type="secondary">{t("admin.dataSourceAssetNoResultDesc")}</Text>
                    </div>
                  ),
                }}
              />
            </div>
          </main>
        ) : (
          <main className="data-source-provider-panel">
            <div className="data-source-provider-panel-header">
              <div>
                <Text strong className="data-source-provider-title">
                  {t("admin.dataSourceProviderTitle")}
                </Text>
                <Paragraph className="data-source-provider-subtitle">
                  {t("admin.dataSourceProviderSubtitle")}
                </Paragraph>
              </div>
            </div>
            <div className="data-source-provider-grid">
              {canCreateLocalSource ? (
                <div className="data-source-local-scan-card">
                  <span className="data-source-provider-logo data-source-icon-local">
                    <FolderOpenOutlined />
                  </span>
                  <span className="data-source-provider-card-copy">
                    <span className="data-source-provider-title-row">
                      <span className="data-source-provider-name">
                        {t("admin.dataSourceLocalScanChatTitle")}
                      </span>
                    </span>
                    <span className="data-source-provider-desc">
                      {t("admin.dataSourceLocalScanChatDesc", {
                        count: localSourceCount,
                      })}
                    </span>
                  </span>
                  <Tooltip
                    title={
                      t("admin.dataSourceLocalScanChatSwitchHint")
                    }
                  >
                    <button
                      type="button"
                      role="switch"
                      aria-checked={localScanChatEnabled}
                      aria-label={t("admin.dataSourceLocalScanChatSwitchAria")}
                      disabled={localScanChatSaving}
                      className={`data-source-chat-switch${localScanChatEnabled ? " is-on" : ""}${
                        localScanChatSaving ? " is-disabled" : ""
                      }`}
                      onClick={() => {
                        void handleToggleLocalScanChat(!localScanChatEnabled);
                      }}
                    >
                      <span className="data-source-chat-switch-thumb" aria-hidden="true" />
                      <span className="data-source-chat-switch-label">
                        {localScanChatEnabled
                          ? t("admin.dataSourceLocalScanChatSwitchEnabledStatus")
                          : t("admin.dataSourceLocalScanChatSwitchDisabledStatus")}
                      </span>
                    </button>
                  </Tooltip>
                </div>
              ) : null}
              {providerAuthOptions.map((item) => {
                const isFeishu = item.type === "feishu";
                const isAuthValid = isFeishu ? isFeishuAuthValid : isNotionAuthValid;
                const isSetupReady = isFeishu ? isFeishuSetupReady : isNotionSetupReady;
                const isProviderLocked = !isAuthValid && !isSetupReady;
                const authStatusText = isAuthValid
                  ? t("admin.dataSourceProviderAuthValid")
                  : isProviderLocked
                    ? t("admin.dataSourceProviderCredentialMissing")
                    : t("admin.dataSourceProviderAuthPending");
                return (
                  <button
                    key={item.type}
                    type="button"
                    className={`data-source-provider-card ${isProviderLocked ? "locked" : ""}`}
                    onClick={() => {
                      if (isFeishu) {
                        handleManageFeishuAuth();
                        return;
                      }
                      if (isNotionAuthValid) {
                        openSourceCreateWizard("notion", { connection: notionOauthConnection });
                        return;
                      }
                      openCloudSetupModal("notion", "create");
                    }}
                  >
                    <span className={`data-source-provider-logo data-source-icon-${item.type}`}>
                      {item.logoUrl ? (
                        <img
                          alt=""
                          aria-hidden="true"
                          loading="lazy"
                          src={item.logoUrl}
                          onError={(event) => {
                            event.currentTarget.style.display = "none";
                          }}
                        />
                      ) : (
                        item.icon
                      )}
                    </span>
                    <span className="data-source-provider-card-copy">
                      <span className="data-source-provider-title-row">
                        <span className="data-source-provider-name">
                          {getSourceTypeTitle(item.type, t)}
                        </span>
                        {item.adminOnly ? (
                          <Tag color="orange">{t("admin.dataSourceAdminOnly")}</Tag>
                        ) : null}
                        {item.type === "feishu" || item.type === "notion" ? (
                          <Tag
                            color={
                              isAuthValid
                                ? "success"
                                : isProviderLocked
                                  ? "default"
                                  : "processing"
                            }
                          >
                            {authStatusText}
                          </Tag>
                        ) : null}
                      </span>
                      <span className="data-source-provider-desc">
                        {isAuthValid
                          ? isFeishu
                            ? t("admin.dataSourceFeishuAuthConnectedHint", {
                                account:
                                  oauthConnection?.provider === "feishu"
                                    ? oauthConnection.accountName
                                    : t("admin.dataSourceFeishuConnectedAccountFallback"),
                              })
                            : t("admin.dataSourceNotionConnected", {
                                account: notionOauthConnection?.accountName || "Notion workspace",
                              })
                          : isProviderLocked
                            ? isFeishu
                              ? t("admin.dataSourceFeishuLockHint")
                              : t("admin.dataSourceNotionSetupRequiredHint")
                            : isFeishu
                              ? t("admin.dataSourceFeishuAuthReadyHint")
                              : t("admin.dataSourceNotionAuthPendingHint")}
                      </span>
                    </span>
                    <span className="data-source-provider-card-arrow" aria-hidden="true">
                      <ArrowRightOutlined />
                    </span>
                  </button>
                );
              })}
            </div>
          </main>
        )}
      </section>

      <Modal
        title={t("admin.dataSourceCreateKnowledgeSource")}
        open={createProviderModalOpen}
        footer={null}
        width={720}
        destroyOnHidden
        onCancel={() => setCreateProviderModalOpen(false)}
      >
        <Paragraph className="data-source-create-provider-intro">
          {t("admin.dataSourceCreateProviderIntro")}
        </Paragraph>
        <div className="data-source-create-provider-grid">
          {creatableSourceTypeOptions.map((item) => {
            const isFeishu = item.type === "feishu";
            const isNotion = item.type === "notion";
            const isCloudProvider = isFeishu || isNotion;
            const isAuthValid = isFeishu ? isFeishuAuthValid : isNotion ? isNotionAuthValid : false;
            const isSetupReady = isFeishu ? isFeishuSetupReady : isNotion ? isNotionSetupReady : true;
            const isProviderLocked = isCloudProvider && !isAuthValid && !isSetupReady;
            const authStatusText = isAuthValid
              ? t("admin.dataSourceProviderAuthValid")
              : isSetupReady
                ? t("admin.dataSourceProviderAuthPending")
                : t("admin.dataSourceProviderCredentialMissing");
            return (
              <button
                key={item.type}
                type="button"
                className={`data-source-create-provider-card ${
                  isProviderLocked ? "locked" : ""
                }`}
                onClick={() => handleCreateProviderSelect(item.type)}
              >
                <span className={`data-source-provider-logo data-source-icon-${item.type}`}>
                  {item.logoUrl ? (
                    <img
                      alt=""
                      aria-hidden="true"
                      loading="lazy"
                      src={item.logoUrl}
                      onError={(event) => {
                        event.currentTarget.style.display = "none";
                      }}
                    />
                  ) : (
                    item.icon
                  )}
                </span>
                <span className="data-source-provider-card-copy">
                  <span className="data-source-provider-title-row">
                    <span className="data-source-provider-name">
                      {getSourceTypeTitle(item.type, t)}
                    </span>
                    {item.adminOnly ? (
                      <Tag color="orange">{t("admin.dataSourceAdminOnly")}</Tag>
                    ) : null}
                    {isCloudProvider ? (
                      <Tag color={isAuthValid ? "success" : isSetupReady ? "processing" : "default"}>
                        {authStatusText}
                      </Tag>
                    ) : null}
                  </span>
                  <span className="data-source-provider-desc">
                    {isProviderLocked
                      ? isFeishu
                        ? t("admin.dataSourceCreateFeishuAuthRequiredHint")
                        : t("admin.dataSourceNotionSetupRequiredForCreate")
                      : getSourceTypeDescription(item.type, t)}
                  </span>
                </span>
                <span className="data-source-provider-card-arrow" aria-hidden="true">
                  <ArrowRightOutlined />
                </span>
              </button>
            );
          })}
        </div>
      </Modal>

      <Modal
        title={
          <div className="data-source-auth-select-title">
            <span>{t("admin.dataSourceSelectFeishuAuthTitle")}</span>
            <Button
              type="link"
              size="small"
              className="data-source-auth-select-guide"
              icon={<FileTextOutlined />}
              onClick={handleOpenFeishuGuideFromAuthSelect}
            >
              {t("admin.dataSourceFeishuSetupGuideAction")}
            </Button>
          </div>
        }
        open={authSelectModalOpen}
        footer={null}
        width={640}
        destroyOnHidden
        onCancel={() => setAuthSelectModalOpen(false)}
      >
        <Paragraph className="data-source-create-provider-intro">
          {t("admin.dataSourceSelectFeishuAuthIntro")}
        </Paragraph>
        <Space direction="vertical" size={10} style={{ width: "100%" }}>
          {validFeishuAccounts.map((account) => (
            <button
              key={account.id}
              type="button"
              className="data-source-auth-option-card"
              onClick={() => {
                if (account.connection) {
                  handleSelectFeishuAuthConnection(account.connection);
                }
              }}
            >
              <span className="data-source-provider-logo data-source-icon-feishu">
                <img
                  alt=""
                  aria-hidden="true"
                  loading="lazy"
                  src="https://www.google.com/s2/favicons?domain=feishu.cn&sz=96"
                  onError={(event) => {
                    event.currentTarget.style.display = "none";
                  }}
                />
              </span>
              <span className="data-source-provider-card-copy">
                <span className="data-source-provider-title-row">
                  <span className="data-source-provider-name">
                    {account.connection?.accountName || account.name}
                  </span>
                  <Tag color="success">{t("admin.dataSourceProviderAuthValid")}</Tag>
                </span>
                <span className="data-source-provider-desc">
                  {account.connection?.connectionId}
                </span>
              </span>
              <span className="data-source-provider-card-arrow" aria-hidden="true">
                <ArrowRightOutlined />
              </span>
            </button>
          ))}
        </Space>
      </Modal>

      <Modal
        title={t("admin.dataSourceOauthManualCallbackTitle")}
        open={manualOauthModalOpen}
        onCancel={() => {
          if (!manualOauthSubmitting) {
            setManualOauthModalOpen(false);
          }
        }}
        onOk={handleSubmitManualOauthCallback}
        okText={t("admin.dataSourceOauthManualCallbackConfirm")}
        okButtonProps={{ loading: manualOauthSubmitting }}
        cancelText={t("common.cancel")}
        destroyOnHidden
      >
        <Space direction="vertical" size={12} style={{ width: "100%" }}>
          <Alert
            showIcon
            type="info"
            message={t("admin.dataSourceOauthManualCallbackDesc")}
          />
          <Input.TextArea
            value={manualOauthCallbackValue}
            onChange={(event) => setManualOauthCallbackValue(event.target.value)}
            placeholder={t("admin.dataSourceOauthManualCallbackPlaceholder")}
            autoSize={{ minRows: 3, maxRows: 6 }}
          />
        </Space>
      </Modal>

      <Modal
        title={
          cloudSetupProvider === "feishu"
            ? t("admin.dataSourceFeishuCredentialModalTitle")
            : t("admin.dataSourceNotionCredentialModalTitle")
        }
        open={feishuSetupModalOpen}
        destroyOnHidden
        onCancel={() => {
          if (feishuSetupSubmitting) {
            return;
          }
          setFeishuSetupModalOpen(false);
          setFeishuSetupIntent(null);
        }}
        onOk={handleSaveFeishuSetup}
        okText={
          feishuSetupIntent
            ? cloudSetupProvider === "feishu"
              ? t("admin.dataSourceFeishuCredentialSaveAndSelect")
              : t("admin.dataSourceNotionCredentialSaveAndSelect")
            : t("common.save")
        }
        okButtonProps={{ loading: feishuSetupSubmitting }}
        cancelButtonProps={{ disabled: feishuSetupSubmitting }}
        cancelText={t("common.cancel")}
      >
        <Form form={feishuSetupForm} layout="vertical">
          <Form.Item
            label={t("admin.dataSourceFeishuAccountName")}
            name="name"
          >
            <Input placeholder={t("admin.dataSourceFeishuAccountNamePlaceholder")} />
          </Form.Item>
          <Form.Item
            label={t("admin.dataSourceAppId")}
            name="appId"
            rules={[{ required: true, message: t("admin.dataSourceAppIdRequired") }]}
          >
            <Input placeholder={t("admin.dataSourceAppIdPlaceholder")} />
          </Form.Item>
          <Form.Item
            label={t("admin.dataSourceAppSecret")}
            name="appSecret"
            rules={[{ required: true, message: t("admin.dataSourceAppSecretRequired") }]}
          >
            <Input.Password placeholder={t("admin.dataSourceAppSecretPlaceholder")} />
          </Form.Item>
          {cloudSetupProvider === "feishu" ? (
            <FeishuCredentialHintAlertFromForm form={feishuSetupForm} />
          ) : (
            <Alert
              showIcon
              type="info"
              message={t("admin.dataSourceNotionCredentialHint")}
            />
          )}
          {cloudSetupProvider !== "feishu" && (
            <Paragraph style={{ marginTop: 12, marginBottom: 0 }}>
              <a
                href="/data-sources/docs/notion-setup?from=create-source"
                target="_blank"
                rel="noreferrer"
              >
                {t("admin.dataSourceNotionSetupGuideAction")}
              </a>
              {t("admin.dataSourceNotionSetupGuideHint")}
            </Paragraph>
          )}
        </Form>
      </Modal>

      <DataSourceWizardModal
        t={t}
        wizardMode={wizardMode}
        wizardOpen={wizardOpen}
        wizardStep={wizardStep}
        form={form}
        selectedType={selectedType}
        isFeishuSetupReady={isFeishuSetupReady}
        isNotionSetupReady={isNotionSetupReady}
        syncMode={syncMode}
        saving={wizardSaving}
        savingMode={wizardSavingMode || undefined}
        localPathOptions={localPathOptions}
        localPathLoading={localPathLoading}
        feishuTargetLoading={feishuTargetLoading}
        feishuTargetTreeData={feishuTargetTreeData}
        allowTypeSelection={false}
        onClose={handleCloseWizard}
        onPrev={() => setWizardStep((step) => step - 1)}
        onNext={handleNextStep}
        onSave={(mode) => {
          void handleSave(mode);
        }}
        onSelectType={handleSelectType}
        onResetFeishuSetup={handleResetFeishuSetup}
        onResetNotionSetup={handleResetNotionSetup}
        onLoadLocalPathOptions={(path) => {
          void loadLocalPathOptions(path);
        }}
        onSearchLocalPathOptions={handleSearchLocalPathOptions}
        onLoadLocalPathChildren={handleLoadLocalPathChildren}
        onLoadFeishuTargetOptions={() => {
          void loadFeishuTargetOptions();
        }}
        onSearchFeishuTargetOptions={handleSearchFeishuTargetOptions}
        onLoadFeishuTargetChildren={handleLoadFeishuTargetChildren}
      />
    </div>
  );
}
