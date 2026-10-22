# Selected public evidence

Read [Quick Start](../../README.md) → [executor](../../project.py) →
[issue cases](../../comparison.md) → these records.

| File | Classification and observation |
| --- | --- |
| [normal.json](normal.json) | Real Runtime/Codex CSV run; one PID/terminal receipt, six successful steps/Artifacts, protected tests and actual patch |
| [brief-missing.json](brief-missing.json) | Deliberate deletion after generation; real Runtime fails, zero launches |
| [wrong-base.json](wrong-base.json) | Deliberate actual empty commit; expected/actual SHA differ, zero launches |
| [missing-coverage.json](missing-coverage.json) | Deliberate row deletion after real child; completion rejected |
| [recovery.json](recovery.json) | Same-session failed snapshot, authenticated UI Retry receipts, successful second Attempt |
| [reuse-custom.json](reuse-custom.json) | Historical first/repeat install, two CSV runs, Task 7 and Unicode task summaries |
| [native.json](native.json) | Unmodified native Skill normal sample; selected brief/spawn tool calls and independent acceptance |

These are mechanically selected JSON values and events from **real historical
runs**, not full traces and not new executions. Faults are explicitly injected;
none is advertised as a naturally reproduced native bug. There is no paired
benchmark, backend model attestation or success-rate/speed/token-efficiency claim.

[manifest.json](manifest.json) pins the prior public snapshot commit, Superpowers
version, unchanged executable hashes and SHA-256 of every excerpt. Each record
contains a historical source path/hash and a `values` mapping keyed by JSON Pointer.
For JSONL, the first pointer component is the **zero-based line index**. An empty
pointer selects the full JSON value. Removed token usage, duplicate API readbacks,
private environment diagnostics and unrelated session data are not reconstructed.
Hashes identify the earlier sanitized exports; child raw and export hashes remain
distinct. They detect accidental changes, not third-party execution attestation.

```bash
# No credentials, model, network or local archives needed:
python3 verify_rc_evidence.py
# Optional, from the example directory, with the pinned commit in Git history:
python3 verify_rc_evidence.py --verify-sources ../..
```

The optional command compares every retained value against the pinned historical
Git blob, not just its hash. The default also checks stage ordering, launch counts,
terminal state, six output slots, fault boundaries and recovery continuity. Neither
command is live E2E. Use the Quick Start for fresh Runtime/Codex execution.

The old exports, root design/audit reports and local archive are untracked and
ignored; they are not installation inputs. Ordinary untracking does **not** remove
prior Git history. [PR_VALIDATION](../../PR_VALIDATION.md) separates dated historical
acceptance from current checkout validation. Source revisions changed during RC:
recovery spans `633ed8fe` → `a3a227af`, Unicode uses `732fab2e`, and the selected
normal/brief runs use `113585a4`. Each case carries its exact provenance. The
existing Runtime service binary's commit remains N/A.

The separate [native plugin showcase receipts](../showcase/receipt.json) and
[real Panel screenshots](../../DEMO.md) were captured on 2026-10-21. They are not
part of this historical CLI manifest. Their normal run required manual continuation;
native edited-input recovery and uninterrupted desktop E2E are BLOCKED.
