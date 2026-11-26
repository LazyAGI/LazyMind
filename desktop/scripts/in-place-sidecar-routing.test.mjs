import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";
import dev from "../electron/src/desktop-dev.js";

test("an in-place test build uses its own startup binaries and the configured data root", () => {
  const main = fs.readFileSync(new URL("../electron/src/main.js", import.meta.url), "utf8").replace(/\r\n/g, "\n");
  const start = main.indexOf("const clientInstanceId =");
  const end = main.indexOf("try {\n  guardDesktopStartup", start);
  assert.ok(start >= 0 && end > start);
  const resourcesPath = path.resolve("Apps", "LazyMind.app", "Contents", "Resources");
  const data = path.resolve("previous user data");
  const env = {
    LAZYMIND_DESKTOP_RESOURCES_ROOT: path.resolve("other app", "runtime"),
    LAZYMIND_DESKTOP_REPO_ROOT: path.resolve("other app", "repo"),
    LAZYMIND_DESKTOP_SIDECAR: path.resolve("other app", "manager"),
    LAZYMIND_DESKTOP_AGENT_CONNECTOR: path.resolve("other app", "connector"),
    LAZYMIND_DESKTOP_RUNTIME_ROOT: data,
  };
  const connectorStart = main.indexOf("const agentConnectorPath =");
  const connectorEnd = main.indexOf("const maxStartupLogEntries", connectorStart);
  assert.ok(connectorStart > end && connectorEnd > connectorStart);
  const actual = vm.runInNewContext(main.slice(start, end) + main.slice(connectorStart, connectorEnd) +
    "\n({ runtimeResourcesRoot, repoRoot, sidecarPath, agentConnectorPath, explicitRuntimeRoot })", {
    isPackaged: true, isWindows: false, unsignedTestProfile: {}, randomBytes: Buffer.alloc,
    resolveAgentConnectorPath: dev.resolveAgentConnectorPath, isExternalRuntimeDev: false,
    process: { env, resourcesPath }, path, loadDesktopCloudConfiguration: () => ({}),
  });
  const runtime = path.join(resourcesPath, "runtime");
  assert.equal(actual.runtimeResourcesRoot, runtime);
  assert.equal(actual.repoRoot, path.join(runtime, "app"));
  assert.equal(actual.sidecarPath, path.join(runtime, "bin", "local-runtime-manager"));
  assert.equal(actual.agentConnectorPath, path.join(runtime, "bin", "lazymind"));
  assert.equal(actual.explicitRuntimeRoot, data);
});
