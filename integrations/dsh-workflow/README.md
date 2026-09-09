# LazyMind Workflow for DeepSeek Harness

LazyMind distributes this precompiled bundle with its connector. Connect DeepSeek Harness from LazyMind to install it into the selected DSH profile, configure MCP and create a private pairing file. Users do not build the bundle or modify DSH source.

The supported SDK baseline is DSH 0.1.2-rc.1. The Node Host adapter uses public scoped tools, lifecycle gates and SessionController; the browser adapter projects standard tool events into a session-scoped Workbench window. It never appends a custom `lazymind-workflow/open` event and never reads DSH's signing credentials.

Workflow approval, execution fencing and delivery intent live in Core. The local Bridge authenticates the plugin with a scoped pairing file and relays protocol messages. `concludeTurn` is used only on successful results; already granted workers can drain, including their required structured output. Delivery uncertainty is reconciled against standard host input records and is not blindly retried.

## Build and validate

From this directory, run `pnpm install --frozen-lockfile`, `pnpm run typecheck`, `pnpm test` and `pnpm run bundle`. Then run `go run ./cmd/package-workflow-bundle` from `local/lazymind-cli` to refresh its embedded deterministic tarball. Commit source, compiled JS, lockfile and embedded archive together. CI verifies they match.

## Configuration

The connector writes a user-layer override for `lazymind-workflow-panel`: `bridgeUrl` (loopback root), `webUrl` (LazyMind frontend), `serverName`, and `pairingFile`. The file path is configuration; its secret is read only in the Node Host and is never passed to browser code or MCP results.

`DSH_HOME` selects the installation root, `LAZYMIND_DSH_PROFILE` selects a profile (default `web`), `LAZYMIND_WEB_URL` optionally overrides the frontend origin, and `LAZYMIND_ASSISTANT_BRIDGE_URL` optionally overrides the loopback Bridge root. A disconnected pairing is disabled while retained for explicit reconnection to existing runs.

Closing or minimizing the window does not stop a workflow. Old runs do not gain fabricated approval records. A historical log already corrupted by the old custom event must be backed up and repaired explicitly; replacing the plugin alone cannot run before DSH's cold-reader rejects that log.

## Recovering a log written by the old plugin

Installation never edits historical sessions. For the known non-ignorable `lazymind-workflow/open` failure, stop DSH and preview one affected log:

```sh
lazymind internal agent deepseek-harness repair-log --file /absolute/path/session.jsonl.zstd
```

Inspect the reported event sequences, then apply explicitly with `--apply --offline`. The command makes a byte-for-byte backup, changes only missing `ignorable` flags on the known event, and refuses a log changed during the operation. Keep DSH stopped throughout repair. Restore the reported backup to the original path, with DSH stopped, to undo. Other unknown events are not repaired by this command.
