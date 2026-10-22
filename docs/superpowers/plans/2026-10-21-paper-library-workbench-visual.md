# Paper Library Workbench Visual Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refine the paper library into a polished research workbench without changing its import and persistence model.

**Architecture:** Keep state and data operations in `PaperLibrary.tsx` and `PaperDetailDrawer.tsx`. Add presentational derived counts and view filters locally, then reshape CSS around a shared restrained visual token system.

**Tech Stack:** React, TypeScript, Ant Design, SCSS, Vite.

## Global Constraints

- Preserve Paper-as-core-entity behaviour and localStorage V3 persistence.
- Do not add dependencies.
- Keep Chinese product copy concise and user-facing.
- Maintain keyboard focus styles and responsive layouts.

---

### Task 1: Add workbench hierarchy and smart views

**Files:**
- Modify: `frontend/src/modules/knowledge/paper/PaperLibrary.tsx`
- Modify: `frontend/src/modules/knowledge/paper/PaperLibrary.scss`

**Interfaces:**
- Consumes: existing `PaperItem`, `filterPapers`, `statusLabel`, and `data.papers`.
- Produces: a local `libraryView` selector that changes the displayed list without mutating collections.

- [ ] **Step 1: Add derived workbench counts**

```ts
const stats = {
  total: data.papers.length,
  unread: data.papers.filter((item) => item.status === 'unread').length,
  important: data.papers.filter((item) => item.important).length,
  incomplete: data.papers.filter((item) => item.incomplete || item.parse === 'failed').length,
};
```

- [ ] **Step 2: Add a smart-view filter before the existing search and sort pipeline**

```ts
const smartScoped = scoped.filter((item) => {
  if (libraryView === 'important') return item.important;
  if (libraryView === 'incomplete') return item.incomplete || item.parse === 'failed';
  return true;
});
```

- [ ] **Step 3: Render the header summary and smart-view buttons**

Use plain text labels: `文献库`, `待读`, `重要`, and `待补全`.

- [ ] **Step 4: Style the hierarchy**

Use an ink header, tight numeric summary, semantic rail dividers, and clear selected state.

- [ ] **Step 5: Build**

Run: `pnpm build` in `frontend/`.

### Task 2: Strengthen list and import information hierarchy

**Files:**
- Modify: `frontend/src/modules/knowledge/paper/PaperLibrary.tsx`
- Modify: `frontend/src/modules/knowledge/paper/PaperLibrary.scss`

**Interfaces:**
- Consumes: existing row actions and `sheet === 'import'` controls.
- Produces: list rows with unobtrusive document signals and an import sequence that explains recognition and duplicate checking.

- [ ] **Step 1: Add a compact material summary to each row**

Show whether the V3 record has attachments and notes without changing the paper model.

- [ ] **Step 2: Reorder visual emphasis**

Place status and star in a quiet action rail; let title and citation carry the primary reading order.

- [ ] **Step 3: Clarify import copy**

State the path as `识别元数据 → 检查重复 → 加入文献库` adjacent to the file import action.

- [ ] **Step 4: Style rows and import blocks**

Use editorial spacing, hairline separators, readable metadata, and one blue primary action.

- [ ] **Step 5: Build**

Run: `pnpm build` in `frontend/`.

### Task 3: Turn the detail drawer into a paper record

**Files:**
- Modify: `frontend/src/modules/knowledge/paper/PaperDetailDrawer.tsx`
- Modify: `frontend/src/modules/knowledge/paper/PaperDetailDrawer.scss`

**Interfaces:**
- Consumes: `PaperRecord`, `PaperLibraryDataV3`, `readingSummary`, and `createNote`.
- Produces: a drawer header showing reading state and metadata completeness, with existing tabs retained.

- [ ] **Step 1: Derive metadata completeness**

```ts
const presentMetadata = [current.title, current.authors, current.year, current.doi || current.arxiv, current.metadata.abstract]
  .filter(Boolean).length;
const completeness = Math.round((presentMetadata / 5) * 100);
```

- [ ] **Step 2: Add a record header and source details**

Render reading status, source identifiers, and completeness as concise record facts.

- [ ] **Step 3: Group overview content into metadata and reading material**

Keep all attachment, note, annotation, and relation data in their existing tabs.

- [ ] **Step 4: Style the drawer**

Use a restrained document-header layout, full-width summary rule, and compact note/attachment cells.

- [ ] **Step 5: Verify**

Run: `pnpm build` in `frontend/`, then inspect `/lib/knowledge/paper-preview`.
