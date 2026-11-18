const { spawn: spawnProcess } = require("node:child_process");
const MAX_LINE_BYTES = 2 * 1024 * 1024;
const errorFor = (code) => Object.assign(new Error(code), { code });
const abortError = () => Object.assign(errorFor("cancelled"), { name: "AbortError" });

function createUpdateHelperClient({ helperPath, spawn = spawnProcess, clock = { setTimeout, clearTimeout } }) {
  return function invoke(command, request = {}, { signal, onProgress = () => {} } = {}) {
    if (!["config", "discover", "download"].includes(command)) return Promise.reject(errorFor("helper_command"));
    if (signal?.aborted) return Promise.reject(abortError());
    let input;
    try { input = JSON.stringify(request); } catch { return Promise.reject(errorFor("request_invalid")); }
    if (Buffer.byteLength(input) > 1024 * 1024) return Promise.reject(errorFor("request_too_large"));
    return new Promise((resolve, reject) => {
      let child, buffer = "", result, pendingError, closed = false, stopping = false, cleanupTimer, watchdog;
      const cleanup = () => {
        if (cleanupTimer) clock.clearTimeout(cleanupTimer);
        if (watchdog) clock.clearTimeout(watchdog);
        signal?.removeEventListener("abort", onAbort);
      };
      const stop = (error) => {
        pendingError ||= error;
        if (closed || stopping) return;
        stopping = true;
        if (command === "download" && !child.stdin.destroyed) {
          // Keep stdin open: the Go command must honor the control message,
          // clean its partial file and release the cache lock before close.
          child.stdin.write('{"cancel":true}\n');
        } else child.kill("SIGTERM");
        cleanupTimer = clock.setTimeout(() => { if (!closed) child.kill("SIGKILL"); }, 5000);
        cleanupTimer?.unref?.();
      };
      const onAbort = () => stop(abortError());
      const parseLine = (line) => {
        if (!line.trim() || pendingError) return;
        let value;
        try { value = JSON.parse(line); } catch { stop(errorFor("helper_protocol")); return; }
        if (!value || typeof value !== "object" || Array.isArray(value) || typeof value.status !== "string" || result) { stop(errorFor("helper_protocol")); return; }
        if (value.status === "downloading") {
          if (command !== "download" || !Number.isSafeInteger(value.downloadedBytes) || !Number.isSafeInteger(value.totalBytes)
            || value.downloadedBytes < 0 || value.totalBytes <= 0 || value.downloadedBytes > value.totalBytes) { stop(errorFor("helper_protocol")); return; }
          try { onProgress(value); } catch { stop(errorFor("helper_protocol")); }
        } else result = value;
      };
      try { child = spawn(helperPath, [command], { shell: false, windowsHide: true, stdio: ["pipe", "pipe", "pipe"] }); }
      catch { reject(errorFor("helper_unavailable")); return; }
      child.stdout.setEncoding("utf8");
      child.stdout.on("data", (chunk) => {
        if (pendingError || closed) return;
        buffer += chunk;
        if (Buffer.byteLength(buffer) > MAX_LINE_BYTES) { buffer = ""; stop(errorFor("helper_protocol")); return; }
        let newline;
        while ((newline = buffer.indexOf("\n")) !== -1) { const line = buffer.slice(0, newline); buffer = buffer.slice(newline + 1); parseLine(line); }
      });
      // Never forward unstructured process output or paths to the renderer.
      child.stderr.resume();
      child.stdin.on("error", () => { if (command !== "config" && !closed && !stopping && !result) stop(errorFor("helper_input")); });
      child.on("error", () => { pendingError ||= errorFor("helper_unavailable"); });
      child.on("close", (code) => {
        closed = true;
        if (buffer.trim() && !pendingError) parseLine(buffer);
        cleanup();
        if (pendingError) { reject(pendingError); return; }
        if (code !== 0 || !result || result.status === "check_failed") {
          const reason = /^[a-z_]{1,64}$/.test(result?.reason || "") ? result.reason : "helper_failed";
          reject(errorFor(reason)); return;
        }
        resolve(result);
      });
      watchdog = clock.setTimeout(() => stop(errorFor("network_timeout")), command === "download" ? 2 * 3600_000 + 5000 : 35_000);
      watchdog?.unref?.();
      signal?.addEventListener("abort", onAbort, { once: true });
      if (signal?.aborted) { onAbort(); return; }
      if (command === "download") child.stdin.write(`${input}\n`);
      else child.stdin.end(command === "config" ? undefined : input);
    });
  };
}
module.exports = { createUpdateHelperClient };
