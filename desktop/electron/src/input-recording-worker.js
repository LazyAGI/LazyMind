// Native hook runs separately so permission/native failures cannot terminate the desktop app.
const { createEventCollector } = require("./input-recording-events");
let hook, collector, timer, finished = false;
function finish(discard = false) {
  if (finished) return;
  finished = true; clearTimeout(timer);
  try { hook?.stop(); } finally {
    if (!discard && collector) process.parentPort.postMessage({ type: "result", ...collector.result() });
    process.exit(0);
  }
}
process.parentPort.once("message", ({ data }) => {
  if (data.type !== "start") return process.exit(1);
  try {
    const { uIOhook, UiohookKey } = require("uiohook-napi");
    hook = uIOhook;
    collector = createEventCollector({ keys: UiohookKey, startedAt: data.startedAt, bounds: data.bounds });
    for (const kind of ["mousedown", "mouseup", "mousemove", "wheel", "keydown", "keyup"]) {
      hook.on(kind, (event) => { if (!finished) collector.add(kind, event); });
    }
    process.parentPort.on("message", ({ data: message }) => {
      if (message.type === "stop" || message.type === "cancel") finish(message.type === "cancel");
    });
    hook.start();
    timer = setTimeout(() => finish(), Math.max(1, 120000 - (Date.now() - data.startedAt)));
    process.parentPort.postMessage({ type: "ready" });
  } catch {
    process.parentPort.postMessage({ type: "failed" });
    finish(true);
  }
});
// Never keep observing after the owning app disappears.
const ownerPID = process.ppid;
setInterval(() => { if (process.ppid !== ownerPID || process.ppid === 1) finish(true); }, 1000).unref();
setTimeout(() => finish(true), 125000).unref();
