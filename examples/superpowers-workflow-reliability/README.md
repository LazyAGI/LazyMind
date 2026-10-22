# See a missing Brief stop a Workflow

[中文](README.CN.md) · [Demo walkthrough](DEMO.md) · [Advanced CLI](ADVANCED.md) · [Review response](REVIEW_RESPONSE.md)

A reusable Superpowers Dispatch Reliability Workflow integration built with LazyMind.

Superpowers [#2282](https://github.com/obra/superpowers/issues/2282) asks what happens
when an implementer is dispatched before its task brief is ready. A Skill tells an
agent what to do; this example gives **one explicitly selected Workflow** an
observable admission boundary. The historical native Skill sample succeeded; we
use labeled fault injection to show the missing-Brief condition, not a claimed
natural reproduction of the upstream bug.

## Why LazyMind?

- **Reliable:** Core admits the implementation step only after its predecessor
  succeeds. A pinned native business check rejects a missing or empty Brief.
- **Observable:** the Panel shows the Brief, step state, code and actual test-output
  Artifacts. Failed attempts remain visible.
- **Recoverable:** Core preserves attempts and user recovery controls. The Advanced
  path has verified UI Retry; edited-input recovery in this native demo is currently
  **BLOCKED** on the tested installation (details below).

The [small native package](../../workflows/superpowers-brief-showcase/workflow.yaml)
uses the existing Codex plugin, graph, script-tool loader, post-step checks and
human review. No Python program drives this graph. Codex prepares the Brief; Core
routes its validation to LazyMind. Only then does Codex execute the authorized
implementation step in a disposable local directory. A configured LazyMind chat
model is therefore also required.

## Try in Codex

Requires macOS/Linux, a running LazyMind **from this PR branch** with a working
native Workflow worker/chat model, desktop Codex, and Node.js >= 22.19 for its
existing Workflow plugin. The plugin does not need a separate Codex CLI install.

1. Get this branch and run/restart LazyMind with its normal
   [setup](../../docs/quick_start.md). Startup registers the new Workflow package.
   In Workflow settings enable **Superpowers Brief Gate Showcase** in manual mode.

   ```bash
   git clone --branch feat/superpowers-workflow-eval --single-branch \
     https://github.com/NicoYang1984/LazyMind.git LazyMind-showcase
   cd LazyMind-showcase
   ```

   For an already deployed server, an administrator may publish the four files in
   [the package](../../workflows/superpowers-brief-showcase/) through the existing
   [Workflow Agent Kit authoring API](../../skills/workflow-agent-kit/references/existing-workflow-authoring.md).
   Custom scripts require the deployment's normal trust approval. The execution
   plugin does not expose authoring tools; it cannot install a missing package.

2. In LazyMind **Settings → External Agent integrations → Codex**, click Connect.
   Follow the [official plugin guide](../../integrations/codex-workflow/README.md).
   If just installed, open a fresh Codex task so the host loads its tools. Existing
   connected tasks can continue using the tools already loaded in them. No other
   Codex tasks are intercepted or modified.

3. In Codex, ask:

   > Use the LazyMind Workflow plugin to run Superpowers Brief Gate Showcase.
   > Open its Panel. Implement only in a new disposable directory and stop for review.

   Codex discovers the package, starts and binds the session, opens its returned
   Panel URL and begins the ready step. It publishes the Brief, starts native
   validation and ends its turn while LazyMind checks that bound revision. The
   official queue integration is intended to wake the same bound task afterward;
   Codex re-reads Core state before implementation. Our CLI-hosted test required
   explicit continuation; automatic desktop execution after queue acceptance remains unverified.
   Inspect `labels.py`, the three assertions and test output, then approve in the Panel.

4. Repeat with a **new** Workflow session and this explicit fault request:

   ```text
   Use the LazyMind Workflow plugin to run Superpowers Brief Gate Showcase.
   FAULT_BRIEF_MISSING
   ```

   Expect `BRIEF_MISSING` in the native gate event, a failed validation step and no
   admitted implementation attempt. A native validation SubAgent already ran;
   this is **not** a claim of zero model calls or zero operating-system processes.

   Preserve the failure for inspection. Edited-input same-session Retry is a
   follow-up product acceptance item, not a required trial step.

If Codex stops after native validation, continue in the same bound task:
“Re-read the LazyMind Workflow state and continue only the next authorized step.”
Open the returned Panel URL manually if needed. Queue `accepted` is not execution
proof; do not fabricate a thread ID or bypass Core admission.

## Real evidence

[Reproduce the demo](DEMO.md): actual plugin MCP calls, native Gate execution,
normal completion after human approval, and missing-Brief rejection with no
implementation attempt. The [receipts and image hashes](evidence/showcase/receipt.json)
include the independent Core **409** admission refusal. Normal plugin execution
used manual same-thread continuation; it is not uninterrupted desktop E2E.
Two real Panel screenshots are available. Video is **NOT_RECORDED**; the optional
60-second recording guide is not a software or review prerequisite.

- [#2282: Brief admission and recovery](cases/issue-2282.md) is the main demo.
- [#2245: full Git SHA and finite coverage](cases/issue-2245.md) is supplementary.
- [Comparison and control boundaries](comparison.md).
- [Compact historical evidence](evidence/public/README.md) and
  [validation record](PR_VALIDATION.md). Recorded success/fault evidence is not a
  claim that a fresh execution has just passed.

The [Advanced guide](ADVANCED.md) retains the CSV Quick Start, install/reuse,
custom single-task Plans and dynamic Coverage, six-step executor, real Git checks
and UI Retry. It uses Python because that executor owns and rechecks the local
Codex process-launch boundary. It remains the path for those stronger local checks.

Run the example regressions from this directory after installing `requirements.txt`:

```bash
python3 -m unittest discover -s . -p 'test_*.py' -v
python3 verify_rc_evidence.py
```

These commands test code and historical evidence; they do not run a plugin E2E.

## Limitations

Desktop automatic continuation and native edited-input same-session Retry remain
unfinished product acceptance items; they are outside this PR's bounded Showcase
acceptance. The tested Panel saved a human Brief revision but disabled Retry.
[Exact observations and follow-ups](REVIEW_RESPONSE.md#remaining-product-boundaries).

The native demo checks a **Brief Artifact**, not a Superpowers brief file on the
Codex host. It gates Core's implementation authorization; ordinary Codex tools
outside this Workflow remain callable. Code/test Artifacts from the external step
are agent-reported: Core does not attest their correspondence to host files.
Inspect the actual disposable files, or use the Advanced executor for deterministic
Git/coverage checks. Native validation uses LazyMind's model; implementation uses
Codex's configured model. Neither path certifies backend model identity.

No full SDD planning/reviewer loop, arbitrary-process containment, shared-index
safety guarantee, Codex Todo UI synchronization or measured speed/reliability/token
gain is claimed. No production Core/Adapter change is required by this example.
New code is [Apache-2.0](../../LICENSE); pinned Superpowers material retains its
[MIT notice](UPSTREAM-LICENSE).
