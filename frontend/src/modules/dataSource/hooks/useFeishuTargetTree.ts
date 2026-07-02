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

      const rawNodes = response.data.items || [];
      const nextNodes = normalizedKeyword
        ? buildFeishuTargetTreeFromScanNodes(rawNodes)
        : mapFeishuScanNodesToTreeNodes(rawNodes);

      setFeishuTargetTreeData(
        nextNodes.length > 0
          ? nextNodes
          : [buildFeishuHelperNode(t("admin.dataSourceNoFeishuTargets"))],
      );
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

    const children = mapFeishuScanNodesToTreeNodes(response.data.items || [], uiTargetType);
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
