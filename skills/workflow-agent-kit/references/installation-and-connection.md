# Install and connect LazyMind

## Check before installing

If `workflow_connection_status` is registered, call it first. A successful result
contains the resolved Core base URL, discovery source, contract version, and a
live capabilities response. The target instance must be selected explicitly;
do not scan for another running installation.

## Install LazyMind

Use an existing LazyMind Desktop installation when possible. For source setup:

```bash
git clone https://github.com/LazyAGI/LazyMind.git
cd LazyMind
make local-up
```

Windows PowerShell uses `make local-win-up`. The complete prerequisites and
Desktop build/download choices are maintained in the repository `README.md`,
`docs/quick_start.md`, and `desktop/README.md`. Do not silently install system
packages or start services without user authorization.

## Register the Workflow MCP server

For external end-to-end tasks, follow `external-agent-quickstart.md`.
The adapter defaults to isolated external mode. Use `--mode full` for the
catalog and Agent-authored package procedures in this document.

Run the repository adapter with the same Python environment that contains
LazyMind's `algorithm` requirements:

```bash
python /absolute/path/to/LazyMind/scripts/lazymind_workflow_mcp.py --mode full
```

Example stdio MCP configuration:

```json
{
  "command": "python",
    "args": ["/absolute/path/to/LazyMind/scripts/lazymind_workflow_mcp.py", "--mode", "full"],
    "env": {
      "LAZYMIND_WORKFLOW_USER_ID": "USER_ID",
      "LAZYMIND_SERVER_URL": "http://localhost:8090"
  }
}
```

For a shared deployment, also set `LAZYMIND_WORKFLOW_BASE_URL` and
`LAZYMIND_WORKFLOW_TOKEN`. Never place a token in the Skill or commit it.

## Endpoint discovery order

The SDK resolves Core in this order:

1. `LAZYMIND_WORKFLOW_BASE_URL`.
2. `LAZYMIND_SERVER_URL` (the browser/server origin, with `/api/core` appended).
3. `LAZYMIND_ENDPOINT_HOST_CORE_BASE_URL`.
4. `LAZYMIND_CORE_API_URL` or `LAZYMIND_CORE_SERVICE_URL`.
5. `LAZYMIND_PUBLIC_URL` (with `/api/core` appended, when no Core URL is set).
6. `generated/service-endpoints.json` under an explicitly selected
   `LAZYMIND_RUNTIME_ROOT`. Without an explicit URL or runtime directory,
   resolution fails with `LAZYMIND_NOT_FOUND`.

This supports dynamically reassigned Desktop ports. A fixed port such as 18001
is a development default, not a discovery protocol.

An explicit `LAZYMIND_RUNTIME_ROOT` selects only that runtime. Missing or invalid
endpoint data does not fall back to a different Desktop installation. Explicit
HTTP(S) URLs are validated before sending credentials. A server URL already
ending in `/api/core` is accepted without adding the prefix twice.

Direct Core URLs are preserved without adding a gateway prefix. Browser URLs
use `/api/core`. When Docker and Desktop run together, set `LAZYMIND_SERVER_URL`
to the instance shown in the browser; do not use another instance's generated
endpoint file. Authenticate as the same user. `workflow_connection_status`
returns `model_configuration` with the user ID, selected provider/model and
readiness from that instance's system model settings (without credentials).

The MCP process binds one SDK client on its first valid tool call. Endpoint,
identity and public-link origin remain fixed for status, submission and polling.
Restart the MCP process after deliberately switching instances or credentials.
Connection failures never trigger discovery of another running instance.

Docker and Desktop share the model configuration API and resolution rules, but
separate installations usually have separate databases and user identities.
Identical settings pages do not imply synchronized settings. The same instance
and authenticated user are required to obtain the same selected model.

## Diagnose failures

- `LAZYMIND_NOT_FOUND`: set an explicit instance URL or runtime directory.
- `IDENTITY_REQUIRED`: set `LAZYMIND_WORKFLOW_USER_ID`, or configure bearer-token
  identity through the deployment gateway.
- `CONTRACT_VERSION_UNSUPPORTED`: update the adapter and Skill together.
- Connection refused: inspect LazyMind runtime status and confirm Core is ready.
- Permission denied: use the same LazyMind identity that owns the Workflow.
