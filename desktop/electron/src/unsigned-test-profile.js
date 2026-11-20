const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

function assertTestPaths(paths) {
  for (const target of Object.values(paths)) {
    for (let current = target;; current = path.dirname(current)) {
      if (fs.lstatSync(current, { throwIfNoEntry: false })?.isSymbolicLink()) throw new Error("unsigned_test_path_alias");
      if (current === path.dirname(current)) break;
    }
  }
}

function initializeTestProfile(app, { account = os.userInfo(), executable = process.execPath, platform = process.platform } = {}) {
  if (platform !== "darwin" || !account.username || account.uid === 0 || !path.isAbsolute(account.homedir)) {
    throw new Error("unsigned_test_account_required");
  }
  const home = account.homedir, support = path.join(home, "Library", "Application Support");
  const paths = {
    application: path.join(home, "Applications", "LazyMind Test.app"),
    runtime: path.join(support, "LazyMind Test"),
    profile: path.join(support, "lazymind-desktop-test"),
    updater: path.join(support, "LazyMindTestUpdater"),
    cli: path.join(home, ".lazymind-test"),
    logs: path.join(home, "Library", "Logs", "LazyMind Test"),
  };
  assertTestPaths(paths);
  if (executable !== path.join(paths.application, "Contents", "MacOS", "LazyMindApp")) {
    throw new Error("unsigned_test_install_location");
  }
  // A packaged test suite must never inherit a production runtime endpoint,
  // database override, install root, or cloud token from a shell.
  for (const key of Object.keys(process.env)) if (key.startsWith("LAZYMIND_")) delete process.env[key];
  process.env.LAZYMIND_HOME = paths.cli;
  process.env.LAZYMIND_HOST_HOME = home;
  process.env.LAZYMIND_DESKTOP_RUNTIME_ROOT = paths.runtime;
  fs.mkdirSync(paths.profile, { recursive: true, mode: 0o700 });
  app.setName("LazyMind Test");
  app.setPath("userData", paths.profile);
  app.setPath("sessionData", paths.profile);
  app.setAppLogsPath(paths.logs);
  return paths;
}

module.exports = { assertTestPaths, initializeTestProfile };
