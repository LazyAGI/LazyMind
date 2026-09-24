# User environment variables

Only user-level variables are persisted and managed in Settings. Conversation
variables remain in process memory. Exact, case-sensitive names are preserved.
The effective priority is conversation > user > process environment. Values are
injected into skill subprocesses; configuration tools receive names, not values.
Names may include ordinary application settings such as SERVICE_BASE_URL,
AWS_REGION and APP_ID. Syntax/length validation preserves case; known runtime
controls (proxy, loader, interpreter startup and trust-store overrides, including
NODE_TLS_REJECT_UNAUTHORIZED) are blocked. Previously stored reserved names cannot
be loaded into runtime or re-enabled; they can still be disabled or deleted.
All values are treated as sensitive, regardless of their names.

## Secure chat input

The agent calls set_session_env or set_user_env with a name and optional
non-sensitive metadata. The tool emits an input card and stops the turn.
The password input posts directly to the authenticated Core
conversations/{id}:env-input endpoint. It never uses query, ask_user answers,
answer autosave, tool arguments or model history to transport values.
Core checks conversation ownership, the persisted card and its target version.
Only a configured/canceled receipt is persisted and sent when chat resumes.
Dual-answer mode does not support secure input cards yet. Such requests display
and persist a notice to switch to single-answer mode and resend the configuration
request (or use Settings for user variables), without saving a variable.
Switching conversations or replacing a card discards its pending UI callbacks;
an already-submitted save may finish on the server but cannot resume another chat.

Session input is routed to the worker that issued the card and remains in memory.
Discovery probes only metadata, with at most eight concurrent probes and an
eight-second total deadline including the write. Values go only to the owning
worker; an uncertain write is not retried against another worker.
Pending session cards expire after 30 minutes; cleanup or a worker restart
invalidates them. User input uses the same encrypted domain service as Settings.
Retries of an already-consumed card return its receipt without writing again.
Cancel never changes the variable. If a session save already succeeded but its
response was lost, cancellation returns the committed configured receipt instead
of claiming to undo the write. Deleting a session override remains immediate;
deleting a user variable still requires explicit confirmation.

Do not paste secrets into ordinary chat: ordinary messages are model input and
this protocol cannot retroactively remove values already sent that way. Use the
dedicated card or Settings. Existing history/tool redaction remains in place.

## Encryption and deployment

New credentials use version 2 AES-GCM with authenticated user ID, variable ID,
name, purpose, schema version and credential revision. The desktop uses the
existing OS secure-store key manager. There is no public default encryption key.

Default Docker Compose startup runs `user-env-key-init` before Core (and
core-dev). It generates a random key on first use and reuses the host file
`data/core/user-env.key` thereafter, including keys created by the previous
local override setup. Core reads a read-only directory mount at
`/run/secrets/user-env/user-env.key`. Ordinary `docker compose up -d` needs no
extra startup flags or local override file. The initializer changes only the
key file, never other contents or permissions of `data/core`.

Back up this key separately from the database. It is ignored by Git and lives
outside container storage, so recreating a container does not rotate it. An
invalid existing key or a different explicitly configured key stops initialization
instead of overwriting it. Do not delete the key when retaining the database.

For managed deployments, Compose also accepts the following host settings as
initial key inputs (file takes precedence). All replicas must receive the same
key; changing an input does not rotate the persisted key. Outside Compose,
servers without an OS secure store must configure one of these on **Core**:

- `LAZYMIND_USER_ENV_SECRET_KEY_FILE`: a private regular file (0600), containing
  a randomly generated secret of at least 32 bytes. This takes precedence.
- `LAZYMIND_USER_ENV_SECRET_KEY`: the same secret supplied by a secret manager.

Generate a key with `openssl rand -base64 -out /private/path/user-env.key 32` and
restrict its permissions before mounting it read-only into Core. The file stores
the encryption key, not users' environment variables. All Core replicas must use
the same server key. Keep it outside the repository and back it up separately
from the database. Missing, short or unreadable keys fail closed. Changing a
version 2 server key without re-encrypting existing rows makes them unreadable;
key rotation is not automatic.

