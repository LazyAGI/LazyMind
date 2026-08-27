import { useEffect, useRef, useState } from "react";
import type { FormInstance, TreeSelectProps } from "antd";
import type { TFunction } from "i18next";
import { getLocalizedErrorMessage } from "@/components/request";
import {
  chooseLocalDiscoveryRoots,
  discoverLocalFolders,
  localFolderAccessStatus,
  type DesktopLocalFolderAccessState,
} from "@/runtime/desktopBridge";
import { isDesktopRuntime } from "@/runtime/mode";
import { dataSourceScanApi } from "../api/clients";
import { listLocalPathRecommendations } from "../api/localPathRecommendations";
import type { SourceFormValues } from "../constants/types";
import { getScanTreeNodePath, type ScanV2TreeNode } from "../utils/scanAccessors";
import type {
  LocalPathRecommendation,
  LocalPathTreeNode,
} from "../utils/feishuTarget";

interface UseLocalPathTreeParams {
  t: TFunction;
  form: FormInstance<SourceFormValues>;
  getPreferredLocalAgentId: () => string;
  recommendationsEnabled?: boolean;
  autoPromptDiscovery?: boolean;
}

export function useLocalPathTree({
  t,
  form,
  getPreferredLocalAgentId,
  recommendationsEnabled = false,
  autoPromptDiscovery = true,
}: UseLocalPathTreeParams) {
  const [localPathOptions, setLocalPathOptions] = useState<LocalPathTreeNode[]>([]);
  const [localPathLoading, setLocalPathLoading] = useState(false);
  const [localPathRecommendations, setLocalPathRecommendations] = useState<
    LocalPathRecommendation[]
  >([]);
  const [localPathRecommendationsLoading, setLocalPathRecommendationsLoading] =
    useState(false);
  const [localPathRecommendationsError, setLocalPathRecommendationsError] =
    useState("");
  const [localDiscoveryAccess, setLocalDiscoveryAccess] =
    useState<DesktopLocalFolderAccessState | null>(null);
  const [localDiscoveryChoosing, setLocalDiscoveryChoosing] = useState(false);
  const localPathRequestSeqRef = useRef(0);
  const recommendationsRequestSeqRef = useRef(0);
  const recommendationsLoadedRef = useRef(false);
  const discoveryPromptedForOpenRef = useRef(false);
  const recommendationsEnabledRef = useRef(recommendationsEnabled);
  const localPathSearchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  recommendationsEnabledRef.current = recommendationsEnabled;

  useEffect(
    () => () => {
      if (localPathSearchTimerRef.current) {
        clearTimeout(localPathSearchTimerRef.current);
      }
    },
    [],
  );

  const loadLocalPathRecommendations = async (forceRefresh = false) => {
    if (!recommendationsEnabledRef.current) {
      return;
    }
    const requestSeq = recommendationsRequestSeqRef.current + 1;
    recommendationsRequestSeqRef.current = requestSeq;
    setLocalPathRecommendationsLoading(true);
    setLocalPathRecommendationsError("");
    const recommendations: LocalPathRecommendation[] = [];
    const errors: unknown[] = [];
    try {
      if (!isDesktopRuntime()) {
        try {
          const response = await listLocalPathRecommendations({
            agent_id: getPreferredLocalAgentId() || undefined,
            force_refresh: forceRefresh,
          });
          const items = (response.data.items || []) as ScanV2TreeNode[];
          items.forEach((item) => {
            const value = getScanTreeNodePath(item);
            if (!value) {
              return;
            }
            const providerPath = `${item.provider_meta?.path || ""}`.trim();
            recommendations.push({
              key: `${item.key || value}`,
              value,
              title: `${item.display_name || item.object_key || value}`,
              path: providerPath || `${item.object_key || value}`,
              source: "server",
            });
          });
        } catch (error) {
          errors.push(error);
        }
      }

      try {
        const access = await localFolderAccessStatus();
        if (recommendationsRequestSeqRef.current !== requestSeq) {
          return;
        }
        setLocalDiscoveryAccess(access);
        if (!autoPromptDiscovery) {
          recommendations.push(...(access?.items || []));
        }
        if (access?.discoveryConsentGranted && access.discoveryRoots.length > 0) {
          const discovery = await discoverLocalFolders();
          if (discovery) {
            if (recommendationsRequestSeqRef.current !== requestSeq) {
              return;
            }
            setLocalDiscoveryAccess(discovery);
            recommendations.push(...(discovery.items || []));
          }
        }
      } catch (error) {
        errors.push(error);
      }

      const merged = new Map<string, LocalPathRecommendation>();
      recommendations.forEach((item) => {
        const key = item.path || item.value;
        if (!merged.has(key)) {
          merged.set(key, item);
        }
      });
      if (recommendationsRequestSeqRef.current !== requestSeq) {
        return;
      }
      setLocalPathRecommendations([...merged.values()]);
      if (merged.size === 0 && errors.length > 0) {
        setLocalPathRecommendationsError(getLocalizedErrorMessage(errors[0]));
      }
      recommendationsLoadedRef.current = true;
    } finally {
      if (recommendationsRequestSeqRef.current === requestSeq) {
        setLocalPathRecommendationsLoading(false);
      }
    }
  };

  const chooseLocalDiscoveryLocations = async () => {
    setLocalDiscoveryChoosing(true);
    setLocalPathRecommendationsError("");
    try {
      const access = await chooseLocalDiscoveryRoots();
      if (!recommendationsEnabledRef.current) {
        return;
      }
      if (access) {
        setLocalDiscoveryAccess(access);
      }
      await loadLocalPathRecommendations(true);
    } catch (error) {
      setLocalPathRecommendationsError(getLocalizedErrorMessage(error));
    } finally {
      setLocalDiscoveryChoosing(false);
    }
  };

  useEffect(() => {
    if (!recommendationsEnabled) {
      recommendationsRequestSeqRef.current += 1;
      recommendationsLoadedRef.current = false;
      discoveryPromptedForOpenRef.current = false;
      setLocalPathRecommendations([]);
      setLocalPathRecommendationsLoading(false);
      setLocalPathRecommendationsError("");
      setLocalDiscoveryAccess(null);
      setLocalDiscoveryChoosing(false);
      return;
    }
    if (!discoveryPromptedForOpenRef.current) {
      discoveryPromptedForOpenRef.current = true;
      if (autoPromptDiscovery) {
        void chooseLocalDiscoveryLocations();
      } else if (!recommendationsLoadedRef.current) {
        void loadLocalPathRecommendations();
      }
    } else if (!recommendationsLoadedRef.current) {
      void loadLocalPathRecommendations();
    }
    // Creating a Desktop source asks for discovery roots once per wizard
    // opening. Editing only loads recommendations until the user explicitly
    // chooses a broader search location. Web runtimes fall back to server data.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoPromptDiscovery, recommendationsEnabled]);

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
        const children = node.children?.length
          ? mapLocalPathNodes(node.children)
          : undefined;
        return {
          key: value,
          value,
          title,
          isLeaf: children?.length ? false : !node.has_children,
          selectable: node.selectable !== false,
          disabled: node.selectable === false,
          nodeRef: node.node_ref,
          targetRef: node.target_ref || value,
          children,
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

      const mappedNodes = mapLocalPathNodes(response.data.items || []);
      const nodes = normalizedPath
        ? mappedNodes.flatMap((node) =>
            node.targetRef === "/" && node.children?.length
              ? node.children
              : [node],
          )
        : mappedNodes;
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
            ? getLocalizedErrorMessage(error)
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
    localPathRecommendations,
    localPathRecommendationsLoading,
    localPathRecommendationsError,
    localDiscoveryAccess,
    localDiscoveryChoosing,
    chooseLocalDiscoveryLocations,
    loadLocalPathRecommendations,
    loadLocalPathOptions,
    handleSearchLocalPathOptions,
    handleLoadLocalPathChildren,
    resetLocalPathBrowseOptions,
  };
}
