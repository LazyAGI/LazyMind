const { execFile, spawn } = require("node:child_process");
const { createHash } = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");
const { promisify } = require("node:util");
const { assertTestPaths } = require("./unsigned-test-profile");

const failure = (code, handoffStarted = false) => Object.assign(new Error(code), { code, handoffStarted });
const run = promisify(execFile);
const hash = bytes => createHash("sha256").update(bytes).digest("hex");
// Both commands run bounded native work. In particular, install may copy
// and validate the entire application before acknowledging the handoff.
const helperTimeoutMs = 10 * 60_000;

function createUnsignedTestInstaller({ profile, currentVersion, resourcesPath, setQuitting, quit }) {
  const source = path.join(resourcesPath, "runtime", "bin", "unsigned-update-helper");
  let disposed = false;
  const capability = config => {
    try {
      assertTestPaths(profile);
      if (disposed || config?.mode !== "unsigned-test" || config.platform !== "darwin" || config.updaterProtocol !== 2 ||
        config.channel !== "personal-test" || config.audience === "official" || config.signingIdentity !== "adhoc" ||
        config.version !== currentVersion || resourcesPath !== path.join(profile.application, "Contents", "Resources")) {
        throw new Error();
      }
      const metadata = path.join(resourcesPath, "app-update.yml");
      if (fs.lstatSync(metadata).size > 65536 || !fs.lstatSync(source).isFile()) throw new Error();
      const { lazymind, updaterCacheDirName } = JSON.parse(fs.readFileSync(metadata, "utf8"));
      if (lazymind?.protocol !== 2 || updaterCacheDirName !== `LazyMind-${config.trustDomain}-${config.audience}-${config.channel}` ||
        ["mode", "version", "releaseId", "platform", "arch", "signingIdentity"].some(key => lazymind[key] !== config[key])) throw new Error();
      return { supported: true };
    } catch { return { supported: false, reason: "native_install_unavailable" }; }
  };
  return {
    capability,
    async prepare({ configuration, release, receipt }) {
      if (!capability(configuration).supported || receipt?.releaseId !== release?.releaseId) throw failure("native_install_unavailable");
      const bytes = fs.readFileSync(source), sha = hash(bytes);
      const helper = path.join(profile.updater, "helpers", sha, "unsigned-update-helper");
      assertTestPaths({ helper });
      fs.mkdirSync(path.dirname(helper), { recursive: true, mode: 0o700 });
      try { fs.writeFileSync(helper, bytes, { mode: 0o700, flag: "wx" }); }
      catch (error) { if (error.code !== "EEXIST") throw error; }
      if (hash(fs.readFileSync(helper)) !== sha) throw failure("native_package_changed");
      let prepared;
      try {
        const result = await run(helper, ["prepare"], { timeout: helperTimeoutMs, maxBuffer: 4096 });
        prepared = JSON.parse(result.stdout);
      } catch { throw failure("native_prepare_failed"); }
      if (disposed || prepared.status !== "prepared" || !/^[a-f0-9]{32}$/.test(prepared.id) || prepared.releaseId !== release.releaseId) {
        throw failure("native_prepare_failed");
      }
      let pending;
      return { install() {
        if (disposed) return Promise.reject(failure("native_install_unavailable"));
        if (!pending) pending = new Promise((resolve, reject) => {
          let text = "", settled = false;
          const child = spawn(helper, ["install", prepared.id], { detached: true, shell: false, stdio: ["ignore", "pipe", "ignore"] });
          const timer = setTimeout(() => {
            child.kill();
            fail("native_handoff_unknown");
          }, helperTimeoutMs + 5_000);
          function fail(code) {
            if (settled) return;
            settled = true;
            clearTimeout(timer);
            reject(failure(code, true));
          }
          child.once("error", () => fail("native_install_failed"));
          child.once("close", () => fail("native_install_failed"));
          child.stdout.on("data", chunk => {
            if (settled) return;
            text += chunk;
            if (text.length > 4096) { child.kill(); fail("native_handoff_unknown"); return; }
            if (!text.includes("\n")) return;
            try {
              if (JSON.parse(text).status !== "accepted") throw new Error();
              child.unref();
              child.stdout.destroy();
              setQuitting(true);
              quit();
              settled = true;
              clearTimeout(timer);
              resolve({ handoff: true });
            } catch { fail("native_handoff_unknown"); }
          });
        });
        return pending;
      } };
    },
    dispose() { disposed = true; },
  };
}

module.exports = { createUnsignedTestInstaller };
