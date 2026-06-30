import { useEffect, useRef, useState } from "react";
import type { TreeSelectProps } from "antd";
import type { TFunction } from "i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "../api/clients";
import type { FeishuTargetType } from "../constants/types";
import {
  getScanTenantId,
  getScanTreeNodePath,
  type ScanV2TreeNode,
} from "../utils/scanAccessors";
import {
  buildFeishuRootTargetNodes,
  buildManualFeishuTargetValue,
  mergeFeishuTargetSearchResults,
  normalizeFeishuTargetType,
  toScanFeishuTargetType,
  type FeishuManualTargetKind,
  type FeishuTargetTreeNode,
} from "../utils/feishuTarget";

interface UseFeishuTargetTreeParams {
  t: TFunction;
  feishuTargetType: FeishuTargetType;
  getActiveFeishuAuthConnectionId: () => string;
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

  const buildManualFeishuTargetNode = (
    targetRef: string,
    kind: FeishuManualTargetKind,
  ): FeishuTargetTreeNode => {
    const normalizedTargetRef = targetRef.trim();
    const targetType = kind === "wiki" ? "wiki_space" : "drive_folder";
    const title =
      kind === "wiki"
        ? t("admin.dataSourceUseCurrentFeishuWikiInput", { value: normalizedTargetRef })
        : t("admin.dataSourceUseCurrentFeishuDriveInput", { value: normalizedTargetRef });
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

  const buildManualFeishuTargetNodes = (targetRef: string): FeishuTargetTreeNode[] =>
    (["drive", "wiki"] as FeishuManualTargetKind[]).map((kind) =>
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

      return (
        refs.includes(targetRef) ||
        Boolean(node.children && hasFeishuTargetRef(node.children, targetRef))
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

  const mapFeishuTargetNodes = (
    nodes: ScanV2TreeNode[],
    inheritedTargetType?: FeishuTargetType,
  ): FeishuTargetTreeNode[] =>
    nodes.map((node) => {
      const value =
        getScanTreeNodePath(node) || `${node.key || node.node_ref || node.display_name}`;
      const title = node.display_name || node.title || node.object_key || value;
      const targetRef = node.target_ref || value;
      const nodeRef = node.node_ref;
      const targetType =
        normalizeFeishuTargetType(node.target_type, `${targetRef || nodeRef || value}`) ||
        inheritedTargetType;

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

  const resetFeishuTargetBrowseOptions = () => {
    feishuTargetRequestSeqRef.current += 1;
    if (feishuTargetSearchTimerRef.current) {
      clearTimeout(feishuTargetSearchTimerRef.current);
      feishuTargetSearchTimerRef.current = null;
    }
    setFeishuTargetTreeData([]);
    setFeishuTargetLoading(false);
  };

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

      const nodes = mapFeishuTargetNodes(response.data.items || []);
      let nextNodes: FeishuTargetTreeNode[];

      if (normalizedKeyword) {
        const rootNodes = buildFeishuRootTargetNodes();
        const mergedNodes = mergeFeishuTargetSearchResults(rootNodes, nodes);
        const baseNodes =
          nodes.length > 0
            ? mergedNodes
            : [...mergedNodes, buildFeishuHelperNode(t("admin.dataSourceNoFeishuTargets"))];
        nextNodes = prependManualFeishuTargetOption(normalizedKeyword, baseNodes);
      } else {
        const baseNodes =
          nodes.length > 0
            ? nodes
            : [buildFeishuHelperNode(t("admin.dataSourceNoFeishuTargets"))];
        nextNodes = baseNodes;
      }

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
              ...buildFeishuRootTargetNodes(),
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
      setFeishuTargetTreeData([]);
      setFeishuTargetLoading(true);
      feishuTargetSearchTimerRef.current = setTimeout(() => {
        void loadFeishuTargetOptions("");
      }, 300);
      return;
    }

    setFeishuTargetTreeData(
      prependManualFeishuTargetOption(normalizedKeyword, buildFeishuRootTargetNodes()),
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
    const uiTargetType =
      normalizeFeishuTargetType(treeNode.targetType, targetRef) || feishuTargetType;
    const targetType = toScanFeishuTargetType(uiTargetType);

    if (treeNode.children) {
      setFeishuTargetTreeData((current) =>
        mergeFeishuTargetChildren(
          current,
          treeNode.key || treeNode.value,
          treeNode.children || [],
        ),
      );
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

    const children = mapFeishuTargetNodes(response.data.items || [], uiTargetType);
    setFeishuTargetTreeData((current) =>
      mergeFeishuTargetChildren(current, treeNode.key || treeNode.value, children),
    );
  };

  return {
    feishuTargetTreeData,
    feishuTargetLoading,
    loadFeishuTargetOptions,
    handleSearchFeishuTargetOptions,
    handleLoadFeishuTargetChildren,
    resetFeishuTargetBrowseOptions,
  };
}
