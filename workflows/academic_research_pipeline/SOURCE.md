# Source and adaptation record

This built-in Workflow is adapted from the local Academic Research Skills package:

- Primary orchestrator: `/home/sensetime/sunxiaoye/safe_home/Docker/LazyMind/tmp/Paper/academic-research-skills/academic-pipeline/SKILL.md`
- Source name/version: `academic-pipeline` v3.21.0 (2026-08-18)
- Dependent contracts reviewed:
  - `deep-research` v2.12.1
  - `academic-paper` v3.3.1
  - `academic-paper-reviewer` v1.11.1

The source path is documentation only. Runtime code never reads or executes files from that directory.

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
