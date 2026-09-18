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
uses dynamic client registration and PKCE. Existing static API key and SSE
connections remain supported. OAuth over SSE, pre-registered clients and CIMD
are outside this version.

## Credentials and failure behavior

Core selects the authenticated user's service and grant. Chat obtains a current
access token via the internal auth-service API before establishing a connection.
Refresh tokens stay in auth service. OAuth tools are not cached across requests.
The internal token endpoint validates the user, server, bound URL, grant and
version; an identifier alone is not authorization.

Database compare-and-set leases coordinate refresh across workers. Reconnection
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
