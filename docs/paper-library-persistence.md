# Paper library persistence

The production paper library must use the server as its source of truth. Production reads and writes never use browser storage. Existing browser copies are left untouched.

Implementation and verification:

1. Store the complete version 3 document graph per dataset in the core database, with dataset permissions, bounded validation, and optimistic revision checks. Preserve papers, folders, collections, tags, attachment references, notes, annotations, relations, merge snapshots, and removal markers together.
2. Load the server before editing. Await every write before changing saved state or reporting success. Reject stale revisions and surface failures without falling back to browser-only saves.
3. Load exports from the server. Refresh shared views on focus and keep arXiv synchronization compatible with versioned writes.
4. Test independent clients, fresh browser storage, denied access, failed reads/writes, conflicts, and preservation of the complete graph. Build and deploy core/frontend at port 8090, then verify against the running database.

Import confirmation and recovery:

- Selecting a PDF only stages its metadata and browser `File`. Uploading and document-task creation begin after the user confirms adding it. Cancelling before confirmation performs no backend write.
- Once an upload returns a document ID, retain it while the confirmation remains open so retrying a failed library snapshot save does not upload the same file again.
- Batch import retries first recover the saved document and processing task. Download a replacement only if the prior document/file is unavailable; preserve processing errors and save document identity before association writes.
- Persist partial document identity with a bounded detached context when the job is cancelled, then stop processing. Cancellation must not erase the identity needed by the next retry.
- Optional metadata-provider failures do not stop the fallback chain. Successful fallbacks retain provider diagnostics; exhausted failures return their combined errors. Unpaywall 404 is an absent record, not a service outage.
- Cross-process arXiv imports use database-backed coordination; deployment requirements and subprocess tests are documented in `backend/core/academic/arxivsource/README.md`.
