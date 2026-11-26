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

function initializeTestProfile(app, {
  account = os.userInfo(), executable = app.getPath("exe"), platform = process.platform, systemApplications = "/Applications",
} = {}) {
  if (platform !== "darwin" || !account.username || account.uid === 0 || !path.isAbsolute(account.homedir)) {
    throw new Error("unsigned_test_account_required");
  }
  const application = [path.join(account.homedir, "Applications"), systemApplications]
    .map(directory => path.join(directory, "LazyMind.app"))
    .find(bundle => executable === path.join(bundle, "Contents", "MacOS", "LazyMindApp"));
  if (!application) throw new Error("unsigned_test_install_location");
  // Pending transactions protect the same installation and data as the ordinary
  // app. Update trust is selected by the embedded configuration, not the profile.
  const paths = { application, updater: path.join(account.homedir, "Library", "Application Support", "LazyMindUpdater") };
  assertTestPaths(paths);
  return paths;
}

module.exports = { assertTestPaths, initializeTestProfile };
