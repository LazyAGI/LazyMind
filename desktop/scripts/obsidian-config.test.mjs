import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  buildObsidianRuntimeEnv,
  clearObsidianConfig,
  emptyObsidianConfig,
  isExistingDirectory,
  loadObsidianConfig,
  saveObsidianConfig,
} = require("../electron/src/obsidian-config.js");

function temporaryDirectory() {
  return fs.mkdtempSync(path.join(os.tmpdir(), "lazymind-obsidian-config-"));
}

test("persists Obsidian root atomically with restricted permissions", () => {
  const root = temporaryDirectory();
  const configPath = path.join(root, "obsidian-config.json");
  const saved = saveObsidianConfig(configPath, path.join(root, "vaults"));
  const loaded = loadObsidianConfig(configPath);

  assert.equal(saved.root, path.join(root, "vaults"));
  assert.equal(loaded.root, saved.root);
  if (process.platform !== "win32") {
    assert.equal(fs.statSync(configPath).mode & 0o777, 0o600);
  }
});

test("invalid configuration fails closed", () => {
  const root = temporaryDirectory();
  const configPath = path.join(root, "obsidian-config.json");
  fs.writeFileSync(configPath, "not-json");

  assert.deepEqual(loadObsidianConfig(configPath), emptyObsidianConfig());
});

test("clears the configured root without deleting the state file", () => {
  const root = temporaryDirectory();
  const configPath = path.join(root, "obsidian-config.json");
  saveObsidianConfig(configPath, root);
  const cleared = clearObsidianConfig(configPath);

  assert.equal(cleared.root, "");
  assert.equal(fs.existsSync(configPath), true);
  assert.equal(loadObsidianConfig(configPath).root, "");
});

test("uses a disabled path when the configured root is unavailable", () => {
  const disabledRoot = path.join(temporaryDirectory(), "disabled");
  const runtimeEnv = buildObsidianRuntimeEnv("/path/that/does/not/exist", disabledRoot);

  assert.equal(runtimeEnv.LAZYLLM_OBSIDIAN_VAULT_PATH, disabledRoot);
  assert.equal(runtimeEnv.LAZYLLM_OBSIDIAN_HOST_ROOT, disabledRoot);
  assert.equal(runtimeEnv.OBSIDIAN_VAULT_PATH, undefined);
  assert.equal(runtimeEnv.OBSIDIAN_HOST_ROOT, undefined);
  assert.equal(isExistingDirectory("/path/that/does/not/exist"), false);
});
