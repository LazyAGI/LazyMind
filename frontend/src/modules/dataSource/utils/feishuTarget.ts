import type { DataNode } from "antd/es/tree";
import type {
  FeishuTargetType,
  NotionTargetType,
  SourceFormValues,
  SourceType,
} from "../constants/types";
import { getScanBindingTarget, type ScanV2Binding } from "./scanAccessors";

export const FEISHU_MANUAL_TARGET_VALUE_PREFIX = "__scan-feishu-manual-target__";
export const FEISHU_DRIVE_ROOT_REF = "feishu:drive:root";
export const FEISHU_WIKI_SPACES_REF = "feishu:wiki:spaces";

export type FeishuManualTargetKind = "wiki" | "drive";

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

export function buildManualFeishuTargetValue(
  kind: FeishuManualTargetKind,
  targetRef: string,
) {
  return `${FEISHU_MANUAL_TARGET_VALUE_PREFIX}:${kind}:${encodeURIComponent(targetRef)}`;
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

  const kind = rawKind as FeishuManualTargetKind;
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

export function collectManualFeishuTargetTypes(value?: SourceFormValues["target"]) {
  const values = Array.isArray(value) ? value : value ? [value] : [];
  const targetTypes = new Map<string, FeishuTargetType>();

  values.forEach((item) => {
    const parsed = parseManualFeishuTargetValue(`${item || ""}`);
    if (parsed?.targetType) {
      targetTypes.set(parsed.targetRef, parsed.targetType);
    }
  });

  return targetTypes;
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

export function isFeishuRootTargetNode(node: FeishuTargetTreeNode) {
  const ref = `${node.targetRef || node.value || node.key || ""}`.trim().toLowerCase();
  return ref === FEISHU_DRIVE_ROOT_REF || ref === FEISHU_WIKI_SPACES_REF;
}

export function buildFeishuRootTargetNodes(): FeishuTargetTreeNode[] {
  return [
    {
      key: FEISHU_DRIVE_ROOT_REF,
      value: FEISHU_DRIVE_ROOT_REF,
      title: "Drive",
      isLeaf: false,
      targetRef: FEISHU_DRIVE_ROOT_REF,
      targetType: "drive_folder",
    },
    {
      key: FEISHU_WIKI_SPACES_REF,
      value: FEISHU_WIKI_SPACES_REF,
      title: "Wiki",
      isLeaf: false,
      targetRef: FEISHU_WIKI_SPACES_REF,
      targetType: "wiki_space",
    },
  ];
}

export function mergeFeishuTargetSearchResults(
  rootNodes: FeishuTargetTreeNode[],
  searchNodes: FeishuTargetTreeNode[],
): FeishuTargetTreeNode[] {
  const rootRefs = new Set(
    rootNodes.map((node) => `${node.targetRef || node.value || ""}`.trim().toLowerCase()),
  );

  const filteredSearchNodes = searchNodes.filter((node) => {
    if (isFeishuRootTargetNode(node)) {
      return false;
    }
    const ref = `${node.targetRef || node.value || ""}`.trim().toLowerCase();
    return !rootRefs.has(ref);
  });

  return [...rootNodes, ...filteredSearchNodes];
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
