# Paper library persistence

The production paper library must use the server as its source of truth. Production reads and writes never use browser storage. Existing browser copies are left untouched.

## Paper graph and navigation

The graph remains `schemaVersion: 3`. A paper is the library unit; PDFs and their versions are attachments. `PaperFolder.parentId` is optional; null or missing means the library root. `paper.folderId: null` places a paper directly at that root. The root uses the dataset display name and has no stored folder entity. Folder counts include descendant papers; the directory content area shows direct child folders and direct papers. All papers and important papers are global views, independent of directory selection.

`paperlibrary.LoadMany` resolves stored graphs and original documents for library reads, exports, and catalog statistics. Saved bibliographic fields and logical filing take precedence over recognition. Source folders retain their parent relationships, including empty folders. Removing a directory subtree moves its papers to the deleted directory's parent and records source-folder removal markers; original files, notes, annotations and personal stars remain.

## HTTP contract

All endpoints below use the existing `target_dataset_id` query parameter and dataset permission checks.

| Endpoint | Result |
| --- | --- |
| `GET /academic/library` | Resolved `data`, `revision`, `can_write`, `paper_count`, `pdf_attachment_count`, current user's `paper_importance`, and processing `active` flag. |
| `PUT /academic/library` | Versioned graph save. Optional `attachment_context` authorizes an explicitly confirmed attach/detach operation; optional `import_context` preserves bibliography-import receipts. |
| `PUT /academic/library/papers/{library_paper_id}/importance` | Accepts `revision` and `is_important`; resolves stable paper identity and saves the current user's mark, including metadata-only papers. Read access is sufficient for personal marks. |

`attachment_context` contains `operation` (`attach` or `detach`), `paper_id`, `document_id`, `attachment_id`, `confirmed`, and `idempotency_key`. Association validation, known identity conflicts, upload-receipt acknowledgement and the graph revision are checked/saved in one transaction. Stale writes return `409 PAPER_LIBRARY_CONFLICT`; the caller retains its draft and any accepted upload. Detachment adds `hiddenDocumentIds`, retains original files and archived attachment references, and cannot be undone by recognition refresh.

The dataset catalog also supplies `paper_count` and `pdf_attachment_count` for paper collections. `document_count` retains its existing meaning. PDF counts deduplicate active, stored document IDs; supplemental materials, external links, detached/deleted originals and unconfirmed uploads are excluded. Versions share a paper identity by arXiv base ID. Individual attachments keep their actual source version/hash; unknown versions stay unknown, and adding an old version preserves the primary attachment and saved bibliography.

Implementation and verification:

1. Store the complete version 3 document graph per dataset in the core database, with dataset permissions, bounded validation, and optimistic revision checks. Preserve papers, folders, collections, tags, attachment references, notes, annotations, relations, merge snapshots, and removal markers together.
2. Load the server before editing. Await every write before changing saved state or reporting success. Reject stale revisions and surface failures without falling back to browser-only saves.
3. Load exports from the server. Refresh shared views on focus and keep arXiv synchronization compatible with versioned writes.
4. Test independent clients, fresh browser storage, denied access, failed reads/writes, conflicts, and preservation of the complete graph. Build and deploy core/frontend at port 8090, then verify against the running database.

Import confirmation and recovery:

- Selecting a PDF only stages its metadata and browser `File`. Uploading and document-task creation begin after the user confirms adding it. Cancelling before confirmation performs no backend write.
- Once an upload returns a document ID, retain it while the confirmation remains open so retrying a failed library snapshot save does not upload the same file again.
- Upload receipts store the proposed paper and request key at `awaiting_association`. Such uploads remain outside the official paper view until the association save succeeds. The import notification can resume confirmation using the original document. Same-hash uploads reuse the active attachment and acknowledge the duplicate receipt without adding another PDF.
- Both global import and detail-page Add PDF use local identification/preview before confirmation. Detail-page uploads fix the target paper. Verified unique identities recommend association; title/author/year evidence requires manual review. Reliably conflicting identities block attachment. Provider failures allow manual review and do not prevent reading a valid original.
- Batch import retries first recover the saved document and processing task. Download a replacement only if the prior document/file is unavailable; preserve processing errors and save document identity before association writes.
- Persist partial document identity with a bounded detached context when the job is cancelled, then stop processing. Cancellation must not erase the identity needed by the next retry.
- Optional metadata-provider failures do not stop the fallback chain. Successful fallbacks retain provider diagnostics; exhausted failures return their combined errors. Unpaywall 404 is an absent record, not a service outage.
- Cross-process arXiv imports use database-backed coordination; deployment requirements and subprocess tests are documented in `backend/core/academic/arxivsource/README.md`.
