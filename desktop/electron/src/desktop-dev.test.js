const assert = require("node:assert/strict");
const test = require("node:test");

const {
  resolveAgentConnectorPath,
  desktopNotificationAPIOrigin,
  desktopNotificationInstanceID,
  desktopNotificationRuntimeReady,
  desktopDevRendererURL,
  desktopDevRuntimeStatus,
  normalizeLoopbackURL,
  restoreDesktopNotificationSession,
} = require("./desktop-dev");

test("normalizeLoopbackURL accepts local HTTP origins", () => {
  assert.equal(normalizeLoopbackURL("http://127.0.0.1:5173/"), "http://127.0.0.1:5173");
  assert.equal(normalizeLoopbackURL("https://localhost:5173/path/"), "https://localhost:5173/path");
});

test("normalizeLoopbackURL rejects remote and executable origins", () => {
  assert.throws(() => normalizeLoopbackURL("https://example.com"), /loopback/);
  assert.throws(() => normalizeLoopbackURL("file:///tmp/index.html"), /loopback|http/);
});

test("desktopDevRendererURL targets the Desktop chat route", () => {
  assert.equal(
    desktopDevRendererURL("http://127.0.0.1:5173"),
    "http://127.0.0.1:5173/agent/chat/home",
  );
});

test("desktopDevRuntimeStatus reports capabilities ready without owning Local Runtime", () => {
  const status = desktopDevRuntimeStatus("http://127.0.0.1:8090");
  assert.equal(status.profile, "desktop-dev");
  assert.equal(status.ownerMatched, false);
  assert.equal(status.services.core.status, "ready");
  assert.equal(status.services.chat.status, "ready");
});

test("desktop notifications accept an external ready runtime in development", () => {
  const status = desktopDevRuntimeStatus("http://127.0.0.1:8090");
  assert.equal(desktopNotificationRuntimeReady(status, true), true);
  assert.equal(desktopNotificationRuntimeReady(status, false), false);
  assert.equal(desktopNotificationRuntimeReady({ ...status, overallStatus: "starting" }, true), false);
});

test("desktop notifications use the external runtime URL in development", () => {
  const status = desktopDevRuntimeStatus("http://127.0.0.1:8090");
  assert.equal(desktopNotificationAPIOrigin(status, true), "http://127.0.0.1:8090");
  assert.equal(desktopNotificationInstanceID(status, true), "http://127.0.0.1:8090");
  assert.equal(desktopNotificationAPIOrigin({ config: { localProxy: { port: 5024 } } }, false), "http://127.0.0.1:5024");
});

test("desktop development restores the saved native notification session", () => {
  const calls = [];
  const restored = restoreDesktopNotificationSession(
    { ok: true, session: { server_url: "http://127.0.0.1:5173", access_token: "token" } },
    { hydrate: (session) => calls.push(["hydrate", session]) },
    { setSession: (session) => { calls.push(["set", session]); return Promise.resolve(); } },
  );
  assert.equal(restored, true);
  assert.deepEqual(calls.map(([name]) => name), ["hydrate", "set"]);
});

for (const isWindows of [false, true]) {
  const binary = isWindows ? "lazymind.exe" : "lazymind";
  const options = { repoRoot: "/repo", runtimeResourcesRoot: "/runtime", isWindows };
  test(`external runtime development uses local CLI (${binary})`, () => {
    assert.equal(resolveAgentConnectorPath({ ...options, isExternalRuntimeDev: true }), `/repo/local/build/bin/${binary}`);
  });
  test(`normal desktop preserves bundled CLI (${binary})`, () => {
    assert.equal(resolveAgentConnectorPath({ ...options, isExternalRuntimeDev: false }), `/runtime/bin/${binary}`);
  });
  test(`explicit connector override wins in both modes (${binary})`, () => {
    for (const isExternalRuntimeDev of [false, true]) {
      assert.equal(resolveAgentConnectorPath({ ...options, isExternalRuntimeDev, override: "/custom/connector" }), "/custom/connector");
    }
  });
}
