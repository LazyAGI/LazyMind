# Paper library persistence

The production paper library must use the server as its source of truth. Browser storage is limited to the explicit preview and recovery of data created before server persistence existed.

Implementation and verification:

1. Store the complete version 3 document graph per dataset in the core database, with dataset permissions, bounded validation, and optimistic revision checks. Preserve papers, folders, collections, tags, attachment references, notes, annotations, relations, merge snapshots, and removal markers together.
2. Load the server before editing. Await every write before changing saved state or reporting success. Reject stale revisions and surface failures without falling back to browser-only saves.
3. Migrate legacy browser data only after fetching the server. Preserve the original snapshot on the server, retain the browser copy, and never overwrite an existing server record during migration. Use a content fingerprint to make migration idempotent.
4. Load exports from the server. Refresh shared views on focus and keep arXiv synchronization compatible with versioned writes.
5. Test independent clients, fresh browser storage, denied access, failed reads/writes, conflicts, migration, and preservation of the complete graph. Build and deploy core/frontend at port 8090, then verify against the running database.
