import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { message } from "antd";
import type { DataNode } from "antd/es/tree";
import type { TFunction } from "i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "../api/clients";
import type {
  DataSourceDetailState,
  DataSourceSummary,
  DocumentStatusRow,
} from "../constants/types";
import type { ScanV2TreeNode } from "../utils/scanAccessors";
import {
  buildSyncGenerateScopes,
  collectScanTreeFileKeys,
  collectScanTreeNodesByKey,
  filterScanTreeChildren,
  getScanTreeNodePage,
  mergeScanTreeChildren,
  normalizeLazyScanTreeNodes,
  type SyncGenerateScope,
  type SyncTreeDataNode,
} from "../utils/scanTree";
import { formatNow, isDocumentNeedSync } from "../utils/detailHelpers";
import { buildSyncTreeData } from "../components/detail/buildSyncTreeData";

const SCAN_TREE_PAGE_SIZE = 50;

interface UseSyncPickerParams {
  t: TFunction;
  id: string;
  routeSource: DataSourceDetailState["source"] | undefined;
  detailSource: DataSourceSummary | undefined;
  documents: DocumentStatusRow[];
  setDocuments: React.Dispatch<React.SetStateAction<DocumentStatusRow[]>>;
  setLastSync: React.Dispatch<React.SetStateAction<string>>;
  setLastOperation: React.Dispatch<
    React.SetStateAction<{
      syncedCount: number;
      ignoredCount: number;
      checkedCount: number;
      time: string;
    } | null>
  >;
  stopSyncPolling: () => void;
  startSyncPolling: (seedDocuments: DocumentStatusRow[] | null) => void;
  refreshDetailFromServer: (options?: {
    setLoading?: boolean;
    showError?: boolean;
    resetSyncState?: boolean;
  }) => Promise<DocumentStatusRow[] | null>;
  resetSyncStateToken: number;
}

export function useSyncPicker({
  t,
  id,
  routeSource,
  detailSource,
  documents,
  setDocuments,
  setLastSync,
  setLastOperation,
  stopSyncPolling,
  startSyncPolling,
  refreshDetailFromServer,
  resetSyncStateToken,
}: UseSyncPickerParams) {
  const [syncSelectedDocIds, setSyncSelectedDocIds] = useState<string[]>([]);
  const [syncPickerOpen, setSyncPickerOpen] = useState(false);
  const [syncTreeNodes, setSyncTreeNodes] = useState<ScanV2TreeNode[]>([]);
  const [syncKnownSelectableFileKeys, setSyncKnownSelectableFileKeys] = useState<
    Set<string>
  >(() => new Set());
  const [syncTreeLoading, setSyncTreeLoading] = useState(false);
  const [, setSyncSelectionToken] = useState<string>("");
  const [syncSubmitting, setSyncSubmitting] = useState(false);
  const [syncKeyword, setSyncKeyword] = useState("");

  const syncTreeRequestSeqRef = useRef(0);
  const syncTreeInitialLoadRef = useRef(false);
  const syncTreeChildrenCacheRef = useRef(new Map<string, ScanV2TreeNode[]>());

  useEffect(() => {
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
  }, [id, routeSource?.id, routeSource?.lastSync]);

  useEffect(() => {
    setSyncSelectedDocIds([]);
    setSyncPickerOpen(false);
  }, [resetSyncStateToken]);

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
        const client = dataSourceScanApi;
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
        const client = dataSourceScanApi;
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
      (key) =>
        syncKnownSelectableFileKeys.size === 0 ||
        syncKnownSelectableFileKeys.has(key),
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
      const client = dataSourceScanApi;
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
      };

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
        message.info(t("admin.dataSourceDetailSyncNoChange", { checkedCount }));
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
      startSyncPolling(refreshedDocuments);

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

  const syncTreeData = useMemo<DataNode[]>(
    () => buildSyncTreeData(syncTreeNodes, t),
    [syncTreeNodes, t],
  );

  const filteredSyncNodeKeys = useMemo(
    () => collectScanTreeFileKeys(syncTreeNodes),
    [syncTreeNodes],
  );
  const hasFilteredSelected =
    filteredSyncNodeKeys.length > 0 &&
    filteredSyncNodeKeys.every((key) => syncSelectedDocIds.includes(key));

  return {
    syncSelectedDocIds,
    setSyncSelectedDocIds,
    syncPickerOpen,
    setSyncPickerOpen,
    syncTreeLoading,
    syncSubmitting,
    syncKeyword,
    setSyncKeyword,
    syncTreeData,
    filteredSyncNodeKeys,
    selectableSyncFileKeys: syncKnownSelectableFileKeys,
    hasFilteredSelected,
    syncTreeRequestSeqRef,
    syncTreeInitialLoadRef,
    openSyncPicker,
    loadSyncTreeChildren,
    runSyncPipeline,
  };
}
