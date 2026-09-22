# User environment variables

Only user-level variables are persisted and managed in Settings. Conversation
variables remain in process memory. Exact, case-sensitive names are preserved.
The effective priority is conversation > user > process environment. Values are
injected into skill subprocesses; the model receives variable names only.

## Encryption and deployment

New credentials use version 2 AES-GCM with authenticated user ID, variable ID,
name, purpose, schema version and credential revision. The desktop uses the
existing OS secure-store key manager. There is no public default encryption key.

For servers without an OS secure store, configure one of these on **Core**:

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
closed and reopened. The precondition is optional for non-UI callers such as
the chat tool, which submits partial updates without replaying a UI snapshot.

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
environment. The chat tool updates the current run too, preserving a same-name
conversation override. Tool replies expose only status, name and scope metadata.
When the chat tool updates an existing value, omitted descriptions and enabled
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
Generation handles are weakly retained, so unused handles do not accumulate.
Conversation credentials are not shared between workers; multi-worker routing
still requires session affinity for them to survive between turns.

## Verification

Use the `lazy-env` Python environment with `PYTHONPATH=algorithm:algorithm/lazyllm`.
Tests cover precedence, disable, value redaction, worker fan-out/failure, AES-GCM
record binding, legacy upgrades, conditional writes, user isolation, soft-delete
recreation, and the Settings controls. Run full frontend type checking as well:
the existing `npm run typecheck` targets only the chat configuration component.
