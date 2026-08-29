import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { stageHistoryInjectionPackage } from "./stage-history-injection-package.mjs";

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const repositoryConfig = path.join(scriptsDir, "..", "history-injection-package.json");

test("repository history injection metadata points to the verified ModelScope asset", () => {
  const config = JSON.parse(readFileSync(repositoryConfig, "utf8"));
  assert.equal(
    config.url,
    "https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/lazymind-history-injection-five-samples-20260829.zip",
  );
  assert.equal(config.sha256, "cbe7774ff2b64ceb7083fa0edfa62d2e0ed59d283c37ade6f210b87267979e9f");
  assert.equal(config.size, 74408904);
  assert.equal(config.runtimeFileName, "history-injection.zip");
  assert.deepEqual(config.conversationIds, [
    "e97d6394-5b7c-4bab-8757-3f04c9aefdde",
    "a3b1d442-7a49-4225-a806-13fc162f7745",
    "bcea6551-d6a2-4dec-83bb-1ccf9fa11f51",
    "e3096625-c245-45a0-bc32-ce203f3c94f3",
    "3ecbcfdc-2e2a-4c38-ae75-060ba6bd02e4",
  ]);
});

test("stages and caches a verified history injection package", async (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-history-package-"));
  const payload = Buffer.from("history-injection-fixture");
  const sha256 = createHash("sha256").update(payload).digest("hex");
  let requests = 0;
  const server = createServer((_request, response) => {
    requests += 1;
    response.writeHead(200, { "content-type": "application/zip" });
    response.end(payload);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());

  const address = server.address();
  const configPath = path.join(root, "package.json");
  writeFileSync(configPath, JSON.stringify({
    version: 1,
    url: `http://127.0.0.1:${address.port}/package.zip`,
    fileName: "package.zip",
    sha256,
    size: payload.length,
    runtimeFileName: "history-injection.zip",
  }));
  const runtimeRoot = path.join(root, "runtime");
  const cacheRoot = path.join(root, "cache");

  const first = await stageHistoryInjectionPackage(runtimeRoot, { configPath, cacheRoot, attempts: 1 });
  assert.deepEqual(readFileSync(first.runtimePath), payload);
  assert.equal(requests, 1);

  const second = await stageHistoryInjectionPackage(runtimeRoot, { configPath, cacheRoot, attempts: 1 });
  assert.deepEqual(readFileSync(second.runtimePath), payload);
  assert.equal(requests, 1, "the verified cache should avoid another network request");
});

test("rejects a package whose checksum does not match", async (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-history-package-bad-"));
  const payload = Buffer.from("wrong-package");
  const server = createServer((_request, response) => response.end(payload));
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const address = server.address();
  const configPath = path.join(root, "package.json");
  writeFileSync(configPath, JSON.stringify({
    version: 1,
    url: `http://127.0.0.1:${address.port}/package.zip`,
    fileName: "package.zip",
    sha256: "0".repeat(64),
    size: payload.length,
    runtimeFileName: "history-injection.zip",
  }));
  const runtimeRoot = path.join(root, "runtime");

  await assert.rejects(
    stageHistoryInjectionPackage(runtimeRoot, {
      configPath,
      cacheRoot: path.join(root, "cache"),
      attempts: 1,
    }),
    /SHA-256 mismatch/,
  );
  assert.equal(existsSync(path.join(runtimeRoot, "history-injection.zip")), false);
});
