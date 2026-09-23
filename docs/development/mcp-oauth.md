# Personal remote MCP OAuth

MCP OAuth is a per-user connection. Add a Streamable HTTP server such as
`https://mcp.notion.com/mcp`, select OAuth, save, and connect the account in the
browser. After returning, discover and explicitly select the permitted tools.
OAuth does not grant every discovered tool automatically. Each user adds and
authorizes their own server; OAuth credentials cannot be shared.

## Deployment

- Auth service and Core databases must be migrated before enabling the new UI.
  Auth service uses its existing Alembic migrations; Core uses its existing
  PostgreSQL/SQLite migration runner.
- Set `LAZYMIND_MCP_OAUTH_PUBLIC_BASE_URL` to the trusted public frontend origin,
  for example `https://mind.example.com`. The callback is
  `/oauth/mcp/callback`. HTTPS is required except for local loopback HTTP.
  The local runtime manager defaults this origin to its loopback frontend port.
- Chat receives `LAZYMIND_AUTH_SERVICE_URL` pointing to the internal auth service
  with `/api/authservice`; container and local runtime templates provide it.
- Keep the existing `LAZYMIND_AUTH_CLOUD_SECRET_KEY` stable across auth workers
  and restarts. It encrypts client credentials, tokens and PKCE state in the
  database. Use the same configured internal service token for Core, chat and
  auth service; never expose either secret to the frontend.
- Public OAuth metadata and token endpoints must be reachable directly from
  auth service. The OAuth transport rejects private/local destinations and
  redirects rather than forwarding credentials to another destination.

No Notion REST integration client ID or client secret is entered: this version
uses dynamic client registration and PKCE. Authorization-server discovery supports
OAuth metadata and OIDC metadata, including issuers with path components. Existing static API key and SSE
connections remain supported. OAuth over SSE, pre-registered clients and CIMD
are outside this version.

## Credentials and failure behavior

Core selects the authenticated user's service and grant. Chat obtains a current
access token via the internal auth-service API before establishing a connection.
Refresh tokens stay in auth service. OAuth tools are not cached across requests.
The internal token endpoint validates the user, server, bound URL, grant and
version; an identifier alone is not authorization.

A failed MCP load removes only that server's tools for the current request.
Healthy tools and ordinary chat remain available. Chat shows a sanitized
reauthorization or connection-failure notice and passes the same availability
state to the Agent; credentials and upstream error bodies are excluded.

Database compare-and-set leases coordinate refresh across workers. Tokens are
reused until actual expiry or an explicit rejection of that token version;
there is no fixed early-refresh window. Concurrent callers reuse a newly
refreshed version even when its lifetime is shorter than 30 seconds. Reconnection
and disconnection invalidate old grant versions, so an in-flight refresh cannot
replace a new authorization or restore a disconnected one. A request already
sent to the remote server cannot be recalled. A process crash during remote
refresh may require reconnecting the account.

An explicit authentication rejection can trigger one coordinated refresh and
retry. Timeouts and ordinary tool failures are not replayed. Authorization
failure is separate from a transport/server failure. Disconnecting blocks new
token retrieval immediately; remote revocation is best effort.

## Acceptance checklist

Automated checks cover ownership, state expiry/replay, refresh coordination,
revocation races, caller identity, tool permission filtering and legacy auth.
Run SQLite and PostgreSQL migration/concurrency checks where available.

A real-account acceptance run requires the user to complete Notion login in the
browser, then verify tool discovery, selected search/read tools, restart recovery
and disconnect. Record that result separately from mocked OAuth/expiry tests.
Do not claim a live authorized call based only on discovery metadata or a 401.

## Validation recorded for this change

On 2026-09-18, an isolated SQLite-backed auth service completed real Notion DCR
and PKCE authorization with the user's browser consent. The chat OAuth adapter
and LazyLLM client discovered 45 tools, received workspace-search results with
body highlights, and fetched a document successfully. Explicit refresh and a
subsequent auth-service restart both preserved working access. Core also
successfully discovered all 45 tools after its SSE response parser was fixed.
Disconnect was then verified to reject the old chat grant. These were direct
product service/client calls, not a complete model-driven conversation or the
full production settings UI. No private document text is included in this report.

Targeted automated checks cover SQLite and PostgreSQL, including multiprocess
refresh and revocation races. Frontend production compilation passes. The
repository-wide frontend typecheck has an existing syntax error in
`src/modules/chat/utils/message.test.ts`; that separate typecheck is not
represented as passing.

The parent PR depends on LazyLLM PR #1330. Both branches have been synchronized
with main, including the host-file API migration. The parent remains a draft
until its gitlink can point to the merged dependency revision. MCP runtime
validation uses SDK 1.x, matching LazyMind's `mcp<2` dependency constraint.

## Review regression checks

On 2026-09-18, review fixes added coverage for mixed healthy/failing MCP loads,
all-MCP-failed chat continuation with visible notices, static API-key same-origin
307 discovery and calls, short-lived token reuse with and without refresh
credentials, concurrent short-token rotation, and path-aware OIDC fallback.
These are automated tests using a simulated model/provider and local HTTP MCP
server; they do not extend the real Notion end-to-end acceptance recorded above.
The earlier CI lint failure was introduced by this PR's new Python files; it
was corrected without relaxing repository lint rules.

The OAuth tests now live in `tests/backend/auth-service/test_mcp_oauth.py`, within
CI's pytest collection directory. The complete auth-service coverage command
collects and passes 185 tests, including all 19 OAuth tests; the model-export
assertion includes both new OAuth models. The aggregate rollback now drops
`mcp_servers.auth_type` in the common SQL section for both database dialects.
`scripts/test_migration_upgrade.sh` passes against disposable PostgreSQL, and
the SQLite migration tests pass. Earlier standalone dev-migration verification
did not cover this aggregate rollback bug.

Python 3.10 lint and all nine MCP auth tests pass using the existing AnyIO
exceptiongroup backport. No new production dependency was added. Main-branch
MCP system/user namespaces are preserved alongside OAuth failure isolation.
