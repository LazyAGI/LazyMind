# Brief before implementation

The existing Codex Workflow plugin is the controller. Core owns this graph:
prepare (Codex) → validate_brief (native LazyMind worker) → implement (Codex + review).
No external Python loop advances it. The native validation step copies the exact
Brief value frozen in its Attempt Context, then a deterministic post-step check
must pass before Core can authorize implementation. Model arguments cannot replace
that input. The code/test outputs of implementation require human review.

Normal request: `Run the Superpowers Brief Gate Showcase.`
Explicit fault injection: add a whole line `FAULT_BRIEF_MISSING` (or
`FAULT_BRIEF_EMPTY`). Codex is instructed to publish the corresponding null/empty
candidate, and the real native gate must reject it. If Codex does not inject the
fault, that run is not a negative sample. The Artifact records the injection.

Recovery: in the Panel Brief tab edit the brief field to the exact fixed task text
specified in prepare. Keep fault_injection as provenance. Then select Gate and Retry.
Core binds the edited revision to the new attempt, keeping failed history. Retry
without fixing the input should fail again. Do not edit the Gate output as a repair;
repair the source Brief and rerun its validator. Human edits/control are trusted
user operations, not an adversarial security boundary.

This checks a Brief Artifact, not a local Superpowers brief file. Codex cannot
obtain an external execution handle for the native validator. The plugin has no
pre-tool interception or host file/test attestation. The Advanced six-step executor
retains the stronger local child-launch, Git and Coverage checks.
