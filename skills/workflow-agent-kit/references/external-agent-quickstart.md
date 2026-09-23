# External Agent Quickstart

## Connect once

Start the adapter in the Python environment containing LazyMind's algorithm
dependencies. External mode exposes only connection, submission, task status and
task result. It does not expose the internal Skill or Workflow catalogs.

```json
{
  "command": "/absolute/path/to/lazy-env/bin/python",
  "args": [
    "/absolute/path/to/LazyMind/scripts/lazymind_workflow_mcp.py",
    "--mode", "external",
    "--agent-type", "codex"
  ],
  "env": {
    "LAZYMIND_SERVER_URL": "https://lazymind.example",
    "LAZYMIND_WORKFLOW_TOKEN": "<deployment-issued-token>"
  }
}
```

Use the host's MCP configuration UI to register this stdio server. Substitute
`trae-work`, `workbuddy`, `cursor`, `raccoon-work`, or `deepseek-harness` for
`codex` as appropriate. A configured agent type is injected automatically and
cannot be overridden by tool arguments. It is routing metadata, not proof of
client identity; the deployment still authenticates and authorizes the user.

For a local trusted development deployment, use its configured user identity
through `LAZYMIND_WORKFLOW_USER_ID`. For Desktop, explicitly select its runtime
directory with `LAZYMIND_RUNTIME_ROOT` or set a Core URL. Use `LAZYMIND_PUBLIC_URL` when the
browser address differs from the Core server address. Never commit credentials.

For the Docker instance opened at `http://localhost:8090`, use
`LAZYMIND_SERVER_URL=http://localhost:8090`. The SDK addresses its gateway at
`http://localhost:8090/api/core`. This is an example deployment address, not a
hard-coded port. Desktop can instead discover its direct Core URL from
`generated/service-endpoints.json` under that selected directory; direct Core
URLs do not get `/api/core`. No platform-directory scanning is performed.
When both deployments exist, select the intended instance explicitly and use
the same authenticated user as its settings page. Set `LAZYMIND_PUBLIC_URL` to
Desktop's actual browser origin if using a direct Core connection.

No model/provider/API-key settings belong in this MCP configuration. Core loads
the task owner's current system model selections at generation time through the
same resolver used for connection readiness. Check `model_configuration` in
`workflow_connection_status`; `connected` alone does not mean a model is ready.
The MCP connection is fixed until the adapter process restarts.

Call `workflow_connection_status` first. It returns supported Agents, request
limits and contract version without listing internal Skills. Core's background
workers must be enabled. After updating Core, restart it at a convenient time
so the new external-task job handler is registered.

## Submit a URL

Call `start_skill_workflow_task`:

```json
{
  "skill": {"name": "report-skill", "url": "https://example.com/report-skill.zip"},
  "task_description": "Use the supplied materials to produce a report.",
  "idempotency_key": "report-request-001"
}
```

SkillHub and GitHub Skill page URLs are also supported. Use a versioned URL or
a ZIP when exact source bytes matter; the same URL reuses its installed source
and does not automatically check the remote server for updates. Names alone
never select an unrelated installed Skill. The idempotency key is optional, but
reusing a known key with unchanged arguments makes retries safe.

## Submit a ZIP directly

Use the path to the user's ZIP on the machine running the MCP process:

```json
{
  "skill": {"name": "report-skill", "zip_path": "/path/to/report-skill.zip"},
  "task_description": "Produce a report from this Skill."
}
```

The adapter reads the file and uploads the encoded bytes. It does not ask the
Agent to generate or print base64. A remote MCP process cannot access a file on
the Agent's machine; for that case, send `zip_base64` bytes or a reachable URL.
Direct HTTP clients use `zip_base64`, not `zip_path`. `zip_sha256` is optional.
Exactly one of the source fields is allowed. The ZIP must contain `SKILL.md`.

ZIP limit: 20 MiB. Total HTTP request limit: 32 MiB including base64 expansion.
Small task attachments use `input_files` with `material_id`, `name`,
`mime_type`, and `content_base64`. Material IDs must not collide with each other
or with `input_bindings`.

## Observe and finish

Submission returns `task_id` promptly. LazyMind runs installation, preflight,
generation/reuse, publication, preparation, execution, and result collection in
the background. It does not need repeated calls to make progress.

Call `get_skill_workflow_task` with `{"task_id":"<returned-id>"}` and follow:

| next_action | Agent behavior |
| --- | --- |
| `poll` | Wait `poll_after_seconds`, then read again. Show changed stage/progress. |
| `read_result` | Call `get_skill_workflow_result`, present actual artifacts and link. |
| `open_lazymind` | Stop automatic polling; show the reason, suggestion and product link. |
| `review_error` | Explain the error and suggested fix. Do not silently start a duplicate run. |

`done` indicates success/failure/cancellation; `waiting_user_action` is a
handoff, not success. This release does not accept external confirmation,
editing, or resume commands. Continue handed-off tasks in LazyMind. Core observes
completed handed-off sessions periodically, so a later result query can retrieve
their outputs without automatically resuming any steps.

On `TASK_SUBMISSION_UNCERTAIN`, retry the same request using
`details.idempotency_key`. On `IDEMPOTENCY_CONFLICT`, restore the original
arguments or use a new key for an intentionally new task. On
`SKILL_NAME_CONFLICT`, choose a distinct name or the original source.

## Advanced authoring

Use `--mode full` only when the user wants the external Agent to inspect or edit
LazyMind package files itself. Full mode retains the existing catalog, authoring
and session-bound tools. Hosted task isolation is a tool-exposure boundary;
underlying HTTP access remains governed by the authenticated user's permissions.
