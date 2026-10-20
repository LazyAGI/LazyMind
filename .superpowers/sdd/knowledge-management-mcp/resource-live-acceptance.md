# P7 8091 public resource acceptance — 2026-10-20

## Environment and method

Executed after the parent's READY2 signal and coordinated Core restart. The parent verified that Core, Chat and Caddy use the knowledge-management-mcp worktree and that 8091 uses its isolated sqliteproxy runtime. This agent did not restart services or change runtime configuration. All functional writes used public HTTP APIs against `http://127.0.0.1:8091`, limited to two newly created libraries. No database writes or pre-existing user-resource mutations were used.

The private stdlib acceptance scripts and raw JSON-RPC evidence are under `/Users/dengyuang/.local/share/lazymind-acceptance/knowledge-resources-20261020/`. Session credentials are private, outside Git. Each logical write uses a stable `X-LazyMind-Invocation-Id`; replay sends another real HTTP request with the same header and parameters, using a fresh JSON-RPC ID. Original bytes are submitted through public multipart original upload, then referenced by `uploaded_file` MCP inputs. Libraries use stored processing; these measurements do not claim parsing/indexing performance.

## Functional results

Passed:

- Exact set of 11 public `knowledge.*` tools. Other capability tools may also be listed.
- Two-library catalog pagination (page size 1), merged document/folder pagination (page size 2), unique IDs without gaps/duplicates, and aggregate counts/bytes.
- Nested folder creation, empty folders, library description/scene, folder name, document tags and clearing tags.
- Same-library document move to root (`parent_id` becomes empty), preserving the document ID; moving back into its folder.
- Duplicate folder-name CONFLICT; descendant-cycle INVALID_ARGUMENT; cross-library destination-name CONFLICT. Failed operations keep source resources visible.
- Complete six-resource tree move (three folders and three original files), all target IDs new, old source IDs absent, empty target folder retained, aggregate document count and bytes conserved.
- A second real request with the same move invocation header returns an identical result and ID mapping. Same header with different parameters returns CONFLICT.
- Tree deletion and identical durable delete replay. Public document.get rejects the former source ID with NOT_FOUND; public get succeeds for a current moved document.
- Two independent root moves with one destination conflict return partial (one success, one failure), preserve the successful root's new ID and the failed root's source ID, and replay the same result.
- A tree with 129 resources returns OPERATION_TOO_LARGE with `dimension=resources`, `actual=129`, `limit=128`. Its 128 child IDs are unchanged and the destination has no corresponding root.
- A two-resource tree containing one 33,554,433-byte original returns OPERATION_TOO_LARGE with `dimension=bytes`, `actual=33554433`, `limit=33554432`. Source IDs remain unchanged and the destination has no corresponding root.

The first public create exposed a real missing-default-algorithm defect. It was corrected in the shared resource service, with red/green coverage for tool default, page default and preserved explicit page algorithm. The public create succeeded after the coordinated restart. This correction is also documented in resources-report.md.

## Actual sqliteproxy budget measurements

8091-only candidate configuration supplied by the parent:

```json
{"max_resources":128,"max_bytes":33554432,"prepare_seconds":15,"copy_bytes_per_second":4194304}
```

Created 100 real text originals totaling exactly 32 MiB and 20 empty folders under one root (121 resources). Imported in five batches of 20, then moved the complete root A→B→A→B sequentially. After every move, public metadata showed exactly 120 descendants, the previous source root was invisible, and aggregate counts/bytes remained correct. No concurrent move rounds were used. Other acceptance agents were active and could influence timings.

| Public request | Run 1 | Run 2 | Run 3 |
| --- | ---: | ---: | ---: |
| Complete cross-library move, original copies included | 820.960 ms | 791.301 ms | 806.783 ms |
| Two-library catalog, including statistics | 53.955 ms | 65.833 ms | 62.461 ms |
| Metadata for all 100 documents beneath moved root | 151.247 ms | 90.351 ms | 94.305 ms |
| B aggregate, file type/stage/tag dimensions | 41.954 ms | 65.518 ms | 66.196 ms |

Move sample median was 806.783 ms; maximum was 820.960 ms. Three samples do not establish a statistically useful p95. These are end-to-end public request times, including the actual deployment's sqliteproxy and external authorization calls; they do not isolate transaction and copying phases. The earlier direct SQLite phase instrumentation remains a separate measurement. Disk-cache state was not controlled, no stress/concurrency bound was proven, and parser/index processing was not requested.

B aggregate was 101 documents / 33,554,447 bytes: the 100 budget originals plus the separate 15-byte other.txt fixture. Catalog statistics matched. The candidate budget passed these local deployment checks with substantial time margin and rejected both resource and byte overages without partial migration; wider production rollout still depends on the parent’s full P7 acceptance.

## Retained fixtures

Retained at the parent's request for browser cross-checking; no automatic cleanup was performed:

- A: `ds_2cde15bcd0e38c7c8d62c711f6f57145`, `KM-E2E-20261020-RESOURCES-125624-A`. Contains scratch, oversize (128 empty children), partial-blocked and byte-limit/bytes-over.txt. Scene was set to paper_collection. Parent subsequently owns the page-side description edit/verification.
- B: `ds_d2045ea2a170ed1abc4c0c23933ea7b7`, `KM-E2E-20261020-RESOURCES-125624-B`. Contains other.txt, budget (100 files + 20 empty folders), partial-good and the partial-blocked conflict fixture. Current budget root: `doc_d7bc3a1af7194c854257b7e65cf1be12`.

The parent independently confirmed B's budget documents, empty folders and stored other.txt through the browser. Browser mutation/round-trip verification and other agents' original-source, tenant, Chat/Work, CLI acceptance belong to their reports.

## Page edit parity follow-up

The parent edited A's description and tag through the browser. Core persisted both, but the edit modal stayed open. Same-value public API diagnosis showed dataset PATCH 200, learning-capabilities GET `items=[]`, followed by the modal's unconditional capabilities PUT returning 400 (`at least one capability is required`). Thus the failure occurred after successful metadata persistence and was not a processing-level transition failure.

UpdateModal now records the loaded active capability references and submits capability settings only when they actually change, reusing the existing lodash equality helper. Metadata edits on libraries without learning capabilities no longer submit an invalid empty configuration. Unchanged configured capabilities also avoid an unnecessary write. Clearing a previously configured selection remains an actual change and keeps the service's existing validation.

Red/green UI regression reproduced the old modal remaining open. Three UpdateModal tests plus nine existing CreateKnowledgeBaseModal tests passed (12 total), covering unconfigured metadata edit, unchanged configured capabilities and explicit clearing of existing capabilities. `git diff --check` passed. Only same-value diagnostic writes to A were used; subsequent GET confirms `scene=paper_collection`, `processing_level=stored`, the parent’s description and tag are intact. Public MCP knowledge.list reads the exact updated description and tag. Final browser modal-close verification awaits the parent’s frontend rebuild/refresh.
