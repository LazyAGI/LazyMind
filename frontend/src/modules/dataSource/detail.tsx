import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Tag,
  Typography,
  message,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import type { DataNode } from "antd/es/tree";
import {
  BookOutlined,
  CheckCircleFilled,
  ClockCircleFilled,
  DeleteOutlined,
  ExclamationCircleFilled,
  SyncOutlined,
} from "@ant-design/icons";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import { getLocalizedErrorMessage } from "@/components/request";

import "./detail.scss";
import DataSourceDetailView from "@/modules/dataSource/common/components/DataSourceDetailView";
import DataSourceSyncPickerModal from "@/modules/dataSource/common/components/DataSourceSyncPickerModal";
import {
  CLOUD_SYNC_POLL_INTERVAL_MS,
  CLOUD_SYNC_TIMEOUT_MS,
  type DataSourceDetailState,
  type DataSourceSummary,
  type DocumentStatusRow,
  type SourceStateValue,
  type SyncStateValue,
  buildDocumentStatusDetail,
  formatBytes,
  formatDateTime,
  getFileUpdateMeta,
  getSyncStateMeta,
  normalizeDataSourceFileUpdateState,
  normalizeDataSourceParseStatus,
  normalizeDataSourceStatus,
  normalizePendingAction,
  resolveSourceState,
  resolveSyncState,
} from "./shared";
import {
  createScanRequestId,
  createScanV2ApiClient,
  getDocumentDisplayName,
  getDocumentLastUpdatedAt,
  getDocumentPath,
  getFirstScanBinding,
  getScanBindingId,
  getScanBindingTarget,
  getScanBindingTreeKey,
  getScanSourceConfigVersion,
  getScanSourceId,
  getScanSourceName,
  getScanSourceUpdatedAt,
  inferSourceKind,
  type ScanV2Binding,
  type ScanV2Client,
  type ScanV2Document,
  type ScanV2Source,
  type ScanV2Summary,
  type ScanV2TreeNode,
} from "./scanV2Api";

const { Text } = Typography;

const SCAN_TREE_PAGE_SIZE = 50;

type SyncTreeDataNode = DataNode & {
  treeKey?: string;
  objectKey?: string;
  nodeRef?: string;
  childrenLoaded?: boolean;
};

type SyncGenerateScope = {
  key?: string;
  object_key?: string;
  node_ref?: string;
  path?: string;
  is_document?: boolean;
  is_container?: boolean;
};

const fallbackSources: Record<
  string,
  DataSourceSummary & { storageUsed: string }
> = {
  "source-feishu-rd": {
    id: "source-feishu-rd",
    name: "飞书研发知识库",
    target: "Wiki://space_rd_platform",
    documentCount: 1284,
    status: "active",
    lastSync: "2026-04-13 10:24",
    addCount: 18,
    deleteCount: 2,
    changeCount: 41,
    storageUsed: "452.8 MB",
  },
  "source-local-ops": {
    id: "source-local-ops",
    name: "运维共享盘",
    target: "/mnt/team-share/ops-docs",
    documentCount: 764,
    status: "active",
    lastSync: "2026-04-13 08:12",
    addCount: 5,
    deleteCount: 0,
    changeCount: 9,
    storageUsed: "218.6 MB",
  },
};

const documentStatusMap: Record<
  string,
  {
    storageUsed: string;
    documents: DocumentStatusRow[];
  }
> = {
  "source-feishu-rd": {
    storageUsed: "452.8 MB",
    documents: [
      {
        id: "fs-1",
        name: "飞书接入开发文档.pdf",
        path: "/接入文档/飞书接入开发文档.pdf",
        size: "1.4 MB",
        tags: ["接入", "飞书"],
        updateState: "changed",
        syncDetail: "内容变更，已完成增量重解析",
        parseStatus: "parsed",
        sourceUpdatedAt: "2026-04-13 10:21",
        updatedAt: "2026-04-13 10:24",
      },
      {
        id: "fs-2",
        name: "OAuth 接口定义说明.docx",
        path: "/接入文档/OAuth 接口定义说明.docx",
        size: "856 KB",
        tags: ["OAuth", "接口"],
        updateState: "new",
        syncDetail: "新文档入库，已生成向量索引",
        parseStatus: "parsed",
        sourceUpdatedAt: "2026-04-13 09:52",
        updatedAt: "2026-04-13 09:58",
      },
      {
        id: "fs-3",
        name: "知识库权限申请流程.md",
        path: "/权限中心/知识库权限申请流程.md",
        size: "122 KB",
        tags: ["权限"],
        updateState: "changed",
        syncDetail: "权限范围更新，等待重建索引",
        parseStatus: "reindexing",
        sourceUpdatedAt: "2026-04-13 09:40",
        updatedAt: "2026-04-13 09:41",
      },
      {
        id: "fs-4",
        name: "旧版连接说明.docx",
        path: "/历史归档/旧版连接说明.docx",
        size: "730 KB",
        tags: ["归档"],
        updateState: "unchanged",
        syncDetail: "检测到重复文档，按多版本策略保留历史版本",
        parseStatus: "duplicate",
        sourceUpdatedAt: "2026-04-11 23:55",
        updatedAt: "2026-04-12 02:01",
      },
    ],
  },
  "source-local-ops": {
    storageUsed: "218.6 MB",
    documents: [
      {
        id: "ops-1",
        name: "巡检标准作业手册.pdf",
        path: "/mnt/team-share/ops-docs/巡检标准作业手册.pdf",
        size: "2.1 MB",
        tags: ["巡检", "SOP"],
        updateState: "changed",
        syncDetail: "内容变更，已完成增量重解析",
        parseStatus: "parsed",
        sourceUpdatedAt: "2026-04-13 08:09",
        updatedAt: "2026-04-13 08:12",
      },
      {
        id: "ops-2",
        name: "应急值班排班.xlsx",
        path: "/mnt/team-share/ops-docs/应急值班排班.xlsx",
        size: "414 KB",
        tags: ["排班"],
        updateState: "new",
        syncDetail: "新文档入库，已完成索引生成",
        parseStatus: "parsed",
        sourceUpdatedAt: "2026-04-13 08:00",
        updatedAt: "2026-04-13 08:05",
      },
      {
        id: "ops-3",
        name: "故障复盘记录.md",
        path: "/mnt/team-share/ops-docs/故障复盘记录.md",
        size: "96 KB",
        tags: ["复盘"],
        updateState: "changed",
        syncDetail: "检测到内容变更，正在重新切分 chunk",
        parseStatus: "reindexing",
        sourceUpdatedAt: "2026-04-13 07:53",
        updatedAt: "2026-04-13 07:58",
      },
      {
        id: "ops-4",
        name: "历史拓扑图.pptx",
        path: "/mnt/team-share/ops-docs/历史拓扑图.pptx",
        size: "8.2 MB",
        tags: ["拓扑", "历史"],
        updateState: "deleted",
        syncDetail: "文件已从源目录删除，等待清理索引",
        parseStatus: "deleted",
        sourceUpdatedAt: "2026-04-12 21:10",
        updatedAt: "2026-04-12 21:16",
      },
    ],
  },
};

