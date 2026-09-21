const { test } = require("node:test");
const assert = require("node:assert/strict");
const { installScreenCapture } = require("../src/screen-capture");

function setup() {
  let handler, template, popup;
  const frame = {};
  const window = { isDestroyed: () => false, webContents: { mainFrame: frame } };
  installScreenCapture({
    session: { setDisplayMediaRequestHandler: (value) => { handler = value; } },
    desktopCapturer: { getSources: async (options) => { assert.deepEqual(options.types, ["screen"]); return [{ id: "screen:1", name: "Screen 1" }, { id: "screen:2", name: "Screen 2" }]; } },
    Menu: { buildFromTemplate: (value) => { template = value; return { popup: (value) => { popup = value; } }; } },
    getWindow: () => window,
  });
  return { request: { frame, userGesture: true, videoRequested: true, audioRequested: false }, handler, get template() { return template; }, get popup() { return popup; } };
}
test("only grants the explicitly selected source, exactly once", async () => {
  const app = setup(), results = [];
  await app.handler(app.request, (value) => results.push(value));
  assert.deepEqual(results, []);
  app.template[1].click(); app.popup.callback();
  assert.deepEqual(results, [{ video: { id: "screen:2", name: "Screen 2" } }]);
});
test("dismissal denies capture", async () => {
  const app = setup(), results = [];
  await app.handler(app.request, (value) => results.push(value));
  app.popup.callback(); assert.deepEqual(results, [{}]);
});
test("rejects subframes, audio and requests without a gesture", async () => {
  for (const patch of [{ frame: {} }, { audioRequested: true }, { userGesture: false }]) {
    const app = setup(), results = [];
    await app.handler({ ...app.request, ...patch }, (value) => results.push(value));
    assert.deepEqual(results, [{}]); assert.equal(app.template, undefined);
  }
});
test("records Electron's string rejection without recording screen contents", async () => {
  let handler;
  const frame = {};
  const logs = [], results = [];
  installScreenCapture({
    session: { setDisplayMediaRequestHandler: (value) => { handler = value; } },
    desktopCapturer: { getSources: async () => { throw "Failed to get sources."; } },
    Menu: { buildFromTemplate: () => { throw new Error("must not open picker"); } },
    getWindow: () => ({ isDestroyed: () => false, webContents: { mainFrame: frame } }),
    log: (stage, details) => logs.push({ stage, details }),
  });
  await handler({ frame, userGesture: true, videoRequested: true, audioRequested: false }, (value) => results.push(value));
  assert.deepEqual(results, [{}]);
  assert.equal(logs.find((entry) => entry.stage === "capture-error").details.message, "Failed to get sources.");
});
