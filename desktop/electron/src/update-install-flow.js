const { targetComponentRevisions } = require("./update-runtime-client");
const failure = code => Object.assign(new Error(code), { code });
const reasonFor = error => /^[a-z_]{1,64}$/.test(error?.code || "") ? error.code : "update_failed";

function createUpdateInstallFlow({ currentVersion, store, nativeInstaller, runtime, context, download, cancelDownload, rediscover,
  publish, getState, revealPackage, clearDownloadIntent, invalidateOffer = () => ({}), clock = { setTimeout, clearTimeout }, now = Date.now }) {
  let saved, operation, disposed = false, epoch = now(), loadFailed = false, repairRequired = false;
  let sourceInspection, cancelling = false, cancellation, cancellationRecorded = false;
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
  const matchesCancellation = (intent, marker) => intent?.version === 1 && intent.phase === "pending" && !intent.takeoverPending
    && marker && intent.attemptId === marker.attemptId && intent.releaseId === marker.releaseId
    && intent.sourceVersion === marker.sourceVersion && intent.nextVersion === marker.nextVersion;
  try {
    saved = store?.read();
    if (!saved || saved.version === 1) {
      const marker = saved?.attemptId ? store.cancellation?.read() : undefined;
      if (matchesCancellation(saved, marker)) {
        if (saved.sourceVersion === currentVersion) { cancellation = marker; cancellationRecorded = true; }
      } else if (!saved?.attemptId || marker) {
        // The primary reminder disappeared or belongs to another attempt. The
        // old cancellation has no authority over that attempt's Go receipt.
        try { store?.cancellation?.clear(); }
        catch { publish({ phase: "error", reason: "update_record_write" }); }
      }
    }
  }
  catch { loadFailed = true; publish({ phase: "repair", reason: "update_record_read", canInstall: false, pendingUpdate: false, pendingDownload: false }); }
  let inspectOnOpen = !loadFailed && saved?.version === 1 && saved.sourceVersion === currentVersion && typeof runtime?.inspectV1Handoff === "function";
  if (inspectOnOpen) publish({ ...fields(), phase: "checking", pendingUpdate: false });
  else if (saved && !loadFailed) publish({ ...fields(), phase: saved.phase === "pending" ? "interrupted" : "repair", reason: saved.phase === "pending" ? undefined : "update_incomplete" });

  const reportCancellationFailure = error => {
    const reason = reasonFor(error);
    if (reason === "update_transaction_conflict" || reason === "update_incomplete") repairRequired = true;
    if (!disposed) publish({ ...fields(), phase: repairRequired ? "repair" : "error", reason, updating: false,
      pendingUpdate: !repairRequired, pendingCancellation: !repairRequired, secondsRemaining: undefined });
  };
  // Keep the visible reminder until Go confirms the exact discard. A separate
  // durable intent distinguishes a completed cancellation from a missing fence.
  const finishCancellation = async (kind, downloadCancelled = false) => {
    const recovering = Boolean(cancellation);
    if (!recovering && saved?.version === 1 && saved.phase === "pending" && saved.attemptId) {
      const { sourceVersion, nextVersion, releaseId, attemptId } = saved;
      cancellation = { version: 1, kind, sourceVersion, nextVersion, releaseId, attemptId };
    }
    if (cancellation) {
      kind = cancellation.kind;
      if (!cancellationRecorded) {
        try { store.cancellation.write(cancellation); cancellationRecorded = true; }
        catch { throw failure("update_record_write"); }
      }
      let discard = true;
      if (recovering) {
        let proof;
        try { proof = await runtime.inspectV1Handoff(); }
        catch (error) { repairRequired = true; throw error; }
        if (disposed) return;
        discard = proof.status !== "absent";
        if (discard && (proof.status !== "prepared" || proof.releaseId !== cancellation.releaseId || proof.attemptId !== cancellation.attemptId)) throw failure("update_transaction_conflict");
      }
      if (discard) await runtime.discardV1Handoff(cancellation.releaseId, cancellation.attemptId);
      if (disposed) return;
      let current;
      try { current = store.read(); } catch { throw failure("update_record_read"); }
      if (current && !matchesCancellation(current, cancellation)) throw failure("update_transaction_conflict");
    }
    try {
      if (kind === "incompatible" || (downloadCancelled && cancellation)) clearDownloadIntent?.();
      else if (!downloadCancelled) cancelDownload(Boolean(cancellation));
      store.clear(cancellation ? 1 : undefined);
      if (cancellation) store.cancellation.clear();
    } catch { throw failure("update_record_write"); }
    saved = undefined;
    cancellation = undefined;
    cancellationRecorded = false;
    const { receipt, offer } = context();
    const incompatible = kind === "incompatible";
    publish({ ...(incompatible || (!receipt && !offer) ? invalidateOffer() : {}),
      phase: incompatible ? "blocked" : receipt ? "downloaded" : offer ? "available" : "idle",
      reason: incompatible ? "component_update_incompatible" : undefined, pendingUpdate: false, pendingCancellation: false,
      updating: false, pendingDownload: false, packageAvailable: !incompatible && Boolean(receipt), secondsRemaining: undefined });
    return "cancelled";
  };

  const bindPrepared = async attemptId => {
    const { takeoverPending, ...reminder } = saved;
    if (reminder.phase !== "pending" || reminder.attemptId !== attemptId || takeoverPending) {
      await write({ ...reminder, phase: "pending", attemptId });
    }
  };
  // Observing a prepared attempt binds it across restarts. Only an explicit
  // takeover interrupted after Go's commit may follow its verified predecessor.
  const reconcileSource = async () => {
    const reminder = saved;
    const proof = await runtime.inspectV1Handoff();
    if (disposed || saved !== reminder) return;
    if (proof.status === "absent" && reminder.phase === "pending" && !reminder.attemptId) return;
    const recoveredTakeover = reminder.takeoverPending === true && proof.previousAttemptId === reminder.attemptId;
    if (proof.status !== "prepared" || proof.releaseId !== reminder.releaseId || reminder.phase === "repair"
      || (reminder.phase === "installing" && !reminder.attemptId)
      || (reminder.attemptId && reminder.attemptId !== proof.attemptId && !recoveredTakeover)) throw failure("update_incomplete");
    await bindPrepared(proof.attemptId);
  };
  const checkSourceOnOpen = () => {
    if (!inspectOnOpen) return;
    if (!sourceInspection) sourceInspection = (cancellation ? finishCancellation(cancellation.kind) : reconcileSource()).then(result => {
      if (result !== "cancelled" && !disposed) publish({ ...fields(), phase: "interrupted", reason: undefined });
      return result;
    }).catch(error => {
      if (cancellation) reportCancellationFailure(error);
      else {
        repairRequired = true;
        if (!disposed) publish({ ...fields(), phase: "repair", pendingUpdate: false, reason: reasonFor(error) });
      }
    }).finally(() => { inspectOnOpen = false; });
    return sourceInspection;
  };
  const alive = op => !disposed && operation === op && !op.cancelled;
  const delay = (op, ms) => new Promise(resolve => { op.wake = resolve; op.timer = clock.setTimeout(() => { op.timer = undefined; op.wake = undefined; resolve(); }, ms); op.timer?.unref?.(); });
  const resume = async op => {
    if (op.epoch) { await runtime.resume(op.epoch); op.epoch = undefined; }
  };
  const verifyComponents = async (release, receipt, handoff, attemptId) => {
    if (typeof runtime.verifyComponents !== "function") throw failure("data_unknown");
    const result = await runtime.verifyComponents(release, receipt, handoff, attemptId);
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
          || !/^[A-Za-z0-9._-]{1,128}$/.test(plan.dataProfile || "")
          || !targetComponentRevisions(plan)) throw failure("component_state_unknown");
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
        op.epoch = runtime.nextEpoch ? runtime.nextEpoch() : ++epoch;
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
        op.committing = true;
        publish({ phase: "installing", secondsRemaining: undefined, pendingUpdate: false });
        if (selected.configuration.updaterProtocol === 1) {
          op.v1Writing = true;
          const prior = saved.attemptId;
          if (prior) await write({ ...saved, takeoverPending: true });
          if (!alive(op)) return;
          const prepared = await verifyComponents(decision.next, selected.receipt, "prepare", prior);
          if (!alive(op)) return;
          if (prepared.dataProfile !== local.dataProfile) throw failure("offer_changed");
          await bindPrepared(prepared.attemptId);
          if (!alive(op)) return;
          const renewed = await runtime.pause(op.epoch);
          if (!alive(op)) return;
          if (!renewed.paused || renewed.epoch !== op.epoch) throw failure("task_state_unknown");
          if (renewed.active !== 0) {
            // Keep the exact preparation for the next idle attempt or Later.
            op.v1Writing = false; op.committing = false;
            await resume(op); continue;
          }
        }
        await write({ ...saved, phase: "installing", dataProfile: local.dataProfile, ...(saved.version === 2 ? { epoch: op.epoch } : {}) });
        if (!alive(op)) return;
        if (selected.configuration.updaterProtocol === 1) {
          const authorized = await verifyComponents(decision.next, selected.receipt, "authorize", saved.attemptId);
          if (authorized.dataProfile !== local.dataProfile) throw failure("offer_changed");
          op.v1Handoff = decision.next.releaseId;
          if (!alive(op)) return;
        }
        op.stopping = true;
        await runtime.stop(op.epoch);
        if (disposed) return;
        const result = await stage.install();
        if (!result?.handoff) {
          if (op.v1Handoff) throw Object.assign(failure("native_handoff_unknown"), { handoffStarted: true });
          throw failure("native_handoff_unknown");
        }
        op.handoff = true;
        return;
      }
    } catch (error) {
      if (error?.handoffStarted) op.handoff = true;
      if (alive(op)) {
        // A transport or write failure can follow a committed transition. An
        // installing reminder is recoverable only if Go still proves prepared.
        if (op.v1Writing && !op.stopping) {
          try { await reconcileSource(); } catch { repairRequired = true; }
        }
        op.incompatible = !op.stopping && !repairRequired && reasonFor(error) === "component_update_incompatible";
        try { if (saved && op.stopping) await write({ ...saved, phase: "repair" }); } catch { error = failure("update_record_write"); }
        publish({ ...fields(), updating: false, pendingUpdate: !op.stopping && !repairRequired,
          phase: op.stopping || repairRequired ? "repair" : "error", reason: reasonFor(error), secondsRemaining: undefined });
      }
    } finally {
      if (!op.handoff) {
        if (op.v1Handoff && !disposed) {
          try { await runtime.discardV1Handoff(op.v1Handoff); }
          catch {
            repairRequired = true;
            if (!disposed) publish({ phase: "repair", reason: "update_record_write", updating: false, pendingUpdate: false });
          }
        }
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
            await finishCancellation("incompatible");
          } catch (error) {
            if (cancellation) reportCancellationFailure(error);
            else publish({ ...fields(), phase: "error", reason: reasonFor(error), updating: false });
          }
        }
      }
      if (operation === op) operation = undefined;
    }
  };
  const assertReady = () => {
    if (disposed || cancelling || cancellation || loadFailed || repairRequired || saved?.phase === "installing" || saved?.phase === "repair") throw failure("update_incomplete");
  };
  return {
    supported,
    checkSourceOnOpen,
    pending: () => Boolean(saved || operation || loadFailed || repairRequired),
    assertReady,
    async begin(releaseId) {
      const inspection = checkSourceOnOpen();
      if (inspection && await inspection === "cancelled") return getState();
      assertReady();
      if (operation) return getState();
      const selected = context();
      if (!supported(selected.configuration) || selected.offer?.next.releaseId !== releaseId) throw failure("native_install_unavailable");
      if (saved && saved.releaseId !== releaseId) throw failure("offer_changed");
      write({ version: selected.configuration.updaterProtocol === 2 ? 2 : 1, phase: "pending", sourceVersion: currentVersion, releaseId, nextVersion: selected.offer.next.version,
        ...(saved?.attemptId ? { attemptId: saved.attemptId } : {}),
        ...(saved?.packagePath ? { packagePath: saved.packagePath } : {}) });
      const op = { cancelled: false }; operation = op;
      publish({ ...fields(), updating: true, pendingUpdate: false, reason: undefined });
      op.promise = run(op);
      return getState();
    },
    async later() {
      if (cancelling || operation?.committing) throw failure("update_incomplete");
      cancelling = true;
      try {
        const inspection = checkSourceOnOpen();
        if (inspection && await inspection === "cancelled") return getState();
        if (disposed || saved?.phase === "installing" || saved?.phase === "repair" || loadFailed || repairRequired) throw failure("update_incomplete");
        const op = operation;
        if (op) {
          op.cancelled = true; cancelDownload();
          await runtime.cancelPreparation?.();
          if (op.timer) clock.clearTimeout(op.timer);
          op.wake?.(); await op.promise;
          if (op.resumeFailed) throw failure("update_resume_failed");
          if (!saved) return getState();
        }
        try { await finishCancellation("later", Boolean(op)); }
        catch (error) {
          if (cancellation) reportCancellationFailure(error);
          throw error;
        }
        return getState();
      } finally { cancelling = false; }
    },
    completeNativeStartup(transactionId) {
      // Native completion verified the effective target (including a forward
      // repair), released the peers, and removed this exact record.
      if (disposed || !transactionId || saved?.version !== 2 || saved.transactionId !== transactionId) return;
      saved = undefined;
      try { clearDownloadIntent(); }
      catch { publish({ phase: "error", reason: "update_record_write", pendingUpdate: false }); return; }
      publish({ ...invalidateOffer(), phase: "installed", packageAvailable: false, pendingUpdate: false, pendingDownload: false, reason: undefined });
    },
    async startup() {
      if (!saved || disposed || saved.version === 2) return;
      if (saved.sourceVersion === currentVersion) return checkSourceOnOpen();
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
