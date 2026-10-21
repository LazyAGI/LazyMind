const fs = require("node:fs/promises");
const os = require("node:os");
const path = require("node:path");

// Resolve an owned reference through Core; never treat renderer-provided paths
// as authorization and never download a copy when the original is unavailable.
async function revealDirectoryFile(reference, {
  lookup, showItemInFolder, platform = process.platform, hostname = os.hostname(),
  realpath = fs.realpath, stat = fs.stat,
}) {
  if (![reference?.directoryId, reference?.entryId].every(id => typeof id === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(id))) {
    throw new Error("Invalid directory reference");
  }
  const file = await lookup(reference);
  const paths = platform === "win32" ? path.win32 : path.posix;
  const namespace = `${hostname}:${platform === "win32" ? "nt" : "posix"}`;
  if (file?.error_code || file?.namespace !== namespace || typeof file.path !== "string" || !paths.isAbsolute(file.path)) {
    throw new Error("Original file is unavailable on this computer");
  }
  const canonical = await realpath(file.path);
  const normalize = value => platform === "win32" ? paths.normalize(value).toLowerCase() : paths.normalize(value);
  if (normalize(canonical) !== normalize(file.path) || !(await stat(canonical)).isFile()) {
    throw new Error("Original file location has changed");
  }
  showItemInFolder(canonical);
  return { ok: true, path: canonical };
}

module.exports = { revealDirectoryFile };