function getParseStatusMeta(status: DocumentStatusRow["parseStatus"], t: TFunction) {
  if (status === "parsed") {
    return {
      color: "#12b76a",
      text: t("admin.dataSourceParseParsed"),
      icon: <CheckCircleFilled />,
    };
  }
  if (status === "reindexing") {
    return {
      color: "#1677ff",
      text: t("admin.dataSourceParseReindexing"),
      icon: <SyncOutlined spin />,
    };
  }
  if (status === "duplicate") {
    return {
      color: "#f79009",
      text: t("admin.dataSourceParseDuplicate"),
      icon: <ClockCircleFilled />,
    };
  }
  if (status === "deleted") {
    return {
      color: "#f04438",
      text: t("admin.dataSourceParseDeleted"),
      icon: <DeleteOutlined />,
    };
  }
  return {
    color: "#f04438",
    text: t("admin.dataSourceParseFailed"),
    icon: <ExclamationCircleFilled />,
  };
}

function isDocumentNeedSync(status: DocumentStatusRow["updateState"]) {
  return status === "new" || status === "changed" || status === "deleted";
}

function formatNow() {
  const current = new Date();
  const pad = (value: number) => `${value}`.padStart(2, "0");
  return `${current.getFullYear()}-${pad(current.getMonth() + 1)}-${pad(
    current.getDate(),
  )} ${pad(current.getHours())}:${pad(current.getMinutes())}`;
}

function getDirectoryLabel(path: string, sourceName: string) {
  const segments = path.split("/").filter(Boolean);
  if (segments.length <= 1) {
    return sourceName;
  }
  return segments.length > 2 ? segments[segments.length - 2] : segments[0];
}

function getDocumentType(name: string) {
  const [, extension = "unknown"] = name.split(/\.(?=[^.]+$)/);
  return extension.toLowerCase();
}

