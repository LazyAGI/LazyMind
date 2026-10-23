# Zotero-style Paper Library Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Upgrade the paper preview into a local-first, visually polished literature library where Paper is the core entity and files, notes, annotations, collections, tags, relations, translators, deduplication, merge undo, and exports are first-class features.

**Architecture:** Keep the existing React page and localStorage persistence, introduce a versioned PaperLibraryData model with migration from the current flat paper records, and isolate import/export/duplicate/merge operations in pure modules. Update the existing PaperLibrary UI with a structured import panel and a detail drawer rather than replacing the page shell.

**Tech Stack:** React 18, TypeScript, Ant Design, Sass, Vitest, localStorage.

## Global Constraints

- Paper is the canonical entity; PDFs and other files are attachments.
- Import failures must preserve the attachment and create an incomplete paper record.
- Duplicate handling must offer merge and preserve a reversible snapshot.
- Citation export and complete-library backup remain separate flows.
- The preview must remain usable without a backend and retain responsive keyboard-accessible UI.

---

### Task 1: Versioned core model and migration

**Files:**
- Create: `frontend/src/modules/knowledge/paper/libraryModel.ts`
- Modify: `frontend/src/modules/knowledge/paper/model.ts`
- Modify: `frontend/src/modules/knowledge/paper/store.ts`
- Test: `frontend/src/modules/knowledge/paper/libraryModel.test.ts`

- [ ] Define `Paper`, `Attachment`, `Note`, `Annotation`, `Collection`, `Tag`, `PaperRelation`, `MergeSnapshot`, and `PaperLibraryDataV3` types.
- [ ] Add `migratePaperLibrary()` that converts existing `PaperItem[]` into V3 Paper records and creates one attachment when a legacy file reference exists.
- [ ] Add `loadPaperLibraryV3()` and `savePaperLibraryV3()` with a schema version and backward-compatible fallback.
- [ ] Cover migration, defaults, and round-trip persistence with Vitest.

### Task 2: Canonical translators and import planning

**Files:**
- Create: `frontend/src/modules/knowledge/paper/translators.ts`
- Modify: `frontend/src/modules/knowledge/paper/scholar.ts`
- Modify: `frontend/src/modules/knowledge/paper/pdfMeta.ts`
- Test: `frontend/src/modules/knowledge/paper/translators.test.ts`

- [ ] Define `CanonicalPaperDraft`, `ImportAttachmentDraft`, and `TranslatorResult`.
- [ ] Wrap DOI/arXiv search, BibTeX/RIS/CSV parsing, PDF metadata extraction, and generic URL metadata into named translators.
- [ ] Preserve incomplete drafts and attachment metadata when lookup fails.
- [ ] Add tests for successful translator output and failed PDF metadata output.

### Task 3: Duplicate detection, merge, and undo

**Files:**
- Create: `frontend/src/modules/knowledge/paper/merge.ts`
- Modify: `frontend/src/modules/knowledge/paper/model.ts`
- Test: `frontend/src/modules/knowledge/paper/merge.test.ts`

- [ ] Implement duplicate scoring using DOI/arXiv exact matches and title-author-year similarity.
- [ ] Implement `mergePapers()` that keeps a selected primary record and unions attachments, notes, annotations, collections, tags, and relations.
- [ ] Store a `MergeSnapshot` and implement `undoMerge()`.
- [ ] Test exact duplicates, probable duplicates, independent records, metadata precedence, and undo.

### Task 4: Citation and complete-library export

**Files:**
- Create: `frontend/src/modules/knowledge/paper/exportLibrary.ts`
- Modify: `frontend/src/modules/knowledge/paper/PaperLibrary.tsx`
- Test: `frontend/src/modules/knowledge/paper/exportLibrary.test.ts`

- [ ] Keep BibTeX/RIS/CSV citation exports and add CSL JSON export.
- [ ] Add JSON complete-library backup export and import with schema validation.
- [ ] Add separate export menu groups: “导出引用” and “备份完整文献库”.
- [ ] Test that complete backup includes attachments, notes, annotations, organization, and merge snapshots.

### Task 5: Structured import panel and Paper detail view

**Files:**
- Modify: `frontend/src/modules/knowledge/paper/PaperLibrary.tsx`
- Modify: `frontend/src/modules/knowledge/paper/PaperLibrary.scss`
- Create: `frontend/src/modules/knowledge/paper/PaperDetailDrawer.tsx`
- Create: `frontend/src/modules/knowledge/paper/PaperDetailDrawer.scss`

- [ ] Replace the flat import sheet with a primary upload zone, DOI/arXiv quick entry, and collapsed external-source section.
- [ ] Add an import review step showing canonical metadata, confidence, attachments, and duplicate suggestions before commit.
- [ ] Add a Paper detail drawer with Overview, Attachments, Notes, Annotations, and Relations tabs.
- [ ] Add attachment, note, tag, collection, and relation actions against V3 data.
- [ ] Keep the visual system quiet and editorial: clear heading hierarchy, compact metadata, accessible focus states, and responsive behavior.

### Task 6: Verification and preview validation

**Files:**
- Modify: `frontend/src/modules/knowledge/paper/PaperLibrary.test.tsx` if present
- Test: relevant model, translator, merge, export tests

- [ ] Run targeted Vitest tests for model, translators, merge, export, and PaperLibrary.
- [ ] Run TypeScript typecheck for the frontend.
- [ ] Open `/lib/knowledge/paper-preview`, verify migration, import panel, detail drawer, export menus, and responsive layout in the browser.
- [ ] Confirm `git diff` contains only intended implementation and documentation changes.
