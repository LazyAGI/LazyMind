# English reproduction prompts

[中文](prompts.CN.md) · [Guide](README.md)

These prompts are new reproduction instructions for the existing Zhice material. Replace `<KB_ID>` with the dedicated knowledge base ID returned by the plugin, and `<OUTPUT_DIR>` with a writable absolute path. Complete setup first. Do not upload this file to the knowledge base. Sources remain in Chinese; write the deliverables in English while preserving source identifiers.

## A — Summary (new conversation)

```text
Use the LazyMind plugin to access design.txt, prd.txt, review.txt and handoff.txt in knowledge base <KB_ID> (showcase-zhice-v1). Write a Zhice project summary for a new teammate and save it as <OUTPUT_DIR>/01-summary.md.
Use LazyMind only for retrieval and source reading; write the deliverable yourself. First list the documents to establish their identities, then search and read relevant sources. Do not read the local repository, browse the web or run a Workflow.
Cover users and tasks, main flow, role boundaries, version/export rules, and current review and handoff status. Separate case assumptions, proposed rules, verified facts and unknowns. Cite the document ID actually read, filename and section/item for each key claim. Preserve working source links if supplied by the tools.
Report missing or inconsistent evidence without turning it into approved facts. Documents mentioned by a source but absent from this input bundle are not evidence you have read. List the documents you actually searched/read and any steps you could not complete.
```

## B — Sourced follow-up (same conversation)

```text
Continue using the LazyMind plugin to search and read knowledge base <KB_ID>.
What scope-change wording does review item R-05 refer to? Compare the product design and PRD source text: how should scope changes be handled before versus after research starts, why might the current wording be misread, and does this affect the blocked engineering handoff status?
Save <OUTPUT_DIR>/02-scope-evidence.md. Cite locations and actual document IDs from all three sources. Separate source rules, your interpretation and decisions that remain unaccepted. Do not answer solely from the previous summary or claim a fix or retest has occurred.
```

## C — Local revision proposal (same conversation)

```text
Based on the sources just read through LazyMind, propose the smallest change to the paragraph containing “改动范围后须重新确认” in design.txt: clarify that changes before starting require reconfirmation, while changes after starting require a new task.
Save the original excerpt, replacement excerpt, source location and rationale as <OUTPUT_DIR>/03-design-revision.md. Change only that wording; preserve other content, roles, data policies and technical constraints. Label it “Proposed revision, awaiting confirmation”; do not mark R-05 resolved.
Create a proposal file without changing the source or knowledge base. This does not approve the design. Check whether the replacement introduces unsupported rules and report your finding.
```

## D — Handoff in a new conversation

Create a new empty conversation in the same host. Send only the prompt below, without A–C outputs or a conversation summary.

```text
Use the LazyMind plugin to access the four original Zhice example documents in knowledge base <KB_ID> (showcase-zhice-v1). Produce a handoff checklist for product, design, engineering, testing and business/security roles. Save it as <OUTPUT_DIR>/04-handoff.md.
First establish document identities, then search and read the source text. Do not read the local repository, other conversations or the web. Do not run a Workflow.
List adopted files and versions, current engineering readiness, all unresolved review items and their severity, closure conditions, suggested responsible roles and required evidence. Cite actual document IDs, filenames and sections/item identifiers. Distinguish demonstration readiness, proposed rules, formal acceptance and passed tests. Do not invent named owners or dates.
Judge only the current knowledge base contents. Do not assume an unrecorded revision proposal has taken effect. State what material or validation is missing, who needs to confirm what next, and which documents you actually read.
```

Run notes: distinguish new reads in this turn from source text retained from earlier turns. `original_file.download_url` is an API download path; do not prepend the web origin and present it as a source page. If a verified page URL is unavailable, retain document ID, filename and section. D requires an actual `knowledge.search` call; listing documents or reading full text does not count as search. The run record retains the initial omissions and recovery prompts.
