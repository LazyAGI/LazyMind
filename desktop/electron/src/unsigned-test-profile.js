const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");

function testPaths({ platform = process.platform, account = os.userInfo() } = {}) {
  if (platform !== "darwin" || !account.username || account.username === "root" || !path.isAbsolute(account.homedir)) throw new Error("unsigned_test_account_required");
  const home = account.homedir, support = path.join(home, "Library", "Application Support");
  return { home, application: path.join(home, "Applications", "LazyMind Test.app"),
    runtime: path.join(support, "LazyMind Test"), profile: path.join(support, "lazymind-desktop-test"),
    updater: path.join(support, "LazyMindTestUpdater"), cli: path.join(home, ".lazymind-test"),
    logs: path.join(home, "Library", "Logs", "LazyMind Test") };
}
function assertPaths(paths) {
  for (const target of Object.values(paths)) for (let current = target;; current = path.dirname(current)) {
    try { if (fs.lstatSync(current).isSymbolicLink()) throw new Error("unsigned_test_path_alias"); }
    catch (error) { if (error.code !== "ENOENT") throw error; }
    if (path.dirname(current) === current) break;
  }
}
function initializeTestProfile({ app, executable = process.execPath, account, platform, build } = {}) {
  const paths = testPaths({ account, platform }); assertPaths(paths);
  if (executable !== path.join(paths.application, "Contents", "MacOS", "LazyMindApp")) throw new Error("unsigned_test_install_location");
  if (build?.lazymindUnsignedTest !== true || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(build.lazymindUnsignedRelease || "") || typeof build.version !== "string" || !build.version) throw new Error("unsigned_test_build_identity");
  // Inherited development overrides must not redirect the packaged test app.
  for (const key of Object.keys(process.env)) if (key.startsWith("LAZYMIND_")) delete process.env[key];
  process.env.LAZYMIND_HOME = paths.cli;
  process.env.LAZYMIND_UNSIGNED_EXPECTED_RELEASE = build.lazymindUnsignedRelease;
  process.env.LAZYMIND_UNSIGNED_EXPECTED_VERSION = build.version;
  return paths;
}
function activateTestProfile(app, paths) {
  assertPaths(paths); app.setName("LazyMind Test");
  fs.mkdirSync(paths.profile, { recursive: true, mode: 0o700 });
  app.setPath("userData", paths.profile); app.setPath("sessionData", paths.profile); app.setAppLogsPath(paths.logs);
}
module.exports = { testPaths, assertPaths, initializeTestProfile, activateTestProfile };
