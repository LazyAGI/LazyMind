# Reuse project knowledge across document tasks

[中文](README.CN.md) · [Full prompts](prompts.md) · [Verification and acceptance](verification.md)

**Status: real Codex CLI / DSH headless outputs and tool traces are available; full acceptance is pending.** Source navigation, provenance-ledger accuracy and independent reproduction remain open. See the [current run and findings](runs/2026-10-14/README.md). Generated files alone do not establish acceptance.

Reuse the existing **Zhice (知策)** product design, PRD, review and handoff examples. Users stay in Codex or DeepSeek Harness (DSH), calling LazyMind through its plugin. LazyMind retrieves and reads project documents; the external Agent writes and saves the deliverables. This example does not run the product delivery Workflow or introduce another research system.

The focus is **reusing the same project sources across Agents and conversations**. Prepare one LazyMind knowledge base; Codex and DSH independently retrieve and read its sources through the plugin, preserving evidence and unresolved decisions across follow-ups, bounded revisions and fresh conversations. No previous conversation summary is copied into the handoff session. Local files are reusable too: keep the [comparison](verification.md#comparison--对照), without claiming fewer actions or faster completion.

## Inputs and provenance

| Pinned input | Existing repository source | Source's own status |
| --- | --- | --- |
| [design.txt](inputs/design.txt) | [Product design HTML](../../skills/featured/product_solution_delivery/assets/design.html) | v1.0; proposed design |
| [prd.txt](inputs/prd.txt) | [PRD HTML](../../skills/featured/product_solution_delivery/assets/prd.html) | v1.0; illustrative draft; rules unconfirmed |
| [review.txt](inputs/review.txt) | [Review HTML](../../skills/featured/product_solution_delivery/assets/review.html) | v2.0; returned for revision; R-01–R-05 unresolved |
| [handoff.txt](inputs/handoff.txt) | [Handoff HTML](../../skills/featured/product_solution_delivery/assets/handoff.html) | v2.0; draft; engineering_readiness: blocked |

[sources.json](sources.json) records the repository revision and HTML SHA-256 hashes. Original case prompts are in [featured.yaml](../../skills/featured/product_solution_delivery/featured.yaml); the Workflow's adaptation history is in [SOURCE.md](../../workflows/product_solution_delivery/SOURCE.md). The sequential prompts here are new reproduction instructions for existing material, not a transcript of the original promotional demo.

Zhice is fictional. The documents are not approved requirements, implemented features or completed tests. Other artifacts mentioned inside them, such as the prototype and version manifest, are outside this four-file input bundle. Do not claim to have read them. Preserve unresolved issues and differences between document statuses.

The Chinese source text is retained without translation. Extraction preserves article text, section anchors, links, table cells and diagram labels, omitting visual layout, navigation and styles. Consult the original HTML for visual relationships. From the repository root, using Python 3's standard library:

```sh
python3 examples/project-knowledge-reuse/prepare_inputs.py --check
# To regenerate the same inputs:
python3 examples/project-knowledge-reuse/prepare_inputs.py
```

Changed source hashes stop regeneration. Review changes and explicitly update the manifest, snapshots and acceptance references before adopting a new source version.

## One-time preparation

1. Start and sign in to your LazyMind instance. Record the deployed version and models; repository HEAD does not prove the running version. Local development validation uses isolated port 8091.
2. Connect your host through LazyMind Settings → External Agent integration, following the [Codex guide](../../integrations/codex-workflow/README.md) or [DSH guide](../../integrations/dsh-workflow/README.md). Record installation, pairing, authorization and required restarts. Use a separate profile or establish the impact before replacing a connection to another instance.
3. Create a dedicated knowledge base named `showcase-zhice-v1`. Upload **only the four TXT files in `inputs/`** and wait for parsing/indexing to finish. Do not upload prompts, acceptance references or generated outputs.
4. Start a new host conversation. Use the plugin to list knowledge bases and documents, then confirm all four documents are readable. Record actual knowledge base and document IDs. Replace prompt placeholders with returned IDs; a display name is not a unique identifier.
5. Retrieve with `knowledge.search`, then request source content or parsed chunks using `knowledge.document.get`. Check truncation and pagination, continuing as needed; a summary is not the original text. Keep actual tool-call evidence. A successful connection or upload alone does not verify retrieval.

## Sequential tasks

Follow the [full prompts](prompts.md), checking and saving each result before proceeding.

| Step | Deliverable | Observable requirement |
| --- | --- | --- |
| A | `01-summary.md` | Cite sources and distinguish case assumptions from verified facts. |
| B | `02-scope-evidence.md` | Locate the R-05 ambiguity and the design/PRD rules before and after starting research. |
| C | `03-design-revision.md` | Propose a change to only the specified paragraph; do not approve rules or close review items. |
| D | `04-handoff.md`, in a **new empty conversation** | Retrieve the four original documents using only the knowledge base identity, recovering R-01–R-05 and closure conditions. |

Do not upload A–C outputs or paste the previous conversation into D. Record any host project instructions or persistent memory that carry over. Step C does not write back to the knowledge base, so D must treat source issues as unresolved. This tests source reuse, not automatic edit synchronization.

Use the Agent's normal file-writing tools to save outputs. Open each file, verify citations and confirm the user can retrieve it. Each citation needs the input filename, section or R/FR/AC identifier, and the document ID actually read. Preserve working source links when tools supply them; do not invent URLs. Evidence must be locatable in the original document.

## Acceptance and limits

Run and record Codex and DSH separately. One host passing does not validate the other. A person who did not implement the example must then reproduce it using only these instructions. See [verification.md](verification.md) for pending records and criteria. Publish sanitized real outputs and record the demo only after acceptance; do not prefill success or efficiency claims.

Four static Chinese documents do not demonstrate large-scale retrieval, live indexing, edit write-back, version management or production readiness. Include knowledge base creation, indexing, plugin setup and manual checking in the cost. The direct-file runs also reuse the same directory successfully. This showcase demonstrates cross-Agent and cross-conversation access, without claiming fewer operations than local files.
