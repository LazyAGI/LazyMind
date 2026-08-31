const fs = require("node:fs");
const path = require("node:path");
const { fileURLToPath } = require("node:url");

const stateVersion = 1;
const defaultScanBudget = Object.freeze({
  maxDepth: 5,
  maxEntries: 20_000,
  timeoutMs: 5_000,
});

const recommendationRules = Object.freeze([
  { productId: "cursor_rules", displayName: "Cursor Rules", suffix: [".cursor", "rules"] },
  { productId: "cursor_skills", displayName: "Cursor Skills", suffix: [".cursor", "skills"] },
  { productId: "agent_skills", displayName: "Cursor / Codex Skills", suffix: [".agents", "skills"] },
  { productId: "codex_skills", displayName: "Codex Skills", suffix: [".codex", "skills"] },
  { productId: "feishu_download", displayName: "飞书下载目录", suffix: ["Downloads", "飞书"] },
  { productId: "feishu_download", displayName: "飞书下载目录", suffix: ["Downloads", "Feishu"] },
  { productId: "feishu_download", displayName: "Lark 下载目录", suffix: ["Downloads", "Lark"] },
  { productId: "feishu_documents", displayName: "飞书文档目录", suffix: ["Documents", "飞书"] },
  { productId: "feishu_documents", displayName: "飞书文档目录", suffix: ["Documents", "Feishu"] },
  { productId: "feishu_documents", displayName: "Lark 文档目录", suffix: ["Documents", "Lark"] },
  { productId: "baidu_download", displayName: "百度网盘下载目录", suffix: ["Downloads", "BaiduNetdiskDownload"] },
  { productId: "baidu_download", displayName: "百度网盘下载目录", suffix: ["BaiduNetdiskDownload"] },
  { productId: "baidu_download", displayName: "百度网盘下载目录", suffix: ["BaiduYunDownload"] },
  { productId: "baidu_download", displayName: "百度网盘下载目录", suffix: ["BaiduDownload"] },
]);

const prunedDirectoryNames = new Set([
  "$recycle.bin",
  ".git",
  ".svn",
  "appdata",
  "caches",
  "library",
  "system volume information",
  "trash",
  "__pycache__",
  "build",
  "dist",
  "node_modules",
  "target",
  "tmp",
  "vendor",
]);

const mediaDirectoryNames = new Set([
  "camera roll",
  "movies",
  "music",
  "photos",
  "pictures",
  "saved pictures",
  "videos",
  "图片",
  "影片",
  "影视",
  "照片",
  "视频",
  "音乐",
]);

function emptyAccessState() {
  return {
    version: stateVersion,
    discoveryConsentGranted: false,
    discoveryRoots: [],
    allowedRoots: [],
    updatedAt: "",
  };
}

function pathModuleFor(platform) {
  return platform === "win32" ? path.win32 : path.posix;
}

function pathKey(value, platform) {
  return platform === "win32" ? value.toLowerCase() : value;
}

function cleanAbsolutePath(value, platform = process.platform) {
  const pathModule = pathModuleFor(platform);
  const text = String(value || "").trim();
  if (!text || !pathModule.isAbsolute(text)) {
    return "";
  }
  return pathModule.normalize(text);
}

function containsPath(root, candidate, platform = process.platform) {
  const pathModule = pathModuleFor(platform);
  const cleanRoot = cleanAbsolutePath(root, platform);
  const cleanCandidate = cleanAbsolutePath(candidate, platform);
  if (!cleanRoot || !cleanCandidate) {
    return false;
  }
  const relative = pathModule.relative(cleanRoot, cleanCandidate);
  return relative === "" || (!relative.startsWith("..") && !pathModule.isAbsolute(relative));
}

function collapseRoots(values, platform = process.platform) {
  const unique = new Map();
  for (const value of values || []) {
    const cleaned = cleanAbsolutePath(value, platform);
    if (cleaned) {
      unique.set(pathKey(cleaned, platform), cleaned);
    }
  }
  const roots = [...unique.values()].sort((left, right) => left.length - right.length);
  return roots.filter((candidate, index) =>
    !roots.slice(0, index).some((root) => containsPath(root, candidate, platform)));
}

function isMediaDirectoryName(name) {
  return mediaDirectoryNames.has(String(name || "").trim().toLowerCase());
}

