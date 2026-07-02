import type { DataNode } from "antd/es/tree";
import type { DocumentStatusRow } from "../constants/types";
import type { ScanV2TreeNode } from "./scanAccessors";
import { normalizeDataSourceFileUpdateState } from "./status";

export type SyncTreeDataNode = DataNode & {
  treeKey?: string;
  objectKey?: string;
  nodeRef?: string;
  childrenLoaded?: boolean;
};

export type SyncGenerateScope = {
  key?: string;
  object_key?: string;
  node_ref?: string;
  path?: string;
  is_document?: boolean;
  is_container?: boolean;
};

export function isSelectableScanTreeDocument(node: ScanV2TreeNode) {
  return node.selectable !== false && node.is_document === true;
}

export function getScanTreeNodeKey(node: ScanV2TreeNode) {
  return `${node.object_key || node.key}`;
}

export function getScanTreeNodeParentKey(node: ScanV2TreeNode) {
  return `${node.parent_key || ""}`.trim();
}

export function collectScanTreeFileKeys(nodes: ScanV2TreeNode[]): string[] {
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

export function collectScanTreeNodesByKey(nodes: ScanV2TreeNode[]) {
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

export function getScanTreeNodePage(payload: unknown) {
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

export function getScanTreeNodeMergeKeys(node: ScanV2TreeNode) {
  return [
    getScanTreeNodeKey(node),
    node.key,
    node.object_key,
    node.node_ref,
  ]
    .map((key) => `${key || ""}`.trim())
    .filter(Boolean);
}

export function normalizeLazyScanTreeNodes(nodes: ScanV2TreeNode[]) {
  return nodes.map((node) => {
    const nextNode = { ...node };
    delete nextNode.children;
    return nextNode;
  });
}

export function filterScanTreeChildren(parentKey: string, children: ScanV2TreeNode[]) {
  return children.filter((child) => {
    if (getScanTreeNodeMergeKeys(child).includes(parentKey)) {
      return false;
    }
    const childParentKey = `${child.parent_key || ""}`.trim();
    return !childParentKey || childParentKey === parentKey;
  });
}

export function buildSyncGenerateScopes(
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

export function mergeScanTreeChildren(
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

export function getTreeNodeUpdateState(node: ScanV2TreeNode) {
  return normalizeDataSourceFileUpdateState(
    node.update_type || node.source_state,
    node.has_update ?? node.source_state !== "UNCHANGED",
  );
}

export function shouldPollDocumentStatus(items: DocumentStatusRow[]) {
  return items.some(
    (item) =>
      item.parseStatus === "reindexing" ||
      item.parseStatus === "downloading" ||
      item.syncState === "PENDING" ||
      item.syncState === "RUNNING",
  );
}
