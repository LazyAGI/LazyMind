import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { stageHistoryInjectionPackage, stageHistoryInjectionDescriptor } from "./stage-history-injection-package.mjs";

const scriptsDir = path.dirname(fileURLToPath(import.meta.url));
const repositoryConfig = path.join(scriptsDir, "..", "history-injection-package.json");

test("repository history injection metadata points to the verified ModelScope asset", () => {
  const config = JSON.parse(readFileSync(repositoryConfig, "utf8"));
  assert.equal(
    config.url,
    "https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/master/lazymind-history-injection-five-samples-20261112-cdff88d0ca65.zip",
  );
  assert.equal(config.sha256, "cdff88d0ca65867842ea12d71f08569ab1f8f37b5512ecb06c505657d0d06c31");
  assert.equal(config.size, 52060542);
  assert.equal(config.runtimeFileName, "history-injection.zip");
  assert.deepEqual(config.conversationIds, [
    "5bea5b4c-081d-5f9a-9017-b53f674fcc03",
    "ec375a1f-d6df-55ea-8d61-dc2dfc6494fd",
    "bcb3f7d6-9ec8-5dd6-a16b-683e31254f97",
    "fcb148c3-c22a-5b4a-aaa2-5aabfac4a339",
    "3f26e747-50cd-5736-8d73-4befce120ff3",
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


test("deferred staging copies only metadata and removes a stale bundled archive", async (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), "lazymind-history-deferred-"));
  let requests = 0;
  const server = createServer((_request, response) => { requests += 1; response.writeHead(503); response.end(); });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  t.after(() => server.close());
  const config = JSON.parse(readFileSync(repositoryConfig, "utf8"));
  config.url = `http://127.0.0.1:${server.address().port}/not-downloaded.zip`;
  const configPath = path.join(root, "input.json");
  writeFileSync(configPath, JSON.stringify(config));
  writeFileSync(path.join(root, "history-injection.zip"), "stale installer payload");
  const result = await stageHistoryInjectionDescriptor(root, { configPath });
  assert.deepEqual(JSON.parse(readFileSync(result.descriptorPath, "utf8")), config);
  assert.equal(existsSync(path.join(root, "history-injection.zip")), false);
  assert.equal(requests, 0, "building a deferred installer must not download the examples");
});