function mapScanSyncDetail(updateState: DocumentStatusRow["updateState"]) {
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

function stringifyScanError(value: unknown) {
  if (!value) return undefined;
  if (typeof value === "string") return value;
  if (typeof value === "object" && value !== null) {
    const message = (value as { message?: string; error?: string }).message ||
      (value as { message?: string; error?: string }).error;
    return message || JSON.stringify(value);
  }
  return `${value}`;
}

function mapScanDocumentToDetail(item: ScanV2Document): DocumentStatusRow {
  const sourceState = resolveSourceState({
    source_state: item.source_state,
    update_type: item.update_type || item.source_state,
    has_update: item.has_update ?? item.source_state !== "UNCHANGED",
  });
  const syncState = resolveSyncState({
    sync_state: item.sync_state,
  });
  const updateState = normalizeDataSourceFileUpdateState(
    item.update_type || item.source_state,
    item.has_update ?? item.source_state !== "UNCHANGED",
  );
  const parseState = [
    item.parse_state,
    item.parse_status,
    item.parse_queue_state,
    item.core_task_state,
    item.scan_orchestration_status,
  ]
    .filter(Boolean)
    .join(" ");
  const lastSyncedAt = formatDateTime(getDocumentLastUpdatedAt(item));
  return {
    id: `${item.document_id}`,
    name: getDocumentDisplayName(item),
    path: getDocumentPath(item),
    size: formatBytes(item.size_bytes),
    tags: item.tags || [],
    updateState,
    syncDetail: item.update_desc || mapScanSyncDetail(updateState),
    parseStatus: normalizeDataSourceParseStatus(parseState),
    sourceUpdatedAt: lastSyncedAt || "-",
    updatedAt: lastSyncedAt || "-",
    sourceState,
    syncState,
    pendingAction: normalizePendingAction(item.pending_action),
    nextSyncAt: item.next_sync_at,
    lastError: stringifyScanError(item.last_error),
    knowledgeBasePresent: item.knowledge_base_present,
  };
}

function buildDetailSummaryFromSource(
  source: ScanV2Source,
  summary: ScanV2Summary | undefined,
  documents: DocumentStatusRow[],
  binding?: ScanV2Binding | null,
  lastSyncedAt?: string,
): DataSourceSummary {
  const sourceId = getScanSourceId(source);
  const target = getScanBindingTarget(binding);
  const lastSync = formatDateTime(lastSyncedAt || binding?.updated_at || getScanSourceUpdatedAt(source)) || "-";
  const isFeishuSource = inferSourceKind(source, binding) === "feishu";
  return {
    id: sourceId,
    name: getScanSourceName(source),
    target: target || "-",
    rootPath: target,
    targetRef: target,
    targetType: binding?.target_type,
    sourceType: isFeishuSource ? "feishu" : "local",
    documentCount: summary?.document_objects || summary?.total_objects || documents.length,
    status: normalizeDataSourceStatus(
      binding?.status || source.status,
      isFeishuSource ? true : binding?.sync_mode !== "manual",
    ),
    lastSync,
    addCount: summary?.new_count || 0,
    deleteCount: summary?.deleted_count || 0,
    changeCount: summary?.modified_count || 0,
    storageUsed: "0 B",
    documents,
    scanManaged: true,
    tenantId: source.tenant_id,
    agentId: binding?.agent_id,
    bindingId: getScanBindingId(binding),
    bindingTreeKey: getScanBindingTreeKey(binding),
    configVersion: getScanSourceConfigVersion(source),
  };
}

function collectScanTreeFileKeys(nodes: ScanV2TreeNode[]): string[] {
  const keys: string[] = [];
  const walk = (items: ScanV2TreeNode[]) => {
    items.forEach((node) => {
      if (node.children?.length) {
        walk(node.children);
      }
      if (!isSelectableScanTreeDocument(node)) {
        return;
      }
      keys.push(`${node.object_key || node.key}`);
    });
  };
  walk(nodes);
  return keys;
}

function collectScanTreeNodesByKey(nodes: ScanV2TreeNode[]) {
  const byKey = new Map<string, ScanV2TreeNode>();
  const walk = (items: ScanV2TreeNode[]) => {
    items.forEach((node) => {
      byKey.set(getScanTreeNodeKey(node), node);
      if (node.children?.length) {
        walk(node.children);
      }
    });
  };
  walk(nodes);
  return byKey;
}

function isSelectableScanTreeDocument(node: ScanV2TreeNode) {
  return node.selectable !== false && node.is_document === true;
}

function getScanTreeNodeKey(node: ScanV2TreeNode) {
  return `${node.object_key || node.key}`;
}

function getScanTreeNodeParentKey(node: ScanV2TreeNode) {
  return `${node.parent_key || ""}`.trim();
}

function getScanTreeNodePage(payload: unknown) {
  const responsePayload = payload as {
    data?: {
      items?: ScanV2TreeNode[];
      next_cursor?: string;
    };
    items?: ScanV2TreeNode[];
    next_cursor?: string;
  };
  const pagePayload = Array.isArray(responsePayload.items)
    ? responsePayload
    : responsePayload.data;

  return {
    items: Array.isArray(pagePayload?.items) ? pagePayload.items : [],
    nextCursor: `${pagePayload?.next_cursor || ""}`,
  };
}

function getScanTreeNodeMergeKeys(node: ScanV2TreeNode) {
  return [
    getScanTreeNodeKey(node),
    node.key,
    node.object_key,
    node.node_ref,
  ]
    .map((key) => `${key || ""}`.trim())
    .filter(Boolean);
}

function normalizeLazyScanTreeNodes(nodes: ScanV2TreeNode[]) {
  return nodes.map((node) => {
    const nextNode = { ...node };
    delete nextNode.children;
    return nextNode;
  });
}

function filterScanTreeChildren(parentKey: string, children: ScanV2TreeNode[]) {
  return children.filter((child) => {
    if (getScanTreeNodeMergeKeys(child).includes(parentKey)) {
      return false;
    }
    const childParentKey = `${child.parent_key || ""}`.trim();
    return !childParentKey || childParentKey === parentKey;
  });
}

function buildSyncGenerateScopes(
  selectedKeys: string[],
  nodeByKey: Map<string, ScanV2TreeNode>,
) {
  const selectedSet = new Set(selectedKeys);
  const scopes: SyncGenerateScope[] = [];

  selectedKeys.forEach((key) => {
    const node = nodeByKey.get(key);
    if (!node) {
      scopes.push({ object_key: key });
      return;
    }

    let parentKey = getScanTreeNodeParentKey(node);
    while (parentKey) {
      if (selectedSet.has(parentKey)) {
        return;
      }
      const parent = nodeByKey.get(parentKey);
      parentKey = parent ? getScanTreeNodeParentKey(parent) : "";
    }

    scopes.push({
      key: node.key,
      object_key: node.object_key || key,
      node_ref: node.node_ref || node.object_key || key,
      is_document: node.is_document === true,
      is_container: node.is_container === true || node.has_children === true,
    });
  });

  return scopes;
}

function mergeScanTreeChildren(
  nodes: ScanV2TreeNode[],
  parentKey: string,
  children: ScanV2TreeNode[],
): ScanV2TreeNode[] {
  return nodes.map((node) => {
    if (getScanTreeNodeMergeKeys(node).includes(parentKey)) {
      return { ...node, children };
    }
    if (node.children?.length) {
      return {
        ...node,
        children: mergeScanTreeChildren(node.children, parentKey, children),
      };
    }
    return node;
  });
}

function getTreeNodeUpdateState(node: ScanV2TreeNode) {
  return normalizeDataSourceFileUpdateState(
    node.update_type || node.source_state,
    node.has_update ?? node.source_state !== "UNCHANGED",
  );
}

function shouldPollByParseStatus(items: DocumentStatusRow[]) {
  return items.some(
    (item) => item.parseStatus !== "parsed" && item.parseStatus !== "failed",
  );
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
    const summaryResponse = await client.getSourceSummary({ sourceId }).catch(() => null);
    const status = `${(summaryResponse?.data as any)?.status || ""}`.toUpperCase();

    if (status === "SUCCEEDED" || status === "PARTIAL_SUCCESS" || summaryResponse?.data) {
      return { run_id: runId, status: status || "SUCCEEDED" };
    }

    if (
      status.includes("FAILED") ||
      status.includes("ERROR") ||
      status.includes("CANCEL")
    ) {
      throw new Error("飞书云同步失败，请检查绑定配置后重试。");
    }

    await sleep(CLOUD_SYNC_POLL_INTERVAL_MS);
  }

  throw new Error("等待飞书目录同步超时，请稍后重试。");
}

