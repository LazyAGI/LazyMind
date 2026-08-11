# Install and connect LazyMind

## Check before installing

If `workflow_connection_status` is registered, call it first. A successful result
contains the resolved Core base URL, discovery source, contract version, and a
live discovery response. Do not ask the user for a port when automatic discovery
works.

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

Run the repository adapter with the same Python environment that contains
LazyMind's `algorithm` requirements:

```bash
python /absolute/path/to/LazyMind/scripts/lazymind_workflow_mcp.py
```

Example stdio MCP configuration:

```json
{
  "command": "python",
    "args": ["/absolute/path/to/LazyMind/scripts/lazymind_workflow_mcp.py"],
    "env": {
      "LAZYMIND_WORKFLOW_USER_ID": "USER_ID"
  }
}
```

For a shared deployment, also set `LAZYMIND_WORKFLOW_BASE_URL` and
`LAZYMIND_WORKFLOW_TOKEN`. Never place a token in the Skill or commit it.

## Endpoint discovery order

The SDK resolves Core in this order:

1. `LAZYMIND_WORKFLOW_BASE_URL`.
2. `LAZYMIND_ENDPOINT_HOST_CORE_BASE_URL`.
3. `LAZYMIND_CORE_API_URL` or `LAZYMIND_CORE_SERVICE_URL`.
4. The local runtime's generated `service-endpoints.json` under
   `LAZYMIND_RUNTIME_ROOT` or the platform LazyMind data directory.

This supports dynamically reassigned Desktop ports. A fixed port such as 18001
is a development default, not a discovery protocol.

## Diagnose failures

- `LAZYMIND_NOT_FOUND`: start LazyMind or set an explicit base URL.
- `IDENTITY_REQUIRED`: set `LAZYMIND_WORKFLOW_USER_ID`, or configure bearer-token
  identity through the deployment gateway.
- `CONTRACT_VERSION_UNSUPPORTED`: update the adapter and Skill together.
- Connection refused: inspect LazyMind runtime status and confirm Core is ready.
- Permission denied: use the same LazyMind identity that owns the Workflow.
