import { BASE_URL } from "@/components/request";
import { type DiffArtifactFile, type DiffFileTreeNode, type ParsedDiffFile } from "./types";
import { getNumberField, getStringField, isRecord } from "./fields";

export function buildCoreDownloadUrl(pathValue: string | undefined) {
  if (!pathValue) {
    return "";
  }

  const normalizedPath = pathValue.trim().replace(/^\/+/, "");
  if (!normalizedPath) {
    return "";
  }
  if (/^https?:\/\//i.test(normalizedPath)) {
    return normalizedPath;
  }

  const baseOrigin = BASE_URL || (typeof window !== "undefined" ? window.location.origin : "");
  if (!baseOrigin) {
    return "";
  }

  const corePath = normalizedPath.startsWith("api/core/")
    ? `/${normalizedPath}`
    : `/api/core/${normalizedPath}`;
  return new URL(corePath, baseOrigin).toString();
}

export function getDiffArtifactFiles(value: unknown): DiffArtifactFile[] {
  if (Array.isArray(value)) {
    return value.flatMap((item) => getDiffArtifactFiles(item));
  }
  if (!isRecord(value)) {
    return [];
  }

  const filesValue = value.files;
  if (Array.isArray(filesValue)) {
    return filesValue
      .filter((item): item is Record<string, unknown> => isRecord(item))
      .map((item, index) => {
        const path = getStringField(item, ["path", "file_path", "relative_path"]) || `unknown-file-${index + 1}`;
        const diffPath = getStringField(item, ["diff_path", "diff_artifact", "artifact_path", "stored_path"]) || "";
        return {
          path,
          diffPath,
          additions: getNumberField(item, ["additions"]),
          deletions: getNumberField(item, ["deletions"]),
          changeKind: getStringField(item, ["change_kind"]),
        };
      })
      .filter((item) => item.diffPath);
  }

  const directDiffPath = getStringField(value, ["diff_path", "diff_artifact", "artifact_path"]);
  if (directDiffPath) {
    return [
      {
        path: getStringField(value, ["path", "file_path", "relative_path"]) || "code-diff.diff",
        diffPath: directDiffPath,
        additions: getNumberField(value, ["additions"]),
        deletions: getNumberField(value, ["deletions"]),
        changeKind: getStringField(value, ["change_kind"]),
      },
    ];
  }

  for (const key of ["data", "result", "payload"]) {
    const nestedFiles = getDiffArtifactFiles(value[key]);
    if (nestedFiles.length > 0) {
      return nestedFiles;
    }
  }

  return [];
}

export function normalizeFetchedDiffArtifact(file: DiffArtifactFile, content: string) {
  const trimmedContent = content.trimEnd();
  if (!trimmedContent) {
    return "";
  }

  if (trimmedContent.includes("diff --git ")) {
    return trimmedContent;
  }

  const lines = trimmedContent.split("\n");
  const hasFileHeaders = lines.some((line) => line.startsWith("--- ")) && lines.some((line) => line.startsWith("+++ "));
  const diffHeader = `diff --git a/${file.path} b/${file.path}`;
  if (hasFileHeaders) {
    return [diffHeader, trimmedContent].join("\n");
  }

  return [diffHeader, `--- a/${file.path}`, `+++ b/${file.path}`, trimmedContent].join("\n");
}

export function getDownloadFileName(downloadUrl: string, fallbackFileName: string) {
  if (!downloadUrl) {
    return fallbackFileName;
  }

  const sanitizedUrl = downloadUrl.split("?")[0]?.split("#")[0] || "";
  const fileName = sanitizedUrl.split("/").filter(Boolean).pop();
  return fileName || fallbackFileName;
}

export function triggerBrowserDownload(downloadUrl: string, fileName: string) {
  const anchor = document.createElement("a");
  anchor.href = downloadUrl;
  anchor.download = fileName;
  anchor.target = "_blank";
  anchor.rel = "noopener noreferrer";
  document.body.appendChild(anchor);
  anchor.click();
  document.body.removeChild(anchor);
}

