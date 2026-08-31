# Skill to Workflow v1

The Host model reads the Skill and authors Workflow text. Every authoring tool is
deterministic infrastructure: it reads a snapshot, stores text, compiles, reports
diagnostics, or publishes. No authoring tool invokes a model or rewrites content.

## 1. Pin and understand the Skill

Call `get_skill_conversion_context(skill_id)`. The Host pins the currently selected
revision. This returns the
complete immutable Skill snapshot, including revision id, tree hash, package files,
referenced content, and the currently available Workflow tool catalog. Treat the
returned revision and tree hash as a pair. Reread the snapshot instead of reading
an unversioned local copy.

Extract, in descriptive language:

- intended user outcome and explicit non-goals;
- required user-provided files or structured inputs;
- executable stages and dependencies;
- tools/capabilities used by each stage;
- observable intermediate and final outputs;
- acceptance criteria and human approval boundaries;
- conditional routes, parallel work, and recoverable failure points.

Decline conversion when essential behavior depends on hidden state, unavailable
tools, credentials embedded in instructions, unbounded arbitrary code, or an
outcome that cannot be observed and validated.

## 2. Design the package

Read `workflow-format.md` completely before generating files. Produce at least:

- `workflow.yaml` — compatibility package filename containing public Workflow
  metadata, steps, materials/slots, and presentation declarations;
- `scenario/state.yml` — graph transitions and executable step contracts;
- `scenario/scenario.md` — descriptive usage and step explanation for ChatAgent.

Generate `scenario/layout.json` only when layout is necessary. Avoid custom
`scripts/` when a framework tool exists; scripts require deterministic audit and
may block publication.

Inputs must be durable materials/Input Resources, not prompts disguised as data.
Each non-external material has exactly one producer. Every step output is required,
observable, and paired with acceptance criteria. A consumer must be downstream of
its producer. Route conditions must be understandable from user intent or durable
state; never encode private Host state.

## 3. Create and repair the draft

Call `create_workflow_draft` with the name, pinned `skill_id`, and initial file
map. The Host injects the revision and tree hash from the conversion context. The
tool only stores exactly what the Host model wrote after checking the snapshot and
allowed paths, and selects the returned draft as the authoring context.

Call `validate_workflow_draft()` for graph compiler feedback, then
`get_workflow_diagnostics()` for strict package, snapshot, tool availability,
and script-audit checks. For each error:

1. identify the violated format or safety invariant;
2. revise the file content in the Host model;
3. call `update_workflow_draft_file` with the path and content; the Host reads and
   injects the current version;
4. let the Host retain the returned incremented draft version for the next update;
5. validate and diagnose again.

Never weaken required outputs, acceptance criteria, safety boundaries, or source
revision linkage merely to silence a diagnostic. On version conflict, reread the
latest draft/diagnostics and reconcile deliberately.

When the source Skill explicitly requires a multi-page composite output, keep the
page contract in the Workflow package rather than in framework prompts:

- represent every page-aligned material as an ordered list slot;
- require producers to publish matching `sort_order` positions across those slots;
- retain `layout: composite`, a `composite_layout` that references every participating
  slot, and `composite_tab_position` when page navigation is required;
- declare full-page HTML with `ui.slots.<slot>.widgetType: html-slide`; never infer the
  widget from a conventional slot name or by inspecting artifact content;
- declare export behavior through `ui.tabs[].actions`, including the provider, input
  slot mapping, formats, and `alignment: sort_order`;
- keep non-HTML composites on their ordinary numbered thumbnail behavior.

During repair, preserve one artifact per page and preserve alignment. Do not collapse
multiple pages into one artifact or model page-aligned materials as unrelated columns.
Compiler diagnostics for widget compatibility, action mappings, and ordered alignment
are authoritative.

## 4. Publish

Call `publish_workflow()` only when strict diagnostics return `valid: true`.
Publish runs the deterministic checks again and creates an immutable Workflow
revision linked to the source Skill revision. It does not call a model. Report the
returned Workflow ref/revision and whether it is enabled; publication does not
imply that a user setting enabled the Workflow.
