import type { DataNode } from "antd/es/tree";
import type {
  FeishuTargetType,
  NotionTargetType,
  SourceFormValues,
  SourceType,
} from "../constants/types";
import {
  getScanBindingTarget,
  getScanTreeNodePath,
  type ScanV2Binding,
  type ScanV2TreeNode,
} from "./scanAccessors";

export const FEISHU_MANUAL_TARGET_VALUE_PREFIX = "__scan-feishu-manual-target__";

export type FeishuTargetTreeNode = DataNode & {
  value: string;
  nodeRef?: string;
  targetRef?: string;
  targetType?: FeishuTargetType;
  children?: FeishuTargetTreeNode[];
};

export type LocalPathTreeNode = DataNode & {
  value: string;
  nodeRef?: string;
  targetRef?: string;
  childrenLoaded?: boolean;
  children?: LocalPathTreeNode[];
};

export function normalizeFeishuTargetType(
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

export function toScanFeishuTargetType(targetType: FeishuTargetType) {
  return targetType === "wiki_space" ? "wiki_node" : targetType;
}

export function toUiFeishuTargetType(targetType?: string): FeishuTargetType | undefined {
  return normalizeFeishuTargetType(targetType);
}

export function parseManualFeishuTargetValue(value: string) {
  const normalizedValue = value.trim();
  if (!normalizedValue.startsWith(`${FEISHU_MANUAL_TARGET_VALUE_PREFIX}:`)) {
    return null;
  }

  const parts = normalizedValue.split(":");
  const rawKind = parts[1] || "";
  const encodedTargetRef = parts.slice(2).join(":");
  if (!["current", "wiki", "drive"].includes(rawKind)) {
    return null;
  }

  let targetRef = encodedTargetRef;
  try {
    targetRef = decodeURIComponent(encodedTargetRef);
  } catch {
  }

  const normalizedTargetRef = targetRef.trim();
  if (!normalizedTargetRef) {
    return null;
  }

  const kind = rawKind as "wiki" | "drive";
  const targetType: FeishuTargetType | undefined =
    kind === "wiki" ? "wiki_space" : kind === "drive" ? "drive_folder" : undefined;
  return {
    kind,
    targetRef: normalizedTargetRef,
    targetType,
  };
}

export function normalizeNotionTargetType(value?: string): NotionTargetType | undefined {
  const normalized = `${value || ""}`.trim().toLowerCase();
  if (normalized === "database" || normalized === "notion_database") {
    return "database";
  }
  if (normalized === "page" || normalized === "notion_page") {
    return "page";
  }
  return undefined;
}

export function collectFeishuTargetTypes(
  nodes: FeishuTargetTreeNode[],
  inheritedTargetType?: FeishuTargetType,
  targetTypes = new Map<string, FeishuTargetType>(),
) {
  nodes.forEach((node) => {
    const value = `${node.value || ""}`.trim();
    const targetRef = `${node.targetRef || node.value || ""}`.trim();
    const nodeRef = `${node.nodeRef || ""}`.trim();
    const targetType =
      normalizeFeishuTargetType(node.targetType, `${targetRef || nodeRef || value}`) ||
      inheritedTargetType;

    if (targetType) {
      const refs = value.startsWith(FEISHU_MANUAL_TARGET_VALUE_PREFIX)
        ? [value]
        : [targetRef, nodeRef, value];
      refs.filter(Boolean).forEach((ref) => {
        targetTypes.set(ref, targetType);
      });
    }

    if (node.children) {
      collectFeishuTargetTypes(node.children, targetType, targetTypes);
    }
  });

  return targetTypes;
}

export function collectFeishuTargetRefs(
  nodes: FeishuTargetTreeNode[],
  targetRefs = new Map<string, string>(),
) {
  nodes.forEach((node) => {
    const value = `${node.value || ""}`.trim();
    const targetRef = `${node.targetRef || node.value || ""}`.trim();
    const nodeRef = `${node.nodeRef || ""}`.trim();

    if (targetRef) {
      [targetRef, nodeRef, value].filter(Boolean).forEach((ref) => {
        targetRefs.set(ref, targetRef);
      });
    }

    if (node.children) {
      collectFeishuTargetRefs(node.children, targetRefs);
    }
  });

  return targetRefs;
}

export function normalizeFeishuTargetRefs(value?: SourceFormValues["target"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values
    .map((item) => {
      const normalizedValue = `${item || ""}`.trim();
      return parseManualFeishuTargetValue(normalizedValue)?.targetRef || normalizedValue;
    })
    .filter(Boolean);
}

export function normalizeCloudTargetRefs(value?: SourceFormValues["target"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values
    .flatMap((item) => `${item || ""}`.split(/\n+/))
    .map((item) => item.trim())
    .filter(Boolean);
}

export function normalizeLocalPathRefs(value?: SourceFormValues["path"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values.map((item) => `${item || ""}`.trim()).filter(Boolean);
}

export function hasFeishuTargetTypes(targetTypes?: Record<string, unknown>) {
  if (!targetTypes) {
    return false;
  }
  return Object.values(targetTypes).some((targetType) =>
    Boolean(normalizeFeishuTargetType(`${targetType || ""}`)),
  );
}

export function getFeishuBindingTargetTypes(bindings: ScanV2Binding[]) {
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

export function normalizeFeishuTargetTypeRecord(targetTypes?: Record<string, unknown>) {
  if (!targetTypes) {
    return undefined;
  }

  const normalizedTypes: Record<string, FeishuTargetType> = {};
  Object.entries(targetTypes).forEach(([targetRef, targetType]) => {
    const normalizedTargetRef = `${targetRef || ""}`.trim();
    const normalizedTargetType = normalizeFeishuTargetType(
      `${targetType || ""}`,
      normalizedTargetRef,
    );
    if (normalizedTargetRef && normalizedTargetType) {
      normalizedTypes[normalizedTargetRef] = normalizedTargetType;
    }
  });

  return hasFeishuTargetTypes(normalizedTypes) ? normalizedTypes : undefined;
}

function getFeishuScanNodeIdentity(node: ScanV2TreeNode) {
  return `${node.object_key || node.key || ""}`.trim();
}

export function mapFeishuScanNodeToTreeNode(
  node: ScanV2TreeNode,
  inheritedTargetType?: FeishuTargetType,
  children?: FeishuTargetTreeNode[],
): FeishuTargetTreeNode | null {
  const value =
    getScanTreeNodePath(node) || `${node.key || node.node_ref || node.display_name}`;
  const normalizedValue = `${value || ""}`.trim();
  if (!normalizedValue) {
    return null;
  }

  const title = node.display_name || node.title || node.object_key || normalizedValue;
  const targetRef = node.target_ref || normalizedValue;
  const nodeRef = node.node_ref;
  const targetType =
    normalizeFeishuTargetType(node.target_type, `${targetRef || nodeRef || normalizedValue}`) ||
    inheritedTargetType;

  return {
    key: normalizedValue,
    value: normalizedValue,
    title,
    isLeaf: children?.length ? false : !node.has_children,
    selectable: node.selectable !== false,
    disabled: node.selectable === false,
    nodeRef,
    targetRef,
    targetType,
    children,
  };
}

export function mapFeishuScanNodesToTreeNodes(
  nodes: ScanV2TreeNode[],
  inheritedTargetType?: FeishuTargetType,
): FeishuTargetTreeNode[] {
  return nodes
    .map((node) => mapFeishuScanNodeToTreeNode(node, inheritedTargetType))
    .filter((node): node is FeishuTargetTreeNode => Boolean(node));
}

export function buildFeishuTargetTreeFromScanNodes(
  nodes: ScanV2TreeNode[],
  inheritedTargetType?: FeishuTargetType,
): FeishuTargetTreeNode[] {
  if (nodes.length === 0) {
    return [];
  }

  const hasNestedChildren = nodes.some(
    (node) => Array.isArray(node.children) && node.children.length > 0,
  );
  if (hasNestedChildren) {
    const mapNested = (
      items: ScanV2TreeNode[],
      inherited?: FeishuTargetType,
    ): FeishuTargetTreeNode[] =>
      items
        .map((item) => {
          const targetRef = item.target_ref || getScanTreeNodePath(item);
          const nodeRef = item.node_ref;
          const targetType =
            normalizeFeishuTargetType(item.target_type, `${targetRef || nodeRef || item.key || ""}`) ||
            inherited;
          const children = item.children?.length
            ? mapNested(item.children, targetType)
            : undefined;
          return mapFeishuScanNodeToTreeNode(item, inherited, children);
        })
        .filter((node): node is FeishuTargetTreeNode => Boolean(node));

    return mapNested(nodes, inheritedTargetType);
  }

  const nodeByIdentity = new Map<string, FeishuTargetTreeNode>();
  const childIdentitiesByParent = new Map<string, string[]>();

  nodes.forEach((node) => {
    const mapped = mapFeishuScanNodeToTreeNode(node, inheritedTargetType);
    if (!mapped) {
      return;
    }

    const identity = getFeishuScanNodeIdentity(node);
    if (!identity) {
      return;
    }

    nodeByIdentity.set(identity, mapped);
    if (node.key && node.key !== identity) {
      nodeByIdentity.set(node.key, mapped);
    }

    const parentKey = `${node.parent_key || ""}`.trim();
    if (parentKey) {
      const siblings = childIdentitiesByParent.get(parentKey) || [];
      if (!siblings.includes(identity)) {
        siblings.push(identity);
      }
      childIdentitiesByParent.set(parentKey, siblings);
    }
  });

  childIdentitiesByParent.forEach((childIdentities, parentKey) => {
    const parent = nodeByIdentity.get(parentKey);
    if (!parent) {
      return;
    }

    parent.children = childIdentities
      .map((identity) => nodeByIdentity.get(identity))
      .filter((node): node is FeishuTargetTreeNode => Boolean(node));
    if (parent.children.length > 0) {
      parent.isLeaf = false;
    }
  });

  const roots: FeishuTargetTreeNode[] = [];
  const rootSet = new Set<FeishuTargetTreeNode>();
  nodes.forEach((node) => {
    const identity = getFeishuScanNodeIdentity(node);
    if (!identity) {
      return;
    }

    const mapped = nodeByIdentity.get(identity);
    if (!mapped || rootSet.has(mapped)) {
      return;
    }

    const parentKey = `${node.parent_key || ""}`.trim();
    if (!parentKey || !nodeByIdentity.has(parentKey)) {
      roots.push(mapped);
      rootSet.add(mapped);
    }
  });

  if (roots.length > 0) {
    return roots;
  }

  return Array.from(new Set(nodeByIdentity.values()));
}

export function resolveSourceTypeFromValues(
  fallbackType: SourceType | null,
  values: SourceFormValues,
): SourceType | null {
  const localPaths = normalizeLocalPathRefs(values.path);
  const feishuTargets = normalizeFeishuTargetRefs(values.target);
  const notionTargets = normalizeCloudTargetRefs(values.target);
  if (localPaths.length > 0 && feishuTargets.length === 0) {
    return "local";
  }
  if (fallbackType === "notion" && notionTargets.length > 0 && localPaths.length === 0) {
    return "notion";
  }
  if (feishuTargets.length > 0 && localPaths.length === 0) {
    return "feishu";
  }
  return fallbackType;
}
