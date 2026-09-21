const { spawn } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");

const electronBinary = require("electron");
const electronRoot = path.resolve(__dirname, "..");
const sourceRoot = path.join(electronRoot, "src");

let child;
let stopping = false;
let restartTimer;
let operation = Promise.resolve();

function startElectron() {
  if (stopping) return;
  const spawned = spawn(electronBinary, [electronRoot], {
    cwd: electronRoot,
    env: process.env,
    stdio: "inherit",
  });
  child = spawned;
  spawned.once("error", (error) => {
    process.stderr.write(`desktop-dev: Electron failed to start: ${error.message}\n`);
  });
  spawned.once("exit", (code, signal) => {
    if (child === spawned) child = undefined;
    if (!stopping && code !== 0) {
      process.stderr.write(`desktop-dev: Electron exited (code=${code}, signal=${signal || "none"}); edit a main-process file to retry\n`);
    }
  });
}

function stopElectron() {
  const current = child;
  child = undefined;
  if (!current?.pid || current.exitCode !== null || current.signalCode !== null) return Promise.resolve();
  return new Promise((resolve) => {
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(forceTimer);
      resolve();
    };
    current.once("exit", finish);
    const forceTimer = setTimeout(() => {
      if (current.exitCode === null && current.signalCode === null) current.kill("SIGKILL");
      // Wait for exit: sending SIGKILL is not proof the old process stopped.
    }, 3000);
    current.kill("SIGTERM");
  });
}

function scheduleRestart(filename) {
  if (!filename || !filename.endsWith(".js") || filename.endsWith(".test.js")) return;
  clearTimeout(restartTimer);
  restartTimer = setTimeout(() => {
    process.stdout.write(`desktop-dev: restarting Electron after ${filename} changed\n`);
    operation = operation.then(stopElectron).then(() => startElectron());
  }, 200);
}

const watcher = fs.watch(sourceRoot, (_event, filename) => scheduleRestart(String(filename || "")));

async function shutdown(signal) {
  if (stopping) return;
  stopping = true;
  clearTimeout(restartTimer);
  watcher.close();
  await operation;
  await stopElectron();
  process.exit(signal === "SIGTERM" ? 0 : 130);
}

process.on("SIGINT", () => void shutdown("SIGINT"));
process.on("SIGTERM", () => void shutdown("SIGTERM"));
startElectron();
