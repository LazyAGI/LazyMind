import { useEffect, useRef, useState } from "react";
import type { TreeSelectProps } from "antd";
import type { TFunction } from "i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "../api/clients";
import type { FeishuTargetType } from "../constants/types";
import { getScanTenantId } from "../utils/scanAccessors";
import {
  buildFeishuTargetTreeFromScanNodes,
  mapFeishuScanNodesToTreeNodes,
  normalizeFeishuTargetType,
  toScanFeishuTargetType,
  type FeishuTargetTreeNode,
} from "../utils/feishuTarget";

interface UseFeishuTargetTreeParams {
  t: TFunction;
  feishuTargetType: FeishuTargetType;
  getActiveFeishuAuthConnectionId: () => string;
}

function cloneFeishuTargetTree(
  nodes: FeishuTargetTreeNode[],
): FeishuTargetTreeNode[] {
  return nodes.map((node) => ({
    ...node,
    children: node.children ? cloneFeishuTargetTree(node.children) : undefined,
  }));
}

function hasBrowsableFeishuTree(nodes: FeishuTargetTreeNode[]) {
  return nodes.some((node) => {
    const value = `${node.value || ""}`.trim();
    return Boolean(value) && !value.startsWith("__scan-feishu-") && !node.disabled;
  });
}

export function useFeishuTargetTree({
  t,
  feishuTargetType,
  getActiveFeishuAuthConnectionId,
}: UseFeishuTargetTreeParams) {
  const [feishuTargetTreeData, setFeishuTargetTreeData] = useState<FeishuTargetTreeNode[]>([]);
  const [feishuTargetLoading, setFeishuTargetLoading] = useState(false);
  const feishuTargetRequestSeqRef = useRef(0);
  const feishuTargetSearchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const browseTreeCacheRef = useRef<FeishuTargetTreeNode[]>([]);
  const isSearchingRef = useRef(false);

  useEffect(
    () => () => {
      if (feishuTargetSearchTimerRef.current) {
        clearTimeout(feishuTargetSearchTimerRef.current);
      }
    },
    [],
  );

  const buildFeishuHelperNode = (title: string): FeishuTargetTreeNode => ({
    key: "__scan-feishu-target-helper__",
    value: "__scan-feishu-target-helper__",
    title,
    disabled: true,
    isLeaf: true,
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

  const rememberBrowseTree = (nodes: FeishuTargetTreeNode[]) => {
    if (!hasBrowsableFeishuTree(nodes)) {
      return;
    }
    browseTreeCacheRef.current = cloneFeishuTargetTree(nodes);
  };

  const restoreBrowseTree = () => {
    const cached = browseTreeCacheRef.current;
    if (!hasBrowsableFeishuTree(cached)) {
      return false;
    }
    setFeishuTargetTreeData(cloneFeishuTargetTree(cached));
    setFeishuTargetLoading(false);
    return true;
  };

  const resetFeishuTargetBrowseOptions = () => {
    feishuTargetRequestSeqRef.current += 1;
    if (feishuTargetSearchTimerRef.current) {
      clearTimeout(feishuTargetSearchTimerRef.current);
      feishuTargetSearchTimerRef.current = null;
    }
    isSearchingRef.current = false;
    browseTreeCacheRef.current = [];
    setFeishuTargetTreeData([]);
    setFeishuTargetLoading(false);
  };

  const seedFeishuTargetTree = (nodes: FeishuTargetTreeNode[]) => {
    feishuTargetRequestSeqRef.current += 1;
    isSearchingRef.current = false;
    // Flat seed nodes are for selected labels only; keep browse cache for reopen.
    setFeishuTargetTreeData(nodes);
    setFeishuTargetLoading(false);
  };

  const loadFeishuTargetOptions = async (
    keyword = "",
    options?: { force?: boolean },
  ) => {
    const requestSeq = feishuTargetRequestSeqRef.current + 1;
    feishuTargetRequestSeqRef.current = requestSeq;
    const authConnectionId = getActiveFeishuAuthConnectionId();

    if (!authConnectionId) {
      isSearchingRef.current = false;
      setFeishuTargetTreeData([
        buildFeishuHelperNode(t("admin.dataSourceFeishuAuthorizeFirstBrowse")),
      ]);
      setFeishuTargetLoading(false);
      return;
    }

    const normalizedKeyword = keyword.trim();
    isSearchingRef.current = Boolean(normalizedKeyword);

    // Reuse the previously browsed tree while the wizard stays open.
    if (!normalizedKeyword && !options?.force && restoreBrowseTree()) {
      return;
    }

    setFeishuTargetTreeData([]);
    setFeishuTargetLoading(true);
    try {
      const client = dataSourceScanApi;
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

      const rawNodes = response.data.items || [];
      const nextNodes = normalizedKeyword
        ? buildFeishuTargetTreeFromScanNodes(rawNodes)
        : mapFeishuScanNodesToTreeNodes(rawNodes);
      const resolvedNodes =
        nextNodes.length > 0
          ? nextNodes
          : [buildFeishuHelperNode(t("admin.dataSourceNoFeishuTargets"))];

      if (!normalizedKeyword) {
        rememberBrowseTree(resolvedNodes);
      }
      setFeishuTargetTreeData(resolvedNodes);
    } catch (error) {
      if (feishuTargetRequestSeqRef.current !== requestSeq) {
        return;
      }
      setFeishuTargetTreeData([
        buildFeishuHelperNode(
          getLocalizedErrorMessage(
            error,
            t("admin.dataSourceFeishuDirectoryListFailedManual"),
          ) || t("admin.dataSourceFeishuDirectoryListFailedManual"),
        ),
      ]);
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
      isSearchingRef.current = false;
      if (restoreBrowseTree()) {
        return;
      }
      setFeishuTargetTreeData([]);
      setFeishuTargetLoading(true);
      feishuTargetSearchTimerRef.current = setTimeout(() => {
        void loadFeishuTargetOptions("");
      }, 300);
      return;
    }

    setFeishuTargetTreeData([]);
    setFeishuTargetLoading(true);
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
    const uiTargetType =
      normalizeFeishuTargetType(treeNode.targetType, targetRef) || feishuTargetType;
    const targetType = toScanFeishuTargetType(uiTargetType);

    if (treeNode.children) {
      setFeishuTargetTreeData((current) => {
        const next = mergeFeishuTargetChildren(
          current,
          treeNode.key || treeNode.value,
          treeNode.children || [],
        );
        if (!isSearchingRef.current) {
          rememberBrowseTree(next);
        }
        return next;
      });
      return;
    }

    const response = await dataSourceScanApi.listBindingTargetChildren({
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

    const children = mapFeishuScanNodesToTreeNodes(response.data.items || [], uiTargetType);
    setFeishuTargetTreeData((current) => {
      const next = mergeFeishuTargetChildren(
        current,
        treeNode.key || treeNode.value,
        children,
      );
      if (!isSearchingRef.current) {
        rememberBrowseTree(next);
      }
      return next;
    });
  };

  return {
    feishuTargetTreeData,
    feishuTargetLoading,
    loadFeishuTargetOptions,
    handleSearchFeishuTargetOptions,
    handleLoadFeishuTargetChildren,
    resetFeishuTargetBrowseOptions,
    seedFeishuTargetTree,
  };
}
