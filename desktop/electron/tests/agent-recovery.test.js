const test = require("node:test");
const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const { PassThrough } = require("node:stream");
const { createAgentRecovery, runRecoveryCommand } = require("../src/agent-recovery");

const tick = () => new Promise(resolve => setImmediate(resolve));
const request = { id: "a".repeat(32), code: "CODEX_CONNECTION_REQUIRED", can_restart: true };

function fixture(reconnect = async () => {}, options = {}) {
  const handlers = new Map(), windows = [], dismissed = [], waits = [];
  class FakeWindow extends EventEmitter {
    constructor(options) {
      super(); this.options = options; this.messages = []; this.destroyed = false;
      this.webContents = new EventEmitter();
      this.webContents.mainFrame = {};
      this.webContents.send = (_channel, state) => this.messages.push({ ...state });
      this.webContents.setWindowOpenHandler = handler => { this.openHandler = handler; };
      windows.push(this);
    }
    setPosition() {}
    setAlwaysOnTop(value, level) { this.topmost = value; this.topmostLevel = level; }
    setVisibleOnAllWorkspaces() {}
    async loadFile() {}
    showInactive() { this.shown = true; }
    show() { this.shown = true; this.activeShow = true; this.topmost = false; }
    focus() { this.focused = true; }
    moveTop() { this.raised = true; }
    flashFrame(value) { this.flashing = value; }
    isDestroyed() { return this.destroyed; }
    isVisible() { return Boolean(this.shown); }
    isAlwaysOnTop() { return Boolean(this.topmost); }
    close() {
      let prevented = false; this.emit("close", { preventDefault: () => { prevented = true; } });
      if (!prevented) this.destroy();
    }
    destroy() { if (!this.destroyed) { this.destroyed = true; this.emit("closed"); } }
  }
  const controller = createAgentRecovery({
    BrowserWindow: FakeWindow, ipcMain: { handle: (name, fn) => handlers.set(name, fn) },
    screen: { getCursorScreenPoint: () => ({}), getDisplayNearestPoint: () => ({ workArea: { x: 0, y: 0, width: 1200, height: 800 } }) },
    wait: signal => new Promise((resolve, reject) => {
      waits.push(resolve); signal.addEventListener("abort", () => reject(new Error("aborted")), { once: true });
    }),
    dismiss: async id => { dismissed.push(id); }, reconnect, locale: () => "zh-CN", successMs: 5, ...options,
  });
  const invoke = (method, event) => {
    const window = windows.at(-1);
    return handlers.get(`lazymind:agentRecovery${method}`)(event || { sender: window.webContents, senderFrame: window.webContents.mainFrame });
  };
  return { controller, windows, dismissed, waits, invoke };
}

test("shows one recovery window, dismisses without restarting, and waits for a new request", async () => {
  let restarts = 0;
  const f = fixture(async () => { restarts++; });
  f.controller.start(); f.controller.start();
  assert.equal(f.waits.length, 1);
  f.waits.shift()({ request }); await tick();
  const window = f.windows[0];
  assert.equal(window.shown, true); assert.equal(window.topmost, true);
  assert.equal(window.options.skipTaskbar, process.platform !== "win32");
  assert.equal(window.options.webPreferences.nodeIntegration, false);
  assert.equal(window.options.webPreferences.sandbox, true);
  assert.equal(window.openHandler().action, "deny");
  assert.equal(f.invoke("State").language, "zh");
  await f.invoke("Dismiss"); await tick();
  assert.equal(restarts, 0);
  assert.deepEqual(f.dismissed, [request.id]);
  assert.equal(f.windows.length, 1);
  f.controller.stop();
});

for (const platform of ["win32", "darwin"]) {
  test(`recovery visibility uses the ${platform} presentation policy without restarting`, async () => {
    let restarts = 0;
    const f = fixture(async () => { restarts++; }, { platform });
    f.controller.start(); f.waits.shift()({ request }); await tick();
    assert.equal(f.windows[0].shown, true);
    assert.equal(Boolean(f.windows[0].focused), platform === "win32");
    assert.equal(Boolean(f.windows[0].activeShow), platform === "win32");
    assert.equal(f.windows[0].topmost, true);
    assert.equal(f.windows[0].topmostLevel, platform === "win32" ? "pop-up-menu" : "floating");
    assert.equal(f.windows[0].options.skipTaskbar, platform !== "win32");
    if (platform === "win32") {
      assert.equal(f.windows[0].raised, true);
      assert.equal(f.windows[0].flashing, true);
      f.windows[0].emit("focus");
      assert.equal(f.windows[0].flashing, false);
    }
    assert.equal(restarts, 0);
    await f.controller.stop();
  });
}