export function getDiffLineType(line: string) {
  if (line.startsWith("+++ ") || line.startsWith("--- ") || line.startsWith("diff --git") || line.startsWith("index ")) {
    return "meta";
  }
  if (line.startsWith("@@")) {
    return "hunk";
  }
  if (line.startsWith("+")) {
    return "add";
  }
  if (line.startsWith("-")) {
    return "remove";
  }
  return "context";
}

export function normalizeDiffPath(path: string) {
  const cleaned = path.replace(/^([ab])\//, "");
  const lazyMindIndex = cleaned.indexOf("LazyMind/");
  if (lazyMindIndex >= 0) {
    return cleaned.slice(lazyMindIndex + "LazyMind/".length);
  }
  return cleaned;
}

export function parseUnifiedDiff(diffText: string): ParsedDiffFile[] {
  const lines = diffText.split("\n");
  const files: ParsedDiffFile[] = [];
  let currentFile: ParsedDiffFile | null = null;
  let fileIndex = 0;

  const pushCurrent = () => {
    if (currentFile) {
      files.push(currentFile);
      currentFile = null;
    }
  };

  for (const line of lines) {
    if (line.startsWith("diff --git ")) {
      pushCurrent();
      fileIndex += 1;
      const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
      const fromPath = match?.[1] || "";
      const toPath = match?.[2] || fromPath || "unknown-file";
      currentFile = {
        id: `diff-file-${fileIndex}`,
        fromPath,
        toPath,
        displayPath: normalizeDiffPath(toPath),
        lines: [line],
        additions: 0,
        deletions: 0,
      };
      continue;
    }

    if (!currentFile) {
      currentFile = {
        id: "diff-file-fallback",
        fromPath: "unknown-file",
        toPath: "unknown-file",
        displayPath: "unknown-file",
        lines: [],
        additions: 0,
        deletions: 0,
      };
    }

    currentFile.lines.push(line);
    if (line.startsWith("+") && !line.startsWith("+++")) {
      currentFile.additions += 1;
    }
    if (line.startsWith("-") && !line.startsWith("---")) {
      currentFile.deletions += 1;
    }
  }

  pushCurrent();
  return files;
}

export function buildDiffFileTree(files: ParsedDiffFile[]): DiffFileTreeNode[] {
  const tree: DiffFileTreeNode[] = [];

  const ensureDirNode = (nodes: DiffFileTreeNode[], name: string, path: string) => {
    let dirNode = nodes.find((node) => node.nodeType === "dir" && node.path === path);
    if (!dirNode) {
      dirNode = {
        name,
        path,
        nodeType: "dir",
        children: [],
      };
      nodes.push(dirNode);
    }
    return dirNode;
  };

  for (const file of files) {
    const segments = file.displayPath.split("/").filter(Boolean);
    let currentNodes = tree;
    let currentPath = "";

    segments.forEach((segment, index) => {
      currentPath = currentPath ? `${currentPath}/${segment}` : segment;
      const isLeafFile = index === segments.length - 1;

      if (isLeafFile) {
        const exists = currentNodes.some(
          (node) => node.nodeType === "file" && node.path === currentPath && node.fileId === file.id,
        );
        if (!exists) {
          currentNodes.push({
            name: segment,
            path: currentPath,
            nodeType: "file",
            fileId: file.id,
            children: [],
          });
        }
      } else {
        const dirNode = ensureDirNode(currentNodes, segment, currentPath);
        currentNodes = dirNode.children;
      }
    });
  }

  const sortNodes = (nodes: DiffFileTreeNode[]) => {
    nodes.sort((a, b) => {
      if (a.nodeType !== b.nodeType) {
        return a.nodeType === "dir" ? -1 : 1;
      }
      return a.name.localeCompare(b.name, "zh-CN", { numeric: true });
    });
    nodes.forEach((node) => sortNodes(node.children));
  };
  sortNodes(tree);
  return tree;
}
