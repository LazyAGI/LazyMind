# Validation and review scope

A reusable Superpowers Dispatch Reliability Workflow integration built with LazyMind.
[PR #852](https://github.com/LazyAGI/LazyMind/pull/852) adds an executable single-task
example, not a complete Superpowers SDD conversion or production Runtime change.

## Review round — 2026-10-21

Latest main `5398ef079c9e3051fbd5d89ae7fbca9c13ebc5d0` merged without conflicts
for this review; the clean LazyLLM checkout was aligned to main's
`4d99455e12fa9cb53b0ed100b36c3c2b010a6b79`. No feature diff changes Core, Adapter,
production Runtime or the submodule pointer. The prior `eec75260` CI run completed
successfully before this round.

Added a small **native** Brief showcase beside the unchanged six-step executor.
[REVIEW_RESPONSE](REVIEW_RESPONSE.md) and [selected receipts](evidence/showcase/receipt.json)
record the actual plugin MCP calls, final package revision, normal completion,
independent file/test check, missing-Brief injection and real HTTP 409 admission
refusal. These new sessions did not use the Python execution loop. The original
53 RC and later 58-test cleanup counts below remain dated historical results.

The final normal native session passed with **manual CLI continuation and real UI
approval**. The negative session blocked implementation and a blind UI Retry failed
again. Edited-input same-session recovery and uninterrupted desktop queue execution
are **BLOCKED**, not E2E successes. No continuous video was recorded; actual Panel
stills and a recording script are in [DEMO](DEMO.md). Local validation and GitHub CI
for this revision are recorded in REVIEW_RESPONSE. A new clean checkout passed
68 tests, install/reuse/preflight and separate CLI normal/missing-Brief E2Es. The
older runs below retain their historical classification; no CLI run is relabeled
as a plugin E2E.

## Versions and historical acceptance

- Main merged without conflicts: `87ee4145166d94f18230e55060b009596aed5e59`.
- RC acceptance: `d76e743b4f8802eebb9f4ed6d74351f23dbea99d`.
- Executable source of selected publish-round E2E: `113585a430e5b5f8f2d97b8963d7d1431329a1a0`.
- Superpowers v7.0.0: `bb92a77741419a4ab5f06e711a283343f1ada0c3`.
- LazyLLM pointer `ba02e9c35aedc011c17b4a30bdf44d548a54b223` matches merged main.
- Existing local Runtime installation; service binary SHA N/A, not rebuilt for these runs.

On 2026-10-20 the RC passed **53 automated regressions**, plus **8 real Runtime
sessions / 6 Codex children** covering install/reuse, CSV, custom Task 7/Unicode,
brief/base/coverage negatives and UI Retry. A later publish check ran **2 more
sessions / 1 child**. These counts are historical, not this cleanup's rerun totals.
Unit/API doubles and injected callbacks are not live E2E. The compact
[public evidence](evidence/public/README.md) preserves selected success/failure
records, Git changes, timestamps, session IDs, recovery attempts and source hashes.
Full historical logs are deliberately outside the tracked Example.

## Previous cleanup validation

That cleanup changed documentation, public evidence and its offline verifier/tests.
The seven execution modules, Workflow contract, existing 53 tests, fixtures and
licenses are unchanged. Custom input seeds were relocated to `tasks/`; old audit
reports and one-off exporters/probes were removed from tracking with local copies.

On **2026-10-21**, a new local Git clone received the staged candidate tree;
its old evidence directories and root reports were absent. The same seven execution
module hashes match the public manifest. No locally retained archive was available
to the tests. That cleanup reran **2 Runtime sessions / 1 actual Codex child**:

| Check | Result |
| --- | --- |
| Automated regressions | PASS — original 53 unchanged + 5 public-evidence checks = 58 |
| Public evidence integrity and source comparison | PASS — all 7 case files; each selected value equals its pinned historical Git source |
| Configure, install, repeat install, preflight | PASS — first created=true, repeat=false; same revision `6a406fcf-3961-4111-bcd3-e1d92a82ed4f` |
| Normal live Runtime/Codex | PASS — six steps and selected Artifacts, one process, exit 0 and turn.completed |
| Missing-brief live injection | PASS — expected BRIEF_MISSING; zero child starts, downstream steps none |
| Independent status/API/Git readback | PASS — parent/Plan/tests unchanged; only csv_records.py changed; four acceptance tests passed again |
| Repository docs / configuration checks | PASS — 2 doc tests; Workflow naming, test placement, migration immutability |
| Python syntax / critical lint / whitespace | PASS — AST, flake8 E9/F63/F7/F82, git diff --check |
| Links, secrets/private paths, license | PASS — relative targets exist, credential/path scan clean, upstream MIT bytes unchanged |
| Full Go/frontend CI | See GitHub PR checks; not claimed as a local rerun |

Normal Session `e0ef1cf77d7042c180fff1bbf8e8ebc5` ran 02:59:05.552–02:59:56.534 UTC,
with raw child SHA-256 `c7de090d11bb122b5af20c5f35af9fcd6767c9493676226ab717ae7de71ce0be`.
Brief-negative Session `1242ee0cbbbe492b914b9ca565b0f648` ran
02:59:56.989–02:59:57.217 UTC; only preparation was selected and no child trace existed.
`status` independently re-read both Runtime projections and Artifacts. The brief was
deliberately deleted after generation. Model requested: gpt-6-astra / medium;
backend identity remains N/A. Complete new logs are retained locally, not added to
the public example. Wrong-base, missing-coverage, custom-task and UI Retry E2Es
above remain **historical** acceptance; they were not rerun in this cleanup.

The PR shrinks from **256 files / 31,147 added lines** to **57 files / about 4,700
added lines**. Of 213 old evidence paths, 212 are untracked; eight seed files are
preserved under tasks/. Public evidence now has nine files (seven cases, manifest,
guide), plus its ignore file. Four root reports and five historical metadata/tool
files are also untracked. Original files and a checksummed archive remain local.
The largest retained public record is about 21 KB; no full child/tool trace is
required for installation or verification.

## Control boundaries

- Core enforces graph admission, leases and required output slots; Python owns the
  actual launch gate and completion checks. YAML reminders alone are not a gate.
- Brief, persisted in_progress state and actual full HEAD are checked before launch.
  Declared finite coverage, protected acceptance files and permitted changes gate success.
- No Codex Todo UI synchronization, backend model attestation, arbitrary-process
  containment, full SDD reviewer loop or uncertain-dispatch replay is implemented.
- Native normal acceptance passed. Different execution scope precludes comparative
  reliability, speed, cost or token-efficiency claims. #2245 is only partially covered.
- Apache-2.0 applies to new code; the pinned upstream [MIT notice](UPSTREAM-LICENSE)
  is preserved. Public Quick Start needs no author-private path or local archive.
- Untracking reduces the current PR diff; it does not erase prior Git history.
