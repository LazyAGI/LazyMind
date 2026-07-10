import type { DataNode } from "antd/es/tree";
import {
  FileOutlined,
  FolderOutlined,
} from "@ant-design/icons";
import type { ReactNode } from "react";
import type { SkillDiffFileRecord, SkillTreeNodeRecord } from "../../skillApi";
import { SKILL_MD_PATH } from "../../skillApi";

export interface SkillTreeFileItem {
  path: string;
  name: string;
  type: "file" | "dir";
  binary: boolean;
  mime: string;
  fileType: string;
}

export const flattenSkillTree = (
  node: SkillTreeNodeRecord,
  parentPath = "",
): SkillTreeFileItem[] => {
  const currentPath = node.path || (parentPath ? `${parentPath}/${node.name}` : node.name);
  if (node.type === "dir") {
    const children = node.children || [];
    return children.flatMap((child) => flattenSkillTree(child, currentPath));
  }

  return [
    {
      path: currentPath,
      name: node.name,
      type: "file",
      binary: node.binary,
      mime: node.mime,
      fileType: node.fileType,
    },
  ];
};

export const buildDiffStatusMap = (files: SkillDiffFileRecord[]) => {
  const map = new Map<string, string>();
  files.forEach((file) => {
    if (file.path) {
      map.set(file.path, file.status);
    }
  });
  return map;
};

export const isTextSkillFile = (item: SkillTreeFileItem) => {
  if (item.binary) {
    return false;
  }
  const ext = item.name.split(".").pop()?.toLowerCase() || "";
  return ["md", "markdown", "txt", "json", "yaml", "yml", "py", "sh", "js", "ts"].includes(ext);
};

export const isMarkdownSkillFile = (item: SkillTreeFileItem) => {
  const ext = item.name.split(".").pop()?.toLowerCase() || "";
  return ext === "md" || ext === "markdown" || item.mime.includes("markdown");
};

export const pickDefaultFilePath = (files: SkillTreeFileItem[]) => {
  if (files.some((item) => item.path === SKILL_MD_PATH)) {
    return SKILL_MD_PATH;
  }
  return files.find((item) => item.type === "file")?.path || "";
};

const statusClassMap: Record<string, string> = {
  added: "is-added",
  modified: "is-modified",
  deleted: "is-deleted",
  renamed: "is-modified",
};

export const getDiffStatusClass = (status?: string) =>
  status ? statusClassMap[status] || "" : "";

export const buildAntTreeData = (
  node: SkillTreeNodeRecord,
  diffStatusMap: Map<string, string>,
  renderTitle?: (item: SkillTreeFileItem, status?: string) => ReactNode,
): DataNode[] => {
  const children = node.children || [];
  return children.map((child) => {
    const path = child.path || child.name;
    const isDir = child.type === "dir";
    const status = !isDir ? diffStatusMap.get(path) : undefined;
    const fileItem: SkillTreeFileItem = {
      path,
      name: child.name,
      type: isDir ? "dir" : "file",
      binary: child.binary,
      mime: child.mime,
      fileType: child.fileType,
    };

    return {
      key: path,
      title: renderTitle ? renderTitle(fileItem, status) : child.name,
      icon: isDir ? <FolderOutlined /> : <FileOutlined />,
      selectable: !isDir,
      isLeaf: !isDir,
      className: getDiffStatusClass(status),
      children: isDir ? buildAntTreeData(child, diffStatusMap, renderTitle) : undefined,
    };
  });
};

export const collectChangedFilePaths = (files: SkillDiffFileRecord[]) =>
  files
    .filter((file) => file.status && file.status !== "unchanged")
    .map((file) => file.path);

export const collectSkillTreeDirectories = (node: SkillTreeNodeRecord): string[] => {
  const result: string[] = [];

  const walk = (current: SkillTreeNodeRecord) => {
    if (current.type !== "dir") {
      return;
    }
    const path = (current.path || "").replace(/^\/+/, "");
    if (path) {
      result.push(path);
    }
    (current.children || []).forEach(walk);
  };

  walk(node);
  return result.sort((left, right) => left.localeCompare(right));
};

export const resolveParentPathFromSelection = (selectedPath: string) => {
  if (!selectedPath) {
    return "";
  }
  const normalized = selectedPath.replace(/^\/+/, "");
  const lastSlash = normalized.lastIndexOf("/");
  return lastSlash >= 0 ? normalized.slice(0, lastSlash) : "";
};

export const buildSkillItemPath = (parentPath: string, name: string) => {
  const trimmedName = name.trim().replace(/^\/+|\/+$/g, "");
  if (!trimmedName) {
    return "";
  }
  const normalizedParent = parentPath.trim().replace(/^\/+|\/+$/g, "");
  return normalizedParent ? `${normalizedParent}/${trimmedName}` : trimmedName;
};
