# Source and adaptation record

This built-in Workflow is adapted from an internally assembled `product-solution-delivery` Skill
package. No private filesystem location is part of the Workflow package or runtime contract.

- Source release: `psd-2026-08-11-portable-lazymind-competitive-analysis-v1`
- Parent contract: `SKILL.md`
- Atomic child contracts:
  - `shape-product-direction`
  - `analyze-competitors`
  - `product-design-full-cycle`
  - `write-prd`
  - `build-product-prototype`
  - `review-product-artifact`
  - `prepare-development-handoff`

The Workflow-local business contracts in `scripts/tools.py` were produced by the internal
LazyMind adaptation and are loaded deterministically at runtime. Runtime code does not read or
execute the original Skill package.

## Open-source design references

The internal Skill and this Workflow do **not** copy the following Skills verbatim. They were
consulted as design references for competitive research scope, evidence collection, competitor
discovery, positioning, profiling, and consulting-analysis structure:

| Referenced Skill | Upstream repository license |
|---|---|
| [Anthropic `competitive-brief`](https://github.com/anthropics/knowledge-work-plugins/blob/main/marketing/skills/competitive-brief/SKILL.md) | [Apache License 2.0](https://github.com/anthropics/knowledge-work-plugins/blob/main/LICENSE) |
| [Bright Data `competitive-intel`](https://github.com/brightdata/skills/blob/main/skills/competitive-intel/SKILL.md) | [MIT License](https://github.com/brightdata/skills/blob/main/LICENSE) |
| [Anysite `competitor-discovery`](https://github.com/anysiteio/agent-skills/blob/main/skills/competitor-discovery/SKILL.md) | [MIT License](https://github.com/anysiteio/agent-skills/blob/main/LICENSE) |
| [Anysite `positioning-map`](https://github.com/anysiteio/agent-skills/blob/main/skills/positioning-map/SKILL.md) | [MIT License](https://github.com/anysiteio/agent-skills/blob/main/LICENSE) |
| [Corey Haines `competitor-profiling`](https://github.com/coreyhaines31/marketingskills/blob/main/skills/competitor-profiling/SKILL.md) | [MIT License](https://github.com/coreyhaines31/marketingskills/blob/main/LICENSE) |
| [ByteDance DeerFlow `consulting-analysis`](https://github.com/bytedance/deer-flow/blob/main/skills/public/consulting-analysis/SKILL.md) | [MIT License](https://github.com/bytedance/deer-flow/blob/main/LICENSE) |

The license labels above describe the respective upstream repositories; they do not change the
license of this repository or imply endorsement by the upstream authors.

## Source-to-Workflow mapping

| Original concept | Built-in Workflow implementation |
|---|---|
| Parent Router and explicit-stage priority | `route_product_stage` with native `route: choice` / `control.next_step` |
| Single stage or explicitly authorized finite chain | one selected stage per Session; a stage chain only guides recommendations |
| Product Workspace / Artifact Manifest | host revisions plus internal `workspace_state` / `stage_manifest`, carried through `workspace_seed` |
| Evidence only when it changes decisions | selected-stage evidence and the design light/heavy branch |
| `shape-product-direction` | text path with artifact type `direction-brief` |
| `analyze-competitors` | `analyze_competitive_position` |
| `product-design-full-cycle` | `route_design_scope`: six domains and per-decision light/heavy effort |
| `write-prd` | text path with artifact type `prd` |
| `build-product-prototype` | `build_interactive_prototype` |
| `review-product-artifact` | text path with artifact type `review-report` |
| `prepare-development-handoff` | text path with artifact type `development-handoff` |
| Stage HITL and explicit continuation | explicit, idempotent stage relay; each new Session retains the same project and conversation |

## LazyMind-native substitutions

- Five text-producing stages share LazyMind Writer for resource profiling, editable Markdown
  outlines, section planning, streaming/checkpointed drafting, revisions and selection rewrite.
- Selected knowledge bases use LazyMind `kb` with inherited runtime filters.
- Current public product evidence uses LazyMind `web_search` and `url_fetch`; provider selection and
  credentials remain generic runtime configuration.
- All seven stages publish synchronized HTML and Markdown for one logical artifact version.
  Existing outlines remain authoritative; visualizations cannot invent evidence or alter structure.
- Finalization pins body, companion view, assessment, Manifest and Workspace in one transaction.
  Failed or partial attempts cannot replace a previous complete publication; edits freeze new revisions.
- Returning stages revise their own baseline alongside selected upstream versions and current requests.
  Readiness, content versions and sourced decision acceptance remain separate; deferred decisions persist.
- Both Routers use upstream branches. Scope-preserving evidence escalation cannot remove hard gates.
  Native Writer, shared conversation files, immutable input bindings and upstream control sessions are reused.
- New package capabilities enable product execution limits and publication; old revisions retain upstream
  behavior. Start, design outline and delivery summary remain internal; historical artifact lists are hidden.
- Upstream embedded resources remain unchanged, including internal runtime tracing. Every child
  also loads `contracts/rich-text-presentation.md` from its pinned package for dual-format output.
  Text stages share native YAML prompt anchors and resolve their stage from the bound execution plan;
  no generic runtime template engine or source-package patch layer is needed.

## Deliberate boundaries

The Workflow does not claim logged-in product operation, paid-source access, browser visual QA,
production frontend implementation, technical architecture, engineering estimates, release dates,
or organizational approval. Missing evidence remains a gap. Runtime trace stays internal. Only the
selected stage runs; continuation or switching requires user action at a completed-stage boundary.
