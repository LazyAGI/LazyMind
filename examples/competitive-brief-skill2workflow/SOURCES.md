# Sources and provenance

## Skill

- Author/repository: [Anthropic knowledge-work-plugins](https://github.com/anthropics/knowledge-work-plugins).
- Source commit: `c75fda00ff0e41c8ee331ae312c8a07f1967653b` (resolved from main on 2026-10-27).
- [Original SKILL.md](https://github.com/anthropics/knowledge-work-plugins/blob/c75fda00ff0e41c8ee331ae312c8a07f1967653b/marketing/skills/competitive-brief/SKILL.md), Git blob `1a7df218996ed350c67e6899a60321f205bb69d6`, 13,150 bytes. The directory contains only this file.
- Its relative reference `../../CONNECTORS.md` resolves to [marketing/CONNECTORS.md](https://github.com/anthropics/knowledge-work-plugins/blob/c75fda00ff0e41c8ee331ae312c8a07f1967653b/marketing/CONNECTORS.md). The connectors are category substitutions, not mandatory subscriptions. Examples include SEO providers, marketing analytics and collaboration services.
- [Pinned LICENSE](https://github.com/anthropics/knowledge-work-plugins/blob/c75fda00ff0e41c8ee331ae312c8a07f1967653b/LICENSE) contains the Apache License 2.0 text and nonstandard trailing text. This case links to the exact file rather than silently normalizing or redistributing it. No upstream Skill body or license file is committed here. Attribution does not imply endorsement.

The original Skill is a research/synthesis instruction package. Inputs are competitor names, optional company positioning and focus areas. Its output is a seven-section competitive brief followed by optional follow-up choices. Public web access supports research; paid SEO, review and market data are supplementary and unavailable in this experiment. There are no executable scripts in the upstream Skill directory.

The imported LazyMind snapshot has two files: byte-identical `SKILL.md` and a platform-added `scripts/run.py` placeholder. The importer adds that default in `backend/core/skillv2/service/service.go`. It does **not** bring the out-of-directory `CONNECTORS.md` into the Skill package. The baseline reads that reference separately. This is a documented input-context difference, not a claim of identical packaging. The generated Workflow does not reuse the placeholder script.

Baseline 1's local Skill copy had one additional trailing newline when read; later runs use the exact upstream blob. The instruction text is otherwise identical. The original bytes and imported text hashes are recorded in the manifest.

## Research corpus

Seven official pages were fetched without authentication on 2026-10-27. See [acquisition records](evidence/acquisition.json) for HTTP status, exact UTC acquisition times, byte counts and SHA-256 of the raw HTML. The files themselves stay outside Git; current live bytes may differ.

| Card | Official page | Evidence covered |
| --- | --- | --- |
| S1 | [Linear pricing](https://linear.app/pricing) | Plans, annual seat prices and selected entitlements |
| S2 | [Linear features](https://linear.app/features) | Vendor positioning and feature emphasis |
| S3 | [Linear Now](https://linear.app/now) | Sampled content themes and dated announcements |
| S4 | [Asana pricing](https://asana.com/pricing) | Plans, annual seat prices and selected entitlements |
| S5 | [Asana product](https://asana.com/product) | Vendor positioning and audience emphasis |
| S6 | [Asana resources](https://asana.com/resources) | Sampled themes and content formats |
| S7 | [Asana releases](https://asana.com/whats-new) | Q3 2026 release messaging |

Both execution modes receive the same [paraphrased fact cards](evidence/source-cards.md). They are **offline replay inputs selected by the experiment operator**, not complete snapshots or independent validation of the vendors' claims. Evidence acquisition is separate from model execution. This controls source variation but does not test live retrieval quality. No paid database, customer account, internal sales record or private document was used.

## LazyMind and prior work

- Audited main: `71a0e5a829f1ef8d9a5d9e9ce720386ba3c69d0e`.
- [External Agent trial](../../docs/external-agent-skill-workflow-trial.md) and [Skill-to-Workflow authoring contract](../../skills/workflow-agent-kit/references/skill-to-workflow.md).
- [PR #709](https://github.com/LazyAGI/LazyMind/pull/709): merged conversion/preflight/recovery work; not evidence that this Skill works.
- [PR #718](https://github.com/LazyAGI/LazyMind/pull/718): authoring/API parity and deterministic finalization.
- [PR #734](https://github.com/LazyAGI/LazyMind/pull/734): hosted external task orchestration.
- [PR #852](https://github.com/LazyAGI/LazyMind/pull/852): merged Superpowers dispatch reliability example. Its native Brief gate and advanced CLI experiment are separate from this automatically converted knowledge-work Skill.
- [Product Solution Delivery provenance](../../workflows/product_solution_delivery/SOURCE.md): an existing manually adapted built-in package that cites competitive-brief as a design reference. It was neither run nor counted as this conversion.

A repository/PR search for competitive/Skill2Workflow found no equivalent published case at the start of this trial. This case adds actual source-URL compatibility, dependency/preflight, generated-package, fixed-corpus baseline and hosted-execution observations.
