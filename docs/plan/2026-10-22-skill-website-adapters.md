# Built-in skill website import adapters

Extend the existing temporary worktree and GitHub importer with built-in adapters for skills.sh, SkillsMP, ClawHub, and SkillHub. Support their public skill detail links, including shared query/fragment variants, through personal imports, admin publishing, and workflow imports. Preserve the user's original website URL as provenance and in Chat recovery.

The sourceurl package owns an explicit adapter registry. Download sites map validated page routes to their documented ZIP API; index sites resolve an authoritative GitHub directory and reuse directory-first retrieval, retaining binary assets and scripts. Never infer a SkillsMP GitHub path from a flattened page slug. Keep the existing size, path, symlink and ambiguity protections. Build/lock resolution remains pinned and separate from interactive website adapters.

- [x] Verify current website contracts against official sources and live public links.
- [x] Add URL adapters with route validation, bounds, cancellation, and regressions.
- [x] Integrate a single registry into personal, admin, and workflow import paths.
- [x] Preserve original source URL separately from the resolved GitHub source and canonical package name.
- [x] Add end-to-end fixtures and opt-in live import tests that create actual skills in temporary SQLite databases.
- [x] Update both UI languages to list supported websites; run Go regressions, frontend checks, and independent review.

The user explicitly delegated implementation choices and requested no further questions.

## Supported sources

| Website | Adapter behavior | Live full-import example |
| --- | --- | --- |
| skills.sh | Resolve the catalog's frontmatter name to its actual repository directory; use pinned Git smart-HTTP/codeload discovery when REST quota is exhausted. Preserve scripts and binary resources. | [React best practices](https://skills.sh/vercel-labs/agent-skills/vercel-react-best-practices) |
| SkillsMP | Read the exact GitHub source declared in JSON-LD; support legacy, canonical and localized detail routes. | [frontend-design](https://skillsmp.com/skills/anthropics-skills-skills-frontend-design-skill-md) |
| ClawHub | Map canonical/legacy detail routes to owner-qualified download API; support hosted ZIP and the pinned GitHub handoff descriptor. | [summarize](https://clawhub.ai/paudyyin/skills/summarize) |
| SkillHub | Map validated namespace/slug to package API; preserve explicit version/tag selectors while ignoring tracking and fragments. | [summarize](https://skillhub.cn/skills/clawhub_paudyyin/summarize) |

All four live examples were resolved, downloaded, validated and saved with `SkillService.CreateSkill` into temporary SQLite databases. Each persisted a canonical SKILL.md and retained the full original website URL, including the submitted query/fragment. The skills.sh React import succeeded during anonymous GitHub REST quota exhaustion. Additional live package checks covered find-skills and Anthropic pdf (including supporting scripts). Live tests are opt-in via `LAZYMIND_LIVE_SKILL_IMPORT_TESTS=1`; regular tests use bounded local fixtures. ClawHub's GitHub handoff is verified against official-contract fixtures, not a claimed live descriptor example.

## Source contracts

- [skills.sh official discovery implementation](https://github.com/vercel-labs/skills/blob/main/src/blob.ts).
- [Git smart HTTP protocol](https://git-scm.com/docs/http-protocol) and [reference advertisement](https://git-scm.com/docs/pack-protocol).
- [SkillsMP canonical URL documentation](https://skillsmp.com/llms.txt).
- [ClawHub download API](https://github.com/openclaw/clawhub/blob/main/docs/http-api.md) and [public GitHub handoff implementation](https://github.com/openclaw/clawhub/blob/main/convex/lib/githubHandoff.ts).

No new external runtime, connector, token or CLI installation is needed. Existing optional GitHub token support remains available. Archive discovery is bounded to 100 MiB per repository archive; folder selection and package validation retain the existing limits. Exact skill directories and public GitHub handoffs preserve immutable commits.

## Verification

- `go test ./skillv2/... ./cmd/builtin-skill-bundle`: passed.
- Skill-related workflow tests: passed.
- Source adapter / downloader race tests and `go vet`: passed.
- Personal and admin pipeline fixtures cover all four adapters and retained binary assets.
- Six frontend suites: 102 tests passed; `pnpm build`: passed.
- Independent review found no remaining actionable issues after fixes to ambiguous selection and ClawHub's exact-byte approval fingerprint. Fingerprints are checked before document-case normalization and metadata filtering. Descriptor and subsequent downloads share the existing 100 MiB transfer budget.
- The existing unrelated frontend TypeScript diagnostics and two workflow model-configuration failures recorded in the previous GitHub implementation plan remain outside this change.
