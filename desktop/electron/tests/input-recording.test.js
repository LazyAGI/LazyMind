const { test } = require("node:test");
const assert = require("node:assert/strict");
const { EventEmitter } = require("node:events");
const { createEventCollector } = require("../src/input-recording-events");
const { createInputRecorder } = require("../src/input-recording");

test("preserves ordinary keys, AltGr, shortcuts, navigation and physical keycodes", () => {
  const collector = createEventCollector({ keys: { A: 30, Enter: 28 }, startedAt: 1000, now: () => 1100 });
  for (const raw of [{ keycode: 30 }, { keycode: 30, shiftKey: true }, { keycode: 30, altKey: true, ctrlKey: true }, { keycode: 30, metaKey: true }, { keycode: 28 }]) collector.add("keydown", raw);
  assert.deepEqual(collector.result().events.map((e) => e.key), ["A", "A", "A", "A", "Enter"]);
  assert.deepEqual(collector.result().events.map((e) => e.keycode), [30, 30, 30, 30, 28]);
  assert.deepEqual(collector.result().events[2].modifiers, ["ctrl", "alt"]);
});
test("filters other screens, normalizes coordinates, throttles motion, and bounds recording", () => {
  let now = 1000;
  const collector = createEventCollector({ keys: {}, startedAt: 1000, bounds: { x: -100, y: 0, width: 100, height: 100 }, now: () => now });
  collector.add("mousedown", { x: 20, y: 20, button: 1 });
  collector.add("mousedown", { x: -50, y: 50, button: 1 });
  collector.add("mousemove", { x: -40, y: 50 });
  collector.add("mousemove", { x: -30, y: 50 });
  now += 251; collector.add("mousemove", { x: -20, y: 50 });
  now += 120000; collector.add("keydown", { keycode: 30 });
  const { events } = collector.result();
  assert.equal(events.length, 3);
  assert.equal(events[0].normalized_x, 0.5);
  assert.equal(events[0].normalized_y, 0.5);
});
test("caps event volume with an explicit limitation", () => {
  const collector = createEventCollector({ keys: {}, startedAt: 0, now: () => 1000 });
  for (let i = 0; i < 1600; i++) collector.add("keydown", { keycode: 30 });
  assert.equal(collector.result().events.length, 1500);
  assert.ok(collector.result().limitations.includes("evidence_size_limit"));
});
function fixture() {
  const child = new EventEmitter(); let killed = 0;
  child.kill = () => { killed++; child.emit("exit", 0); };
  child.postMessage = (m) => {
    if (m.type === "start") queueMicrotask(() => child.emit("message", { type: "ready" }));
    if (m.type === "stop") child.emit("message", { type: "result", events: [{ seconds: 1, kind: "mousedown" }], limitations: [] });
  };
  const recorder = createInputRecorder({ now: () => 1000, spawn: () => { queueMicrotask(() => child.emit("spawn")); return child; } });
  return { child, recorder, killed: () => killed };
}
test("requires a fresh screen selection; stops worker and returns only that session", async () => {
  const { recorder, killed } = fixture();
  await assert.rejects(recorder.start(1000), /source-required/);
  recorder.authorize(); const session = await recorder.start(1000);
  await assert.rejects(recorder.stop("wrong"), /expired/);
  const result = await recorder.stop(session.session_id);
  assert.equal(result.events.length, 1); assert.equal(killed(), 1);
  await assert.rejects(recorder.start(1000), /source-required/);
});
test("cancel discards the session and does not let old cancellation affect a new one", async () => {
  const { recorder, killed } = fixture();
  recorder.authorize(); const session = await recorder.start(1000);
  recorder.cancelSession("old"); assert.equal(killed(), 0);
  recorder.cancelSession(session.session_id); assert.equal(killed(), 1);
  await assert.rejects(recorder.stop(session.session_id), /expired/);
});
test("worker crash produces a failure, never successful empty evidence", async () => {
  const { recorder, child } = fixture();
  recorder.authorize(); const session = await recorder.start(1000);
  child.emit("exit", 1);
  await assert.rejects(recorder.stop(session.session_id), /unavailable/);
});
