import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  Alert,
  Button,
  Card,
  Form,
  Input,
  Modal,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ApiOutlined,
  EditOutlined,
  EyeOutlined,
  FolderOpenOutlined,
  PlusOutlined,
  ReloadOutlined,
  WarningFilled,
} from "@ant-design/icons";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { useNavigate } from "react-router-dom";
import {
  Configuration as ScanConfiguration,
  DefaultApi as ScanDefaultApi,
  type Agent as ScanAgent,
  type Source as ScanSource,
  type CloudSourceBinding,
  type SourceDocumentItem as ScanSourceDocumentItem,
} from "@/api/generated/scan-client";
import {
  Configuration as CoreConfiguration,
  DatasetsApi as CoreDatasetsApi,
  DefaultApi as CoreDefaultApi,
  type Dataset as CoreDataset,
} from "@/api/generated/core-client";
import { BASE_URL, axiosInstance, getLocalizedErrorMessage } from "@/components/request";

import "./index.scss";
import DataSourceDetailDrawer from "./components/DataSourceDetailDrawer";
import DataSourceSummaryCards from "./components/DataSourceSummaryCards";
import DataSourceWizardModal from "./components/DataSourceWizardModal";
import {
  FEISHU_DATA_SOURCE_OAUTH_CHANNEL,
  clearFeishuDataSourceWizardDraft,
  consumeFeishuDataSourceOAuthResult,
  consumeFeishuDataSourceWizardDraft,
  finishFeishuDataSourceOAuth,
  openCenteredPopup,
  requestFeishuDataSourceAuthorizeUrl,
  saveFeishuDataSourceWizardDraft,
  type FeishuDataSourceConnection,
  type FeishuDataSourceOAuthMessage,
  type FeishuDataSourceWizardDraft,
} from "./feishuOAuth";
import {
  CLOUD_SYNC_POLL_INTERVAL_MS,
  CLOUD_SYNC_TIMEOUT_MS,
  DEFAULT_SCAN_TENANT_ID,
  FEISHU_APP_SETUP_STORAGE_KEY,
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
  formatBytes,
  formatDateTime,
  getConnectionMeta,
  getSourceTypeTitle,
  getStatusMeta,
  getSyncModeLabel,
  isCloudType,
  normalizeDataSourceConnectionState,
  normalizeDataSourceFileUpdateState,
  normalizeDataSourceParseStatus,
  normalizeDataSourceStatus,
} from "./shared";

const { Paragraph, Text } = Typography;
const DEFAULT_SCHEDULE_TIME = "02:00:00";
const SCHEDULE_TIME_PATTERN = /^([01]\d|2[0-3]):[0-5]\d:[0-5]\d$/;

function normalizeScheduleTime(scheduleTime?: string) {
  const value = `${scheduleTime || ""}`.trim();
  const minutePrecisionMatch = value.match(/^([01]\d|2[0-3]):[0-5]\d$/);
  if (minutePrecisionMatch) {
    return `${value}:00`;
  }
  return SCHEDULE_TIME_PATTERN.test(value) ? value : DEFAULT_SCHEDULE_TIME;
}

function normalizeKnowledgeBaseName(value?: string) {
  return `${value || ""}`.trim().toLowerCase();
}

function getDatasetDisplayName(dataset: CoreDataset) {
  return `${dataset.display_name || dataset.name || ""}`.trim();
}

const sourceTypeOptions: Array<{
  type: SourceType;
  icon: ReactNode;
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
  },
];

function createScanApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new ScanDefaultApi(
    new ScanConfiguration({
      basePath: baseUrl,
      baseOptions: {
        headers: { "Content-Type": "application/json" },
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

function createCoreApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new CoreDefaultApi(
    new CoreConfiguration({
      basePath: baseUrl,
      baseOptions: {
        headers: { "Content-Type": "application/json" },
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

function createCoreDatasetsApiClient() {
  const baseUrl = BASE_URL || window.location.origin;
  return new CoreDatasetsApi(
    new CoreConfiguration({
      basePath: baseUrl,
      baseOptions: {
        headers: { "Content-Type": "application/json" },
      },
    }),
    baseUrl,
    axiosInstance,
  );
}

function listScanAgents(client: ScanDefaultApi) {
  return client.apiScanAgentsGet({
    params: {
      tenant_id: DEFAULT_SCAN_TENANT_ID,
    },
  });
}

async function listKnowledgeBaseNames(client = createCoreDatasetsApiClient()) {
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

function sleep(ms: number) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

async function waitForCloudSyncRun(
  client: ScanDefaultApi,
  sourceId: string,
  runId?: string,
) {
  const deadline = Date.now() + CLOUD_SYNC_TIMEOUT_MS;

  while (Date.now() < deadline) {
    const runsResponse = await client.apiScanSourcesIdCloudSyncRunsGet({
      id: sourceId,
      limit: 20,
    });
    const matchedRun = runId
      ? runsResponse.data.items?.find((item) => item.run_id === runId)
      : runsResponse.data.items?.[0];
    const status = (matchedRun?.status || "").toUpperCase();

    if (status === "SUCCEEDED" || status === "PARTIAL_SUCCESS") {
      return matchedRun;
    }

    if (
      status.includes("FAILED") ||
      status.includes("ERROR") ||
      status.includes("CANCEL")
    ) {
      throw new Error(matchedRun?.error_message || "飞书云同步失败，请检查绑定配置后重试。");
    }

    await sleep(CLOUD_SYNC_POLL_INTERVAL_MS);
  }

  throw new Error("等待飞书目录同步超时，请稍后重试。");
}

function isFeishuScanSource(source: ScanSource) {
  const originPlatform = (source.default_origin_platform || "").toUpperCase();
  const originType = (source.default_origin_type || "").toUpperCase();
  const sourceType = (source.source_type || "").toUpperCase();
  const rootPath = (source.root_path || "").toLowerCase();

  return (
    originPlatform.includes("FEISHU") ||
    originType.includes("CLOUD_SYNC") ||
    sourceType.includes("CLOUD") ||
    rootPath.startsWith("cloud://source/")
  );
}

function parseFeishuScheduleExpr(expr?: string) {
  const parsed = parseReconcileSchedule(expr);
  if (!parsed) {
    return null;
  }
  return {
    syncMode: "scheduled" as const,
    scheduleCycle: parsed.scheduleCycle,
    scheduleTime: parsed.scheduleTime,
  };
}

function buildFeishuScheduleExpr(scheduleCycle?: string, scheduleTime?: string) {
  return buildReconcileSchedule(scheduleCycle, scheduleTime);
}

function buildFeishuManualScheduleExpr() {
  return "manual";
}

// Shared schedule expression helpers (used by both local reconcile_schedule and
// cloud schedule_expr). Format follows backend: `daily@HH:MM:SS`,
// `every2d@HH:MM:SS`, `every7d@HH:MM:SS`, or `manual`.
function parseReconcileSchedule(expr?: string): {
  scheduleCycle: "daily" | "twoDays" | "weekly";
  scheduleTime: string;
} | null {
  if (!expr) return null;
  const trimmed = expr.trim();
  if (!trimmed) return null;
  const lower = trimmed.toLowerCase();
  if (lower === "manual" || lower === "manual_only") return null;

  const dailyMatch = trimmed.match(/^daily@(([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)$/i);
  if (dailyMatch) {
    return { scheduleCycle: "daily", scheduleTime: normalizeScheduleTime(dailyMatch[1]) };
  }
  const everyMatch = trimmed.match(/^every(\d+)d@(([01]\d|2[0-3]):[0-5]\d(?::[0-5]\d)?)$/i);
  if (everyMatch) {
    const days = Number(everyMatch[1]);
    const time = normalizeScheduleTime(everyMatch[2]);
    if (days === 2) return { scheduleCycle: "twoDays", scheduleTime: time };
    if (days === 7) return { scheduleCycle: "weekly", scheduleTime: time };
    return { scheduleCycle: "daily", scheduleTime: time };
  }
  return null;
}

function buildReconcileSchedule(scheduleCycle?: string, scheduleTime?: string): string {
  const time = normalizeScheduleTime(scheduleTime);
  if (scheduleCycle === "twoDays") return `every2d@${time}`;
  if (scheduleCycle === "weekly") return `every7d@${time}`;
  return `daily@${time}`;
}

function getScheduleCycleLabel(scheduleCycle: string, t: TFunction): string {
  if (scheduleCycle === "twoDays") return t("admin.dataSourceCycleTwoDays");
  if (scheduleCycle === "weekly") return t("admin.dataSourceCycleWeekly");
  return t("admin.dataSourceCycleDaily");
}

function buildFeishuScheduleLabel(binding: CloudSourceBinding | null, t: TFunction) {
  const parsed = parseFeishuScheduleExpr(binding?.schedule_expr);
  if (!parsed) {
    return t("admin.dataSourceSyncModeManual");
  }

  return t("admin.dataSourceScheduleLabel", {
    cycle: getScheduleCycleLabel(parsed.scheduleCycle, t),
    time: parsed.scheduleTime,
  });
}

function buildFeishuNextSyncLabel(binding: CloudSourceBinding | null, t: TFunction) {
  const nextSyncAt = formatDateTime(binding?.next_sync_at);
  if (nextSyncAt !== "-") {
    return t("admin.dataSourceNextSyncPlanned", {
      time: nextSyncAt,
    });
  }

  const parsed = parseFeishuScheduleExpr(binding?.schedule_expr);
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

function mapScanDocumentToDetail(item: ScanSourceDocumentItem): DetailDocumentItem {
  const updateState = normalizeDataSourceFileUpdateState(
    item.update_type,
    item.has_update,
  );
  const parseState = [
    item.parse_state,
    item.core_task_state,
    item.scan_orchestration_status,
  ]
    .filter(Boolean)
    .join(" ");
  const lastSyncedAt = formatDateTime(item.last_synced_at);
  return {
    id: `${item.document_id}`,
    name: item.name,
    path: item.path,
    size: formatBytes(item.size_bytes),
    tags: item.tags || [],
    updateState,
    syncDetail: item.update_desc || mapScanSyncDetail(updateState),
    parseStatus: normalizeDataSourceParseStatus(parseState),
    sourceUpdatedAt: lastSyncedAt,
    updatedAt: lastSyncedAt,
  };
}

function getReconcileSeconds(scheduleCycle?: string) {
  if (scheduleCycle === "twoDays") {
    return 2 * 24 * 60 * 60;
  }
  if (scheduleCycle === "weekly") {
    return 7 * 24 * 60 * 60;
  }
  return 24 * 60 * 60;
}

function pickScanAgent(agents: ScanAgent[], preferredAgentId?: string) {
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

function loadFeishuAppSetup(): FeishuAppSetup | null {
  try {
    const raw = localStorage.getItem(FEISHU_APP_SETUP_STORAGE_KEY);
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

function persistFeishuAppSetup(setup: FeishuAppSetup) {
  localStorage.setItem(FEISHU_APP_SETUP_STORAGE_KEY, JSON.stringify(setup));
}

function clearFeishuAppSetup() {
  localStorage.removeItem(FEISHU_APP_SETUP_STORAGE_KEY);
}

function inferScheduleCycle(scheduleLabel: string) {
  const normalized = scheduleLabel.toLowerCase();
  if (
    scheduleLabel.includes("每 2 天") ||
    normalized.includes("every 2 day") ||
    normalized.includes("2 day")
  ) {
    return "twoDays";
  }
  if (scheduleLabel.includes("每周") || normalized.includes("week")) {
    return "weekly";
  }
  return "daily";
}

function getOAuthStateFromConnection(
  connection?: FeishuDataSourceConnection | null,
): OAuthState {
  if (!connection) {
    return "pending";
  }

  if (connection.status === "connected") {
    return "connected";
  }
  if (connection.status === "expired") {
    return "expired";
  }
  if (connection.status === "error") {
    return "error";
  }

  return "pending";
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
  const [wizardOpen, setWizardOpen] = useState(false);
  const [wizardStep, setWizardStep] = useState(0);
  const [wizardMode, setWizardMode] = useState<"create" | "edit">("create");
  const [selectedType, setSelectedType] = useState<SourceType | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [detailId, setDetailId] = useState<string | null>(null);
  const [oauthState, setOauthState] = useState<OAuthState>("pending");
  const [connectionVerified, setConnectionVerified] = useState(false);
  const [oauthConnection, setOauthConnection] =
    useState<FeishuDataSourceConnection | null>(null);
  const [feishuAppSetup, setFeishuAppSetup] = useState<FeishuAppSetup | null>(
    () => loadFeishuAppSetup(),
  );
  const [feishuSetupModalOpen, setFeishuSetupModalOpen] = useState(false);
  const [pendingSelectFeishu, setPendingSelectFeishu] = useState(false);
  const [feishuSetupForm] = Form.useForm<FeishuAppSetup>();
  const [manualOauthModalOpen, setManualOauthModalOpen] = useState(false);
  const [manualOauthCallbackValue, setManualOauthCallbackValue] = useState("");
  const [manualOauthSubmitting, setManualOauthSubmitting] = useState(false);
  const oauthAttemptRef = useRef<PendingOAuthAttempt | null>(null);
  const [scanAgents, setScanAgents] = useState<ScanAgent[]>([]);
  const [knowledgeBaseNames, setKnowledgeBaseNames] = useState<string[]>([]);
  const [scanLoading, setScanLoading] = useState(false);
  const [validatedAgentId, setValidatedAgentId] = useState<string | null>(null);
  const [wizardSaving, setWizardSaving] = useState(false);

  const syncMode = Form.useWatch("syncMode", form) || "scheduled";
  const feishuTargetType = (Form.useWatch("targetType", form) || "wiki_space") as FeishuTargetType;
  const isFeishuSetupReady = Boolean(
    feishuAppSetup?.appId.trim() && feishuAppSetup?.appSecret.trim(),
  );

  const detailSource = sources.find((item) => item.id === detailId);
  const totalDocuments = sources.reduce((sum, item) => sum + item.documentCount, 0);
  const activeCount = sources.filter((item) => item.status === "active").length;
  const warningCount = sources.filter((item) =>
    ["expired", "error"].includes(item.status),
  ).length;
  const scheduledCount = sources.filter((item) => item.syncMode === "scheduled").length;

  const buildScanScheduleLabel = (source: ScanSource) => {
    if (!source.watch_enabled) {
      return t("admin.dataSourceSyncModeManual");
    }

    const parsed = parseReconcileSchedule(source.reconcile_schedule);
    if (parsed) {
      const cycleLabel = getScheduleCycleLabel(parsed.scheduleCycle, t);
      return `${cycleLabel} ${parsed.scheduleTime} ${t("admin.dataSourceScheduleAutoSuffix")}`;
    }

    const reconcileSeconds = source.reconcile_seconds || 0;
    if (reconcileSeconds === 7 * 24 * 60 * 60) {
      return `${t("admin.dataSourceCycleWeekly")} (${reconcileSeconds}s)`;
    }
    if (reconcileSeconds === 2 * 24 * 60 * 60) {
      return `${t("admin.dataSourceCycleTwoDays")} (${reconcileSeconds}s)`;
    }
    if (reconcileSeconds === 24 * 60 * 60) {
      return `${t("admin.dataSourceCycleDaily")} (${reconcileSeconds}s)`;
    }
    return `${t("admin.dataSourceSyncModeScheduled")} (${reconcileSeconds}s)`;
  };

  const buildScanNextSyncLabel = (source: ScanSource) => {
    if (!source.watch_enabled) {
      return t("admin.dataSourceNextSyncManual");
    }
    const parsed = parseReconcileSchedule(source.reconcile_schedule);
    if (parsed) {
      return t("admin.dataSourceNextSyncPlanned", { time: parsed.scheduleTime });
    }
    const reconcileSeconds = source.reconcile_seconds || 0;
    const hourEstimate = Math.max(1, Math.round(reconcileSeconds / 3600));
    return t("admin.dataSourceNextSyncPlanned", {
      time: `${hourEstimate}h`,
    });
  };

  const mapScanSourceToDataSource = (
    source: ScanSource,
    fallback?: DataSourceItem,
    binding: CloudSourceBinding | null = source.cloud_binding || null,
  ): DataSourceItem => {
    const documentsPayload = source.documents;
    const summary = documentsPayload?.summary;
    const documentsSource = documentsPayload?.source;
    const isFeishuSource = isFeishuScanSource(source);
    const sourceStatus = normalizeDataSourceStatus(
      binding?.status || source.status,
      isFeishuSource ? true : source.watch_enabled,
    );
    const connectionState = normalizeDataSourceConnectionState(binding?.status || source.status);
    const currentTime = formatDateTime(
      documentsSource?.last_synced_at || binding?.updated_at || source.updated_at,
    );
    const detailDocuments = documentsPayload?.items
      ? documentsPayload.items.map(mapScanDocumentToDetail)
      : fallback?.detailDocuments || [];
    const fileCandidates = documentsPayload?.items
      ? detailDocuments.map((item) => ({
        id: item.id,
        name: item.name,
        path: item.path,
        size: item.size,
        type: item.path.split(".").pop() || "",
        updateState: item.updateState,
      }))
      : fallback?.fileCandidates || [];
    const documentCount = summary?.total_document_count ?? fallback?.documentCount ?? 0;
    const addCount = summary?.new_count ?? fallback?.addCount ?? 0;
    const deleteCount = summary?.deleted_count ?? fallback?.deleteCount ?? 0;
    const changeCount = summary?.modified_count ?? fallback?.changeCount ?? 0;
    const storageUsed =
      typeof summary?.storage_bytes === "number"
        ? formatBytes(summary.storage_bytes)
        : fallback?.storageUsed || "0 B";

    if (isFeishuSource) {
      return {
        id: source.id,
        name: source.name,
        type: "feishu",
        knowledgeBase: source.name,
        description: t("admin.dataSourceTypeFeishuDesc"),
        target: binding?.target_ref || fallback?.target || source.root_path,
        syncMode: parseFeishuScheduleExpr(binding?.schedule_expr) ? "scheduled" : "manual",
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
            id: `scan-log-${source.id}-${binding?.updated_at || source.updated_at}`,
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
              binding?.last_error ||
              (parseFeishuScheduleExpr(binding?.schedule_expr)
                ? t("admin.dataSourceSyncModeScheduledDesc")
                : t("admin.dataSourceSyncModeManualDesc")),
          },
        ],
        warning: binding?.last_error || t("admin.dataSourceReadonlyPermissionHint"),
        oauthConnection:
          fallback?.oauthConnection && fallback.oauthConnection.connectionId === binding?.auth_connection_id
            ? fallback.oauthConnection
            : null,
        agentId: source.agent_id,
        tenantId: source.tenant_id,
        scanManaged: true,
        storageUsed:
          typeof summary?.storage_bytes === "number"
            ? formatBytes(summary.storage_bytes)
            : fallback?.storageUsed || "0 B",
        detailDocuments,
        rootPath: source.root_path,
        targetRef: binding?.target_ref || fallback?.targetRef,
        targetType: (binding?.target_type as FeishuTargetType | undefined) || fallback?.targetType,
        authConnectionId: binding?.auth_connection_id || fallback?.authConnectionId,
        datasetId: source.dataset_id,
      };
    }

    return {
      id: source.id,
      name: source.name,
      type: "local",
      knowledgeBase: source.name,
      description: t("admin.dataSourceTypeLocalDesc"),
      target: source.root_path,
      syncMode: source.watch_enabled ? "scheduled" : "manual",
      scheduleLabel: buildScanScheduleLabel(source),
      status: sourceStatus,
      connectionState,
      lastSync: currentTime,
      nextSync: buildScanNextSyncLabel(source),
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
          id: `scan-log-${source.id}-${source.updated_at}`,
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
          description: source.watch_enabled
            ? t("admin.dataSourceSyncModeScheduledDesc")
            : t("admin.dataSourceSyncModeManualDesc"),
        },
      ],
      warning: t("admin.dataSourceReadonlyPermissionHint"),
      oauthConnection: null,
      agentId: source.agent_id,
      tenantId: source.tenant_id,
      scanManaged: true,
      storageUsed,
      detailDocuments,
      rootPath: source.root_path,
      datasetId: source.dataset_id,
    };
  };

  const refreshSources = async (showSuccessMessage = false) => {
    const client = createScanApiClient();
    setScanLoading(true);
    try {
      const sourcesResponse = await client.apiScanSourcesGet();

      const sourceList = sourcesResponse.data.items || [];
      const previousSourceMap = new Map(
        sources.map((item) => [item.id, item]),
      );
      const nextSources = sourceList.map((source) =>
        mapScanSourceToDataSource(
          source,
          previousSourceMap.get(source.id),
        ),
      );

      setSources(nextSources);

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
      setScanLoading(false);
    }
  };

  const refreshKnowledgeBaseNames = async () => {
    try {
      setKnowledgeBaseNames(await listKnowledgeBaseNames());
    } catch (error) {
      console.error("Failed to refresh knowledge base names", error);
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
    message.error(payload.message || t("admin.dataSourceOauthFailedRetry"));
  };

  useEffect(() => {
    const draft = consumeFeishuDataSourceWizardDraft();
    if (draft) {
      const normalizedWizardStep = Math.min(Math.max(draft.wizardStep, 0), 1);
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
  }, []);

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
    setOauthState("pending");
    setConnectionVerified(false);
    setOauthConnection(null);
    setValidatedAgentId(null);
    setManualOauthModalOpen(false);
    setManualOauthCallbackValue("");
    setManualOauthSubmitting(false);
  };

  const openCreateWizard = () => {
    resetWizard();
    form.setFieldsValue({
      syncMode: "scheduled",
      scheduleCycle: "daily",
      scheduleTime: DEFAULT_SCHEDULE_TIME,
      conflictPolicy: "versioned",
      targetType: "wiki_space",
    });
    setWizardOpen(true);
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
      scheduleCycle:
        inferScheduleCycle(record.scheduleLabel),
      scheduleTime: normalizeScheduleTime(
        record.scheduleLabel.match(/\d{2}:\d{2}(?::\d{2})?/)?.[0],
      ),
      conflictPolicy: record.conflictPolicy,
      path: record.type === "local" ? record.target : undefined,
      target: record.type === "feishu" ? record.targetRef || record.target : undefined,
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
    form.setFieldsValue({
      syncMode: "scheduled",
      scheduleCycle: "daily",
      scheduleTime: DEFAULT_SCHEDULE_TIME,
      conflictPolicy: "versioned",
      path: "",
      target: "",
      targetType: type === "feishu" ? "wiki_space" : undefined,
    });
  };

  const openFeishuSetupModal = (autoSelect = false) => {
    setPendingSelectFeishu(autoSelect);
    feishuSetupForm.setFieldsValue({
      appId: feishuAppSetup?.appId || "",
      appSecret: feishuAppSetup?.appSecret || "",
    });
    setFeishuSetupModalOpen(true);
  };

  const handleSaveFeishuSetup = async () => {
    const values = await feishuSetupForm.validateFields();
    const nextSetup: FeishuAppSetup = {
      appId: values.appId.trim(),
      appSecret: values.appSecret.trim(),
    };

    persistFeishuAppSetup(nextSetup);
    setFeishuAppSetup(nextSetup);
    setFeishuSetupModalOpen(false);
    message.success(t("admin.dataSourceFeishuCredentialSaved"));

    if (pendingSelectFeishu) {
      applySourceType("feishu");
      setPendingSelectFeishu(false);
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
      openFeishuSetupModal(true);
      return;
    }
    applySourceType(type);
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
      const normalizedPath = `${path}`.trim();

      if (!normalizedPath) {
        message.warning(t("admin.dataSourceAccessPathRequired"));
        return;
      }

      let currentAgents = scanAgents;
      if (currentAgents.length === 0) {
        const agentsResponse = await listScanAgents(createScanApiClient());
        currentAgents = agentsResponse.data.items || [];
        setScanAgents(currentAgents);
      }

      const preferredAgentId =
        validatedAgentId ||
        (editingId
          ? sources.find((item) => item.id === editingId)?.agentId
          : undefined);
      const selectedAgent = pickScanAgent(currentAgents, preferredAgentId);
      if (!selectedAgent?.agent_id) {
        message.error("未发现可用扫描 Agent，请先启动并注册扫描 Agent。");
        return;
      }

      const validateResponse = await createScanApiClient().apiScanAgentsFsValidatePost({
        agentPathRequest: {
          agent_id: selectedAgent.agent_id,
          path: normalizedPath,
        },
      });
      const validation = validateResponse.data;
      const passed =
        Boolean(validation.allowed) &&
        Boolean(validation.exists) &&
        Boolean(validation.readable) &&
        Boolean(validation.is_dir);

      setConnectionVerified(passed);
      if (passed) {
        setValidatedAgentId(selectedAgent.agent_id);
        message.success(t("admin.dataSourceConnectionTestSuccess"));
        return;
      }

      message.error(validation.reason || "路径校验未通过，请检查目录是否存在且具备只读权限。");
    } catch (error) {
      setConnectionVerified(false);
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
    }
  };

  const handleConnectAccount = async () => {
    const previousState = oauthState;
    const previousVerified = connectionVerified;
    const previousConnection = oauthConnection;

    try {
      if (!isFeishuSetupReady || !feishuAppSetup) {
        message.warning(t("admin.dataSourceFeishuCredentialRequired"));
        return;
      }

      await form.validateFields(["target"]);
      let currentAgents = scanAgents;
      if (currentAgents.length === 0) {
        const agentsResponse = await listScanAgents(createScanApiClient());
        currentAgents = agentsResponse.data.items || [];
        setScanAgents(currentAgents);
      }

      const selectedAgent = pickScanAgent(currentAgents, validatedAgentId || undefined);
      if (!selectedAgent?.agent_id || !selectedAgent.tenant_id) {
        message.error("未发现可用扫描 Agent，请先启动并注册扫描 Agent。");
        return;
      }

      setOauthState("waiting");
      setValidatedAgentId(selectedAgent.agent_id);
      const authorizeUrl = await requestFeishuDataSourceAuthorizeUrl({
        tenantId: selectedAgent.tenant_id,
        appId: feishuAppSetup.appId,
        appSecret: feishuAppSetup.appSecret,
        scopes: FEISHU_DEFAULT_SCOPES,
        returnUrl: window.location.href,
      });

      const draft: FeishuDataSourceWizardDraft = {
        wizardOpen: true,
        wizardStep,
        wizardMode,
        selectedType,
        editingId,
        validatedAgentId: selectedAgent.agent_id,
        oauthState: "waiting",
        connectionVerified: previousVerified,
        oauthConnection: previousConnection,
        formValues: form.getFieldsValue(true),
      };

      saveFeishuDataSourceWizardDraft(draft);

      const popup = openCenteredPopup(authorizeUrl, t("admin.dataSourceFeishuAuthWindowTitle"));

      oauthAttemptRef.current = {
        timerId: null,
        previousState,
        previousVerified,
        previousConnection,
        resolved: false,
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
        return;
      }

      window.location.assign(authorizeUrl);
    } catch (error: any) {
      setOauthState(previousState);
      setConnectionVerified(previousVerified);
      setOauthConnection(previousConnection);
      message.error(error?.message || t("admin.dataSourceAuthorizeUrlFailed"));
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
          targetType: record.targetType,
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
        },
      },
    });
  };

  const validateConnectionBeforeSave = () => {
    if (!selectedType) {
      message.warning(t("admin.dataSourceSelectTypeFirst"));
      return false;
    }

    if (isCloudType(selectedType) && !isFeishuSetupReady) {
      message.warning(t("admin.dataSourceFeishuCredentialFirst"));
      return false;
    }

    if (!connectionVerified) {
      message.warning(t("admin.dataSourceTestConnectionFirst"));
      return false;
    }

    return true;
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
      setWizardStep(1);
    }
  };

  const handleSaveLocalSource = async (values: SourceFormValues) => {
    const rootPath = `${values.path || ""}`.trim();
    const sourceName = `${values.knowledgeBase || getSourceTypeTitle("local", t)}`.trim();
    const isScheduled = (values.syncMode || "scheduled") === "scheduled";
    const reconcileSeconds = getReconcileSeconds(values.scheduleCycle);
    const reconcileSchedule = isScheduled
      ? buildReconcileSchedule(values.scheduleCycle, values.scheduleTime)
      : "manual";
    const currentLocalSource =
      editingId && selectedType === "local"
        ? sources.find((item) => item.id === editingId && item.type === "local")
        : undefined;

    if (!rootPath) {
      message.warning(t("admin.dataSourceAccessPathRequired"));
      return;
    }

    const client = createScanApiClient();
    let currentAgents = scanAgents;
    if (currentAgents.length === 0) {
      const agentsResponse = await listScanAgents(client);
      currentAgents = agentsResponse.data.items || [];
      setScanAgents(currentAgents);
    }

    const selectedAgent = pickScanAgent(
      currentAgents,
      validatedAgentId || currentLocalSource?.agentId,
    );
    if (!selectedAgent) {
      message.error("未发现可用扫描 Agent，请先启动并注册扫描 Agent。");
      return;
    }

    try {
      if (currentLocalSource?.scanManaged) {
        await client.apiScanSourcesIdPut({
          id: currentLocalSource.id,
          updateSourceRequest: {
            name: sourceName,
            root_path: rootPath,
            reconcile_seconds: reconcileSeconds,
            reconcile_schedule: reconcileSchedule,
            idle_window_seconds: 300,
          },
        });

        if (isScheduled) {
          await client.apiScanSourcesIdWatchEnablePost({
            id: currentLocalSource.id,
            enableWatchRequest: {
              reconcile_seconds: reconcileSeconds,
              reconcile_schedule: reconcileSchedule,
            },
          });
        } else {
          await client.apiScanSourcesIdWatchDisablePost({
            id: currentLocalSource.id,
          });
        }
      } else {
        const algosResponse = await createCoreApiClient().apiCoreDatasetAlgosGet();
        const algos = algosResponse.data.algos || [];
        const selectedAlgo = algos[0];
        if (!selectedAlgo?.algo_id) {
          message.error("未获取到可用知识库算法，请先检查 Core 服务算法配置。");
          return;
        }

        const kbResponse = await client.apiScanKnowledgeBasesPost({
          createKnowledgeBaseRequest: {
            name: sourceName,
            algo: {
              algo_id: selectedAlgo.algo_id,
              display_name: selectedAlgo.display_name,
              description: selectedAlgo.description,
            },
          },
        });

        const createSourceResponse = await client.apiScanSourcesPost({
          createSourceRequest: {
            tenant_id: selectedAgent.tenant_id,
            agent_id: selectedAgent.agent_id,
            dataset_id: kbResponse.data.dataset_id,
            name: sourceName,
            root_path: rootPath,
            watch_enabled: isScheduled,
            reconcile_seconds: reconcileSeconds,
            reconcile_schedule: reconcileSchedule,
            idle_window_seconds: 300,
          },
        });

        const createdSourceId = createSourceResponse.data.id;
        if (!createdSourceId) {
          message.error("数据源创建成功但未返回 source id，无法配置监听状态。");
          return;
        }

        if (isScheduled) {
          await client.apiScanSourcesIdWatchEnablePost({
            id: createdSourceId,
            enableWatchRequest: {
              reconcile_seconds: reconcileSeconds,
              reconcile_schedule: reconcileSchedule,
            },
          });
        } else {
          await client.apiScanSourcesIdWatchDisablePost({
            id: createdSourceId,
          });
        }
      }

      setValidatedAgentId(selectedAgent.agent_id);
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

  const validateFeishuTargetBeforeSave = async (
    client: ScanDefaultApi,
    authConnectionId: string,
    targetType: FeishuTargetType,
    targetRef: string,
  ) => {
    try {
      const response = await client.apiScanCloudTargetValidatePost({
        validateCloudTargetRequest: {
          provider: "feishu",
          auth_connection_id: authConnectionId,
          target_type: targetType,
          target_ref: targetRef,
        },
      });

      if (response.data.valid) {
        return true;
      }

      const validation = response.data as typeof response.data & {
        reason?: string;
        message?: string;
        detail?: string;
      };
      const errorMessage =
        validation.reason ||
        validation.message ||
        validation.detail ||
        t("admin.dataSourceFeishuTargetValidateFailed");
      form.setFields([{ name: "target", errors: [errorMessage] }]);
      message.error(errorMessage);
      return false;
    } catch (error) {
      const errorMessage =
        getLocalizedErrorMessage(error, t("admin.dataSourceFeishuTargetValidateFailed")) ||
        t("admin.dataSourceFeishuTargetValidateFailed");
      form.setFields([{ name: "target", errors: [errorMessage] }]);
      message.error(errorMessage);
      return false;
    }
  };

  const handleSaveFeishuSource = async (values: SourceFormValues) => {
    const sourceName = `${values.knowledgeBase || getSourceTypeTitle("feishu", t)}`.trim();
    const targetRef = `${values.target || ""}`.trim();
    const targetType = (values.targetType || "wiki_space") as FeishuTargetType;
    const currentFeishuSource =
      editingId && selectedType === "feishu"
        ? sources.find((item) => item.id === editingId && item.type === "feishu")
        : undefined;

    const authConnectionId =
      oauthConnection?.connectionId || (wizardMode === "edit" ? currentFeishuSource?.authConnectionId : "");

    if (!authConnectionId) {
      message.warning(t("admin.dataSourceTestConnectionFirst"));
      return;
    }

    if (!targetRef) {
      message.warning(t("admin.dataSourceFeishuSpaceRequired"));
      return;
    }

    const client = createScanApiClient();
    let currentAgents = scanAgents;
    if (currentAgents.length === 0) {
      const agentsResponse = await listScanAgents(client);
      currentAgents = agentsResponse.data.items || [];
      setScanAgents(currentAgents);
    }

    const selectedAgent = pickScanAgent(
      currentAgents,
      validatedAgentId || currentFeishuSource?.agentId,
    );
    if (!selectedAgent?.agent_id || !selectedAgent.tenant_id) {
      message.error("未发现可用扫描 Agent，请先启动并注册扫描 Agent。");
      return;
    }

    try {
      const targetValid = await validateFeishuTargetBeforeSave(
        client,
        authConnectionId,
        targetType,
        targetRef,
      );
      if (!targetValid) {
        return;
      }

      let sourceId = currentFeishuSource?.id || "";
      if (currentFeishuSource?.scanManaged) {
        await client.apiScanSourcesIdPut({
          id: currentFeishuSource.id,
          updateSourceRequest: {
            name: sourceName,
            idle_window_seconds: 600,
            default_origin_platform: "FEISHU",
            default_origin_type: "CLOUD_SYNC",
            default_trigger_policy: "IMMEDIATE",
          },
        });
      } else {
        const algosResponse = await createCoreApiClient().apiCoreDatasetAlgosGet();
        const algos = algosResponse.data.algos || [];
        const selectedAlgo = algos[0];
        if (!selectedAlgo?.algo_id) {
          message.error("未获取到可用知识库算法，请先检查 Core 服务算法配置。");
          return;
        }

        const kbResponse = await client.apiScanKnowledgeBasesPost({
          createKnowledgeBaseRequest: {
            name: sourceName,
            algo: {
              algo_id: selectedAlgo.algo_id,
              display_name: selectedAlgo.display_name,
              description: selectedAlgo.description,
            },
          },
        });

        const createSourceResponse = await client.apiScanSourcesPost({
          createSourceRequest: {
            tenant_id: selectedAgent.tenant_id,
            agent_id: selectedAgent.agent_id,
            dataset_id: kbResponse.data.dataset_id,
            name: sourceName,
            watch_enabled: false,
            idle_window_seconds: 600,
            default_origin_type: "CLOUD_SYNC",
            default_origin_platform: "FEISHU",
            default_trigger_policy: "IMMEDIATE",
          },
        });

        sourceId = createSourceResponse.data.id || "";
      }

      if (!sourceId) {
        message.error("数据源创建成功但未返回 source id，无法继续配置飞书绑定。");
        return;
      }

      await client.apiScanSourcesIdCloudBindingPost({
        id: sourceId,
        upsertCloudSourceBindingRequest: {
          provider: "feishu",
          enabled: true,
          auth_connection_id: authConnectionId,
          target_type: targetType,
          target_ref: targetRef,
          reconcile_after_sync: true,
          reconcile_delay_minutes: 10,
          include_patterns: FEISHU_INCLUDE_PATTERNS,
          exclude_patterns: FEISHU_EXCLUDE_PATTERNS,
          max_object_size_bytes: FEISHU_MAX_OBJECT_SIZE_BYTES,
          ...(values.syncMode === "scheduled"
            ? {
                schedule_expr: buildFeishuScheduleExpr(
                  values.scheduleCycle,
                  values.scheduleTime,
                ),
                schedule_tz: "Asia/Shanghai",
              }
            : {
                schedule_expr: buildFeishuManualScheduleExpr(),
                schedule_tz: "Asia/Shanghai",
              }),
        },
      });

      message.info("正在从飞书拉取最新目录，请稍候。");
      const triggerResponse = await client.apiScanSourcesIdCloudSyncTriggerPost({
        id: sourceId,
        triggerCloudSyncRequest: {
          trigger_type: "manual",
        },
      });
      await waitForCloudSyncRun(client, sourceId, triggerResponse.data.run_id);

      setValidatedAgentId(selectedAgent.agent_id);
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

  const handleSave = async () => {
    if (!selectedType || wizardSaving) {
      return;
    }

    setWizardSaving(true);
    try {
      const syncStrategyFields =
        form.getFieldValue("syncMode") === "scheduled"
          ? ["syncMode", "scheduleCycle", "scheduleTime"]
          : ["syncMode"];

      if (wizardMode === "edit") {
        await form.validateFields(syncStrategyFields);
      } else {
        await form.validateFields();
      }

      const values = form.getFieldsValue(true);

      if (
        wizardMode !== "edit" &&
        !(await ensureKnowledgeBaseNameUnique(values.knowledgeBase))
      ) {
        return;
      }

      if (wizardMode !== "edit" && !validateConnectionBeforeSave()) {
        return;
      }

      if (selectedType === "local") {
        await handleSaveLocalSource(values);
        return;
      }
      await handleSaveFeishuSource(values);
    } finally {
      setWizardSaving(false);
    }
  };

  const columns: ColumnsType<DataSourceItem> = [
    {
      title: t("admin.dataSourceTableSource"),
      dataIndex: "name",
      key: "name",
      width: 280,
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
      width: 120,
      render: (type: SourceType) => <Tag>{getSourceTypeTitle(type, t)}</Tag>,
    },
    {
      title: t("admin.dataSourceTableKnowledgeBase"),
      dataIndex: "knowledgeBase",
      key: "knowledgeBase",
      width: 140,
    },
    {
      title: t("admin.dataSourceTableSyncStrategy"),
      key: "syncMode",
      width: 260,
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
      width: 140,
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
      width: 220,
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
      width: 180,
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
      render: (_value, record) => (
        <Space wrap>
          <Button type="link" icon={<EyeOutlined />} onClick={() => openDetailPage(record)}>
            {t("admin.dataSourceActionDetail")}
          </Button>
          <Button type="link" icon={<EditOutlined />} onClick={() => openEditWizard(record)}>
            {t("admin.dataSourceActionConfig")}
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
        <Button
          type="primary"
          icon={<PlusOutlined />}
          className="admin-page-primary-button"
          onClick={openCreateWizard}
        >
          {t("admin.dataSourceCreate")}
        </Button>
      </div>

      <DataSourceSummaryCards
        t={t}
        total={sources.length}
        activeCount={activeCount}
        scheduledCount={scheduledCount}
        totalDocuments={totalDocuments}
        warningCount={warningCount}
      />

      <Card
        className="data-source-list-card"
        title={t("admin.dataSourceListTitle")}
        extra={
          <Space size="middle">
            <Button
              icon={<ReloadOutlined />}
              loading={scanLoading}
              onClick={() => {
                void refreshSources(true);
              }}
            >
              {t("admin.dataSourceRefresh")}
            </Button>
          </Space>
        }
      >
        <Table<DataSourceItem>
          rowKey="id"
          columns={columns}
          dataSource={sources}
          loading={scanLoading}
          pagination={{ pageSize: 6, showSizeChanger: false }}
          className="admin-page-table data-source-table"
          scroll={{ x: 1480, y: "clamp(22vh, calc(100vh - 560px), 52vh)" }}
        />
      </Card>

      <DataSourceDetailDrawer
        t={t}
        open={Boolean(detailSource)}
        source={detailSource}
        onClose={() => setDetailId(null)}
        onEdit={openEditWizard}
      />

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
          setFeishuSetupModalOpen(false);
          setPendingSelectFeishu(false);
        }}
        onOk={handleSaveFeishuSetup}
        okText={
          pendingSelectFeishu
            ? t("admin.dataSourceFeishuCredentialSaveAndSelect")
            : t("common.save")
        }
        cancelText={t("common.cancel")}
      >
        <Form form={feishuSetupForm} layout="vertical">
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

      <DataSourceWizardModal
        t={t}
        wizardMode={wizardMode}
        wizardOpen={wizardOpen}
        wizardStep={wizardStep}
        form={form}
        existingKnowledgeBaseNames={getKnownKnowledgeBaseNames()}
        selectedType={selectedType}
        isFeishuSetupReady={isFeishuSetupReady}
        oauthState={oauthState}
        oauthConnection={oauthConnection}
        connectionVerified={connectionVerified}
        syncMode={syncMode}
        feishuTargetType={feishuTargetType}
        saving={wizardSaving}
        onClose={handleCloseWizard}
        onPrev={() => setWizardStep((step) => step - 1)}
        onNext={handleNextStep}
        onSave={() => {
          void handleSave();
        }}
        onSelectType={handleSelectType}
        onResetFeishuSetup={handleResetFeishuSetup}
        onConnectAccount={() => {
          void handleConnectAccount();
        }}
        onOpenManualOauthModal={() => setManualOauthModalOpen(true)}
        onTestConnection={() => {
          void handleTestConnection();
        }}
        onInvalidateConnection={() => {
          setConnectionVerified(false);
          setValidatedAgentId(null);
        }}
      />
    </div>
  );
}
