# Paper library persistence

The server is the source of truth. The library uses normalized `paper_library_entries`, `paper_library_folders`, `paper_library_attachments`, and `paper_library_notes`. Entries own reading state, tags, filing, revisions, and confirmed bibliography. `academic_works` owns scholarly identity; attachments retain document and import identities. Removing an entry or detaching an attachment does not delete the original document.

## Upgrading existing libraries

The pending normalized-entity migration retains `paper_libraries`. Its original `data_json`, revision, and timestamps form an immutable archive. The subsequent preservation migration creates `paper_library_legacy_states` and converts `datasets.ext.library_kind = 'paper'` to `datasets.scene = 'academic_papers'`, without removing other dataset settings.

`paperlibrary.EnsureLegacy` converts an archived library in one database transaction before its first library access or statistics query. The transaction locks the dataset, imports papers, folder hierarchy, attachments, notes, and the owner's important-paper marks, and commits `migrated_at` with the normalized rows. Failed conversions roll back completely; the original graph remains available for a safe retry. A committed marker prevents subsequent reads, restarts, or catalog refreshes from replaying old state over normalized edits or removed memberships.

Duplicate legacy bibliography identities keep separate private work identities when necessary to retain distinct entries, with their confirmed identifiers and a visible association warning. Existing scholarly work IDs are reused even when no public identifier is available. Hidden papers and orphaned historical child materials are retained behind removed-entry tombstones, so migration does not resurrect previously removed papers. Ambiguous document memberships, foreign-dataset documents, or invalid folder trees fail conversion atomically and leave the archive intact for explicit resolution. No migration silently discards such records.

## Auxiliary data and complete backups

`paper_library_legacy_states.extras_json` stores auxiliary data that is not represented by normalized columns: collections, annotations, relations, merge and migration snapshots, source identities, additional metadata, and unknown future fields. Reads merge these fields behind the current normalized entities. Current revisions, bibliography, filing, notes, attachments, and removal state take precedence; an archived entity cannot reappear merely because it remains in auxiliary JSON.

The API returns auxiliary fields alongside `data.folders`, `data.papers`, `data.attachments`, and `data.notes`. `data.legacy` contains the immutable original graph for complete backup and audit. Per-entity extensions such as `zoteroSources`, `collectionIds`, metadata confidence, PMID, ISBN, attachment MIME type, and file size round-trip through the projection. A complete backup should serialize the entire returned `data`, including unknown fields and `legacy`. Restoring a backup into another library preserves its original archive in `restoredArchives`, deduplicated by a SHA-256 content identity; it never overwrites the destination archive. Backup/import envelopes support up to 48 MiB.

## Mutations and imports

`GET /academic/library?target_dataset_id=…` returns the current data, write permission, counts, and processing activity. Paper and folder mutations use their current entity revision; stale writes return `409 PAPER_LIBRARY_CONFLICT`. The former whole-graph `PUT` contract is retired. The normalized PATCH/POST endpoints perform individual changes transactionally.

Moving a paper replaces its source collection memberships with the destination folder, or clears them when moving to the library root. Deleting a folder subtree moves normalized papers to the deleted root's parent and removes the deleted folder IDs from live auxiliary collections and paper memberships, including hidden historical records. Original notes, annotations, document files, and the immutable graph archive remain retained.

`POST /academic/zotero/import` submits confirmed metadata to `paperlibrary.ImportSnapshot`. One transaction adds new stable entities, resolves scholarly identity, remaps dependent references, and merges auxiliary data. Repeating the same source import reuses existing records. Existing corrected bibliography, reading state, filing, and notes take precedence over source snapshots. Each attachment retains its own source even when its paper matches an existing bibliography from another provider. Attachment upload happens after metadata confirmation and preserves its saved document identity when processing or response delivery fails.

Attachment availability requires an existing, non-deleted document backed by a regular original file. Missing files remain visible as unavailable historical references. Their presence does not enable reader actions or silently recreate removed membership.

Complete version 3 and 4 snapshots include the folders, papers, attachments, and notes arrays. They may retain children of previously removed papers and unavailable original-document references. Import preserves those children behind removed-entry tombstones and retains their source snapshot in backup history. Missing or deleted originals never create new scholarly document links; references to another dataset are rejected. Partial imports still require existing parent papers and document references.

## Verification

`paperlibrary/legacy_test.go` covers populated conversion, full auxiliary preservation, idempotent imports, injected transactional failure, normalized edits after migration, and SQLite close/reopen. The same package tests run on PostgreSQL with the isolated-schema ORM test configuration.

`migrate/legacy_paper_library_test.go` inserts a populated graph immediately before the normalized-entity SQL migration, executes the remaining real migration sequence, and checks the archive, dataset scene, normalized projection, auxiliary fields, and repeated reads on SQLite and an isolated PostgreSQL database. Aggregate/dev schema parity and rollback tests remain in `migrate/paper_library_test.go`.