Version 1 credentials created during development are upgraded on successful
read, using a conditional update. The old public key is accepted only to decrypt
version 1 rows, never for new writes. If a custom old key differs from the new
server key, supply it as `LAZYMIND_USER_ENV_LEGACY_SECRET_KEY` during migration.
An unavailable new key leaves the old database row unchanged. Metadata, soft
deletions and per-user ownership are preserved.

## Updates and lifecycle

PATCH handles values, names, descriptions and enable/disable. Updates check the
loaded revision, ciphertext and timestamp, modify only submitted fields, and
return HTTP 409 on a concurrent change. They never recreate a deleted row.
Settings sends only changed fields and includes `expected_updated_at` from the
displayed record on edits and toggles. Core checks that timestamp at database
microsecond precision before mutating the row. Stale clients receive HTTP 409;
the UI refreshes only the conflicting row and keeps unsaved edits visible until the dialog is
closed and reopened. Chat input cards bind an existing variable's version too;
stale cards cannot overwrite a change made in Settings.

Credential values preserve leading and trailing whitespace; empty, whitespace-only
and NUL-containing values are rejected. Masks fully hide credentials of 16 or
fewer characters; longer values expose only the first and last four characters.
List responses include `credential_status` (`available` or `unavailable`). An
unreadable credential does not hide the remaining list: its metadata remains
visible with a fixed mask and it can be disabled, deleted, or replaced without
decrypting the old value. Enabling or renaming without a replacement requires a
readable credential. Replacement still requires a working encryption key. An
enabled unreadable credential fails runtime resolution; it is never silently
omitted to fall back to a process-level credential.

Each chat turn reloads enabled user credentials. A change in Settings therefore
takes effect on the next turn; an already running subprocess retains its own
environment. Submitting a chat input card resumes in a new turn, preserving a
same-name conversation override. Tool replies expose only status, name and scope.
When a chat input card updates an existing value, omitted descriptions and enabled
flags stay unchanged; explicit empty descriptions and false flags are applied.

SubAgent runs and remote workflow attempts also load enabled credentials from
the task owner's database records immediately before execution. Retries and
post-step checkpoint recovery fetch the current values, including enable/disable
and deletion changes. Credentials travel only in private runtime fields, never
in durable task parameters, snapshots, checkpoint data or model prompts. Each
execution replaces its environment map so it cannot retain stale credentials.
Conversation overrides remain local to the main chat runtime; they are not
implicitly inherited by independent SubAgent tasks.

Without an explicit persistence request, variable setup uses `set_session_env`.
Requests for permanent, user-level, future-conversation or Settings-saved values
use `set_user_env` only, without creating an additional conversation override.

Archive/trash/purge notifications clear conversation memory. In Router mode,
cleanup is broadcast to registered starting, healthy and unhealthy workers across
algorithm versions. A worker failure returns 503 instead of claiming success.
Notifications are best-effort; restarting a worker also discards its memory.
Restoring a conversation does not intentionally restore its credentials.
Moving a task conversation to trash from Task Center uses the same cleanup
notification, only after the database transaction commits. Cleanup invalidates
the active conversation generation: tools from an older turn cannot repopulate
its store. A new turn gets a fresh generation and may set new credentials.
Generation handles are weakly retained except for short-lived pending input cards.
Conversation credentials are not shared between workers; multi-worker routing
still requires session affinity for them to survive between turns.

## Verification

Use the `lazy-env` Python environment with `PYTHONPATH=algorithm:algorithm/lazyllm`.
Tests cover precedence, disable, value redaction, worker fan-out/failure, AES-GCM
record binding, legacy upgrades, conditional writes, user isolation, soft-delete
recreation, and the Settings controls. Run full frontend type checking as well:
the existing `npm run typecheck` targets only the chat configuration component.
