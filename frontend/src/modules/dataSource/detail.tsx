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
  resolveParsedDocumentCount,
  resolveStorageUsed,
  resolveSourceState,
  resolveSyncState,
} from "./shared";
import {
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
  type ScanV2Document,
  type ScanV2Source,
  type ScanV2Summary,
  type ScanV2TreeNode,
} from "./scanV2Api";

const { Text } = Typography;

const SCAN_TREE_PAGE_SIZE = 50;
const DETAIL_STATUS_POLL_INTERVAL_MS = 3000;
const DETAIL_STATUS_POLL_TIMEOUT_MS = CLOUD_SYNC_TIMEOUT_MS;
const DETAIL_SEARCH_DEBOUNCE_MS = 300;

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

function buildFallbackSources(t: TFunction): Record<
  string,
  DataSourceSummary & { storageUsed: string }
> {
  return {
    "source-feishu-rd": {
      id: "source-feishu-rd",
      name: t("admin.dataSourceDemoData.sources.feishuRdName"),
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
      name: t("admin.dataSourceDemoData.sources.localOpsName"),
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
}

function buildDocumentStatusMap(t: TFunction): Record<
  string,
  {
    storageUsed: string;
    documents: DocumentStatusRow[];
  }
> {
  return {
    "source-feishu-rd": {
      storageUsed: "452.8 MB",
      documents: [
        {
          id: "fs-1",
          name: t("admin.dataSourceDemoData.docs.feishuDevDocName"),
          path: t("admin.dataSourceDemoData.docs.feishuDevDocPath"),
          size: "1.4 MB",
          tags: [
            t("admin.dataSourceDemoData.tags.integration"),
            t("admin.dataSourceDemoData.tags.feishu"),
          ],
          updateState: "changed",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.changedReparsed"),
          parseStatus: "parsed",
          sourceUpdatedAt: "2026-04-13 10:21",
          updatedAt: "2026-04-13 10:24",
        },
        {
          id: "fs-2",
          name: t("admin.dataSourceDemoData.docs.oauthSpecName"),
          path: t("admin.dataSourceDemoData.docs.oauthSpecPath"),
          size: "856 KB",
          tags: ["OAuth", t("admin.dataSourceDemoData.tags.api")],
          updateState: "new",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.newVectorIndexed"),
          parseStatus: "parsed",
          sourceUpdatedAt: "2026-04-13 09:52",
          updatedAt: "2026-04-13 09:58",
        },
        {
          id: "fs-3",
          name: t("admin.dataSourceDemoData.docs.permissionFlowName"),
          path: t("admin.dataSourceDemoData.docs.permissionFlowPath"),
          size: "122 KB",
          tags: [t("admin.dataSourceDemoData.tags.permission")],
          updateState: "changed",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.permissionReindexing"),
          parseStatus: "reindexing",
          sourceUpdatedAt: "2026-04-13 09:40",
          updatedAt: "2026-04-13 09:41",
        },
        {
          id: "fs-4",
          name: t("admin.dataSourceDemoData.docs.legacyConnectionName"),
          path: t("admin.dataSourceDemoData.docs.legacyConnectionPath"),
          size: "730 KB",
          tags: [t("admin.dataSourceDemoData.tags.archive")],
          updateState: "unchanged",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.duplicateVersioned"),
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
          name: t("admin.dataSourceDemoData.docs.inspectionManualName"),
          path: t("admin.dataSourceDemoData.docs.inspectionManualPath"),
          size: "2.1 MB",
          tags: [t("admin.dataSourceDemoData.tags.inspection"), "SOP"],
          updateState: "changed",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.changedReparsed"),
          parseStatus: "parsed",
          sourceUpdatedAt: "2026-04-13 08:09",
          updatedAt: "2026-04-13 08:12",
        },
        {
          id: "ops-2",
          name: t("admin.dataSourceDemoData.docs.dutyScheduleName"),
          path: t("admin.dataSourceDemoData.docs.dutySchedulePath"),
          size: "414 KB",
          tags: [t("admin.dataSourceDemoData.tags.schedule")],
          updateState: "new",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.newIndexDone"),
          parseStatus: "parsed",
          sourceUpdatedAt: "2026-04-13 08:00",
          updatedAt: "2026-04-13 08:05",
        },
        {
          id: "ops-3",
          name: t("admin.dataSourceDemoData.docs.incidentReviewName"),
          path: t("admin.dataSourceDemoData.docs.incidentReviewPath"),
          size: "96 KB",
          tags: [t("admin.dataSourceDemoData.tags.review")],
          updateState: "changed",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.rechunking"),
          parseStatus: "reindexing",
          sourceUpdatedAt: "2026-04-13 07:53",
          updatedAt: "2026-04-13 07:58",
        },
        {
          id: "ops-4",
          name: t("admin.dataSourceDemoData.docs.topologyArchiveName"),
          path: t("admin.dataSourceDemoData.docs.topologyArchivePath"),
          size: "8.2 MB",
          tags: [
            t("admin.dataSourceDemoData.tags.topology"),
            t("admin.dataSourceDemoData.tags.history"),
          ],
          updateState: "deleted",
          syncDetail: t("admin.dataSourceDemoData.syncDetails.sourceDeletedCleanup"),
          parseStatus: "deleted",
          sourceUpdatedAt: "2026-04-12 21:10",
          updatedAt: "2026-04-12 21:16",
        },
      ],
    },
  };
}

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
  if (status === "pending") {
    return {
      color: "default",
      text: t("admin.dataSourceParsePending"),
      icon: <ClockCircleFilled />,
    };
  }
  if (status === "downloading") {
    return {
      color: "#1677ff",
      text: t("admin.dataSourceParseDownloading"),
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
  if (status === "download_failed") {
    return {
      color: "#f04438",
      text: t("admin.dataSourceParseDownloadFailed"),
      icon: <ExclamationCircleFilled />,
    };
  }
  if (status === "parse_failed") {
    return {
      color: "#f04438",
      text: t("admin.dataSourceParseParseFailed"),
      icon: <ExclamationCircleFilled />,
    };
  }
  if (status === "canceled") {
    return {
      color: "#f79009",
      text: t("admin.dataSourceParseCanceled"),
      icon: <ClockCircleFilled />,
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

function mapScanSyncDetail(
  updateState: DocumentStatusRow["updateState"],
  t: TFunction,
) {
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

function mapScanDocumentToDetail(
  item: ScanV2Document,
  t: TFunction,
  sourceType?: DataSourceSummary["sourceType"],
): DocumentStatusRow {
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
  const effectiveParseState = item.effective_parse_status || item.effectiveParseStatus;
  const fallbackParseState = [
    item.parse_state,
    item.parse_status,
    item.parse_queue_state,
    item.core_task_state,
    item.scan_orchestration_status,
  ]
    .filter(Boolean)
    .join(" ");
  const parseState = `${effectiveParseState || ""}`.trim() || fallbackParseState;
  const lastSyncedAt = formatDateTime(getDocumentLastUpdatedAt(item));
  return {
    id: `${item.document_id}`,
    name: getDocumentDisplayName(item),
    path: getDocumentPath(item),
    size: formatBytes(item.size_bytes),
    tags: item.tags || [],
    updateState,
    syncDetail: item.update_desc || mapScanSyncDetail(updateState, t),
    parseStatus: normalizeDataSourceParseStatus(parseState, item.last_error, {
      sourceType,
    }),
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
    parsedDocumentCount: resolveParsedDocumentCount(summary),
    status: normalizeDataSourceStatus(
      binding?.status || source.status,
      isFeishuSource ? true : binding?.sync_mode !== "manual",
    ),
    lastSync,
    addCount: summary?.new_count || 0,
    deleteCount: summary?.deleted_count || 0,
    changeCount: summary?.modified_count || 0,
    storageUsed: resolveStorageUsed(summary),
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

function shouldPollDocumentStatus(
  items: DocumentStatusRow[],
  trackedKeys?: Set<string>,
) {
  return items.some(
    (item) =>
      item.parseStatus === "reindexing" ||
      item.parseStatus === "downloading" ||
      item.syncState === "PENDING" ||
      item.syncState === "RUNNING",
  );
}

export default function DataSourceDetail() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { id = "" } = useParams();
  const location = useLocation();
  const [keyword, setKeyword] = useState("");

  const routeState = location.state as DataSourceDetailState | null;
  const routeSource = routeState?.source;
  const fallbackSources = useMemo(() => buildFallbackSources(t), [t]);
  const documentStatusMap = useMemo(() => buildDocumentStatusMap(t), [t]);
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
  const [displayDocuments, setDisplayDocuments] = useState<DocumentStatusRow[]>(initialDocumentsSeed);
  const [detailLoading, setDetailLoading] = useState(true);
  const [documentLoading, setDocumentLoading] = useState(false);
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
  const syncPollSeqRef = useRef(0);
  const detailRefreshSeqRef = useRef(0);
  const documentRefreshSeqRef = useRef(0);
  const documentSearchSeqRef = useRef(0);
  const keywordRef = useRef("");
  const syncTreeRequestSeqRef = useRef(0);
  const syncTreeInitialLoadRef = useRef(false);
  const syncTreeChildrenCacheRef = useRef(new Map<string, ScanV2TreeNode[]>());

  const stopSyncPolling = useCallback(() => {
    syncPollingActiveRef.current = false;
    syncPollSeqRef.current += 1;
    documentRefreshSeqRef.current += 1;
    if (syncPollTimerRef.current) {
      clearTimeout(syncPollTimerRef.current);
      syncPollTimerRef.current = null;
    }
  }, []);

  useEffect(() => {
    stopSyncPolling();
    setDetailSource(initialSource);
    setDocuments(initialDocumentsSeed);
    setDisplayDocuments(initialDocumentsSeed);
    setKeyword("");
    setDocumentLoading(false);
    setSyncSelectedDocIds([]);
    setSyncPickerOpen(false);
    setSyncTreeNodes([]);
    setSyncKnownSelectableFileKeys(new Set());
    setSyncTreeLoading(false);
    setSyncSelectionToken("");
    setSyncSubmitting(false);
    detailRefreshSeqRef.current += 1;
    documentRefreshSeqRef.current += 1;
    documentSearchSeqRef.current += 1;
    syncTreeRequestSeqRef.current += 1;
    syncTreeInitialLoadRef.current = false;
    syncTreeChildrenCacheRef.current.clear();
    setLastOperation(null);
  }, [id, routeSource?.id, routeSource?.lastSync, stopSyncPolling, t]);

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

      const requestSeq = detailRefreshSeqRef.current + 1;
      detailRefreshSeqRef.current = requestSeq;

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
        const sourceType = inferSourceKind(source, binding);
        const documentsResponse = await client.listSourceDocuments({
          sourceId: id,
          page: 1,
          pageSize: 200,
        });
        const summaryResponse = await client.getSourceSummary({ sourceId: id }).catch(() => null);
        const nextDocuments = (documentsResponse.data.items || []).map(
          (item) => mapScanDocumentToDetail(item, t, sourceType),
        );
        if (detailRefreshSeqRef.current !== requestSeq) {
          return null;
        }

        const nextSource = buildDetailSummaryFromSource(
          source,
          (summaryResponse?.data || sourceResponse.data.summary) as ScanV2Summary | undefined,
          nextDocuments,
          binding,
          undefined,
        );

        setDetailSource(nextSource);
        setDocuments(nextDocuments);
        if (!keywordRef.current.trim()) {
          setDisplayDocuments(nextDocuments);
        }
        setLastSync(nextSource.lastSync || t("admin.dataSourceNeverSynced"));
        if (resetSyncState) {
          setSyncSelectedDocIds([]);
          setSyncPickerOpen(false);
        }

        return nextDocuments;
      } catch (error) {
        if (showError && detailRefreshSeqRef.current === requestSeq) {
          message.error(
            getLocalizedErrorMessage(error, t("common.requestFailed")) ||
              t("common.requestFailed"),
          );
        }
        return null;
      } finally {
        if (setLoading && detailRefreshSeqRef.current === requestSeq) {
          setDetailLoading(false);
        }
      }
    },
    [id, routeSource?.bindingId, routeSource?.tenantId, t],
  );

  const refreshDocumentsFromServer = useCallback(
    async ({
      showError = false,
    }: {
      showError?: boolean;
    } = {}): Promise<DocumentStatusRow[] | null> => {
      if (!id) {
        return [];
      }

      const requestSeq = documentRefreshSeqRef.current + 1;
      documentRefreshSeqRef.current = requestSeq;

      try {
        const client = createScanV2ApiClient();
        const documentsResponse = await client.listSourceDocuments({
          sourceId: id,
          page: 1,
          pageSize: 200,
        });
        const nextDocuments = (documentsResponse.data.items || []).map((item) =>
          mapScanDocumentToDetail(item, t),
        );
        if (documentRefreshSeqRef.current !== requestSeq) {
          return null;
        }

        setDocuments(nextDocuments);
        if (!keywordRef.current.trim()) {
          setDisplayDocuments(nextDocuments);
        }

        return nextDocuments;
      } catch (error) {
        if (showError && documentRefreshSeqRef.current === requestSeq) {
          message.error(
            getLocalizedErrorMessage(error, t("common.requestFailed")) ||
              t("common.requestFailed"),
          );
        }
        return null;
      }
    },
    [id, t],
  );

  const startSyncPolling = useCallback(
    (seedDocuments: DocumentStatusRow[] | null, trackedKeys?: Set<string>) => {
      if (!seedDocuments || !shouldPollDocumentStatus(seedDocuments, trackedKeys)) {
        stopSyncPolling();
        return;
      }

      stopSyncPolling();
      syncPollingActiveRef.current = true;
      const pollSeq = syncPollSeqRef.current + 1;
      syncPollSeqRef.current = pollSeq;
      const startedAt = Date.now();

      const pollOnce = async () => {
        if (!syncPollingActiveRef.current || syncPollSeqRef.current !== pollSeq) {
          return;
        }

        if (Date.now() - startedAt >= DETAIL_STATUS_POLL_TIMEOUT_MS) {
          stopSyncPolling();
          return;
        }

        const latestDocuments = await refreshDocumentsFromServer({
          showError: false,
        });
        if (!syncPollingActiveRef.current || syncPollSeqRef.current !== pollSeq) {
          return;
        }

        if (latestDocuments && !shouldPollDocumentStatus(latestDocuments, trackedKeys)) {
          stopSyncPolling();
          return;
        }

        syncPollTimerRef.current = setTimeout(
          pollOnce,
          DETAIL_STATUS_POLL_INTERVAL_MS,
        );
      };

      syncPollTimerRef.current = setTimeout(
        pollOnce,
        DETAIL_STATUS_POLL_INTERVAL_MS,
      );
    },
    [refreshDocumentsFromServer, stopSyncPolling],
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
      startSyncPolling(nextDocuments);
    };

    void loadDetail();

    return () => {
      cancelled = true;
      detailRefreshSeqRef.current += 1;
    };
  }, [refreshDetailFromServer, startSyncPolling]);

  useEffect(() => {
    keywordRef.current = keyword;
    const normalizedKeyword = keyword.trim();
    const requestSeq = documentSearchSeqRef.current + 1;
    documentSearchSeqRef.current = requestSeq;

    if (!normalizedKeyword) {
      setDisplayDocuments(documents);
      setDocumentLoading(false);
      return;
    }

    const timerId = setTimeout(async () => {
      if (!id) {
        return;
      }

      setDocumentLoading(true);
      try {
        const client = createScanV2ApiClient();
        const documentsResponse = await client.listSourceDocuments({
          sourceId: id,
          bindingId: detailSource?.bindingId || routeSource?.bindingId,
          keyword: normalizedKeyword,
          page: 1,
          pageSize: 200,
        });
        if (documentSearchSeqRef.current !== requestSeq) {
          return;
        }
        setDisplayDocuments(
          (documentsResponse.data.items || []).map((item) =>
            mapScanDocumentToDetail(item, t),
          ),
        );
      } catch (error) {
        if (documentSearchSeqRef.current !== requestSeq) {
          return;
        }
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
      } finally {
        if (documentSearchSeqRef.current === requestSeq) {
          setDocumentLoading(false);
        }
      }
    }, DETAIL_SEARCH_DEBOUNCE_MS);

    return () => {
      clearTimeout(timerId);
      documentSearchSeqRef.current += 1;
    };
  }, [detailSource?.bindingId, id, keyword, routeSource?.bindingId, t]);

  useEffect(() => {
    if (!keywordRef.current.trim()) {
      setDisplayDocuments(documents);
    }
  }, [documents]);

  const sourceNameForPath = detailSource?.name || t("admin.dataSourceFallbackName");

  const loadSyncTree = useCallback(
    async (
      keywordValue: string,
      options: { selectAll?: boolean; closeOnError?: boolean } = {},
    ) => {
      if (!detailSource?.id) {
        message.error(t("admin.dataSourceDetailMissingForTree"));
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
        const selectableKeys = collectScanTreeFileKeys(cachedChildren);
        setSyncTreeNodes((current) =>
          mergeScanTreeChildren(current, parentKey, cachedChildren),
        );
        if (syncSelectedDocIds.includes(parentKey)) {
          setSyncSelectedDocIds((current) =>
            Array.from(new Set([...current, ...selectableKeys])),
          );
        }
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
        if (syncSelectedDocIds.includes(parentKey)) {
          setSyncSelectedDocIds((current) =>
            Array.from(new Set([...current, ...selectableKeys])),
          );
        }
      } catch (error) {
        message.error(
          getLocalizedErrorMessage(error, t("common.requestFailed")) ||
            t("common.requestFailed"),
        );
      }
    },
    [detailSource, syncKeyword, syncSelectedDocIds, t],
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
      message.error(t("admin.dataSourceDetailMissingForTree"));
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
      message.error(t("admin.dataSourceDetailMissingForSync"));
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
      startSyncPolling(refreshedDocuments, targetSet);

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

  const checkedTreeKeys = syncSelectedDocIds;
  const filteredSyncNodeKeys = useMemo(
    () => collectScanTreeFileKeys(syncTreeNodes),
    [syncTreeNodes],
  );
  const selectableSyncFileKeys = useMemo(
    () => syncKnownSelectableFileKeys,
    [syncKnownSelectableFileKeys],
  );
  const hasFilteredSelected =
    filteredSyncNodeKeys.length > 0 &&
    filteredSyncNodeKeys.every((id) => syncSelectedDocIds.includes(id));

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
                : parseStatus === "reindexing" || parseStatus === "downloading"
                  ? "processing"
                  : parseStatus === "pending"
                    ? "default"
                  : parseStatus === "duplicate"
                    ? "warning"
                  : parseStatus === "canceled"
                    ? "warning"
                    : "error"
            }
            title={record.lastError || record.syncDetail}
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
      documentLoading={documentLoading}
      lastSync={lastSync}
      lastOperation={lastOperation}
      keyword={keyword}
      setKeyword={setKeyword}
      filteredDocuments={displayDocuments}
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
