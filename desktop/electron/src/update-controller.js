const { isTrustedNotificationSender } = require("./native-notifications");
const { createUpdateInstallFlow } = require("./update-install-flow");
const clone = (value) => JSON.parse(JSON.stringify(value));
const failure = (code) => Object.assign(new Error(code), { code });
const reasonFor = (error) => /^[a-z_]{1,64}$/.test(error?.code || "") ? error.code : "check_failed";

function createDesktopUpdater({ currentVersion, invokeHelper, readLocalState, revealPackage = () => {},
  downloadIntent, installIntent, nativeInstaller, runtime, clock = { setTimeout, clearTimeout }, random = Math.random, report = () => {} }) {
  let state = { revision: 0, phase: "idle", currentVersion, canInstall: false, route: [] };
  let config, checking, checkAbort, active, queued, offer, receipt, timer, started, disposed = false;
  let interrupted, resuming, recordFailure;
  let installFlow;
  const listeners = new Set();
  const getState = () => clone(state);
  const publish = (patch) => {
    if (disposed) return;
    state = { ...state, ...patch, revision: state.revision + 1, canInstall: Boolean(installFlow?.supported(config?.config)) };
    // A destroyed renderer must never turn a successful transfer into failure.
    for (const listener of listeners) { try { listener(getState()); } catch { /* Renderer lifecycle is independent of downloading. */ } }
  };
  const clearOffer = () => {
    offer = undefined; receipt = undefined;
    return { releaseId: undefined, nextVersion: undefined, targetVersion: undefined, route: [], downloadedBytes: 0, totalBytes: 0 };
  };
  const offerFields = () => offer ? { releaseId: offer.next.releaseId, nextVersion: offer.next.version,
    targetVersion: offer.targetVersion || offer.next.version, route: [...offer.route], downloadedBytes: 0,
    totalBytes: offer.next.package.size + (offer.next.feed?.size || 0) + (offer.next.components?.size || 0) } : clearOffer();

  const check = (continuing = false, explicit = false) => {
    if (disposed || active || queued || receipt || (!continuing && (interrupted || resuming || installFlow?.pending()))) return Promise.resolve(getState());
    if (checking) return checking;
    if (recordFailure && !continuing) {
      if (!explicit) return Promise.resolve(getState());
      try { downloadIntent?.clear(); recordFailure = undefined; }
      catch { recordFailure = "update_record_write"; publish({ phase: "error", reason: recordFailure }); return Promise.resolve(getState()); }
    }
    checkAbort = new AbortController();
    const signal = checkAbort.signal;
    const preserveInstalled = !explicit && state.phase === "installed";
    publish({ ...clearOffer(), phase: preserveInstalled ? "installed" : "checking", reason: undefined });
    checking = (async () => {
      try {
        if (!config) config = await invokeHelper("config", {}, { signal });
        if (disposed) return getState();
        if (config.status === "disabled") { publish({ phase: "disabled", reason: "not_configured" }); return getState(); }
        if (config.status !== "enabled" || !config.config) throw failure("helper_protocol");
        if (config.config.version && config.config.version !== currentVersion) throw failure("build_identity_mismatch");
        const local = await readLocalState();
        const decision = await invokeHelper("discover", { state: local }, { signal });
        if (disposed) return getState();
        if (resuming?.cancelled && recordFailure) throw failure(recordFailure);
        if (["direct", "bridge"].includes(decision.status)) {
          if (!decision.next || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(decision.next.releaseId || "")
            || typeof decision.next.version !== "string" || !Array.isArray(decision.route) || decision.route[0] !== decision.next.releaseId
            || !Number.isSafeInteger(decision.next.package?.size) || decision.next.package.size <= 0) throw failure("helper_protocol");
          offer = clone(decision);
          const fields = offerFields(); if (!Number.isSafeInteger(fields.totalBytes)) throw failure("helper_protocol");
          publish({ ...fields, phase: "available", reason: "compatible", channel: config.config.channel });
        } else if (decision.status === "up_to_date") publish({ phase: preserveInstalled ? "installed" : "up_to_date", reason: decision.reason, channel: config.config.channel });
        else if (["blocked", "manual_required", "unsupported_system"].includes(decision.status)) publish({ phase: "blocked", reason: decision.reason, channel: config.config.channel });
        else throw failure("helper_protocol");
      } catch (error) {
        if (!disposed) { const reason = reasonFor(error); publish({ ...clearOffer(), phase: "error", reason }); report(reason); }
      } finally { checking = undefined; checkAbort = undefined; }
      return getState();
    })();
    return checking;
  };

  const launchDownload = (releaseId) => {
    if (disposed || !offer || offer.next.releaseId !== releaseId) return Promise.reject(failure("offer_changed"));
    const operation = { abort: new AbortController(), cancelled: false, promise: null };
    active = operation; receipt = undefined;
    operation.promise = Promise.resolve().then(async () => {
      try {
        if (operation.cancelled || disposed) return;
        try { downloadIntent?.write({ version: 1, sourceVersion: currentVersion, releaseId, nextVersion: offer.next.version }); recordFailure = undefined; }
        catch { recordFailure = "update_record_write"; throw failure(recordFailure); }
        interrupted = undefined;
        publish({ phase: "downloading", reason: undefined, downloadedBytes: 0, pendingDownload: false });
        const local = await readLocalState();
        if (operation.cancelled || disposed) return;
        const result = await invokeHelper("download", { releaseId, state: local }, { signal: operation.abort.signal,
          onProgress: (progress) => { if (!disposed && active === operation && !operation.cancelled) publish({ phase: "downloading", downloadedBytes: progress.downloadedBytes, totalBytes: progress.totalBytes }); } });
        if (operation.cancelled || disposed) return;
        if (result.status !== "downloaded" || result.releaseId !== releaseId || typeof result.packagePath !== "string" || !result.packagePath) throw failure("helper_protocol");
        // Keep the measured source profile with the private receipt so even a
        // pre-install recovery failure can later confirm a manual repair.
        receipt = { ...result, dataProfile: local.dataProfile };
        publish({ phase: "downloaded", reason: undefined, downloadedBytes: state.totalBytes });
      } catch (error) {
        if (!disposed && !operation.cancelled) {
          const reason = reasonFor(error);
          if (reason === "offer_changed" || reason === "policy_replay" || reason === "policy_signature") clearOffer();
          publish({ ...(offer ? {} : clearOffer()), phase: "error", reason }); report(reason);
        }
      } finally {
        if (active === operation) active = undefined;
        if (!disposed && operation.cancelled && !queued) publish({ ...offerFields(), phase: recordFailure ? "error" : offer ? "available" : "idle", reason: recordFailure });
      }
    }).then(getState);
    return operation.promise;
  };

  const download = (releaseId) => {
    if (typeof releaseId !== "string" || !offer || offer.next.releaseId !== releaseId || checking || disposed) return Promise.reject(failure("offer_changed"));
    if (queued) return queued.promise;
    if (active && !active.cancelled) return active.promise;
    if (!active) return launchDownload(releaseId);
    // A cancelled child may still hold its cache lock and be removing a partial
    // file. Accept the retry intent now, but serialize the new process after it.
    const pending = { cancelled: false, promise: null };
    queued = pending;
    pending.promise = active.promise.catch(() => {}).then(() => {
      if (queued === pending) queued = undefined;
      return disposed || pending.cancelled ? getState() : launchDownload(releaseId);
    });
    return pending.promise;
  };

  const cancel = () => {
    if (disposed) return getState();
    if (resuming) resuming.cancelled = true;
    if (queued) { queued.cancelled = true; queued = undefined; }
    if (active && !active.cancelled) { active.cancelled = true; active.abort.abort(); publish({ phase: "cancelling", reason: undefined }); }
    try { downloadIntent?.clear(); recordFailure = undefined; }
    catch { recordFailure = "update_record_write"; publish({ phase: active ? "cancelling" : "error", reason: recordFailure }); return getState(); }
    interrupted = undefined;
    publish({ pendingDownload: false, ...(!active && !checking ? { ...(offer ? {} : clearOffer()), phase: receipt ? "downloaded" : offer ? "available" : "idle", reason: undefined } : {}) });
    return getState();
  };
  const resumeDownload = () => {
    if (resuming) return resuming.promise;
    if (!interrupted || disposed) return Promise.reject(failure("download_not_ready"));
    const pending = { releaseId: interrupted.releaseId, cancelled: false, promise: null };
    resuming = pending;
    pending.promise = Promise.resolve().then(async () => {
      if (pending.cancelled || disposed) return getState();
      await check(true);
      if (pending.cancelled || disposed) return getState();
      if (!offer) return getState();
      if (offer.next.releaseId !== pending.releaseId) {
        try { downloadIntent?.clear(); recordFailure = undefined; } catch { recordFailure = "update_record_write"; publish({ reason: recordFailure, phase: "error" }); return getState(); }
        interrupted = undefined;
        publish({ phase: "available", reason: "offer_changed", pendingDownload: false });
        return getState();
      }
      return download(pending.releaseId);
    }).finally(() => { if (resuming === pending) resuming = undefined; });
    return pending.promise;
  };
  const schedule = () => {
    if (disposed || state.phase === "disabled") return;
    timer = clock.setTimeout(async () => { timer = undefined; if (disposed) return; await check(); schedule(); }, (25 + random() * 10) * 60_000);
    timer?.unref?.();
  };
  try {
    const saved = downloadIntent?.read();
    if (saved?.sourceVersion === currentVersion) {
      interrupted = saved;
      publish({ phase: "interrupted", pendingDownload: true, releaseId: saved.releaseId, nextVersion: saved.nextVersion });
    } else if (saved) downloadIntent.clear();
  } catch { recordFailure = "update_record_read"; publish({ phase: "error", reason: recordFailure }); }
  if (installIntent) installFlow = createUpdateInstallFlow({ currentVersion, store: installIntent, nativeInstaller, runtime, clock,
    context: () => ({ configuration: config?.config, offer, receipt }), download, cancelDownload: cancel,
    rediscover: async () => { const local = await readLocalState(); return { local, decision: await invokeHelper("discover", { state: local }) }; },
    publish, getState, revealPackage, clearDownloadIntent: () => downloadIntent?.clear(), invalidateOffer: clearOffer,
  });
  return { getState, check: () => check(false, true),
    download: (releaseId) => installFlow?.pending() ? Promise.reject(failure("native_update_busy")) : download(releaseId),
    cancel: () => installFlow?.pending() ? installFlow.later() : cancel(),
    resumeDownload: () => installFlow?.pending() ? Promise.reject(failure("native_update_busy")) : resumeDownload(),
    async install(releaseId) {
      if (!installFlow) throw failure("native_install_unavailable");
      installFlow.assertReady();
      if (!offer) await check(true);
      return installFlow.begin(releaseId || state.releaseId);
    },
    async later() { return installFlow ? installFlow.later() : cancel(); },
    subscribe(listener) { listeners.add(listener); return () => listeners.delete(listener); },
    start() { if (!started) started = Promise.resolve(installFlow?.startup()).then(() => check()).then(() => { schedule(); return getState(); }); return started; },
    async revealDownloaded() { if (receipt) await revealPackage(receipt.packagePath); else if (installFlow) await installFlow.reveal(); else throw failure("download_not_ready"); },
    dispose() { if (disposed) return; disposed = true; installFlow?.dispose(); if (timer) clock.clearTimeout(timer); if (queued) queued.cancelled = true; active?.abort.abort(); checkAbort?.abort(); listeners.clear(); },
  };
}

function registerUpdateIPC({ ipcMain, updater, authorize }) {
  const actions = { status: () => updater.getState(), check: () => updater.check(), download: (releaseId) => updater.download(releaseId), cancel: () => updater.cancel(), reveal: () => updater.revealDownloaded(), resume: () => updater.resumeDownload(), install: (releaseId) => updater.install(releaseId), later: () => updater.later() };
  for (const [name, action] of Object.entries(actions)) ipcMain.handle(`lazymind:update:${name}`, async (event, value) => {
    if (!authorize(event)) throw failure("update_sender_untrusted");
    return action(value);
  });
}

function sendUpdateState(window, origin, state) {
  if (!window) return false;
  const contents = window.webContents;
  if (!isTrustedNotificationSender({ sender: contents, senderFrame: contents?.mainFrame }, window, origin)) return false;
  try { contents.send("lazymind:update:state", state); return true; } catch { return false; }
}
module.exports = { createDesktopUpdater, registerUpdateIPC, sendUpdateState };