function loadAccessState(statePath, platform = process.platform) {
  try {
    const parsed = JSON.parse(fs.readFileSync(statePath, "utf8"));
    return {
      version: stateVersion,
      discoveryConsentGranted: parsed?.discoveryConsentGranted === true,
      discoveryRoots: collapseRoots(parsed?.discoveryRoots, platform),
      allowedRoots: collapseRoots(parsed?.allowedRoots, platform),
      updatedAt: typeof parsed?.updatedAt === "string" ? parsed.updatedAt : "",
    };
  } catch {
    // Invalid or unreadable state fails closed: no discovery or content roots
    // are restored until the user authorizes them again.
    return emptyAccessState();
  }
}

function saveAccessState(statePath, state, platform = process.platform) {
  const next = {
    version: stateVersion,
    discoveryConsentGranted: state?.discoveryConsentGranted === true,
    discoveryRoots: collapseRoots(state?.discoveryRoots, platform),
    allowedRoots: collapseRoots(state?.allowedRoots, platform),
    updatedAt: new Date().toISOString(),
  };
  fs.mkdirSync(path.dirname(statePath), { recursive: true });
  const temporaryPath = `${statePath}.${process.pid}.tmp`;
  fs.writeFileSync(temporaryPath, `${JSON.stringify(next, null, 2)}\n`, { mode: 0o600 });
  fs.renameSync(temporaryPath, statePath);
  return next;
}

function isRuleMatch(relativeSegments, rule, platform) {
  if (relativeSegments.length < rule.suffix.length) {
    return false;
  }
  const tail = relativeSegments.slice(-rule.suffix.length);
  return tail.every((segment, index) => {
    const expected = rule.suffix[index];
    return platform === "win32"
      ? segment.toLowerCase() === expected.toLowerCase()
      : segment === expected;
  });
}

function recommendationFor(directoryPath, rule) {
  return {
    key: `desktop:${rule.productId}:${directoryPath}`,
    value: directoryPath,
    path: directoryPath,
    title: rule.displayName,
    productId: rule.productId,
    source: "desktop_discovery",
  };
}

function recommendationsForExactFolders(values, platform = process.platform) {
  const pathModule = pathModuleFor(platform);
  const found = new Map();
  for (const directoryPath of collapseRoots(values, platform)) {
    const parsed = pathModule.parse(directoryPath);
    const segments = directoryPath
      .slice(parsed.root.length)
      .split(/[\\/]+/u)
      .filter(Boolean);
    for (const rule of recommendationRules) {
      if (!isRuleMatch(segments, rule, platform)) {
        continue;
      }
      found.set(pathKey(directoryPath, platform), recommendationFor(directoryPath, rule));
      break;
    }
  }
  return [...found.values()].sort((left, right) => left.path.localeCompare(right.path));
}

async function directoryExists(directoryPath, fsPromises) {
  try {
    const info = await fsPromises.stat(directoryPath);
    return info.isDirectory();
  } catch {
    return false;
  }
}

