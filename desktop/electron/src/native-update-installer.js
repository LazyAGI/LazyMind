const fs = require("node:fs");
const path = require("node:path");
const http = require("node:http");
const { createHash, randomBytes, timingSafeEqual } = require("node:crypto");
const { spawn: spawnProcess } = require("node:child_process");

const failure = (code, handoffStarted = false) => Object.assign(new Error(code), { code, handoffStarted });
const id = value => typeof value === "string" && /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(value);

async function verifiedFile(filename, expected) {
  let file;
  try {
    if (!path.isAbsolute(filename) || !Number.isSafeInteger(expected.size) || expected.size <= 0
      || !/^[a-f0-9]{64}$/.test(expected.sha256)) throw new Error();
    if (!(await fs.promises.lstat(filename)).isFile()) throw new Error();
    file = await fs.promises.open(filename, fs.constants.O_RDONLY | (fs.constants.O_NOFOLLOW || 0));
    const stat = await file.stat();
    if (!stat.isFile() || stat.size !== expected.size) throw new Error();
    const sha256 = createHash("sha256"), sha512 = createHash("sha512");
    let size = 0;
    for await (const chunk of file.createReadStream({ start: 0, autoClose: false })) {
      size += chunk.length; sha256.update(chunk); sha512.update(chunk);
    }
    if (size !== expected.size || sha256.digest("hex") !== expected.sha256) throw new Error();
    return { file, size, sha512: sha512.digest("base64") };
  } catch {
    await file?.close();
    throw failure("native_package_changed");
  }
}

async function privateFeed(source, release, platform) {
  const authorization = `Bearer ${randomBytes(32).toString("hex")}`;
  const filename = `${release.releaseId}-${release.arch}.${platform === "win32" ? "exe" : "zip"}`;
  const metadataPath = platform === "win32" ? "/latest.yml" : "/latest-mac.yml";
  const info = { version: release.version, files: [{ url: filename, size: source.size, sha512: source.sha512 }],
    path: filename, sha512: source.sha512, releaseDate: new Date().toISOString() };
  const metadata = Buffer.from(JSON.stringify(info));
  const server = http.createServer((request, response) => {
    const received = Buffer.from(request.headers.authorization || ""), wanted = Buffer.from(authorization);
    if (received.length !== wanted.length || !timingSafeEqual(received, wanted)) { response.writeHead(401); response.end(); return; }
    if (!["GET", "HEAD"].includes(request.method)) { response.writeHead(405); response.end(); return; }
    let pathname;
    try { pathname = new URL(request.url, "http://127.0.0.1").pathname; } catch { response.writeHead(400); response.end(); return; }
    if (pathname === metadataPath) {
      response.writeHead(200, { "Content-Type": "application/json", "Content-Length": metadata.length });
      response.end(request.method === "HEAD" ? undefined : metadata); return;
    }
    if (pathname !== `/${filename}`) { response.writeHead(404); response.end(); return; }
    response.writeHead(200, { "Content-Type": "application/octet-stream", "Content-Length": source.size });
    if (request.method === "HEAD") { response.end(); return; }
    const stream = source.file.createReadStream({ start: 0, autoClose: false });
    stream.on("error", () => response.destroy());
    response.on("close", () => stream.destroy());
    stream.pipe(response);
  });
  await new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", resolve); });
  return { url: `http://127.0.0.1:${server.address().port}/`, headers: { Authorization: authorization },
    close: () => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }) };
}

