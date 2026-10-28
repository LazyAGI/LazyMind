# Supplier navigation and information polish

Approved in chat on 2026-10-26.

Goal: make supplier ordering freely editable, promote newly verified suppliers with motion, and simplify supplier information without coupling published API protocols to runtime adapters.

Architecture: retain per-user/tenant sidebar preferences; keep immutable service identity and independent protocol declarations. All runtime validation continues using existing adapters.

## Implementation

- [x] Ordering: test initial SenseNova priority, unrestricted saved order and filtered drag; remove permanent sections.
- [x] Preferences: test scoped hide/restore and move-to-front persistence. Promote only explicit successful verification events, never initial data loading; ignore events from another account.
- [x] Interaction: drag-handle click opens hide/restore menu; pointer threshold preserves vertical dragging. Animate committed row positions and respect reduced motion.
- [x] Details: keep service-switch scroll position, replace editing label with accessible check, remove redundant title and source-date text.
- [x] Protocols: independently declare each built-in service's published API formats with official references and coverage; show separately from runtime support.
- [x] Logo/actions: preserve uploaded blue-purple cube design in the existing SVG asset, remove redundant model overflow button while retaining the API settings entry.
- [x] Verification: targeted behavior tests, scoped ESLint, typecheck, model-settings suite and documentation checks; review integrated changes before pushing.

Constraints: preserve unrelated output/ files and LazyLLM submodule; no live keys or paid upstream calls. Hiding changes sidebar visibility only and must remain reversible. Brand grouping and service configuration stay intact.

Verification: 87 model-settings files / 1153 tests passed; Go modelprovider/common and OpenAPI checks passed; TypeScript scoped check, ESLint, documentation checks, generated API freshness, production build passed. Isolated browser preview verified promotion, hide/restore, service-reading scroll preservation, and desktop/mobile layouts without live credentials.