async function discoverRecommendedFolders({
  roots,
  cursorWorkspaceStorageRoots = [],
  excludedRoots = [],
  platform = process.platform,
  fsPromises = fs.promises,
  budget = {},
  now = Date.now,
}) {
  const pathModule = pathModuleFor(platform);
  const scanBudget = { ...defaultScanBudget, ...budget };
  const startedAt = now();
  const cleanRoots = collapseRoots(roots, platform);
  const cleanExcludedRoots = collapseRoots(excludedRoots, platform);
  const found = new Map();
  let scannedEntries = 0;
  let truncated = false;
  let stoppedReason = "";

  const addRecommendation = (directoryPath, rule) => {
    const key = pathKey(directoryPath, platform);
    if (!found.has(key)) {
      found.set(key, recommendationFor(directoryPath, rule));
    }
  };
  const isExcluded = (directoryPath) =>
    cleanExcludedRoots.some((root) => containsPath(root, directoryPath, platform));

  // Check deterministic default locations first so a large sibling directory
  // cannot consume the scan budget before common tool folders are reached.
  for (const root of cleanRoots) {
    for (const rule of recommendationRules) {
      const candidate = pathModule.join(root, ...rule.suffix);
      if (!isExcluded(candidate) && await directoryExists(candidate, fsPromises)) {
        addRecommendation(candidate, rule);
      }
    }
  }

  // Cursor persists exact local workspace URIs in small workspace.json files.
  // Reading those records after discovery consent lets us verify project-level
  // .cursor folders without recursively walking deeper through the whole home.
  const cursorRules = recommendationRules.filter((rule) =>
    rule.productId === "cursor_rules" || rule.productId === "cursor_skills");
  for (const storageRoot of collapseRoots(cursorWorkspaceStorageRoots, platform)) {
    let entries;
    try {
      entries = await fsPromises.readdir(storageRoot, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const entry of entries.slice(0, 500)) {
      if (!entry.isDirectory()) {
        continue;
      }
      try {
        const raw = await fsPromises.readFile(
          pathModule.join(storageRoot, entry.name, "workspace.json"),
          "utf8",
        );
        const folderURI = String(JSON.parse(raw)?.folder || "");
        if (!folderURI.startsWith("file://")) {
          continue;
        }
        const workspacePath = fileURLToPath(folderURI);
        if (
          isExcluded(workspacePath) ||
          !cleanRoots.some((root) => containsPath(root, workspacePath, platform))
        ) {
          continue;
        }
        for (const rule of cursorRules) {
          const candidate = pathModule.join(workspacePath, ...rule.suffix);
          if (await directoryExists(candidate, fsPromises)) {
            addRecommendation(candidate, rule);
          }
        }
      } catch {
        // Deleted, remote, or malformed workspace records are ignored.
      }
    }
  }

  const queue = cleanRoots
    .filter((root) => !isExcluded(root))
    .map((root) => ({ directoryPath: root, depth: 0, segments: [] }));
  while (queue.length > 0) {
    if (now() - startedAt >= scanBudget.timeoutMs) {
      truncated = true;
      stoppedReason = "timeout";
      break;
    }
    if (scannedEntries >= scanBudget.maxEntries) {
      truncated = true;
      stoppedReason = "entry_limit";
      break;
    }

    const current = queue.shift();
    if (!current || current.depth >= scanBudget.maxDepth) {
      continue;
    }

    let entries;
    try {
      entries = await fsPromises.readdir(current.directoryPath, { withFileTypes: true });
    } catch {
      continue;
    }

    for (const entry of entries) {
      scannedEntries += 1;
      if (scannedEntries > scanBudget.maxEntries) {
        truncated = true;
        stoppedReason = "entry_limit";
        break;
      }
      if (!entry.isDirectory() || entry.isSymbolicLink?.()) {
        continue;
      }
      if (prunedDirectoryNames.has(entry.name.toLowerCase()) || isMediaDirectoryName(entry.name)) {
        continue;
      }
      if (entry.name.startsWith(".") && ![".agents", ".codex", ".cursor"].includes(entry.name.toLowerCase())) {
        continue;
      }

      const segments = [...current.segments, entry.name];
      const directoryPath = pathModule.join(current.directoryPath, entry.name);
      if (isExcluded(directoryPath)) {
        continue;
      }
      for (const rule of recommendationRules) {
        if (isRuleMatch(segments, rule, platform)) {
          addRecommendation(directoryPath, rule);
        }
      }
      queue.push({ directoryPath, depth: current.depth + 1, segments });
    }
  }

  return {
    items: [...found.values()].sort((left, right) => left.path.localeCompare(right.path)),
    scannedEntries,
    truncated,
    stoppedReason,
    durationMs: Math.max(0, now() - startedAt),
  };
}

function resolveExistingDirectories(values, platform = process.platform) {
  const resolved = [];
  for (const value of values || []) {
    const cleaned = cleanAbsolutePath(value, platform);
    if (!cleaned) {
      throw new Error(`Folder path must be absolute: ${value}`);
    }
    let canonical = cleaned;
    try {
      canonical = fs.realpathSync.native(cleaned);
    } catch (error) {
      throw new Error(`Folder is not accessible: ${cleaned} (${error.message})`);
    }
    const info = fs.statSync(canonical);
    if (!info.isDirectory()) {
      throw new Error(`Path is not a folder: ${canonical}`);
    }
    resolved.push(canonical);
  }
  return collapseRoots(resolved, platform);
}

module.exports = {
  cleanAbsolutePath,
  collapseRoots,
  containsPath,
  defaultScanBudget,
  discoverRecommendedFolders,
  emptyAccessState,
  loadAccessState,
  isMediaDirectoryName,
  recommendationRules,
  recommendationsForExactFolders,
  resolveExistingDirectories,
  saveAccessState,
};
