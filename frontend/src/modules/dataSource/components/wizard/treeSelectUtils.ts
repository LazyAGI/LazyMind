import type { ReactNode } from "react";
import type { DataNode } from "antd/es/tree";

export type CollapsibleTreeNode = DataNode & {
  value?: string | number;
  children?: CollapsibleTreeNode[];
};

export type LocalPathSelectOption = DataNode & {
  value: string;
  nodeRef?: string;
  targetRef?: string;
  children?: LocalPathSelectOption[];
};

export function normalizeTreeSelectValues(value: unknown) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  return values.map((item) => `${item || ""}`.trim()).filter(Boolean);
}

function collectDescendantValues(
  nodes: CollapsibleTreeNode[] | undefined,
  descendantValues: Set<string>,
) {
  nodes?.forEach((node) => {
    const nodeValue = `${node.value || ""}`.trim();
    if (nodeValue) {
      descendantValues.add(nodeValue);
    }
    collectDescendantValues(node.children, descendantValues);
  });
}

export function collapseSelectedTreeValues(
  value: unknown,
  treeData: CollapsibleTreeNode[],
) {
  const values = normalizeTreeSelectValues(value);
  const selectedValues = new Set(values);
  const descendantValues = new Set<string>();

  const visit = (nodes: CollapsibleTreeNode[]) => {
    nodes.forEach((node) => {
      const nodeValue = `${node.value || ""}`.trim();
      if (nodeValue && selectedValues.has(nodeValue)) {
        collectDescendantValues(node.children, descendantValues);
        return;
      }
      if (node.children) {
        visit(node.children);
      }
    });
  };

  visit(treeData);
  return values.filter((item) => !descendantValues.has(item));
}

function getTreeNodeTitleText(node: CollapsibleTreeNode) {
  const title = node.title;
  if (typeof title === "string" || typeof title === "number") {
    return `${title}`.trim();
  }
  return `${node.value || node.key || ""}`.trim();
}

export function buildTreeValuePathMap(treeData: CollapsibleTreeNode[]) {
  const pathMap = new Map<string, string>();

  const visit = (nodes: CollapsibleTreeNode[], parentTitles: string[]) => {
    nodes.forEach((node) => {
      const nodeValue = `${node.value || node.key || ""}`.trim();
      const title = getTreeNodeTitleText(node);
      const nextTitles = title ? [...parentTitles, title] : parentTitles;
      if (nodeValue) {
        pathMap.set(nodeValue, nextTitles.join(" / ") || nodeValue);
      }
      if (node.children) {
        visit(node.children, nextTitles);
      }
    });
  };

  visit(treeData, []);
  return pathMap;
}

export function getTreeSelectLabelText(label: ReactNode) {
  if (typeof label === "string" || typeof label === "number") {
    return `${label}`.trim();
  }
  return "";
}

export function collectTreeExpandableKeys(nodes: CollapsibleTreeNode[]) {
  const keys: Array<string | number> = [];

  const visit = (items: CollapsibleTreeNode[]) => {
    items.forEach((node) => {
      if (!node.children?.length) {
        return;
      }
      const nodeKey = node.key ?? node.value;
      if (nodeKey !== undefined && nodeKey !== null && `${nodeKey}` !== "") {
        keys.push(nodeKey);
      }
      visit(node.children);
    });
  };

  visit(nodes);
  return keys;
}
