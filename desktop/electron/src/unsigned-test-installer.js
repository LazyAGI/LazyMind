const fs = require("node:fs");
const path = require("node:path");
const { createHash } = require("node:crypto");
const { spawn: spawnProcess } = require("node:child_process");
const { assertPaths } = require("./unsigned-test-profile");
const failure = code => Object.assign(new Error(code), { code });

function createUnsignedTestInstaller({ profile, resourcesPath, currentVersion, setQuitting, quit, spawn = spawnProcess }) {
  let busy = false, terminal = false, disposed = false, preparedStage;
  const operations = new Set();
  const source = path.join(resourcesPath, "runtime", "bin", "unsigned-update-helper");
  const capability = config => {
    try {
      assertPaths(profile);
      const marker = JSON.parse(fs.readFileSync(path.join(resourcesPath, "unsigned-test.json"), "utf8"));
      if (disposed || config?.mode !== "unsigned-test" || config.platform !== "darwin" || config.signingIdentity !== "adhoc"
        || config.version !== currentVersion || marker.appId !== "ai.lazymind.desktop.test"
        || resourcesPath !== path.join(profile.application, "Contents", "Resources")
        || ["mode", "version", "releaseId", "platform", "arch", "signingIdentity", "trustDomain", "audience", "channel"].some(key => marker[key] !== config[key])
        || !fs.lstatSync(source).isFile()) throw new Error();
      return { supported: true };
    } catch { return { supported: false, reason: "native_install_unavailable" }; }
  };
  function invoke(helper, args, input, install = false) {
    return new Promise((resolve, reject) => {
      const child = spawn(helper, args, { shell: false, detached: install, stdio: ["pipe", "pipe", "ignore"] });
      let output = "", settled = false, accepted = false;
      const cancel = () => { if (!accepted) { child.kill(); finish(failure("native_install_unavailable")); } };
      operations.add(cancel);
      const timer = setTimeout(() => { child.kill(); finish(failure("native_handoff_unknown")); }, install ? 15000 : 120000);
      function finish(error, value) {
        if (settled) return; settled = true; clearTimeout(timer); operations.delete(cancel);
        if (error) reject(error); else resolve(value);
      }
      child.once("error", () => finish(failure("native_install_failed")));
      child.stdin.on("error", () => finish(failure("native_install_failed")));
      child.stdout.on("data", chunk => {
        if (settled) return;
        output += chunk;
        if (output.length > 4096) { finish(failure("helper_protocol")); return; }
        if (install && output.includes("\n")) {
          try {
            if (JSON.parse(output.trim()).status !== "accepted") throw new Error();
            accepted = true;
            child.unref(); child.stdout.destroy(); setQuitting(true); quit(); finish(null, { handoff: true });
          } catch { setQuitting(false); finish(failure("native_handoff_unknown")); }
        }
      });
      child.once("close", code => {
        if (install) { finish(failure("native_install_failed")); return; }
        try { if (code !== 0) throw new Error(); finish(null, JSON.parse(output)); }
        catch { finish(failure("native_prepare_failed")); }
      });
      child.stdin.end(input ? JSON.stringify(input) : "");
    });
  }
  async function discardPreparation() {
    if (!preparedStage || terminal) return;
    const result = await invoke(preparedStage.helper, ["discard", preparedStage.id]);
    if (result.status !== "discarded") throw failure("native_prepare_failed");
    preparedStage = undefined;
  }
  return {
    capability,
    discardPreparation,
    async prepare({ configuration, release, receipt }) {
      if (!capability(configuration).supported || busy || terminal) throw failure("native_install_unavailable");
      if (receipt?.releaseId !== release?.releaseId || receipt?.version !== release?.version) throw failure("native_identity_mismatch");
      busy = true;
      try {
        await discardPreparation();
        const bytes = fs.readFileSync(source), hash = createHash("sha256").update(bytes).digest("hex");
        const helper = path.join(profile.updater, "helpers", hash, "unsigned-update-helper");
        assertPaths({ ...profile, helper });
        fs.mkdirSync(path.dirname(helper), { recursive: true, mode: 0o700 });
        try { fs.writeFileSync(helper, bytes, { mode: 0o700, flag: "wx" }); } catch (error) { if (error.code !== "EEXIST") throw error; }
        if (createHash("sha256").update(fs.readFileSync(helper)).digest("hex") !== hash) throw failure("native_package_changed");
        const prepared = await invoke(helper, ["prepare"], { release, packagePath: receipt.packagePath });
        if (disposed || prepared.status !== "prepared" || !/^install-[0-9]+$/.test(prepared.id)) throw failure("native_prepare_failed");
        preparedStage = { helper, id: prepared.id };
        const stage = preparedStage;
        let attempt;
        return { install() {
          if (attempt) return attempt;
          if (disposed || terminal || preparedStage !== stage) return Promise.reject(failure("native_install_unavailable"));
          terminal = true; attempt = invoke(helper, ["install", prepared.id], null, true); return attempt;
        } };
      } finally { busy = false; }
    },
    dispose() { disposed = true; for (const cancel of operations) cancel(); },
  };
}
module.exports = { createUnsignedTestInstaller };