function createNativeUpdateInstaller({ platform = process.platform, arch = process.arch, currentVersion, isPackaged,
  resourcesPath, createUpdater, nativeAutoUpdater, spawn = spawnProcess, setQuitting = () => {}, quit,
  handoffTimeoutMs = 10 * 60_000 } = {}) {
  let updater, disposed = false, preparing = false, installing = false, terminal = false, currentStage;
  let closeFeed, cancelHandoff;
  const nativePlatform = platform === "win32" ? "windows" : platform;
  const nativeArch = arch === "x64" ? "amd64" : arch;
  const capability = configuration => {
    try {
      if (disposed || !isPackaged || !["win32", "darwin"].includes(platform) || configuration?.mode !== "official"
        || configuration.version !== currentVersion || configuration.platform !== nativePlatform || configuration.arch !== nativeArch
        || configuration.updaterProtocol !== 1 || ![configuration.trustDomain, configuration.audience, configuration.channel, configuration.releaseId].every(id)
        || typeof configuration.signingIdentity !== "string" || !configuration.signingIdentity.trim()) throw new Error();
      const filename = path.join(resourcesPath, "app-update.yml");
      const stat = fs.lstatSync(filename);
      if (!stat.isFile() || stat.size > 64 * 1024) throw new Error();
      const meta = JSON.parse(fs.readFileSync(filename, "utf8"));
      const expected = { protocol: 1, releaseId: configuration.releaseId, version: currentVersion, mode: "official",
        platform: nativePlatform, arch: nativeArch, signingIdentity: configuration.signingIdentity };
      if (!meta.lazymind || Object.entries(expected).some(([key, value]) => meta.lazymind[key] !== value)
        || meta.updaterCacheDirName !== `LazyMind-${configuration.trustDomain}-${configuration.audience}-${configuration.channel}`
        || (platform === "win32" && (!Array.isArray(meta.publisherName) || meta.publisherName.length !== 1 || meta.publisherName[0] !== configuration.signingIdentity))) throw new Error();
      return { supported: true };
    } catch { return { supported: false, reason: "native_install_unavailable" }; }
  };

  const windowsInstall = filename => new Promise((resolve, reject) => {
    let child, settled = false;
    const finish = error => {
      if (settled) return;
      settled = true; clearTimeout(timer); cancelHandoff = undefined;
      if (error) reject(error); else resolve({ handoff: true });
    };
    const timer = setTimeout(() => { terminal = true; child?.unref(); finish(failure("native_handoff_unknown", true)); }, handoffTimeoutMs);
    cancelHandoff = () => { terminal = true; finish(failure("native_handoff_unknown", true)); };
    try {
      child = spawn(filename, ["--updated", "/S", "--force-run", "--simple-install"], { shell: false, detached: true, windowsHide: true, stdio: "ignore" });
      child.once("error", () => finish(failure("native_install_failed")));
      child.once("spawn", () => {
        if (settled) return;
        terminal = true;
        child.unref();
        try { setQuitting(true); quit(); finish(); }
        catch { finish(failure("native_handoff_unknown", true)); }
      });
    } catch { finish(failure("native_install_failed")); }
  });

  const macInstall = () => new Promise((resolve, reject) => {
    const native = nativeAutoUpdater || require("electron").autoUpdater;
    let settled = false;
    const finish = error => {
      if (settled) return;
      settled = true; clearTimeout(timer); cancelHandoff = undefined;
      native.removeListener("before-quit-for-update", accepted);
      updater.removeListener("error", failed);
      if (error) { setQuitting(false); reject(error); } else resolve({ handoff: true });
    };
    const accepted = () => finish();
    const failed = () => finish(failure("native_install_failed", true));
    const timer = setTimeout(() => finish(failure("native_handoff_unknown", true)), handoffTimeoutMs);
    cancelHandoff = () => finish(failure("native_handoff_unknown", true));
    native.once("before-quit-for-update", accepted);
    updater.once("error", failed);
    terminal = true;
    try { setQuitting(true); updater.quitAndInstall(); }
    catch { failed(); }
  });

  return {
    capability,
    async prepare({ configuration, release, receipt }) {
      if (!capability(configuration).supported) throw failure("native_install_unavailable");
      if (preparing || installing || terminal) throw failure("native_update_busy");
      if (!release || !id(release.releaseId) || typeof release.version !== "string" || !release.version || release.version.length > 128
        || release.platform !== configuration.platform || release.arch !== configuration.arch || release.signingIdentity !== configuration.signingIdentity
        || receipt?.releaseId !== release.releaseId || receipt.version !== release.version) throw failure("native_identity_mismatch");
      const selected = { releaseId: release.releaseId, version: release.version, arch: release.arch };
      const expected = { sha256: release.package?.sha256, size: release.package?.size };
      preparing = true; currentStage = undefined;
      let source, feed;
      try {
        source = await verifiedFile(receipt.packagePath, expected);
        if (disposed) throw failure("native_install_unavailable");
        if (!updater) {
          updater = createUpdater ? createUpdater() : new (require("electron-updater")[platform === "win32" ? "NsisUpdater" : "MacUpdater"])();
          updater.logger = null;
          // The library has its own error listener in production; retain a
          // handler for late native events even after a handoff has failed.
          updater.on("error", () => {});
        }
        updater.autoDownload = false; updater.autoInstallOnAppQuit = false;
        // MacUpdater uses this flag to choose native quitAndInstall over plain
        // app.quit. Windows relaunch is explicit in our fixed NSIS arguments.
        updater.autoRunAppAfterInstall = platform === "darwin";
        updater.channel = "latest"; updater.allowPrerelease = true; updater.allowDowngrade = false;
        updater.disableWebInstaller = true; updater.disableDifferentialDownload = true;
        await updater.netSession.setProxy({ mode: "direct" });
        feed = await privateFeed(source, selected, platform);
        closeFeed = feed.close;
        updater.requestHeaders = feed.headers;
        updater.setFeedURL({ provider: "generic", url: feed.url });
        const result = await updater.checkForUpdates();
        if (disposed || result?.isUpdateAvailable !== true || result.updateInfo?.version !== selected.version) throw failure("native_identity_mismatch");
        const files = await updater.downloadUpdate();
        if (disposed || !Array.isArray(files) || files.length !== 1 || typeof files[0] !== "string") throw failure("native_prepare_failed");
        const verified = await verifiedFile(files[0], expected);
        await verified.file.close();
        const stage = { filename: files[0], attempt: null };
        currentStage = stage;
        return { install() {
          if (stage.attempt) return stage.attempt;
          stage.attempt = (async () => {
            if (disposed || currentStage !== stage || preparing || installing || terminal) throw failure("native_install_unavailable");
            installing = true;
            try {
              const final = await verifiedFile(stage.filename, expected);
              await final.file.close();
              if (disposed || currentStage !== stage) throw failure("native_install_unavailable");
              return await (platform === "win32" ? windowsInstall(stage.filename) : macInstall());
            } finally { installing = false; }
          })();
          return stage.attempt;
        } };
      } catch (error) {
        if (error?.code?.startsWith("native_")) throw error;
        throw failure(error?.code === "ERR_UPDATER_INVALID_SIGNATURE" ? "native_signature_invalid" : "native_prepare_failed");
      } finally {
        if (feed) await feed.close();
        closeFeed = undefined;
        await source?.file.close();
        preparing = false;
      }
    },
    dispose() {
      disposed = true; currentStage = undefined;
      cancelHandoff?.();
      void closeFeed?.();
    },
  };
}
module.exports = { createNativeUpdateInstaller };
