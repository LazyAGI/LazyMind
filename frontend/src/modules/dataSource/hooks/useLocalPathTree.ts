import { useEffect, useRef, useState } from "react";
import type { FormInstance, TreeSelectProps } from "antd";
import type { TFunction } from "i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import { dataSourceScanApi } from "../api/clients";
import type { SourceFormValues } from "../constants/types";
import { getScanTreeNodePath, type ScanV2TreeNode } from "../utils/scanAccessors";
import type { LocalPathTreeNode } from "../utils/feishuTarget";

interface UseLocalPathTreeParams {
  t: TFunction;
  form: FormInstance<SourceFormValues>;
  getPreferredLocalAgentId: () => string;
}

export function useLocalPathTree({
  t,
  form,
  getPreferredLocalAgentId,
}: UseLocalPathTreeParams) {
  const [localPathOptions, setLocalPathOptions] = useState<LocalPathTreeNode[]>([]);
  const [localPathLoading, setLocalPathLoading] = useState(false);
  const localPathRequestSeqRef = useRef(0);
  const localPathSearchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (localPathSearchTimerRef.current) {
        clearTimeout(localPathSearchTimerRef.current);
      }
    },
    [],
  );

  const buildLocalPathHelperOptions = (helperText?: string): LocalPathTreeNode[] => {
    if (!helperText) {
      return [];
    }

    return [
      {
        key: "__scan-local-path-helper__",
        value: "__scan-local-path-helper__",
        title: helperText,
        disabled: true,
        isLeaf: true,
      },
    ];
  };

  const mapLocalPathNodes = (nodes: ScanV2TreeNode[]): LocalPathTreeNode[] =>
    nodes
      .filter((node) => node.is_container || !node.is_document)
      .map((node) => {
        const value =
          getScanTreeNodePath(node) || `${node.key || node.node_ref || node.display_name}`;
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

  const resetLocalPathBrowseOptions = () => {
    localPathRequestSeqRef.current += 1;
    if (localPathSearchTimerRef.current) {
      clearTimeout(localPathSearchTimerRef.current);
      localPathSearchTimerRef.current = null;
    }
    setLocalPathOptions([]);
    setLocalPathLoading(false);
  };

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

    setLocalPathOptions([]);
    setLocalPathLoading(true);
    try {
      const client = dataSourceScanApi;
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

      const nodes = mapLocalPathNodes(response.data.items || []);
      const nextNodes =
        nodes.length > 0
          ? nodes
          : buildLocalPathHelperOptions(t("admin.dataSourceNoLocalDirectories"));
      setLocalPathOptions(nextNodes);
    } catch (error) {
      if (localPathRequestSeqRef.current !== requestSeq) {
        return;
      }
      setLocalPathOptions(
        buildLocalPathHelperOptions(
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
      setLocalPathOptions([]);
      setLocalPathLoading(true);
      localPathSearchTimerRef.current = setTimeout(() => {
        void loadLocalPathOptions("");
      }, 300);
      return;
    }

    setLocalPathOptions([]);
    setLocalPathLoading(true);
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

    if (treeNode.children) {
      setLocalPathOptions((current) =>
        mergeLocalPathChildren(
          current,
          treeNode.key || treeNode.value,
          treeNode.children || [],
        ),
      );
      return;
    }

    const response = await dataSourceScanApi.listBindingTargetChildren({
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
    setLocalPathOptions((current) =>
      mergeLocalPathChildren(current, treeNode.key || treeNode.value, children),
    );
  };

  return {
    localPathOptions,
    localPathLoading,
    loadLocalPathOptions,
    handleSearchLocalPathOptions,
    handleLoadLocalPathChildren,
    resetLocalPathBrowseOptions,
  };
}
