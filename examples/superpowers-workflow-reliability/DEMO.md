# Brief gate: real screens and a recording plan

[Try in Codex](README.md#try-in-codex) · [Selected receipts](evidence/showcase/receipt.json)

**Status:** real Codex-plugin MCP calls, native Runtime execution and browser
screenshots captured. **No continuous video/GIF was recorded.** The available
browser automation captures stills; it does not expose video recording. Desktop
automatic queue continuation and edited-input recovery remain open acceptance
items. Do not present this shot list as a finished product video.

## What was actually seen

![Real Panel: missing Brief blocks validation](evidence/showcase/blocked.png)

The Brief was deliberately null. A native `check_showcase_brief` event returned
`BRIEF_MISSING`. Core rejected a direct implementation-admission request with HTTP
409 `WORKFLOW_ADMISSION_DENIED`, reason `recovery_required`.

![Real Panel: actual implementation and tests await human review](evidence/showcase/review.png)

This is the separate **normal** session: Codex wrote the file, ran three tests and
published the outputs through MCP. Independent file/Artifact comparison and tests
passed. Only the actual Panel approval completed the session. The normal image is
not a recovered version of the failed session.

## Reproducible 75-second shot plan

Use a disposable test task, the [README instructions](README.md#try-in-codex), an
OS screen recorder and the real Panel. Keep credentials and unrelated tasks out of
frame. Record actions live; label any time cuts. Never fabricate terminal output.

| Time | Action / screen | Short English caption |
| --- | --- | --- |
| 0–10s | Open the [native successful sample](evidence/public/native.json) and #2282 case; show its PASS | “The native Skill sample passed. Missing Brief is a deliberate fault injection.” |
| 10–25s | In a Codex task with the installed plugin, send the normal request; show actual workflow.start and Panel | “Codex stays the controller. LazyMind owns step admission.” |
| 25–40s | Show native Gate success, implementation artifacts and real review boundary | “Inspect the brief, code and actual test output before approval.” |
| 40–60s | Start a separate session with FAULT_BRIEF_MISSING; show null input, failed Gate and no implementation attempt | “Fault injection: the native check blocks implementation authorization.” |
| 60–75s | Show failed history and the current recovery limitation; link Advanced recovery | “Failed state is preserved. Native edited-input Retry still needs a product fix.” |

Do not record a successful same-session native recovery ending until it has really
passed. After that gap is resolved, replace the last shot with: edit the source
Brief → confirm its new saved version → Retry Gate → observe the new attempt and
implementation → review. Preserve the failed attempt and source revision in view.

In our CLI-hosted test, queue notifications were accepted but the CLI turn exited;
we explicitly resumed the same Codex thread. The desktop `open_in_codex` tool was
not available to that CLI process, so this task opened the real returned Panel in
the browser. These are integration limits, not a fully automatic desktop demo.