test("only the dedicated main frame can confirm; duplicate clicks never duplicate restart", async () => {
  let restarts = 0, complete;
  const f = fixture(async (_id, stage) => {
    restarts++; stage("closing"); await new Promise(resolve => { complete = resolve; });
  });
  f.controller.start(); f.waits.shift()({ request }); await tick();
  await assert.rejects(f.invoke("Confirm", { sender: {}, senderFrame: {} }), /RECOVERY_WINDOW_REQUIRED/);
  await assert.rejects(f.invoke("Confirm", { sender: f.windows[0].webContents, senderFrame: {} }), /RECOVERY_WINDOW_REQUIRED/);
  const pending = f.invoke("Confirm");
  await f.invoke("Confirm"); await f.invoke("Dismiss");
  f.windows[0].close();
  assert.equal(restarts, 1); assert.equal(f.windows[0].isDestroyed(), false);
  assert.equal(f.invoke("State").stage, "closing");
  complete(); await pending;
  assert.equal(f.invoke("State").stage, "connected");
  await new Promise(resolve => setTimeout(resolve, 15));
  assert.equal(f.windows[0].isDestroyed(), true);
  f.controller.stop();
});

test("restart failure stays in the same window and permits an explicit retry", async () => {
  let attempts = 0;
  const f = fixture(async () => { if (++attempts === 1) throw new Error("Codex refused to close"); });
  f.controller.start(); f.waits.shift()({ request }); await tick();
  await f.invoke("Confirm");
  assert.equal(f.invoke("State").stage, "failed");
  assert.match(f.invoke("State").error, /refused/);
  assert.equal(f.windows.length, 1);
  await f.invoke("Confirm");
  assert.equal(attempts, 2);
  assert.equal(f.invoke("State").stage, "connected");
  f.controller.stop(); await tick();
});

test("connection is not treated as completion before chat restoration and handoff finish", async () => {
  let advance;
  const f=fixture(async(_id,stage)=>{
    stage("connecting"); stage("restoring");
    await new Promise(resolve=>{advance=()=>{stage("resuming");resolve();};});
  });
  f.controller.start();f.waits.shift()({request});await tick();
  const pending=f.invoke("Confirm");
  assert.equal(f.invoke("State").stage,"restoring");
  assert.equal(f.invoke("State").busy,true);
  assert.equal(f.windows[0].isDestroyed(),false);
  advance();await pending;
  assert.equal(f.invoke("State").stage,"connected");
  assert.ok(f.windows[0].messages.some(state=>state.stage==="resuming"));
  f.controller.stop();
});

test("an already restored results panel closes its recovery window without restarting",async()=>{
 let restarted=0,completed=0;
 const f=fixture(async()=>{restarted++;},{check:async()=>({ready:true}),finishView:async()=>{completed++;},checkMs:5});
 f.controller.start();f.waits.shift()({request:{...request,view_only:true}});await tick();
 await new Promise(r=>setTimeout(r,20));
 assert.equal(restarted,0);assert.equal(completed,1);assert.equal(f.windows[0].isDestroyed(),true);
 assert.ok(f.windows[0].messages.some(state=>state.stage==="connected"));
 f.controller.stop();
});

test("a view acknowledgement closes a window even while its reconnect job is still waiting",async()=>{
 let ready;
 const f=fixture(async(_id,stage,signal)=>{
  stage("showing-panel");await new Promise((_resolve,reject)=>signal.addEventListener("abort",()=>reject(Error("cancelled")),{once:true}));
 },{check:()=>new Promise(resolve=>{ready=resolve}),finishView:async()=>{},checkMs:5});
 f.controller.start();f.waits.shift()({request:{...request,view_only:true}});await tick();
 const pending=f.invoke("Confirm");assert.equal(f.invoke("State").busy,true);
 ready({ready:true});await tick();assert.equal(f.invoke("State").stage,"connected");
 await new Promise(r=>setTimeout(r,20));await pending;
 assert.equal(f.windows[0].isDestroyed(),true);f.controller.stop();
});

test("a view probe cannot close a request that gained a pending execution",async()=>{
 const f=fixture(undefined,{check:async()=>({ready:true}),finishView:async()=>{throw Error("execution queued")},checkMs:50});
 f.controller.start();f.waits.shift()({request:{...request,view_only:true}});await tick();
 assert.equal(f.invoke("State").stage,"confirm");assert.equal(f.windows[0].isDestroyed(),false);f.controller.stop();
});

test("a non-restartable problem never invokes restart and logout discards late requests", async () => {
  let restarts = 0;
  const f = fixture(async () => { restarts++; });
  f.controller.start(); f.waits.shift()({ request: { ...request, can_restart: false } }); await tick();
  await f.invoke("Confirm"); assert.equal(restarts, 0);
  f.controller.stop(); await tick();
  f.controller.start(); const late = f.waits.pop(); f.controller.stop();
  late({ request }); await tick();
  assert.equal(f.windows.length, 1);
});

