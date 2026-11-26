import assert from "node:assert/strict";
import test from "node:test";
import startup from "../electron/src/update-startup.js";

const revisions = { rag: "b".repeat(64), cluster: "c".repeat(64) };

// Exercise the real Electron bootstrap boundary. The runtime owns the
// selections and may report a pending revision only after saving its retry.
function openTarget(wanted, active, pending, dirty = false, missing = [], reportedMissing = missing) {
  const tx = "a".repeat(32), owner = "test-target-owner", release = "target";
  const identity = { transactionId: tx, owner };
  const hasRAG = wanted.includes("rag");
  const targetComponents = Object.fromEntries(wanted.filter(id => !missing.includes(id)).map(id => [id, { id, revision: revisions[id] }]));
  const peer = { ...identity, ready: true, sealed: true };
  const calls = [];
  const answers = {
    inspect: { ...identity, status: "maintenance_required", targetReleaseId: release, targetVersion: "1.1.0",
      dataProfile: "known", sourceHadRAG: hasRAG, componentMode: hasRAG ? "sync-target" : "absent",
      ...(Object.keys(targetComponents).length ? { targetComponents } : {}), unavailableComponents: missing },
    activate: { ...identity, status: "activated" },
    up: { ...identity, status: "ready", bootstrap: true },
    verify: { ...identity, status: "ready", releaseId: release, version: "1.1.0", dataProfile: "known", dirty,
      components: active, pendingComponents: pending, unavailableComponents: reportedMissing, peers: { core: peer, scan: peer } },
    complete: { ...identity, status: "completed", peersReleased: { core: true, scan: true } },
  };
  const result = startup.prepareDesktopStartup({ isPackaged: true, ownerToken: owner,
    currentReleaseId: release, currentVersion: "1.1.0", invoke: async (cmd, args) => {
      const action = cmd === "update-bootstrap" ? args[args.indexOf("--action") + 1] : cmd;
      calls.push(action);
      return JSON.stringify(answers[action] || {});
    } });
  return { result, calls };
}

for (const [name, wanted, active, pending] of [
  ["neither installed", [], {}, {}],
  ["RAG only, offline", ["rag"], {}, { rag: revisions.rag }],
  ["Cluster only, offline", ["cluster"], {}, { cluster: revisions.cluster }],
  ["both, only RAG ready", ["rag", "cluster"], { rag: revisions.rag }, { cluster: revisions.cluster }],
  ["both pending", ["rag", "cluster"], {}, revisions],
]) {
  test(`target can open before optional downloads finish: ${name}`, async () => {
    const { result, calls } = openTarget(wanted, active, pending);
    assert.equal((await result).allowed, true);
    assert.deepEqual(calls, ["inspect", "activate", "up", "verify", "complete"]);
  });
}

for (const [name, wanted, active, pending, dirty] of [
  ["unselected component marked pending", [], {}, { cluster: revisions.cluster }],
  ["wrong queued target", ["rag"], {}, { rag: "e".repeat(64) }],
  ["chosen component silently lost", ["rag", "cluster"], { rag: revisions.rag }, {}],
  ["component both active and queued", ["cluster"], { cluster: revisions.cluster }, { cluster: revisions.cluster }],
  ["stale loaded revision with a valid queued target", ["rag"], { rag: "d".repeat(64) }, { rag: revisions.rag }],
  ["core data proof failed", ["rag"], {}, { rag: revisions.rag }, true],
]) {
  test(`deferred component proof cannot mask: ${name}`, async () => {
    const { result, calls } = openTarget(wanted, active, pending, dirty);
    await assert.rejects(result);
    assert.ok(!calls.includes("complete"));
  });
}

test("a previously selected Cluster may have no descriptor in the installed release", async () => {
  const { result, calls } = openTarget(["cluster"], {}, {}, false, ["cluster"]);
  assert.equal((await result).allowed, true);
  assert.equal(calls.at(-1), "complete");
});

for (const reported of [[], ["rag"], ["cluster", "cluster"]]) {
  test(`unavailable selections must match the saved choices: ${JSON.stringify(reported)}`, async () => {
    const { result, calls } = openTarget(["cluster"], {}, {}, false, ["cluster"], reported);
    await assert.rejects(result);
    assert.ok(!calls.includes("complete"));
  });
}
