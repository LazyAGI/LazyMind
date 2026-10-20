# Knowledge management MCP implementation plan

Approved by the user on 2026-10-20. Spec: knowledge-management-mcp-spec.md; user override there is binding.

## Global constraints
- Inherit existing user account and resource permissions; no MCP-specific grant system.
- LazyMind confirms deletion and cross-library moves ONLY. External callers control interaction.
- Nine management tools; keep public get/search; native KBToolkit retains five read/search tools.
- Shared Core services own all transactions and rules; no handler wrapping, fabricated requests, or parallel business implementation.
- Four original sources; fixed content/version refs; AcquireRemoteOriginal separate from ImportOriginal.
- Work fixed-version sources require an existing published versioned artifact. They do not copy or implicitly upgrade legacy artifacts. Artifact V2 rollout defaults remain unchanged; isolated 8091 acceptance explicitly enables the existing flag.
- Bounded synchronous whole-root moves including empty folders; root atomicity, truthful processing/cleanup status.
- Active Core ownership fences all retrieval and stale task publication.
- Trusted runtime/lease identity; no user/model-supplied confirmation or credential fields.
- Stable idempotency keys, transactional resource receipts, no blind write replay.
- Preserve existing API defaults, CLI requiredTools, old pinned workflows, PPT and native KB group identity.
- PostgreSQL and SQLite migrations with up/down and existing aggregate updated.
- dya/ branch; do not change primary checkout or 8090. Isolated 8091 deployment only after backup.

## Tasks
1. P1 Contracts/data: schemas, existing action permissions, result/error types, scene/version/receipts.
2. P2 Shared reads and management: catalog, multi-library pagination, bounded aggregation, create/metadata/same-library moves, page reuse.
3. P3 Originals: controlled acquisition and independent binding, four sources, upload channel, academic reuse.
4. P4 Move/delete: tree budget, prepare/commit, root atomicity, visibility and stale task fencing, cleanup.
5. P5 Host: short-lived run credentials, default provider, restrictive tool loading, delete/cross-move confirmation, progress/results.
6. P6 Protocol/consumers: seven new MCP registrations, CLI uploads, native management migration, pinned workflow/PPT compatibility.
7. P7 Verification: calibrated budgets, dual DB migration tests, 8091 real E2E and uniform enablement only after gates pass.

## Review focus
- Cross-user/tenant/run/lease leakage; no permission expansion by internal identity.
- Empty allowlist must load nothing; structured errors survive model adaptation.
- Mutation conflict/races, partial results, lost response, stable replay returns actual IDs.
- Original bytes/version preserved; source bounds, redirect/DNS/path checks, multi-binding ownership.
- Deleted/moved source never resurfaces through indexes or old tasks; targets not reported ready prematurely.

## Acceptance
Run relevant Go/Python/frontend tests, race/failure injection for transactions, PostgreSQL+SQLite migrations, then 8091 login/chat/work/search/CLI E2E. Baseline failures must be distinguished. Do not equate unit tests or service health with E2E acceptance.
