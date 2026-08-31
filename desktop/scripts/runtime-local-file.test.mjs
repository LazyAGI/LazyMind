import assert from "node:assert/strict";
import path from "node:path";
import test from "node:test";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  extractStaticFilesRel,
  resolveRuntimeLocalFile,
} = require("../electron/src/runtime-local-file.js");

const dataDir = path.resolve("/tmp/lazymind-data");

test("extracts static-files relative paths from signed desktop URLs", () => {
  assert.equal(
    extractStaticFilesRel(
      "https://127.0.0.1:8090/api/core/static-files/subagent/chat-artifacts/a.docx?expires=1&sig=abc",
    ),
    "subagent/chat-artifacts/a.docx",
  );
  assert.equal(
    extractStaticFilesRel("/static-files/workflow-artifacts/out.png"),
    "workflow-artifacts/out.png",
  );
  assert.equal(
    extractStaticFilesRel("/static-files/subagent/foo%20bar.txt"),
    "subagent/foo bar.txt",
  );
});

test("maps subagent and upload URLs onto the desktop data directory", () => {
  assert.equal(
    resolveRuntimeLocalFile(
      dataDir,
      "https://host/api/core/static-files/subagent/chat-artifacts/report.docx",
    ),
    path.join(dataDir, "subagent", "chat-artifacts", "report.docx"),
  );
  assert.equal(
    resolveRuntimeLocalFile(dataDir, "/static-files/tenants/root/file.pdf"),
    path.join(dataDir, "core", "uploads", "tenants", "root", "file.pdf"),
  );
  assert.equal(
    resolveRuntimeLocalFile(dataDir, "/data/subagent/chat-artifacts/out.txt"),
    path.join(dataDir, "subagent", "chat-artifacts", "out.txt"),
  );
  assert.equal(
    resolveRuntimeLocalFile(
      dataDir,
      "/var/lib/lazymind/uploads/workflow-artifacts/out.png",
    ),
    path.join(dataDir, "core", "uploads", "workflow-artifacts", "out.png"),
  );
});

test("rejects path traversal and files outside the data directory", () => {
  assert.equal(
    resolveRuntimeLocalFile(dataDir, "/static-files/subagent/foo/../../secret"),
    "",
  );
  assert.equal(resolveRuntimeLocalFile(dataDir, "/etc/passwd"), "");
  assert.equal(
    resolveRuntimeLocalFile(dataDir, path.join(dataDir, "..", "outside.txt")),
    "",
  );
});
