# Paper Library Workbench Visual Design

## Goal

Make the paper library feel like a calm, professional research workbench while preserving the current Zotero-style paper, attachment, note, collection, and import behaviour.

## Visual direction

The interface uses paper white, deep ink, cool slate, and one restrained scholarly blue. Titles remain serif to signal reading material; controls and metadata use the existing sans-serif system. Avoid decorative gradients, repeated card containers, and oversized metrics.

## Layout

- The header becomes a compact workbench summary: library title, total count, current reading workload, and primary import/export actions.
- The navigation rail adds semantic smart views for important and incomplete records, separated from user collections.
- The list uses an editorial hierarchy: title first, bibliographic line second, metadata and available material third. Per-paper actions remain visible without competing with the title.
- The import drawer presents one primary route (file import), one compact identifier route (DOI/arXiv), and collapsible advanced sources.
- The paper drawer becomes a document record: source and reading state in the header, a compact health summary, then metadata, attachments, notes, annotations, and relations.

## Interaction and responsive rules

- Existing import, export, filter, edit, selection, and persistence handlers remain unchanged.
- Smart views filter in memory and do not create or remove collections.
- Keyboard focus remains visible and controls retain accessible labels.
- At tablet widths the rail joins the main column; drawers use the full available width on small screens.

## Validation

- Build with `pnpm build` from `frontend/`.
- Open the preview and inspect the header, smart views, list hierarchy, import drawer, and paper detail drawer.
