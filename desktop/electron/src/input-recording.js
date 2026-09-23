const { randomUUID } = require("node:crypto");

function createInputRecorder({ spawn, now = Date.now }) {
  let grant = null, active = null;
  function cancel() {
    grant = null;
    if (!active) return;
    const old = active; active = null;
    clearTimeout(old.expiry);
    old.child.kill();
    old.fail?.();
  }
  return {
    authorize(bounds) { cancel(); grant = { bounds, time: now() }; },
    cancel,
    async start(startedAt) {
      if (!grant || now() - grant.time > 15000 || !Number.isFinite(startedAt) || Math.abs(now() - startedAt) > 15000) throw new Error("recording-source-required");
      const { bounds } = grant; grant = null;
      const child = spawn();
      const state = { child, id: randomUUID(), result: null, exited: false };
      active = state;
      state.expiry = setTimeout(() => { if (active === state) cancel(); }, 130000);
      state.expiry.unref?.();
      await new Promise((resolve, reject) => {
        let settled = false;
        const timeout = setTimeout(() => { fail(); if (active === state) cancel(); }, 5000);
        const fail = () => {
          if (!settled) { settled = true; clearTimeout(timeout); reject(new Error("recording-input-unavailable")); }
        };
        state.fail = fail;
        child.on("message", (message) => {
          if (active !== state) return;
          if (message.type === "ready" && !settled) { settled = true; clearTimeout(timeout); resolve(); }
          if (message.type === "result") { state.result = { events: message.events, limitations: message.limitations }; state.done?.(); }
          if (message.type === "failed") { fail(); state.exited = true; state.done?.(); child.kill(); }
        });
        child.on("exit", () => { state.exited = true; fail(); state.done?.(); });
        child.on("spawn", () => child.postMessage({ type: "start", startedAt, bounds }));
      }).catch((error) => { if (active === state) cancel(); throw error; });
      return { session_id: state.id };
    },
    async stop(id) {
      const state = active;
      if (!state || state.id !== id) throw new Error("recording-session-expired");
      if (!state.result && !state.exited) {
        await new Promise((resolve) => {
          const timeout = setTimeout(resolve, 2000);
          state.done = () => { clearTimeout(timeout); resolve(); };
          state.child.postMessage({ type: "stop" });
        });
      }
      const result = state.result;
      if (active === state) cancel();
      if (!result) throw new Error("recording-input-unavailable");
      return result;
    },
    cancelSession(id) { if (active?.id === id) cancel(); },
  };
}
module.exports = { createInputRecorder };
