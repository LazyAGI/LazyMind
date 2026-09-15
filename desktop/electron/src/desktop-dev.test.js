const assert = require("node:assert/strict");
const test = require("node:test");

const {
  desktopDevRendererURL,
  desktopDevRuntimeStatus,
  normalizeLoopbackURL,
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
