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
import { useLocation, useNavigate } from "react-router-dom";
import {
  type CloudConnectionResponse,
} from "@/api/generated/auth-client";
import {
  type Dataset as CoreDataset,
} from "@/api/generated/core-client";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  dataSourceCloudOauthApi,
  dataSourceDatasetsApi,
  dataSourceModelProvidersApi,
  unwrapDataSourceApiData,
} from "./api";

import "./index.scss";
import DataSourceWizardModal from "./components/DataSourceWizardModal";
import ExternalServiceConfigModal, {
  type ExternalServiceConfigModalService,
} from "@/modules/modelProvider/components/ExternalServiceConfigModal";
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
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  clearFeishuDataSourceWizardDraft,
  consumeFeishuDataSourceOAuthResult,
  consumeFeishuDataSourceWizardDraft,
  finishFeishuDataSourceOAuth,
  openCenteredPopup,
  requestFeishuDataSourceAuthorizeUrl,
  saveFeishuDataSourceWizardDraft,
  type FeishuConnectionStatus,
  type FeishuDataSourceConnection,
  type FeishuDataSourceOAuthMessage,
  type FeishuDataSourceWizardDraft,
} from "@/modules/dataSource/common/feishuOAuth";
import {
  CLOUD_SYNC_POLL_INTERVAL_MS,
  CLOUD_SYNC_TIMEOUT_MS,
  FEISHU_DEFAULT_SCOPES,
  FEISHU_EXCLUDE_PATTERNS,
  FEISHU_INCLUDE_PATTERNS,
  FEISHU_MAX_OBJECT_SIZE_BYTES,
  type DataSourceItem,
  type DetailDocumentItem,
  type FeishuAppSetup,
  type FeishuTargetType,
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
const LOCAL_SCAN_CHAT_STORAGE_KEY = "lazymind:datasource:local-scan:chat-enabled";
const LOCAL_PATH_CACHE_ROOT_KEY = "__root__";
const FEISHU_TARGET_CACHE_ROOT_KEY = "__root__";
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
type DataSourceSaveMode = "create" | "createAndSync";
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

function normalizeKnowledgeBaseName(value?: string) {
  return `${value || ""}`.trim().toLowerCase();
}

function resolveSourceTypeFromValues(
  fallbackType: SourceType | null,
  values: SourceFormValues,
): SourceType | null {
  const localPaths = normalizeLocalPathRefs(values.path);
  const feishuTargets = normalizeFeishuTargetRefs(values.target);
  if (localPaths.length > 0 && feishuTargets.length === 0) {
    return "local";
  }
  if (feishuTargets.length > 0 && localPaths.length === 0) {
    return "feishu";
  }
  return fallbackType;
}

function getDatasetDisplayName(dataset: CoreDataset) {
  return `${dataset.display_name || dataset.name || ""}`.trim();
}

function loadLocalScanChatEnabled() {
  try {
    return localStorage.getItem(LOCAL_SCAN_CHAT_STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

function persistLocalScanChatEnabled(enabled: boolean) {
  localStorage.setItem(LOCAL_SCAN_CHAT_STORAGE_KEY, enabled ? "true" : "false");
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
];
const providerAuthOptions = sourceTypeOptions.filter((item) => item.type === "feishu");

const datasourceConnectors: Array<{ key: string; name: string; icon: ReactNode; logoUrl?: string }> = [
  {
    key: "sciverse",
    name: "Sciverse",
    icon: <SearchOutlined />,
    logoUrl: "https://www.google.com/s2/favicons?domain=sciverse.space&sz=96",
  },
];

function normalizeProviderName(value: string) {
  return value.trim().toLowerCase().replace(/[^a-z0-9]+/g, "");
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

  return {
    id: connection.connection_id,
    name: displayName,
    appId,
    appSecret: cachedAccount?.appSecret || "",
    chatEnabled: cachedAccount?.chatEnabled ?? false,
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

async function listKnowledgeBaseNames(client = dataSourceDatasetsApi) {
  const names: string[] = [];
  let pageToken: string | undefined;

  for (let pageIndex = 0; pageIndex < 20; pageIndex += 1) {
    const response = await client.apiCoreDatasetsGet({
      pageToken,
      pageSize: 200,
    });
    names.push(
      ...(response.data.datasets || []).map(getDatasetDisplayName).filter(Boolean),
    );

    const nextPageToken = response.data.next_page_token || "";
    if (!nextPageToken || nextPageToken === pageToken) {
      break;
    }
    pageToken = nextPageToken;
  }

  return names;
}

async function listDefaultKnowledgeBaseIds(client = dataSourceDatasetsApi) {
  const ids: string[] = [];
  let pageToken: string | undefined;

  for (let pageIndex = 0; pageIndex < 20; pageIndex += 1) {
    const response = await client.apiCoreDatasetsGet({
      pageToken,
      pageSize: 200,
    });
    ids.push(
      ...(response.data.datasets || [])
        .filter((dataset) => dataset.default_dataset)
        .map((dataset) => dataset.dataset_id)
        .filter(Boolean),
    );

    const nextPageToken = response.data.next_page_token || "";
    if (!nextPageToken || nextPageToken === pageToken) {
      break;
    }
    pageToken = nextPageToken;
  }

  return ids;
}

function sleep(ms: number) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

async function waitForCloudSyncRun(
  client: ScanV2Client,
  sourceId: string,
  runId?: string,
) {
  const deadline = Date.now() + CLOUD_SYNC_TIMEOUT_MS;

  while (Date.now() < deadline) {
    const [detailResponse, summaryResponse] = await Promise.all([
      client.getSource({ sourceId }).catch(() => null),
      client.getSourceSummary({ sourceId }).catch(() => null),
    ]);
    const bindings = (detailResponse?.data.bindings || []) as ScanV2Binding[];
    const summary = summaryResponse?.data as Record<string, any> | undefined;
    const matchedBinding = bindings.find((item) => {
      const status = `${item.status || ""}`.toUpperCase();
      return status === "ACTIVE" || status === "ERROR";
    });
    const status = `${matchedBinding?.status || summary?.status || ""}`.toUpperCase();

    if (status === "ACTIVE" || summary?.total_objects !== undefined) {
      return { run_id: runId, status: status || "SUCCEEDED" };
    }

    if (
      status.includes("FAILED") ||
      status.includes("ERROR") ||
      status.includes("CANCEL")
    ) {
      throw new Error(getBindingLastError(matchedBinding) || "飞书云同步失败，请检查绑定配置后重试。");
    }

    await sleep(CLOUD_SYNC_POLL_INTERVAL_MS);
  }

  throw new Error("等待飞书目录同步超时，请稍后重试。");
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

function collectFeishuTargetTypes(
  nodes: FeishuTargetTreeNode[],
  inheritedTargetType?: FeishuTargetType,
  targetTypes = new Map<string, FeishuTargetType>(),
) {
  nodes.forEach((node) => {
    const targetRef = `${node.targetRef || node.value || ""}`.trim();
    const nodeRef = `${node.nodeRef || ""}`.trim();
    const targetType =
      normalizeFeishuTargetType(
        node.targetType,
        `${targetRef || nodeRef || node.value || ""}`,
      ) || inheritedTargetType;

    if (targetType) {
      [targetRef, nodeRef, `${node.value || ""}`.trim()]
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

function normalizeFeishuTargetRefs(value?: SourceFormValues["target"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values.map((item) => `${item || ""}`.trim()).filter(Boolean);
}

function normalizeLocalPathRefs(value?: SourceFormValues["path"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values.map((item) => `${item || ""}`.trim()).filter(Boolean);
}

function hasFeishuTargetTypes(targetTypes?: Record<string, FeishuTargetType>) {
  return Boolean(targetTypes && Object.keys(targetTypes).length > 0);
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
  targetTypes?: Record<string, FeishuTargetType>,
) {
  if (!targetTypes) {
    return undefined;
  }

  const normalizedTypes: Record<string, FeishuTargetType> = {};
  Object.entries(targetTypes).forEach(([targetRef, targetType]) => {
    const normalizedTargetRef = `${targetRef || ""}`.trim();
    const normalizedTargetType = normalizeFeishuTargetType(
      targetType,
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

function mapScanSyncDetail(updateState: FileUpdateState) {
  if (updateState === "new") {
    return "新文件待入库";
  }
  if (updateState === "changed") {
    return "内容变化待重解析";
  }
  if (updateState === "deleted") {
    return "源端删除待清理";
  }
  return "当前文件已是最新";
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
  const location = useLocation();
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
  const [externalServiceModalOpen, setExternalServiceModalOpen] = useState(false);
  const [activeExternalService, setActiveExternalService] =
    useState<ExternalServiceConfigModalService | null>(null);
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
  const [feishuSetupModalOpen, setFeishuSetupModalOpen] = useState(false);
  const [feishuSetupIntent, setFeishuSetupIntent] =
    useState<FeishuSetupIntent>(null);
  const [feishuSetupSubmitting, setFeishuSetupSubmitting] = useState(false);
  const [feishuSetupForm] = Form.useForm<FeishuAccountFormValues>();
  const [manualOauthModalOpen, setManualOauthModalOpen] = useState(false);
  const [manualOauthCallbackValue, setManualOauthCallbackValue] = useState("");
  const [manualOauthSubmitting, setManualOauthSubmitting] = useState(false);
  const oauthAttemptRef = useRef<PendingOAuthAttempt | null>(null);
  const scanAgents: ScanV2AgentHint[] = [];
  const [knowledgeBaseNames, setKnowledgeBaseNames] = useState<string[]>([]);
  const [defaultDatasetIds, setDefaultDatasetIds] = useState<string[]>([]);
  const [localScanChatEnabled, setLocalScanChatEnabled] = useState(
    loadLocalScanChatEnabled,
  );
  const [localScanChatSaving, setLocalScanChatSaving] = useState(false);
  const [scanLoading, setScanLoading] = useState(false);
  const [validatedAgentId, setValidatedAgentId] = useState<string | null>(null);
  const [wizardSaving, setWizardSaving] = useState(false);
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
  const validFeishuAccounts = feishuAuthAccounts.filter(
    (account) =>
      account.status === "connected" && Boolean(account.connection?.connectionId),
  );
  const isFeishuAuthValid = validFeishuAccounts.length > 0;
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
        title: `使用当前输入：${normalizedPath}`,
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
        nodes.length > 0 ? nodes : buildManualLocalPathOptions("", "未获取到可选目录");
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
            ? getLocalizedErrorMessage(error, "目录列表获取失败，可先手动输入路径")
            : "后端未返回可用扫描 Agent，暂时只能手动输入路径",
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
        buildFeishuHelperNode("请先完成飞书授权，再联机选择空间或文件夹"),
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
      const nextNodes =
        nodes.length > 0 ? nodes : [buildFeishuHelperNode("未获取到可选飞书目标")];
      feishuTargetOptionsCacheRef.current.set(cacheKey, nextNodes);
      setFeishuTargetTreeData(nextNodes);
    } catch (error) {
      if (feishuTargetRequestSeqRef.current !== requestSeq) {
        return;
      }
      setFeishuTargetTreeData([
        buildFeishuHelperNode(
          getLocalizedErrorMessage(error, "飞书目录列表获取失败，可先手动输入目标 ID") ||
            "飞书目录列表获取失败，可先手动输入目标 ID",
        ),
      ]);
    } finally {
      if (feishuTargetRequestSeqRef.current === requestSeq) {
        setFeishuTargetLoading(false);
      }
    }
  };

  const handleSearchFeishuTargetOptions = (keyword: string) => {
    if (feishuTargetSearchTimerRef.current) {
      clearTimeout(feishuTargetSearchTimerRef.current);
    }
    feishuTargetSearchTimerRef.current = setTimeout(() => {
      void loadFeishuTargetOptions(keyword);
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

  const applyDatasetChatDefault = async (
    datasetId: string,
    datasetName: string,
    chatEnabled: boolean,
  ) => {
    const client = dataSourceDatasetsApi;
    if (chatEnabled) {
      await client.apiCoreDatasetsDatasetSetDefaultPost({
        dataset: datasetId,
        setDefaultDatasetRequest: { name: datasetName },
      });
      return;
    }

    await client.apiCoreDatasetsDatasetUnsetDefaultPost({
      dataset: datasetId,
      unsetDefaultDatasetRequest: { name: datasetName },
    });
  };

  const syncDefaultDatasetState = (datasetIds: string[], chatEnabled: boolean) => {
    setDefaultDatasetIds((current) => {
      const next = new Set(current);
      datasetIds.forEach((datasetId) => {
        if (chatEnabled) {
          next.add(datasetId);
        } else {
          next.delete(datasetId);
        }
      });
      return [...next];
    });
  };

  const handleToggleLocalScanChat = async (chatEnabled: boolean) => {
    if (localScanChatSaving) {
      return;
    }

    const localSources = sources.filter((item) => item.type === "local");
    const localSourcesWithDataset = localSources.filter((item) => item.datasetId);
    const previousValue = localScanChatEnabled;
    setLocalScanChatSaving(true);
    setLocalScanChatEnabled(chatEnabled);

    try {
      await Promise.all(
        localSourcesWithDataset.map((source) =>
          applyDatasetChatDefault(
            source.datasetId || "",
            source.knowledgeBase || source.name,
            chatEnabled,
          ),
        ),
      );
      syncDefaultDatasetState(
        localSourcesWithDataset
          .map((source) => source.datasetId)
          .filter((datasetId): datasetId is string => Boolean(datasetId)),
        chatEnabled,
      );
      persistLocalScanChatEnabled(chatEnabled);
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
    const isFeishuSource = inferSourceKind(source, binding) === "feishu";
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
    const storageUsed = fallback?.storageUsed || "0 B";

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
      addCount,
      deleteCount,
      changeCount,
      permissions: [t("admin.dataSourcePermissionReadOnly")],
      conflictPolicy: "overwrite",
      enabled: sourceStatus === "active",
      scopeMode: "all",
      selectedFiles: [],
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
      const [sourcesResponse, nextDefaultDatasetIds] = await Promise.all([
        client.listSources({
          keyword: keyword || undefined,
          page: nextPage,
          pageSize: nextPageSize,
        }),
        listDefaultKnowledgeBaseIds().catch((error) => {
          console.error("Failed to refresh default knowledge bases", error);
          return defaultDatasetIds;
        }),
      ]);
      const sourceList = (sourcesResponse.data.items || []) as ScanV2Source[];
      const previousSourceMap = new Map(
        sources.map((item) => [item.id, item]),
      );
      const nextSources = await Promise.all(
        sourceList.map(async (source) => {
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
      const localDatasetIds = nextSources
        .filter((item) => item.type === "local")
        .map((item) => item.datasetId)
        .filter((datasetId): datasetId is string => Boolean(datasetId));
      const nextLocalScanChatEnabled = loadLocalScanChatEnabled();

      setDefaultDatasetIds(nextDefaultDatasetIds);
      setLocalScanChatEnabled(nextLocalScanChatEnabled);
      if (localDatasetIds.length > 0 && nextLocalScanChatEnabled) {
        syncDefaultDatasetState(localDatasetIds, true);
      }
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

  const refreshKnowledgeBaseNames = async () => {
    try {
      setKnowledgeBaseNames(await listKnowledgeBaseNames());
    } catch (error) {
      console.error("Failed to refresh knowledge base names", error);
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
      if (nextOauthState === "connected") {
        setFeishuAuthAccounts((current) => {
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
        form.setFieldsValue(draft.formValues);
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
    void refreshKnowledgeBaseNames();
    void refreshFeishuAuthAccounts();
  }, []);

  useEffect(() => {
    if (activeView !== "connectors" || feishuAuthAccountsLoadedRef.current) {
      return;
    }
    void refreshFeishuAuthAccounts();
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

  const getKnownKnowledgeBaseNames = () => [
    ...knowledgeBaseNames,
    ...sources.map((item) => item.knowledgeBase),
  ];

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
          : undefined,
      targetType: record.type === "feishu" ? record.targetType || "wiki_space" : undefined,
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
      targetType: type === "feishu" ? "wiki_space" : undefined,
    });
  };

  const startFeishuOAuth = async (options?: {
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
    const activeSetup = options?.setup || feishuAppSetup;
    const previousState = options?.previousState ?? oauthState;
    const previousVerified = options?.previousVerified ?? connectionVerified;
    const previousConnection = options?.previousConnection ?? oauthConnection;

    try {
      if (!activeSetup?.appId.trim() || !activeSetup.appSecret.trim()) {
        message.warning(t("admin.dataSourceFeishuCredentialRequired"));
        return false;
      }

      const selectedAgent = pickScanAgent(scanAgents, validatedAgentId || undefined) || {
        agent_id: validatedAgentId || "",
        tenant_id: getScanTenantId(),
      };

      setOauthState("waiting");
      setValidatedAgentId(selectedAgent.agent_id || null);
      const authorizeUrl = await requestFeishuDataSourceAuthorizeUrl({
        tenantId: selectedAgent.tenant_id || getScanTenantId(),
        appId: activeSetup.appId,
        appSecret: activeSetup.appSecret,
        scopes: FEISHU_DEFAULT_SCOPES,
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

      const popup = openCenteredPopup(authorizeUrl, t("admin.dataSourceFeishuAuthWindowTitle"));

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

  const openFeishuSetupModal = (
    intent: FeishuSetupIntent = null,
    account?: FeishuAuthAccount | null,
  ) => {
    setFeishuSetupIntent(intent);
    setEditingFeishuAccountId(account?.id || null);
    feishuSetupForm.setFieldsValue({
      name: account?.name || "",
      appId: account?.appId || feishuAppSetup?.appId || "",
      appSecret: account?.appSecret || feishuAppSetup?.appSecret || "",
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
      const shouldStartOAuth = feishuSetupIntent === "create" || feishuSetupIntent === "auth";
      const nextAccount = upsertFeishuAuthAccount(values, "waiting");

      persistFeishuAppSetup(nextSetup);
      setFeishuAppSetup(nextSetup);
      setFeishuSetupModalOpen(false);
      const setupIntent = feishuSetupIntent;
      setFeishuSetupIntent(null);
      setEditingFeishuAccountId(null);
      message.success(t("admin.dataSourceFeishuCredentialSaved"));

      if (shouldStartOAuth) {
        resetWizard();
        setWizardMode("create");
        setEditingId(null);
        const feishuFormValues = {
          syncMode: "scheduled",
          scheduleWeekdays: DEFAULT_SCHEDULE_WEEKDAYS,
          scheduleTime: DEFAULT_SCHEDULE_TIME,
          conflictPolicy: "versioned",
          path: [],
          target: [],
          targetType: "wiki_space",
        };

        applySourceType("feishu");
        setWizardOpen(setupIntent === "create");
        setWizardStep(1);
        await startFeishuOAuth({
          setup: nextSetup,
          draftSelectedType: "feishu",
          draftWizardStep: 1,
          draftWizardMode: "create",
          draftEditingId: null,
          draftFormValues: feishuFormValues,
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

  const handleSelectType = (type: SourceType) => {
    if (type === "feishu" && !isFeishuSetupReady) {
      openFeishuSetupModal("create");
      return;
    }
    applySourceType(type);
  };

  const openSourceCreateWizard = (
    type: SourceType,
    options?: { connection?: FeishuDataSourceConnection | null },
  ) => {
    const reusableConnection =
      type === "feishu"
        ? options?.connection || oauthConnection
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
      type === "feishu" &&
      reusableConnection?.connectionId &&
      getOAuthStateFromConnection(reusableConnection) === "connected"
    ) {
      setOauthConnection(reusableConnection);
      setOauthState("connected");
      setConnectionVerified(true);
    }
  };

  const handleCreateProviderSelect = (type: SourceType) => {
    if (type !== "feishu") {
      setCreateProviderModalOpen(false);
      openSourceCreateWizard(type);
      return;
    }

    if (isFeishuAuthValid) {
      setCreateProviderModalOpen(false);
      setAuthSelectModalOpen(true);
      return;
    }

    setCreateProviderModalOpen(false);
    resetWizard();
    setWizardMode("create");
    setEditingId(null);
    applySourceType("feishu");
    setWizardStep(1);

    if (!isFeishuAuthValid) {
      openFeishuSetupModal("create");
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

  const handleManageDatasourceConnector = async (connector: typeof datasourceConnectors[number]) => {
    if (connector.key !== "sciverse") {
      return;
    }
    try {
      const response = await dataSourceModelProvidersApi.apiCoreModelProvidersGet({
        category: "datasource",
        keyword: connector.name,
      });
      const providers = unwrapDataSourceApiData<{
        providers?: Array<{
          id: string;
          name: string;
          description?: string;
          is_configured?: boolean;
        }>;
      }>(response.data).providers || [];
      const provider = providers.find(
        (item) => normalizeProviderName(item.name) === normalizeProviderName(connector.name)
      );
      if (!provider) {
        message.error(t("modelProvider.external.loadFailed"));
        return;
      }
      setActiveExternalService({
        key: provider.id,
        name: provider.name || connector.name,
        description:
          provider.description ||
          t("modelProvider.external.sciverseDesc", { defaultValue: "Sciverse 面向科研场景提供论文搜索、元数据检索和文献内容读取能力。" }),
        fields: ["apiKey"],
        logo: connector.icon,
        logoUrl: connector.logoUrl || "",
        tone: "violet",
        status: provider.is_configured ? "configured" : "missing",
        category: "tools",
        providerCategory: "datasource",
        baseUrl: "https://api.sciverse.space",
      });
      setExternalServiceModalOpen(true);
    } catch (error) {
      message.error(getLocalizedErrorMessage(error, t("modelProvider.external.loadFailed")));
    }
  };

  useEffect(() => {
    const params = new URLSearchParams(location.search);
    if (params.get("provider") !== "sciverse") {
      return;
    }
    const connector = datasourceConnectors.find((item) => item.key === "sciverse");
    if (!connector) {
      return;
    }
    setActiveView("connectors");
    void handleManageDatasourceConnector(connector);

    params.delete("provider");
    params.set("view", "connectors");
    navigate(
      {
        pathname: "/data-sources",
        search: `?${params.toString()}`,
      },
      { replace: true },
    );
  }, [location.search]);

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

  const handleTestConnection = async () => {
    if (selectedType !== "local") {
      setConnectionVerified(true);
      message.success(t("admin.dataSourceConnectionTestSuccess"));
      return;
    }

    try {
      await form.validateFields(["path"]);
      const { path = "" } = form.getFieldsValue(["path"]);
      const normalizedPaths = normalizeLocalPathRefs(path);

      if (normalizedPaths.length === 0) {
        message.warning(t("admin.dataSourceAccessPathRequired"));
        return;
      }

      const preferredAgentId =
        validatedAgentId ||
        (editingId
          ? sources.find((item) => item.id === editingId)?.agentId
          : undefined);
      const selectedAgent = pickScanAgent(scanAgents, preferredAgentId);
      const validateResponses = await Promise.all(
        normalizedPaths.map((normalizedPath) =>
          createScanV2ApiClient().validateBindingTarget({
            validateBindingTargetRequest: {
              connector_type: "local_fs",
              target_type: "local_path",
              target_ref: normalizedPath,
              agent_id: selectedAgent?.agent_id || preferredAgentId || undefined,
            },
          }),
        ),
      );
      const passed = validateResponses.every((response) => {
        const validation = response.data;
        return (
          Boolean(validation.target_ref) &&
          Boolean(validation.target_fingerprint || validation.root_object_key)
        );
      });

      setConnectionVerified(passed);
      if (passed) {
        setValidatedAgentId(selectedAgent?.agent_id || preferredAgentId || null);
        message.success(t("admin.dataSourceConnectionTestSuccess"));
        return;
      }

      message.error("路径校验未通过，请检查目录是否存在且具备只读权限。");
    } catch (error) {
      setConnectionVerified(false);
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    }
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
        syncDetail: mapScanSyncDetail(item.updateState),
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
          await refreshSources(false, { page: nextPage });
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

  const ensureKnowledgeBaseNameUnique = async (value?: string) => {
    if (wizardMode === "edit") {
      return true;
    }

    const normalizedValue = normalizeKnowledgeBaseName(value);
    if (!normalizedValue) {
      return false;
    }

    const duplicateMessage = t("admin.dataSourceKnowledgeBaseNameDuplicated");
    const knownNameSet = new Set(
      getKnownKnowledgeBaseNames().map(normalizeKnowledgeBaseName).filter(Boolean),
    );
    if (knownNameSet.has(normalizedValue)) {
      form.setFields([{ name: "knowledgeBase", errors: [duplicateMessage] }]);
      return false;
    }

    try {
      const latestNames = await listKnowledgeBaseNames();
      setKnowledgeBaseNames(latestNames);
      if (latestNames.map(normalizeKnowledgeBaseName).includes(normalizedValue)) {
        form.setFields([{ name: "knowledgeBase", errors: [duplicateMessage] }]);
        return false;
      }
    } catch (error) {
      console.error("Failed to validate knowledge base name", error);
    }

    return true;
  };

  const handleNextStep = () => {
    if (wizardStep === 0) {
      if (!selectedType) {
        message.warning(t("admin.dataSourceSelectOneTypeFirst"));
        return;
      }
      if (selectedType === "feishu" && !oauthConnection?.connectionId) {
        if (isFeishuSetupReady && feishuAppSetup && oauthState !== "waiting") {
          void startFeishuOAuth({
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
      setWizardStep(1);
    }
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
      provider_options: {},
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

      if (localScanChatEnabled && datasetIdForLocalSource) {
        await applyDatasetChatDefault(datasetIdForLocalSource, sourceName, true);
        syncDefaultDatasetState([datasetIdForLocalSource], true);
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

  const handleSaveFeishuSource = async (
    values: SourceFormValues,
    saveMode: DataSourceSaveMode,
  ) => {
    const sourceName = `${values.knowledgeBase || getSourceTypeTitle("feishu", t)}`.trim();
    const targetRefs = normalizeFeishuTargetRefs(values.target);
    const currentFeishuSource =
      editingId && selectedType === "feishu"
        ? sources.find((item) => item.id === editingId && item.type === "feishu")
        : undefined;

    const authConnectionId =
      oauthConnection?.connectionId || (wizardMode === "edit" ? currentFeishuSource?.authConnectionId : "");

    if (targetRefs.length === 0) {
      message.warning(t("admin.dataSourceFeishuSpaceRequired"));
      return;
    }

    const client = createScanV2ApiClient();
    const selectedAgent = pickScanAgent(
      scanAgents,
      validatedAgentId || currentFeishuSource?.agentId,
    );
    const treeTargetTypeMap = collectFeishuTargetTypes(feishuTargetTreeData);
    const fallbackTargetTypes = normalizeFeishuTargetTypeRecord(currentFeishuSource?.targetTypes);
    const defaultTargetType =
      normalizeFeishuTargetType(currentFeishuSource?.targetType) ||
      normalizeFeishuTargetType(values.targetType) ||
      "wiki_space";
    const targets = targetRefs.map((targetRef) => ({
      targetRef,
      targetType:
        treeTargetTypeMap.get(targetRef) ||
        fallbackTargetTypes?.[targetRef] ||
        normalizeFeishuTargetType(undefined, targetRef) ||
        defaultTargetType,
    }));

    try {
      let sourceId = currentFeishuSource?.id || "";
      const schedulePolicy =
        values.syncMode === "scheduled"
          ? buildSchedulePolicy(values.scheduleWeekdays, values.scheduleTime)
          : undefined;
      const bindingRequest = {
        connector_type: "feishu",
        sync_mode: values.syncMode === "scheduled" ? "scheduled" : "manual",
        schedule_policy: schedulePolicy,
        auth_connection_id: authConnectionId,
        provider_options: {
          include_patterns: FEISHU_INCLUDE_PATTERNS,
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
        message.error("数据源创建成功但未返回 source id，无法继续配置飞书绑定。");
        return;
      }

      if (saveMode === "createAndSync") {
        message.info(t("admin.dataSourceDetailCloudSyncPreparing"));
        const latestSource = await client.getSource({ sourceId }).catch(() => null);
        const latestBinding = getFirstScanBinding(
          latestSource?.data.bindings as ScanV2Binding[] | undefined,
        );
        const triggerResponse = await client.triggerSourceSync({
          sourceId,
          triggerSourceSyncRequest: {
            request_id: createScanRequestId("feishu-sync"),
            binding_id: getScanBindingId(latestBinding) || currentFeishuSource?.bindingId,
            scope_type: "full",
            scope_ref: {},
          },
        });
        await waitForCloudSyncRun(client, sourceId, triggerResponse.data.run_ids?.[0]);
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
    try {
      const syncStrategyFields =
        form.getFieldValue("syncMode") === "scheduled"
          ? ["syncMode", "scheduleWeekdays", "scheduleTime"]
          : ["syncMode"];

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

      if (
        wizardMode !== "edit" &&
        !(await ensureKnowledgeBaseNameUnique(values.knowledgeBase))
      ) {
        return;
      }

      if (effectiveSourceType === "local") {
        await handleSaveLocalSource(values, saveMode);
        return;
      }
      await handleSaveFeishuSource(values, saveMode);
    } finally {
      setWizardSaving(false);
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
      width: 90,
      render: (type: SourceType) => <Tag>{getSourceTypeTitle(type, t)}</Tag>,
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
                scroll={{ x: 1280, y: "calc(100vh - 300px)" }}
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
              {providerAuthOptions.map((item) => {
                const isFeishuLocked = !isFeishuAuthValid && !isFeishuSetupReady;
                const authStatusText = isFeishuAuthValid
                  ? t("admin.dataSourceProviderAuthValid")
                  : isFeishuLocked
                    ? t("admin.dataSourceProviderCredentialMissing")
                    : t("admin.dataSourceProviderAuthPending");
                return (
                  <button
                    key={item.type}
                    type="button"
                    className={`data-source-provider-card ${isFeishuLocked ? "locked" : ""}`}
                    onClick={handleManageFeishuAuth}
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
                        {item.type === "feishu" ? (
                          <Tag
                            color={
                              isFeishuAuthValid
                                ? "success"
                                : isFeishuLocked
                                  ? "default"
                                  : "processing"
                            }
                          >
                            {authStatusText}
                          </Tag>
                        ) : null}
                      </span>
                      <span className="data-source-provider-desc">
                        {isFeishuAuthValid
                          ? t("admin.dataSourceFeishuAuthConnectedHint", {
                              account:
                                oauthConnection?.accountName ||
                                t("admin.dataSourceFeishuConnectedAccountFallback"),
                            })
                          : isFeishuLocked
                            ? t("admin.dataSourceFeishuLockHint")
                            : t("admin.dataSourceFeishuAuthReadyHint")}
                      </span>
                    </span>
                    <span className="data-source-provider-card-arrow" aria-hidden="true">
                      <ArrowRightOutlined />
                    </span>
                  </button>
                );
              })}
              {datasourceConnectors.map((connector) => (
                <button
                  key={connector.key}
                  type="button"
                  className="data-source-provider-card"
                  onClick={() => {
                    void handleManageDatasourceConnector(connector);
                  }}
                >
                  <span className={`data-source-provider-logo data-source-icon-${connector.key}`}>
                    {connector.logoUrl ? (
                      <img
                        alt=""
                        aria-hidden="true"
                        loading="lazy"
                        src={connector.logoUrl}
                        onError={(event) => {
                          event.currentTarget.style.display = "none";
                        }}
                      />
                    ) : (
                      connector.icon
                    )}
                  </span>
                  <span className="data-source-provider-card-copy">
                    <span className="data-source-provider-title-row">
                      <span className="data-source-provider-name">{connector.name}</span>
                    </span>
                    <span className="data-source-provider-desc">
                      Sciverse 面向科研场景的论文搜索与文献内容读取能力
                    </span>
                  </span>
                  <span className="data-source-provider-card-arrow" aria-hidden="true">
                    <ArrowRightOutlined />
                  </span>
                </button>
              ))}
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
          {sourceTypeOptions.map((item) => {
            const isFeishu = item.type === "feishu";
            const isFeishuLocked = isFeishu && !isFeishuAuthValid;
            const authStatusText = isFeishuAuthValid
              ? t("admin.dataSourceProviderAuthValid")
              : t("admin.dataSourceProviderCredentialMissing");
            return (
              <button
                key={item.type}
                type="button"
                className={`data-source-create-provider-card ${
                  isFeishuLocked ? "locked" : ""
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
                    {isFeishu ? (
                      <Tag color={isFeishuAuthValid ? "success" : "default"}>
                        {authStatusText}
                      </Tag>
                    ) : null}
                  </span>
                  <span className="data-source-provider-desc">
                    {isFeishuLocked
                      ? t("admin.dataSourceCreateFeishuAuthRequiredHint")
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
        title={t("admin.dataSourceFeishuCredentialModalTitle")}
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
            ? t("admin.dataSourceFeishuCredentialSaveAndSelect")
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
          <Alert
            showIcon
            type="info"
            message={t("admin.dataSourceFeishuCredentialHint")}
          />
        </Form>
      </Modal>

      <ExternalServiceConfigModal
        open={externalServiceModalOpen}
        service={activeExternalService}
        onClose={() => setExternalServiceModalOpen(false)}
        onChanged={() => {
          if (activeExternalService) {
            setActiveExternalService({
              ...activeExternalService,
              status: "configured",
            });
          }
        }}
      />

      <DataSourceWizardModal
        t={t}
        wizardMode={wizardMode}
        wizardOpen={wizardOpen}
        wizardStep={wizardStep}
        form={form}
        existingKnowledgeBaseNames={getKnownKnowledgeBaseNames()}
        selectedType={selectedType}
        isFeishuSetupReady={isFeishuSetupReady}
        connectionVerified={connectionVerified}
        syncMode={syncMode}
        saving={wizardSaving}
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
        onTestConnection={() => {
          void handleTestConnection();
        }}
        onInvalidateConnection={() => {
          setConnectionVerified(false);
          setValidatedAgentId(null);
        }}
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
