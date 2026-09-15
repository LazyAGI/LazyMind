# Existing deployed Workflow authoring

Use this procedure to inspect or modify a Workflow already stored by a LazyMind
deployment. “Modify the database Workflow” means changing its editable draft
through the public authoring contract; it does not authorize direct SQL writes.

## Collect or discover the minimum context

Accept any stable locator the user has:

- Deployment URL or an open Workflow detail URL, such as
  `http://host:port/workflows/<draft-id>`.
- Draft id, Workflow id, Workflow ref, or published revision id.
- Desired behavioral or file-level changes and acceptance criteria.
- Whether the user wants only a saved draft or also wants it published.

Discover the base URL, local runtime, authenticated user, owner, current draft,
and revision metadata before asking for them. Do not ask for a database password,
API token, port, or owner id when the local deployment/profile can resolve it.
If authentication or ownership cannot be resolved, request the least-privileged
credential or the correct signed-in identity; never print or persist secrets.

Ask a concise clarifying question only when ambiguity affects the result, for
example when multiple owner-visible Workflows match, the requested behavior is
underspecified, or replacing the current draft would discard unrelated changes.

## Connect and locate the record

1. Prefer `workflow_connection_status`, then public Workflow MCP authoring tools.
2. Otherwise discover Core using `installation-and-connection.md` and use its
   authenticated public authoring API. A frontend port may proxy Core but is not
   itself proof of the Core base URL.
3. For a source checkout with a running Docker Compose deployment, inspect service
   status and configuration before connecting. Read-only SQL may locate a draft
   when the API cannot: in current LazyMind schemas, the URL UUID commonly maps to
   the Workflow draft table's primary id, while published resources, immutable
   revisions, and revision entries are stored separately. Discover these relations
   from the live schema and repository models; do not assume physical names or
   columns across versions.
4. Bind every read and mutation to the authenticated owner. Treat “not found” as a
   possible identity mismatch, not permission to query or alter another owner.

Never copy credentials into commands that will be committed, logs shown to the
user, Skill files, or source files.

## Inspect before changing

Read and retain:

- Draft id, owner, draft version, dirty/deleted/generation state, and timestamps.
- Workflow id/ref, head revision id/no, base revision, and publication status.
- Exact `workflow.yaml`, `scenario/state.yml`, `scenario/scenario.md`, optional
  `scenario/driver.md`, scripts, and layout content.
- Current compiler diagnostics and script/capability audit.

Report whether the locator identifies an unpublished draft, a draft based on a
published revision, or an immutable published revision. Distinguish the draft's
optimistic-lock version from the public Workflow revision number.

## Modify safely

1. Translate the user's request into explicit file changes. Preserve unrelated
   content and any newer edits discovered after the initial read.
2. When starting from a published revision, call the version-to-edit operation to
   materialize that exact revision as the editable draft. Warn before replacing a
   dirty draft with older content and require user confirmation if work may be lost.
3. Submit exact file content through `update_workflow_draft_file` or the equivalent
   public draft API with the latest expected draft version. Refresh and reconcile
   on conflict; never overwrite blindly.
4. Run draft validation and strict diagnostics. Repair graph, required-file,
   capability, and script-audit errors without weakening requested behavior.
5. Re-read the saved draft and compare the effective changes with the request.

Do not use SQL `INSERT`, `UPDATE`, or `DELETE` for authoring. Direct writes can
desynchronize revision entries, blobs, tree hashes, compiled graphs, head pointers,
audit metadata, and optimistic-lock versions. If public mutation tools are absent,
stop after read-only inspection and explain the missing capability.

## Decide whether to ask about publication

Publication is a separate material action because it creates an immutable revision
that may become available to future runs.

| User intent and state | Action |
| --- | --- |
| User explicitly says publish, release, save as a new version, or equivalent | Validate, then publish without asking again. |
| User asks to modify, fix, or save but does not mention publication | Save and validate the draft, then ask whether to publish. |
| User explicitly asks for draft-only changes or says not to publish | Do not publish and do not repeatedly ask. |
| Draft is invalid or diagnostics remain | Do not offer publication as ready; report blockers first. |
| Existing Workflow has no published revision | Describe publication as the first version, not a new version. |
| Existing Workflow has a published head and the draft differs | Describe publication as a new immutable revision and state the next revision number when authoritative metadata provides it. |
| Publish result is unknown or times out | Re-read draft and revisions before retrying or claiming success. |

Before publishing, state the target Workflow/ref, whether this is first publication
or a new revision, and any user-visible behavior change. After publishing, report
the returned revision id/no and whether enablement is a separate action.