export default function DataSourceDetail() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = "" } = useParams();
  const location = useLocation();
  const [keyword, setKeyword] = useState("");

  const routeState = location.state as DataSourceDetailState | null;
  const routeSource = routeState?.source;
  const fallbackSource = fallbackSources[id];
  const initialSource = routeSource
    ? {
        ...routeSource,
        storageUsed: routeSource.storageUsed || documentStatusMap[routeSource.id]?.storageUsed || fallbackSource?.storageUsed || "0 B",
      }
    : fallbackSource;

  const initialDocumentsSeed =
    routeSource?.documents || (initialSource && documentStatusMap[initialSource.id]?.documents) || [];
  const [detailSource, setDetailSource] = useState<DataSourceSummary | undefined>(initialSource);
  const [documents, setDocuments] = useState<DocumentStatusRow[]>(initialDocumentsSeed);
  const [detailLoading, setDetailLoading] = useState(true);
  const [syncSelectedDocIds, setSyncSelectedDocIds] = useState<string[]>([]);
  const [syncPickerOpen, setSyncPickerOpen] = useState(false);
  const [syncTreeNodes, setSyncTreeNodes] = useState<ScanV2TreeNode[]>([]);
  const [syncKnownSelectableFileKeys, setSyncKnownSelectableFileKeys] = useState<Set<string>>(
    () => new Set(),
  );
  const [syncTreeLoading, setSyncTreeLoading] = useState(false);
  const [, setSyncSelectionToken] = useState<string>("");
  const [syncSubmitting, setSyncSubmitting] = useState(false);
  const [syncKeyword, setSyncKeyword] = useState("");
  const [lastSync, setLastSync] = useState(
    initialSource?.lastSync || t("admin.dataSourceNeverSynced"),
  );
  const [lastOperation, setLastOperation] = useState<{
    syncedCount: number;
    ignoredCount: number;
    checkedCount: number;
    time: string;
  } | null>(null);
  const syncPollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const syncPollingActiveRef = useRef(false);
  const syncTreeRequestSeqRef = useRef(0);
  const syncTreeInitialLoadRef = useRef(false);
  const syncTreeChildrenCacheRef = useRef(new Map<string, ScanV2TreeNode[]>());

  const stopSyncPolling = useCallback(() => {
    syncPollingActiveRef.current = false;
    if (syncPollTimerRef.current) {
      clearTimeout(syncPollTimerRef.current);
      syncPollTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    stopSyncPolling();
    setDetailSource(initialSource);
    setDocuments(initialDocumentsSeed);
    setSyncSelectedDocIds([]);
    setSyncPickerOpen(false);
    setSyncTreeNodes([]);
    setSyncKnownSelectableFileKeys(new Set());
    setSyncTreeLoading(false);
    setSyncSelectionToken("");
    setSyncSubmitting(false);
    syncTreeRequestSeqRef.current += 1;
    syncTreeInitialLoadRef.current = false;
    syncTreeChildrenCacheRef.current.clear();
    setLastOperation(null);
  }, [id, routeSource?.id, routeSource?.lastSync, stopSyncPolling]);

  useEffect(() => {
    setLastSync(detailSource?.lastSync || t("admin.dataSourceNeverSynced"));
  }, [detailSource?.lastSync, t]);

  useEffect(() => () => {
    stopSyncPolling();
  }, [stopSyncPolling]);

  const refreshDetailFromServer = useCallback(
    async ({
      setLoading = false,
      showError = true,
      resetSyncState = false,
    }: {
      setLoading?: boolean;
      showError?: boolean;
      resetSyncState?: boolean;
    } = {}): Promise<DocumentStatusRow[] | null> => {
      if (!id) {
        return [];
      }

      if (setLoading) {
        setDetailLoading(true);
      }

      try {
        const client = createScanV2ApiClient();
        const sourceResponse = await client.getSource({ sourceId: id });
        const source = {
          ...sourceResponse.data.source,
          tenant_id: routeSource?.tenantId,
        };
        const binding = getFirstScanBinding(
          sourceResponse.data.bindings as ScanV2Binding[] | undefined,
        );
        const documentsResponse = await client.listSourceDocuments({
          sourceId: id,
          bindingId: getScanBindingId(binding) || routeSource?.bindingId,
          page: 1,
          pageSize: 200,
        });
        const summaryResponse = await client.getSourceSummary({ sourceId: id }).catch(() => null);
        const nextDocuments = (documentsResponse.data.items || []).map(
          mapScanDocumentToDetail,
        );
        const nextSource = buildDetailSummaryFromSource(
          source,
          (summaryResponse?.data || sourceResponse.data.summary) as ScanV2Summary | undefined,
          nextDocuments,
          binding,
          undefined,
        );

        setDetailSource(nextSource);
        setDocuments(nextDocuments);
        setLastSync(nextSource.lastSync || t("admin.dataSourceNeverSynced"));
        if (resetSyncState) {
          setSyncSelectedDocIds([]);
          setSyncPickerOpen(false);
        }

        return nextDocuments;
      } catch (error) {
        if (showError) {
          message.error(
            getLocalizedErrorMessage(error, t("common.requestFailed")) ||
              t("common.requestFailed"),
          );
        }
        return null;
      } finally {
        if (setLoading) {
          setDetailLoading(false);
        }
      }
    },
    [id, routeSource?.tenantId, t],
  );

  useEffect(() => {
    let cancelled = false;

    const loadDetail = async () => {
      const nextDocuments = await refreshDetailFromServer({
        setLoading: true,
        showError: !cancelled,
        resetSyncState: true,
      });
      if (cancelled || nextDocuments === null) {
        return;
      }
    };

    void loadDetail();

    return () => {
      cancelled = true;
    };
  }, [refreshDetailFromServer]);

  const filteredDocuments = useMemo(() => {
    const normalized = keyword.trim().toLowerCase();
    if (!normalized) {
      return documents;
    }

    return documents.filter(
      (item) =>
        item.name.toLowerCase().includes(normalized) ||
        item.path.toLowerCase().includes(normalized) ||
        item.syncDetail.toLowerCase().includes(normalized),
    );
  }, [documents, keyword]);

  const sourceNameForPath = detailSource?.name || t("admin.dataSourceFallbackName");

  const loadSyncTree = useCallback(
    async (
      keywordValue: string,
      options: { selectAll?: boolean; closeOnError?: boolean } = {},
    ) => {
      if (!detailSource?.id) {
        message.error("未获取到数据源信息，无法加载目录树。");
        if (options.closeOnError) {
          setSyncPickerOpen(false);
        }
        return;
      }

      const requestSeq = syncTreeRequestSeqRef.current + 1;
      syncTreeRequestSeqRef.current = requestSeq;
      setSyncTreeLoading(true);

      try {
        const normalizedKeyword = keywordValue.trim();
        const client = createScanV2ApiClient();
        const response = normalizedKeyword
          ? await client.searchSourceTree({
              sourceId: detailSource.id,
              sourceTreeSearchRequest: {
                binding_id: detailSource.bindingId,
                tree_key: detailSource.bindingTreeKey,
                keyword: normalizedKeyword,
                include_documents: true,
                include_containers: true,
                list_mode: "page",
                page_size: SCAN_TREE_PAGE_SIZE,
              },
            })
          : await client.listSourceTreeChildren({
              sourceId: detailSource.id,
              sourceTreeChildrenRequest: {
                binding_id: detailSource.bindingId,
                tree_key: detailSource.bindingTreeKey,
                include_documents: true,
                include_containers: true,
                list_mode: "page",
                page_size: SCAN_TREE_PAGE_SIZE,
                parent_key: "",
              },
            });

        if (syncTreeRequestSeqRef.current !== requestSeq) {
          return;
        }

        const treePage = getScanTreeNodePage(response.data);
        const nextTreeNodes = normalizeLazyScanTreeNodes(treePage.items);
        const nextSelectionToken = treePage.nextCursor;
        const nextSelectableKeys = collectScanTreeFileKeys(nextTreeNodes);

        setSyncTreeNodes(nextTreeNodes);
        syncTreeChildrenCacheRef.current.clear();
        setSyncKnownSelectableFileKeys((prev) => {
          const next = new Set(prev);
          nextSelectableKeys.forEach((key) => next.add(key));
          return next;
        });
        setSyncSelectionToken(nextSelectionToken);
        setSyncSelectedDocIds((prev) =>
          options.selectAll ? nextSelectableKeys : prev,
        );
      } catch (error) {
        if (syncTreeRequestSeqRef.current !== requestSeq) {
          return;
        }
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
        if (options.closeOnError) {
          setSyncPickerOpen(false);
        }
      } finally {
        if (syncTreeRequestSeqRef.current === requestSeq) {
          setSyncTreeLoading(false);
        }
      }
    },
    [detailSource, t],
  );

  const loadSyncTreeChildren = useCallback(
    async (node: DataNode) => {
      if (!detailSource?.id || syncKeyword.trim()) {
        return;
      }

      const treeNode = node as SyncTreeDataNode;
      const parentKey = `${treeNode.objectKey || treeNode.key || ""}`.trim();
      if (!parentKey) {
        return;
      }
      const cachedChildren = syncTreeChildrenCacheRef.current.get(parentKey);
      if (cachedChildren) {
        setSyncTreeNodes((current) =>
          mergeScanTreeChildren(current, parentKey, cachedChildren),
        );
        return;
      }

      try {
        const client = createScanV2ApiClient();
        const requestPage = async (candidate: string) => {
          const response = await client.listSourceTreeChildren({
            sourceId: detailSource.id,
            sourceTreeChildrenRequest: {
              binding_id: detailSource.bindingId,
              tree_key: detailSource.bindingTreeKey,
              parent_key: candidate,
              include_documents: true,
              include_containers: true,
              list_mode: "page",
              page_size: SCAN_TREE_PAGE_SIZE,
            },
          });
          return getScanTreeNodePage(response.data);
        };
        const treePage = await requestPage(parentKey);

        const children = filterScanTreeChildren(
          parentKey,
          normalizeLazyScanTreeNodes(treePage.items),
        );
        const selectableKeys = collectScanTreeFileKeys(children);
        syncTreeChildrenCacheRef.current.set(parentKey, children);
        setSyncKnownSelectableFileKeys((prev) => {
          const next = new Set(prev);
          selectableKeys.forEach((key) => next.add(key));
          return next;
        });
        setSyncTreeNodes((current) =>
          mergeScanTreeChildren(current, parentKey, children),
        );
      } catch (error) {
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
      }
    },
    [detailSource, syncKeyword, t],
  );

  useEffect(() => {
    if (!syncPickerOpen) {
      return;
    }

    const timerId = window.setTimeout(() => {
      const shouldSelectAll =
        syncTreeInitialLoadRef.current && syncKeyword.trim() === "";
      syncTreeInitialLoadRef.current = false;
      void loadSyncTree(syncKeyword, {
        selectAll: shouldSelectAll,
        closeOnError: shouldSelectAll,
      });
    }, 300);

    return () => {
      window.clearTimeout(timerId);
    };
  }, [loadSyncTree, syncKeyword, syncPickerOpen]);

  const openSyncPicker = () => {
    if (!detailSource?.id) {
      message.error("未获取到数据源信息，无法加载目录树。");
      return;
    }

    setSyncKeyword("");
    setSyncPickerOpen(true);
    setSyncTreeNodes([]);
    setSyncKnownSelectableFileKeys(new Set());
    setSyncSelectionToken("");
    setSyncTreeLoading(true);
    syncTreeChildrenCacheRef.current.clear();
    syncTreeInitialLoadRef.current = true;
    setSyncSelectedDocIds([]);
  };

  const runSyncPipeline = async (targetDocumentIds: string[]) => {
    if (targetDocumentIds.length === 0) {
      message.warning(t("admin.dataSourceDetailSelectFileFirst"));
      return false;
    }

    if (!detailSource?.id) {
      message.error("未获取到数据源信息，无法发起拉取。");
      return false;
    }

    const targetPaths = targetDocumentIds.filter(
      (id) =>
        syncKnownSelectableFileKeys.size === 0 ||
        syncKnownSelectableFileKeys.has(id),
    );
    if (targetPaths.length === 0) {
      message.warning(t("admin.dataSourceDetailSelectFileFirst"));
      return false;
    }

    const targetSet = new Set(targetPaths);
    const syncScopeNodesByKey = collectScanTreeNodesByKey(syncTreeNodes);
    const targetScopes = buildSyncGenerateScopes(targetPaths, syncScopeNodesByKey);
    if (targetScopes.length === 0) {
      message.warning(t("admin.dataSourceDetailSelectFileFirst"));
      return false;
    }
    const currentTime = formatNow();

    stopSyncPolling();
    setSyncSubmitting(true);
    try {
      const client = createScanV2ApiClient();
      if (detailSource.sourceType === "feishu") {
        message.info(t("admin.dataSourceDetailCloudSyncPreparing"));
      }

      const generateTasksRequest: {
        mode: string;
        binding_id?: string;
        scopes: SyncGenerateScope[];
        priority?: number;
      } = {
        mode: "partial",
        binding_id: detailSource.bindingId,
        scopes: targetScopes,
        priority: 5,
      }

      // If at least one selected target is a deleted-state synthetic node,
      // attempting with a stale selection_token may be rejected by backend.
      // Retry once without the token in that case.
      const hasDeletedTarget = documents.some(
        (item) =>
          (targetSet.has(item.id) || targetSet.has(item.path)) &&
          (item.sourceState === "DELETED" || item.updateState === "deleted"),
      );

      let generateResponse;
      try {
        generateResponse = await client.generateParseTasks({
          sourceId: detailSource.id,
          generateTasksRequest,
        });
      } catch (err) {
        if (hasDeletedTarget) {
          generateResponse = await client.generateParseTasks({
            sourceId: detailSource.id,
            generateTasksRequest: {
              ...generateTasksRequest,
              mode: "full",
            },
          });
        } else {
          throw err;
        }
      }
      const result = generateResponse.data as typeof generateResponse.data & {
        ignored_unchanged_count?: number;
      };
      const checkedCount = result.requested_count ?? targetPaths.length;
      const syncedCount = result.accepted_count ?? 0;
      const ignoredCount =
        result.ignored_unchanged_count ??
        result.skipped_count ??
        Math.max(checkedCount - syncedCount, 0);

      const checkedRows = documents.filter(
        (item) => targetSet.has(item.id) || targetSet.has(item.path),
      );
      const hasDocumentMatch = checkedRows.length > 0;

      if (hasDocumentMatch) {
        setDocuments((prev) =>
          prev
            .map((item) => {
              if (
                (!targetSet.has(item.id) && !targetSet.has(item.path)) ||
                !isDocumentNeedSync(item.updateState)
              ) {
                return item;
              }

              if (item.updateState === "deleted") {
                return null;
              }

              return {
                ...item,
                updateState: "unchanged",
                parseStatus: "parsed",
                syncDetail: t("admin.dataSourceDetailManualSyncDone"),
                updatedAt: currentTime,
              };
            })
            .filter(Boolean) as DocumentStatusRow[],
        );
      }

      setLastSync(currentTime);
      setLastOperation({
        syncedCount,
        ignoredCount,
        checkedCount,
        time: currentTime,
      });

      if (syncedCount === 0) {
        message.info(
          t("admin.dataSourceDetailSyncNoChange", { checkedCount }),
        );
      } else {
        message.success(
          t("admin.dataSourceDetailSyncDone", {
            syncedCount,
            ignoredCount,
          }),
        );
      }

      setSyncSelectedDocIds([]);

      const refreshedDocuments = await refreshDetailFromServer({
        setLoading: false,
        showError: true,
        resetSyncState: false,
      });
      if (refreshedDocuments && shouldPollByParseStatus(refreshedDocuments)) {
        syncPollingActiveRef.current = true;

        const pollOnce = async () => {
          if (!syncPollingActiveRef.current) {
            return;
          }

          const latestDocuments = await refreshDetailFromServer({
            setLoading: false,
            showError: false,
            resetSyncState: false,
          });
          if (!syncPollingActiveRef.current) {
            return;
          }

          if (latestDocuments && !shouldPollByParseStatus(latestDocuments)) {
            stopSyncPolling();
            return;
          }

          syncPollTimerRef.current = setTimeout(pollOnce, 3000);
        };

        syncPollTimerRef.current = setTimeout(pollOnce, 3000);
      }

      return true;
    } catch (error) {
      stopSyncPolling();
      message.error(
        getLocalizedErrorMessage(error, t("common.requestFailed")) ||
          t("common.requestFailed"),
      );
      return false;
    } finally {
      setSyncSubmitting(false);
    }
  };

  const syncTreeData = useMemo<DataNode[]>(() => {
    const toDataNode = (nodes: ScanV2TreeNode[]): SyncTreeDataNode[] =>
      nodes.map((node) => {
        const children = node.children ? toDataNode(node.children) : undefined;
        const updateState = getTreeNodeUpdateState(node);
        const updateMeta = getFileUpdateMeta(updateState, t);
        const updateText = `${node.update_desc || node.source_state || ""}`.trim() || updateMeta.text;
        const hasUpdateStatus =
          typeof node.has_update === "boolean" || Boolean(node.update_type || node.update_desc || node.source_state);
        const title = node.display_name || node.title || node.object_key || node.key;

        return {
          key: getScanTreeNodeKey(node),
          treeKey: `${node.key}`,
          objectKey: node.object_key,
          nodeRef: node.node_ref,
          isLeaf: !node.has_children,
          disableCheckbox: !isSelectableScanTreeDocument(node),
          title: (
            <div className="data-source-sync-tree-file">
              <div className="data-source-sync-tree-file-main">
                <span>{title}</span>
                {hasUpdateStatus ? (
                  <span
                    className={`data-source-sync-tree-chip data-source-sync-tree-chip-${updateState}`}
                    title={updateText}
                  >
                    {updateText}
                  </span>
                ) : null}
              </div>
            </div>
          ),
          childrenLoaded: Boolean(node.children),
          children,
        };
      });

    return toDataNode(syncTreeNodes);
  }, [syncTreeNodes, t]);

  const checkedTreeKeys = useMemo(
    () => ({ checked: syncSelectedDocIds, halfChecked: [] }),
    [syncSelectedDocIds],
  );
  const filteredSyncNodeKeys = useMemo(
    () => collectScanTreeFileKeys(syncTreeNodes),
    [syncTreeNodes],
  );
  const selectableSyncFileKeys = useMemo(
    () => syncKnownSelectableFileKeys,
    [syncKnownSelectableFileKeys],
  );
  const hasFilteredSelected = filteredSyncNodeKeys.some((id) =>
    syncSelectedDocIds.includes(id),
  );

  const columns: ColumnsType<DocumentStatusRow> = [
    {
      title: t("admin.dataSourceDetailTableDocName"),
      dataIndex: "name",
      key: "name",
      width: 360,
      render: (_value, record) => (
        <div className="data-source-detail-doc">
          <div className="data-source-detail-doc-name">
            <BookOutlined />
            <span>{record.name}</span>
          </div>
          <div className="data-source-detail-doc-path">{record.path}</div>
        </div>
      ),
    },
    {
      title: t("admin.dataSourceDetailTableTags"),
      dataIndex: "tags",
      key: "tags",
      width: 160,
      render: (tags: string[]) =>
        tags.length ? (
          <div className="data-source-detail-tags">
            {tags.map((tag) => (
              <Tag key={tag}>{tag}</Tag>
            ))}
          </div>
        ) : (
          "-"
        ),
    },
    {
      title: t("admin.dataSourceDetailTableDirectory"),
      dataIndex: "path",
      key: "path",
      width: 160,
      render: (path: string) => getDirectoryLabel(path, sourceNameForPath),
    },
    {
      title: t("admin.dataSourceDetailTableUpdateState"),
      dataIndex: "updateState",
      key: "updateState",
      width: 240,
      render: (_value, record) => {
        const sourceState: SourceStateValue = record.sourceState || "UNCHANGED";
        const syncState: SyncStateValue = record.syncState || "IDLE";
        const updateMeta = getFileUpdateMeta(record.updateState, t);
        const syncMeta = getSyncStateMeta(
          syncState,
          {
            nextSyncAt: record.nextSyncAt,
            lastError: record.lastError,
            knowledgeBasePresent: record.knowledgeBasePresent,
            sourceState,
          },
          t,
        );
        const detail =
          record.syncDetail ||
          buildDocumentStatusDetail(
            {
              source_state: sourceState,
              sync_state: syncState,
              next_sync_at: record.nextSyncAt,
              last_error: record.lastError,
              knowledge_base_present: record.knowledgeBasePresent,
              update_type: record.updateState.toUpperCase(),
              has_update: record.updateState !== "unchanged",
            },
            t,
          );
        const shouldShowSyncState = syncState !== "IDLE";
        return (
          <div className="data-source-detail-update-state">
            <span className={`data-source-update-chip data-source-update-chip-${record.updateState}`}>
              <span className="data-source-update-chip-dot" />
              {updateMeta.text}
            </span>
            {shouldShowSyncState ? (
              <Tag color={syncMeta.color} style={{ marginInlineEnd: 0 }}>
                {syncMeta.text}
              </Tag>
            ) : null}
            <Text type="secondary" title={detail}>
              {detail}
            </Text>
          </div>
        );
      },
    },
    {
      title: t("admin.dataSourceDetailTableParseStatus"),
      dataIndex: "parseStatus",
      key: "parseStatus",
      width: 140,
      render: (parseStatus: DocumentStatusRow["parseStatus"], record) => {
        const meta = getParseStatusMeta(parseStatus, t);
        return (
          <Tag
            color={
              parseStatus === "parsed"
                ? "success"
                : parseStatus === "reindexing"
                  ? "processing"
                  : parseStatus === "duplicate"
                    ? "warning"
                    : "error"
            }
            title={record.syncDetail}
          >
            {meta.text}
          </Tag>
        );
      },
    },
    {
      title: t("admin.dataSourceDetailTableDocType"),
      dataIndex: "name",
      key: "docType",
      width: 120,
      render: (name: string) => getDocumentType(name),
    },
    {
      title: t("admin.dataSourceDetailTableSize"),
      dataIndex: "size",
      key: "size",
      width: 120,
      render: (size: string) => (
        <Text className="data-source-detail-size" type="secondary">
          {size}
        </Text>
      ),
    },
    {
      title: t("admin.dataSourceDetailTableUpdatedAt"),
      dataIndex: "updatedAt",
      key: "updatedAt",
      width: 180,
    },
  ];

  return (
    <DataSourceDetailView
      t={t}
      detailSource={detailSource ?? null}
      detailLoading={detailLoading}
      lastSync={lastSync}
      documents={documents}
      lastOperation={lastOperation}
      keyword={keyword}
      setKeyword={setKeyword}
      filteredDocuments={filteredDocuments}
      columns={columns}
      onBack={() => navigate("/data-sources")}
      onOpenSyncPicker={() => {
        void openSyncPicker();
      }}
      syncPickerModal={
        <DataSourceSyncPickerModal
          t={t}
          open={syncPickerOpen}
          syncSubmitting={syncSubmitting}
          selectedCount={syncSelectedDocIds.length}
          syncKeyword={syncKeyword}
          setSyncKeyword={setSyncKeyword}
          hasFilteredSelected={hasFilteredSelected}
          filteredSyncNodeKeys={filteredSyncNodeKeys}
          setSyncSelectedDocIds={setSyncSelectedDocIds}
          syncTreeLoading={syncTreeLoading}
          syncTreeData={syncTreeData}
          checkedTreeKeys={checkedTreeKeys}
          selectableSyncFileKeys={selectableSyncFileKeys}
          onLoadSyncTreeNode={loadSyncTreeChildren}
          onCancel={() => {
            if (!syncSubmitting) {
              syncTreeRequestSeqRef.current += 1;
              syncTreeInitialLoadRef.current = false;
              setSyncPickerOpen(false);
            }
          }}
          onOk={() => {
            void (async () => {
              const finished = await runSyncPipeline(syncSelectedDocIds);
              if (finished) {
                setSyncPickerOpen(false);
              }
            })();
          }}
        />
      }
    />
  );
}