function commandFixture() {
  const child = new EventEmitter(); child.stdout = new PassThrough(); child.stderr = new PassThrough();
  child.kills = 0; child.kill = () => { child.kills++; };
  return child;
}

test("an open window refreshes restart eligibility in both directions without restarting automatically", async () => {
  const unavailable = { ...request, code: "CODEX_CONTROL_UNAVAILABLE", can_restart: false };
  for (const [initial, latest] of [[unavailable, request], [request, unavailable]]) {
    let restarts = 0;
    const f = fixture(async () => { restarts++; });
    try {
      f.controller.start(); f.waits.shift()({ request: initial }); await tick();
      assert.equal(f.invoke("State").can_restart, initial.can_restart);
      f.waits.shift()({ request: latest }); await tick();
      assert.equal(f.windows.length, 1);
      assert.equal(f.invoke("State").code, latest.code);
      assert.equal(f.invoke("State").can_restart, latest.can_restart);
      assert.equal(f.windows[0].messages.at(-1).can_restart, latest.can_restart);
      assert.equal(restarts, 0);
      await f.invoke("Confirm");
      assert.equal(restarts, latest.can_restart ? 1 : 0);
    } finally { await f.controller.stop(); }
  }
});

test("a diagnosis refresh preserves active recovery progress", async () => {
  let complete;
  const f = fixture(async (_id, stage) => {
    stage("connecting"); await new Promise(resolve => { complete = resolve; });
  });
  f.controller.start(); f.waits.shift()({ request }); await tick();
  const pending = f.invoke("Confirm");
  f.waits.shift()({ request: { ...request, code: "CODEX_CONTROL_UNAVAILABLE", can_restart: false } }); await tick();
  assert.equal(f.invoke("State").stage, "connecting");
  assert.equal(f.invoke("State").busy, true);
  complete(); await pending;
  assert.equal(f.invoke("State").stage, "connected");
  await f.controller.stop();
});

test("refresh ignores a different request and responses arriving after logout", async () => {
  for (const stop of [false, true]) {
    const f = fixture();
    f.controller.start(); f.waits.shift()({ request }); await tick();
    const refresh = f.waits.shift();
    const window = f.windows[0], messageCount = window.messages.length;
    if (stop) await f.controller.stop();
    refresh({ request: { ...request, id: stop ? request.id : "b".repeat(32), can_restart: false } }); await tick();
    assert.equal(window.messages.length, messageCount);
    if (!stop) assert.equal(f.invoke("State").can_restart, true);
    assert.equal(f.windows.length, 1);
    await f.controller.stop();
  }
});
test("progress is streamed but only a successful completed command counts as recovery", async () => {
  for (const success of [true, false]) {
    const child = commandFixture(), stages = [];
    const done = runRecoveryCommand({ spawn: (_exe, args) => {
      assert.deepEqual(args, ["internal", "codex-recovery", "reconnect", request.id]); return child;
    }, executable: "connector", env: {}, id: request.id, onStage: stage => stages.push(stage) });
    child.stdout.write('{"stage":"closing"}\n{"stage":"connec');
    child.stdout.write('ted"}\n');
    if (success) child.stdout.write('{"stage":"done"}\n');
    else child.stderr.write("Codex failed to exit");
    child.emit("close", success ? 0 : 1);
    if (success) await done; else await assert.rejects(done, /failed to exit/);
    assert.deepEqual(stages, ["closing", "connected"]);
  }
});

test("cancelling the desktop listener cancels its recovery child", async () => {
  const child = commandFixture(), abort = new AbortController();
  const done = runRecoveryCommand({ spawn: () => child, executable: "connector", id: request.id, onStage: () => {}, signal: abort.signal });
  abort.abort(); await assert.rejects(done, /cancelled/); assert.equal(child.kills, 1);
});

test("a verified panel clears a reconnect failure without another restart", async () => {
 let ready = false, restarts = 0, completed = 0;
 const f = fixture(async () => { restarts++; throw Error("CODEX_CDP_UNAVAILABLE"); }, {
  check: async () => ({ready}), finishView: async () => { completed++; }, checkMs: 5, successMs: 1000,
 });
 try {
  f.controller.start(); f.waits.shift()({request: {...request, view_only: true}}); await tick();
  await f.invoke("Confirm"); assert.equal(f.invoke("State").stage, "failed");
  ready = true;
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(f.invoke("State").stage, "connected");
  assert.equal(f.invoke("State").error, "");
  assert.equal(restarts, 1); assert.equal(completed, 1);
 } finally { await f.controller.stop(); }
});
