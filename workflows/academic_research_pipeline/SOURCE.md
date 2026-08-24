# Source and adaptation record

This built-in Workflow is a modified adaptation of the following upstream work:

- Upstream project: [Academic Research Skills](https://github.com/Imbad0202/academic-research-skills)
- Primary orchestrator: [`academic-pipeline/SKILL.md` at v3.21.0](https://github.com/Imbad0202/academic-research-skills/blob/v3.21.0/academic-pipeline/SKILL.md)
- Source release: [`v3.21.0` (2026-08-18)](https://github.com/Imbad0202/academic-research-skills/releases/tag/v3.21.0), commit `2b639c1`
- Upstream copyright: Copyright (c) 2026 Cheng-I Wu
- Upstream license: [Creative Commons Attribution-NonCommercial 4.0 International (CC BY-NC 4.0)](https://github.com/Imbad0202/academic-research-skills/blob/v3.21.0/LICENSE)
- Dependent contracts reviewed:
  - [`deep-research`](https://github.com/Imbad0202/academic-research-skills/tree/v3.21.0/deep-research) v2.12.1
  - [`academic-paper`](https://github.com/Imbad0202/academic-research-skills/tree/v3.21.0/academic-paper) v3.3.1
  - [`academic-paper-reviewer`](https://github.com/Imbad0202/academic-research-skills/tree/v3.21.0/academic-paper-reviewer) v1.11.1

This adaptation changes the upstream execution model, prompts, state transitions, runtime
interfaces, retrieval integration, Writer integration, validation, and document export so that it
runs as a LazyMind built-in Workflow. It does not read or execute an upstream checkout at runtime.

The upstream license requires attribution, a link to the license, and an indication that changes
were made. It also limits licensed use and distribution to non-commercial purposes. This notice
does not remove that restriction; commercial use of adapted upstream material requires separate
permission from the upstream rights holder or another applicable legal basis.

## Stage mapping

| Source contract | LazyMind Workflow stage |
|---|---|
| Stage 1 RESEARCH / deep-research scoping | `formulate_research` |
| deep-research investigation | `retrieve_literature` |
| deep-research verification and synthesis | `synthesize_evidence` |
| academic-paper architecture | `build_paper_outline` (human approval) |
| academic-paper drafting | `write_paper_draft` (human approval) |
| Stage 2.5 INTEGRITY | `pre_review_integrity` (mandatory human checkpoint) |
| Stage 3 REVIEW | `peer_review` (five perspective reports) |
| Stage 4 REVISE | `revise_paper` (human approval) |
| Stage 3' RE-REVIEW | `re_review` |
| Stage 4' RE-REVISE | `second_revision` (Major only, hard cap) |
| Stage 4.5 FINAL INTEGRITY | `final_integrity` (mandatory human checkpoint) |
| Stage 5 FINALIZE | `finalize_paper` (Markdown/DOCX) |
| Stage 6 PROCESS SUMMARY | `process_summary` |

## LazyMind-native substitutions

- Research retrieval uses LazyMind `academic_search`; provider selection remains generic and can resolve to Sciverse or another available academic provider.
- Selected knowledge bases use LazyMind `kb` with inherited runtime filters.
- Outline, drafting, revisions and selection rewrites use LazyMind Writer Toolkit.
- Workflow-local Python handles only academic outline validation, registered-evidence checks and MD/DOCX export.
- User confirmation is represented by Workflow `human` steps rather than Claude-specific checkpoint hooks.
- The intake accepts generic `research paper` as a paper type and GB/T 7714 as a
  Chinese-local compatibility extension; the source Skill's APA/Chicago/MLA/IEEE/Vancouver
  choices remain supported.

## Deliberately unsupported or bounded features

The Workflow does not claim to implement Claude hook guards, Material Passport/reset boundaries, proprietary plagiarism detection, full-text DOI verification when a provider returns metadata only, institutional ethics authorization, cross-model reviewer calibration, PRISMA/meta-analysis execution, Pandoc/LaTeX/tectonic PDF output, or the source package's private deterministic schema/checker suite. Missing capabilities remain `UNKNOWN`, `NOT_CHECKED`, warnings, or explicit retrieval limits rather than fabricated PASS states.

The source package's optional visualization branch is not enabled automatically: this
adaptation has no verified research dataset or chart specification from which it could safely
produce academic figures. Authors can add reviewed figures to the editable manuscript without
changing the registered-evidence and integrity contracts.
