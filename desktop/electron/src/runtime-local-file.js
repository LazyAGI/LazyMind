const path = require("node:path");

function stripQuery(value) {
  const raw = String(value || "");
  const index = raw.indexOf("?");
  return index >= 0 ? raw.slice(0, index) : raw;
}

function isInsideRoot(root, candidate) {
  const resolvedRoot = path.resolve(root);
  const resolvedCandidate = path.resolve(candidate);
  const relative = path.relative(resolvedRoot, resolvedCandidate);
  return relative !== "" && !relative.startsWith("..") && !path.isAbsolute(relative);
}

function joinUnderRoot(root, rel) {
  const parts = String(rel || "")
    .replace(/\\/g, "/")
    .split("/")
    .filter(Boolean);
  if (parts.length === 0 || parts.some((part) => part === "." || part === "..")) {
    return "";
  }
  const candidate = path.resolve(root, ...parts);
  return isInsideRoot(root, candidate) ? candidate : "";
}

function extractStaticFilesRel(source) {
  const raw = String(source || "").trim();
  if (!raw) {
    return "";
  }
  let pathname = raw;
  try {
    if (/^https?:\/\//i.test(raw)) {
      pathname = new URL(raw).pathname;
    }
  } catch {
    return "";
  }
  pathname = stripQuery(pathname);
  const marker = "/static-files/";
  const index = pathname.indexOf(marker);
  if (index < 0) {
    return "";
  }
  let rel = pathname.slice(index + marker.length);
  try {
    rel = decodeURIComponent(rel);
  } catch {
    // Keep the encoded path when it is not valid URI encoding.
  }
  return rel.replace(/\\/g, "/");
}

function resolveRuntimeLocalFile(dataDir, source) {
  const root = String(dataDir || "").trim();
  if (!root) {
    return "";
  }
  const resolvedRoot = path.resolve(root);
  const raw = stripQuery(String(source || "").trim());
  if (!raw) {
    return "";
  }

  const staticRel = extractStaticFilesRel(raw);
  if (staticRel) {
    if (staticRel === "subagent" || staticRel.startsWith("subagent/")) {
      return joinUnderRoot(resolvedRoot, staticRel);
    }
    return joinUnderRoot(path.join(resolvedRoot, "core", "uploads"), staticRel);
  }

  const slashPath = raw.replace(/\\/g, "/");
  const mappedPrefixes = [
    ["/data/subagent/", path.join(resolvedRoot, "subagent")],
    ["/var/lib/lazymind/uploads/", path.join(resolvedRoot, "core", "uploads")],
  ];
  for (const [prefix, mappedRoot] of mappedPrefixes) {
    if (slashPath === prefix.slice(0, -1) || slashPath.startsWith(prefix)) {
      return joinUnderRoot(mappedRoot, slashPath.slice(prefix.length));
    }
  }

  if (path.isAbsolute(raw)) {
    return isInsideRoot(resolvedRoot, raw) ? path.resolve(raw) : "";
  }
  return "";
}

module.exports = {
  extractStaticFilesRel,
  resolveRuntimeLocalFile,
};
