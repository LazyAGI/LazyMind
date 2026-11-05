const failure = code => Object.assign(new Error(code), { code });
const reasonFor = error => /^[a-z_]{1,64}$/.test(error?.code || "") ? error.code : "update_failed";

function createUpdateInstallFlow({ currentVersion, store, nativeInstaller, runtime, context, download, cancelDownload, rediscover,
  publish, getState, revealPackage, clearDownloadIntent, invalidateOffer = () => ({}), clock = { setTimeout, clearTimeout }, now = Date.now }) {
  let saved, operation, disposed = false, epoch = now(), loadFailed = false, repairRequired = false;
  const write = value => {
    try {
      const result = store.write(value);
      if (result?.then) return result.then(() => { saved = value; }, () => { throw failure("update_record_write"); });
      saved = value;
    } catch { throw failure("update_record_write"); }
  };
  const supported = configuration => Boolean(nativeInstaller?.capability(configuration).supported && runtime && store);
  const fields = () => saved ? { releaseId: saved.releaseId, nextVersion: saved.nextVersion, pendingUpdate: true, pendingDownload: false,
    packageAvailable: Boolean(saved.packagePath) } : {};
  try { saved = store?.read(); }
  catch { loadFailed = true; publish({ phase: "repair", reason: "update_record_read", canInstall: false }); }
  if (saved) publish({ ...fields(), phase: saved.phase === "pending" ? "interrupted" : "repair", reason: saved.phase === "pending" ? undefined : "update_incomplete" });
  const alive = op => !disposed && operation === op && !op.cancelled;
  const delay = (op, ms) => new Promise(resolve => { op.wake = resolve; op.timer = clock.setTimeout(() => { op.timer = undefined; op.wake = undefined; resolve(); }, ms); op.timer?.unref?.(); });
  const resume = async op => {
    if (op.epoch) { await runtime.resume(op.epoch); op.epoch = undefined; }
  };
  const verifyComponents = async (release, receipt) => {
    if (typeof runtime.verifyComponents !== "function") throw failure("data_unknown");
    const result = await runtime.verifyComponents(release, receipt);
    if (!/^[A-Za-z0-9._-]{1,128}$/.test(result?.dataProfile || "")) throw failure("data_unknown");
    return result;
  };
  const run = async op => {
    try {
      await download(saved.releaseId);
      if (!alive(op)) return;
      const selected = context();
      if (!selected.receipt || selected.offer?.next.releaseId !== saved.releaseId) throw failure(getState().reason || "download_not_ready");
      write({ ...saved, packagePath: selected.receipt.packagePath, dataProfile: selected.receipt.dataProfile });
      publish({ packageAvailable: true, phase: "preparing", pendingDownload: false });
      if (selected.configuration.updaterProtocol === 2) {
        if (typeof runtime.prepareComponents !== "function") throw failure("updater_protocol");
        const plan = await runtime.prepareComponents(selected.offer.next, selected.receipt);
        if (!alive(op)) return;
        if (plan?.status !== "prepared" || !/^[a-f0-9]{32}$/.test(plan.transactionId || "")
          || typeof plan.sourceHadRAG !== "boolean" || !/^[A-Za-z0-9._-]{1,128}$/.test(plan.dataProfile || "")
          || (plan.sourceHadRAG ? plan.componentMode !== "sync-target" || !/^[a-f0-9]{64}$/.test(plan.targetRAG?.revision || "")
            : plan.componentMode !== "absent" || plan.targetRAG !== undefined)) throw failure("component_state_unknown");
        write({ ...saved, ...plan, version: 2, phase: "pending" });
      } else {
        await verifyComponents(selected.offer.next, selected.receipt);
      }
      if (!alive(op)) return;
      const stage = await nativeInstaller.prepare({ configuration: selected.configuration, release: selected.offer.next, receipt: selected.receipt });
      if (!alive(op)) return;
      for (;;) {
        publish({ phase: "waiting", secondsRemaining: undefined, reason: undefined });
        const status = await runtime.status();
        if (!alive(op)) return;
        if (status.active !== 0) { await delay(op, 2000); if (!alive(op)) return; continue; }
        op.epoch = ++epoch;
        const locked = await runtime.pause(op.epoch);
        if (!alive(op)) return;
        if (!locked.paused || locked.epoch !== op.epoch) throw failure("task_state_unknown");
        if (locked.active !== 0) { await resume(op); await delay(op, 2000); if (!alive(op)) return; continue; }
        let retry = false;
        for (let seconds = 10; seconds > 0; seconds--) {
          publish({ phase: "countdown", secondsRemaining: seconds });
          await delay(op, 1000); if (!alive(op)) return;
          const current = await runtime.pause(op.epoch);
          if (!alive(op)) return;
          if (!current.paused || current.epoch !== op.epoch) throw failure("task_state_unknown");
          if (current.active !== 0) { retry = true; break; }
        }
        if (retry) { await resume(op); continue; }
        const { decision, local } = await rediscover();
        if (!alive(op)) return;
        if (!["direct", "bridge"].includes(decision.status) || decision.next?.releaseId !== saved.releaseId
          || decision.next.version !== saved.nextVersion || JSON.stringify(decision.next.package) !== JSON.stringify(selected.offer.next.package)
          || JSON.stringify(decision.next.components) !== JSON.stringify(selected.offer.next.components)
          || local.dirty || !local.dataProfile) throw failure("offer_changed");
        const compatible = await verifyComponents(decision.next, selected.receipt);
        if (!alive(op)) return;
        if (compatible.dataProfile !== local.dataProfile) throw failure("offer_changed");
        // Discovery and component inspection can outlast a pause lease.
        // Renew admission and recheck work before entering shutdown.
        const finalPause = await runtime.pause(op.epoch);
        if (!alive(op)) return;
        if (!finalPause.paused || finalPause.epoch !== op.epoch) throw failure("task_state_unknown");
        if (finalPause.active !== 0) { await resume(op); continue; }
        // Persist the uncertain outcome before stopping any service. A crash at
        // this boundary must lead to repair, never a blind second installation.
        await write({ ...saved, phase: "installing", dataProfile: local.dataProfile, ...(saved.version === 2 ? { epoch: op.epoch } : {}) });
        op.stopping = true;
        publish({ phase: "installing", secondsRemaining: undefined, pendingUpdate: false });
        await runtime.stop(op.epoch);
        if (disposed) return;
        const result = await stage.install();
        if (!result?.handoff) throw failure("native_handoff_unknown");
        op.handoff = true;
        return;
      }
    } catch (error) {
      if (error?.handoffStarted) op.handoff = true;
      if (alive(op)) {
        op.incompatible = !op.stopping && reasonFor(error) === "component_update_incompatible";
        try { if (saved && op.stopping) await write({ ...saved, phase: "repair" }); } catch { error = failure("update_record_write"); }
        publish({ ...fields(), updating: false, pendingUpdate: !op.stopping, phase: op.stopping ? "repair" : "error", reason: reasonFor(error), secondsRemaining: undefined });
      }
    } finally {
      if (!op.handoff) {
        try { await resume(op); }
        catch {
          op.resumeFailed = true;
          if (!disposed) {
            // Do not lose the failed recovery when the operation is released.
            // Repeated Later/Install must not claim that admission reopened.
            repairRequired = true;
            try { await write({ ...saved, phase: "repair" }); } catch { loadFailed = true; }
            publish({ phase: "repair", reason: "update_resume_failed", updating: false, pendingUpdate: false, secondsRemaining: undefined });
          }
        }
        if (op.incompatible && !op.resumeFailed && !disposed) {
          try {
            await nativeInstaller?.discardPreparation?.();
            try { clearDownloadIntent(); store.clear(); saved = undefined; }
            catch { throw failure("update_record_write"); }
            publish({ ...invalidateOffer(), phase: "blocked", reason: "component_update_incompatible", updating: false,
              pendingUpdate: false, pendingDownload: false, packageAvailable: false, secondsRemaining: undefined });
          } catch (error) {
            publish({ ...fields(), phase: "error", reason: reasonFor(error), updating: false });
          }
        }
      }
      if (operation === op) operation = undefined;
    }
  };
  return {
    supported,
    pending: () => Boolean(saved || operation || loadFailed || repairRequired),
    assertReady() { if (disposed || loadFailed || repairRequired || saved?.phase === "installing" || saved?.phase === "repair") throw failure("update_incomplete"); },
    async begin(releaseId) {
      if (disposed || loadFailed || repairRequired || saved?.phase === "installing" || saved?.phase === "repair") throw failure("update_incomplete");
      if (operation) return getState();
      const selected = context();
      if (!supported(selected.configuration) || selected.offer?.next.releaseId !== releaseId) throw failure("native_install_unavailable");
      if (saved && saved.releaseId !== releaseId) throw failure("offer_changed");
      write({ version: selected.configuration.updaterProtocol === 2 ? 2 : 1, phase: "pending", sourceVersion: currentVersion, releaseId, nextVersion: selected.offer.next.version,
        ...(saved?.packagePath ? { packagePath: saved.packagePath } : {}) });
      const op = { cancelled: false }; operation = op;
      publish({ ...fields(), updating: true, pendingUpdate: false, reason: undefined });
      op.promise = run(op);
      return getState();
    },
    async later() {
      if (operation?.stopping || saved?.phase === "installing" || saved?.phase === "repair" || loadFailed || repairRequired) throw failure("update_incomplete");
      cancelDownload();
      const op = operation;
      if (op) { op.cancelled = true; await runtime.cancelPreparation?.(); if (op.timer) clock.clearTimeout(op.timer); op.wake?.(); await op.promise; if (op.resumeFailed) throw failure("update_resume_failed"); }
      await nativeInstaller?.discardPreparation?.();
      // A complete cache stays intact. The separate download reminder retains
      // explicit continuation after restart when no installation is pending.
      try { store.clear(); saved = undefined; } catch { throw failure("update_record_write"); }
      publish({ phase: context().receipt ? "downloaded" : "available", updating: false, pendingUpdate: false, secondsRemaining: undefined, reason: undefined });
      return getState();
    },
    async startup() {
      if (!saved || disposed) return;
      if (saved.sourceVersion === currentVersion) return;
      try {
        if (saved.nextVersion !== currentVersion || saved.phase === "pending" || !saved.dataProfile) throw failure("update_incomplete");
        const { configuration, local } = await runtime.verifyStartup();
        if (disposed) return;
        if (configuration.releaseId !== saved.releaseId || configuration.version !== currentVersion || local.dirty || local.dataProfile !== saved.dataProfile) throw failure("update_incomplete");
        clearDownloadIntent(); store.clear(); saved = undefined;
        publish({ phase: "installed", pendingUpdate: false, pendingDownload: false, packageAvailable: false, reason: undefined });
      } catch (error) { publish({ ...fields(), pendingUpdate: false, phase: "repair", reason: reasonFor(error) }); }
    },
    async reveal() { if (!saved?.packagePath) throw failure("download_not_ready"); await revealPackage(saved.packagePath); },
    dispose() { disposed = true; if (operation) { operation.cancelled = true; if (operation.timer) clock.clearTimeout(operation.timer); operation.wake?.(); } nativeInstaller?.dispose(); },
  };
}
module.exports = { createUpdateInstallFlow };
