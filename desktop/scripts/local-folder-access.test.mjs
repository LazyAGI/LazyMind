import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  collapseRoots,
  containsPath,
  discoverRecommendedFolders,
  isMediaDirectoryName,
  loadAccessState,
  recommendationsForExactFolders,
  saveAccessState,
} = require("../electron/src/local-folder-access.js");

function temporaryDirectory() {
  return fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-folder-access-"));
}

test("collapses duplicate and descendant roots without crossing path boundaries", () => {
  assert.deepEqual(
    collapseRoots(["/Users/alice/work", "/Users/alice", "/Users/alice2"], "darwin"),
    ["/Users/alice", "/Users/alice2"],
  );
  assert.equal(containsPath("/Users/alice", "/Users/alice/work", "darwin"), true);
  assert.equal(containsPath("/Users/alice", "/Users/alice2", "darwin"), false);
});

test("persists discovery and exact allowed roots atomically", () => {
  const root = temporaryDirectory();
  const statePath = path.join(root, "state", "local-folder-access.json");
  const saved = saveAccessState(statePath, {
    discoveryConsentGranted: true,
    discoveryRoots: [root],
    allowedRoots: [path.join(root, "workspace"), path.join(root, "workspace", "nested")],
  });
  const loaded = loadAccessState(statePath);

  assert.equal(saved.discoveryConsentGranted, true);
  assert.deepEqual(loaded.discoveryRoots, [root]);
  assert.deepEqual(loaded.allowedRoots, [path.join(root, "workspace")]);
  if (process.platform !== "win32") {
    assert.equal(fs.statSync(statePath).mode & 0o777, 0o600);
  }
});

test("invalid persisted access state fails closed", () => {
  const root = temporaryDirectory();
  const statePath = path.join(root, "local-folder-access.json");
  fs.writeFileSync(statePath, "not-json");

  const loaded = loadAccessState(statePath);

  assert.equal(loaded.discoveryConsentGranted, false);
  assert.deepEqual(loaded.discoveryRoots, []);
  assert.deepEqual(loaded.allowedRoots, []);
});

test("maps already allowed product folders to recommendations without scanning", () => {
  assert.deepEqual(
    recommendationsForExactFolders([
      "/Users/alice/.codex/skills",
      "/Users/alice/project/.cursor/rules",
      "/Users/alice/ordinary-folder",
    ], "darwin").map((item) => [item.title, item.path]),
    [
      ["Codex Skills", "/Users/alice/.codex/skills"],
      ["Cursor Rules", "/Users/alice/project/.cursor/rules"],
    ],
  );
  assert.deepEqual(
    recommendationsForExactFolders([
      String.raw`C:\Users\alice\.codex\skills`,
    ], "win32").map((item) => item.title),
    ["Codex Skills"],
  );
});

test("discovers known tool folders within a shallow authorized root", async () => {
  const root = temporaryDirectory();
  const codex = path.join(root, ".codex", "skills");
  const cursor = path.join(root, "projects", "demo", ".cursor", "rules");
  fs.mkdirSync(codex, { recursive: true });
  fs.mkdirSync(cursor, { recursive: true });
  fs.mkdirSync(path.join(root, "projects", "demo", "node_modules", ".cursor", "rules"), { recursive: true });

  const result = await discoverRecommendedFolders({ roots: [root] });

  assert.deepEqual(result.items.map((item) => item.path), [codex, cursor].sort());
  assert.equal(result.truncated, false);
});

test("uses Cursor workspace records to find project rules beyond the shallow scan depth", async () => {
  const root = temporaryDirectory();
  const workspace = path.join(root, "one", "two", "three", "four", "project");
  const rules = path.join(workspace, ".cursor", "rules");
  const storage = path.join(root, "cursor-workspace-storage");
  fs.mkdirSync(rules, { recursive: true });
  fs.mkdirSync(path.join(storage, "workspace-id"), { recursive: true });
  fs.writeFileSync(
    path.join(storage, "workspace-id", "workspace.json"),
    JSON.stringify({ folder: new URL(`file://${workspace}`).href }),
  );

  const result = await discoverRecommendedFolders({
    roots: [root],
    cursorWorkspaceStorageRoots: [storage],
    budget: { maxDepth: 2, maxEntries: 100, timeoutMs: 5_000 },
  });

  assert.equal(result.items.some((item) => item.path === rules), true);
});

test("filters media folders by system path and cross-platform directory names", async () => {
  const root = temporaryDirectory();
  const pictures = path.join(root, "RelocatedMedia");
  const ignoredRules = path.join(pictures, "project", ".cursor", "rules");
  const keptRules = path.join(root, "Work", "project", ".cursor", "rules");
  fs.mkdirSync(ignoredRules, { recursive: true });
  fs.mkdirSync(keptRules, { recursive: true });

  const result = await discoverRecommendedFolders({
    roots: [root],
    excludedRoots: [pictures],
  });

  assert.equal(result.items.some((item) => item.path === ignoredRules), false);
  assert.equal(result.items.some((item) => item.path === keptRules), true);
  for (const name of ["Music", "Pictures", "Movies", "Videos", "Camera Roll", "音乐", "图片", "影视"]) {
    assert.equal(isMediaDirectoryName(name), true, name);
  }
});

test("stops discovery at the entry budget without failing unreadable branches", async () => {
  const root = temporaryDirectory();
  for (let index = 0; index < 20; index += 1) {
    fs.mkdirSync(path.join(root, `folder-${index}`));
  }

  const result = await discoverRecommendedFolders({
    roots: [root],
    budget: { maxDepth: 4, maxEntries: 5, timeoutMs: 5_000 },
  });

  assert.equal(result.truncated, true);
  assert.equal(result.stoppedReason, "entry_limit");
});
